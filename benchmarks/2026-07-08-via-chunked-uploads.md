# Benchmark 2 — via chunked resumable uploads (L-size task)

The regime byproxy was designed for: ~12 files to read, ~4 to write, a real
roadmap item (via P3.4) with 6 named acceptance tests. Benchmark 1 (S-task)
predicted the curves would cross here. **They did not. byproxy lost.**

## Setup

- Repo: go-via/via at `8bd5f1d`, two identical worktrees from HEAD.
- Task (identical text for both): chunked upload POSTs to
  `/_upload/{token}/{index}` with resume; crypto-random session-bound tokens,
  1h TTL + sweep; progress Signal; size caps at chunk and assembly; filename
  sanitization. Done = 6 named `TestUpload_*` tests green + vet + full
  `go test -race ./`. Browser test excluded for both.
- Run A: byproxy — this session as orchestrator (Opus-class), native
  `byproxy-explorer` (Haiku) and `byproxy-builder` (Sonnet) agents, one
  builder resumed across all phases, RYGB per unit (3 units), Blue folded
  into final VERIFY.
- Run B: solo — one general-purpose agent, session model (Opus-class),
  plain prompt.

## The bill

| | raw tokens | opus-equivalent¹ | wall (agent) | outcome |
|---|---|---|---|---|
| **Run A explorers** (haiku ×6) | 163,112 | 10.9k | ~5 min | |
| **Run A builder** (sonnet ×9 dispatches) | 658,983 | 131.8k | ~21 min | |
| **Run A orchestrator** (opus-class) | ~8–10k est.² | ~9k | — | |
| **Run A total** | ~830k | **~152k** | ~26 min + orchestration | 6/6 tests green, race+vet clean |
| **Run B solo** (opus-class) | 84,622 | **84.6k** | 7.6 min | 6/6 tests green, race+vet clean |

¹ List-price ratios: sonnet 1/5, haiku 1/15 of opus-class.
² Dispatch/judgment text estimated; orchestrator ran inside a larger session.

**byproxy cost ~1.8× more in dollars and ~4× more wall time.** Both runs
delivered green suites. Independent audits (below) found one real defect in
each.

## Why byproxy lost — the builder-transcript tax

The dominant cost was not exploration (haiku was 11k oe — cheap, as
designed). It was the **resumed builder**: per-dispatch cost grew nearly
monotonically — 33.6k, 37.3k, 59.7k, 63.9k, 72.2k, 81.8k, 89.0k, 101.2k,
120.5k. Keeping one builder warm across 9 dispatches means its transcript
grows like a solo context, but every resume re-bills the growing prefix.
The "warm builder" optimization from benchmark 1's lessons *backfired at L
scale*: the builder became a solo agent paying its context repeatedly,
while the solo baseline paid for its context growth once, incrementally, in
a single run.

Secondary costs, each real:
- **Escalation round-trips.** One legitimate builder escalation (SignalStr
  doesn't survive per-GET page re-instantiation — a genuine test-design bug
  it correctly refused to hack around) cost a ~60k diagnose-and-redirect
  cycle. The solo agent hit the same class of framework quirk and absorbed
  it inline.
- **Orchestrator spec bugs are expensive.** My too-tight SCOPE fence forced
  the builder into a package-global store it knew was wrong (it flagged the
  leak in RISKS); widening the fence and reworking cost a 72k dispatch.
  In byproxy, a bad spec is a full round-trip; solo just… fixes its plan.
- **Phase granularity multiplies dispatches.** 3 units × Red/Green(+
  redirects) = 9 builder turns. Each turn pays transcript re-entry. The
  RYGB discipline itself was cheap; the *turn structure* it imposes on a
  resumed agent was not.

## Quality — independent explorer audits of both trees

**Run A (byproxy):** one CRITICAL defect — `removeExpiredUploads` skips
assembled uploads, so completed uploads stay in the map forever
(exploitable memory growth). Also: no API retrieves `assembledBytes`
(orchestrator design gap — my fixed design never specced retrieval), three
specced-but-untested branches (413 overflow, idempotent re-POST, contiguity
rejection — design mandated code the tests don't force, an RYGB violation I
authored), lock held during assembly concat.

**Run B (solo):** one real race — two concurrent final-chunk POSTs can both
pass the completeness check and call `assemble()` in parallel, corrupting
the output file (`O_TRUNC` interleaving). Also: progress Signal wired but
never tested, token dropped before size validation (by design, but easy to
regress). Better memory bounds than Run A (per-chunk cap, disk persistence
instead of unbounded in-memory retention).

Net: **comparable defect rates; solo more feature-complete** (disk
persistence + progress wiring vs Run A's in-memory placeholder). Neither
defect was fixed post-audit — symmetric treatment.

Process observations (both directions):
- Yellow caught a real false-pass risk (session-cookie distinctness) and
  the U1 critique-then-redirect loop worked as designed.
- The builder violated ESCALATE-IF once (22-line amendment vs a 15-line
  bound, self-waived) — prompt discipline is softer than a tool fence.
- Right-reason RED discipline held throughout, including the staged U3
  green that un-masked a compile-shadowed assertion.

## Standings after two benchmarks

| | S-task (bench 1) | L-task (bench 2) |
|---|---|---|
| Dollars | **byproxy** (~40% cheaper) | **solo** (~45% cheaper) |
| Wall time | solo (~2×) | solo (~4×) |
| Quality | tie | ~tie, solo more complete |

The working theory ("byproxy wins where reading dominates") is now
half-falsified: reading *was* delegated cheaply (11k oe of haiku), but the
win was consumed by the write-side turn structure. The cost center isn't
exploration — it's **builder re-entry**.

## What would actually change the outcome

1. **Fresh builder per unit, full CONTEXT in the dispatch** — stop resuming
   one builder across everything; pay a small re-read per unit instead of a
   compounding transcript. (Reverses benchmark 1's lesson at L scale; the
   right rule is probably: resume within a unit, fresh across units.)
2. **Batch RYGB phases per dispatch** — Red+Green in one builder turn with
   the Yellow critique between *units*, not between *phases*, halves builder
   turns. Costs some phase purity; the per-turn tax is currently the bigger
   number.
3. **Spec retrieval/design review before building** — both of Run A's design
   gaps (no retrieval API, untested mandated branches) were orchestrator
   authoring errors a one-shot design-review explorer would have caught for
   ~20k haiku tokens.
4. **Prompt-cache-aware accounting** — if resumed-builder prefixes are
   served from cache, the true dollar gap shrinks; `subagent_tokens` doesn't
   expose the split. Needs measurement before trusting either number hard.

## Honest bottom line

Across the two regimes tested, byproxy's only demonstrated dollar win is on
small tasks — the opposite of the design thesis — and it is uniformly slower.
Its real, replicated strengths are process ones: auditable telegraph logs,
right-reason test discipline, and cheap read-only critiques that catch real
test gaps. As a cost-optimization architecture, the current turn structure
gives back what the model-tier arbitrage earns. Fix builder re-entry
(items 1–2) and re-run an L-task before drawing a final conclusion.
