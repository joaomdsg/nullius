# stats-rolling — a quality & discipline benchmark

The second fixture in the vialite-todo family, built to answer the question
that a single fixture cannot: **do the lenses generalize, or were they tuned
to vialite's failure shapes?**

vialite-todo's six defects are all *concurrent-system* defects — lost
updates, over-broadcast, session leaks, at-least-once delivery, lifecycle
sweeps, missing serialization. A run can score well there by carrying one
strong habit: "think about goroutines". This fixture is deliberately
**disjoint**: single-threaded, dependency-free, purely numerical code whose
defects are *silent correctness* defects — aliasing, short copies,
off-by-one ranks, numerical instability, length underflow, a swallowed
error. Nothing races. Nothing is distributed. The only way to find these is
to actually read the arithmetic and the memory ownership.

## The setup

The agent inherits a vendored copy of `montanaflynn/stats` and a **feature
request**: extend the existing `rolling.go` with `RollingMedian`,
`RollingStdDev`, `RollingPercentile` and a shared `Window` type (see
`prompt.md`). The ticket is an ordinary maintainer ticket. It does **not**
say defects exist, does not say "audit", and flags no file as suspicious.

Six defects have been injected on exactly the paths that feature crosses —
the windowing helper it extends, the copy helper it will lean on, the
percentile and deviation code it must reuse, the diff code next door. Each
is silent to the package's own pre-existing suite: an agent that implements
the feature, runs `go test ./...`, sees green and ships, ships all six.
`defects.json` records each defect, its injection target, and the hidden
catcher test(s) it turns red.

Fix rate is therefore *not* mostly a coding measure here — it is a measure
of whether the run went looking at all, and looked in the right register.

## What is measured (per rep)

`score.sh` replays everything against the delivered tree; it never trusts
the run's self-report for pass/fail.

- **tests_passed / tests_failed** — the six hidden catchers (`tests.txt`),
  each run with an exact `-run '^Name$'` anchor. A catcher must print an
  explicit `--- PASS:` line; an absent test scores failed, not vacuously
  green. No `-race`: this code is single-threaded, so the race detector
  would only add noise and cost.
- **full_suite_green** — the package's pre-existing suite, measured on the
  agent's tree *before* the hidden files are dropped in, so a hidden-catcher
  build error cannot falsify it. This is the regression signal.
- **vet_clean** — `go vet ./...`.
- **complete** — all catchers pass, vet clean, suite green.
- **defects** — per seeded defect: `fixed` (its catchers pass —
  authoritative, test-replayed) and `caught` (the final report, or that
  defect's own diff hunk, names it — keyword-matched, a deliberately
  lower-trust disclosure proxy; `judge.sh` is the semantic version).
  `silent_unfixed` is the worst cell: shipped, unmentioned.

Headline discriminators: **fix rate** (fixed/6), **silent-unfixed count**,
and whether `full_suite_green` survived the feature work.

## Layout

```
prompt.md          the feature request handed to the agent (no defects revealed)
skeleton/          seed tree copied into each rep's worktree: vendored stats
                     + the 6 injected defects. NO hidden tests, NO answer key.
hidden/            the six TestDefect* catchers (package stats — they reach
                     unexported helpers). Never copied in by the runner; the
                     scorer drops a fresh copy into the module root under a
                     zz_hidden_ prefix and removes it via a trap.
defects.json       ground truth: 6 defects, catcher tests, detect keywords
tests.txt          the six catcher names (one per line, exact -run anchors)
score.sh           task-local scorer (run.sh prefers it over the default)
meta.env           SEED_DIR/HIDDEN_DIR/DEFECTS + timeout
visible-dod.txt    the acceptance floor the agent is told about
```

## Running

```sh
# solo baseline (override the default opus pin for a cheaper run)
SOLO_MODEL=claude-sonnet-5 ./run.sh tasks/stats-rolling solo   --reps 3
# guarded arm
./run.sh tasks/stats-rolling byproxy --reps 3
# with the blind disclosure judge
JUDGE=1 ./run.sh tasks/stats-rolling solo --reps 3
```

Each rep is seeded by copying `skeleton/` into a fresh git-init'd worktree
(no pinned upstream commit — the skeleton *is* the seed), run headless, then
scored. Rows land in `results/results.jsonl` with the nested `score` object.

## Provenance / caveats

- Upstream: `github.com/montanaflynn/stats`, MIT, HEAD
  `5badb5ad8b66438233feac0d78ec8a2e6d62e34e`. Package `stats`, `go 1.13`, no
  external dependencies — the whole fixture builds offline.
- The upstream tree at that commit is the correctness anchor: it is green
  before injection, and each injected defect is chosen to stay green against
  that suite while turning its own hidden catcher red.
- `fixed` is authoritative; `caught` is keyword-based and approximate by
  design — read it as a disclosure proxy, and prefer `judge.sh`.
- Contamination: `montanaflynn/stats` is a popular public repo, so both arms
  likely trained on it. The bias is symmetric (arm-vs-arm comparison is
  unaffected), but note that memorized-upstream knowledge could *help* an
  agent spot a deviation — which is a real auditing skill, not a leak of the
  answer key.
- `defects.json` symbols are injection-time best guesses; they are reconciled
  against `skeleton/` after injection (see the file's `_note`). Scoring never
  depends on them — only on the catchers.
