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
	"time"
)

// Finding is one hunter verdict over one target.
type Finding struct {
	File        string `json:"file"`
	Lens        string `json:"lens"`
	Fn          string `json:"fn"`
	SnippetHead string `json:"snippet_head"`
	Verdict     string `json:"verdict"` // PRESENT | ABSENT | AMBIGUOUS
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

type Ledger struct {
	Rulings map[string]Ruling `json:"rulings"`
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
	l := &Ledger{Rulings: map[string]Ruling{}, path: path}
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
	l.path = path
	return l, nil
}

// Save writes atomically (tmp+rename in the target directory).
func (l *Ledger) Save() error {
	raw, err := json.MarshalIndent(l, "", "  ")
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
	l.Rulings[f.Key()] = Ruling{Finding: f, Status: status, Detail: detail, UpdatedAt: time.Now()}
}

// Filter splits fresh hunt findings against the ledger: `fresh` was never
// ruled (bill it), `regressed` was ruled fixed but reappeared (resurface
// loudly), and anything ruled refuted/out-of-mandate/open is dropped as
// already-ruled work.
func (l *Ledger) Filter(findings []Finding) (fresh, regressed []Finding) {
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
