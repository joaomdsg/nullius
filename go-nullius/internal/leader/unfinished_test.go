package leader

import (
	"strings"
	"testing"

	"go-nullius/internal/ledger"
)

// Unfinished is the generalized bail-out guard. It must nudge in BOTH the
// pre-hunt gap (no findings yet, no close) and the post-hunt gap (open
// items), and fall silent only once a close-out has actually run — this
// is what the qwen "quit after mapping terrain, 0 findings" failure needs.
func TestUnfinished(t *testing.T) {
	// Pre-hunt: empty ledger, no close → must nudge to proceed.
	led := newLedger(t)
	closer := &CloseTool{Ledger: led}
	if msg := Unfinished(led, closer); msg == "" {
		t.Fatal("pre-hunt (empty ledger, no close) must nudge, got empty")
	}

	// Post-hunt: an open checklist item → must nudge about ruling it.
	led.Rule(ledger.Finding{File: "a.go", Fn: "A"}, ledger.StatusOpen, "on checklist")
	if msg := Unfinished(led, closer); !strings.Contains(msg, "UNRULED") {
		t.Fatalf("open item must nudge about ruling, got %q", msg)
	}

	// A successful close-out ends the mandate: silent even with the hook set.
	closer.closed.Store(true)
	if msg := Unfinished(led, closer); msg != "" {
		t.Fatalf("after close ran, must be silent, got %q", msg)
	}
}
