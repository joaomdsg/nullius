# Benchmark 8 — the effort ladder, and craft parity once the judge can see the tests

**Date:** 2026-07-14 · **Task:** `vialite-todo` (6 seeded defects, hidden
oracle, 287-test hidden suite) · **Env:** CONTAINER=1, JUDGE=1 · **Model:**
`claude-fable-5` throughout (methodology, not tier, is the variable).

## TL;DR

Ran plain and nullius at both low and high effort on the repaired harness.
Two results. **(1) The efficient cell is nullius-low:** it beats plain-low
on both cost and quality, and *matches plain-HIGH's quality at 26% of its
cost*. High effort is wasted on nullius (doubles cost for the identical
6/6 outcome); it rescues plain (4.33→6/6) but at ~$23. **(2) Once a
diff-capture bug was fixed so the blind quality judge could actually see
the agents' new test files, code craft is a statistical tie** — all arms
~7–8/10 overall. nullius's ~half-cost advantage is context discipline, not
corner-cutting.

## The 2×2 (all fable-5, repaired harness)

| | **plain** | **nullius** |
|---|---|---|
| **low** | $12.36 · 4.33/6 · 1/3 race-clean (n=3 baseline) | **$6.17 · 6/6 · clean · 7 tests · 46 turns** |
| **high** | $23.34 · 6/6 · clean · 11 tests · 104 turns | $12.25 · 6/6 · clean · 14 tests · 41 turns |

plain-low is the adopted n=3 post-gcc baseline mean (see the 2026-07-13
postscript); the other three cells are single fixed-harness reps run today.
nullius-low's $6.17/6-of-6 sits on the v4 confirmation mean ($6.23/5.75,
n=4), so it is a consistent draw, not a lucky one.

Three findings:

1. **nullius-low dominates plain-low** — half the cost ($6.17 vs $12.36)
   *and* better quality (6/6 vs 4.33/6, race-clean vs 1/3). The core
   headline, reconfirmed on a fresh fixed-harness rep.
2. **nullius-low matches plain-HIGH quality at 26% of its cost** — both
   6/6, race-clean, blind 6/6; $6.17 vs $23.34. nullius at *low* effort
   equals plain at *high* effort for a quarter of the price. The strongest
   single comparison in the campaign.
3. **Effort has opposite value per arm.** For nullius, low→high doubles
   cost ($6.17→$12.25) for the *same* 6/6 race-clean outcome (only more
   tests: 7→14) — wasted spend on this task. For plain, high effort buys
   the fixes low effort misses (4.33→6/6) but costs $23. The turn counts
   show the mechanism: nullius stays at ~41–46 turns (haiku explorers
   absorb the bulk) while plain-high runs 104.

## Craft: blind SWE-quality judge, 3 votes/diff (0–10)

| diff | root-cause | tests | minimality | idiom | **overall** |
|---|---|---|---|---|---|
| plain-high | 7.7 | 8.0 | 6.7 | 8.0 | 7.3 |
| nullius-high | 8.0 | 8.0 | 7.0 | 8.0 | 7.0 |
| nullius-low | 8.3 | 8.0 | 7.3 | 8.0 | **8.0** |

Every gap is ≤1 point — inside the judge's own ≥2-point trust threshold.
**Craft is a tie.** nullius-low edges highest and most consistent (overall
votes `8,8,8`), but treat that as noise-level. The honest claim: nullius
produces code of equal craft to plain; the cost win is not paid for in
quality.

## Instrument repair — the diff-capture bug (affects every prior craft judgment)

`run.sh` captured each rep's change set with plain `git diff`, which
**omits untracked files**. Agents that wrote their tests in *new*
`*_test.go` files therefore had those tests silently dropped from the diff
fed to both diff-based judges (quality + blind disclosure). The symptom: an
earlier 21-vote craft run scored the `tests` dimension **~1/10 for every
arm**, dragging `overall` to ~4 — and every rationale literally said "adds
zero tests" despite test-first runs. It was a measurement artifact, not a
quality signal.

Fix: stage everything, then diff with the harness-injected `.claude/`
excluded —
`git add -A && git diff --cached -- . ':(exclude).claude'` — so new files
are included and the arm's copied-in agents/skills are not. Validated
mechanically (new test file appears, `.claude` excluded) and on both
high-effort reps (11 and 14 test functions captured, 0 leak). The judge
vindicated it: re-judged on complete diffs, `tests` rose **1→8** and
`overall` **~4→~7–8**. Prior craft numbers (including benchmark 7-era) were
computed on test-stripped diffs and should be treated as invalid on the
`tests`/`overall` axes; the *relative* plain≈nullius tie happened to
survive because the omission hit both arms equally.

## Threats to validity

1. **n=1 per fixed-harness cell** (plain-high, nullius-high, nullius-low);
   only plain-low is n=3. nullius-low agreeing with the v4 n=4 mean is the
   guard, but the high-effort cells are single draws.
2. **Sub-2-point craft gaps are noise.** The "nullius-low is tidiest"
   reading is not supported at n=1/3-votes; the defensible claim is parity.
3. **Same-task tuning.** All of this is vialite-todo; lens/effort
   generalization to a disjoint-defect task remains the owed benchmark
   (NEXT.md #4).

## What this changes

- The half-cost-at-matched-quality headline now holds at **both** effort
  levels, and the craft dimension — previously unmeasurable due to the
  diff bug — comes back a **tie**. nullius is cheaper *and* not worse.
- **Recommended operating point: nullius-low.** High effort buys nothing
  on this task's ceiling.
- Every future run judges the complete change set; the blind quality judge
  is now trustworthy on tests.

Raw rows: `harness/results/results.jsonl` (arms `plain-fable-5-high`,
`nullius-fable-5-high`, `nullius-fable-5-low`); craft votes in
`harness/results/quality-hi-lo.jsonl`.
