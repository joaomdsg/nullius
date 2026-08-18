# cc-nullius — starved-orchestrator plugin for Claude Code

The nullius methodology as a Claude Code plugin: the main-thread context
pays only for judgment; bulk absorption, lens hunts, implementation, and
verification run in cheap throwaway subagent contexts. Cumulative session
cost is quadratic in retained context — starving the orchestrator attacks
the growth term directly.

## What it ships

- **Diet governor** (`hooks/diet-governor.mjs`, PreToolUse, zero-dep node): on the main
  thread only (subagent calls, identified by `agent_id` — the reliable
  discriminator; `agent_type` can appear on the main thread in `--agent`
  sessions — pass untouched):
  - denies Grep/Glob/WebFetch/WebSearch → steers to scouts;
  - denies whole reads of files > `NULLIUS_MAX_READ` (250) lines and exact
    duplicate reads (ledger keyed on path+range+mtime, per session; deny
    reason names the narrower-range escape for post-compaction re-reads);
    binaries (extension or NUL sniff) are exempt from the line cap —
    newline bytes in a PNG are not lines;
  - denies heavy Bash (builds/tests/linters/package installs) and
    unbounded wide searches (incl. `grep -R`/`--recursive`);
  - bounds other single-line Bash with `2>&1 | tail -n 30`, preserving the
    command's exit code via `exit "${PIPESTATUS[0]}"`; commands with `#`
    comments or trailing `;` are left unrewritten/normalized (both broke
    the `{ …; }` wrapper); the rewrite carries NO permission decision, so
    the user's normal permission prompt still applies;
  - NO size cap on main-thread Edit/Write (measured 2026-07-19): a
    PreToolUse hook fires *after* the model generated the file, so a
    size-deny only makes the craftsman regenerate the same bytes —
    double-billing. The delegate-vs-write crossover (~1,800 lines cold /
    ~130 lean for an Opus leader) is set by variables a hook can't see,
    so the decision lives in the doctrine, before generation. The
    tests-first ratchet still applies (every `NULLIUS_EDITS_PER_TEST`
    source edits require a test-file touch);
  - boundary gate: edits that remove/rename exported symbols are DENIED in
    craftsman hands (design decisions escalate up) and ALLOWED on the main
    thread at any size — boundary-shaping is the orchestrator's. This gate
    catches a *regression*, so its one wasted generation is worth it;
  - craftsman tests-first gate: inside craftsman, the first
    Edit/Write must touch a test file or SOURCE edits are denied
    (scaffolding/config/docs are exempt — nothing to test).

  - MCP gate: main-thread `mcp__*` calls with bulk-verb names
    (read/fetch/search/list/query/…) are steered to scouts — MCP responses
    are otherwise ungoverned bulk; `NULLIUS_MCP_OK=1` disables;
  - QUICK mode (`.nullius-quick`, auto-expires after 4h /
    `NULLIUS_QUICK_TTL_H`): diet-lite for trivial tasks — sweeps, reads,
    edits, MCP, heavy Bash all pass, only tail-bounding stays. Toggled by
    `/nullius:quick`; also the answer for mechanical cross-file renames;
  - session telemetry: denies/rewrites/dispatches (per agent type) counted
    in `$TMPDIR/nullius-stats-<session>`, reported by `/nullius:diet status`.
  - Escapes: `#nullius:ok` in a command, `.nullius-quick` (diet-lite, 4h),
    `.nullius-off` file, `NULLIUS_OFF=1`.
- **Agents**: `scout` (haiku — absorption, terrain maps, and the
  close-out record: full suite + linters + exported-surface diff, ≤40-line
  anchored testimony), `lens-hunter` (haiku — one lens over named
  targets, strict PRESENT/ABSENT/AMBIGUOUS grammar), `craftsman`
  (sonnet — last resort for one pinned indivisible change, tests-first,
  public-API surface frozen). There is no judge tier: verification is
  absorption; every ruling is the orchestrator's.
- **Skill** `starve`: the orchestrator doctrine (two-turn hunt —
  terrain then targeted lenses, with the terrain ruling as a load-bearing
  GATE: quoted lens targets → FULL mode, core lenses never deselected;
  quoted absences on pure greenfield → BUILD mode, ceremony stands down
  and the leader builds under the diet alone; doubt → FULL. Capped ruled
  checklist with no line left unruled; out-of-mandate has a cost; close
  via scout record + surface diff in every mode).
- **Commands**: `/nullius:hunt`, `/nullius:close`, `/nullius:compact`,
  `/nullius:quick on|off|status`, `/nullius:diet on|off|status`.
