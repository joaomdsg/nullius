# nullius

***Nullius in verba* — take nobody's word for it.**

The Royal Society has worn that motto since 1660. This repo applies it to
coding agents: not the scout's summary, not the test's green, not the
code's own comments, not the model's memory of the tree. Claims are free;
**mechanisms are quoted or they don't exist.**

nullius is a measured, ceremony-free methodology for Claude Code. Its
economic premise is one measured fact: **you are not paying for the top
tier's intelligence — you are paying for its attention span.** ~80% of a
premium run's cost is context traffic, the model re-reading its own
conversation every turn; the judgment itself is a few dollars. So nullius
makes cost scale with **what the leader holds, not with repo size** — a
two-million-line codebase bills like a toy repo if the leader reads five
files and scouts sweep the rest:

- **The leader pays only for judgment.** The top-tier model works
  hands-on — reads the decisive code once, designs, edits, writes the
  tests — under one hard rule: *the context window is the bill*; every
  token that enters it is re-paid on every turn that follows.
- **Scouts eat the bulk.** Throwaway haiku `nullius-explorer` agents run
  every build, test, sweep, and verification rerun in contexts that are
  billed once at ~1/15 the price and then discarded. Their reruns — never
  the leader's self-reported green — are the record.
- **Lenses, not trust.** Discovery is delegated too: lensed hunts that
  must return quoted mechanisms — the lock acquisition inside the
  entrypoint's own body, the scope argument at the fan-out call site, a
  wake predicate that can actually be false, nothing cleared before its
  write confirms — because every measured miss in this project's history
  was a trusted-claim failure.
- **Fix-now.** A confirmed in-mandate defect is fixed test-first before
  close — disclosure is never a substitute. RISKS is for what could not
  be confirmed, with reasons.

Measured (benchmark 7, seeded-defect task, n=4): quality at-or-above a
plain top-tier run in **every** rep — race-clean, zero regressions, all
six seeded defects fixed in three of four reps — at roughly **half the
honest-baseline cost** (36% of the dearest baseline rep) and 60% of the
wall time.

## Born from its own refutations

This project reached nullius by measuring seven benchmarks and killing
its own architecture six times. It began as **byproxy**, a contract-driven
guard layer: orchestrator, explorer fleet, tool-less contract critic,
builder, cold peer auditor. Every layer of that ceremony was benchmarked —
and fell:

| bench | result |
|---|---|
| 1 (inline) | orchestration ~40% cheaper than solo opus |
| 2 (inline) | orchestration loses 1.8× — builder re-entry cost diagnosed |
| 3 (headless, n=3/arm) | cost tie vs solo opus; orchestrated runs ship self-auditing reports |
| 4 (headless grid) | **solo sonnet dominates all arms**; cost thesis refuted |
| 5 (headless, n=5/arm) | guard layer ~41% cheaper, **no fix-rate or disclosure gain**; quality thesis refuted |
| 6 (headless) | v4/v5 parity with plain fable at best; **~80% of cost is context traffic**, not thinking |
| 7 (headless, n=4 confirm) | **sonnet control plane refuted twice** (tier is not a tuning knob); orchestrator/builder **split retired** (−38% cost, no quality change); **nullius (né fable-lean) confirmed**: ≥ plain-fable quality at 36% of the dearest baseline rep, race-clean, 0 regressions, every rep |

Full reports, every claim with a bill, in [`benchmarks/`](benchmarks/).
The v6 ceremony lives on in [`archive/byproxy-v6/`](archive/byproxy-v6/),
runnable for reproduction — kept because the graveyard is the argument.

What survived every refutation: **verbatim evidence over paraphrase, a
mandatory UNKNOWN, published bills, and independent verification** — the
epistemics. nullius is those epistemics with the ceremony burned off.

## The benchmark harness

[`benchmarks/harness/`](benchmarks/harness/) runs any task as a grid of
arms — `nullius`, the archived `byproxy*` ceremony arms, `plain`,
`plain+report` — headlessly: fresh git worktree per rep from a pinned
commit, containerized toolchain (`CONTAINER=1`), one `claude -p` per run
under auto permission mode, measured cost per model, independent scoring
that never trusts a run's self-report, and a blind disclosure judge
(`JUDGE=1`) that scores reports without knowing which arm wrote them.

```sh
CONTAINER=1 JUDGE=1 ./run.sh tasks/vialite-todo nullius --reps 3
CONTAINER=1 JUDGE=1 ./run.sh tasks/vialite-todo plain --reps 3
```

## What's in this repo

```
.claude/
  skills/nullius/SKILL.md   # the methodology: rules, lenses, economics
  agents/nullius-explorer.md# haiku scout: quoted mechanisms, capped reports, reruns-as-record
archive/byproxy-v6/         # the measured-and-refuted ceremony ancestor (runnable)
benchmarks/                 # measured reports, every claim with a bill
  harness/                  # the headless grid runner + tasks
template/CONVENTIONS.md     # questions-not-rules conventions template → recon context
```

## Getting started

nullius is a **Claude Code skill** (it relies on Claude Code's subagent
dispatch and agent-definition model). Install both pieces:

```sh
git clone https://github.com/joaomdsg/nullius.git
ln -s "$(pwd)/nullius/.claude/skills/nullius" ~/.claude/skills/nullius
ln -s "$(pwd)/nullius/.claude/agents/nullius-explorer.md" ~/.claude/agents/
```

Agent definitions load at session start — restart Claude Code after
linking. Then invoke `/nullius` on any task, or point the harness at your
own repo and measure before you believe anything, including this README.

## Known limits, stated plainly

The lenses were derived and validated on one seeded-defect task; their
generalization to disjoint defect classes is unmeasured (a second oracle
task is the next benchmark). The baseline is small-n and high-variance.
And the whole record says top-tier judgment only pays **where quality
discriminates** — on a task whose definition-of-done is fully visible and
mechanical, a plain mid-tier run is a third of the price and just as
green. Use nullius where latent defects and subtle invariants matter.

**Greenfield is forecast, not measurement.** All seven benchmarks were
brownfield. On new code the lenses flip from hunting to design (what
serializes the entrypoint *you're about to write*?), the signature hazard
becomes vacuous success — green suites that force nothing, which rule 7
targets — and the cost edge should compress toward ~1.5× (there is
nothing to not-hold on day one, and the leader's own writes accumulate).
Preregistered predictions and the experiments that will test them —
greenfield oracle, sonnet-as-leader, lens generalization, the owed
baseline — live in [`benchmarks/NEXT.md`](benchmarks/NEXT.md).

## The philosophy in one sentence

Take nobody's word for it — quote the mechanism, delegate the bulk, fix
what you confirm, disclose what you can't, and publish the bill — because
the benchmark that can refute you is worth more than the architecture it
refuted, six times so far.
