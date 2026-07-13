# Benchmark 7 — the sonnet control plane refuted twice, and fable-lean found

**Date:** 2026-07-13 · **Task:** `vialite-todo` (6 seeded defects, hidden
oracle, 287-test hidden suite) · **Env:** CONTAINER=1 (pinned image),
JUDGE=1 (blind disclosure judge) · **Directive:** match plain-fable quality
at ⅓ of plain-fable cost.

## TL;DR

Re-tiering the v6 guard layer to a sonnet control plane failed on both
axes, twice (split and merged). What worked was abandoning the ceremony
entirely: **fable-lean** — a hands-on fable-5 that pays for judgment only,
with haiku explorers absorbing all bulk context in throwaway sessions and
lens-driven hunts that demand quoted mechanisms instead of trusted claims.
Confirmed over n=4: **mean $6.23 (36% of plain fable's $17.09), fix-rate
5.75/6 vs plain's 5/6, race-clean and zero regressions in every rep, mean
wall 11.4 min vs 18.** Three of four reps fixed all six defects; the worst
rep tied plain fable's quality at 41% of its cost.

## The baseline and the target

`plain-fable-5-low` (2026-07-11, n=1): **$17.09**, fixed 5/6 (missed
sse-premature-clear silently), race clean, 0 regressions, blind disclosure
5/6, wall 18 min. Target: ≤$5.70 at that quality.

Token anatomy of that run (its `modelUsage`): $10.16 of $17.09 was **cache
reads** — a ~104k context re-read across 98 turns — $3.44 cache writes,
$3.28 output. Cost is context traffic, not thinking. (Fable-5 pricing
back-solves exactly from these rows: $10/M in, $50/M out, $1/M cache-read,
$12.5/M cache-write.)

## Act 1 — v6: sonnet control plane, split (refuted)

v6 moved the orchestrator from fable-5@low to sonnet-5@high, pinned fable
to the one tool-less critic call, added token-diet rules, and fixed the
sandbox (see "Instrument repair" below).

| | cost | fixed | regr | race | blind | wall |
|---|---|---|---|---|---|---|
| v5 fable hands-off (prior) | $19.18 | 4/6 | 0 | dirty | 4 | 33 m |
| **v6 sonnet split** | **$20.56** | **3/6** | **3** | dirty | 5 | 69 m |

More expensive than the fable version it replaced, and worse. Three sinks,
from the per-chain token split: the orchestrator ingested 1.4M tokens of
reports/reads (3× the fable version — soft caps were ignored); a
"batch everything into one dispatch" rule produced an 89-message builder
session whose growing-context re-reads (14.5M cache-read tokens) cost more
than the round-trips it saved; and the sonnet auditor ran 66 messages
where v5's fable auditor did the same job in 21. The three regressions
were all in dispose-lifecycle tests.

## Act 2 — byproxy-nobuilder: the split ablation (split refuted)

Same contracts, critic, gate, audit — but no builder agent: the
orchestrator implements every unit itself. Isolates the orchestrator/
builder split now that both roles share a tier.

| | cost | fixed | regr | race | blind | wall |
|---|---|---|---|---|---|---|
| v6 split | $20.56 | 3/6 | 3 | dirty | 5 | 69 m |
| **v6 merged (nobuilder)** | **$12.65** | 3/6 | **3 (identical tests)** | dirty | 3 | 43 m |

**The split is pure waste at same tier: −$7.91 (38%) with quality
unchanged** — down to the *same three* dispose-test regressions. Both
sonnet configs botched the same `sync.Once`-style dispose fix the same
way, and the same-tier auditor waved it through both times. With working
`-race`, full process discipline, and hard caps, sonnet judgment capped at
3/6 — benchmark 5's conclusion reproduced under the best process we have.
Quality follows the judging tier; process does not substitute.

## Act 3 — fable-lean: pay for judgment only

Design (one paragraph of system prompt, no skill, no ceremony): fable-5
works the task **hands-on** — reads the decisive files once, designs,
edits, writes tests itself — under one hard rule: *your context window is
the bill*. All bulk goes to haiku `byproxy-explorer` agents in throwaway
contexts: builds, tests, vet, broad sweeps, exit-check reruns (the trusted
record). A design nuance the v5 data forced: report-mediated intake can
exceed raw reading (v5's orchestrator took in more report tokens than
plain fable read raw), so the leader still reads the few files it will
edit directly — everything else arrives as capped reports.

Four iterations, each converting a measured miss into a hunting lens:

| rep | change | cost | fixed | regr | race | blind | wall |
|---|---|---|---|---|---|---|---|
| v1 | base diet | $7.22 | 3/6 | 0 | dirty | 3 | 13 m |
| v2 | + delegated hunt lenses, fix-now rule | $8.30 | 4/6 | 0 | dirty | 4 | 13 m |
| v3 | + quoted-mechanism lenses | $8.20 | 5/6 | 0 | dirty | 5 | 14 m |
| v4 | + wake-predicate lens | **$5.70** | **6/6** | 0 | **clean** | **6** | 10.5 m |

The lens arc is the finding. Every miss was a **trusted-claim failure**,
and each became a lens demanding the quoted mechanism instead:

- v1 starved the hunt (found 3 seeded + 3 adjacent bonus defects) → v2
  delegates discovery itself to lensed haiku sweeps. v2 fixed
  sse-premature-clear — the fault-path defect plain fable never fixed.
- v2 still missed action (trusted a *comment* claiming `actionMu`
  serializes handlers — the seeded bug is that `runAction` never takes it)
  and statesess (wrote a data-isolation test that passed while the seeded
  leak was in the wake fan-out — a vacuous test). → v3 lenses: quote the
  lock acquisition *inside the entrypoint's body* (a comment, a field, or
  a sibling's lock is not serialization — the un-taken lock IS the
  finding); quote the scope argument *at the fan-out call site* (nil scope
  IS the finding); plus a close-gate: every relied-upon claim verified
  against quoted code, every test names the change that would fail it.
