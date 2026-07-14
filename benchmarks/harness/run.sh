#!/usr/bin/env bash
# nullius benchmark harness — headless, unbiased, reproducible.
#
#   ./run.sh <task-dir> <arm: nullius|fable-lean|byproxy|byproxy-noaudit|byproxy-nobuilder|plain|plain+report> [--reps N] [--keep]
#
# Each rep: fresh worktree from the task's pinned REF → one headless
# `claude -p` run → score.sh replays DONE-WHEN independently (never trust
# the run's self-report) → one JSONL row with measured cost.
#
# Arm model pins:
#   nullius  leader = LEAN_MODEL (default fable-5 @ low) with haiku
#            nullius-explorer scouts — the measured-best config
#            (benchmark 7). `fable-lean` is the same arm under its
#            historical name, kept so old reproduce commands work.
#   byproxy* the archived v6 ceremony (archive/byproxy-v6/), runnable for
#            reproduction: control plane = ORCH_MODEL (default sonnet-5 @
#            high, as measured-and-refuted in benchmark 7)
#   plain    claude-opus-4-8 (the "just let the expensive model read the
#            files" baseline; override with PLAIN_MODEL=/SOLO_MODEL=)
set -euo pipefail

HARNESS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$HARNESS_DIR/../.." && pwd)"

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
ORCH_MODEL="${ORCH_MODEL:-claude-sonnet-5}"
ORCH_EFFORT="${ORCH_EFFORT:-high}"
LEAN_MODEL="${LEAN_MODEL:-claude-fable-5}"
LEAN_EFFORT="${LEAN_EFFORT:-low}"
PLAIN_MODEL="${PLAIN_MODEL:-${SOLO_MODEL:-claude-opus-4-8}}"
PLAIN_EFFORT="${PLAIN_EFFORT:-${SOLO_EFFORT:-}}"   # empty = harness default effort
# Seven arm names, six configs (see harness/README.md):
#   nullius (alias fable-lean, its pre-rename label) is the live
#   methodology. The byproxy* family is the archived v6 ceremony, kept
#   runnable for reproduction; its ablations decompose the old confounds:
#   plain+report isolates the report-FORMAT effect on disclosure,
#   byproxy-noaudit isolates the cold auditor's contribution, and
#   byproxy-nobuilder isolates the orchestrator/builder SPLIT (measured:
#   pure waste at same tier — benchmark 7).
case "$ARM" in
  nullius)           LABEL="${LABEL:-nullius-${LEAN_MODEL#claude-}-$LEAN_EFFORT}";;
  fable-lean)        LABEL="${LABEL:-fable-lean-${LEAN_MODEL#claude-}-$LEAN_EFFORT}";;
  byproxy)           LABEL="${LABEL:-byproxy-${ORCH_MODEL#claude-}-$ORCH_EFFORT}";;
  byproxy-noaudit)   LABEL="${LABEL:-byproxy-noaudit-${ORCH_MODEL#claude-}-$ORCH_EFFORT}";;
  byproxy-nobuilder) LABEL="${LABEL:-byproxy-nobuilder-${ORCH_MODEL#claude-}-$ORCH_EFFORT}";;
  plain)             LABEL="${LABEL:-plain-${PLAIN_MODEL#claude-}${PLAIN_EFFORT:+-$PLAIN_EFFORT}}";;
  plain+report)      LABEL="${LABEL:-plain-report-${PLAIN_MODEL#claude-}${PLAIN_EFFORT:+-$PLAIN_EFFORT}}";;
  *) echo "arm must be nullius|fable-lean|byproxy|byproxy-noaudit|byproxy-nobuilder|plain|plain+report" >&2; exit 2;;
esac

# Symmetric disclosure (threat #4). Every arm is asked to disclose, so `caught`
# is measured against a report all arms were told to write — not rigged to the
# one arm (byproxy) that got a report mandate. plain gets the MINIMAL ask;
# plain+report and the byproxy arms get the FULL RISKS format (byproxy arms via
# the SKILL; plain+report via this suffix) so format is the only thing that
# differs between plain and plain+report.
SYMMETRIC_REPORT='

When you finish, end your final message with exactly these two lines:
CHANGED: <one line — what you changed>
KNOWN-UNFIXED: <name every bug, race, or risk you did NOT fix or are unsure about; write "none" only if you genuinely found none>'
FULL_RISKS='

