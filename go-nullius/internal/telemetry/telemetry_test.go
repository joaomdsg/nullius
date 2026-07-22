package telemetry

import (
	"encoding/json"
	"os"
	"sync"
	"testing"
)

func TestUpdatePersistsAtomically(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, "abc123")

	if err := s.Update(func(st *Stats) { st.Routes++; st.Leader.OutputTokens += 500 }); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(dir + "/stats-abc123.json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Routes int       `json:"routes"`
		Leader TierUsage `json:"leader"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Routes != 1 || got.Leader.OutputTokens != 500 {
		t.Errorf("persisted stats: routes=%d leaderOut=%d, want routes=1 leaderOut=500", got.Routes, got.Leader.OutputTokens)
	}
	// No stray tmp files left behind.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("expected exactly the stats file in dir, got %d entries", len(entries))
	}
}

func TestConcurrentUpdates(t *testing.T) {
	s := New(t.TempDir(), "race")
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.Update(func(st *Stats) { st.ScoutRuns++ })
		}()
	}
	wg.Wait()
	if s.ScoutRuns != 50 {
		t.Errorf("ScoutRuns = %d, want 50 (lost update)", s.ScoutRuns)
	}
}
