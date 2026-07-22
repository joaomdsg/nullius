# go-nullius v0 — build state (checkpoint 1)

Verified facts captured on disk so the build survives context compaction. Judged
against `DESIGN.md`. Auth store decision: **Claude Code's** (`~/.claude/.credentials.json`),
fall through to `ANTHROPIC_API_KEY`. Live canary: approved.

## DONE (stage 4 cold-testable core — compiles + `go test ./... -race` green, `go vet` clean)

- `internal/governor/classify.go` (+test) — pre-dispatch gate. `Gate(cmd)` → Verdict{Allow,Route,Reason}.
  Heavy (unbounded build/test/lint) and wide-search route to scout; bounded (final pipe = tail/head/wc/grep -c) passes.
- `internal/rule/verify.go` (+test) — `Verify(locator, evidence)`: evidence ≥20 chars, exists verbatim on disk
  (whitespace-normalized), within ±20 lines if `path:line` given. Honest specific errors.
- `internal/ledger/ledger.go` (+test) — `.nullius/hunt.json`. Finding.Key = sha1(file|lens|fn|norm snippet head),
  stable across line/whitespace drift. `Filter` → fresh (never ruled) + regressed (was `fixed`, reappeared); drops
  already-ruled. Atomic tmp+rename Save.
- `internal/telemetry/telemetry.go` (+test) — `.nullius/stats-<session>.json`, mutex-guarded, atomic flush.
  Denies/Routes/Evictions/ScoutRuns/Turns + per-tier (leader/scouts) token usage. Race-clean.

go.mod: module `go-nullius`, go 1.24, `github.com/anthropics/anthropic-sdk-go v1.58.1`.

## VERIFIED WIRE CONTRACT (OAuth subscription path)

- Token endpoint: `https://platform.claude.com/v1/oauth/token`
- Refresh body: `{grant_type:"refresh_token", client_id, refresh_token}` → resp `{access_token, refresh_token, expires_in (sec)}`.
  Store expiry as `now + expires_in*1000 - 5min` (ms), refresh early. (pi source: pi-ai/dist/auth/oauth/anthropic.js)
