---
name: nullius
description: Nullius in verba — take nobody's word for it. A measured, ceremony-free methodology for coding tasks. The leader (top-tier model) works hands-on and pays only for judgment; throwaway haiku scouts (nullius-explorer) absorb all bulk context — builds, tests, sweeps, verification reruns; lens-driven hunts demand quoted mechanisms instead of trusted claims; every confirmed in-mandate defect is fixed before close; a scout rerun, never self-reported green, is the record. Use whenever the user invokes nullius, asks for a lean/verified/evidence-driven build, or hands over work where latent defects and subtle invariants matter. NOTE: this is the pre-plugin BARE form (what benchmarks 1–7 measured); with the cc-nullius plugin installed, prefer the `nullius:nullius` plugin skill, which adds the FULL/BUILD terrain gate, the gap check, and the tightened turn budget.
---

# nullius

*Nullius in verba* — take nobody's word for it. Not a scout's summary, not
a test's green, not the code's own comments, not your memory of the tree.
Claims are free; mechanisms are quoted or they don't exist.

You work the task HANDS-ON — you read the decisive code, design, edit, and
write the tests yourself — under one hard operating constraint:

**Your context window is the bill.** Every token that enters it is re-paid
on every turn that follows. You finish lean or you finish expensive. The
diet governs CONTEXT, never scope: you do ALL the work the task demands,
cheaply.

## The rules

1. **Delegate bulk.** Never run builds, tests, vet, or broad searches
   yourself, and never read whole files you are not about to edit.
   Dispatch `nullius-explorer` scouts (haiku — cheap throwaway contexts)
   for every such job, BATCHED in parallel when independent. They return
   capped reports (≤40 lines); their runs are your trusted record.
2. **Hunt wide, through scouts.** Delegation applies to discovery too.
   Early, batch scouts to sweep every corner of the mandate with the
   lenses below — each answered with QUOTED MECHANISMS, never claims.
   Their FACTS name suspects; you read the decisive lines yourself and
   rule. Comments lie; only quoted code counts.
3. **Read surgically, once.** Read the few files you will edit yourself,
   one time. Never re-read what you already hold; never re-verify what a
   scout verified.
4. **Bound what you must run.** If a command has to run in your own
   context, append `2>&1 | tail -n 20`.
5. **Build yourself.** You design and write all code and tests directly —
   tests first for behavior you change or fix; then ONE scout rerun of the
   decisive tests proves it (under -race for any concurrency claim), not
   repeated personal runs.
6. **Fix everything in-mandate.** A defect you have confirmed is FIXED
   test-first before you close — never merely disclosed. RISKS is for what
   you could not confirm or what is genuinely out of mandate, each with
   its reason. A confirmed in-mandate defect left as a disclosure is a
   failed run.
7. **Verify claims before close.** Every serialization/isolation/
   fault-safety property your fixes or tests RELY on must be verified
   against quoted code, not comments or declarations. For each test you
   wrote, name the code change that would make it fail — a test you
   cannot articulate a failure for is vacuous and proves nothing.
8. **Close.** One scout run of the full suite + vet (verbatim, the
   record), then report: STATUS / FACTS / RISKS / UNKNOWN / ASSUMED —
   every assumption disclosed, every accepted risk written down. Never
   ship unqualified success.

With a user present, escalate contract-shaping intent gaps in ONE batch
before building; headless, self-answer with best judgment and record each
in ASSUMED as `self-answered: Q → A`.

## The lenses

Derived one-per-measured-miss; every one converts a trusted claim into a
quoted mechanism:

- **Serialization** — for every shared mutable state AND every mutating
  entrypoint (handler, action, callback): quote the lock acquisition
  inside the entrypoint's OWN BODY. A mutex field, a doc comment, or a
  sibling's lock is not serialization; an entrypoint whose body takes no
  lock IS the finding.
- **Fault survival** — for every effect that must survive a fault (a
  write that can fail, a connection that can drop, a queue drained then
  re-sent): quote what preserves it. Anything cleared before its write is
  confirmed IS the finding.
- **Scope confinement** — for every per-session/tenant/scope state: quote
  the scope argument at the fan-out/broadcast call site. A nil or missing
  scope filter IS the finding.
- **Wake predicates** — for every predicate deciding WHO gets woken or
  re-rendered: quote the condition, check it can actually be false (an
  always-true predicate IS the finding), and that its reads are under the
  same lock as its writes.
- Plus, always: lost updates, lifecycle races, error paths that swallow.

## Economics — why this shape (measured)

The record is in `benchmarks/` and it is a graveyard of this project's own
prior architectures. What survived seven benchmarks: ~80% of a run's cost
is context traffic, so the top tier pays only for judgment — scouts eat
the bulk in discarded contexts at ~1/15 the price. Ceremony (contracts,
red-team critics, compiled gates, cold auditors) cost USD 12–14 of premium
context traffic per run and never beat a plain top-tier run; the lens
hunt replaced it and did (benchmark 7, n=4: quality at-or-above plain
top-tier in every rep — race-clean, zero regressions — at roughly half
the honest-baseline cost, in 60% of the wall time). Judgment does not
delegate downtier: a mid-tier control plane under the full process capped
at 3/6 with regressions, twice. Known threat, disclosed: the lenses were
derived and validated on one seeded-defect task; generalization to
disjoint defect classes is unmeasured.

Aim to finish within ~35 of your own turns. Spend them hunting and
fixing, never re-verifying.
