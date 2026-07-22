// Package seeded is a defect fixture (vialite-style): known defects in,
// checklist out. Do not fix these — the e2e acceptance test asserts the
// hunt CATCHES them.
package seeded

import "sync"

// Counter is incremented from many goroutines (see Spawn).
// SEEDED DEFECT (lost-updates): Inc is a read-modify-write on shared
// state with no lock held across the cycle.
type Counter struct {
	mu sync.Mutex // present but unused by Inc
	n  int
}

func (c *Counter) Inc() {
	c.n = c.n + 1
}

func (c *Counter) Value() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// Spawn fans out concurrent increments.
func Spawn(c *Counter, k int) {
	var wg sync.WaitGroup
	for i := 0; i < k; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Inc()
		}()
	}
	wg.Wait()
}

// Sender batches messages and flushes them to a remote.
// SEEDED DEFECT (fault-survival): Flush clears the buffer BEFORE the
// send is confirmed — a failed send loses the whole batch.
type Sender struct {
	mu  sync.Mutex
	buf []string
}

func (s *Sender) Enqueue(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf = append(s.buf, msg)
}

func (s *Sender) Flush(send func([]string) error) {
	s.mu.Lock()
	batch := s.buf
	s.buf = nil
	s.mu.Unlock()
	go func() {
		_ = send(batch) // failure silently drops the cleared batch
	}()
}