- Messages call: `POST /v1/messages`, header `Authorization: Bearer <token>` **and `anthropic-beta: oauth-2025-04-20`**
  (beta header from claude-api skill — pi source didn't expose it; REQUIRED on /v1/messages or 401).
- Claude Code store `~/.claude/.credentials.json`: `claudeAiOauth.{accessToken, refreshToken, expiresAt(ms),
  refreshTokenExpiresAt, scopes[], rateLimitTier, subscriptionType}`. READ SHAPE ONLY — never print values (CLAUDE.md).

## VERIFIED SDK BINDINGS (anthropic-sdk-go v1.58.1)

- Auth: `option.WithAuthToken(tok)` sets `Authorization: Bearer`. Beta header via `option.WithHeaderAdd("anthropic-beta","oauth-2025-04-20")`.
  Per-client and per-request. NO built-in OAuth/refresh — we own refresh (canary it).
- Streaming: `stream := client.Messages.NewStreaming(ctx, params)`; `msg := anthropic.Message{}`;
  `for stream.Next() { msg.Accumulate(stream.Current()) }`; check `stream.Err()`.
- Tools: response `anthropic.ToolUseBlock{ID,Name,Input}` via `block.AsAny().(type)`; `variant.JSON.Input.Raw()` for raw JSON;
  `anthropic.NewToolResultBlock(id, content, isError)`; all results in ONE `anthropic.NewUserMessage(results...)`.
  `resp.ToParam()` appends assistant turn.
- cache_control: per-block IS supported — `System: []anthropic.TextBlockParam{{Text:..., CacheControl: anthropic.NewCacheControlEphemeralParam()}}`
  (authoritative claude-api skill; the SDK-facts scout's "message-level only" was incomplete). Put LEADER_RULES here, breakpoint after it.
- Thinking: leader is Opus/Fable-tier → `budget_tokens` is REMOVED (400). Use `Thinking: {OfAdaptive:&ThinkingConfigAdaptiveParam{}}`
  or omit; control depth with `OutputConfig` effort ("low" default per DESIGN:44). NOT `ThinkingConfigParamOfEnabled`.
- Stop reasons (constants): `StopReasonEndTurn/MaxTokens/StopSequence/ToolUse/PauseTurn/Refusal`.
  `model_context_window_exceeded` NOT a Go constant — handle by string compare on resp.StopReason if needed.
- Watch: open issues #388/#390/#393 (tool-use deadlocks in runners), #372 (base64 doc OOM ~7×).

## DONE (stages 1-2, live-canary verified 2026-07-22)

1. `internal/auth` — precedence: own store (~/.config/go-nullius/credentials.json, 0600) → CC store → ANTHROPIC_API_KEY.
   Refresh persists ONLY to own store (endpoint rotates refresh tokens; CC's file never written — protects CC login).
   client_id: env NULLIUS_OAUTH_CLIENT_ID → DefaultClientID 9d1c250a-e61b-44d9-88ed-5944d1962f5e (public CC client id;
   ASSUMED — not yet exercised, verified only when a real refresh fires). Live READ canary passed (source=claude-code,
   token valid). Live REFRESH deliberately deferred to a real 401 (speculative refresh would burn CC's refresh token).
2. `internal/api` — Client wrapper: OAuth→WithAuthToken+beta header, env→WithAPIKey; proactive refresh at expiry margin;
   one refresh-and-retry on 401 (streaming: only if zero events consumed). **LIVE STREAM CANARY PASSED** over
   subscription OAuth: end_turn, 12in/4out — NO "You are Claude Code" spoof needed (resolves the UNKNOWN).
   Gotcha: SDK service methods have pointer receivers — snapshot() must return *anthropic.Client (addressable copy).
   Canaries gated by NULLIUS_LIVE_CANARY=1.

## DONE (stages 3-6, 2026-07-22 — v0 COMPLETE, seeded-defect acceptance passed live)

3. `internal/agent` — manual tool-use loop: parallel tool_use run concurrently, ALL results in ONE user msg (block
   order preserved); LEADER_RULES frozen w/ cache_control breakpoint every call; refusal/max_tokens/pause_turn handled
   as constants, model_context_window_exceeded by string compare. Editor hook (pre-call sweep) + Tail hook (ledger
   recited at the context edge, stale renderings stripped every turn).
4. `internal/tools` — read (ranged, 1500-line/64KB cap AT the tool, dup-read served as resident marker), edit (mtime
   staleness via Tracker; creation via empty old_string), bash (governor.Gate pre-dispatch unless Ungated for scouts;
   timeout kills process GROUP; 48KB cap w/ marker; REAL exit code as data, never an error result).
5. `internal/scout` — nested `go-nullius -p <obj> --model haiku --scout --session <id>`: idle-kill watchdog, 32KB
   report cap, retry classes (429/529/overloaded/rate-limit = RETRYABLE ×3 w/ ×4 backoff), exit-code-as-verdict,
   child stats folded into parent Scouts tier from the child's stats FILE.
6. `internal/leader` — hunt (8 prose lenses, JSON-per-line grammar, ledger Filter: fresh→open, fixed-reappeared→
   REGRESSED, ruled dropped; batched refuter attacks every ABSENT), rule (fixed needs pinning test; refuted needs
   locator+evidence passing rule.Verify against disk; triggers editor eviction), close (REFUSES with open items;
   scout record + STATUS/FACTS/RISKS/UNKNOWN/ASSUMED skeleton). `internal/governor/editor.go` — eviction sweep
   (ruled file → tool results replaced with markers, idempotent, tiny results skipped) + residency registry.
   `cmd/go-nullius` — leader/scout modes, model aliases, REPL + -p headless (stdout = final report only).

**ACCEPTANCE (live, 2026-07-22): internal/e2e seeded-defect fixture — real haiku hunters through the real binary
caught BOTH seeded defects (lost-updates Inc, fault-survival Flush) as PRESENT with anchored quotes, 10.4s.**

## DONE (layer 2 — smart post-close compaction, 2026-07-22)

CloseTool arms a consumable sentinel (`ConsumeClosed`, atomic.Bool) on a clean close record only —
REFUSED closes never arm it. On the Run's terminal end_turn the loop stashes the final report (the
close ledger) as the pending compact record; the NEXT Run drops the whole transcript, resets the
editor (residency must not survive — the content it points at is gone), and starts from ONE user
message: `≡NULLIUS-COMPACT≡` header + close ledger verbatim + `=== NEW MANDATE ===` + new prompt.
`Compactions` counter added to telemetry. Doctrine: post-close is the one near-lossless compaction
point — the ledger IS the summary. Side fix: the loop now appends the final assistant turn on
end_turn (previously multi-Run sessions lost the model's own prior answers).
Pinned by: agent/compact_test.go (compact fires after close, NOT without; editor reset exactly once;
ledger verbatim), leader/close_sentinel_test.go (arm-on-clean-only, consume-once), governor
TestEditorReset.

## NEXT (layer 2, post-v0)
- Exercise the live REFRESH path (first real 401) — client_id constant still ASSUMED until then.
- Instrument the prevention-vs-compaction experiment: evictions counter is live; needs an A/B harness + bench arm.
- JSONL transcript persistence; adaptive thinking/effort params for the leader (omitted in v0 — API defaults).
- Wire governor Denies counter (currently only Routes increments — Gate has no non-route deny).

## OPEN GAPS (self-answered ASSUMED, revisit)
- Module path `go-nullius` (not a URL) — fine for a private binary; change to a repo path if published.
- `-p` (headless) flag + `--model` not yet designed; scout runner (stage 5) defines them.
