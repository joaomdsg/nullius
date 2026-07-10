# byproxy

Facts by proxy. You build; explorers guard.

A guard layer for coding agents in Claude Code: the capable model designs,
writes the code, and judges — cheap read-only explorers gather every fact,
execute every check, and audit every diff. The model doing the judging
never gathers its own evidence; the model gathering evidence never judges.

## Why this shape

byproxy's benchmarks (see below) refuted it twice. Benchmark 4 killed the
cost thesis: orchestrating the build through cheaper models buys nothing once
the mid-tier is capable enough to just do the work. Benchmark 5 — built to
measure quality directly, with seeded defects and a hidden oracle — killed
the quality thesis too: with the guard layer forced and verified, it fixed no
more defects and disclosed no more of them than a plain solo run, while
running cheaper. The epistemics that *looked* like the durable win in early
inline runs did not survive controlled measurement.

What honestly remains is narrower than the original pitch, and worth being
clear about: the guards make a run **cheaper and more legible**, not more
correct. Recon and audit delegate reading to Haiku (the measured ~41% cost
cut), and a run that discloses its own gaps is easier to trust than one that
ships unqualified success — even when the two are equally correct. That is
the case for the shape below; it is not a case that it finds more bugs.

byproxy v3 is only the guards:

- **Recon** — parallel explorers pull house patterns, existing symbols,
  and test layout before design. Facts arrive quoted, gaps declared.
- **Design gate** — the builder compiles its own design into falsifiable
  checks from a fixed failure taxonomy (write-only API, mandated-untested
  code, trivially-passing tests, symbol collisions); an explorer executes
  them mechanically — pass/fail with verbatim evidence — before any code
  exists. The explorer never critiques; it verifies claims against the tree.
- **Guarded TDD build** — done directly by the capable model: right-reason
  failure quoted before implementation, minimal to green, prune.
- **Independent audit** — a cold explorer re-runs the exit checks and hunts
  uncovered code per landed unit; the final audit runs the whole suite.
  Every accepted risk is written down in the close.

Explorer reports always carry `VERBATIM` (machine output quoted, never
paraphrased) and a mandatory `UNKNOWN` — the cheapest model in the loop
never decides what information survives, and confident silence is treated
as the failure mode.

## The record

The data that forced this shape. Benchmarks 1–4 were generated at commit
[`8f7ed5c`](../../tree/8f7ed5ca7fdd4f966138e6e8d92cbb9a0b57ebd1) under the
earlier orchestrated architecture; benchmark 5 tests the current guard layer.
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
  skills/byproxy/SKILL.md   # the guard-layer skill (workflow, gate taxonomy, protocol)
  agents/                   # byproxy-explorer (haiku, read-only tool fence)
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
ln -s "$(pwd)/byproxy/.claude/agents/byproxy-explorer.md" ~/.claude/agents/byproxy-explorer.md
```

Agent definitions load at session start — restart Claude Code after
linking. Then invoke `/byproxy` on any task, or point the harness at your
own repo and measure before you believe anything, including this README.

## The philosophy in one sentence

Make the cheap model report instead of judge, make the builder prove its
failures before its fixes, audit everything with cold eyes, and publish
the bill — because the benchmark that can refute you is worth more than
the architecture it refuted.
