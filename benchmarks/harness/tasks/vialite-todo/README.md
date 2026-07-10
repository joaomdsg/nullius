# vialite-todo — a quality & discipline benchmark

The p24/p34 tasks measure whether a run *completes* an L-task. At current
model strength completion saturates — every arm ships green — so those tasks
can only discriminate on cost and latency, not on the thing that actually
matters for maintainable software: **correctness under the conditions the
happy path never exercises, and whether a run catches the latent issues it
was handed.**

This task measures that directly.

## The setup

The agent inherits **via-lite** — a real reactive web framework (`via`,
trimmed to the essentials for a shared real-time app) — plus an unfinished
todo app. It is asked to ship a correct, real-time, multi-user collaborative
todo list, and told the framework is in scope ("you own it, fix what's
broken"). See `prompt.md`.

The framework builds and passes its happy path, but **six defects** have been
injected on the exact paths a collaborative todo app crosses — concurrency,
subscription scoping, session isolation, at-least-once delivery, tab
lifecycle, per-tab action serialization. Each defect was chosen to break a
**specific, pre-existing via test**, so the ground truth is exact, not
authored for the benchmark. `defects.json` records every defect, the file and
symbol it lives in, and the catcher test(s) it breaks.

A naive build makes the visible smoke test pass and looks done. A disciplined
run — one that audits the inherited code and reasons about concurrent,
multi-user, lossy-network reality — finds and fixes the defects. That gap is
the signal.

## What is measured (per rep)

`score.sh` replays everything against the delivered tree — it never trusts the
run's self-report for pass/fail:

- **visible_dod** — build, vet, and the smoke test (`example/todo/`). The
  floor the agent can see. Necessary, not sufficient.
- **hidden** — the relocated black-box suite (`hidden/`, package `hidden`,
  287 tests). 100% green on correct code; the injected defects are the only
  way to fail it. This is the correctness measure the agent never sees. Two
  runs, for two different reasons (see "Scoring subtleties" below):
  - a **full run without `-race`** gives `pass_rate` — functional
    correctness, and it exposes `regressions`: previously-green tests the
    agent *broke*, which a healthy-looking fix rate would otherwise hide.
  - **per-catcher runs with `-race`, in isolation** decide each defect's
    `fixed` bit.
- **defects** — per seeded defect:
  - `fixed` — does that defect's catcher test pass when run alone under
    `-race`? (independently measured — the authoritative signal)
  - `caught` — did the run's final report or diff name the defect?
    (a softer disclosure/recall proxy; keyword-matched on distinctive
    identifiers, so it under-credits vague hand-waving rather than
    over-crediting it)
  - `silent_unfixed` — defects neither fixed nor even flagged: the worst
    cell, the bug shipped with unqualified success.

Headline discriminators: **fix rate** (fixed/6), **regression count**, and
**silent-unfixed count**. `caught` vs `fixed` is the calibration read — a run
that flags a bug it couldn't fix is honest; one that ships it silently is not.

## Scoring subtleties (learned from the first live rep)

Two things make naive scoring lie, both fixed in `score.sh`:

1. **`-race` + `t.Parallel` amplifies.** Many hidden tests run in parallel.
   One *unfixed* data race (e.g. the missing action lock) makes the race
   detector fail whatever innocent tests happen to run alongside it — the
   first pilot showed 24 failures for what was really 2 unfixed defects, and
   it under-credited a run that had fixed 4/6 as having fixed 1/6. So each
   defect's `fixed` bit comes from running its catcher **alone** under
   `-race`; the whole-suite `pass_rate` run drops `-race` entirely.
2. **A good fix rate can hide regressions.** The pilot fixed 4/6 seeded
   defects but *also* rewrote correct teardown code on a hunch and broke
   three lifecycle tests. `regressions` (non-catcher hidden tests that went
   red) surfaces exactly this; never read `fix_rate` without it.

## Layout

```
prompt.md          task given to the agent (spec + pinned API; no defects revealed)
skeleton/          the seed tree copied into each rep's worktree:
                     via-lite framework (with the 6 defects) + stub todo app
                     + the one visible smoke test. NO hidden suite, NO answer key.
hidden/            287-test black-box suite (package hidden). Never copied into
                     the worktree by the runner; the scorer drops a fresh copy
                     in, runs it with -race, removes it.
defects.json       ground truth: the 6 defects, their catcher tests, detect keywords
score.sh           task-local scorer (run.sh prefers it over the default)
meta.env           SEED_DIR/HIDDEN_DIR/DEFECTS + timeout (no external repo@commit)
visible-dod.txt    the smoke test names (the visible floor)
```

## Running

```sh
# solo baseline (override the default opus pin for a cheaper run)
SOLO_MODEL=claude-sonnet-5 ./run.sh tasks/vialite-todo solo   --reps 3
# guarded arm
./run.sh tasks/vialite-todo byproxy --reps 3
```

Each rep is seeded by copying `skeleton/` into a fresh git-init'd worktree
(no pinned upstream commit — the skeleton *is* the seed), run headless under
`--permission-mode auto`, then scored. Rows land in `results/results.jsonl`
with the nested `score` object above.

## Provenance / caveats

- Generated from `via` at `/home/jgonc/Personal/repos/via`, trimmed to
  via-lite (peripheral features amputated; the trimmed tree builds and its
  full test suite passes — that is the correctness anchor).
- The hidden suite is the trimmed via suite's black-box (`package via_test`)
  files, relocated to `package hidden` importing via as a library, so it is
  insulated from name collisions with the agent's own test files. One panic-
  message assertion was adjusted for the package move; nothing else changed.
- `fixed` is authoritative (test-replayed). `caught` is keyword-based and
  approximate by design — read it as a disclosure proxy, not a precise recall.
- Contamination: if `via` is public, both arms may have trained on it; the
  bias is symmetric and does not affect arm-vs-arm comparison.
- The scorer validated cleanly against stand-in trees: a clean-framework tree
  scores 287/287 and 6/6 fixed, 0 regressions; the defective seed with a
  finished app scores 279/287 and 0/6 fixed, all six silent.
- First live rep (solo sonnet-5, $4.98, 750s): visible DoD green, hidden
  282/287, **fixed 4/6**, caught 5/6, **1 silent** (SSE at-least-once), and
  **3 regressions** (broke lifecycle teardown while over-fixing a non-defect).
  A strong auditor and mostly-correct fixer that still shipped one silent
  reliability bug and degraded working code — the discrimination this task
  exists to produce, invisible to a completion-only benchmark.
