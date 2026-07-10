# Benchmark harness

Headless, unbiased, reproducible byproxy-vs-plain runs. Replaces the inline
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
  explorer/builder tiers come from the agent definitions). Plain arm =
  **claude-opus-4-8** (the "just let the expensive model read the files"
  baseline; override with `PLAIN_MODEL=`, or legacy `SOLO_MODEL=`).

The two arms are `byproxy` and `plain` (formerly `solo`). **`plain` means
plain Claude Code with no byproxy skills/agents and no global config — not
"no subagents".** Both arms get the same allowlist, native subagent dispatch
included; they differ only by the guard layer (the project `.claude/skills`
+ `agents` and the forced-methodology prompt the byproxy arm gets). Run
`plain` under `CONTAINER=1` for the "no global config" guarantee (see below):
a host run inherits your `~/.claude`, so neither arm is truly plain on the
host — only inside the fixed image. Historical `results.jsonl` rows keep
their `solo-*` labels.

## Usage

```sh
./run.sh tasks/p34-uploads byproxy --reps 3
./run.sh tasks/p34-uploads plain   --reps 3
jq -s 'group_by(.arm) | map({arm:.[0].arm,
  mean_cost:(map(.cost_usd)|add/length),
  complete:(map(.score.complete)|map(select(.))|length)})' results/results.jsonl
```

`--keep` preserves the worktree for post-mortem. Raw per-run JSON (full
usage breakdown) lands next to `results.jsonl` in `results/`.

### Containerized runs (controlled env)

`CONTAINER=1` runs the `claude` invocation inside a pinned sandbox image
(`Dockerfile`: claude CLI + Go toolchain fixed) so the only variable across
a campaign is the arm, not the host. Build once, then:

```sh
docker build -t byproxy-bench:latest .
export ANTHROPIC_API_KEY=...            # required; passed by -e, never inlined
CONTAINER=1 ./run.sh tasks/vialite-todo plain --reps 5
```

Auth is passed by reference (`-e ANTHROPIC_API_KEY`) — bills to that key's
account, not the host OAuth login. The container runs as the invoking
`uid:gid` with a throwaway `$HOME`, so files it writes into the worktree
stay host-owned and scoring/cleanup are unchanged. Networking is the
default bridge (outbound-only); the container is otherwise isolated.

The image is a fixed `byproxy-bench:latest` — deliberately not overridable.
A controlled env that can be swapped per run isn't controlled; the CLI and
toolchain pins live in the `Dockerfile` (rebuild to change them, on purpose).

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
