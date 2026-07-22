// Package telemetry writes the session stats file (.nullius/stats-<id>.json).
// The stats file is the record of what the governor and loop actually did —
// truth is the file, never the model's self-report.
package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type TierUsage struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheReadTokens     int64 `json:"cache_read_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_tokens"`
	Requests            int   `json:"requests"`
}

type Stats struct {
	mu        sync.Mutex `json:"-"`
	Session   string     `json:"session"`
	StartedAt time.Time  `json:"started_at"`
	UpdatedAt time.Time  `json:"updated_at"`

	Denies    int `json:"denies"`
	Routes    int `json:"routes"`
	Evictions int `json:"evictions"`
	ScoutRuns int `json:"scout_runs"`
	Turns     int `json:"turns"`

	Leader TierUsage `json:"leader"`
	Scouts TierUsage `json:"scouts"`

	path string
}

// New creates a stats tracker persisting to dir/stats-<session>.json.
func New(dir, session string) *Stats {
	return &Stats{
		Session:   session,
		StartedAt: time.Now(),
		path:      filepath.Join(dir, "stats-"+session+".json"),
	}
}

// Update applies fn under the lock and flushes atomically. Telemetry must
// never kill the run: flush errors are returned but callers may log-and-go.
func (s *Stats) Update(fn func(*Stats)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s)
	s.UpdatedAt = time.Now()
	return s.flushLocked()
}

func (s *Stats) flushLocked() error {
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".stats-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}
