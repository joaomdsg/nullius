# nullius — rationale

Every rule in `SKILL.md` is a measured failure mode. This file holds the
measurements so the doctrine itself stays operative. **Read a section only
when you are about to break the rule it defends** — absorbing this file as
preamble is exactly the cost the doctrine exists to avoid.

## Why context is the bill, twice

Residency is re-paid on every later turn — cheaply in dollars via cache
reads, expensively in cache writes — and it dilutes attention: long
contexts miss defects that short ones catch. Turns are the other half:
cost ≈ turns × residency. Measured, one run took 57 leader turns against a
plain run's 25 on identical output, which was almost exactly the observed
1.8× leader cost gap.

Open question (ROADMAP #15): the diet governs context but nothing governs
reasoning effort, and turn-batching × high effort concentrates spend. Batch
for attention; treat the dollar saving as unproven until measured.

## Why writes stay with the leader

The delegate-vs-write crossover scales INVERSELY with the leader↔craftsman
output-rate gap:

    crossover_lines ≈ cold-absorption tax ÷ (leader_out − craftsman_out)

It re-stales on every leader-model swap — recompute from the gap, never
reuse a line count. Worked, craftsman = sonnet ($15/M out):

| Leader | Rate | Gap | Crossover |
|---|---|---|---|
| Opus | $26/M | $11/M | ~1,800 lines cold / ~130 lean (MEASURED) |
| fable-5 | $50/M | $35/M | ~560 cold / ~40 lean (EXTRAPOLATED, unmeasured) |
| sonnet | — | none | delegation never wins on write cost, only residency |

A cold craftsman that re-absorbs what the leader already holds erases the
saving: measured, cold-dispatched sonnet cost MORE per output token than
the Opus leader writing the code directly. The ~$0.4 cold-absorption tax
dominates until the build is very large. Published consensus agrees:
delegated reading is the win, delegated writing is the tax.

The governor deliberately does NOT cap write size. Measured 2026-07-19: a
post-generation size-deny double-bills, because the leader has already
spent the output tokens and the craftsman regenerates the same bytes. The
decision belongs in doctrine, before generation.

## Why recursion is a scale tool, not a cost win

True A/B, 2026-07-20, same mandate, fable leader: the nested `nullius-build`
run cost MORE than a solo run ($13.46 vs $11.33). Its worth is elsewhere —
it completes builds a single context cannot, and its mandatory
scout-verified close shipped an actually-clean deliverable where the solo
run shipped a broken build under a false "compiling" self-report. The older
"3.1× cheaper" figure was a fallback artifact from a run where recursion
never engaged; do not cite it.

## Why briefs carry pointers, not depth

The measured failure was value-passing: a leader compressed reachable code
(`reference/`, present in the builder's own tree) into prose verdicts —
"stub grid / stub SMS / stub map" — and the reference-blind builder obeyed a
summary it could not check, shipping a worse port than a solo run under the
same mandate. Prior art converges: Anthropic (workers do their own
retrieval), Manus (filesystem-as-memory, lazy fetch), A2A (pass a contextId,
the worker fetches), Cognition (share context, don't compress it).

Two rules survive regardless: an explicit DEPTH RULE, because agents
mis-judge effort without one (Anthropic); and a scout-verified close, never
self-reported.

## Why the gate must never soften

One softened brownfield hunt shipped the signature defect silently (5/6).
Conversely, full process on lens-hostile greenfield cost +78% and 2.3× wall
clock for identical quality — hence BUILD mode. Greenfield is still where
spec-silent choices concentrate: 7 self-answered ASSUMED on one greenfield
run, which interactively would have been a single user batch.

## Why judgment never delegates downtier

Measured twice: a mid-tier control plane capped at 3/6.

## Why lenses are aimed, not swept

Fault-survival is the only lens that ever catches
clear-before-confirmed-write: 0/4 for open sweeps, and it re-missed the one
time the hunt was softened. Scope confinement ships as a leak when a
downstream filter is confirmed PRESENT instead of the call's scope
argument — 2026-07-24, statesess `broadcastRender(ctx, nil, …)` was missed
because the hunt verified the revs gate rather than the scope arg. Each
fan-out call is its own obligation.

## Why ABSENT beats AMBIGUOUS beats a guess

Mechanically-certain ABSENTs get fixed; vague testimony drowns. The quote
is the value. And a hunter's quote can name a mechanism that exists yet
does not cover the suspect: a wake-predicate REFUTED on a quoted
"buffered chan-1 + hold/pending" shipped the always-true predicate (5/6),
while the same run's behavioral test on CAS beat a wrong hunter clearance.

## Why nothing is dismissed silently

Measured: 74 of 370 rulings were free dismissals. A suspect silently
dropped is the leader's failure; a defect never reported is the hunt's. The
record must distinguish them.

## Why the close is a scout, from clean

A run shipped a 0-byte source file and self-reported green — the build had
never run. A file that exists but is blank is a broken build, not a stub.
Separately, a green-DoD run scored zero after a "cleanup" deleted a public
method that only hidden consumers used: green tests do not cover a surface
change.

## Why the fix carries its test

Rep 2: a 3-line fix that skipped one lifecycle path is how regressions
ship. Hence the tests-first ratchet.

## Why the ledger is written early, and by you

Measured 2026-08-17/18, two auto-compaction drives: the ctx-sentinel
nudged, the model kept serving the task, auto-compaction fired between
turns and the record was lost. An advisory injection does not get the
ledger written — which is why the governor DENIES context-filling calls
past the knee with no fresh ledger, keeping Write/Edit/Bash open so the
ledger can be written without lifting the gate.

Compaction cannot be triggered or shaped by a plugin (upstream #37307 and
#58538, both closed "not planned"; PreCompact can only block, never
rewrite). So nullius keeps the record on disk and speaks first in the fresh
context. Under `bin/nullius` the auto-compact window is 200k, so compaction
fires on its own just past the knee.

Ledger freshness windows differ on purpose: the governor gate uses 30
minutes because it only needs a ledger to EXIST before compaction (a
10-minute window re-gated the leader twice inside one task, measured
2026-08-18); the sentinel keeps 10 minutes so it still nudges for a
refresh.

## Why recitation, not re-reading

Drift is pattern-matching to recent behavior; restating the goal measurably
reduces it, and edge placement is the one robust lost-in-the-middle
counter. A close-time re-read alone is confirmation theater — rechecks
confirm, they rarely correct.
