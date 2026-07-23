package leader

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-nullius/internal/ledger"
)

// fakeDeferredHunt satisfies both subTool and deferredHunter: it returns
// canned ABSENTs so the batch-refute path is exercised without real
// subprocesses.
type fakeDeferredHunt struct {
	fakeSub
	absents []ledger.Finding
}

func (f *fakeDeferredHunt) RunDeferred(ctx context.Context, in json.RawMessage) (string, []ledger.Finding, bool) {
	out, isErr := f.fakeSub.Run(ctx, in)
	return out, f.absents, isErr
}

// A dispatch batch with several hunts must refute ALL their ABSENTs in
// ONE scout dispatch — not one refuter round-trip per hunt (the turn tax
// the batch exists to kill).
func TestDispatchBatchesRefutationAcrossHunts(t *testing.T) {
	hunt := &fakeDeferredHunt{
		fakeSub: fakeSub{reply: "hunt ok"},
		absents: []ledger.Finding{
			{File: "a.go", Fn: "F", Verdict: "ABSENT", Lens: "serialization", Detail: "mutex held", SnippetHead: "mu.Lock()"},
		},
	}
	scout := &fakeSub{reply: "[abc] UPHELD a.go:10 mu.Lock()"}
	d := &DispatchTool{Hunt: hunt, Scout: scout}

	out, isErr := d.Run(context.Background(), json.RawMessage(
		`{"tasks":[{"kind":"hunt","lens":"serialization","targets":["a.go:F"]},{"kind":"hunt","lens":"lost-updates","targets":["b.go:G"]}]}`))
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	// Both hunts contributed absents (same fake) → exactly ONE refuter
	// dispatch on the scout, carrying the batched items.
	refuterCalls := 0
	for _, c := range scout.calls {
		if strings.Contains(c, "refuter") {
			refuterCalls++
		}
	}
	if refuterCalls != 1 {
		t.Fatalf("want exactly 1 batched refuter dispatch, got %d (scout calls: %v)", refuterCalls, scout.calls)
	}
	if !strings.Contains(out, "REFUTER REPORT") {
		t.Errorf("digest must carry the refuter report: %q", out)
	}
}

// No ABSENTs → no refuter dispatch at all.
func TestDispatchSkipsRefuterWithoutAbsents(t *testing.T) {
	hunt := &fakeDeferredHunt{fakeSub: fakeSub{reply: "hunt ok"}}
	scout := &fakeSub{reply: "should never run"}
	d := &DispatchTool{Hunt: hunt, Scout: scout}

	d.Run(context.Background(), json.RawMessage(
		`{"tasks":[{"kind":"hunt","lens":"serialization","targets":["a.go:F"]}]}`))
	for _, c := range scout.calls {
		if strings.Contains(c, "refuter") {
			t.Fatalf("refuter dispatched with zero absents: %v", scout.calls)
		}
	}
}

// The standalone hunt tool (leader calling hunt directly, outside a
// batch) still refutes inline — deferral is the batch's optimization,
// not a behavior change for single hunts.
func TestHuntRunStillRefutesInline(t *testing.T) {
	led := newLedger(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n\nfunc F() {\n\tcount++ // no lock\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scout := &fakeSub{reply: "V|a.go:F|ABSENT|a.go:4|`count++ // no lock`"}
	h := &HuntTool{Ledger: led, Scout: scout, Dir: dir}

	out, isErr := h.Run(context.Background(), json.RawMessage(`{"lens":"serialization","targets":["a.go:F"]}`))
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if !strings.Contains(out, "REFUTER REPORT") {
		t.Errorf("standalone hunt must still refute inline: %q", out)
	}
	if len(scout.calls) != 2 {
		t.Errorf("want hunter + refuter = 2 dispatches, got %d", len(scout.calls))
	}
}
