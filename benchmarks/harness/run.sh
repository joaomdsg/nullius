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
  nullius-rev1)      LABEL="${LABEL:-nullius-rev1-${LEAN_MODEL#claude-}-$LEAN_EFFORT}";;
  nullius-rev2)      LABEL="${LABEL:-nullius-rev2-${LEAN_MODEL#claude-}-$LEAN_EFFORT}";;
  cc-nullius)        LABEL="${LABEL:-cc-nullius-${LEAN_MODEL#claude-}-$LEAN_EFFORT}";;
  fable-lean)        LABEL="${LABEL:-fable-lean-${LEAN_MODEL#claude-}-$LEAN_EFFORT}";;
  byproxy)           LABEL="${LABEL:-byproxy-${ORCH_MODEL#claude-}-$ORCH_EFFORT}";;
  byproxy-noaudit)   LABEL="${LABEL:-byproxy-noaudit-${ORCH_MODEL#claude-}-$ORCH_EFFORT}";;
  byproxy-nobuilder) LABEL="${LABEL:-byproxy-nobuilder-${ORCH_MODEL#claude-}-$ORCH_EFFORT}";;
  go-nullius)        LABEL="${LABEL:-go-nullius-${GO_MODEL#claude-}-$GO_EFFORT}";;
  plain)             LABEL="${LABEL:-plain-${PLAIN_MODEL#claude-}${PLAIN_EFFORT:+-$PLAIN_EFFORT}}";;
  plain+report)      LABEL="${LABEL:-plain-report-${PLAIN_MODEL#claude-}${PLAIN_EFFORT:+-$PLAIN_EFFORT}}";;
  *) echo "arm must be nullius|nullius-rev1|nullius-rev2|cc-nullius|fable-lean|byproxy|byproxy-noaudit|byproxy-nobuilder|plain|plain+report" >&2; exit 2;;
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
  elif [[ "$ARM" == "nullius-rev1" ]]; then
    # Slim ablation of nullius (benchmark 9): same methodology, three levers
    # aimed at the measured residual cost — leader cache re-reads scale as
    # resident_context x turns. (a) trimmed prompt (~45% fewer methodology
    # tokens resident every turn); (b) fan-in hunt: ONE batch of <=3
    # explorers with <=25-line reports instead of an open-ended hunter
    # fleet; (c) ranged reads + hard turn-batching pressure (~20 turns) and
    # a BACKGROUNDED closing scout overlapped with report drafting.
    mkdir -p "$WT/.claude/agents"
    cp "$ROOT_DIR/.claude/agents/nullius-explorer.md" "$WT/.claude/agents/"
    CLAUDE_ARGS+=(--model "$LEAN_MODEL" --effort "$LEAN_EFFORT")
    CLAUDE_ARGS+=(--append-system-prompt "You are a senior engineer working this task HANDS-ON. Your context window is the bill: every token that enters it is re-paid on every later turn, and every TURN re-pays the whole context — so batch aggressively and finish in few turns (aim under ~20 of your own turns). The diet governs CONTEXT, never scope: you do ALL the work the task demands, cheaply. Rules, non-negotiable: (1) DELEGATE BULK: never run builds, tests, linters, or broad searches yourself — dispatch nullius-explorer subagents (Agent tool; cheap throwaway contexts) for every such job; their capped reports are your trusted record. (2) HUNT ONCE, WIDE: discovery is ONE parallel batch of AT MOST 3 explorers in your first turn, together sweeping every corner of the mandate. The standing question for every sweep: for each property the code RELIES on to be correct, quote the mechanism that enforces it — a comment, a name, a field, or a sibling function that enforces it elsewhere is NOT a mechanism, and a relied-on property with no quotable enforcement IS the finding. Apply it to whatever invariants the mandate's code actually has, e.g.: exclusive access to shared mutable state (quote the guard inside the mutating entrypoint's OWN body); effects that must survive a fault or retry (quote what preserves or replays them — anything discarded before its effect is confirmed IS the finding); data confined to a scope, tenant, session, or permission (quote the filter at the crossing point); resources that must be released or bounded (quote the release on EVERY path, including error paths); conditions deciding who is woken, retried, rendered, or skipped (quote the condition and check it can actually take both values); boundaries and edges (empty, zero, max, duplicate, concurrent, out-of-order); and error paths that swallow, mask, or half-apply. Cap each explorer's report at 25 lines: suspects as file:line plus the decisive quoted lines, zero narration. Comments lie; only quoted code counts — you read the decisive lines yourself and rule. (3) READ RANGED, ONCE: read only files you will edit, only ONCE, and only the decisive line ranges (use offset/limit) — never a whole file over ~150 lines, never re-read what you hold, never re-verify what an explorer verified. (4) Own commands bounded: append '2>&1 | tail -n 20'. (5) BUILD YOURSELF, BATCHED: design and write all code and tests directly — tests first for behavior you change; group independent edits into the SAME turn; then ONE explorer rerun of the decisive tests proves the whole batch (under the toolchain's race/sanitizer flags for any concurrency claim, when available), not per-edit runs. (6) FIX EVERYTHING IN-MANDATE: a defect you have confirmed is FIXED test-first before you close — never merely disclosed; RISKS is only for what you could not confirm or what is out of mandate, each with its reason. (7) VERIFY BEFORE CLOSE: every invariant your fixes or tests rely on must be backed by quoted code, not comments; for each test you wrote, name the code change that would make it fail. (8) CLOSE OVERLAPPED: immediately after your final edit, dispatch ONE explorer to run the full suite plus the project's static checks with run_in_background: true, draft your report while it runs, and finalize only with its verbatim output as the record — never self-reported green. This is a headless run with no user: never call AskUserQuestion; make the call and record it under ASSUMED.")
    RUN_PROMPT="$PROMPT$FULL_RISKS"
  elif [[ "$ARM" == "nullius-rev2" ]]; then
    # rev2 = rev1 + fan-in hunt: hunters are UNCAPPED in number (6-10
    # orthogonal sweeps) because their reports never enter the leader's
    # context — each writes full findings to /tmp/nullius-findings/ and
    # returns a one-line receipt; a sink explorer merges them into a
    # deduplicated INDEX (mechanical, no ranking — judgment never
    # delegates downtier); the leader ranged-reads only the decisive
    # findings entries. Trades one serial hop of walltime for recall at
    # near-constant leader residency. Findings live outside the worktree
    # so the scored diff stays clean.
    mkdir -p "$WT/.claude/agents"
    cp "$ROOT_DIR/.claude/agents/nullius-explorer.md" "$WT/.claude/agents/"
    cp "$ROOT_DIR/.claude/agents/nullius-hunter.md" "$WT/.claude/agents/"
    # Host mode shares /tmp across reps; stale findings from a prior rep
    # would contaminate the sink. Container mode gets a fresh /tmp anyway.
    rm -rf /tmp/nullius-findings
    CLAUDE_ARGS+=(--model "$LEAN_MODEL" --effort "$LEAN_EFFORT")
    CLAUDE_ARGS+=(--append-system-prompt "You are a senior engineer working this task HANDS-ON. Your context window is the bill: every token that enters it is re-paid on every later turn, and every TURN re-pays the whole context — so batch aggressively and finish in few turns (aim under ~20 of your own turns). The diet governs CONTEXT, never scope: you do ALL the work the task demands, cheaply. Rules, non-negotiable: (1) DELEGATE BULK: never run builds, tests, linters, or broad searches yourself — dispatch nullius-explorer subagents (Agent tool; cheap throwaway contexts) for every such job; their capped reports are your trusted record. (2) HUNT ONCE, WIDE, FAN-IN: discovery is ONE parallel batch of 6-10 nullius-hunter subagents in your first turn, each assigned ONE orthogonal slice of the mandate (by subsystem, by entrypoint family, by invariant class — cover EVERY corner). Give each hunter the standing question: for each property the slice's code RELIES on to be correct, quote the mechanism that enforces it — a comment, a name, a field, or a sibling function that enforces it elsewhere is NOT a mechanism, and a relied-on property with no quotable enforcement IS the finding; sweep exclusive access to shared mutable state, effects that must survive a fault or retry, data confined to a scope/tenant/session, resources that must be released on EVERY path, conditions deciding who is woken/retried/rendered/skipped (can they take both values?), boundaries and edges (empty, zero, max, duplicate, concurrent, out-of-order), and error paths that swallow, mask, or half-apply. Each hunter writes its FULL findings to its own file under /tmp/nullius-findings/ and returns a one-line receipt. When all receipts are in, dispatch ONE nullius-explorer as SINK to read every file in /tmp/nullius-findings/ and return a deduplicated INDEX, ≤40 lines, one line per unique suspect: file:line — relied-on property — findings file holding the quote. The sink merges and dedupes ONLY — no ranking, no filtering, no verdicts; judgment is yours alone. You then Read the decisive findings entries (ranged) and the decisive source lines yourself, and rule. Comments lie; only quoted code counts. (3) READ RANGED, ONCE: read only files you will edit (plus findings entries the index flags), only ONCE, and only the decisive line ranges (use offset/limit) — never a whole source file over ~150 lines, never re-read what you hold, never re-verify what a scout verified. (4) Own commands bounded: append '2>&1 | tail -n 20'. (5) BUILD YOURSELF, BATCHED: design and write all code and tests directly — tests first for behavior you change; group independent edits into the SAME turn; then ONE explorer rerun of the decisive tests proves the whole batch (under the toolchain's race/sanitizer flags for any concurrency claim, when available), not per-edit runs. (6) FIX EVERYTHING IN-MANDATE: a defect you have confirmed is FIXED test-first before you close — never merely disclosed; RISKS is only for what you could not confirm or what is out of mandate, each with its reason. (7) VERIFY BEFORE CLOSE: every invariant your fixes or tests rely on must be backed by quoted code, not comments; for each test you wrote, name the code change that would make it fail. (8) CLOSE OVERLAPPED: immediately after your final edit, dispatch ONE explorer to run the full suite plus the project's static checks with run_in_background: true, draft your report while it runs, and finalize only with its verbatim output as the record — never self-reported green. This is a headless run with no user: never call AskUserQuestion; make the call and record it under ASSUMED.")
    RUN_PROMPT="$PROMPT$FULL_RISKS"
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
    mkdir -p "$WT/.claude/hooks"
    cp -r "$ROOT_DIR/cc-nullius/agents" "$WT/.claude/agents"
    cp "$ROOT_DIR/cc-nullius/hooks/diet-governor.mjs" "$WT/.claude/hooks/"
    cat > "$WT/.claude/nullius-settings.json" <<'EOF'
{
  "hooks": {
    "PreToolUse": [
      { "matcher": "Read|Grep|Glob|WebFetch|WebSearch|Bash|Edit|Write|Agent|Task|mcp__.*",
        "hooks": [ { "type": "command",
          "command": "node \"$CLAUDE_PROJECT_DIR/.claude/hooks/diet-governor.mjs\"" } ] }
    ]
  }
}
EOF
    CLAUDE_ARGS+=(--model "$LEAN_MODEL" --effort "$LEAN_EFFORT" --settings ".claude/nullius-settings.json")
    CC_SKILL_BODY="$(awk 'f{print} /^---[[:space:]]*$/{c++; if(c==2) f=1}' "$ROOT_DIR/cc-nullius/skills/nullius/SKILL.md")"
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
    echo "arm must be nullius|nullius-rev1|nullius-rev2|cc-nullius|fable-lean|byproxy|byproxy-noaudit|byproxy-nobuilder|plain|plain+report" >&2; exit 2
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
      ${CLAUDE_CODE_AUTO_COMPACT_WINDOW:+-e CLAUDE_CODE_AUTO_COMPACT_WINDOW="$CLAUDE_CODE_AUTO_COMPACT_WINDOW"} \
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
      score:$score, blind_disclosure:$blind,
      compactions:$compactions, compact_window:(if $compactwin == "" then null else ($compactwin|(tonumber? // null)) end),
      diffstat:$diffstat, new_files:$untracked, raw:$raw}' \
    | tee -a "$JSONL"

  cleanup; trap - EXIT
done
