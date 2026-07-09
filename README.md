# byproxy

You act by proxy. You never touch the code directly.

A multi-agent coding orchestrator for Claude Code — and the benchmark
harness that honestly refuted half of it.

## What this is now

byproxy began as a cost thesis: *the capable model is expensive, so ration
it — cheap explorers as its eyes, one mid-tier builder as its hands, the
orchestrator reasoning only over terse telegraphs.* Four benchmarks later
([`benchmarks/`](benchmarks/), every claim with a measured bill), the thesis
is dead and something better survived:

- **The cost thesis: refuted.** Solo sonnet-5 rewrote a public API with
  distributed semantics for $2.44, beating every orchestrated
  configuration and solo opus. The frontier moved: the cheap model is
  capable, so there is nothing left to arbitrage. byproxy's own harness
  proved this against byproxy.
- **The guard layer: verified.** Across every benchmark, the pieces that
  caught real defects and prevented real information loss were the
  *epistemics*: the TGS reporting obligations, the design red-team, the
  independent audit, and final reports that disclose their own known gaps.
  Solo runs shipped unqualified success; orchestrated runs shipped a
  defect list. Same green tests — different value to the human who has to
  maintain the code.

So read this repo two ways: as a working orchestration skill (it completes
L-tasks 100% of the time in headless runs, with an audit trail), and as a
documented experiment whose honest conclusion is that you probably want
its guards more than its org chart.

## The architecture

The **control plane / data plane** split from networking, applied to
coding agents. The orchestrator decides and dispatches; it never reads a
project file or runs a project command.

- **Orchestrator.** Reasons over telegraphs only. Its judgment is the
  system's scarce resource — benchmarks showed a strong-but-terse control
  plane (13–15 turns) keeps the fleet cheap, and a weak one flails
  expensively. Don't cheap out here; the price is paid in dispatch
  discipline, not per-token rates.
- **Explorers** (Haiku, read-only tool fence). One narrow question each:
  recon, design red-team, post-unit audit. They report facts and quote
  machine output verbatim — never verdicts. The cheapest model in the loop
  must never decide what information survives.
- **Builder** (Sonnet, the only writer). One fresh instance per unit,
  executing a full guarded TDD cycle per dispatch against a forwarded
  context pack — never resumed across units (measured: a warm builder
  transcript re-bills its history every resume and dominated cost in
  benchmark 2).

## TGS — the telegraph protocol

All inter-agent messages use **TGS** (Telegraphic-Grok-Schema): labeled
fields, no prose, machine output quoted verbatim and bounded, absence
reported explicitly. `UNKNOWN:` is mandatory — confident silence is the
failure mode. `RULED-OUT:` prevents re-dispatching dead ends. A builder
dispatch:

```
TASK: fix off-by-one token count lex.go:31
SCOPE: lex.go only
CONTEXT: comment lex.go:29 claims intentional skip — preserve intent or update it
DONE-WHEN: TestParseHeader + TestComment green
ESCALATE-IF: 2 consecutive fail runs | fix requires touching parse.go | diff >30 lines
```

Full spec: [`references/tgs-spec.md`](.claude/skills/byproxy/references/tgs-spec.md).
These reporting obligations are the repo's most transferable idea — they
survived every benchmark and belong in any multi-agent system, byproxy or
not.

## The guarded build cycle (RYGB v2)

The builder never writes freehand, and byproxy never pays a dispatch per
TDD phase (measured: that tripled builder turns for no correctness gain):

- **Design red-team** (once per task) — the orchestrator fixes each unit's
  test list and API surface; an explorer attacks the design before
  anything builds: write-only features, mandated-but-untested code,
  trivially-passing tests.
- **One dispatch = one unit's full cycle** — tests first, right-reason
  failure quoted verbatim, minimal implementation, prune, diff stat
  quoted (bounds are verified by the orchestrator, never trusted).
- **Audit, pipelined** — an explorer audits each landed unit while the
  next unit builds; the final audit re-runs everything as verification.

Full spec: [`references/rygb-cycle.md`](.claude/skills/byproxy/references/rygb-cycle.md).

## The benchmark harness

[`benchmarks/harness/`](benchmarks/harness/) runs any task as a grid of
arms — orchestrated or solo, any model pins — headlessly: fresh git
worktree per rep from a pinned commit, one `claude -p` per run under auto
permission mode (no bypass), measured `total_cost_usd`, and **independent
scoring that never trusts a run's self-report** (it caught a builder
reporting green on a broken test build, and its own scoring bug).

```sh
./run.sh tasks/p24-broadcast solo --reps 3                       # solo arm
ORCH_MODEL=claude-fable-5 ORCH_EFFORT=low \
  ./run.sh tasks/p24-broadcast byproxy --reps 3                  # orchestrated arm
```

## The record

| bench | task | result |
|---|---|---|
| 1 (inline) | S: add one method | byproxy ~40% cheaper than solo opus |
| 2 (inline) | L: chunked uploads | byproxy loses 1.8× — builder re-entry diagnosed → v2 redesign |
| 3 (headless, n=3/arm) | L: chunked uploads | cost tie vs solo opus; byproxy: $0.15 cost spread + self-auditing reports |
| 4 (headless grid) | L: public-API rewrite | **solo sonnet dominates all arms ($2.44)**; sonnet-orchestrator ablation fails; cost thesis refuted |

All 12 scored headless runs, both architectures, completed their DoD
(6 acceptance tests + vet + full race suite) — completion doesn't
discriminate at this task size; cost, latency, and report honesty do.

## Where this goes (v3, if built)

The rational default today is **solo sonnet + byproxy's guard layer**:
recon-informed context up front, a design red-team before building, an
independent audit after — ~$0.10–0.60 of explorer time wrapped around a
$2.44 solo run, capturing the measured quality value at a tenth of the
measured orchestration premium. Orchestration optional; epistemics
mandatory.

## What's in this repo

```
.claude/
  skills/byproxy/           # orchestrator instructions (SKILL.md) + references
  agents/                   # byproxy-explorer (haiku, read-only), byproxy-builder (sonnet)
benchmarks/                 # four benchmark reports, every claim with a bill
  harness/                  # the headless grid runner + tasks
template/CONVENTIONS.md     # questions-not-rules conventions template → dispatch CONTEXT
```

## Getting started

byproxy is a **Claude Code skill** (Claude-specific — it relies on Claude
Code's subagent dispatch and agent-definition model). Install both pieces:

```sh
git clone https://github.com/joaomdsg/byproxy.git
ln -s "$(pwd)/byproxy/.claude/skills/byproxy" ~/.claude/skills/byproxy
ln -s "$(pwd)/byproxy/.claude/agents/byproxy-explorer.md" ~/.claude/agents/byproxy-explorer.md
ln -s "$(pwd)/byproxy/.claude/agents/byproxy-builder.md"  ~/.claude/agents/byproxy-builder.md
```

Agent definitions load at session start — restart Claude Code after
linking. Then invoke `/byproxy` on any task, or point the harness at your
own repo and measure before you believe anything, including this README.

## The philosophy in one sentence

Make the cheap model report instead of judge, make the builder prove its
failures before its fixes, audit everything independently, and publish the
bill — because the benchmark that can refute you is worth more than the
architecture it refuted.
