---
description: Run the nullius close-out protocol — scout verification record, public-surface regression check, qualified report.
---

Close out the current nullius task. This is TWO turns: the close dispatch,
then the report — steps 4-6 share the second message.

1. Confirm every checklist item has an explicit disposition (FIXED /
   REFUTED / out-of-mandate). A confirmed defect left as a disclosure is a
   failed run — fix it before closing. A silently dropped suspect is a
   failed close.

2. Dispatch ONE `scout` for the close record, from CLEAN:
   - **verify** — full test suite + build + vet + the project's linters,
     `-race`/equivalent where concurrency was touched. Failures verbatim;
     each green command gets a one-line tally, not its output.
   - **surface** — `git diff` against the base revision, every removed or
     signature-changed exported/public symbol listed verbatim.
   - **integrity** — any empty/0-byte source file or missing package
     declaration, named explicitly. A file that exists but is blank is a
     broken build, not a stub.

3. You rule on the record: any failure, and any surface change you did not
   decide by name, goes back into the loop — never rationalized into RISKS.
   A failed close gets a fresh scout after fixes. If the project has NO
   runnable test suite, the close does not silently degrade to build+vet:
   name the missing suite in RISKS — an unpinned fix is how regressions
   ship, and the record must say the pin is absent.

4. Refresh the terrain cache: write
   `$(git rev-parse --show-toplevel)/.nullius/terrain.md` — repo root, same
   rule as the ledger, one `.nullius/` per checkout — the current
   lens-target map (≤60 lines, `path:symbol` lists per lens, quoted
   absences) stamped with `commit: $(git rev-parse HEAD)`. Small write,
   yours; it makes the NEXT session's Turn A a delta-scout instead of a
   full re-map. Skip only if `.nullius/` is inappropriate for the repo
   (then say so in the report).

5. Report: STATUS / FACTS (each with its quoted evidence or the scout's
   verbatim record) / RISKS (with reasons) / UNKNOWN / ASSUMED (every
   self-answered question). Never unqualified success.

6. **Ratification** (interactive): flush the gap ledger — every
   PROVISIONAL choice and material ASSUMED, one line each, with the cost of
   reversing NOW (cheap) vs after later work builds on it (not). Silence
   splits by escape layer, lazy-consensus style: declare that layer-2 items
   (revertible in the diff) STAND UNLESS OBJECTED TO — silence ratifies,
   and any later objection evaporates the consent and re-enters the loop.
   Layer-3 items (escaped the worktree: exported API, formats, shared
   state, sends) never ratify by silence — they block the close until
   answered. An overrule re-enters the loop before the task is called done.

Invoke `nullius:compact` in the same message as the report — post-close is
the one point where compaction is near-lossless, and the ledger IS the
summary.