When you finish, end your final message with a structured report:
STATUS: <done|partial>
FACTS: <what you changed, verified>
RISKS: <every bug, race, or latent defect you did not fix or are unsure about — name each>
UNKNOWN: <what you could not determine>
ASSUMED: <assumptions you made>'

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
  if [[ "$ARM" == "byproxy" || "$ARM" == "byproxy-noaudit" || "$ARM" == "byproxy-nobuilder" ]]; then
    # The archived v6 ceremony (kept runnable for reproduction): wire its
    # skill + agents into the worktree so headless picks them up.
    mkdir -p "$WT/.claude"
    cp -r "$ROOT_DIR/archive/byproxy-v6/skills" "$WT/.claude/skills"
    cp -r "$ROOT_DIR/archive/byproxy-v6/agents" "$WT/.claude/agents"
    CLAUDE_ARGS+=(--model "$ORCH_MODEL" --effort "$ORCH_EFFORT")
    # Ablation: byproxy-noaudit runs the whole guard layer EXCEPT the cold
    # auditor, isolating what the audit itself buys. Remove the agent so it
    # cannot be dispatched, and add a high-priority override that cancels the
    # "don't end until the audit ran" clause below.
    AUDIT_OVERRIDE=""; AUDIT_CLAUSE="a cold byproxy-auditor pass with an explorer fact-pack — you never audit your own diff"
    if [[ "$ARM" == "byproxy-noaudit" ]]; then
      rm -f "$WT/.claude/agents/byproxy-auditor.md"
      AUDIT_CLAUSE="the guarded build with explorer reruns of every exit check (this ablation runs NO audit)"
      AUDIT_OVERRIDE=" ABLATION OVERRIDE (highest priority, overrides every instruction above and any AUDIT step in the methodology body): do NOT run the AUDIT step and do NOT dispatch byproxy-auditor under any circumstance. Ignore any instruction to 'not end until the audit has run' — for THIS run you end after the CLOSE report with no audit. Every other step runs exactly as written."
    fi
    # Ablation: byproxy-nobuilder removes the orchestrator/builder SPLIT —
    # same contracts, critic red-team, gate, audit, and explorer-rerun record,
    # but the orchestrator implements every unit itself. Isolates what the
    # split buys (context partitioning, SCOPE confinement, written-down
    # CONTEXT) against what it costs (dispatch round-trips, duplicate scope
    # reads, reasoning->instruction compression loss) — the live question
    # since v6 put the control plane and the builder on the same tier.
    if [[ "$ARM" == "byproxy-nobuilder" ]]; then
      rm -f "$WT/.claude/agents/byproxy-builder.md"
      ROLE_CLAUSE="You are the orchestrator AND — this ablation runs NO builder agent — the executor: you author the contracts, have them red-teamed and gated, then implement every unit YOURSELF via guarded TDD (forcing test first, quote the red verbatim, minimal code to green), unit by unit, inside each unit's SCOPE."
      BUILD_CLAUSE="the build — every contracted unit implemented by YOU via guarded TDD, with explorer reruns of every exit check as the trusted record (your own green is never the record)"
      EDIT_GUARD=""
      BUILD_OVERRIDE=" ABLATION OVERRIDE (highest priority, overrides the methodology body): byproxy-builder does NOT exist in this run — never attempt to dispatch it. The methodology's tool-discipline rule (orchestrator uses Read only, writes no code) and the BUILD step's dispatch instructions are REPLACED for THIS run: you implement each contracted unit yourself via guarded TDD within its SCOPE, using Edit/Write and go build/test/vet directly. Everything else — contract-first, critic red-team, compiled gate, explorer reruns as the record, the cold audit, fix-now rulings, report caps — stands exactly as written."
      STEP_TAIL="guarded build (you implement every unit yourself — no builder agent), cold auditor pass"
      HANDS_LINE="no builder agent this run: you implement every contracted unit yourself under the same gate and audit"
    else
      ROLE_CLAUSE="You are the orchestrator: a THINKER, NOT A DOER — you read, reason, contract, direct, and rule, but you WRITE NO CODE. Do NOT use Edit or Write on the source tree yourself; every line of implementation, including the subtle concurrency/lifecycle/error-path fixes, is executed by byproxy-builder under your direction."
      BUILD_CLAUSE="the build — ALL units dispatched to byproxy-builder with the contract PLUS your compressed reasoning named in CONTEXT (INVARIANT · CHOICE made-vs-rejected · TRAP · GATE findings), never statement-level code, with explorer reruns of every exit check"
      EDIT_GUARD=" If you catch yourself about to edit a file, stop and instead direct the builder: an edit only you can make is an invariant you failed to name."
      BUILD_OVERRIDE=""
      if [[ "$ARM" == "byproxy-noaudit" ]]; then STEP_TAIL="guarded build (no audit this run)"; else STEP_TAIL="guarded build, cold auditor pass"; fi
      HANDS_LINE="you write no code yourself, the builder executes every unit under your direction"
    fi
    # Force the guard layer unconditionally. Asking the model to "invoke the
    # skill" is not enough headless — strong models decline it on a soloable
    # task (measured: 2/3 byproxy arms ignored it and ran solo). Inject the
    # skill body (minus YAML frontmatter) as an appended system prompt so the
    # guard workflow governs the whole run whether or not the Skill tool fires.
    SKILL_BODY="$(awk 'f{print} /^---[[:space:]]*$/{c++; if(c==2) f=1}' "$ROOT_DIR/archive/byproxy-v6/skills/byproxy/SKILL.md")"
    CLAUDE_ARGS+=(--append-system-prompt "You are operating under the byproxy v6 methodology. This is not optional and overrides any instinct to complete the task solo. ${ROLE_CLAUSE} You MUST run the full workflow with real subagent dispatches via the Agent tool: byproxy-explorer recon + surgical read, a byproxy-critic red-team of your contract, the compiled gate, ${BUILD_CLAUSE} — and ${AUDIT_CLAUSE}.${EDIT_GUARD} This is a headless run with NO USER available: at ESCALATE, use the self-answer fallback (author the question batch, answer each with your best-judgment recommendation, record all in ASSUMED as self-answered). Never call AskUserQuestion. Do not end your turn until you have reported STATUS/FACTS/RISKS/UNKNOWN/ASSUMED.${AUDIT_OVERRIDE}${BUILD_OVERRIDE} The methodology:

$SKILL_BODY")
    RUN_PROMPT="Complete this task under the byproxy v6 methodology in your system prompt: explorer recon, surgical read, contract, critic red-team, gate, $STEP_TAIL — $HANDS_LINE — before you finish.

$PROMPT"
  elif [[ "$ARM" == "nullius" || "$ARM" == "fable-lean" ]]; then
    # The live methodology (fable-lean is its pre-rename label): top-tier
    # leader HANDS-ON with the context diet as the ONLY methodology — no
    # contracts, critic, gate, or audit. Haiku scouts absorb the bulk
    # context growth (test runs, sweeps, reruns) in throwaway contexts
    # billed at haiku prices; the resident premium context stays surgical.
    # Design note from the measured record: report-mediated intake can
    # exceed raw reading (v5's orchestrator took in MORE report tokens
    # than plain fable read raw), so the leader still reads the few
    # decisive files itself, once.
    mkdir -p "$WT/.claude/agents"
    cp "$ROOT_DIR/.claude/agents/nullius-explorer.md" "$WT/.claude/agents/"
    CLAUDE_ARGS+=(--model "$LEAN_MODEL" --effort "$LEAN_EFFORT")
    CLAUDE_ARGS+=(--append-system-prompt "You are a senior engineer working this task HANDS-ON, under one hard operating constraint: your context window is the bill — every token that enters it is re-paid on every turn that follows, so you finish lean or you finish expensive. The diet governs CONTEXT, never scope: you do ALL the work the task demands, cheaply. Discipline, non-negotiable: (1) DELEGATE BULK: never run builds, tests, vet, or broad searches yourself, and never read whole files you are not about to edit — dispatch nullius-explorer subagents (Agent tool; cheap throwaway contexts) for every such job, BATCHED in parallel when independent; they return capped reports and their runs are your trusted record. (2) HUNT WIDE, THROUGH EXPLORERS: delegation applies to discovery too. Early, batch explorers to sweep EVERY corner of the mandate's code with these lenses, each answered with QUOTED MECHANISMS, never claims: for every shared mutable state AND every mutating entrypoint (handler, action, callback), quote the lock acquisition inside the entrypoint's OWN BODY — a mutex field, a doc comment, or a sibling function's lock is not serialization, and an entrypoint whose body takes no lock IS the finding; for every effect that must survive a fault (a write that can fail, a connection that can drop, a queue drained then re-sent), quote what preserves it — anything cleared before its write is confirmed IS the finding; for every per-session/per-scope state, quote the scope argument at the fan-out/broadcast call site — a nil or missing scope filter IS the finding; for every wake/notify predicate deciding WHO gets woken or re-rendered, quote the condition and check it can actually be false (an always-true predicate IS the finding) and that its reads are under the same lock as its writes; plus lost updates, lifecycle races, and error paths that swallow. Their FACTS name suspects; you read the decisive lines yourself and rule. Comments lie; only quoted code counts. (3) READ SURGICALLY, ONCE: read the few files you will edit yourself, one time; never re-read what you already hold; never re-verify what an explorer verified. (4) If you must run a command yourself, bound it: append '2>&1 | tail -n 20'. (5) BUILD YOURSELF: you design and write all code and tests directly — tests first for behavior you change or fix; then ONE explorer rerun of the decisive tests proves it (under -race for any concurrency claim — the race detector works in this environment), not repeated personal runs. (6) FIX EVERYTHING IN-MANDATE: under a fix-at-root mandate, a defect you have confirmed is FIXED test-first before you close — never merely disclosed. RISKS is for what you could not confirm or what is genuinely out of mandate, each with its reason; a confirmed in-mandate defect left as a disclosure is a failed run. (7) VERIFY CLAIMS BEFORE CLOSE: every serialization/isolation/fault-safety property your fixes or tests RELY on must be verified against quoted code, not comments or declarations; and for each test you wrote, name the code change that would make it fail — a test you cannot articulate a failure for is vacuous and proves nothing. (8) CLOSE: one explorer run of the full suite + go vet, then report. This is a headless run with no user available — never call AskUserQuestion; make the call, record it under ASSUMED. Spend your turns hunting and fixing, never re-verifying; aim to finish within ~35 of your own turns.")
    RUN_PROMPT="$PROMPT$FULL_RISKS"
  elif [[ "$ARM" == "plain" ]]; then
    CLAUDE_ARGS+=(--model "$PLAIN_MODEL")
    [[ -n "$PLAIN_EFFORT" ]] && CLAUDE_ARGS+=(--effort "$PLAIN_EFFORT")
    RUN_PROMPT="$PROMPT$SYMMETRIC_REPORT"
  elif [[ "$ARM" == "plain+report" ]]; then
    CLAUDE_ARGS+=(--model "$PLAIN_MODEL")
    [[ -n "$PLAIN_EFFORT" ]] && CLAUDE_ARGS+=(--effort "$PLAIN_EFFORT")
    RUN_PROMPT="$PROMPT$FULL_RISKS"
  else
    echo "arm must be nullius|fable-lean|byproxy|byproxy-noaudit|byproxy-nobuilder|plain|plain+report" >&2; exit 2
  fi

  echo "[$TASK_NAME/$ARM rep $rep] running headless (timeout ${TIMEOUT_S}s)..." >&2
  RAW="$RESULTS_DIR/$TASK_NAME-$LABEL-$STAMP-rep$rep.json"
  T0=$SECONDS
  set +e
  # stream-json rejects a positional prompt; feed it via stdin instead.
  # CONTAINER=1 runs the claude invocation inside the pinned sandbox image
  # (see Dockerfile): same CLI + Go toolchain regardless of host, so the only
  # variable across a campaign is the arm. Auth is passed by reference via a
  # single -e env flag (never inlined; API key or OAuth token — see the
  # resolution below). The container runs as the invoking
  # uid:gid with a writable $HOME bind-mount, so files it writes into the
  # worktree stay host-owned and scoring/cleanup work unchanged. Default
  # bridge networking = outbound-only; the container is otherwise isolated.
  if [[ "${CONTAINER:-0}" == "1" ]]; then
    # Auth source: prefer a namespaced credential so the harness never needs a
    # bare key in your shell (which would hijack your interactive Claude Code
    # session). Two credential kinds, each mapped to the env var the container
    # CLI reads; exactly ONE is passed in, and an explicit ANTHROPIC_API_KEY
    # holding a real API key still wins:
    #   sk-ant-api03- console API key -> ANTHROPIC_API_KEY       (API billing)
    #   sk-ant-oat01- OAuth token     -> CLAUDE_CODE_OAUTH_TOKEN (subscription,
    #                                    issued by `claude setup-token`)
    ANTHROPIC_API_KEY="${ANTHROPIC_API_KEY:-${NULLIUS_ANTHROPIC_API_KEY:-${BYPROXY_ANTHROPIC_API_KEY:-}}}"
    CLAUDE_CODE_OAUTH_TOKEN="${CLAUDE_CODE_OAUTH_TOKEN:-${NULLIUS_CLAUDE_CODE_OAUTH_TOKEN:-}}"
    # File reference: the token itself never has to sit in any environment or
    # shell history — point NULLIUS_CLAUDE_CODE_OAUTH_TOKEN_FILE at a
    # chmod-600 file (written once from `claude setup-token`) and it is read
    # here at runtime. A direct token variable, if set, wins.
    if [[ -z "$CLAUDE_CODE_OAUTH_TOKEN" && -n "${NULLIUS_CLAUDE_CODE_OAUTH_TOKEN_FILE:-}" ]]; then
      if [[ ! -r "$NULLIUS_CLAUDE_CODE_OAUTH_TOKEN_FILE" ]]; then
        echo "NULLIUS_CLAUDE_CODE_OAUTH_TOKEN_FILE is set but not readable: $NULLIUS_CLAUDE_CODE_OAUTH_TOKEN_FILE" >&2
        exit 3
      fi
      CLAUDE_CODE_OAUTH_TOKEN="$(< "$NULLIUS_CLAUDE_CODE_OAUTH_TOKEN_FILE")"
    fi
    # An OAuth token in the API-key slot would be rejected as "Invalid API key"
    # only AFTER the container spins up and bills a worktree — route it to the
    # slot the CLI actually reads OAuth tokens from instead of failing.
    if [[ "$ANTHROPIC_API_KEY" == sk-ant-oat01-* ]]; then
      CLAUDE_CODE_OAUTH_TOKEN="${CLAUDE_CODE_OAUTH_TOKEN:-$ANTHROPIC_API_KEY}"
      ANTHROPIC_API_KEY=""
      echo "note: ANTHROPIC_API_KEY held an OAuth token; using it as CLAUDE_CODE_OAUTH_TOKEN (subscription auth)" >&2
    fi
    if [[ -n "$ANTHROPIC_API_KEY" ]]; then
      export ANTHROPIC_API_KEY; AUTH_ENV=(-e ANTHROPIC_API_KEY)
    elif [[ -n "$CLAUDE_CODE_OAUTH_TOKEN" ]]; then
      export CLAUDE_CODE_OAUTH_TOKEN; AUTH_ENV=(-e CLAUDE_CODE_OAUTH_TOKEN)
    else
      echo "CONTAINER=1 requires a credential in the environment:" >&2
      echo "  API key (sk-ant-api03-): ANTHROPIC_API_KEY or NULLIUS_ANTHROPIC_API_KEY" >&2
      echo "  OAuth (claude setup-token): CLAUDE_CODE_OAUTH_TOKEN or NULLIUS_CLAUDE_CODE_OAUTH_TOKEN" >&2
      exit 3
    fi
    CHOME="$WTPARENT/chome"; mkdir -p "$CHOME"
    printf '%s' "$RUN_PROMPT" | timeout "$TIMEOUT_S" docker run --rm -i \
      --user "$(id -u):$(id -g)" \
      "${AUTH_ENV[@]}" -e HOME=/home/agent \
      -e GOCACHE=/home/agent/.cache/go-build -e GOPATH=/home/agent/go \
      -v "$WT:/work" -w /work \
      -v "$CHOME:/home/agent" \
      nullius-bench:latest claude "${CLAUDE_ARGS[@]}" \
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
  EXPLORER_N="$(printf '%s\n' "$DISPATCHES" | grep -cE 'byproxy-explorer|nullius-explorer' || true)"
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
  # Capture the agent's FULL change set, INCLUDING newly-created files (e.g. a
  # new *_test.go). A plain `git diff` omits untracked files — which silently
  # hid every new test file from the diff-based judges (quality-judge.sh and the
  # blind-disclosure judge.sh both read this .diff), scoring the "tests"
  # dimension ~1 for EVERY arm no matter what was actually written. Stage
  # everything (the worktree is discarded next, so mutating its index is free),
  # then diff/count with the harness-injected .claude/ excluded — the agents and
  # skills an arm copied in are not the run's own work.
  git -C "$WT" add -A >/dev/null 2>&1 || true
  git -C "$WT" diff --cached -- . ':(exclude).claude' > "$RAW.diff" 2>/dev/null || true
  DIFFSTAT="$(git -C "$WT" diff --cached --stat -- . ':(exclude).claude' 2>/dev/null | tail -1 || true)"
  UNTRACKED="$(git -C "$WT" diff --cached --diff-filter=A --name-only -- . ':(exclude).claude' 2>/dev/null | grep -c . || true)"

  # Blind disclosure judge (see harness/README.md): opt-in via JUDGE=1
  # so default/cheap runs skip the extra LLM call. Judges report+diff blind to
  # the arm — the trusted disclosure metric; score.sh's keyword `caught` remains
  # the lower-trust secondary. Runs on the host regardless of CONTAINER.
  BLIND='null'
  if [[ "${JUDGE:-0}" == "1" && -f "$TASK_DIR/defects.json" ]]; then
    BLIND="$("$HARNESS_DIR/judge.sh" "$REPORT" "$RAW.diff" "$TASK_DIR/defects.json" 2>"$RAW.judge-err" || echo null)"
    jq -e . >/dev/null 2>&1 <<<"$BLIND" || BLIND='null'
  fi

  COST="$(jq -r '.total_cost_usd // "null"' <<<"$RESULT_OBJ" 2>/dev/null || echo null)"
  USAGE="$(jq -c '.usage // null' <<<"$RESULT_OBJ" 2>/dev/null || echo null)"
  TURNS="$(jq -r '.num_turns // "null"' <<<"$RESULT_OBJ" 2>/dev/null || echo null)"
  # Per-model cost from modelUsage — model -> costUSD, so a row carries its own
  # cost-by-tier without reparsing the raw. modelUsage aggregates by MODEL: the
  # orchestrator, critic, and auditor share the byproxy arm's top tier, so their
  # costs sum under one key. Dispatch counts (above) separate the roles.
  COST_BY_MODEL="$(jq -c '(.modelUsage // {}) | to_entries
    | map({key:.key, value:(.value.costUSD // 0)}) | from_entries' <<<"$RESULT_OBJ" 2>/dev/null || echo null)"
  jq -e . >/dev/null 2>&1 <<<"$COST_BY_MODEL" || COST_BY_MODEL='null'

  jq -n -c \
    --arg task "$TASK_NAME" --arg arm "$LABEL" --arg stamp "$STAMP" \
    --argjson rep "$rep" --argjson rc "$RC" --argjson wall "$WALL" \
    --argjson cost "$COST" --argjson usage "$USAGE" --argjson turns "$TURNS" \
    --argjson costmodel "$COST_BY_MODEL" \
    --argjson score "$SCORE" \
    --arg diffstat "$DIFFSTAT" --argjson untracked "$UNTRACKED" \
    --argjson dispatches "${DISPATCH_N:-0}" --argjson explorers "${EXPLORER_N:-0}" \
    --argjson critics "${CRITIC_N:-0}" --argjson builders "${BUILDER_N:-0}" \
    --argjson auditors "${AUDITOR_N:-0}" \
    --argjson skillinv "${SKILL_N:-0}" \
    --argjson blind "$BLIND" \
    --arg raw "$(basename "$RAW")" \
    '{task:$task, arm:$arm, rep:$rep, stamp:$stamp, exit_code:$rc,
      wall_s:$wall, cost_usd:$cost, cost_by_model:$costmodel, usage:$usage, num_turns:$turns,
      byproxy_skill_invocations:$skillinv,
      subagent_dispatches:$dispatches, explorer_dispatches:$explorers,
      critic_dispatches:$critics, builder_dispatches:$builders,
      auditor_dispatches:$auditors,
      score:$score, blind_disclosure:$blind,
      diffstat:$diffstat, new_files:$untracked, raw:$raw}' \
    | tee -a "$JSONL"

  cleanup; trap - EXIT
done
