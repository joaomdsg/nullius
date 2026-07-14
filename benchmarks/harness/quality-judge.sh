#!/usr/bin/env bash
# Blind SWE-quality judge — nullius benchmarks (see harness/README.md).
#
# The caveat this closes: score.sh measures pass/fail only; two diffs that both
# pass can differ hugely in engineering quality. This judge scores a single
# rep's DIFF against the task prompt — with NO arm label and NO run report
# (reports are arm-identifying: the nullius report format alone would unblind
# the judge) — on root-causing, tests, minimality, and idiom.
#
#   quality-judge.sh <task-dir> <diff-file>
#
# Env: QUALITY_JUDGE_MODEL (default claude-sonnet-5) picks the judge tier.
# Auth resolution mirrors judge.sh: host `claude` login, or ANTHROPIC_API_KEY/
# NULLIUS_ANTHROPIC_API_KEY (legacy BYPROXY_ honored) for API billing, or
# CLAUDE_CODE_OAUTH_TOKEN/NULLIUS_CLAUDE_CODE_OAUTH_TOKEN[_FILE] for
# subscription auth; an OAuth token in the API-key slot is routed over.
#
# Emits one JSON object on stdout:
#   {quality_model, scores:{root_cause,tests,minimality,idiom,overall}, rationale}
# Scores are 0-10. On any failure it still emits JSON (with .error) so a
# caller never loses a row.
set -uo pipefail

TASK_DIR="${1:?usage: quality-judge.sh <task-dir> <diff-file>}"
DIFF="${2:?}"
QUALITY_JUDGE_MODEL="${QUALITY_JUDGE_MODEL:-claude-sonnet-5}"
: "${ANTHROPIC_API_KEY:=${NULLIUS_ANTHROPIC_API_KEY:-${BYPROXY_ANTHROPIC_API_KEY:-}}}"
: "${CLAUDE_CODE_OAUTH_TOKEN:=${NULLIUS_CLAUDE_CODE_OAUTH_TOKEN:-}}"
if [[ -z "$CLAUDE_CODE_OAUTH_TOKEN" && -r "${NULLIUS_CLAUDE_CODE_OAUTH_TOKEN_FILE:-}" ]]; then
  CLAUDE_CODE_OAUTH_TOKEN="$(< "$NULLIUS_CLAUDE_CODE_OAUTH_TOKEN_FILE")"
fi
if [[ "$ANTHROPIC_API_KEY" == sk-ant-oat01-* ]]; then
  CLAUDE_CODE_OAUTH_TOKEN="${CLAUDE_CODE_OAUTH_TOKEN:-$ANTHROPIC_API_KEY}"
  ANTHROPIC_API_KEY=""
fi
# Export only non-empty credentials: an exported-but-empty var may shadow the
# host `claude` login in the CLI's env-first credential precedence.
if [[ -n "$ANTHROPIC_API_KEY" ]]; then export ANTHROPIC_API_KEY; else unset ANTHROPIC_API_KEY; fi
if [[ -n "$CLAUDE_CODE_OAUTH_TOKEN" ]]; then export CLAUDE_CODE_OAUTH_TOKEN; else unset CLAUDE_CODE_OAUTH_TOKEN; fi

emit_err() { jq -n --arg m "$QUALITY_JUDGE_MODEL" --arg e "$1" --arg raw "${2:-}" \
  '{quality_model:$m, error:$e, raw:$raw}'; exit 0; }
command -v jq >/dev/null     || { echo '{"error":"jq missing"}'; exit 0; }
command -v claude >/dev/null  || emit_err "claude CLI missing"
[ -f "$TASK_DIR/prompt.md" ] || emit_err "task prompt missing: $TASK_DIR/prompt.md"
[ -s "$DIFF" ] || emit_err "diff missing or empty: $DIFF"

TASK_TXT="$(cat "$TASK_DIR/prompt.md")"
# Cap the diff so a huge diff can't blow the judge's context; note if truncated.
DIFF_TXT="$(head -3000 "$DIFF")"
DIFF_LINES="$(wc -l < "$DIFF")"
[ "${DIFF_LINES:-0}" -gt 3000 ] && DIFF_TXT="$DIFF_TXT
[... diff truncated at 3000 lines for the judge ...]"

read -r -d '' PROMPT <<EOF || true
You are a blind software-engineering quality judge. You are given a coding
TASK and the GIT DIFF one anonymous run produced for it. You do not know who
or what produced the diff, and you must not guess or reward verbosity.

Score the diff 0-10 on each dimension (10 = exemplary senior engineering):
- root_cause: fixes address the underlying mechanism, not symptoms; no
  band-aids (sleeps as synchronization, broadened locks that mask the bug,
  swallowed errors).
- tests: added/changed tests pin the behavior that was fixed and would fail
  if the fix regressed; concurrency claims are tested in a race-detectable
  way; no vacuous or tautological tests.
- minimality: the diff is focused; no collateral rewrites, dead code, stray
  formatting churn, or scope creep beyond the task.
- idiom: idiomatic for the language and codebase; clear naming; lock/channel
  discipline is locally evident; comments state constraints, not narration.
- overall: holistic SWE excellence, NOT an average — a diff with one
  disqualifying flaw scores low overall even if other dimensions are high.

Return ONLY a JSON object, no prose, no code fences, of this exact shape:
{"scores":{"root_cause":N,"tests":N,"minimality":N,"idiom":N,"overall":N},"rationale":"<2-3 sentences, the decisive observations>"}

=== TASK ===
$TASK_TXT

=== GIT DIFF (may be truncated) ===
$DIFF_TXT
EOF

RAW="$(printf '%s' "$PROMPT" | claude -p --model "$QUALITY_JUDGE_MODEL" --output-format json 2>/dev/null)"
RESULT="$(jq -r '.result // ""' <<<"$RAW" 2>/dev/null || true)"
[ -n "$RESULT" ] || emit_err "judge produced no result" "$RAW"

# Parse: try the result as-is, then carve the outermost {...} (strips fences /
# stray prose the model may add despite instructions).
parse_scores() { jq -c 'select(.scores != null) | {scores, rationale}' 2>/dev/null; }
OBJ="$(printf '%s' "$RESULT" | parse_scores)"
if [ -z "$OBJ" ]; then
  CARVED="$(printf '%s' "$RESULT" | sed -n '/{/,$p' | sed -e 's/```//g')"
  OBJ="$(printf '%s' "$CARVED" | parse_scores)"
fi
[ -z "$OBJ" ] && emit_err "judge did not return parseable JSON" "$RESULT"

jq -n --arg m "$QUALITY_JUDGE_MODEL" --argjson o "$OBJ" \
  '{quality_model:$m} + $o'
