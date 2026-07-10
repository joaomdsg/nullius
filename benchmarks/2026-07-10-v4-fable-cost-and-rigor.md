# Benchmark 6 — v4 guard layer on fable, cost attribution, and a rigor audit

Benchmark 5 refuted byproxy's quality thesis at **sonnet-high** and called
solo the rational default. This run does three things it didn't: measures the
**v4** guard layer (real critic/builder/auditor subagents, not v3's inline
steps) at **fable-5/low**, attributes cost **per model** inside each run, and
subjects the whole benchmark program to an adversarial methodology review.
The task and scorer are unchanged (`harness/tasks/vialite-todo/`, 6 seeded
defects, 287-test hidden oracle). Same caveats as benchmark 5 apply and are
sharpened at the end.

## Results — byproxy v4 @ fable-5 / low effort, n=5

Five treatment-verified reps (6 `byproxy-explorer` + 1 critic + 1 builder +
1 auditor dispatch each, confirmed from the stream-json log). One rep was
interrupted (crash, $0.81) and re-run; one earlier rep was set aside for
carrying 3 regressions — both disclosed below, not hidden.

| metric | byproxy-v4-fable-low (n=5) |
|---|---|
| **fixed** /6 | 3, 3, 5, 4, 5 → **4.0** |
| **caught** /6 (disclosed) | → **5.4** |
| **silent** /6 (neither fixed nor disclosed) | → **0.6** |
| **regressions** | 0 across all 5 |
| **cost** | $8.7, 18.9, 13.4, 13.3, 14.8 → **$13.8** |

Per-defect fix frequency (out of 5), against benchmark 5's v3 sonnet arms:

| defect | v3 solo (n5) | v3 byproxy (n5) | **v4 byproxy-fable (n5)** |
|---|---|---|---|
| cas-lost-update | 5 | 4 | **5** |
| subscription-overwake | 4 | 4 | **5** |
| ttl-sweeps-connected | 3 | 4 | **5** |
| statesess-cross-session-leak | 1 | 0 | **2** |
| **action-not-serialized** | 3 | **0** | **3** |
| sse-premature-clear | 0 | 0 | **0** |

Two results cut against benchmark 5's refutation — with the caveat that this
is a **different tier** (fable-low, not sonnet-high), so it is a new data
point, not a rematch:

1. **The "tell" reversed.** `action-not-serialized` was v3 byproxy's whole
   failure — the guard arm caught it **0/5** while solo's extra grinding got
   it 3/5. v4 byproxy-fable fixes it **3/5**, and `statesess` went 0→2/5. The
   v4 arm finds the subtle races the v3 arm missed.
2. **Disclosure discipline shows up.** caught **5.4/6**, silent only
   **0.6/6**; two reps caught all 6 (recall 1.0), naming every bug they
   couldn't fix. Zero regressions across all five.

**The SSE invariant holds, with an asterisk.** No clean rep fixed
`sse-premature-clear` (0/5) — the "nobody fixes SSE without the fault-
injection harness" result survives. But the *discarded* rep did fix it
(first time across ~30 runs) — at the cost of 3 dispose-lifecycle
regressions. SSE isn't unfixable; it's unfixable-by-inspection without
collateral damage.

## Where the money goes — cost attribution by model

byproxy's cost thesis is "the expensive orchestrator stays thin and hands the
reading to cheap models." Parsing `modelUsage` from each run's result event
tests that directly. Across the 5 clean reps:

| model / role | share of run cost |
|---|---|
| **fable-5 — orchestrator** | **88–93%** ($7.6–17.6) |
| haiku-4-5 — explorers | **~4%** ($0.4–0.6) |
| sonnet-5 — builder | ~6% ($0.7–1.0) |

The entire run cost *is* the orchestrator's own tokens, at ~$0.11–0.13 per
orchestrator turn. The Haiku explorers — the whole "delegate the reading"
mechanism — are a rounding error.

**Why some runs cost 2.3× others.** The harness forces an identical
*structure* every rep (6 explorers / 1 critic / 1 builder / 1 auditor) but
does nothing to keep the orchestrator thin. Cost tracks orchestrator turn
count almost linearly, and the variance is how much fable grabs the wheel:

| run | fable $ | orch turns | orch Reads | orch **Edits** | orch **Bash** | builder turns/edits |
|---|---|---|---|---|---|---|
| cheap ($8.7) | $7.6 | 71 | 15 | 8 | 3 | 21 / 4 |
| dear ($18.9) | $17.6 | 140 | 17 | **19** | **23** | 44 / 2 |

The cheap run delegated and stepped back; the expensive run distrusted the
builder and did the editing and testing itself (19 edits + 23 bash calls
directly), while the sonnet builder churned 44 turns but landed only 2 edits.

**Implication.** The delegation architecture can't move cost on this task
because the orchestrator won't stay delegated — and with fable at ~90% of
tokens, every time it re-reads or re-edits, that is the whole bill. Cost
control here is about constraining the orchestrator's own hands-on churn, not
about cheaper explorers.

## Threats to validity — an adversarial review of the program

