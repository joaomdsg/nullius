# Benchmark harness

Headless, unbiased, reproducible nullius-vs-plain runs (plus the archived
byproxy ceremony arms, kept runnable for reproduction). Replaces the inline
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
- Model pins: `nullius` leader = **claude-fable-5 @ low effort** (override
  `LEAN_MODEL=`/`LEAN_EFFORT=`); the archived byproxy control plane =
  `ORCH_MODEL`/`ORCH_EFFORT` (default sonnet-5 @ high, as
  measured-and-refuted in benchmark 7). Plain arm = **claude-opus-4-8**
  (the "just let the expensive model read the files" baseline; override
  with `PLAIN_MODEL=`, or legacy `SOLO_MODEL=`).

Seven arm names, six configs:

- `nullius` — **the live methodology and measured-best config (benchmark
  7)**: no ceremony at all. A hands-on top-tier leader that delegates all
  bulk context — builds, tests, sweeps, verification reruns — to throwaway
  haiku `nullius-explorer` scouts, hunts with quoted-mechanism lenses, and
  fixes everything confirmed in-mandate before close. Confirmed n=4:
  quality ≥ plain fable every rep, race-clean, 0 regressions, at 36% of
  the dearest baseline rep (~half the honest n=2 baseline mean).
- `fable-lean` — the same arm under its pre-rename label, kept so the
  benchmark-7 reproduce commands work verbatim.
- `plain` — plain Claude Code, no nullius/byproxy skills or agents, no
  global config (**not** "no subagents"; the native subagent scaffold is
  still there). Gets a minimal symmetric disclosure ask so `caught` is
  measured fairly.
- `plain+report` — `plain` **plus** the full RISKS report format: isolates
  whether the report *format* alone drives disclosure.
- `byproxy` — the archived v6 ceremony (`archive/byproxy-v6/`), forced via
  the appended methodology. Runnable for reproduction; refuted in
  benchmark 7.
- `byproxy-noaudit` — the ceremony **minus** the cold auditor: isolates
  what the audit itself buys (row must show `auditor_dispatches:0`).
- `byproxy-nobuilder` — the ceremony with the orchestrator/builder
  **split** removed (row must show `builder_dispatches:0`). Measured
  (benchmark 7): the split is pure waste at same tier — −38% cost, quality
  unchanged.

**`plain` means plain Claude Code with no nullius/byproxy skills or agents and no global
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

**Blind SWE-quality judge.** `quality-judge.sh <task-dir> <diff-file>` scores
one rep's diff 0-10 on root-causing, tests, minimality, idiom, and overall —
blind by construction: it sees the task prompt and the diff only, never the
arm label and never the run report (the report format alone would unblind
it). It judges how well a run did what it did; completeness against seeded
defects stays `score.sh`'s job — the two are complementary. Run it ad hoc
over `results/*.diff` and collect rows into `results/quality.jsonl`; tier is
`QUALITY_JUDGE_MODEL` (default `claude-sonnet-5`). Caveat: single-vote
scores; distrust differences smaller than ~2 points.

## Usage

```sh
CONTAINER=1 JUDGE=1 ./run.sh tasks/vialite-todo nullius --reps 3
CONTAINER=1 JUDGE=1 ./run.sh tasks/vialite-todo plain   --reps 3
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
docker build -t nullius-bench:latest .
export NULLIUS_ANTHROPIC_API_KEY=sk-ant-api03-...   # namespaced; won't hijack
                                                    # your interactive session
CONTAINER=1 ./run.sh tasks/vialite-todo plain --reps 5
```

Auth is passed by reference — exactly one credential env var enters the
container, never inlined. Two credential kinds are accepted, each preferred in
its namespaced form so a bare key never has to sit in your shell and override
your own interactive Claude Code login:

- **API key** (`sk-ant-api03-`, console.anthropic.com):
  `NULLIUS_ANTHROPIC_API_KEY` (legacy `BYPROXY_ANTHROPIC_API_KEY` honored)
  maps to the container's `ANTHROPIC_API_KEY` and bills to that key's account.
- **OAuth token** (`sk-ant-oat01-`, from `claude setup-token`):
  `NULLIUS_CLAUDE_CODE_OAUTH_TOKEN` (or `CLAUDE_CODE_OAUTH_TOKEN`) maps to the
  container's `CLAUDE_CODE_OAUTH_TOKEN` and bills to the subscription. An
  OAuth token found in the API-key slot is routed here rather than rejected.
  Caveat: `cost_usd` comes from the CLI's result event either way, but keep a
  campaign on one auth mode so cost comparisons stay apples-to-apples.

  Prefer the **file reference** so the token never sits in an environment
  variable or shell history — only a path does (safe to export from your
  shell profile):

  ```sh
  install -m 700 -d ~/.config/nullius
  claude setup-token          # browser flow; shown once, in YOUR terminal
  cat > ~/.config/nullius/oauth-token   # paste the token, Enter, Ctrl-D
  chmod 600 ~/.config/nullius/oauth-token
  export NULLIUS_CLAUDE_CODE_OAUTH_TOKEN_FILE=~/.config/nullius/oauth-token
  ```

  run.sh and judge.sh read the file at runtime; a direct token variable, if
  set, wins over the file.

An explicit `ANTHROPIC_API_KEY` holding a real API key still takes precedence
over everything else. run.sh preflights the credential and fails fast — before
the container spins up — when none is usable; `test-auth-wiring.sh` covers the
whole resolution against a shimmed docker (no network, no billing). The same
resolution applies to the JUDGE=1 host-side judge (judge.sh), which otherwise
falls back to the host `claude` login. The container runs as the invoking
`uid:gid` with a throwaway `$HOME`, so files it writes into the worktree
stay host-owned and scoring/cleanup are unchanged. Networking is the
default bridge (outbound-only); the container is otherwise isolated.

The image is a fixed `nullius-bench:latest` — deliberately not overridable.
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
- Code-quality comparison is the blind `quality-judge.sh` pass (see above);
  it is single-vote per diff, so treat small score differences as noise.
- Go-specific scorer; generalize per task type as needed.
- Permissions: runs use `--permission-mode auto` (the classifier gates
  headless runs too) plus an allowlist for the routine loop (file tools,
  go toolchain, git reads, subagent dispatch). No blanket bypass; the
  worktree additionally confines repo damage. If a run stalls or degrades
  because the classifier denied something legitimate, widen the allowlist
  in run.sh rather than reaching for bypassPermissions.
