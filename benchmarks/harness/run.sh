#!/usr/bin/env bash
# nullius benchmark harness — headless, unbiased, reproducible.
#
#   ./run.sh <task-dir> <arm: cc-nullius|byproxy|byproxy-noaudit|byproxy-nobuilder|go-nullius|plain|plain+report> [--reps N] [--keep]
#
# Each rep: fresh worktree from the task's pinned REF → one headless
# `claude -p` run → score.sh replays DONE-WHEN independently (never trust
# the run's self-report) → one JSONL row with measured cost.
#
# Arm model pins:
#   cc-nullius  leader = LEAN_MODEL (default fable-5 @ low) driving the
#            cc-nullius PLUGIN (scout/lens-hunter haiku, craftsman sonnet)
#            with the diet governor hook live.
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

# EXTRA_PROMPT_FILE: optional mandate suffix appended to the task prompt for
# EVERY arm in the invocation — used by A/B designs that vary the mandate
# (e.g. prevention-vs-compaction's full-familiarization clause) while keeping
# it identical across arms. Set LABEL too, so rows stay distinguishable.
if [[ -n "${EXTRA_PROMPT_FILE:-}" ]]; then
  [[ -r "$EXTRA_PROMPT_FILE" ]] || { echo "EXTRA_PROMPT_FILE is set but not readable: $EXTRA_PROMPT_FILE" >&2; exit 3; }
  PROMPT="$PROMPT

$(cat "$EXTRA_PROMPT_FILE")"
fi

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
GO_MODEL="${GO_MODEL:-opus}"     # go-nullius arm: binary model alias (haiku|sonnet|opus|fable) or full id
GO_EFFORT="${GO_EFFORT-low}"     # go-nullius arm: output_config effort (single-dash: explicit "" stays empty; haiku rejects the param)
# Arm names and configs (see harness/README.md):
#   cc-nullius is the live methodology. The byproxy* family is the archived v6 ceremony, kept
#   runnable for reproduction; its ablations decompose the old confounds:
#   plain+report isolates the report-FORMAT effect on disclosure,
#   byproxy-noaudit isolates the cold auditor's contribution, and
#   byproxy-nobuilder isolates the orchestrator/builder SPLIT (measured:
#   pure waste at same tier — benchmark 7).
case "$ARM" in
  cc-nullius)        LABEL="${LABEL:-cc-nullius-${LEAN_MODEL#claude-}-$LEAN_EFFORT}";;
  byproxy)           LABEL="${LABEL:-byproxy-${ORCH_MODEL#claude-}-$ORCH_EFFORT}";;
  byproxy-noaudit)   LABEL="${LABEL:-byproxy-noaudit-${ORCH_MODEL#claude-}-$ORCH_EFFORT}";;
  byproxy-nobuilder) LABEL="${LABEL:-byproxy-nobuilder-${ORCH_MODEL#claude-}-$ORCH_EFFORT}";;
  go-nullius)        LABEL="${LABEL:-go-nullius-${GO_MODEL#claude-}-$GO_EFFORT}";;
  plain)             LABEL="${LABEL:-plain-${PLAIN_MODEL#claude-}${PLAIN_EFFORT:+-$PLAIN_EFFORT}}";;
  plain+report)      LABEL="${LABEL:-plain-report-${PLAIN_MODEL#claude-}${PLAIN_EFFORT:+-$PLAIN_EFFORT}}";;
  *) echo "arm must be cc-nullius|byproxy|byproxy-noaudit|byproxy-nobuilder|plain|plain+report" >&2; exit 2;;
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

