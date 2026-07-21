# pi-nullius

The nullius harness as a [pi](https://pi.dev) extension. Any pi session
becomes a nullius leader: judgment stays in one premium context; bulk
burns in throwaway scout subprocesses; the context diet is enforced by
code, not prompt.

## What it does

- **`scout` tool** — dispatches a read-only sub-session (`pi -p --mode
  json --no-session`) on a cheap model with the nullius explorer prompt.
  Modes: recon / narrow / hunt / rerun. Report hard-capped (default 40
  lines) *in code* — a chatty scout gets truncated, not obeyed. Per-scout
  cost/tokens/turns returned in tool details.
- **Diet governor** (`tool_call` hook) — mechanically blocks, with a
  steering reason:
  - whole reads of files > `NULLIUS_MAX_READ` lines (default 250) → ranged
    read or scout;
  - exact-duplicate reads of a range already held (cleared when the file
    is edited);
  - build/test/linter commands in the leader's own context → `scout
    mode=rerun` (escape hatch: prefix command with `#nullius:ok`);
  - unbounded repo-wide `grep -r`/`rg` → scout, or bound it;
  - silently bounds other single-line commands with `| tail -n 30`.
- **Nullius compactor** (`session_before_compact`) — replaces pi's generic
  summary with a STATUS/GOAL/FACTS/EDITS/VERIFIED/RISKS/UNKNOWN/
  RULED-OUT/ASSUMED evidence ledger written by the scout model. Falls
  back to the built-in summarizer on any failure.
- **Leader rules + lens library** appended to the system prompt
  (`before_agent_start`) — the full nullius discipline including the
  8-lens hunt library (serialization, fault survival, scope confinement,
  wake predicates, lost updates, resource release, boundaries, swallowed
  errors).
- **Live status bar** — resident context tokens, growth per turn, leader
  vs scout spend. `/nullius` prints stats + config.
- **`consult` tool** (opt-in: set `NULLIUS_ADVISOR_MODEL`) — a
  reasoning-only advisor (no tools, `--no-tools`) that supplies the
  judgment a fast leader can't. The leader fills a load-bearing pack
  template (goal / state / anchored evidence / question); packs with
  unanchored evidence are rejected in code. The advisor keeps a
  persistent pi session across consults and answers with
  RULING / PLAN (each step with a VERIFY predicate) /
  CONSULT-AGAIN-WHEN / DEMAND / ASSUMPTIONS. Consults are mandatory at
  three gates: stage=plan (checklist landed), stage=surprise (evidence
  contradicts plan), stage=close (before the close report). Intended
  pairing: fast leader+scout (e.g. qwen3.6) with a slow reasoning
  advisor (e.g. glm5.2) — the tool loop runs at fast-model speed, the
  advisor is paid ~3-5 times per task on tiny packs.

## Install

```bash
pi install /path/to/nullius/pi-nullius   # already done on this machine
pi /login                                # auth at least one provider
```

## Config (env)

| Var | Default | Meaning |
|---|---|---|
| `NULLIUS_SCOUT_MODEL` | `local-fast/qwen3.6` | `provider/model-id` for scouts (mixed-provider OK) |
| `NULLIUS_SCOUT_TIMEOUT` | `300` | per-scout wall clock (s) |
| `NULLIUS_ADVISOR_MODEL` | — | `provider/model-id` for the reasoning advisor; unset = no consult tool |
| `NULLIUS_ADVISOR_TIMEOUT` | `600` | per-consult wall clock (s) |
| `NULLIUS_DIET` | on | `0` disables the governor |
| `NULLIUS_MAX_READ` | `250` | whole-read line ceiling |
| `NULLIUS_MAX_EDIT` | `40` | focused-fix edit-line ceiling |
| `NULLIUS_TESTS_FIRST` | `3` | source edits allowed per test touch |
| `NULLIUS_SCOUT_PARALLEL` | `5` | hunt-pipeline scout concurrency |
| `NULLIUS_AUTOHUNT` | on | `0` disables the auto-hunt on session start |
| `NULLIUS_DEBUG` | — | `1` enables debug logging to stderr |
| `NULLIUS_OFF` | — | `1` disables the extension |

## Why this shape (measured, see ../benchmarks)

~80% of a premium run's cost is context traffic: leader cost ≈
resident_context × turns. So the top tier pays only for judgment; scouts
eat the bulk in discarded contexts at ~1/15 the price. On the vialite-todo
bench, this discipline (as a Claude Code prompt) hit plain-top-tier
quality at 26% of its cost. Here the diet is *mechanical* — the leader
cannot drift from it — and compaction preserves the evidence ledger
instead of a lossy generic summary, so the context growth rate is capped
by construction.

Benchmark arm (`pi-nullius` in benchmarks/harness/run.sh): planned once a
provider is authed — headless `pi -p --mode json`, cost parsed from
message usage events.
