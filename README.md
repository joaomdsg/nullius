# byproxy

Truth by proxy. Facts from explorers, intent from the user, judgment kept
where it's paid for.

A contract-driven guard layer for coding agents in Claude Code: the
orchestrator reads the hard code itself, escalates understanding gaps to
the user, and authors falsifiable contracts; cheap read-only explorers
gather every fact and re-run every check; a tool-less peer red-teams the
contract before code exists; a Sonnet builder ships the volume; a cold
peer auditor judges the landed diff. Nothing that matters is assumed —
every kind of truth is fetched from where it lives.

## Why this shape

byproxy's benchmarks (see below) refuted it twice. Benchmark 4 killed the
cost thesis: orchestrating the build through cheaper models buys nothing once
the mid-tier is capable enough to just do the work. Benchmark 5 — built to
measure quality directly, with seeded defects and a hidden oracle — killed
the quality thesis too: with the guard layer forced and verified, it fixed no
more defects and disclosed no more of them than a plain solo run, while
running cheaper. The epistemics that *looked* like the durable win in early
inline runs did not survive controlled measurement.

What honestly remained after v3 was narrower than the original pitch: the
guards made a run **cheaper and more legible**, not more correct. Recon
and audit delegated reading to Haiku (the measured ~41% cost cut), and a
run that discloses its own gaps is easier to trust than one that ships
unqualified success — even when the two are equally correct. v4 keeps
that and goes after what v3 gave up.

byproxy v4 responds to benchmark 5's diagnosis — the guard layer converged
faster and cheaper but *shallower*, missing the subtle defects that solo
grinding found — by concentrating each model where its tier earns its
price, and fetching every kind of truth from where it actually lives:
tree facts from explorers, intent from the user, judgment from the
orchestrator alone.

- **Understand** — parallel Haiku explorers map the terrain; the
  orchestrator then reads the critical-path files verbatim itself (the
  map decides *where* to read, never substitutes for reading), and
  escalates every contract-shaping intent gap to the user in one batch.
  Unescalated assumptions go to a mandatory `ASSUMED` ledger, disclosed
  at close.
- **Contract** — the orchestrator authors falsifiable behaviors with
  acceptance semantics, test obligations, doc scope, and per-unit exit
  checks — the spec a capable builder executes without hand-holding.
- **Red-team** — a tool-less same-tier critic attacks the contract on
  paper: behaviors the task implies that no line forces, lines a lazy
  implementation satisfies vacuously. It never sees the tree or the
  orchestrator's reasoning; tree facts it needs come back as questions
  for cheap explorer checks.
- **Gate** — the contract compiles into mechanical checks (write-only
  API, mandated-untested, trivially-passing, collisions, concurrency
  invariants) that an explorer executes against the tree before code.
- **Build** — routed by judgment density, not size: concurrency,
  lifecycle, invariants, and error paths the orchestrator edits directly
  under guarded TDD; fully-specifiable volume goes to a persistent
  Sonnet builder that ships tests + implementation + docs against the
  contract. An explorer re-runs every exit check — self-reported green
  is never the record.
- **Cold peer audit** — a same-tier auditor sees the diff, task, and
  contract, never the reasoning. It judges — including behaviors the
  task implies that the *contract* missed — and hunts latent defects
  with probe tests it writes and runs under the race detector, because
  the measured record shows inspection alone misses the worst ones.

Every report still carries `VERBATIM` (machine output quoted, never
paraphrased) and a mandatory `UNKNOWN` — no agent in the loop decides
silently what information survives, and confident silence is treated as
the failure mode. v4's bet is stated in the skill and not yet measured:
contract + red-team + cold peer audit should recover the fix-rate v3's
economy gave away, at a cost still below a solo run of the
orchestrator's own tier. Benchmark 6 exists to refute it.

## The record

