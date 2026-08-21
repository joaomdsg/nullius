---
description: Run the two-turn nullius hunt (terrain, then targeted lenses) and build the ruled checklist.
---

Run the nullius hunt phase over: $ARGUMENTS (if empty, the current task's
mandate). This is TWO of your turns, not six — steps 2-4 share one message.

1. **Turn A — terrain**: if `.nullius/terrain.md` exists, dispatch ONE
   scout to validate it first — `git diff --stat <stamped-commit>..HEAD`
   against the map's recorded commit; unchanged files keep their entries,
   only the drift is re-scouted. A stale or absent map means a full map:
   dispatch 2-3 `scout` agents in ONE parallel message to map the mandate
   precisely — every mutating entrypoint, shared mutable state,
   fan-out/broadcast site, queue/buffer/retry state, background sweep/TTL,
   lock, and error path. Output: named target lists (`path:symbol`), not
   prose; for every lens with no targets, the QUOTED basis for the
   absence, never a bare "none".

**Steps 2, 3 and 4 go out in a SINGLE message**: the gate ruling, the
hunter dispatches, and the gap questions last.

2. **THE GATE — rule on the terrain and quote the ruling in your report**:
   FULL mode if any lens has targets, any pre-existing code is in scope, or
   the terrain is ambiguous (doubt → FULL; a softened brownfield hunt is
   the measured 5/6 failure). BUILD mode only on quoted absences — pure
   greenfield, no lens terrain: run the gap check, skip the lens turn and
   the checklist, build hands-on under the diet, re-map your OWN new code
   when done (hunt any terrain the build created), then `/nullius:close`.

3. **Gap check** (interactive only): ONE batch, cap ~4 — each question
   carries the terrain finding that raised it, why neither code nor
   doctrine settles it, and your recommendation (missing any → not a gap;
   rule it, record ASSUMED). `AskUserQuestion` goes LAST in the message, so
   the detached dispatches never wait on intent — only your rulings do.
   Classify each choice by escape analysis, first hit wins: pure read → no
   decision; undo artifact (one cheap command restores prior state) →
   proceed PROVISIONAL on your recommendation; effect escapes the worktree
   (exported API, storage/wire format, shared DB, remote refs, sends,
   spends, dependency lock-in) → BLOCK on the answer; unclassifiable →
   block. Intent binds; factual claims inside answers are verified like any
   testimony. A scope-changing answer = mandate shift: delta Turn A,
   re-rule the gate. Later gaps join a gap ledger (ID · item · provisional
   ruling · escape layer), recited at the tail of every checklist update —
   no one-question turns — and flushed at the close ratification. Headless:
   self-answer into ASSUMED.

4. **Turn B — lenses** (FULL mode): dispatch `lens-hunter` agents in ONE
   parallel message, one per lens, each carrying the exact targets Turn A
   named for it and the V| grammar. Hunters write their full findings to a
   uniquely-named file under `/tmp/nullius-findings/` and return a ONE-LINE
   receipt, so N hunters cost N lines and you read only the slices you rule
   on. **If a hunter falls back to returning verdicts inline** (its receipt
   says `INLINE — write failed`), that is when the aggregate matters: cap the
   remaining dispatches at ⌊120/N⌋ lines each so the fan-in stays ≤120. Terrain sharpens aim, never coverage:
   a core lens (serialization, fault-survival, scope-confinement,
   wake-predicates, lost-updates, lifecycle-races, swallowed-errors,
   resource-release) runs whenever its terrain exists, and fault-survival
   runs regardless. Terrain may add lenses; it never deletes one.

5. Merge the verdicts into a checklist — ABSENT (quoted, mechanically
   certain) first, then decidable AMBIGUOUS. Cap at 40; note overflow
   explicitly. Record it with the todo tools and restate the open items.

6. Rule the WHOLE checklist in ONE turn: read the decisive lines yourself
   (≤40 lines and one Read per item — if you need more, the hunter's quote
   was insufficient, so re-dispatch rather than browse), then rule
   PRESENT/defect/out-of-mandate. Every item gets an explicit disposition;
   none may be silently dropped. An out-of-mandate ruling must quote the
   mandate text that excludes it. A REFUTED on a core-lens suspect never
   rests on hunter testimony alone: quote the protecting mechanism from
   lines YOU read, or pin the property with a behavioral test.

**Never fix an UNRULED item — but ruling and fixing may share a turn.**
Batch the first fixes into the same message as the last rulings.