# CRED_FILE (opt-in): path to a host Claude credentials JSON (normally
# "$HOME/.claude/.credentials.json") so a containerised rep can ride your
# existing OAuth login with no credential in the environment at all. Validated
# here, by PATH ONLY — the contents are never read, logged or interpolated
# anywhere in this harness.
if [[ -n "${CRED_FILE:-}" ]]; then
  if [[ "${CONTAINER:-0}" != "1" ]]; then
    echo "CRED_FILE is only supported with CONTAINER=1 (the host path already uses your own \$HOME login)" >&2
    exit 3
  fi
  if [[ ! -r "$CRED_FILE" || ! -s "$CRED_FILE" ]]; then
    echo "CRED_FILE is set but missing, unreadable or empty: $CRED_FILE" >&2
    exit 3
  fi
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
    # The credential copy dies with every rep, KEEP or not.
    rm -f "$WTPARENT/chome/.claude/.credentials.json" >/dev/null 2>&1 || true
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
  # REMOVED ARMS: fable-lean, nullius, nullius-rev1, nullius-rev2 (the
  # fable-lean block and the rev2 fan-in block both lived here). They copied
  # .claude/agents/nullius-explorer.md and
  # nullius-hunter.md, both deleted in dfdf7af (salvaged into cc-nullius/), so
  # they could no longer run. Restore the agents from the commit before that:
  #   git show b7893a7:.claude/agents/nullius-explorer.md
  #   git show b7893a7:.claude/agents/nullius-hunter.md
  # The arm code itself is in this file at the commit preceding its deletion.
  elif [[ "$ARM" == "cc-nullius" ]]; then
    # The cc-nullius PLUGIN arm: same doctrine as nullius, but the diet is
    # MECHANIZED — a PreToolUse hook (diet governor) denies main-thread
    # sweeps/whole-reads/heavy-Bash and steers to the plugin agents
    # (scout/lens-hunter haiku; craftsman sonnet, last resort; no judge
    # tier — the close-out record is a scout dispatch). Everything is
    # copied INTO the worktree so container mode works unchanged; the hook
    # is wired via --settings (project-settings hooks may be untrusted
    # headless). The skill body is force-injected like the other arms —
    # headless models ignore optional skills.
    # NOTHING is hand-copied any more: the plugin install inside the container
    # (see PLUGIN_MOUNT / nullius-entry below) supplies agents, hooks and the
    # hooks.json wiring from one source of truth. Host (CONTAINER=0) reps still
    # need the plugin installed in the ambient CLI.
