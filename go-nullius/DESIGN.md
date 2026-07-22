# go-nullius v0 — design contract

A ground-up Go agent encoding the nullius methodology, talking to Claude
directly over the Messages API on a Teams subscription (OAuth + extra
usage credits — the same path pi uses, verified working). Scoped
2026-07-22 from the pi-nullius v0.2 review, the repo-wide experiment
sweep, and the harness scoping discussion. This doc is the v0 contract —
the build is judged against it.

## Transport (settled — corrected)

pi proves the path: subscription OAuth against `/v1/messages`
(`Authorization: Bearer <token>` + `anthropic-beta: oauth-2025-04-20`),
billed to Teams extra-usage credits. go-nullius therefore **owns the
agent loop** — and that is the point: the two things no hook-based port
(cc or pi) can do become possible only here:

1. **Pre-generation gating** — the governor shapes the request BEFORE
   tokens are paid for. No post-hoc denials, no resend double-bills
   (the measured flaw that killed cc's size caps and pi's waive).
2. **Context surgery** — evict tool results the leader has already ruled
   on, keep the ledger at the context edge, enforce "your context is the
   bill" by construction instead of by steering.

This is also the experiment the harness IS: prevention-vs-compaction /
structure-vs-judgment, run for real (context-econ's named missing A/B).

**Auth v0:** reuse an existing subscription OAuth credential store (pi's
or Claude Code's) — read access token, refresh via refresh token on 401.
`ANTHROPIC_API_KEY` env as the fallback for API-billed runs. Own OAuth
flow is layer 2; don't build a login UX in v0.

## Shape

```
go-nullius (one static binary, anthropic-sdk-go)
├── agent loop     — manual tool-use loop, streaming, exit conditions
├── leader tools   — read · edit · bash · scout · hunt · rule · close
├── governor       — pre-dispatch gate + context editor (in the loop)
└── scout runner   — nested go-nullius -p (haiku), read-only tool set
```

Leader model: session default (opus-tier via sub). Scouts: haiku.
Effort: LOW documented as the recommended leader default (measured:
nullius-low ≈ plain-HIGH quality at 26% cost).

## Minimum behaviors

1. **Agent loop.** Manual tool-use loop over `/v1/messages` (streaming;
   parallel tool_use blocks executed concurrently, all results in ONE
   user message). Handles `refusal`, `max_tokens`,
   `model_context_window_exceeded`, `pause_turn`. LEADER_RULES as the
   frozen system prompt with a `cache_control` breakpoint; volatile
   context after it (prompt-caching discipline from day one — cache
   reads are 0.1×, and turns × residency is the measured bill).
2. **Four workspace tools, hardened where it's cheap:** read (ranged,
   whole-read cap ≥ enforced at the TOOL, not a hook), edit (staleness
   check: reject writes if file changed since last read — mtime), bash
   (timeout, output cap WITH truncation marker and REAL exit code — the
   boundCommand lesson inverted: bounding happens in the tool result
   builder, never in the command), glob/grep folded into scout or bash.
3. **scout** — nested `go-nullius -p --model haiku` subprocess,
   read-only tools, one narrow dispatch, capped anchored report. Ports
   pi's paid-for hardening: idle-kill, byte cap, exit-code-as-verdict,
   retry classification (429/529/limit = RETRYABLE w/ backoff; else
   surface — nullius-build's exit-75 lesson).
4. **hunt** — prose-lens haiku dispatches, NO tree-sitter in v0 (the
   measured 6/6 arc came from lensed hunters + quoted-mechanism V|
   grammar, not the scanner). recon scout names terrain per lens → one
   hunter per lens → refuters attack every ABSENT → checklist. Findings
   AND rulings durable in `.nullius/hunt.json` keyed by
   `sha1(file+lens+fn+snippet-head)`; re-hunt filters ruled items,
   resurfaces re-appearing `fixed` as REGRESSED, never re-bills ruled
   work.
5. **rule** — mechanical quote-verify (evidence min 20 chars, honest
   failure message).
6. **close** — refuses with open checklist items; ONE scout runs suite +
   linters from clean, `git diff` AND `git status --short` (untracked
   new test files are in the record — the diff-capture lesson), flags
   0-byte source files; emits the STATUS/FACTS/RISKS/UNKNOWN/ASSUMED
   skeleton pre-filled with the verbatim record. Leader rules; scout
   reports. False-greens are the measured failure class.
7. **Governor, in-loop (the reason this harness exists):**
   - pre-dispatch: heavy-cmd and wide-search route to scout (denial =
     tool result teaching the route, costs one turn, never a
     regeneration);
   - context editing: after a checklist item is ruled, its supporting
     tool results are evicted from history (summarized to one line);
     dup-read served from the still-resident copy instead of denied;
   - the ledger (checklist + gap ledger) is re-rendered at the TAIL of
     the message list every turn — recitation by construction.
8. **Telemetry:** `.nullius/stats-<session>.json`, atomic tmp+rename —
   denies, routes, evictions, scout runs/cost, tokens by tier. Stats
   file is truth, not model self-report.

## Deliberately dropped from v0

Edit size/ratchet gates (wrong enforcement shape until measured
in-loop), compactor (context editing above is the prevention bet; add
compaction only if eviction proves insufficient), ctx-sentinel (the
loop can just… manage context), advisor tier, TUI (plain stdout +
`-p` mode), MCP client, sessions beyond a JSONL transcript, own OAuth
login flow, tree-sitter (layer 2), recursion (nullius-build exists).

## Verify at build time (never trust recall)

- The OAuth wire contract pi actually uses: header set, token refresh
  endpoint/shape, which models the sub grants (incl. haiku for scouts)
  — read pi's source (npx cache) and mimic; canary-test the refresh.
- anthropic-sdk-go: streaming accumulator, `auth_token` support,
  cache_control placement — from the SDK repo, not memory.
- Adaptive thinking + effort params for the chosen leader model.

## Size & build order

~3-5k lines Go. Order mirrors risk:
(1) API client + auth reuse + one streamed turn (canary);
(2) agent loop + read/edit/bash with the hardened result builders;
(3) scout runner + retry classes (everything else stands on it);
(4) rule (pure, cold-testable) → hunt + ledger → close;
(5) governor: pre-dispatch routing, then eviction (measure before/after
    — this IS the prevention-vs-compaction experiment, instrument it);
(6) LEADER_RULES + end-to-end acceptance on a seeded-defect fixture
    (vialite-style: known defects in, checklist out).
