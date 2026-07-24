package enumerate

import "testing"

func TestUnguardedFieldWriteFlagsMissingLock(t *testing.T) {
	src := `package p

import "sync"

type S struct {
	mu sync.Mutex
	n  int
}

// unguarded: writes s.n with no Lock.
func (s *S) Bad(v int) {
	s.n = v
}

// guarded: Lock present.
func (s *S) Good(v int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n = v
}

// a struct with NO mutex — writes must not flag.
type Plain struct{ n int }

func (p *Plain) Set(v int) { p.n = v }
`
	f := parseSrc(t, src)
	got := linesOfLens(WriteToGuardedFieldWithoutLock(f))

	if !got[12] { // s.n = v inside Bad
		t.Errorf("unguarded write on a mutex-bearing struct must flag (line 12); got %v", got)
	}
	if got[19] { // s.n = v inside Good (locked)
		t.Error("a write under a held lock must NOT flag (line 19)")
	}
	if got[25] { // p.n = v inside Plain.Set (no mutex)
		t.Error("write on a mutex-free struct must NOT flag (line 25)")
	}
	if len(got) != 1 {
		t.Errorf("exactly one write should flag, got %v", got)
	}
}

func TestUnguardedFieldWriteRegistered(t *testing.T) {
	f := parseSrc(t, "package p\nimport \"sync\"\ntype S struct{ mu sync.Mutex; n int }\nfunc (s *S) B(v int){ s.n = v }\n")
	base, err := DefaultRegistry().BuildBaseline("go", f.Lang)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, l := range base {
		if l.ID() == "unguarded-field-write" {
			found = true
			if len(l.Enumerate(f)) != 1 {
				t.Errorf("expected 1 unguarded write, got %d", len(l.Enumerate(f)))
			}
		}
	}
	if !found {
		t.Fatal("unguarded-field-write not registered in the go baseline")
	}
}

func TestGuardedStructsDetectsRWMutex(t *testing.T) {
	f := parseSrc(t, "package p\nimport \"sync\"\ntype A struct{ mu sync.RWMutex; x int }\ntype B struct{ x int }\n")
	g := guardedStructs(f)
	if !g["A"] {
		t.Error("A (has RWMutex) must be guarded")
	}
	if g["B"] {
		t.Error("B (no mutex) must not be guarded")
	}
}
