---
description: Run the nullius close-out protocol — scout verification record, public-surface regression check, qualified report.
---

Close out the current nullius task:

1. Confirm every checklist item has an explicit disposition (FIXED /
   REFUTED / out-of-mandate). A confirmed defect left as a disclosure is
   a failed run — fix it before closing. A silently dropped suspect is a
   failed close.
2. Dispatch ONE `scout` to produce the close record: **verify** —
   full test suite + build + vet + the project's linters,
   `-race`/equivalent where concurrency was touched, decisive output
   VERBATIM; **surface** — `git diff` against the base revision, every
   removed or signature-changed exported/public symbol listed verbatim.
3. You rule on the record: any failure, and any surface change you did
   not decide by name, goes back into the loop — never rationalized into
   RISKS. A failed close gets a fresh scout after fixes. If the project
   has NO runnable test suite, the close does not silently degrade to
   build+vet: name the missing suite explicitly in RISKS — an unpinned
   fix is how regressions ship, and the record must say the pin is absent.
4. Refresh the terrain cache: write `.nullius/terrain.md` — the current
   lens-target map (≤60 lines, path:symbol lists per lens, quoted
   absences) stamped with `commit: $(git rev-parse HEAD)`. Small write,
   yours; it makes the NEXT session's Turn A a delta-scout instead of a
   full re-map. Skip only if `.nullius/` is inappropriate for the repo
   (then say so in the report).
5. Report to the user: STATUS / FACTS (each with its quoted evidence or
   the scout's verbatim record) / RISKS (with reasons) / UNKNOWN /
   ASSUMED (every self-answered question). Never unqualified success.
6. **Ratification** (interactive sessions): flush the gap ledger — every
   PROVISIONAL choice and material ASSUMED, one line each, with the cost
   of reversing NOW (cheap) vs after later work builds on it (not).
   Silence splits by escape layer, lazy-consensus style: declare that
   layer-2 items (revertible in the diff) STAND UNLESS OBJECTED TO —
   silence ratifies, any later objection evaporates the consent and
   re-enters the loop. Layer-3 items (escaped the worktree: exported
   API, formats, shared state, sends) never ratify by silence — they
   block the close until answered. An overrule re-enters the loop
   before the task is called done.
