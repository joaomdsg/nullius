package ledger

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

// The leader loop runs every tool_use block concurrently, so N parallel
// rule() calls hit the same Ledger at once. Before the mutex this raced
// on the Rulings map ("fatal error: concurrent map writes"). This pins
// the invariant: concurrent Rule/Save/Resolve/CountOpen are safe and no
// write is lost. Run under -race.
func TestLedgerConcurrentRule(t *testing.T) {
	l := &Ledger{Rulings: map[string]Ruling{}, path: filepath.Join(t.TempDir(), "hunt.json")}

	const n = 32
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			f := Finding{File: fmt.Sprintf("f%d.go", i), Lens: "swallowed-errors", Fn: fmt.Sprintf("Fn%d", i), SnippetHead: fmt.Sprintf("head %d", i)}
			l.Rule(f, StatusRefuted, "detail")
			_ = l.Save()
			_, _, _ = l.Resolve(f.Key())
			_ = l.CountOpen()
			_, _ = l.Filter([]Finding{f}) // reads the map concurrently with peers' Rule
			_ = l.Snapshot()
		}(i)
	}
	wg.Wait()

	if got := len(l.Snapshot()); got != n {
		t.Fatalf("after %d concurrent rulings, ledger has %d entries, want %d", n, got, n)
	}
}

// Snapshot and CountOpen are the accessors the leader package uses for the
// checklist render and the close-out gate. Pin their semantics: CountOpen
// counts only open rulings, and Snapshot returns an independent copy
// (mutating it must not disturb the ledger).
func TestSnapshotAndCountOpen(t *testing.T) {
	l := &Ledger{Rulings: map[string]Ruling{}, path: filepath.Join(t.TempDir(), "h.json")}
	l.Rule(Finding{File: "a.go", Fn: "A"}, StatusOpen, "")
	l.Rule(Finding{File: "b.go", Fn: "B"}, StatusOpen, "")
	l.Rule(Finding{File: "c.go", Fn: "C"}, StatusRefuted, "protected")

	if got := l.CountOpen(); got != 2 {
		t.Fatalf("CountOpen = %d, want 2", got)
	}
	snap := l.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("Snapshot size = %d, want 3", len(snap))
	}
	for k := range snap {
		delete(snap, k)
	}
	if l.CountOpen() != 2 {
		t.Fatal("mutating the snapshot disturbed the ledger")
	}
}
