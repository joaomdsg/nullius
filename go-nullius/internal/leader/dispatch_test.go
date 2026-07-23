package leader

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

type fakeSub struct {
	mu    sync.Mutex
	calls []string
	reply string
	isErr bool
}

func (f *fakeSub) Run(_ context.Context, in json.RawMessage) (string, bool) {
	f.mu.Lock()
	f.calls = append(f.calls, string(in))
	f.mu.Unlock()
	return f.reply, f.isErr
}

// dispatch is the mechanical intent layer: the smart leader declares a
// BATCH of subagent tasks, the orchestrator runs them (hunt→ledger,
// scout→report), and hands back ONE curated digest. This pins the
// routing (hunt gets lens+targets, scout gets objective) and the digest.
func TestDispatchBatch(t *testing.T) {
	hunt := &fakeSub{reply: "hunt fault-survival: 2 findings — 2 fresh"}
	scout := &fakeSub{reply: "EXIT:0 all tests pass"}
	d := &DispatchTool{Hunt: hunt, Scout: scout}

	in := `{"tasks":[
		{"kind":"hunt","lens":"fault-survival","targets":["live.go:frame","via.go:pulse"]},
		{"kind":"scout","objective":"run go test ./... -race","tier":"fast"}
	]}`
	out, isErr := d.Run(context.Background(), json.RawMessage(in))
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	// Both results curated into the digest, each labeled by its task.
	if !strings.Contains(out, "hunt fault-survival") || !strings.Contains(out, "all tests pass") {
		t.Fatalf("digest missing a task result:\n%s", out)
	}
	// Routing: the hunt sub-tool received lens+targets; scout got objective.
	if len(hunt.calls) != 1 || !strings.Contains(hunt.calls[0], "fault-survival") || !strings.Contains(hunt.calls[0], "live.go:frame") {
		t.Fatalf("hunt routing wrong: %v", hunt.calls)
	}
	if len(scout.calls) != 1 || !strings.Contains(scout.calls[0], "go test ./... -race") {
		t.Fatalf("scout routing wrong: %v", scout.calls)
	}
}

func TestDispatchEmpty(t *testing.T) {
	d := &DispatchTool{Hunt: &fakeSub{}, Scout: &fakeSub{}}
	if _, isErr := d.Run(context.Background(), json.RawMessage(`{"tasks":[]}`)); !isErr {
		t.Fatal("empty tasks[] must be an error result")
	}
}

// A failing sub-task must be marked in the digest, not silently dropped,
// and must not fail the whole batch.
func TestDispatchMarksSubError(t *testing.T) {
	hunt := &fakeSub{reply: "boom", isErr: true}
	scout := &fakeSub{reply: "ok"}
	d := &DispatchTool{Hunt: hunt, Scout: scout}
	in := `{"tasks":[{"kind":"hunt","lens":"x","targets":["a"]},{"kind":"scout","objective":"y"}]}`
	out, isErr := d.Run(context.Background(), json.RawMessage(in))
	if isErr {
		t.Fatal("a single sub-task error must not fail the batch")
	}
	if !strings.Contains(out, "ERROR") || !strings.Contains(out, "boom") {
		t.Fatalf("sub-task error not surfaced in digest:\n%s", out)
	}
}
