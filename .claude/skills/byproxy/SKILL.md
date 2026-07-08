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
1. DECOMPOSE   task → questions (parallel) + build units (serial)
2. RECON?      terrain unfamiliar → 1 open recon dispatch. familiar → skip
3. EXPLORE     fan out ALL independent questions in one turn.
               pull project facts from CONVENTIONS.md into dispatch CONTEXT
4. SPEC        write builder dispatch: TASK/SCOPE/CONTEXT/DONE-WHEN/ESCALATE-IF
5. BUILD       drive each unit through RYGB (references/rygb-cycle.md):
               Red (builder) → Yellow (explorer) → Green (builder) →
               Blue (explorer). You judge Yellow/Blue findings; queue
               new-cycle work, never interleave.
6. ON ESCALATE dispatch explorer to diagnose → redirect builder (DIAG/FIX)
               never diagnose by reading code yourself
7. VERIFY      final explorer run: DONE-WHEN checks, report VERBATIM
8. CLOSE       one TGS summary to user: STATUS/FACTS/RISKS/UNKNOWN
```

Step 2 heuristic: skip recon iff the log or the user already establishes the
terrain. When in doubt, recon — one Haiku run buys peripheral vision.

## Dispatching subagents

Spawn via the Agent tool. The agent definitions carry the TGS-embedded system
prompts, the model tier, and the tool fences — you only send TGS dispatch
fields as the prompt.

- Explorer: `subagent_type: byproxy-explorer` (Haiku). Edit/Write are absent
  from its tool fence; Bash is granted so it can run tests/builds, so
  read-only-through-Bash is enforced by its prompt discipline, not the
  platform. Never put a write instruction in an explorer dispatch. State the
  mode in the dispatch: `recon` | `narrow` | `yellow` | `blue`.
- Builder: `subagent_type: byproxy-builder` (Sonnet, write tools). One at a
  time, serial.
- Explorers parallel → send them as multiple Agent calls in ONE turn.
- Send nothing outside TGS fields.

Install note: these agents live in `.claude/agents/`. A project that only
symlinks the skill won't get them — see the README install step.

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
