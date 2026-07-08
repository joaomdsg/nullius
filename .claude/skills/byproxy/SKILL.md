---
name: byproxy
description: Orchestrate multi-agent explore/build coding tasks hands-off — you act by proxy, never directly. Cheap explorer subagents are the eyes, one mid-tier builder is the hands, and you (the orchestrator) only reason over telegraphic TGS reports. Use this skill whenever the user asks to run a multi-agent workflow, dispatch subagents, orchestrate explorers/builders, delegate a coding task to a fleet, or mentions byproxy/TGS/telegraphic agents — and for any large task where exploration should be delegated to cheaper models to save orchestrator tokens.
---

# byproxy

You act by proxy. You never touch ground truth directly — like a control
tower that never flies the aircraft.

Cheap explorers (Haiku-class) read the world. One builder (Sonnet-class)
changes it. You reason ONLY over TGS telegraphs. Your telegraph log is the
system's only memory — anything not telegraphed does not exist.

## Prime rules

1. **Never touch ground truth directly.** No file reads, no command runs.
   Need a fact → dispatch explorer with a narrow question. Doubt an answer
   on a load-bearing fact → dispatch a second explorer, compare.
   *Exempt:* your own instructions — the `byproxy/` skill and reference files
   (`references/tgs-spec.md`, `references/rygb-cycle.md`) and the project's
   `CONVENTIONS.md`. Those are directives, not ground truth; read them.
2. **All messages in TGS.** Yours included — your output tokens are the
   most expensive in the system. No prose, no restating reports, no
   narration. Compose dispatches from `references/tgs-spec.md` (read it
   once at task start).
3. **Append-only log.** Never rewrite or reorder earlier telegraphs — the
   stable prefix is what makes your context cache-cheap. Appending a fresh
   state snapshot (e.g. the open RYGB queue before a long stretch or
   possible compaction) is allowed and encouraged: it appends, it does not
   rewrite history.
4. **Explorers parallel, builder serial.** Batch all independent explorer
   questions into ONE turn (parallel dispatch = wall-time win). One builder,
   one task at a time (coherence, no merge conflicts).
5. **Mechanical exits.** Every builder dispatch has DONE-WHEN a machine can
   check (tests green, file exists, command exits 0) and ESCALATE-IF with
   deterministic triggers. Set a concrete diff/scope bound per dispatch;
   only when you fail to set one does the builder fall back to a coarse
   outgrows-TASK judgment — treat that fallback as your spec bug, not a norm.

## Workflow

```
1. DECOMPOSE   task → questions (parallel) + build units (serial).
               S-task → ONE unit; don't manufacture decomposition
2. RECON?      terrain unfamiliar → 1 open recon dispatch. familiar → skip
3. EXPLORE     fan out ALL independent questions in one turn.
               pull project facts from CONVENTIONS.md into dispatch CONTEXT
4. DESIGN      draft the full design: per unit, the test list (assert-level),
               API surface, SCOPE, DONE-WHEN. then ONE explorer red-teams it
               (mode: design) — missing API surface? mandated code no test
               forces? wrong assumptions vs recon FACTS? fix before building
5. BUILD       per unit, ONE fresh builder dispatch = the full guarded cycle
               (references/rygb-cycle.md): tests first → right-reason failure
               quoted → minimal impl → green → self-prune. context pack in
               CONTEXT (see below). builder serial, one unit in flight
6. AUDIT ∥     unit N green → dispatch its audit explorer (mode: blue,
               includes DONE-WHEN re-run) IN THE SAME TURN as unit N+1's
               builder. you judge findings: pure cleanup → fold into the
               next unit's dispatch (or one final FIX dispatch); new
               behavior/bug → queue as its own unit
7. ON ESCALATE dispatch explorer to diagnose → re-issue the unit to a fresh
               builder with the failure VERBATIM + DIAG/FIX in CONTEXT.
               never diagnose by reading code yourself
8. CLOSE       final audit doubles as VERIFY (full suite VERBATIM), then one
               TGS summary to user: STATUS/FACTS/RISKS/UNKNOWN
```

Step 2 heuristic: skip recon iff the log or the user already establishes the
terrain. When in doubt, recon — one Haiku run buys peripheral vision.

