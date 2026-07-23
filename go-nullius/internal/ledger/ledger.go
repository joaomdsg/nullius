// Package ledger persists hunt findings and their rulings across sessions
// (.nullius/hunt.json). Ruled work is never re-billed: a re-hunt filters
// already-ruled findings, and a finding previously ruled "fixed" that
// reappears is resurfaced as REGRESSED instead of silently re-opened.
package ledger

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Finding is one hunter verdict over one target.
type Finding struct {
	File        string `json:"file"`
	Line        int    `json:"line,omitempty"` // hunter's path:line anchor (quote-gate corrected)
	Lens        string `json:"lens"`
	Fn          string `json:"fn"`
	SnippetHead string `json:"snippet_head"`
	Verdict     string `json:"verdict"` // PRESENT (protection verified) | ABSENT (protection missing = defect) | AMBIGUOUS
	Detail      string `json:"detail"`
}

// Ruling statuses.
const (
	StatusOpen      = "open"
	StatusFixed     = "fixed"
	StatusRefuted   = "refuted"
	StatusOutOfMand = "out-of-mandate"
	StatusRegressed = "REGRESSED"
)

type Ruling struct {
	Finding   Finding   `json:"finding"`
	Status    string    `json:"status"`
	Detail    string    `json:"detail"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Ledger is shared mutable state: the leader loop executes every tool_use
// block concurrently, so parallel rule() calls mutate it from many
// goroutines at once. mu serializes ALL access to Rulings — every method
// that reads or writes the map takes it, and callers must go through those
// methods rather than touching Rulings directly.
// Step statuses for the execution plan the craftsman drains.
const (
	StepPending = "pending"
	StepDone    = "done"
	StepFailed  = "failed"
)

// Step is one unit of the leader's execution plan: a fix or feature slice
// a fast craftsman implements (with its pinning test) in a throwaway
// context, off the leader's thread. The leader adds steps after ruling;
// the drain orchestrator runs one craftsman per pending step.
type Step struct {
	ID        string    `json:"id"`
	Target    string    `json:"target"` // file:symbol / file:line the fix touches
	Intent    string    `json:"intent"` // the change + intended mechanism
	Test      string    `json:"test"`   // the behavior the pinning test must assert
	Status    string    `json:"status"` // pending | done | failed
	Result    string    `json:"result"` // craftsman report / local-test outcome
	UpdatedAt time.Time `json:"updated_at"`
}

type Ledger struct {
	mu      sync.Mutex
	Rulings map[string]Ruling `json:"rulings"`
	Steps   map[string]Step   `json:"steps"`
	path    string
}

var wsRe = regexp.MustCompile(`\s+`)

// Key identifies a finding stably across line drift:
// sha1(file|lens|fn|normalized snippet head).
func (f Finding) Key() string {
	head := wsRe.ReplaceAllString(strings.TrimSpace(f.SnippetHead), " ")
	if len(head) > 80 {
		head = head[:80]
	}
	h := sha1.Sum([]byte(f.File + "|" + f.Lens + "|" + f.Fn + "|" + head))
	return hex.EncodeToString(h[:])
}

// Load reads the ledger at path, returning an empty ledger if absent.
func Load(path string) (*Ledger, error) {
	l := &Ledger{Rulings: map[string]Ruling{}, Steps: map[string]Step{}, path: path}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return l, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, l); err != nil {
		return nil, fmt.Errorf("corrupt ledger %s: %v", path, err)
	}
	if l.Rulings == nil {
		l.Rulings = map[string]Ruling{}
	}
	if l.Steps == nil {
		l.Steps = map[string]Step{}
	}
	l.path = path
	return l, nil
}

// stepKey is a stable content hash so re-planning the same change is
// idempotent (the leader may re-declare a step across audit rounds).
func stepKey(target, intent string) string {
	h := sha1.Sum([]byte("step|" + target + "|" + intent))
	return hex.EncodeToString(h[:])
}

// AddStep records a pending execution-plan step, returning its id. Same
// (target,intent) is idempotent: it keeps the existing step's status so a
// re-plan never resurrects a done step or duplicates work.
func (l *Ledger) AddStep(target, intent, test string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	id := stepKey(target, intent)
	if _, ok := l.Steps[id]; ok {
		return id
	}
	l.Steps[id] = Step{ID: id, Target: target, Intent: intent, Test: test, Status: StepPending, UpdatedAt: time.Now()}
	return id
}

// PendingSteps returns the steps still awaiting a craftsman, in no
// particular order (the drain orchestrator schedules them).
func (l *Ledger) PendingSteps() []Step {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []Step
	for _, s := range l.Steps {
		if s.Status == StepPending {
			out = append(out, s)
		}
	}
	return out
}

// MarkStep resolves a step by id or unique prefix and records its outcome.
func (l *Ledger) MarkStep(prefix, status, result string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	var hits []string
	for k := range l.Steps {
		if strings.HasPrefix(k, prefix) {
			hits = append(hits, k)
		}
	}
	switch len(hits) {
	case 0:
		return fmt.Errorf("no step matches id %q", prefix)
	case 1:
		s := l.Steps[hits[0]]
		s.Status, s.Result, s.UpdatedAt = status, result, time.Now()
		l.Steps[hits[0]] = s
		return nil
	default:
		return fmt.Errorf("step id %q is ambiguous (%d matches)", prefix, len(hits))
	}
}

// SnapshotSteps returns a copy of all steps for safe iteration by readers
// outside this package (drain summary, audit render).
func (l *Ledger) SnapshotSteps() []Step {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Step, 0, len(l.Steps))
	for _, s := range l.Steps {
		out = append(out, s)
	}
	return out
}

// Save writes atomically (tmp+rename in the target directory). The map is
// marshaled under the lock so a concurrent Rule cannot mutate it mid-encode;
// the slow file I/O runs unlocked (concurrent Saves are harmless — each
// writes a valid snapshot and the last atomic rename wins).
func (l *Ledger) Save() error {
	l.mu.Lock()
	raw, err := json.MarshalIndent(l, "", "  ")
	l.mu.Unlock()
	if err != nil {
		return err
	}
	dir := filepath.Dir(l.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".hunt-*.json")
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
	return os.Rename(tmp.Name(), l.path)
}

// Rule records a disposition for a finding.
func (l *Ledger) Rule(f Finding, status, detail string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Rulings[f.Key()] = Ruling{Finding: f, Status: status, Detail: detail, UpdatedAt: time.Now()}
}

// Resolve matches a full key or a unique prefix against the ledger,
// returning the canonical key and its ruling.
func (l *Ledger) Resolve(prefix string) (string, Ruling, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var hits []string
	for k := range l.Rulings {
		if strings.HasPrefix(k, prefix) {
			hits = append(hits, k)
		}
	}
	switch len(hits) {
	case 0:
		return "", Ruling{}, fmt.Errorf("no checklist item matches key %q", prefix)
	case 1:
		return hits[0], l.Rulings[hits[0]], nil
	default:
		return "", Ruling{}, fmt.Errorf("key %q is ambiguous (%d matches); use more characters", prefix, len(hits))
	}
}

// CountOpen returns the number of rulings still marked open.
func (l *Ledger) CountOpen() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, ru := range l.Rulings {
		if ru.Status == StatusOpen {
			n++
		}
	}
	return n
}

// Snapshot returns a shallow copy of the rulings for safe iteration by
// readers outside this package (checklist render, close-out gate).
func (l *Ledger) Snapshot() map[string]Ruling {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make(map[string]Ruling, len(l.Rulings))
	for k, v := range l.Rulings {
		out[k] = v
	}
	return out
}

// Filter splits fresh hunt findings against the ledger: `fresh` was never
// ruled (bill it), `regressed` was ruled fixed but reappeared (resurface
// loudly), and anything ruled refuted/out-of-mandate/open is dropped as
// already-ruled work.
func (l *Ledger) Filter(findings []Finding) (fresh, regressed []Finding) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, f := range findings {
		r, seen := l.Rulings[f.Key()]
		switch {
		case !seen:
			fresh = append(fresh, f)
		case r.Status == StatusFixed:
			regressed = append(regressed, f)
		}
	}
	return fresh, regressed
}