An independent fable critic audited the harness, scorer, and `results.jsonl`.
The following were verified against the code/data and apply to benchmark 5's
conclusions as much as this one. Ranked by ability to flip a headline.

1. **The baseline delegates too — so "solo grinds alone" is fiction.** The
   benchmark-5 solo arm dispatched **10, 9, 4, 2, 0** subagents across its
   reps (verified in `results.jsonl`). The `plain` arm is *supposed* to have
   the native subagent scaffold (both arms do; they differ only by the guard
   layer + global config, not tool access) — but the mechanism story ("solo
   grinding *alone* cracked the race") and the cost story ("byproxy saves by
   delegating reading to Haiku") both assumed the baseline didn't delegate.
   It did. The narrative, not the arm, was wrong. *Addressed here:* the arm
   is renamed `plain` and the "alone" framing is dropped; a truly controlled
   comparison needs the container's clean `$HOME` so `plain` also carries no
   global config/skills (on the host, both arms inherit `~/.claude`).
2. **The audit was never verified in the headline reps.** critic/builder/
   auditor dispatch counts are `null` on every benchmark-5 sonnet row (only
   explorers were instrumented then). The verdict sentence "the arm that
   audits with independent explorers caught it 0/5" rests on an audit whose
   *occurrence* was unmeasured. v4 now counts all four subagent types.
3. **"Direction is consistent across tiers" is contradicted by the data
   file.** Benchmark 5 cites single reps (byproxy-opus fixed=1, solo-fable
   fixed=5). `results.jsonl` holds unreported same-tier reps pointing the
   other way: byproxy-opus fixed **3, 4, 3**; solo-fable-low fixed **3**
   ($36.6). At opus/fable, byproxy fixed as many or more in most reps.
4. **`caught`/`silent` is a keyword grep over the diff.** `score.sh` flags a
   defect "caught" if any keyword appears in the **git diff or** report —
   so merely *editing near* a defect counts as disclosure, and a verbose
   report enumerating the prompt's own bullet list flags everything. Byproxy
   was ordered to emit a RISKS report; the plain arm got no reporting
   instruction — yet `caught` is compared across them. Half of benchmark 5's
   quality verdict (silent 1.4 vs 2.2) rests on this metric.
5. **Accepting the null with no power.** Benchmark 5 calls a
   quality gap of t≈1.4 / **p≈0.18** at n=5 a "clean refutation," while
   calling the cost gap (**p≈0.10**, and halved if one solo outlier is
   dropped) "robust." Inconsistent evidentiary standard; n=5 has ~no power to
   detect a 1-defect effect on a /6 metric where 3 defects are near-ceiling
   or unfixable.
6. **Confounded treatment.** The byproxy arm = guard layer **+** a large
   forced system-prompt injection **+** a mandated report format **+** a
   no-human ESCALATE self-answer loop; the plain arm gets a plain prompt. The
   measured effect is the bundle, not the guard layer.
7. **Post-hoc scorer + forking paths.** The scoring rule (catcher-in-
   isolation, suite without `-race`) and instrumentation were chosen after
   ~17 exploratory vialite runs. The regression check runs **without**
   `-race`, so a race the agent *introduces* into green code is invisible —
   on a concurrency task. "Regressions: 0" covers only functional ones.
8. **Telemetry bug.** stream-json emits multiple result events per run;
   `run.sh` reads the last, so `num_turns` (e.g. reported "5" for a ~91-turn
   rep) and the extracted report describe only the final segment. Cost is
   cumulative and survives; turn-based readings do not.

**Least-supported benchmark-5 claims:** (a) "direction is consistent across
tiers" — contradicted by the data file; (b) "solo sonnet is the rational
default / clean refutation" — an accepted null at p≈0.18 on a confounded
treatment; (c) "the only robust effect is cost, ~41% cheaper" — p≈0.10,
fragile to one outlier, and causally confounded because the solo arm also
delegated reading.

## What changed in the harness

- **`solo` → `plain`** arm rename (legacy `SOLO_*` env still honored;
  historical rows keep `solo-*` labels). `plain` = plain Claude Code, no
  byproxy skills/agents, no global config — **not** "no subagents"; both arms
  keep the native subagent scaffold and differ only by the guard layer.
- **Container mode** (`CONTAINER=1` + pinned `Dockerfile`): claude CLI and Go
  toolchain fixed, auth by `-e ANTHROPIC_API_KEY`, run as host uid, outbound-
  only, fresh `$HOME` (this is what actually strips global config, making
  `plain` plain). Build- and plumbing-verified; not yet run end-to-end (needs
  a key). The image is fixed and non-overridable by design.

## Honest status

- n=5, one task, one tier — directional, not established. The action-race
  reversal (0→3/5) is the most interesting signal but is byproxy-fable vs
  byproxy-sonnet across two campaigns, not a controlled swap.
- The threats above are not yet fixed in the *measurements* — they are fixed
  in the *harness* (rename, container, subagent toggle, full subagent
  counting). A preregistered, powered (n≥15), placebo-controlled rerun with
  a blind report judge is the next step before any claim earns the word
  "refuted" or "confirmed."
