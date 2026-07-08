# Benchmark 1 — via `SliceOps.RemoveAt` (S-size task)

First real byproxy run with a token bill, against the baseline the README
demands: "just let the expensive model read the files."

## Setup

- **Repo:** [go-via/via](https://github.com/go-via/via) at `8bd5f1d` — a Go
  reactive web library, ~130 files, strong test conventions.
- **Task (identical for both runs):** add `SliceOps.RemoveAt(i int)` —
  removes the element at index i, out-of-range is a no-op. Test-first, repo
  conventions, edge cases covered, `go vet` + `go test -race ./` green.
- **Isolation:** two git worktrees from the same HEAD. Neither run saw the
  other.
- **Run A (byproxy):** orchestrator (Opus-class, this session) dispatching
  explorers (Haiku) and a builder (Sonnet) through the RYGB loop.
- **Run B (solo):** one general-purpose agent on the session model
  (Opus-class), plain task prompt, no pre-chewed findings.

## Run A — telegraph log (condensed)

| # | Dispatch | Agent (model) | Tokens | Outcome |
|---|---|---|---|---|
| E1 | narrow: SliceOps internals | Explore (haiku)¹ | not reported² | ops[] closure pattern, guard style, method list |
| E2 | narrow: test pattern + conventions | Explore (haiku)¹ | not reported² | vt harness, Fire/AwaitFrame, naming rules, testify usage |
| B1 | RED: failing test | general-purpose (sonnet)¹ | 36,040 | test fails only on RemoveAt-undefined; flagged frame-on-no-op risk |
| E3 | yellow: test critique | Explore (haiku)¹ | not reported² | ruled out the frame risk (quoted `markStateDirty` — unconditional); found missing i=0 / i=len-1 / empty coverage |
| B1' | redirect (DIAG/FIX): amend test | same builder, resumed | 31,173 | +4 edge-case steps, still fails for the right reason |
| B2 | GREEN: implement | **byproxy-builder** (sonnet) | 14,653 | 13-line method, suite green, 3 tool calls |
| E4 | blue: audit | **byproxy-explorer** (haiku) | 25,031 | 100% branch coverage, aliasing verified correct, vet + full race suite green VERBATIM |
| — | VERIFY | skipped | 0 | Blue already ran the full DONE-WHEN checks verbatim; a separate dispatch would duplicate them |

¹ The native `byproxy-*` agent definitions were symlinked mid-session and
weren't in the Agent registry until a restart — early dispatches emulated
them (generic agent + template inlined in the prompt). The registry picked
them up mid-run; GREEN and Blue used the real definitions.
² The Explore-type results did not include usage blocks. Estimated
~15–25k each based on comparable haiku runs (E4, same class: 25k).

**Measured subagent total:** 106,897 tokens (sonnet 81,866 + haiku 25,031).
**Estimated with the three unreported haiku runs:** ~160–180k.
**Orchestrator overhead:** ~3–4k output tokens of dispatches/judgment
(estimated from dispatch text; the orchestrator ran inside a larger session
so its exact share isn't separable).
**Wall time:** ~5.5 min of agent runtime, plus orchestrator turns.

## Run B — solo bill

| Agent | Tokens | Tool calls | Wall time |
|---|---|---|---|
| general-purpose (opus-class) | 36,544 | 15 | 2.8 min |

TDD sequence followed unprompted-in-detail (test → right-reason failure →
minimal impl → green), vet + full race suite green.

## Quality

**Tie.** The `RemoveAt` implementations are logically identical (same guard,
same fresh-allocation copy — both matched the repo's Prepend/Filter
no-aliasing convention). Test coverage equivalent: both cover first/middle/
last removal, removal to empty, and both out-of-range directions. The solo
run's test was marginally cleverer in one spot (proving a no-op via a
follow-up append frame); the byproxy run's edge coverage arrived only after
the Yellow critique — the loop worked, but the solo model included the same
cases on its own.

## Verdict — corrected (v2)

*The first version of this section called cost "roughly a tie." That was
wrong: it compared raw token counts, and tokens are not priced equally
across models. Priced in dollars, byproxy won.*

Converting Run A to opus-equivalent tokens at list-price ratios
(Sonnet ≈ 1/5 of Opus, Haiku ≈ 1/15):

| | raw tokens | price ratio | opus-equivalent |
|---|---|---|---|
| Builder (sonnet) | 81,866 | 0.20 | 16.4k |
| Explorers (haiku, incl. ~60k est. unreported) | ~85k | 0.067 | ~5.7k |
| **byproxy total** | ~167k | | **~22k** |
| **Solo (opus)** | 36,544 | 1.0 | **36.5k** |

On this S-size task — byproxy's *worst* regime, two small files, nothing to
delegate — byproxy was **~40% cheaper in dollars**, while losing on raw
tokens (~3–5×) and wall time (~2×). Quality was equal.

The dollar win survives even here because the price asymmetry is the whole
design: byproxy can burn 5–15× the raw tokens of a solo opus run and still
come out ahead. The README's caveat ("on a small change, just reading may be
cheaper") held for *time*, not for *money* — one explorer round-trip
(E1+E2+E3) exceeded the solo agent's entire information budget in tokens,
but at haiku prices it barely registered on the bill.

What byproxy *did* demonstrably deliver:

- **Process legibility.** The telegraph log above is a complete, auditable
  record of what was learned, decided, and built — the solo run's equivalent
  reasoning is buried in an opaque transcript.
- **The Yellow catch is real.** The builder's initial test genuinely lacked
  the off-by-one cases; a cheap read-only critique found them before any
  implementation existed. (The solo model didn't need the catch this time —
  on a harder task, that's not guaranteed.)
- **Native agent definitions matter.** RED with the template inlined in the
  prompt: 36k tokens. GREEN with the registered `byproxy-builder`: 14.6k.
  Same model tier, same codebase. The per-dispatch template tax the design
  worries about is real and the agent-definition mechanism removes it.

## Open questions for benchmark 2

The interesting regime is untested: an L-size task where the solo agent must
page in 15+ files (repeatedly, across a long session) while byproxy's
orchestrator context stays telegraph-sized. Benchmark 1's ~40% dollar win in
byproxy's worst regime suggests the margin there could be large. Also
needed: exact usage reporting for every dispatch
(the three unreported explorer runs weaken this bill), and a measured
orchestrator-share rather than an estimate.

## Operational lessons

1. Agent definitions load at session start — symlinking them mid-session
   requires a restart (or they appear only when the registry refreshes).
   The README install step should say so.
2. Blue's audit, given a DONE-WHEN-shaped dispatch, naturally subsumes the
   VERIFY step. The SKILL workflow could fold VERIFY into Blue for RYGB
   tasks and keep it separate only for non-TDD dispatches.
3. Explorer reports came back well-formed TGS on the first try in all four
   dispatches — the protocol is cheap to follow, at least for Haiku-class
   models on narrow questions.