CLAUDE_ARGS+=(--model "$LEAN_MODEL" --effort "$LEAN_EFFORT")
    # Resolve the doctrine skill from what EXISTS (the plugin's skill dirs have
    # been renamed before): prefer `starve` — the session-start hook's entry
    # point — else the sole skill, if there is exactly one. A missing or empty
    # body is fatal: a doctrine-less cc-nullius rep silently invalidates the arm.
    CC_SKILL_MD="$ROOT_DIR/cc-nullius/skills/starve/SKILL.md"
    if [[ ! -r "$CC_SKILL_MD" ]]; then
      CC_SKILL_CANDS=("$ROOT_DIR"/cc-nullius/skills/*/SKILL.md)
      if [[ ${#CC_SKILL_CANDS[@]} -eq 1 && -r "${CC_SKILL_CANDS[0]}" ]]; then
        CC_SKILL_MD="${CC_SKILL_CANDS[0]}"
      else
        echo "cc-nullius arm: doctrine SKILL.md not found (looked for $CC_SKILL_MD; candidates: ${CC_SKILL_CANDS[*]})" >&2; exit 3
      fi
    fi
    CC_SKILL_BODY="$(awk 'f{print} /^---[[:space:]]*$/{c++; if(c==2) f=1}' "$CC_SKILL_MD")"
    [[ -n "${CC_SKILL_BODY//[[:space:]]/}" ]] || { echo "cc-nullius arm: doctrine body empty in $CC_SKILL_MD — refusing to run a doctrine-less rep" >&2; exit 3; }
    CLAUDE_ARGS+=(--append-system-prompt "You are the nullius starved orchestrator; the doctrine below governs this run. A diet-governor hook denies context-fattening calls on your thread — by design: obey each denial's steering reason, never fight it. Headless run, no user: never call AskUserQuestion; self-answer and record under ASSUMED as 'self-answered: Q -> A'. Do not end before the close-out scout record (full suite + linters + exported-surface diff) has run and you have ruled on it. The doctrine:

$CC_SKILL_BODY")
    RUN_PROMPT="$PROMPT$FULL_RISKS"
  elif [[ "$ARM" == "go-nullius" ]]; then
    # The ground-up Go agent (go-nullius/): its own /v1/messages loop with the
    # nullius machinery compiled in — governor gate, editor eviction sweeps,
    # hunt/rule/close tools, haiku scout subprocesses, post-close compaction.
    # No claude CLI involved; execution and cost extraction branch below.
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
    echo "arm must be cc-nullius|byproxy|byproxy-noaudit|byproxy-nobuilder|plain|plain+report" >&2; exit 2
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
  if [[ "$ARM" == "go-nullius" ]]; then
    # Build once per rep from the repo tree; run headless in the worktree.
    # Auth: the binary resolves CC subscription OAuth / ANTHROPIC_API_KEY
    # itself (internal/auth precedence). stdout = final report only.
    GN_BIN="$WTPARENT/go-nullius-bin"
    (cd "$ROOT_DIR/go-nullius" && go build -o "$GN_BIN" ./cmd/go-nullius) \
      || { echo "go-nullius build failed" >&2; exit 4; }
    GN_SESSION="bench"
    # Bounded 429 retry: the binary has no leader-level rate-limit retry
    # (scouts do); a first-call 429 otherwise kills the rep with zero
    # billed work. 5 attempts, 120s backoff, keyed on the stderr message.
    GN_ATTEMPT=1
    while :; do
      GN_RC=0
      (cd "$WT" && timeout "$TIMEOUT_S" "$GN_BIN" -p "$RUN_PROMPT" \
          --model "$GO_MODEL" --effort "$GO_EFFORT" --dir "$WT" --session "$GN_SESSION") \
        > "$RAW" 2>"$RAW.stderr" || GN_RC=$?
      if [[ $GN_RC -ne 0 ]] && grep -qiE '429|rate_limit' "$RAW.stderr" && [[ $GN_ATTEMPT -lt 5 ]]; then
        echo "  go-nullius hit rate limit (attempt $GN_ATTEMPT/5), retrying in 120s..." >&2
        GN_ATTEMPT=$((GN_ATTEMPT+1)); sleep 120; continue
      fi
      break
    done
    (exit "$GN_RC")   # RC=$? below must see the binary's real exit, not break's 0
  elif [[ "${CONTAINER:-0}" == "1" ]]; then
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
    elif [[ -n "${CRED_FILE:-}" ]]; then
      AUTH_ENV=()   # no env credential: the copied login below is the auth
    else
      echo "CONTAINER=1 requires a credential in the environment (or CRED_FILE):" >&2
      echo "  API key (sk-ant-api03-): ANTHROPIC_API_KEY or NULLIUS_ANTHROPIC_API_KEY" >&2
      echo "  OAuth (claude setup-token): CLAUDE_CODE_OAUTH_TOKEN or NULLIUS_CLAUDE_CODE_OAUTH_TOKEN" >&2
      echo "  existing host login: CRED_FILE=\$HOME/.claude/.credentials.json" >&2
      exit 3
    fi
    CHOME="$WTPARENT/chome"; mkdir -p "$CHOME"
    # CRED_FILE: COPY the login into this rep's throwaway home, where the
    # container's claude looks for it. Never bind-mounted — read-only would
    # break OAuth refresh mid-run, writable would let the container rewrite
    # your real credential store. The copy refreshes freely and dies with the
    # worktree; only its path and size are ever mentioned.
    if [[ -n "${CRED_FILE:-}" ]]; then
      CRED_COPY="$CHOME/.claude/.credentials.json"
      mkdir -p "$CHOME/.claude"
      ( umask 077; cp "$CRED_FILE" "$CRED_COPY" )
      chmod 600 "$CRED_COPY"
      if [[ ${#AUTH_ENV[@]} -gt 0 ]]; then
        echo "note: CRED_FILE copied to the rep home, but an env credential wins for this run (${AUTH_ENV[1]})" >&2
      else
        echo "note: authenticating from CRED_FILE ($CRED_FILE, $(wc -c < "$CRED_FILE") bytes), copied into the rep home" >&2
      fi
    fi
    # Per-rep host dir for the container's TMPDIR, so files the run drops there
    # (notably the diet-governor stats file) are readable by the capture below.
    # Mounted under HOME rather than over /tmp: nothing in the image expects a
    # pre-populated /home/agent/tmp, and --user uid:gid owns it like $CHOME.
    CTMP="$WTPARENT/ctmp"; mkdir -p "$CTMP"
    # cc-nullius runs the REAL PLUGIN: `claude plugin install` from the
    # bind-mounted working tree, instead of hand-copying agents/hooks and
    # re-implementing hooks.json as a --settings file. Measured 2026-08-20: the
    # hand-copied path shipped only diet-governor.mjs while it imports
    # ./ledger-path.mjs (added in dfdf7af), so the hook died with
    # ERR_MODULE_NOT_FOUND on EVERY call and every containerized rep ran
    # ungoverned. Installing the plugin makes the benchmark test the artifact
    # users actually install, and the install FAILS LOUDLY here.
    PLUGIN_MOUNT=(); CONTAINER_ENTRY=""
    if [[ "$ARM" == "cc-nullius" ]]; then
      PLUGIN_MOUNT=(-v "$ROOT_DIR/cc-nullius:/plugin-src:ro")
      CONTAINER_ENTRY="/usr/local/bin/nullius-entry"
      cat > "$CHOME/nullius-entry" <<'ENTRY'
#!/bin/sh
# Install the plugin, then exec claude with the args the harness passed.
set -e
claude plugin marketplace add /plugin-src >/tmp/plug.log 2>&1 || {
  echo "FATAL: plugin marketplace add failed" >&2; cat /tmp/plug.log >&2; exit 9; }
claude plugin install nullius@nullius-local >>/tmp/plug.log 2>&1 || {
  echo "FATAL: plugin install failed" >&2; cat /tmp/plug.log >&2; exit 9; }
claude plugin list 2>/dev/null | grep -q "nullius@nullius-local" || {
  echo "FATAL: plugin not listed after install" >&2; cat /tmp/plug.log >&2; exit 9; }
exec claude "$@"
ENTRY
      chmod +x "$CHOME/nullius-entry"
      PLUGIN_MOUNT+=(-v "$CHOME/nullius-entry:/usr/local/bin/nullius-entry:ro")
    fi
    printf '%s' "$RUN_PROMPT" | timeout "$TIMEOUT_S" docker run --rm -i \
      --user "$(id -u):$(id -g)" \
      "${AUTH_ENV[@]}" -e HOME=/home/agent \
      -e CLAUDE_PROJECT_DIR=/work \
      ${CLAUDE_CODE_AUTO_COMPACT_WINDOW:+-e CLAUDE_CODE_AUTO_COMPACT_WINDOW="$CLAUDE_CODE_AUTO_COMPACT_WINDOW"} \
      -e GOCACHE=/home/agent/.cache/go-build -e GOPATH=/home/agent/go \
      -v "$WT:/work" -w /work \
      -v "$CHOME:/home/agent" \
      -e TMPDIR=/home/agent/tmp -v "$CTMP:/home/agent/tmp" \
      "${PLUGIN_MOUNT[@]}" \
      nullius-bench:latest ${CONTAINER_ENTRY:-claude} "${CLAUDE_ARGS[@]}" \
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
  RESULT_OBJ="$(grep '"type":"result"' "$RAW" 2>/dev/null | tail -1 || true)"
  [[ -z "$RESULT_OBJ" ]] && RESULT_OBJ='{}'
  # Count real subagent dispatches from the parent's tool_use events — the
  # only trustworthy proof the byproxy treatment actually ran. Total Agent/
  # Task calls, and specifically byproxy-explorer ones.
  DISPATCHES="$(jq -rc 'select(.type=="assistant") | .message.content[]? | select(.type=="tool_use" and (.name=="Task" or .name=="Agent")) | (.input.subagent_type // "unknown")' "$RAW" 2>/dev/null)"
  DISPATCH_N="$(printf '%s' "$DISPATCHES" | grep -c . || true)"
  EXPLORER_N="$(printf '%s\n' "$DISPATCHES" | grep -cE 'byproxy-explorer|nullius-explorer|nullius-scout|nullius-lens-hunter' || true)"
  CRAFTSMAN_N="$(printf '%s\n' "$DISPATCHES" | grep -c 'nullius-craftsman' || true)"
  CCJUDGE_N="$(printf '%s\n' "$DISPATCHES" | grep -c 'nullius-judge' || true)"
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
  git -C "$WT" diff --cached -- . ':(exclude).claude' ':(exclude).nullius' > "$RAW.diff" 2>/dev/null || true
  DIFFSTAT="$(git -C "$WT" diff --cached --stat -- . ':(exclude).claude' ':(exclude).nullius' 2>/dev/null | tail -1 || true)"
  UNTRACKED="$(git -C "$WT" diff --cached --diff-filter=A --name-only -- . ':(exclude).claude' ':(exclude).nullius' 2>/dev/null | grep -c . || true)"

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
  # Cache economics per model + overall KV hit rate. Motivation (measured +
  # sourced, 2026-07-17 research sweep): cache reads bill at 0.1x base input,
  # so "resident context re-paid every turn" is ~10% true in dollars — the
  # real per-arm cost drivers are cache WRITES and turn count, and no prior
  # bench row could distinguish them. hit_rate = read / (read + write + raw).
  USAGE_BY_MODEL="$(jq -c '(.modelUsage // {}) | to_entries | map({key:.key, value:{
      in:(.value.inputTokens // 0), out:(.value.outputTokens // 0),
      cache_read:(.value.cacheReadInputTokens // 0),
      cache_write:(.value.cacheCreationInputTokens // 0)}}) | from_entries' \
    <<<"$RESULT_OBJ" 2>/dev/null || echo null)"
  jq -e . >/dev/null 2>&1 <<<"$USAGE_BY_MODEL" || USAGE_BY_MODEL='null'
  CACHE_TOTALS="$(jq -c '[.[]] | {in:(map(.in)|add // 0), out:(map(.out)|add // 0),
      cache_read:(map(.cache_read)|add // 0), cache_write:(map(.cache_write)|add // 0)}
    | . + {hit_rate:(if (.cache_read + .cache_write + .in) > 0
        then ((.cache_read / (.cache_read + .cache_write + .in)) * 1000 | round / 1000)
        else null end)}' <<<"$USAGE_BY_MODEL" 2>/dev/null || echo null)"
  jq -e . >/dev/null 2>&1 <<<"$CACHE_TOTALS" || CACHE_TOTALS='null'
  # Auto-compact events in the stream (prevention-vs-compaction A/B): each
  # compaction is a system message with subtype compact_boundary.
  COMPACTIONS="$(grep -c '"subtype":[[:space:]]*"compact_boundary"' "$RAW" 2>/dev/null || true)"
  COMPACTIONS="${COMPACTIONS:-0}"

  # go-nullius arm: RAW is the plain-text final report (no stream-json), so
  # the extractions above all came up empty. The binary's stats file is the
  # economics record: token counts per tier (leader = GO_MODEL, scouts =
  # haiku), priced here at USD/Mtok with cache_read = 0.1x and cache_write =
  # 1.25x the input price.
  if [[ "$ARM" == "go-nullius" ]]; then
    cp "$RAW" "$REPORT" 2>/dev/null || true
    GN_STATS="$WT/.nullius/stats-$GN_SESSION.json"
    case "$GO_MODEL" in
      haiku)  GN_LM="claude-haiku-4-5";  GN_IN=1;  GN_OUT=5;;
      sonnet) GN_LM="claude-sonnet-5";   GN_IN=3;  GN_OUT=15;;
      opus)   GN_LM="claude-opus-4-8";   GN_IN=5;  GN_OUT=25;;
      fable)  GN_LM="claude-fable-5";    GN_IN=10; GN_OUT=50;;
      *)      GN_LM="$GO_MODEL";         GN_IN=5;  GN_OUT=25;
              echo "warn: unknown GO_MODEL '$GO_MODEL', pricing as opus-4-8" >&2;;
    esac
    if [[ -r "$GN_STATS" ]]; then
      COST="$(jq --argjson i "$GN_IN" --argjson o "$GN_OUT" '
        (.leader.input_tokens*$i + .leader.output_tokens*$o
         + .leader.cache_read_tokens*($i*0.1) + .leader.cache_creation_tokens*($i*1.25)
         + .scouts.input_tokens*1 + .scouts.output_tokens*5
         + .scouts.cache_read_tokens*0.1 + .scouts.cache_creation_tokens*1.25)
        / 1e6 * 10000 | round / 10000' "$GN_STATS")"
      USAGE_BY_MODEL="$(jq -c --arg lm "$GN_LM" '{
        ($lm): {in:.leader.input_tokens, out:.leader.output_tokens,
                cache_read:.leader.cache_read_tokens, cache_write:.leader.cache_creation_tokens},
        "claude-haiku-4-5-scouts": {in:.scouts.input_tokens, out:.scouts.output_tokens,
                cache_read:.scouts.cache_read_tokens, cache_write:.scouts.cache_creation_tokens}
        }' "$GN_STATS")"
      CACHE_TOTALS="$(jq -c '[.[]] | {in:(map(.in)|add // 0), out:(map(.out)|add // 0),
          cache_read:(map(.cache_read)|add // 0), cache_write:(map(.cache_write)|add // 0)}
        | . + {hit_rate:(if (.cache_read + .cache_write + .in) > 0
            then ((.cache_read / (.cache_read + .cache_write + .in)) * 1000 | round / 1000)
            else null end)}' <<<"$USAGE_BY_MODEL")"
      TURNS="$(jq '.turns' "$GN_STATS")"
      DISPATCH_N="$(jq '.scout_runs' "$GN_STATS")"
      EXPLORER_N="$DISPATCH_N"
      COMPACTIONS="$(jq '.compactions' "$GN_STATS")"
      COST_BY_MODEL="$(jq -n --arg lm "$GN_LM" --argjson c "$COST" '{($lm): $c}')"
    else
      echo "warn: go-nullius stats file missing ($GN_STATS) — cost unrecorded" >&2
    fi
  fi

  # cc-nullius arm: the diet-governor hook keeps its own best-effort telemetry
  # (denies/rewrites/dispatch counts) at $TMPDIR/nullius-stats-<session_id>
  # (diet-governor.mjs: statsFile), keyed by the CLI's own session_id, which
  # RESULT_OBJ carries. Host mode shares the same $TMPDIR as this script;
  # CONTAINER=1 points the container's TMPDIR at the $CTMP bind mount above, so
  # the file lands on the host either way. Telemetry only — never abort the rep
  # over it.
  GOVERNOR_STATS='null'
  if [[ "$ARM" == "cc-nullius" ]]; then
    CC_SESSION="$(jq -r '.session_id // empty' <<<"$RESULT_OBJ" 2>/dev/null || true)"
    if [[ -n "$CC_SESSION" ]]; then
      if [[ "${CONTAINER:-0}" == "1" ]]; then
        CC_STATS_DIR="${CTMP:-${TMPDIR:-/tmp}}"
      else
        CC_STATS_DIR="${TMPDIR:-/tmp}"
      fi
      CC_STATS_FILE="$CC_STATS_DIR/nullius-stats-$CC_SESSION"
      if [[ -r "$CC_STATS_FILE" ]]; then
        GOVERNOR_STATS="$(cat "$CC_STATS_FILE" 2>/dev/null || echo null)"
        jq -e . >/dev/null 2>&1 <<<"$GOVERNOR_STATS" || { GOVERNOR_STATS='null'; echo "warn: cc-nullius governor stats file malformed ($CC_STATS_FILE)" >&2; }
      else
        # The hook names the stats file from its own hook-payload session_id,
        # which is not guaranteed to equal the CLI result's session_id — the
        # two diverge in practice. Fall back to any nullius-stats-* file(s) in
        # the same dir before giving up.
        CC_STATS_GLOB=("$CC_STATS_DIR"/nullius-stats-*)
        if [[ -e "${CC_STATS_GLOB[0]}" ]]; then
          if [[ "${#CC_STATS_GLOB[@]}" -eq 1 ]]; then
            GOVERNOR_STATS="$(cat "${CC_STATS_GLOB[0]}" 2>/dev/null || echo null)"
            jq -e . >/dev/null 2>&1 <<<"$GOVERNOR_STATS" \
              || { GOVERNOR_STATS='null'; echo "warn: cc-nullius governor stats file malformed (${CC_STATS_GLOB[0]})" >&2; }
            [[ "$GOVERNOR_STATS" != null ]] \
              && echo "note: governor stats found under a different session id ($(basename "${CC_STATS_GLOB[0]}"))" >&2
          else
            GOVERNOR_STATS="$(jq -s -e 'reduce .[] as $f ({}; reduce ($f|keys[]) as $k (.;
                   .[$k] = (if ($f[$k]|type) == "number" then ((.[$k] // 0) + $f[$k]) else $f[$k] end)))' \
              "${CC_STATS_GLOB[@]}" 2>/dev/null || echo null)"
            if [[ "$GOVERNOR_STATS" == null ]]; then
              echo "warn: cc-nullius governor stats files malformed (${CC_STATS_GLOB[*]})" >&2
            else
              echo "note: merged ${#CC_STATS_GLOB[@]} governor stats files" >&2
            fi
          fi
        else
          echo "warn: cc-nullius governor stats file missing ($CC_STATS_FILE) — telemetry unrecorded" >&2
        fi
      fi
    else
      echo "warn: cc-nullius session_id not found in result — governor stats unrecorded" >&2
    fi
  fi

  jq -n -c \
    --arg task "$TASK_NAME" --arg arm "$LABEL" --arg stamp "$STAMP" \
    --argjson rep "$rep" --argjson rc "$RC" --argjson wall "$WALL" \
    --argjson cost "$COST" --argjson usage "$USAGE" --argjson turns "$TURNS" \
    --argjson costmodel "$COST_BY_MODEL" \
    --argjson usagemodel "$USAGE_BY_MODEL" --argjson cache "$CACHE_TOTALS" \
    --argjson score "$SCORE" \
    --arg diffstat "$DIFFSTAT" --argjson untracked "$UNTRACKED" \
    --argjson dispatches "${DISPATCH_N:-0}" --argjson explorers "${EXPLORER_N:-0}" \
    --argjson critics "${CRITIC_N:-0}" --argjson builders "${BUILDER_N:-0}" \
    --argjson auditors "${AUDITOR_N:-0}" \
    --argjson craftsmen "${CRAFTSMAN_N:-0}" --argjson ccjudges "${CCJUDGE_N:-0}" \
    --argjson skillinv "${SKILL_N:-0}" \
    --argjson blind "$BLIND" \
    --argjson governorstats "$GOVERNOR_STATS" \
    --argjson compactions "${COMPACTIONS:-0}" \
    --arg compactwin "${CLAUDE_CODE_AUTO_COMPACT_WINDOW:-}" \
    --arg raw "$(basename "$RAW")" \
    '{task:$task, arm:$arm, rep:$rep, stamp:$stamp, exit_code:$rc,
      wall_s:$wall, cost_usd:$cost, cost_by_model:$costmodel,
      usage_by_model:$usagemodel, cache:$cache, usage:$usage, num_turns:$turns,
      byproxy_skill_invocations:$skillinv,
      subagent_dispatches:$dispatches, explorer_dispatches:$explorers,
      critic_dispatches:$critics, builder_dispatches:$builders,
      auditor_dispatches:$auditors,
      craftsman_dispatches:$craftsmen, judge_dispatches:$ccjudges,
      score:$score, blind_disclosure:$blind, governor_stats:$governorstats,
      compactions:$compactions, compact_window:(if $compactwin == "" then null else ($compactwin|(tonumber? // null)) end),
      diffstat:$diffstat, new_files:$untracked, raw:$raw}' \
    | tee -a "$JSONL"

  cleanup; trap - EXIT
done
