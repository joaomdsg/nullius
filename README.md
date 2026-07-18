# nullius

***Nullius in verba* — take nobody's word for it.**

You are not paying for a frontier model's intelligence. You are paying
for its attention span. ~80% of a premium coding run's cost is context
traffic — the model re-reading its own conversation every turn. The
judgment is a few dollars; the rest is rent.

nullius is a Claude Code methodology built on that one measured fact.
The top-tier model works hands-on and holds almost nothing: throwaway
haiku scouts absorb every build, test, and sweep in contexts billed once
at ~1/15 the price and then discarded. Discovery runs through lensed
hunts that must return **quoted mechanisms** — the lock acquisition in
the entrypoint's own body, the scope argument at the fan-out call site —
never claims, because every measured miss in this project's history was
a trusted-claim failure. Confirmed defects are fixed test-first before
close; a scout rerun, never self-reported green, is the record.

## The numbers

Seeded-defect oracle task (6 planted concurrency bugs), headless harness,
independent scoring, blind disclosure judge. Full reports in
[`benchmarks/`](benchmarks/).

| arm | cost | defects fixed | race-clean |
|---|---:|---:|---|
| plain fable, high effort | $23.34 | 6/6 | yes |
| **nullius, low effort** | **$6.17** | **6/6** | **yes** |
| plain fable, low effort (baseline, n=3) | $12.36 | 4.33/6 | 1/3 |
| nullius (benchmark 7, n=4 mean) | $6.23 | 5.75/6 | 4/4, 0 regressions |
| v6 ceremony, sonnet control plane | $20.56 | 3/6 | — |

Greenfield (lens-hostile by design: single-threaded double-entry ledger,
29 hidden conformance tests, frozen before any rep — n=1 each):

| arm | cost | hidden suite | turns |
|---|---:|---:|---:|
| plain fable, low effort | $3.98 | 29/29 | 25 |
| cc-nullius plugin, full process | $7.08 | 29/29 | 57 |
| **cc-nullius plugin, terrain-gated** | **$4.54** | **29/29** | **12** |

The full process on empty lens terrain is pure tax (+78%). The fix is a
load-bearing gate: terrain scouts must QUOTE the absence of lens targets
before ceremony stands down — and with it, the plugin lands within noise
of plain while keeping the mechanical guarantee that saved the brownfield
runs (one softened hunt = the signature defect shipped silently, 5/6).

Same quality as the honest top-tier run at **26% of its cost**. Better
quality than the same-effort baseline at **half its cost** — in every
rep. And the failed rows matter as much as the winning ones: contracts,
red-team critics, and cold auditors cost $12–14 of premium context per
run and never beat a plain top-tier run; a mid-tier control plane under
the full process capped at 3/6, twice. Tier is not a tuning knob.
Ceremony is not rigor. This project benchmarked its own architecture to
death, refutation after refutation, to learn that — the graveyard is the
argument, and it lives in [`archive/byproxy-v6/`](archive/byproxy-v6/),
runnable.

## Reproduce it

The harness runs any task as a headless grid — fresh worktree per rep,
containerized toolchain, measured cost per model, scoring that never
trusts a run's self-report:

```sh
cd benchmarks/harness
CONTAINER=1 JUDGE=1 ./run.sh tasks/vialite-todo nullius --reps 3
CONTAINER=1 JUDGE=1 ./run.sh tasks/vialite-todo plain --reps 3
```

## Install

As a Claude Code plugin ([`cc-nullius/`](cc-nullius/) — diet-governor
hook that ENFORCES the starvation, plus the scout/hunter/craftsman
agents and the doctrine as a skill):

```sh
/plugin marketplace add <path-to-repo>/cc-nullius
/plugin install nullius@nullius-local
```

Or as the bare skill + agent:

```sh
git clone https://github.com/joaomdsg/nullius.git
ln -s "$(pwd)/nullius/.claude/skills/nullius" ~/.claude/skills/nullius
ln -s "$(pwd)/nullius/.claude/agents/nullius-explorer.md" ~/.claude/agents/
```

Restart Claude Code (agents load at session start), then invoke
`/nullius` on any task.

## Limits, stated plainly

The lenses were derived and validated on one seeded-defect task;
generalization to disjoint BROWNFIELD defect classes is unmeasured (the
greenfield task was built lens-hostile on purpose). Nearly every
plugin-era number is n=1 — replication is the standing debt. The
brownfield baseline is small-n and high-variance. And top-tier judgment only pays
where quality discriminates: on a mechanical task with a fully visible
definition of done, a plain mid-tier run is a third of the price and
just as green. Preregistered next experiments:
[`benchmarks/NEXT.md`](benchmarks/NEXT.md).

Measure before you believe anything — including this README.
