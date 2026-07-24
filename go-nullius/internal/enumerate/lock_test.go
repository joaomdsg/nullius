package enumerate

import "testing"

func TestLockWithoutReleaseFlagsMissingUnlock(t *testing.T) {
	src := `package p

import "sync"

type S struct{ mu sync.Mutex }

// leak: Lock, no Unlock anywhere in the function.
func (s *S) Bad() {
	s.mu.Lock()
	s.mu.Lock()
}

// safe: defer Unlock.
func (s *S) GoodDefer() {
	s.mu.Lock()
	defer s.mu.Unlock()
}

// safe: direct Unlock.
func (s *S) GoodDirect() {
	s.mu.Lock()
	s.mu.Unlock()
}
`
	f := parseSrc(t, src)
	got := linesOfLens(LockWithoutRelease(f))

	// The two Lock acquires in Bad (lines 9,10) must be flagged; the safe methods must not.
	if !got[9] || !got[10] {
		t.Errorf("both unreleased Lock acquires must flag; got %v", got)
	}
	for l := range got {
		if l >= 14 { // anything in GoodDefer/GoodDirect
			t.Errorf("safe method line %d must NOT flag", l)
		}
	}
}

func TestLockWithoutReleaseRWMutexPairs(t *testing.T) {
	// RLock is paired with RUnlock, not Unlock — an RLock released only by Unlock still flags,
	// and one released by RUnlock does not.
	src := `package p

import "sync"

type S struct{ mu sync.RWMutex }

func (s *S) BadRead() {
	s.mu.RLock()
}

func (s *S) GoodRead() {
	s.mu.RLock()
	defer s.mu.RUnlock()
}
`
	f := parseSrc(t, src)
	got := linesOfLens(LockWithoutRelease(f))
	if !got[8] {
		t.Errorf("RLock without RUnlock must flag (line 8); got %v", got)
	}
	if got[12] || got[13] {
		t.Errorf("RLock+defer RUnlock must NOT flag; got %v", got)
	}
}

func TestLockWithoutReleaseRegisteredWithEvidence(t *testing.T) {
	f := parseSrc(t, "package p\nimport \"sync\"\ntype S struct{ mu sync.Mutex }\nfunc (s *S) B() { s.mu.Lock() }\n")
	base, err := DefaultRegistry().BuildBaseline("go", f.Lang)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, l := range base {
		if l.ID() == "lock-without-release" {
			found = true
			cs := l.Enumerate(f)
			if len(cs) != 1 {
				t.Fatalf("expected 1 unreleased lock, got %d", len(cs))
			}
			if !cs[0].OnLens(cs[0].Line) {
				t.Errorf("candidate must implicate its own site via Evidence")
			}
		}
	}
	if !found {
		t.Fatal("lock-without-release not registered in the go baseline")
	}
}

func linesOfLens(cs []Candidate) map[int]bool {
	m := map[int]bool{}
	for _, c := range cs {
		m[c.Line] = true
	}
	return m
}