The data that forced this shape. Benchmarks 1–4 were generated at commit
[`8f7ed5c`](../../tree/8f7ed5ca7fdd4f966138e6e8d92cbb9a0b57ebd1) under the
earlier orchestrated architecture; benchmark 5 tested the v3 guard layer.
v4 is unmeasured until benchmark 6 runs.
Full reports with every bill in [`benchmarks/`](benchmarks/).

| bench | task | result |
|---|---|---|
| 1 (inline) | S: add one method | orchestration ~40% cheaper than solo opus |
| 2 (inline) | L: chunked uploads | orchestration loses 1.8× — builder re-entry cost diagnosed |
| 3 (headless, n=3/arm) | L: chunked uploads | cost tie vs solo opus; orchestrated runs ship self-auditing reports |
| 4 (headless grid) | L: public-API rewrite | **solo sonnet dominates all arms ($2.44)**; cost thesis refuted |
| 5 (headless, n=5/arm) | quality: seeded-defect todo app | **guard layer ~41% cheaper, no fix-rate or disclosure gain**; quality thesis refuted too |

Benchmarks 1–4 couldn't discriminate on quality — every arm completed, so
only cost and latency separated them, and the guards' value showed up only
as anecdote (self-auditing reports). Benchmark 5 was built to measure quality
directly, with seeded defects and a hidden oracle, and forced+verified the
guard layer actually ran (9–12 explorer dispatches per rep). The result: on
that task at sonnet strength, the guards bought cost, not correctness — solo
sonnet matched byproxy's fix rate and shipped no more silent bugs, and found
the hardest defect *more* often. The org chart fell in benchmark 4; the
epistemics fell in benchmark 5. What's left is an honest, well-instrumented
harness and the reporting discipline — worth keeping, not for a measured
quality edge, but because a run that discloses its own gaps is easier to
trust even when it isn't more correct.

## The benchmark harness

[`benchmarks/harness/`](benchmarks/harness/) runs any task as a grid of
arms — guarded or solo, any model pins — headlessly: fresh git worktree
per rep from a pinned commit, one `claude -p` per run under auto
permission mode (no bypass), measured `total_cost_usd`, and independent
scoring that never trusts a run's self-report (it caught an agent
reporting green on a broken test build, and its own scoring bug).

```sh
./run.sh tasks/p24-broadcast solo --reps 3
./run.sh tasks/p24-broadcast byproxy --reps 3
```

## What's in this repo

```
.claude/
  skills/byproxy/SKILL.md   # the v4 skill (workflow, contract format, gate taxonomy, protocol)
  agents/
    byproxy-explorer.md     # haiku · read-only breadth: recon, checks, exit-check re-runs
    byproxy-critic.md       # same-tier · tool-less contract red-team
    byproxy-builder.md      # sonnet · contract-executing tests+impl+docs, persistent
    byproxy-auditor.md      # same-tier · cold peer audit, probe-test armed
benchmarks/                 # measured reports, every claim with a bill (commit 8f7ed5c)
  harness/                  # the headless grid runner + tasks
template/CONVENTIONS.md     # questions-not-rules conventions template → recon context
```

## Getting started

byproxy is a **Claude Code skill** (it relies on Claude Code's subagent
dispatch and agent-definition model). Install both pieces:

```sh
git clone https://github.com/joaomdsg/byproxy.git
ln -s "$(pwd)/byproxy/.claude/skills/byproxy" ~/.claude/skills/byproxy
for a in byproxy/.claude/agents/*.md; do ln -s "$(pwd)/$a" ~/.claude/agents/; done
```

Agent definitions load at session start — restart Claude Code after
linking. Then invoke `/byproxy` on any task, or point the harness at your
own repo and measure before you believe anything, including this README.

## The philosophy in one sentence

Fetch facts from the cheap model, intent from the user, and judgment from
nowhere; make every builder prove its failures before its fixes; audit
with cold eyes that run probes instead of trusting their reading; publish
the bill — because the benchmark that can refute you is worth more than
the architecture it refuted.
