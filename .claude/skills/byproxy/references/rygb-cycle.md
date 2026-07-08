# RYGB — the guarded build cycle (v2, batched)

byproxy never lets the builder write code freehand — but it also never pays
a dispatch per phase (benchmark 2 measured that tax: 3× the builder turns,
each re-billing a growing transcript). One unit = ONE fresh builder dispatch
executing the whole disciplined cycle internally, guarded on both sides:
a design red-team before any unit builds, an independent audit after each
unit lands. **The cheapest model reports findings, the expensive model
decides** — that rule is unchanged; what moved is *when* the checks run.

One unit = a file or a coherent group of functions, with an assert-level
test list fixed by the orchestrator at DESIGN time.

## Before any unit: DESIGN red-team (once per task)

You (orchestrator) draft the full design — per unit: test list down to the
asserts, API surface, SCOPE, DONE-WHEN. One explorer (mode: design)
critiques it against the recon FACTS:

- API surface a consumer needs but the design omits (write-only features)
- mandated code no listed test forces (spec'd overreach — it will ship
  untested)
- tests that could trivially pass (404-on-missing-route class)
- collisions with house patterns the recon established

You judge, fix the design, then build. This pass replaces most of what
per-unit Yellow used to catch, at a fraction of the round-trips.

## Per unit: one builder dispatch, full cycle inside

The fresh builder receives a context pack (forwarded FACTS, prior units'
changes, house patterns — never "go read") and runs internally:

```
🔴 write the unit's tests exactly as listed → run → QUOTE the failure
   VERBATIM and confirm it is the right reason (missing behavior, not a
   test bug). wrong reason → fix the test before any implementation.
🟢 minimal implementation → scoped suite green.
   prune: any line no test forces gets deleted.
report: STATUS/FACTS/VERBATIM(red quote + green run + git diff --stat)/
        RISKS/UNKNOWN
```

The right-reason quote is mandatory — a report without it is malformed.
The diff --stat is mandatory — you verify the bounds; never trust a
self-waived limit (observed failure mode).

## After each unit: AUDIT, pipelined

Unit N green → dispatch its audit explorer (mode: blue) in the SAME turn as
unit N+1's builder. The audit re-runs DONE-WHEN and reports findings only:
code no test forces, uncovered branches, bugs/hazards, VERBATIM quotes.

You classify each finding:
- **pure cleanup** (dead code, trivially safe) → fold into the NEXT unit's
  dispatch CONTEXT, or one final FIX dispatch if no units remain
- **new behavior or behavior-changing bugfix** → APPEND to the queue as its
  own unit. never inline mid-flight

The LAST audit doubles as VERIFY: full suite + vet, quoted VERBATIM.

## Escalation (unchanged principle, cheaper mechanics)

Builder escalates on its deterministic triggers with BLOCKED/TRIED/VERBATIM/
NEED. You dispatch a diagnosis explorer if the cause is unclear, then
re-issue the unit to a FRESH builder with the failure quote + DIAG/FIX in
its context pack. At most one in-place redirect (SendMessage) per unit —
past that, the resumed transcript costs more than a fresh start.
2nd escalation on the same unit → your design is wrong: back to DESIGN.

## Judgment stays with you

Design red-team, audits, and diagnosis explorers report facts and VERBATIM
quotes — never verdicts. When a verdict genuinely needs raw context a
~400-token report can't carry, request a bounded VERBATIM package (≤80
lines, per SKILL.md failure handling); if that still isn't enough, ask the
user.

## Queue discipline

- The queue lives in your append-only telegraph log — its only persistence.
  Before a long stretch or possible compaction, append a fresh queue
  snapshot telegraph.
- One entry per finding. Work an entry only as its own unit, after the
  current unit is green.
- Wiring/orchestration code (entrypoints, DI, connection setup) is exempt
  from the cycle — dispatch the builder to write it directly, verified by
  build/vet/manual run, no unit test.
