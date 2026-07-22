package leader

import (
	"context"
	"encoding/json"
	"testing"

	"go-nullius/internal/ledger"
)

type okDispatcher struct{}

func (okDispatcher) Run(context.Context, json.RawMessage) (string, bool) {
	return "all commands exit 0", false
}

// TestConsumeClosed pins the compaction sentinel lifecycle: set only by a
// successful close record, consumed exactly once.
func TestConsumeClosed(t *testing.T) {
	led := &ledger.Ledger{Rulings: map[string]ledger.Ruling{}}
	c := &CloseTool{Ledger: led, Scout: okDispatcher{}}

	if c.ConsumeClosed() {
		t.Fatal("sentinel set before any close ran")
	}

	// A REFUSED close (open items) must NOT arm compaction.
	led.Rulings["deadbeefcafe"] = ledger.Ruling{Status: ledger.StatusOpen} // key ≥8 chars: close prints k[:8]
	if out, isErr := c.Run(context.Background(), []byte(`{}`)); !isErr {
		t.Fatalf("close with open items did not refuse: %s", out)
	}
	if c.ConsumeClosed() {
		t.Fatal("REFUSED close armed compaction — the transcript would be dropped mid-mandate")
	}

	// A clean close arms it, once.
	led.Rulings = map[string]ledger.Ruling{}
	if out, isErr := c.Run(context.Background(), []byte(`{}`)); isErr {
		t.Fatalf("clean close errored: %s", out)
	}
	if !c.ConsumeClosed() {
		t.Fatal("clean close did not arm the sentinel")
	}
	if c.ConsumeClosed() {
		t.Fatal("sentinel not consumed on first read")
	}
}

// Flips red if close.Run stops arming the sentinel (compaction would
// silently never fire again).
