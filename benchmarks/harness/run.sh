#!/usr/bin/env bash
# byproxy benchmark harness — headless, unbiased, reproducible.
#
#   ./run.sh <task-dir> <arm: byproxy|solo> [--reps N] [--keep]
#
# Each rep: fresh worktree from the task's pinned REF → one headless
# `claude -p` run → score.sh replays DONE-WHEN independently (never trust
# the run's self-report) → one JSONL row with measured cost.
#
# Arm model pins:
#   byproxy  orchestrator = claude-fable-5 @ low effort (cheap control plane;
#            explorers/builders get their tiers from the agent definitions)
#   solo     claude-opus-4-8 (the "just let the expensive model read the
#            files" baseline; override with SOLO_MODEL=)
set -euo pipefail

HARNESS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BYPROXY_ROOT="$(cd "$HARNESS_DIR/../.." && pwd)"

TASK_DIR="$(cd "$1" && pwd)"; ARM="$2"; shift 2
REPS=1; KEEP=0
while [[ $# -gt 0 ]]; do case "$1" in
  --reps) REPS="$2"; shift 2;;
  --keep) KEEP=1; shift;;
  *) echo "unknown flag $1" >&2; exit 2;;
esac; done

# task metadata: REPO (path), REF (commit), TIMEOUT_S (optional)
source "$TASK_DIR/meta.env"
TIMEOUT_S="${TIMEOUT_S:-3600}"
PROMPT="$(cat "$TASK_DIR/prompt.md")"
TASK_NAME="$(basename "$TASK_DIR")"

# Arm variants via env: ORCH_MODEL/ORCH_EFFORT pin the byproxy control
# plane; SOLO_MODEL pins the solo arm. LABEL names the variant in results
# (defaults to arm+model so ablations stay distinguishable).
ORCH_MODEL="${ORCH_MODEL:-claude-fable-5}"
ORCH_EFFORT="${ORCH_EFFORT:-low}"
SOLO_MODEL="${SOLO_MODEL:-claude-opus-4-8}"
if [[ "$ARM" == "byproxy" ]]; then
  LABEL="${LABEL:-byproxy-${ORCH_MODEL#claude-}-$ORCH_EFFORT}"
else
  LABEL="${LABEL:-solo-${SOLO_MODEL#claude-}}"
fi

RESULTS_DIR="$HARNESS_DIR/results"; mkdir -p "$RESULTS_DIR"
JSONL="$RESULTS_DIR/results.jsonl"

for rep in $(seq 1 "$REPS"); do
  STAMP="$(date +%Y%m%dT%H%M%S)"
  WT="$(mktemp -d)/wt"
  git -C "$REPO" worktree add "$WT" "$REF" >/dev/null 2>&1

  cleanup() {
    if [[ "$KEEP" -eq 0 ]]; then
      git -C "$REPO" worktree remove --force "$WT" >/dev/null 2>&1 || true
    else
      echo "kept worktree: $WT" >&2
    fi
  }
  trap cleanup EXIT

  # auto mode: the permission classifier runs headless and gates anything
  # outside the allowlist; no blanket bypass. The allowlist covers the
  # routine loop (file tools, go toolchain, git reads, subagent dispatch)
  # so reps never stall on a prompt.
  CLAUDE_ARGS=(-p --output-format json --permission-mode auto
    --allowedTools "Read Edit Write Grep Glob Agent Task
      Bash(go build*) Bash(go test*) Bash(go vet*) Bash(gofmt*)
      Bash(git diff*) Bash(git status*) Bash(git log*) Bash(git show*)
      Bash(ls*) Bash(cat*) Bash(grep*) Bash(rg*) Bash(find*) Bash(wc*)")
  if [[ "$ARM" == "byproxy" ]]; then
    # wire the skill + agents into the worktree so headless picks them up
    mkdir -p "$WT/.claude"
    cp -r "$BYPROXY_ROOT/.claude/skills" "$WT/.claude/skills"
    cp -r "$BYPROXY_ROOT/.claude/agents" "$WT/.claude/agents"
    CLAUDE_ARGS+=(--model "$ORCH_MODEL" --effort "$ORCH_EFFORT")
    RUN_PROMPT="Use the byproxy skill (invoke it now) to complete this task. Follow it strictly — you are the orchestrator; never read project files or run project commands yourself.

$PROMPT"
  elif [[ "$ARM" == "solo" ]]; then
    CLAUDE_ARGS+=(--model "$SOLO_MODEL")
    RUN_PROMPT="$PROMPT"
  else
    echo "arm must be byproxy|solo" >&2; exit 2
  fi

  echo "[$TASK_NAME/$ARM rep $rep] running headless (timeout ${TIMEOUT_S}s)..." >&2
  RAW="$RESULTS_DIR/$TASK_NAME-$LABEL-$STAMP-rep$rep.json"
  T0=$SECONDS
  set +e
  (cd "$WT" && timeout "$TIMEOUT_S" claude "${CLAUDE_ARGS[@]}" "$RUN_PROMPT") > "$RAW" 2>"$RAW.stderr"
  RC=$?
  set -e
  WALL=$((SECONDS - T0))

  echo "[$TASK_NAME/$ARM rep $rep] scoring independently..." >&2
  # A scoring failure must never lose a paid run: default to an error
  # marker, keep the diff for post-mortem, and always emit the row.
  SCORE="$("$HARNESS_DIR/score.sh" "$WT" "$TASK_DIR/tests.txt" 2>"$RAW.score-err")" \
    || SCORE='{"error":"score.sh failed — see .score-err","complete":false}'
  jq -e . >/dev/null 2>&1 <<<"$SCORE" \
    || SCORE='{"error":"score.sh emitted non-JSON — see .score-err","complete":false}'
  git -C "$WT" diff > "$RAW.diff" 2>/dev/null || true
  DIFFSTAT="$(git -C "$WT" diff --stat 2>/dev/null | tail -1 || true)"
  UNTRACKED="$(git -C "$WT" ls-files --others --exclude-standard 2>/dev/null | grep -v '^\.claude/' | grep -c . || true)"

  COST="$(jq -r '.total_cost_usd // "null"' "$RAW" 2>/dev/null || echo null)"
  USAGE="$(jq -c '.usage // null' "$RAW" 2>/dev/null || echo null)"
  TURNS="$(jq -r '.num_turns // "null"' "$RAW" 2>/dev/null || echo null)"

  jq -n -c \
    --arg task "$TASK_NAME" --arg arm "$LABEL" --arg stamp "$STAMP" \
    --argjson rep "$rep" --argjson rc "$RC" --argjson wall "$WALL" \
    --argjson cost "$COST" --argjson usage "$USAGE" --argjson turns "$TURNS" \
    --argjson score "$SCORE" \
    --arg diffstat "$DIFFSTAT" --argjson untracked "$UNTRACKED" \
    --arg raw "$(basename "$RAW")" \
    '{task:$task, arm:$arm, rep:$rep, stamp:$stamp, exit_code:$rc,
      wall_s:$wall, cost_usd:$cost, usage:$usage, num_turns:$turns,
      score:$score, diffstat:$diffstat, new_files:$untracked, raw:$raw}' \
    | tee -a "$JSONL"

  cleanup; trap - EXIT
done
