# Benchmark 5 — a quality task, and the guard layer's turn to be refuted

Benchmarks 1–4 measured *completion*, which saturates: at current model
strength every arm ships green, so those tasks can only rank cost and
latency. Benchmark 4 killed byproxy's cost thesis. This one goes after the
half that survived — the **guard layer** (recon → design gate → independent
explorer audit → disclose-your-gaps) — on a task built to expose the thing
completion can't: **correctness under conditions the happy path never hits,
and whether a run catches the latent issues it was handed.**

## The task: `vialite-todo`

The agent inherits **via-lite** (via trimmed to the essentials for a
real-time shared app) plus an unfinished todo app, and must ship a correct
multi-user collaborative list. The framework is declared in scope. Six
defects are seeded on the paths a collaborative todo app crosses —
concurrency (CAS lost-update, unserialized actions), subscription scoping,
session isolation, at-least-once SSE delivery, tab lifecycle. **Each defect
was chosen to break a specific pre-existing via test**, so ground truth is
exact, not authored for the benchmark. A 287-test black-box hidden suite
(relocated to its own package, never shown to the agent) is the correctness
oracle; it is 100% green on correct code. See
[`harness/tasks/vialite-todo/`](harness/tasks/vialite-todo/).

Scored per rep, all replayed against the delivered tree: **fixed** (defect's
catcher passes, run in isolation under `-race`), **caught** (the run's report
names the defect — disclosure), **silent** (neither fixed nor disclosed — the
worst cell), **regressions** (previously-green tests the run broke), and cost.

## Results — sonnet-5 / high effort, n=5 per arm

Every byproxy rep is treatment-verified: 9–12 `byproxy-explorer` dispatches
each, confirmed from the stream-json event log (the plain summary omits
subagent calls — see "Harness notes").

| metric | solo (n=5) | byproxy (n=5) | significance |
|---|---|---|---|
| **fixed** /6 | 2,4,3,3,4 → **3.2** | 3,1,2,3,3 → **2.4** | not significant (t≈1.4, p≈0.18) |
| **cost** | $6.9–20.8 → **$12.0** | $5.1–10.7 → **$7.1** | byproxy ~41% cheaper (t≈1.9, p≈0.10) |
| **silent** bugs | → **1.4** | → **2.2** | solo fewer; within noise |
| **hidden** pass | ~283/287 | ~283/287 | tie |
| **regressions** | 0 | 0 | tie |

Per-defect fix frequency (out of 5) tells the real story:

| defect | solo | byproxy | |
|---|---|---|---|
| cas-lost-update | 5 | 4 | both easy — spec demands it |
| subscription-overwake | 4 | 4 | both easy |
| ttl-sweeps-connected | 3 | 4 | byproxy edge |
| statesess-cross-session-leak | 1 | 0 | solo only, rarely |
| **action-not-serialized** | **3** | **0** | solo only — the tell |
| sse-premature-clear | 0 | 0 | nobody, ever |

## Verdict — the guard layer bought cost, not quality

On `vialite-todo` at sonnet-5/high, **byproxy v3's guard layer shows no
measurable correctness benefit.** It fixed slightly fewer defects (2.4 vs
3.2) and shipped slightly more silent bugs (2.2 vs 1.4) — both differences
inside the noise band. The only robust effect is cost: delegating the reading
to Haiku explorers ran ~41% cheaper.

The per-defect breakdown is the opposite of the guard-layer thesis. Solo's
entire edge is the **action-serialization race** — the arm grinding solo
(longer, pricier) cracked it 3/5 times; the arm that *audits with independent
explorers* caught it **0/5**. Byproxy converges faster and cheaper but
shallower: the same economy that saves the money means less deep grinding on
the subtlest defect. The audit did not find what the extra solo effort did.

And a fixed point across ~30 runs and every model: **nobody ever fixed the
SSE at-least-once bug.** A premature queue-clear that only manifests under
write-failure injection is not findable by inspection or by any test the
agent thinks to write — a useful reminder that some latent defects are
invisible without the fault-injection harness that seeded them.

This is a clean refutation, and a fitting one. First the cost thesis
(benchmark 4), now the quality thesis: on this class of task at this model
strength, **solo sonnet is the rational default** — same correctness, and if
you let it spend, it finds more. The guard layer's disclosure discipline,
which looked like the durable win in earlier inline runs, did not reproduce
as a measurable advantage once the treatment was forced and verified.

## Caveats (kept honest)

- **n=5, one task, one model/effort.** Fix-rate and silent-bug gaps are not
  statistically clean; only the cost gap approaches significance. Direction
  is consistent, magnitude is not established.
- Single-rep instrumented probes on the other tiers (not averaged) pointed
  the same way: byproxy cheaper, not more correct — solo-opus fixed=3/$12.5
  vs byproxy-opus fixed=1/$6.5; solo-fable fixed=5/$16.0 vs byproxy-fable
  fixed=3/$9.4. Directional only.
- The guard layer *did* run: forced via appended system prompt, verified via
  9–12 explorer dispatches per rep. This is not a "byproxy didn't engage"
  artifact — an earlier read that said so was a measurement error (below).

## Harness notes (two bugs the pilot and grid caught)

1. **`-race` + `t.Parallel` amplifies.** Running the full hidden suite under
   `-race` lets one unfixed data race fail dozens of innocent parallel tests;
   the first pilot mis-scored a 4/6 run as 1/6. Fixed: each defect's `fixed`
   bit comes from its catcher run **alone** under `-race`; the whole-suite
   pass rate drops `-race`.
2. **The summary hid the treatment.** `--output-format json` omits subagent
   and Skill tool calls, so byproxy arms looked like they ran solo — a wrong
   conclusion drawn from a lossy log. Fixed: capture `stream-json`, count
   `explorer_dispatches` per row. All n=5 byproxy reps then showed 9–12.

Reproduce: `PLAIN_MODEL=claude-sonnet-5 PLAIN_EFFORT=high ./run.sh
tasks/vialite-todo plain --reps 5` and the `byproxy` arm with
`ORCH_MODEL=claude-sonnet-5 ORCH_EFFORT=high`. (The `solo` arm is now named
`plain`; legacy `SOLO_*` env still works.)

## Addendum (2026-07-10) — this writeup's claims are contested

An adversarial methodology review found several of the conclusions above
under-supported or confounded — most seriously, the "solo" arm here **also
dispatched up to 10 subagents/rep**, so both the cost and the "solo grinds
alone" mechanism stories are confounded; the independent audit's *occurrence*
was never instrumented in these reps; and "direction is consistent across
tiers" is contradicted by unreported reps in `results.jsonl`. See the
**Threats to validity** section of `2026-07-10-v4-fable-cost-and-rigor.md`.
Treat the "clean refutation" framing as provisional pending a preregistered,
powered rerun.
