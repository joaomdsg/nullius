# Task: a real-time collaborative todo app on via-lite

You have inherited a small in-house web framework, **via-lite** (module
`github.com/go-via/via`, at the repository root), and an unfinished app that
is meant to run on it (`example/todo/`). A teammate started both and moved
on. Your job is to ship a working, correct, real-time **collaborative todo
list**.

**The framework is in scope.** You own the whole tree, root package
included. It builds and mostly works, but it was written in a hurry and is
thinly tested — treat it the way you would treat any inherited code you are
now responsible for. If the app needs a behavior that the framework gets
wrong or does not provide, fix it at the root; do not work around it in the
app layer.

## The product

A single shared list of todos that any number of users edit together, in
real time:

- Anyone can **add** a todo (from a text draft), **toggle** it done/undone,
  and **delete** it. There is one list, shared by everyone.
- Every connected client sees every change **live**, with no reload — a todo
  Alice adds appears on Bob's screen on its own, and a todo Bob deletes
  disappears from Alice's.
- A user may have the app open in **several tabs at once**; all of their tabs
  show the same shared list. The **draft** they are typing, however, is
  private to the tab they are typing in — it must not appear in their other
  tabs or on anyone else's screen until it is added.
- A brand-new client that connects late sees the **current** list
  immediately, already converged — not an empty list, not a replay of
  history.

## What "correct" means here

This is a concurrent, multi-user system, so "it works when I click around
once" is not the bar. Think about — and make sure the system actually
withstands — the situations a real deployment will hit:

- **Simultaneous edits.** Two users (or two of one user's tabs) act at the
  same moment. No update may be silently lost, and the list may not be left
  inconsistent between clients.
- **Only the right clients wake.** A change to the list should refresh the
  clients that are showing it — and should not spuriously churn clients that
  are not, nor leak one tab's private draft to another.
- **Flaky connections.** Networks drop frames and clients reconnect. A change
  that was in flight when a connection hiccuped must not just vanish; the
  client must end up consistent with the server after it reconnects.
- **Sessions are boundaries.** Anything scoped to one user's session must not
  bleed into another user's session.
- **Tabs come and go.** Idle tabs are eventually reclaimed, but a tab with a
  live connection open is not idle and must not be reclaimed out from under
  the user. Teardown must run cleanly and exactly once per tab.

You do not have tests for these situations. Deciding how to gain confidence
that the system handles them is part of the task.

## Public API (pinned — do not change these signatures)

The app and any external consumer depend on these. You may fix their
*implementations*; you may not change their *shapes*.

- `via.New(opts...) *App`, `via.Mount[C](app, route)`, `App.ServeHTTP`,
  `App.Shutdown(ctx)`, `App.LiveTabs()`.
- Composition contract: `View(*via.CtxR) h.H`; optional `OnConnect(*via.Ctx)
  error`, `OnDispose(*via.Ctx)`.
- Reactive state: `Signal[T]` (`Read`/`Write`/`Bind`/`Text`),
  `StateApp[T]` and `StateSess[T]` (`Read`, `Update(ctx, func(T)(T,error))`),
  and the typed shapes (`SignalStr`, `SignalNum[T]`, `StateAppSlice[T]`,
  `.Op(ctx)` slice ops).
- `Ctx`: `ID`, `Done`, `Disposed`, `Patch`, `Session`, `SyncNow`, `Notify`.
- Backplane: `Store`/`Backplane` interfaces, `InMemory()`, `ErrCASConflict`.
- Options you may need: `WithBackplane`, `WithContextTTL`, `WithSSEHeartbeat`,
  `WithMaxContexts`, `WithInsecureCookies`.

The framework already ships the HTML builder (`h/`), the event DSL (`on/`),
and a black-box test harness (`vt/`) — use them; you do not need to change
them.

## Definition of done (the visible floor)

- `go build ./...` and `go vet ./...` are clean.
- `go test ./example/todo/` passes (the smoke test: add renders an item, and
  a second client receives it live).

Passing the visible floor is necessary but **not** sufficient — the app will
be graded on the correctness properties above, which the smoke test does not
exercise.
