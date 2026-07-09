# Benchmark 4 — four-arm grid on a discriminating task (via P2.4)

The question benchmark 3 left open: is the orchestrator's price tag the last
cost lever, and does orchestration survive a cheap-but-capable solo
baseline? Task upgraded to force discrimination: **unified scoped
Broadcast** — a *rewrite* of an existing public API (delete four old entry
points, migrate every in-tree caller, keep the pre-existing suite green)
with distributed semantics (bounded fan-out pool, ctx deadline into a
stallable backplane, honest pod-local receipts). Same harness as
benchmark 3: headless, fresh pinned worktrees, auto permission mode,
independent scoring.

## Results (sorted by mean cost)

| arm | n | mean cost | range | mean wall | complete |
|---|---|---|---|---|---|
| **solo-sonnet-5** | 3 | **$2.44** | $1.98–2.70 | **7.9 min** | **3/3** |
| byproxy-fable-5-low | 1 | $4.94 | — | 14.2 min | 1/1 |
| byproxy-sonnet-5-medium | 3 | $5.15 | $3.38–6.30 | 25.2 min | 3/3 |
| solo-opus-4-8 | 1 | $7.02 | — | 16.9 min | 1/1 |

(A fourth byproxy-sonnet run completed at $4.45 but its independent score
was lost to a harness bug — fixed in run.sh; its cost is consistent with
the arm's range. Every scored run, all four arms, was fully green:
6/6 tests + vet + full race suite, callers migrated.)

## Verdict — byproxy's cost thesis is refuted

**solo-sonnet-5 dominates every arm on every metric**: cheapest, fastest,
tightest spread, 3/3 complete on a task explicitly chosen to be hard to
be green-but-wrong on. The premise byproxy was built on — "the capable
model is expensive, so ration it through cheap proxies" — is dissolved by
the frontier having moved: **the cheap model is capable.** Sonnet-5 solo
rewrote a public API with distributed semantics for $2.44; the entire
control-plane apparatus can only add cost on top of that.

Secondary findings, each real:

- **The sonnet-orchestrator ablation failed.** Projected ~$2.6–3.2; actual
  mean $5.15 with a $2.92 spread. A mid-tier control plane is cheaper per
  token but flails more — costlier dispatches, longer walls (21–29 min),
  and benchmark 3's cost-predictability did NOT replicate with it. The
  fable-low orchestrator's tight 13–15-turn discipline was doing real
  work; per-token price was the wrong thing to optimize. Control-plane
  *judgment* is what keeps the fleet cheap, and judgment is what premium
  models sell.
- **Both byproxy arms beat solo-opus.** Benchmark 2's "orchestration loses
  to solo" was really "v1 loses"; v2 wins against the expensive baseline.
  It just doesn't matter, because the expensive baseline lost to its own
  little sibling.
- **The process value persisted.** byproxy runs again shipped disclosure-
  laden reports (residual gaps named, semantic tradeoffs documented — e.g.
  ToTabs predicates staying pod-local with Appended:false). Solo runs
  reported unqualified success. Pass/fail scoring cannot see this
  difference; a human maintainer can.

## What survives

Across four benchmarks, the durable assets are not the economics:

1. **TGS's reporting obligations** (UNKNOWN/RULED-OUT/VERBATIM) — they
   measurably prevented information loss in every orchestrated run.
2. **The independent audit** — it caught real defects (assembled-upload
   leak, self-reported-green-with-broken-build) that self-reports missed,
   in both architectures' output.
3. **The harness itself** — unbiased, self-hardening (it caught its own
   scoring bug), and the only reason any of these claims are trustworthy.

The honest repositioning: byproxy is not a cost optimizer. At today's
prices the rational default is **solo sonnet + an independent audit pass**
(~$0.10–0.60 of haiku/sonnet explorer time) — which is byproxy's *guard
layer* without its *orchestration layer*. A "byproxy-lite" that wraps any
solo run in recon-informed context, a design red-team, and a post-hoc
audit would capture ~all of the measured quality value at ~10% of the
measured premium. That is the v3 worth building — if any.

## Cumulative scoreboard (4 benchmarks, 16 scored headless/inline runs)

- Cost: solo-sonnet > everything (bench 4); byproxy never beat the
  cheapest competent solo on any task.
- Reliability: 100% completion for both architectures on both headless
  tasks — completion doesn't discriminate at this task size; cost,
  latency, and report quality do.
- Latency: solo wins everywhere, 1.6–3×.
- Epistemics: byproxy wins everywhere — audit trails, defect disclosure,
  design review — at a premium that should be paid per-guard, not
  per-architecture.