- **Terrain cache**: the close writes `.nullius/terrain.md` (commit-stamped,
  ≤60 lines); the next session's Turn A validates it with one scout and
  re-maps only the git drift — session 2+ stops re-paying absorption.
- **Compaction survival**: a plugin cannot trigger or shape compaction
  (upstream #37307/#58538, both closed "not planned"; `PreCompact` can only
  block, never rewrite the summary). So nullius writes the record instead:
  `/nullius:compact` distills the session into `.nullius/ledger.md`
  (MANDATE/RULED/UNRULED/FACTS/VERIFICATION/RISKS/UNKNOWN/ASSUMED/NEXT) and
  the `compact-reinject` hook — `SessionStart` with `matcher: "compact"` —
  reads it back into the fresh context, declaring the ledger authoritative
  over the lossy summary. This covers *auto*-compaction, which a nudge to
  the user cannot. No ledger on disk? The hook says so loudly rather than
  letting the model mistake a summary for a record. **No copy-paste in the
  loop**: the ctx-sentinel tells the model to invoke `nullius:compact` itself
  (plugin commands are model-invocable via the Skill tool), and `bin/nullius`
  exports `CLAUDE_CODE_AUTO_COMPACT_WINDOW=200000` so compaction fires by
  itself just past the 128k knee instead of at the model's ~967k ceiling —
  the one place the platform still requires a human is triggering `/compact`
  on demand, and lowering the window removes the need to. A ledger written in
  the last 10 minutes suppresses further nudges. And because a nudge is only
  advice — measured twice: the model kept serving the user's task, compaction
  fired, the record was lost — there is a **ledger gate** in the governor: past
  the knee with no fresh ledger, context-FILLING calls (Read/Grep/Glob/Web/
  Agent/MCP) are denied until `nullius:compact` runs. Write/Edit/Bash stay open
  so the ledger can be written under the gate; `#nullius:ok`, `.nullius-off`
  and `NULLIUS_OFF=1` still escape. The gate accepts a ledger up to 30 min old
  (it guarantees a record EXISTS before compaction); the sentinel's own
  suppression window is 10 min, so it keeps asking for refreshes.
- **Tests**: `node --test hooks/*.test.mjs` — behavioral suite piping synthetic
  PreToolUse payloads through the governor (rewrite validity, exit-code
  preservation, no auto-approve, gates, ledger, craftsman markers, QUICK
  mode + expiry, MCP gate, telemetry).
- **Tracker**: [ROADMAP.md](ROADMAP.md) — day-to-day blind spots, ranked,
  with measured basis and status.

## Lessons from the measured runs baked in

| Measured failure (benchmarks + pi-nullius runs) | Countermeasure here |
|---|---|
| Leaders ignore system-prompt "should hunt" | `/nullius:hunt` mechanizes the fan-out; skill makes it step 0 |
| 168-item checklist flood → churn, 2/6 fixes | 40-item cap, mechanically-certain ABSENTs ranked first |
| Only mechanically-decided findings got fixed | lens-hunter's strict quote-or-AMBIGUOUS grammar |
| Rubber-stamp adjudication (74/370 free dismissals) | no-line-left-unruled dispositions; out-of-mandate rulings must quote the mandate |
| Green DoD, hidden 0/6: leader deleted public `AppendToHead` | craftsman public-surface freeze + surface diff in the close-scout record |
| rep 3: softened hunt went 5/6 — sse-premature-clear silent again | two-turn hunt: terrain sharpens aim, core lenses never deselected, fault-survival unconditional |
| greenfield-ledger: full process on empty lens terrain = +78% cost, 2.3× wall, identical 29/29 quality vs plain | terrain gate: quoted absences → BUILD mode (ceremony stands down, diet stays); doubt → FULL |
| Context bloat from re-reads and command floods | duplicate-read ledger, tail-bounding, whole-read cap |
| Judgment delegated downtier capped at 3/6 | orchestrator rules on decisive lines itself; agents never decide |
| 7 craftsman dispatches = $4.58 of a $13.27 run (rep 1); published consensus: delegated writes are the tax | craftsman demoted to last resort — intelligence fans out, writes stay home |
| Size-cap deny fires *after* the model generated the file → double-billing; crossover (~1,800 lines cold / ~130 lean, Opus leader) needs vars a hook can't see (measured 2026-07-19) | removed Write/Edit size caps; delegate-or-write is a pre-generation doctrine decision; hook keeps only correctness gates (tests-first, boundary) |

## Install (local)

```
/plugin marketplace add /home/jgonc/Personal/repos/nullius/cc-nullius
/plugin install nullius@nullius-local
```

Best paired with a top-tier orchestrator model (`/model`) — the whole
economics is judgment uptier, bulk downtier.