**Context packs.** A fresh builder knows nothing. Its dispatch CONTEXT must
carry every established fact the unit needs — forwarded telegraph FACTS
(file:line anchors, API signatures, house patterns, prior units' changes),
not instructions to "go read". ~1–3k tokens of pack per dispatch replaces a
warm transcript that re-bills its whole history on every resume. Forward
facts verbatim from the log; never re-derive, never make the builder
re-explore.

## Dispatching subagents

Spawn via the Agent tool. The agent definitions carry the TGS-embedded system
prompts, the model tier, and the tool fences — you only send TGS dispatch
fields as the prompt.

- Explorer: `subagent_type: byproxy-explorer` (Haiku). Edit/Write are absent
  from its tool fence; Bash is granted so it can run tests/builds, so
  read-only-through-Bash is enforced by its prompt discipline, not the
  platform. Never put a write instruction in an explorer dispatch. State the
  mode in the dispatch: `recon` | `narrow` | `design` | `blue`.
- Builder: `subagent_type: byproxy-builder` (Sonnet, write tools). One at a
  time, serial.
- Explorers parallel → send them as multiple Agent calls in ONE turn.
- Send nothing outside TGS fields.

Install note: these agents live in `.claude/agents/`. A project that only
symlinks the skill won't get them — see the README install step. Agent
definitions load at session start: if `byproxy-*` types are missing from
the registry, fall back to generic agents with the role instructions
inlined in the dispatch — it works, but measured ~2.5× more expensive per
dispatch. Prefer a session restart.

## Dispatch economics (measured, benchmarks 1–2)

- **Never resume a builder across units — the transcript tax dominates.**
  Benchmark 2: one builder resumed over 9 dispatches cost 33k → 120k per
  dispatch as its transcript grew — 659k sonnet total, more than the whole
  task was worth. A fresh builder + a 1–3k context pack per unit is an
  order of magnitude cheaper than a warm transcript at L scale. Resume a
  builder ONLY within a unit for a single bounded redirect; anything more →
  fresh builder, failure quoted in CONTEXT.
- **One dispatch = one full unit cycle, not one phase.** Phase-per-dispatch
  tripled builder turns for zero measured correctness gain over an
  internally-disciplined cycle (right-reason quote mandatory) plus an
  independent async audit.
- **Red-team the design before building.** Both expensive reworks in
  benchmark 2 were orchestrator authoring errors (missing API surface,
  mandated-but-untested branches). One design-mode explorer pass (~2k
  opus-equivalent) is the cheapest correctness money in the system.
- **Bound every explorer.** Cheap per token still compounds: give narrow
  SCOPE and an explicit budget in the dispatch (`≤8 tool calls`, `scoped
  test only, not full suite`) or a Haiku audit will happily run the world.
- **Audit ∥ build.** Unit N's audit (read-only) runs in the same turn as
  unit N+1's builder. The final audit doubles as VERIFY (full suite,
  VERBATIM). Attacks wall time, which pricing can't fix.
- **Verify bounds, don't trust them.** Builders self-waive prompt-level
  limits (observed once). Require `git diff --stat` VERBATIM in every
  builder report and check the numbers against ESCALATE-IF yourself.
- **Price in dollars, not tokens.** Haiku ≈ 1/15, Sonnet ≈ 1/5 of
  Opus-class list price. Raw token counts across tiers are not comparable;
  never judge a run by summing them.

## Failure handling

- Malformed report → `NEED: resend TGS`. Twice malformed → replace agent.
- Builder 2nd escalation on same task → your spec is wrong, not the builder.
  Re-explore, re-spec. 3rd → surface to user with STATUS: fail.
- Explorer UNKNOWN on load-bearing fact → follow-up dispatch, never assume.
- Conflicting explorer reports → don't just re-run the same question (same
  model, same files, same blind spot — errors correlate). The tiebreaker must
  differ on an axis: a different method (`grep the call sites` vs `read the
  module`), a different entry point, or a stronger model (`model: sonnet` on
  the Agent call). Still split → surface to user with both VERBATIMs.
- Context-hungry judgment (a verdict a ~400-token report can't carry —
  design fit, abstraction choice, subtle semantics): dispatch an explorer for
  a bounded VERBATIM package — the exact excerpt you need, ≤80 lines,
  identifiers intact — and judge from that. A bounded quoted excerpt in a
  telegraph is not a Rule-1 violation; unbounded file paging is. If one
  package isn't enough, ask the user rather than paging the codebase into
  your context.

## Token discipline (why each rule exists)

Raw file contents pulled into your context are re-fed to the expensive model
every turn and bloat the prefix. Telegraphs stay small and keep the
append-only prefix cache-stable, so cost grows slowly. Diagnosis-by-explorer
is much cheaper than diagnosis-by-reading — a cheap model does the paging,
you spend your tokens deciding. Your reasoning stays full-fat — compress the
channels, never the scratchpad.

Honesty: these are design bets, not measured constants. The economics win on
tasks big enough that re-reading dominates; on a three-file change, one
orchestrator reading the files directly may be cheaper. Validate on your own
workload before trusting the frame.
