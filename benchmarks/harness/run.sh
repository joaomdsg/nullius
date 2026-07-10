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
#   byproxy  guarded builder = ORCH_MODEL (defaults below; explorers get
#            their tier from the agent definition)
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
# plane; PLAIN_MODEL/PLAIN_EFFORT pin the plain arm (plain claude, no guard
# layer). LABEL names the variant in results (defaults to arm+model so
# ablations stay distinguishable). The legacy SOLO_MODEL/SOLO_EFFORT names
# are still honored so older reproduce commands keep working.
ORCH_MODEL="${ORCH_MODEL:-claude-fable-5}"
ORCH_EFFORT="${ORCH_EFFORT:-low}"
PLAIN_MODEL="${PLAIN_MODEL:-${SOLO_MODEL:-claude-opus-4-8}}"
PLAIN_EFFORT="${PLAIN_EFFORT:-${SOLO_EFFORT:-}}"   # empty = harness default effort
if [[ "$ARM" == "byproxy" ]]; then
  LABEL="${LABEL:-byproxy-${ORCH_MODEL#claude-}-$ORCH_EFFORT}"
else
  LABEL="${LABEL:-plain-${PLAIN_MODEL#claude-}${PLAIN_EFFORT:+-$PLAIN_EFFORT}}"
fi

RESULTS_DIR="$HARNESS_DIR/results"; mkdir -p "$RESULTS_DIR"
JSONL="$RESULTS_DIR/results.jsonl"

