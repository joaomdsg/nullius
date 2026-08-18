---
description: Record the session state to .nullius/ledger.md. Invoke it yourself; never hand the user a /compact line.
---

Compaction is lossy and a plugin cannot shape the summary. So nullius does
not rely on it: write the record to disk first, and the `compact-reinject`
SessionStart hook reads it back into the fresh context automatically —
including after an *auto*-compaction you never asked for.

Run this when the ctx-sentinel fires, before any compaction you can see
coming, and at close. It is cheap; a missing ledger is not.

1. **Do not scout, do not re-read.** The ledger is written from what you
   already hold. If you don't know something, that is an UNKNOWN line, not
   a dispatch.

2. **Write `$(git rev-parse --show-toplevel)/.nullius/ledger.md`** (create
   `.nullius/` if absent), ~120 lines. **The repo ROOT, never the cwd**: the
   hooks walk up to find the ledger and take the FIRST one they hit, so a
   ledger written in a subdirectory shadows the real record for every session
   started at or below it. No repo? Then the cwd is the only home there is.
   max, in this order. Omit a section only if it is genuinely empty:

   ```
   # nullius ledger
   commit: <git rev-parse HEAD>   updated: <date -Is>

   ## MANDATE
   The task in the user's terms, plus every boundary ruled out of mandate.

   ## RULED
   One line per settled item: FIXED / REFUTED / OUT-OF-MANDATE, each with
   the deciding mechanism and its path:line. This is the part compaction
   destroys and the part that is expensive to re-derive.

   ## UNRULED
   Every checklist line still open — the live worklist for the next
   context. If the hunt is mid-flight, say which lens and which targets.

   ## FACTS
   Quoted mechanisms with path:line that later reasoning depends on.
   Verified testimony only; it must be safe not to re-scout these.

   ## VERIFICATION
   The last scout record verbatim-ish: suite/build/vet/linter results and
   when. Never your own recollection of green.

   ## RISKS / UNKNOWN / ASSUMED
   As in the close report. Every self-answered question stays visible.

   ## NEXT
   The first two or three concrete actions, in order.
   ```

3. **Verify the write**: `wc -l "$(git rev-parse --show-toplevel)/.nullius/ledger.md"`
   — and confirm the path printed is the root, not a subdirectory. Confirm the commit
   stamp matches `git rev-parse HEAD`. A stale stamp makes every path:line
   in the file suspect.

4. **Do not ask the user to do anything.** You invoke this skill yourself —
   `Skill(skill: "nullius:compact")` — the moment the ctx-sentinel fires;
   waiting for the user to type a line is the failure mode this replaces.
   Compaction itself still cannot be triggered by a plugin, the model, or a
   hook (upstream #37307/#58538), but it does not need to be: `bin/nullius`
   launches with `CLAUDE_CODE_AUTO_COMPACT_WINDOW=200000`, so compaction
   fires on its own shortly past the knee and `compact-reinject` restores
   this ledger. Nothing is copy-pasted.

   Only if the session is NOT under that launcher (the window is still the
   platform's ~967k) is the manual line worth offering, once:

   ```
   /compact preserve the nullius ledger sections (MANDATE/RULED/UNRULED/FACTS/VERIFICATION/RISKS/UNKNOWN/ASSUMED/NEXT) verbatim; drop scout reports, file dumps, tool output and edit churn.
   ```

5. **Say what the ledger does not carry.** One line: what you knew that
   did not survive the distillation, so the user can decide whether to
   answer it now rather than after the context is gone.

Never compact mid-hunt by choice — finish the lens sweep first. But if
compaction is being forced on you (the ceiling, not a preference), write
the ledger anyway: an UNRULED list is worth far more than a lost one.
