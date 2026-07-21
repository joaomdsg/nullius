# 2026-07-21 — the scale discriminator: a large SPA→Via port, three ways

The `port-todo` family (now removed) never separated a solo model from a
delegating one: the source was small and fully specified, so fable-low held
it in context and nailed every trap solo. `agri-alert-via-port` restores the
**scale lever** — the port source is a real multi-screen SPA (many components,
two databases, auth/HMAC, maps, four grids, SMS) too large to hold in a
64k-capped context, so a solo arm must skim or delegate. The observable API is
a generic, synthetic [`CONTRACT.md`](harness/tasks/agri-alert-via-port/CONTRACT.md);
the *depth* lives in the mounted (private, never-committed) source.

## What ran

Three arms, all **fable-5 / effort low**, same 64k cap, same 135-file source
mounted (`AGRI_SOURCE_DIR`), scored by the same black-box 24-test oracle.

- **plain** — one solo `claude -p`, no plugin. Pays all context traffic itself.
- **recursive** — a fable-low **leader** (diet-governor-provisioned, scouts for
  bulk) authors a thin brief and delegates the whole build to a nested
  **sonnet-low craftsman** via `nullius-build`.
- **nullius-solo** — the same fable-low leader under the same diet governor +
  haiku scouts, but it **writes every Go file itself** — no nested builder.
  Isolates the method from the recursion.

## Result

| arm | score | total cost | turns | wall | breakdown |
|-----|-------|-----------|-------|------|-----------|
| plain        | 24/24 | **$8.95** | 55    | 1029s | fable $8.95 + haiku $0.00 |
| recursive    | 24/24 | **$4.88** | 7+44  | 1059s | leader $1.26 + craftsman $3.62 |
| nullius-solo | 24/24 | **$6.34** | 29    | 806s  | fable $5.72 + haiku $0.62 |

Every arm produced a **fully conformant port — 24/24**.

> **Correction (2026-07-21, post-run):** as first run, all three arms reported
> **22/24**, each failing the *same two* tests — T1 and T7. Three independent
> codebases failing the identical pair was the tell: both were **oracle
> false-negatives**, not port defects. T1 read the `HttpOnly` flag back out of
> a Go `cookiejar`, which only preserves `Name`/`Value` (the flag is always
> false there) — the ports *did* set `HttpOnly`. T7 asserted a freshly-created
> contact on page 1, but with 20 seeded contacts + PAGE_SIZE 15 the new id (21)
> sorts onto page 2 — the ports *did* HTML-escape via Via's `h.Text`. Both were
> fixed in the oracle (`hidden/`) and re-frozen; re-scoring the *unchanged* arm
> binaries lifts all three from 22/24 to **24/24**. The cost figures below are
> from the original runs and are unaffected.

## Read

- **Quality is a flat tie at 24/24.** With the scale lever engaged, the
  delegating arms neither gained nor lost quality vs the solo arm — all three
  produced fully conformant ports.
- **Cost ranks recursive < nullius-solo < plain.** The nullius method *alone*
  (governor keeps the leader off bulk, haiku eats it for $0.62) cuts plain's
  bill **~29%** at equal quality. Adding recursion cuts **~45%**: the fable
  leader pays just $1.26 for judgment and the sonnet craftsman absorbs the
  write-bulk at $3.62.
- **nullius-solo was fastest** (806s) and leanest in turns (29 vs plain's 55) —
  the diet's real product is fewer, cheaper turns, not more of them.
- A contract-only run (no source mounted) of nullius-solo scored the same
  22/24 at $5.37 — source added ~$1, as expected for the heavier input.

## Calibration — the deferred-reference risk bit, then paid off

The oracle was built and frozen (`FROZEN.sha256`, 18 files) *before* any full
passing reference existed. That risk **materialized**: two assertions were
false-negatives no conformant port could pass (T1's jar-read of `HttpOnly`,
T7's page-1 assertion against a page-2 contact), and with no passing reference
to check against, they shipped. The tell was the run itself — three independent
arms converging on the *identical two* failures is the signature of an oracle
bug, not three coincidental identical port gaps. Once caught, both were fixed
against the real Via mechanism (`h.Text` escapes; `App.sessionCookie` sets
`HttpOnly`) and the arms re-scored clean. Lesson: "the suite is frozen and
green-on-a-stub" is not calibration; convergent cross-arm failures are the
cheapest validation signal and should be read as a bug hypothesis first.

## Caveat

n=1 per arm — a cost-ranking signal, not a variance-bounded claim. With the
oracle corrected the ceiling is a genuine 24/24 that all three arms reach, so
the separation between arms is entirely on **cost**, not quality.
