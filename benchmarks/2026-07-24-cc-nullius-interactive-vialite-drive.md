# cc-nullius interactive drive vs vialite-todo — loop closure + cost anatomy

**Date:** 2026-07-24 · **Arm:** `bin/nullius` wrapper → interactive `claude` +
plugin **v0.1.10** · **Model:** Fable 5 @ **xhigh** effort (the human env
default), auto mode · **Driver:** hand-driven in a detached tmux session
(kickoff + monitoring via `tmux capture-pane` + the governor stats file), NOT
the headless harness · **Wall:** 42m50s.

First *interactive daily-driver* run of cc-nullius on the injected-defect
vialite bench, done to (a) empirically close the statesess loop after the
v0.1.10 doctrine fix, and (b) watch a normal human-driven session for issues
and cost-increasing behaviors. Scored afterward by overlaying the hidden
catcher suite into the worktree.

## Result: 6/6 fixed · 287/287 hidden · build+vet clean

| Injected defect | catcher | fixed |
|---|---|---|
| cas-lost-update | TestApp_concurrentUpdatesDoNotLoseIncrements | ✅ |
| subscription-overwake | TestApp_/TestUser_writeWakesOnlyTabsThatReadTheKey | ✅ |
| **statesess-cross-session-leak** | **TestUser_writeDoesNotLeakAcrossSessions** | ✅ |
| sse-premature-clear | TestSSE_redelivers/retainsQueued… | ✅ |
| ttl-sweeps-connected | TestSSE_connectedTabSurvivesContextTTLWithoutHeartbeat | ✅ |
| action-not-serialized | TestAction_concurrentPOSTsAreSerializedPerCtx | ✅ |

Full hidden suite `go test ./hidden/` → **287/287**, 0 regressions. `go build`
/ `go vet` clean. Diff: 14 files, +156/−156 (todo app build + framework fixes +
88 lines dead crypto/encoding deleted). `statesess.go` +22 (the `nil`→`sess`
fan-out scope fix).

## The loop closed on statesess

`statesess-cross-session-leak` is the defect the **headless** bench (0.1.9)
shipped **silently** — `broadcastRender(ctx, nil, s.wireKey)` fans a
session-scoped write app-wide. Root cause (transcript-confirmed): the
scope-confinement hunt was mis-aimed at a downstream revs-gate filter and never
examined the fan-out call's scope argument. Fixed in **v0.1.10**: the
scope-confinement lens now demands the scope ARGUMENT at every fan-out call
site vs the enclosing scope tier; a downstream filter is not the confinement.

This drive, on 0.1.10, **surfaced it in terrain** verbatim — *"the
session-scoped state broadcast passes a nil session scope argument at
statesess.go:140"* — and the fix landed and passes its catcher. Chain closed:
miss → root-cause → doctrine fix → ship/reinstall → re-run catches + fixes.

**Caveat — not clean apples-to-apples.** This drive ran Fable-5 @ **xhigh**,
far stronger reasoning than the headless bench's fable-**low**. So 6/6 confirms
0.1.10 surfaces+fixes statesess, but does not isolate doctrine from raw model
strength. The clean confirmation is still a **headless fable-low re-run on
0.1.10**. The terrain scout naming `:140` explicitly is strong evidence the
DOCTRINE did the work, not just the stronger model.

## Cost anatomy (the watch brief)

The headline: **context discipline was flawless and is NOT the cost.**

1. **ctx stayed ≥70% free the entire 43 min** — even through the full todo-app
   build + six framework fixes. The diet held perfectly; all bulk lived in
   scout/hunter throwaway contexts.
2. **The cost driver is REASONING EFFORT, not context.** Fable-5 @ xhigh (the
   human's default) turned the batched build into a single **~23-min /
   ~90k-output-token** turn — ~$4.5 of reasoning on one turn if API-billed. The
   pre-build planning turn alone was 9min/26k tokens.
3. **Interaction — turn-batching × effort concentrates spend.** nullius's
   turn-economy (few big batched turns, correct for cache/residency) multiplied
   by xhigh (huge reasoning per turn) packs cost into long reasoning blocks.
   The two levers are orthogonal: the diet governs CONTEXT, effort governs
   REASONING, and a lean context does nothing to cap the latter.
4. **Trap — switching `/effort` mid-run invalidates the prompt cache.** The
   confirmation modal states it: switching re-reads the full history. When the
   modal fired the expensive build turn was already done, so the re-cache would
   have cost more than low-effort saves on the few remaining close turns — the
   switch was DECLINED (reversibility favored keeping the warm cache). Lesson:
   set effort BEFORE the run, or only switch when substantial expensive turns
   remain.
5. **Governor healthy under a real drive.** 16 denies (all deny→delegate
   steering, zero stalls or loops), 9 `#nullius:ok` escapes (legit local
   builds), 9 dispatches (5 scout / 4 lens-hunter), builds routed to scouts.
   Gap-check correctly **self-resolved** — no spurious questions, because the
   vialite mandate is tightly specified (proceeded on a 4-item layer-2 ledger
   G1–G4). ctx-sentinel fired at the attention knee and appended the `/compact`
   handoff line.

`/cost` on a subscription plan reports usage **%**, not dollars — no clean
per-session dollar figure; the ~$4.5 build-turn figure above is a token-based
estimate at Fable-5 API rates.

## Telemetry (final)

`denies:16 · dispatches:9 (scout:5, lens-hunter:4) · escape:ok:9 · ctx:band:5 ·
ctx:nudge:1`. Terrain surfaced 4/6 defects pre-lens (cas no-retry, over-wake,
sse clear-before-write, statesess nil-scope); staticcheck SA4004 mechanically
corroborated the lost-update.

## Reproduce

Seed `tasks/vialite-todo/skeleton` into a fresh git dir, `bin/nullius <dir>`
(plugin 0.1.10 loaded next session), hand it `prompt.md` as the mandate, drive
via tmux. Score: overlay `tasks/vialite-todo/hidden/` into the worktree, run
each defect's catcher in isolation under `-race`.
