Work in the current directory (Go library "via", a reactive web framework). Rework the existing Broadcast API into this unified scoped design:

**Unified scoped Broadcast**
- One entry point: `Broadcast(ctx context.Context, p Patch, opts ...BroadcastOption) (BroadcastReceipt, error)` on the App (or equivalent receiver matching house style — study the existing broadcast.go first).
- Scoping options: `ToRoute(route)`, `ToSession(sid)`, `ToTabs(pred)` (predicate over tabs), and `ToTopic(topic)` implemented over the predicate path. `Ctx.Subscribe(topic)` is the only new subscription surface.
- Patch constructors: `NotifyPatch(...)` and `SignalsPatch(...)` as one-liners. Script delivery exists only as `via.UnsafeScriptPatch(js)`. The old raw script-string Broadcast signature is DELETED, not renamed or kept as a deprecated alias — update every in-tree caller and test that used it.
- Receipt is honest and pod-local: `BroadcastReceipt{LocalTabs int, Appended bool}` — LocalTabs = tabs actually delivered to on this pod; Appended = whether the patch was appended to the backplane.
- The ctx flows into the backplane Append with a 5-second default deadline; a stalled/blocked backplane must surface as a returned error, not a hang.
- Fan-out uses a bounded worker pool (do not spawn a goroutine per tab).

Where the roadmap references machinery absent in-tree (viavet linting, drain workers, metrics counters, production.md), implement the minimal in-scope equivalent or skip it — the deliverable is the API + behavior below, not those subsystems.

Definition of done — all six of these tests written by you and green, plus `go vet ./` clean and `go test -race ./` green for the root package, with all pre-existing tests still passing (callers of the old API updated, not deleted):
TestApp_broadcastDeliversPatchToAllTabs, TestApp_broadcastScopesToRoute, TestApp_broadcastIsolatesTopicsAcrossSubscribers, TestApp_broadcastHonorsContextCancellation, TestApp_broadcastReceiptReportsLocalCountAndAppend, TestBroadcast_returnsErrorWhenBackplaneStalls.

Follow the repository's CONVENTIONS.md (test-first, behavioral test names, outside-in through exported API, real over stub over mock). Study the existing broadcast.go, backplane.go, and their tests before changing anything. Keep the diff as small as correctness allows. Do NOT write a browser test.