for rep in $(seq 1 "$REPS"); do
  STAMP="$(date +%Y%m%dT%H%M%S)"
  WTPARENT="$(mktemp -d)"
  WT="$WTPARENT/wt"

  # Two seeding modes. A git task pins an external repo@commit and checks it
  # out into a worktree. A SEED_DIR task (self-contained skeleton shipped in
  # the task dir) is seeded by COPYING that dir; we git-init the copy so the
  # scorer's `git diff` still captures the agent's changes. HIDDEN_DIR /
  # DEFECTS live in the task dir and are deliberately NOT copied — the rep
  # never sees the hidden suite or the ground truth.
  if [[ -n "${SEED_DIR:-}" ]]; then
    cp -r "$TASK_DIR/$SEED_DIR" "$WT"
    ( cd "$WT" && git init -q && git add -A && git -c user.email=b@b -c user.name=b commit -qm seed )
  else
    git -C "$REPO" worktree add "$WT" "$REF" >/dev/null 2>&1
  fi

  cleanup() {
    if [[ "$KEEP" -eq 0 ]]; then
      if [[ -z "${SEED_DIR:-}" ]]; then
        git -C "$REPO" worktree remove --force "$WT" >/dev/null 2>&1 || true
      fi
      rm -rf "$WTPARENT" >/dev/null 2>&1 || true
    else
      echo "kept worktree: $WT" >&2
    fi
  }
  trap cleanup EXIT

  # auto mode: the permission classifier runs headless and gates anything
  # outside the allowlist; no blanket bypass. The allowlist covers the
  # routine loop (file tools, go toolchain, git reads, subagent dispatch)
  # so reps never stall on a prompt.
  # stream-json (+ --verbose, required with -p) so the FULL event log is
  # captured — the plain json summary omits subagent tool calls, making the
  # byproxy treatment (explorer dispatch) unobservable. We parse the final
  # result event for the summary and count Agent/Task dispatches from the stream.
  # Both arms get the same allowlist, including native subagent dispatch
  # (Agent/Task). "plain" does NOT mean "no subagents" — it means plain
  # Claude Code with no byproxy skills/agents and no global config: the arms
  # differ only by the guard layer (project .claude/skills + agents + the
  # forced methodology prompt), not by tool access.
  CLAUDE_ARGS=(-p --output-format stream-json --verbose --permission-mode auto
    --allowedTools "Read Edit Write Grep Glob Agent Task SendMessage
      Bash(go build*) Bash(go test*) Bash(go vet*) Bash(gofmt*)
      Bash(git diff*) Bash(git status*) Bash(git log*) Bash(git show*)
      Bash(ls*) Bash(cat*) Bash(grep*) Bash(rg*) Bash(find*) Bash(wc*)")
  if [[ "$ARM" == "byproxy" ]]; then
    # wire the skill + agents into the worktree so headless picks them up
    mkdir -p "$WT/.claude"
    cp -r "$BYPROXY_ROOT/.claude/skills" "$WT/.claude/skills"
    cp -r "$BYPROXY_ROOT/.claude/agents" "$WT/.claude/agents"
    CLAUDE_ARGS+=(--model "$ORCH_MODEL" --effort "$ORCH_EFFORT")
    # Force the guard layer unconditionally. Asking the model to "invoke the
    # skill" is not enough headless — strong models decline it on a soloable
    # task (measured: 2/3 byproxy arms ignored it and ran solo). Inject the
    # skill body (minus YAML frontmatter) as an appended system prompt so the
    # guard workflow governs the whole run whether or not the Skill tool fires.
    SKILL_BODY="$(awk 'f{print} /^---[[:space:]]*$/{c++; if(c==2) f=1}' "$BYPROXY_ROOT/.claude/skills/byproxy/SKILL.md")"
    CLAUDE_ARGS+=(--append-system-prompt "You are operating under the byproxy v4 methodology. This is not optional and overrides any instinct to complete the task solo. You MUST run its full workflow with real subagent dispatches via the Agent tool: byproxy-explorer recon + surgical read, a byproxy-critic red-team of your contract, the compiled gate, the build (byproxy-builder for delegable volume; explorer reruns of every exit check), and a cold byproxy-auditor pass with an explorer fact-pack — you never audit your own diff. This is a headless run with NO USER available: at ESCALATE, use the self-answer fallback (author the question batch, answer each with your best-judgment recommendation, record all in ASSUMED as self-answered). Never call AskUserQuestion. Do not end your turn until the audit has run and you have reported STATUS/FACTS/RISKS/UNKNOWN/ASSUMED. The methodology:

$SKILL_BODY")
    RUN_PROMPT="Complete this task under the byproxy v4 methodology in your system prompt: explorer recon, surgical read, contract, critic red-team, gate, guarded build, cold auditor pass — before you finish.

$PROMPT"
  elif [[ "$ARM" == "plain" ]]; then
    CLAUDE_ARGS+=(--model "$PLAIN_MODEL")
    [[ -n "$PLAIN_EFFORT" ]] && CLAUDE_ARGS+=(--effort "$PLAIN_EFFORT")
    RUN_PROMPT="$PROMPT"
  else
    echo "arm must be byproxy|plain" >&2; exit 2
  fi

  echo "[$TASK_NAME/$ARM rep $rep] running headless (timeout ${TIMEOUT_S}s)..." >&2
  RAW="$RESULTS_DIR/$TASK_NAME-$LABEL-$STAMP-rep$rep.json"
  T0=$SECONDS
  set +e
  # stream-json rejects a positional prompt; feed it via stdin instead.
  # CONTAINER=1 runs the claude invocation inside the pinned sandbox image
  # (see Dockerfile): same CLI + Go toolchain regardless of host, so the only
  # variable across a campaign is the arm. Auth is passed by reference via
  # -e ANTHROPIC_API_KEY (never inlined). The container runs as the invoking
  # uid:gid with a writable $HOME bind-mount, so files it writes into the
  # worktree stay host-owned and scoring/cleanup work unchanged. Default
  # bridge networking = outbound-only; the container is otherwise isolated.
  if [[ "${CONTAINER:-0}" == "1" ]]; then
    if [[ -z "${ANTHROPIC_API_KEY:-}" ]]; then
      echo "CONTAINER=1 requires ANTHROPIC_API_KEY in the environment" >&2
      exit 3
    fi
    CHOME="$WTPARENT/chome"; mkdir -p "$CHOME"
    printf '%s' "$RUN_PROMPT" | timeout "$TIMEOUT_S" docker run --rm -i \
      --user "$(id -u):$(id -g)" \
      -e ANTHROPIC_API_KEY -e HOME=/home/agent \
      -e GOCACHE=/home/agent/.cache/go-build -e GOPATH=/home/agent/go \
      -v "$WT:/work" -w /work \
      -v "$CHOME:/home/agent" \
      byproxy-bench:latest claude "${CLAUDE_ARGS[@]}" \
      > "$RAW" 2>"$RAW.stderr"
  else
    (cd "$WT" && printf '%s' "$RUN_PROMPT" | timeout "$TIMEOUT_S" claude "${CLAUDE_ARGS[@]}") > "$RAW" 2>"$RAW.stderr"
  fi
  RC=$?
  set -e
  WALL=$((SECONDS - T0))

  echo "[$TASK_NAME/$ARM rep $rep] scoring independently..." >&2
  # RAW is now a stream-json event log (one JSON object per line). The last
  # type=result event carries the summary fields; extract it for parsing.
  RESULT_OBJ="$(grep '"type":"result"' "$RAW" 2>/dev/null | tail -1)"
  [[ -z "$RESULT_OBJ" ]] && RESULT_OBJ='{}'
  # Count real subagent dispatches from the parent's tool_use events — the
  # only trustworthy proof the byproxy treatment actually ran. Total Agent/
  # Task calls, and specifically byproxy-explorer ones.
  DISPATCHES="$(jq -rc 'select(.type=="assistant") | .message.content[]? | select(.type=="tool_use" and (.name=="Task" or .name=="Agent")) | (.input.subagent_type // "unknown")' "$RAW" 2>/dev/null)"
  DISPATCH_N="$(printf '%s' "$DISPATCHES" | grep -c . || true)"
  EXPLORER_N="$(printf '%s\n' "$DISPATCHES" | grep -c 'byproxy-explorer' || true)"
  CRITIC_N="$(printf '%s\n' "$DISPATCHES" | grep -c 'byproxy-critic' || true)"
  BUILDER_N="$(printf '%s\n' "$DISPATCHES" | grep -c 'byproxy-builder' || true)"
  AUDITOR_N="$(printf '%s\n' "$DISPATCHES" | grep -c 'byproxy-auditor' || true)"
  SKILL_N="$(jq -rc 'select(.type=="assistant") | .message.content[]? | select(.type=="tool_use" and .name=="Skill") | .input.skill // "?"' "$RAW" 2>/dev/null | grep -c 'byproxy' || true)"
  # Extract the run's final message so a report-aware scorer can measure
  # disclosure/recall (never used for pass/fail — that is replayed).
  REPORT="$RAW.report"; jq -r '.result // ""' <<<"$RESULT_OBJ" 2>/dev/null > "$REPORT" || true
  # A scoring failure must never lose a paid run: default to an error
  # marker, keep the diff for post-mortem, and always emit the row.
  # A task may ship its own scorer (task-local score.sh); it receives the
  # worktree, the task dir, and the report. Otherwise the default replays
  # the named tests in tests.txt.
  if [[ -x "$TASK_DIR/score.sh" ]]; then
    SCORE="$("$TASK_DIR/score.sh" "$WT" "$TASK_DIR" "$REPORT" 2>"$RAW.score-err")" \
      || SCORE='{"error":"task score.sh failed — see .score-err","complete":false}'
  else
    SCORE="$("$HARNESS_DIR/score.sh" "$WT" "$TASK_DIR/tests.txt" 2>"$RAW.score-err")" \
      || SCORE='{"error":"score.sh failed — see .score-err","complete":false}'
  fi
  jq -e . >/dev/null 2>&1 <<<"$SCORE" \
    || SCORE='{"error":"score.sh emitted non-JSON — see .score-err","complete":false}'
  git -C "$WT" diff > "$RAW.diff" 2>/dev/null || true
  DIFFSTAT="$(git -C "$WT" diff --stat 2>/dev/null | tail -1 || true)"
  UNTRACKED="$(git -C "$WT" ls-files --others --exclude-standard 2>/dev/null | grep -v '^\.claude/' | grep -c . || true)"

  COST="$(jq -r '.total_cost_usd // "null"' <<<"$RESULT_OBJ" 2>/dev/null || echo null)"
  USAGE="$(jq -c '.usage // null' <<<"$RESULT_OBJ" 2>/dev/null || echo null)"
  TURNS="$(jq -r '.num_turns // "null"' <<<"$RESULT_OBJ" 2>/dev/null || echo null)"

  jq -n -c \
    --arg task "$TASK_NAME" --arg arm "$LABEL" --arg stamp "$STAMP" \
    --argjson rep "$rep" --argjson rc "$RC" --argjson wall "$WALL" \
    --argjson cost "$COST" --argjson usage "$USAGE" --argjson turns "$TURNS" \
    --argjson score "$SCORE" \
    --arg diffstat "$DIFFSTAT" --argjson untracked "$UNTRACKED" \
    --argjson dispatches "${DISPATCH_N:-0}" --argjson explorers "${EXPLORER_N:-0}" \
    --argjson critics "${CRITIC_N:-0}" --argjson builders "${BUILDER_N:-0}" \
    --argjson auditors "${AUDITOR_N:-0}" \
    --argjson skillinv "${SKILL_N:-0}" \
    --arg raw "$(basename "$RAW")" \
    '{task:$task, arm:$arm, rep:$rep, stamp:$stamp, exit_code:$rc,
      wall_s:$wall, cost_usd:$cost, usage:$usage, num_turns:$turns,
      byproxy_skill_invocations:$skillinv,
      subagent_dispatches:$dispatches, explorer_dispatches:$explorers,
      critic_dispatches:$critics, builder_dispatches:$builders,
      auditor_dispatches:$auditors,
      score:$score, diffstat:$diffstat, new_files:$untracked, raw:$raw}' \
    | tee -a "$JSONL"

  cleanup; trap - EXIT
done