- v3 fixed both, then dropped the *easiest* defect (the always-true
  `subscribed()` predicate) → v4 lens: every wake/notify predicate must be
  quoted and falsifiable, reads under the same lock as writes.

## Confirmation — locked v4 config, n=4

| rep | cost | fixed | race | regr | blind | hidden suite |
|---|---|---|---|---|---|---|
| 1 | $5.70 | 6/6 | clean | 0 | 6 | 287/287 |
| 2 | $6.06 | 6/6 | clean | 0 | 6 | 287/287 |
| 3 | $6.17 | 6/6 | clean | 0 | 6 | 287/287 |
| 4 | $6.99 | 5/6 | clean | 0 | 5 | 286/287 |
| **mean** | **$6.23** | **5.75** | 4/4 | 0/4 | 5.75 | |

Rep 4's miss: statesess — caught and disclosed, fix didn't land. Every rep
race-clean and regression-free; plain fable's own rep left 2 hidden tests
failing (285/287). Verdict against the directive: **quality at-or-above
plain fable in every rep; cost 36% on the mean, 33% best-rep** — the ⅓
line is touched, not yet owned.

Craft check (rep-1 diff read): fix comments document constraints, not
actions ("taken before holdNotify so the unlock (LIFO) runs after the
release fires the wake"); the dispose fix articulates *why `sync.Once`
would be wrong* (Shutdown's signal-then-dispose sequencing) — the exact
fix both sonnet runs turned into regressions; a monotonic ID counter
avoids the ID-reuse hazard plain fable shipped (`max+1` after delete); 7
files / 113 insertions vs plain fable's 14-file blast radius.

## Instrument repair (affects every arm)

The bench image (`node:22-bookworm-slim`) had **no C compiler and
CGO_ENABLED=0**: `go test -race` could not run *inside the sandbox* for
any arm, ever — a v5 report says verbatim "-race was not run: this sandbox
has no C compiler" — while host-side scoring raced fine. The Dockerfile
now ships gcc with CGO_ENABLED=1. Every pattern-5 "forcing test under
-race" instruction before this fix was silently vacuous in-container.

## Threats to validity

1. **Lens overfitting is the big one.** The four lenses were derived from
   misses on this task's six seeded defects, then validated on the same
   task. The lens *classes* (unserialized entrypoints, clear-before-write,
   unscoped fan-out, unfalsifiable predicates) are generic concurrency/
   fault patterns, but their generalization is unmeasured. A different
   task with disjoint defect classes is the required next benchmark.
2. **Baseline asymmetry:** plain fable's $17.09/5-of-6 predates the gcc
   fix — it could not run `-race` in-sandbox; the lean reps could. A
   plain-fable rerun on the repaired image is owed before the headline
   ratio is treated as settled.
3. **n:** baseline n=1; lean confirmation n=4; sonnet refutations n=1
   each. The sonnet quality result matches benchmark 5 (n=5), so it rests
   on more than one row; the lean numbers rest on four.
4. **Iteration selection:** v1→v4 was tuned against live scores; the n=4
   confirmation of the *locked* config is the guard against having
   selected a lucky row, but within-task tuning remains.

## What this changes

- **Split:** retired at same tier — measured pure waste.
- **Sonnet control plane:** refuted twice with the strongest process
  available; the judging tier is not a tuning knob.
- **Ceremony:** contracts + critic + gate + auditor cost $12–14 of
  premium-context traffic and never beat plain fable on this task. The
  lean shape — judgment hands-on, bulk delegated, lenses instead of
  gates, fix-now instead of disclosure — beat plain fable on every
  measured axis at once: cost (0.36×), wall (0.63×), fix rate, race
  hygiene, suite health, disclosure.
- Whether fable-lean becomes byproxy v7 is a naming decision; the
  measured recommendation is unambiguous.

Raw rows: `harness/results/results.jsonl` (arms `byproxy-v6-sonnet-high`,
`byproxy-nobuilder-v6-sonnet-high`, `fable-lean-*`, baseline
`plain-fable-5-low`). Reproduce: `CONTAINER=1 JUDGE=1 ./run.sh
tasks/vialite-todo fable-lean --reps 3` (config pinned in run.sh's
`fable-lean` arm; LEAN_MODEL/LEAN_EFFORT to override).
