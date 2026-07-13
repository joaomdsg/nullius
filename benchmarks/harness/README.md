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
- Model pins: byproxy arm control plane = **claude-sonnet-5 @ high effort**
  (v6 — measured: ~80% of run cost is context traffic, and a *resident*
  fable-5 orchestrator spent $9–10/run on that traffic alone; the premium
  tier is pinned to the one small-context judgment call, the critic, via
  its agent definition). Override with `ORCH_MODEL=`/`ORCH_EFFORT=`;
  explorer/builder/critic tiers come from the agent definitions. Plain
  arm = **claude-opus-4-8** (the "just let the expensive model read the
  files" baseline; override with `PLAIN_MODEL=`, or legacy `SOLO_MODEL=`).

Six arms:

- `plain` — plain Claude Code, no byproxy skills/agents, no global config
  (**not** "no subagents"; the native subagent scaffold is still there).
  Gets a minimal symmetric disclosure ask so `caught` is measured fairly.
- `byproxy` — the full guard layer (v6), forced via the appended methodology.
- `plain+report` — `plain` **plus** the full RISKS report format, no guard
  layer: isolates whether the report *format* alone drives disclosure.
- `byproxy-noaudit` — the full guard layer **minus** the cold auditor:
  isolates what the audit itself buys (row must show `auditor_dispatches:0`).
- `byproxy-nobuilder` — the guard layer with the orchestrator/builder
  **split** removed: same contracts, critic, gate, and cold audit, but the
  orchestrator implements every unit itself (row must show
  `builder_dispatches:0`). Measured (benchmark 7): the split is pure waste
  at same tier — −38% cost, quality unchanged.
- `fable-lean` — **the measured-best config (benchmark 7)**: no ceremony at
  all. A hands-on top-tier leader (default `claude-fable-5@low`; override
  `LEAN_MODEL=`/`LEAN_EFFORT=`) that delegates all bulk context — builds,
  tests, sweeps, verification reruns — to throwaway haiku explorers, hunts
  with quoted-mechanism lenses, and fixes everything confirmed in-mandate
  before close. Confirmed n=4: quality ≥ plain fable every rep at 36% of
  its cost.

**`plain` means plain Claude Code with no byproxy skills/agents and no global
config — not "no subagents".** Every arm gets the same allowlist, native
subagent dispatch included; they differ only by the guard layer and the
report ask. Run under `CONTAINER=1` for the "no global config" guarantee
(see below): a host run inherits your `~/.claude`, so nothing is truly plain
on the host — only inside the fixed image. Historical `results.jsonl` rows
keep their `solo-*` labels.

**Blind disclosure judge.** `JUDGE=1` runs `judge.sh` after each rep — it
judges the report+diff **blind to the arm** and free of the detect keywords,
and writes `blind_disclosure` onto the row. This is the trusted disclosure
metric; `score.sh`'s keyword `caught` is the lower-trust secondary. Judge
tier is `JUDGE_MODEL` (default `claude-sonnet-5`).

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
export BYPROXY_ANTHROPIC_API_KEY=sk-ant-api03-...   # namespaced; won't hijack
                                                    # your interactive session
CONTAINER=1 ./run.sh tasks/vialite-todo plain --reps 5
```

Auth is passed by reference (`-e ANTHROPIC_API_KEY`) — bills to that key's
account, not the host OAuth login. The harness reads `BYPROXY_ANTHROPIC_API_KEY`
(preferred, so a bare `ANTHROPIC_API_KEY` never has to sit in your shell and
override your own Claude Code login) and maps it to the container's
`ANTHROPIC_API_KEY`; an explicit `ANTHROPIC_API_KEY` still takes precedence.
The credential must be a Messages-API key (`sk-ant-api03-`); an OAuth token
(`sk-ant-oat01-`, from `claude setup-token`) is rejected — run.sh preflights
for this and fails fast. The container runs as the invoking
`uid:gid` with a throwaway `$HOME`, so files it writes into the worktree
stay host-owned and scoring/cleanup are unchanged. Networking is the
default bridge (outbound-only); the container is otherwise isolated.

The image is a fixed `byproxy-bench:latest` — deliberately not overridable.
A controlled env that can be swapped per run isn't controlled; the CLI and
toolchain pins live in the `Dockerfile` (rebuild to change them, on purpose).
The image ships `gcc` with `CGO_ENABLED=1` so the race detector works
*inside* the sandbox — without it, every arm's `go test -race` silently
degrades to non-race runs (measured: a run disclosed "-race was not run: no
C compiler") while host-side scoring still races, an asymmetry that guts
race-forcing exit checks.

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
