# Benchmark harness

Headless, unbiased, reproducible byproxy-vs-solo runs. Replaces the inline
orchestration used for benchmarks 1–2, which had four biases: a
terrain-contaminated orchestrator, unmeasured orchestrator cost,
experimenter steering mid-run, and single samples.

## Design

- Each rep runs in a **fresh git worktree** at the task's pinned REF.
- Each arm is **one headless `claude -p` invocation** — the orchestrator
  starts clean every rep and its cost is *measured* (`total_cost_usd` in
  the JSON output), not estimated.
- **Scoring is independent**: `score.sh` replays the task's named tests +
  vet + full race suite against the tree. A run's self-report is never
  trusted (observed failure mode: builder reported green with a broken
  test build).
- Model pins: byproxy arm orchestrator = **claude-fable-5 @ low effort**
  (the control plane should be smart but cheap-per-token-and-terse;
  explorer/builder tiers come from the agent definitions). Solo arm =
  **claude-opus-4-8** (the "just let the expensive model read the files"
  baseline; override with `SOLO_MODEL=`).

## Usage

```sh
./run.sh tasks/p34-uploads byproxy --reps 3
./run.sh tasks/p34-uploads solo    --reps 3
jq -s 'group_by(.arm) | map({arm:.[0].arm,
  mean_cost:(map(.cost_usd)|add/length),
  complete:(map(.score.complete)|map(select(.))|length)})' results/results.jsonl
```

`--keep` preserves the worktree for post-mortem. Raw per-run JSON (full
usage breakdown) lands next to `results.jsonl` in `results/`.

## Adding a task

`tasks/<name>/` needs three files:
- `meta.env` — `REPO` (local path), `REF` (pinned commit), optional
  `TIMEOUT_S`
- `prompt.md` — the task text, identical for both arms
- `tests.txt` — one Go test name per line; the independent DONE-WHEN

## Caveats that remain

- Nondeterminism: report distributions, not single reps (≥3 reps/arm).
- Pass/fail scoring only; code-quality comparison needs a separate blind
  judge pass over the diffs (planned: third headless run with an audit
  prompt that doesn't know which arm produced which diff).
- Go-specific scorer; generalize per task type as needed.
- Permissions: runs use `--permission-mode auto` (the classifier gates
  headless runs too) plus an allowlist for the routine loop (file tools,
  go toolchain, git reads, subagent dispatch). No blanket bypass; the
  worktree additionally confines repo damage. If a run stalls or degrades
  because the classifier denied something legitimate, widen the allowlist
  in run.sh rather than reaching for bypassPermissions.
