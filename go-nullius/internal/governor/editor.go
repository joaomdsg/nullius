package governor

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/anthropics/anthropic-sdk-go"
)

// LedgerTailPrefix marks the recited-ledger text block so old renderings
// can be found and removed before each call — the ledger lives ONLY at
// the context edge.
const LedgerTailPrefix = "≡NULLIUS-LEDGER≡\n"

// Editor is the in-loop context editor: after a checklist item is ruled,
// the tool results that supported it are evicted from history (replaced
// with a one-line marker); duplicate reads are served from the resident
// copy instead of re-absorbed. This is prevention, not compaction.
type Editor struct {
	mu        sync.Mutex
	ruled     map[string]bool     // file path → ruled (evict its tool results)
	swept     map[string]bool     // files already swept (skip rescans)
	resident  map[string]bool     // read key (path|offset|limit) → still resident
	readsByFP map[string][]string // file path → read keys (cleared on evict)
}

func NewEditor() *Editor {
	return &Editor{
		ruled: map[string]bool{}, swept: map[string]bool{},
		resident: map[string]bool{}, readsByFP: map[string][]string{},
	}
}

// MarkRuled schedules eviction of every tool result supporting file.
func (e *Editor) MarkRuled(file string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ruled[file] = true
	delete(e.swept, file)
	// Evicted content is no longer resident: future reads must hit disk.
	for _, k := range e.readsByFP[file] {
		delete(e.resident, k)
	}
	delete(e.readsByFP, file)
}

// ReadKey builds the residency key for a ranged read.
func ReadKey(path string, offset, limit int) string {
	return fmt.Sprintf("%s|%d|%d", path, offset, limit)
}

// NoteRead records that a read's content is now resident in context.
func (e *Editor) NoteRead(path string, offset, limit int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	k := ReadKey(path, offset, limit)
	e.resident[k] = true
	e.readsByFP[path] = append(e.readsByFP[path], k)
}

// IsResident reports whether an identical read is still in context —
// the caller serves a pointer to the resident copy instead of the bytes.
func (e *Editor) IsResident(path string, offset, limit int) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.resident[ReadKey(path, offset, limit)]
}

// Invalidate drops residency for a file (it changed on disk).
func (e *Editor) Invalidate(path string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, k := range e.readsByFP[path] {
		delete(e.resident, k)
	}
	delete(e.readsByFP, path)
}

// Reset drops all editor state. Called at post-close compaction: the
// transcript the residency registry describes no longer exists, so every
// future read must hit disk again.
func (e *Editor) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ruled = map[string]bool{}
	e.swept = map[string]bool{}
	e.resident = map[string]bool{}
	e.readsByFP = map[string][]string{}
}

// Sweep walks the message history and evicts tool results whose
// supporting file has been ruled: read results by input path, bash
// results whose command mentions the path. Returns evictions performed.
func (e *Editor) Sweep(msgs []anthropic.MessageParam) int {
	e.mu.Lock()
	files := make([]string, 0, len(e.ruled))
	for f := range e.ruled {
		if !e.swept[f] {
			files = append(files, f)
		}
	}
	for _, f := range files {
		e.swept[f] = true
	}
	e.mu.Unlock()
	if len(files) == 0 {
		return 0
	}

	// tool_use id → (name, input) from assistant turns.
	type meta struct{ name, input string }
	byID := map[string]meta{}
	for _, m := range msgs {
		for _, b := range m.Content {
			if tu := b.OfToolUse; tu != nil {
				raw, _ := json.Marshal(tu.Input)
				byID[tu.ID] = meta{tu.Name, string(raw)}
			}
		}
	}

	evicted := 0
	for _, m := range msgs {
		for _, b := range m.Content {
			tr := b.OfToolResult
			if tr == nil {
				continue
			}
			mt, ok := byID[tr.ToolUseID]
			if !ok || (mt.name != "read" && mt.name != "bash") {
				continue
			}
			var hit string
			for _, f := range files {
				if strings.Contains(mt.input, f) {
					hit = f
					break
				}
			}
			if hit == "" || isEvictionMarker(tr.Content) {
				continue
			}
			size := 0
			for _, c := range tr.Content {
				if t := c.OfText; t != nil {
					size += len(t.Text)
				}
			}
			if size < 200 {
				continue // markers and tiny results aren't worth evicting
			}
			tr.Content = []anthropic.ToolResultBlockParamContentUnion{{
				OfText: &anthropic.TextBlockParam{Text: fmt.Sprintf(
					"[evicted after ruling on %s — was %d bytes of %s output; re-read if needed]", hit, size, mt.name)},
			}}
			evicted++
		}
	}
	return evicted
}

func isEvictionMarker(content []anthropic.ToolResultBlockParamContentUnion) bool {
	return len(content) == 1 && content[0].OfText != nil &&
		strings.HasPrefix(content[0].OfText.Text, "[evicted after ruling")
}
