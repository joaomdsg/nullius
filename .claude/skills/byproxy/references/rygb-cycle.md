# RYGB — the builder's discipline (orchestrator-driven)

byproxy never lets the builder write code freehand. New behavior is built one
logical unit at a time through **Red → Yellow → Green → Blue**. You (the
orchestrator) drive the loop; the builder writes, explorers observe, you
judge. This is byproxy's core rule applied to TDD: **the cheapest model
reports findings, the expensive model decides.**

One cycle = one unit (a file, or a coherent group of functions). Run all four
phases before the next unit. Maintain a **queue** in your telegraph log for
work that surfaces mid-cycle — never interleave it.

## The loop

```
🔴 RED     builder dispatch → write failing test(s) for the unit.
           DONE-WHEN: new test present, fails at compile/assertion for the
           stated reason (not a setup bug).

🟡 YELLOW  explorer dispatch (mode: yellow) → critique the test. Findings only:
           right-reason failure? trivial-pass possible? missing critical edge
           cases? structural issues?
           YOU judge. Test genuinely wrong → redirect builder (DIAG/FIX) to
           amend the test, re-run Yellow. Clean → proceed.

🟢 GREEN   builder dispatch → minimal implementation to pass. Nothing the test
           doesn't force.
           DONE-WHEN: scoped suite green.

🔵 BLUE    explorer dispatch (mode: blue) → audit coverage + correctness.
           Findings only: code not required by tests? uncovered branches?
           bugs / leaks / unhandled edges?
           YOU classify each finding:
             • pure cleanup (dead code, trivially safe) → redirect builder to
               remove it, re-run scoped suite.
             • needs a new Red→Green cycle (uncovered behavior worth keeping,
               or a bug whose fix changes observable behavior) → APPEND to the
               queue as its own unit. Do NOT fix inline.
           Cycle closes with the suite green.
```

## Judgment stays with you

Yellow and Blue explorers report facts and quote code/output VERBATIM. They
do NOT decide whether a gap "matters," whether an edge case is "critical
enough," or whether code should be removed. Those are your calls — made from
the telegraphs. When a verdict genuinely needs raw context a ~400-token
report can't carry, request a bounded VERBATIM package (the exact excerpt,
≤80 lines, per SKILL.md failure handling) and judge from that; if that still
isn't enough, ask the user.

## Queue discipline

- The queue lives in your append-only telegraph log — that is its only
  persistence. If a session may span compaction, periodically emit the open
  queue as an explicit telegraph so it survives in the visible prefix.
- One entry per finding. Work an entry only after the current cycle is green.
- Wiring/orchestration code (entrypoints, DI, connection setup) is exempt
  from the cycle — dispatch the builder to write it directly, verified by
  build/vet/manual run, no unit test.
