// Package tools implements the leader's workspace tools: read (ranged,
// capped at the tool), edit (staleness-checked), bash (governor-gated,
// timeout + output cap with a truncation marker and the REAL exit code).
package tools

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// Tracker records the mtime observed at each read so edit can reject
// writes to files that changed underneath the model.
type Tracker struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func NewTracker() *Tracker { return &Tracker{seen: map[string]time.Time{}} }

func (t *Tracker) Record(path string, mtime time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.seen[path] = mtime
}

// Last returns the mtime observed at the most recent read of path.
func (t *Tracker) Last(path string) (time.Time, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	mt, ok := t.seen[path]
	return mt, ok
}

// Fresh returns nil only if path was read before AND its mtime is
// unchanged since that read.
func (t *Tracker) Fresh(path string) error {
	t.mu.Lock()
	last, ok := t.seen[path]
	t.mu.Unlock()
	if !ok {
		return fmt.Errorf("%s has not been read this session; read it before editing", path)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !fi.ModTime().Equal(last) {
		return fmt.Errorf("%s changed on disk since it was last read; re-read it before editing", path)
	}
	return nil
}
