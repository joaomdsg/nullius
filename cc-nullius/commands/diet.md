---
description: Toggle the nullius diet governor for this project (on|off|status).
---

Manage the nullius diet governor. Argument: $ARGUMENTS (on | off | status; default status).

- **off**: create the marker file `.nullius-off` in the project root
  (`touch .nullius-off  #nullius:ok`). The governor allows everything
  while it exists. Warn the user the orchestrator is now unstarved.
- **on**: remove `.nullius-off` (`rm -f .nullius-off  #nullius:ok`).
- **status**: report, in one compact block:
  - mode: OFF (`.nullius-off` or `NULLIUS_OFF=1`), QUICK (`.nullius-quick`
    fresh — see /nullius:quick), or ON;
  - knobs: `NULLIUS_MAX_READ` / `NULLIUS_EDITS_PER_TEST` / `NULLIUS_TAIL_LINES`
    / `NULLIUS_CTX_KNEE` (ledger gate + ctx-sentinel)
    (defaults 120 / 4 / 30 / 128k); the read floor halves past the knee;
    Write/Edit have no size cap;
  - session telemetry: `cat "${TMPDIR:-/tmp}"/nullius-stats-*  #nullius:ok`
    (best-effort JSON per session: denies, rewrites, dispatches incl.
    per-agent-type, quick_passes, escapes, `ctx:nudge`, `ledger_gate`,
    `compact:reinject`/`compact:noledger`, and the turn channel's `turn:n` /
    `turn:nudge`). Report the counts and what they say about this session's
    economics — many denies means you are fighting the governor instead of
    routing around it; a high `turn:nudge` against a low `turn:n` means the
    turns are being spent one dispatch at a time.
