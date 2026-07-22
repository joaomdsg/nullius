package leader

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-nullius/internal/governor"
	"go-nullius/internal/ledger"
)

// fakeScout replays scripted reports and records objectives.
type fakeScout struct {
	replies    []string
	objectives []string
	fail       bool
}

func (f *fakeScout) Run(_ context.Context, input json.RawMessage) (string, bool) {
	var in struct{ Objective string }
	_ = json.Unmarshal(input, &in)
	f.objectives = append(f.objectives, in.Objective)
	if f.fail {
		return "boom", true
	}
	r := f.replies[0]
	if len(f.replies) > 1 {
		f.replies = f.replies[1:]
	}
	return r, false
}

func newLedger(t *testing.T) *ledger.Ledger {
	l, err := ledger.Load(filepath.Join(t.TempDir(), "hunt.json"))
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func huntInput(t *testing.T) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"lens": "fault-survival", "targets": []string{"q.go:flush"}})
	return raw
}

const hunterReport = `scanning...
{"file":"q.go","fn":"flush","snippet_head":"buf = nil // before write","verdict":"PRESENT","detail":"buffer cleared before write confirmed at q.go:10"}
{"file":"q.go","fn":"retry","snippet_head":"pending guarded by mu","verdict":"ABSENT","detail":"retry state held until ack at q.go:30"}
noise line`

func TestHuntFilesFindingsAndRefutesAbsents(t *testing.T) {
	l := newLedger(t)
	fs := &fakeScout{replies: []string{hunterReport, "[ab12] UPHELD q.go:30 quote"}}
	h := &HuntTool{Ledger: l, Scout: fs}

	out, isErr := h.Run(context.Background(), huntInput(t))
	if isErr {
		t.Fatal(out)
	}
	if !strings.Contains(out, "2 fresh") || !strings.Contains(out, "PRESENT q.go flush") {
		t.Errorf("checklist rendering wrong:\n%s", out)
	}
	if !strings.Contains(out, "REFUTER REPORT") {
		t.Error("ABSENT finding must trigger the refuter dispatch")
	}
	if len(fs.objectives) != 2 || !strings.Contains(fs.objectives[1], "refuter") && !strings.Contains(fs.objectives[1], "Attack each claim") {
		t.Errorf("want hunter + refuter dispatches, got %d", len(fs.objectives))
	}
	if got := countOpen(l); got != 2 {
		t.Errorf("open items = %d, want 2", got)
	}

	// Re-hunt: identical findings are already on the checklist — dropped.
	fs2 := &fakeScout{replies: []string{hunterReport, "n/a"}}
	h2 := &HuntTool{Ledger: l, Scout: fs2}
	out, _ = h2.Run(context.Background(), huntInput(t))
	if !strings.Contains(out, "0 fresh") || !strings.Contains(out, "2 already-ruled") {
		t.Errorf("re-hunt must not re-bill ruled work:\n%s", out)
	}
	if len(fs2.objectives) != 1 {
		t.Error("no fresh ABSENTs → no refuter dispatch")
	}
}

func TestHuntRegressedResurfaces(t *testing.T) {
	l := newLedger(t)
	f := ledger.Finding{File: "q.go", Lens: "fault-survival", Fn: "flush",
		SnippetHead: "buf = nil // before write", Verdict: "PRESENT"}
	l.Rule(f, ledger.StatusFixed, "pinned by TestFlush")

	fs := &fakeScout{replies: []string{hunterReport, "refuted stuff"}}
	h := &HuntTool{Ledger: l, Scout: fs}
	out, _ := h.Run(context.Background(), huntInput(t))
	if !strings.Contains(out, "1 REGRESSED") {
		t.Errorf("previously-fixed finding must resurface as REGRESSED:\n%s", out)
	}
}

func TestRuleQuoteVerifyGate(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "q.go")
	if err := os.WriteFile(src, []byte("package q\n\nfunc retry() {\n\tmu.Lock() // pending guarded until ack\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	l := newLedger(t)
	f := ledger.Finding{File: src, Lens: "fault-survival", Fn: "retry", SnippetHead: "x", Verdict: "ABSENT"}
	l.Rule(f, ledger.StatusOpen, "on checklist")
	key := f.Key()

	ed := governor.NewEditor()
	ed.NoteRead(src, 0, 0)
	r := &RuleTool{Ledger: l, Ed: ed}

	// refuted with evidence NOT on disk → mechanical rejection.
	raw, _ := json.Marshal(map[string]string{"key": key[:8], "status": "refuted",
		"locator": src, "evidence": "this quote does not exist anywhere in the file"})
	if out, isErr := r.Run(context.Background(), raw); !isErr || !strings.Contains(out, "mechanical verification") {
		t.Errorf("fabricated evidence must be rejected, got %q", out)
	}

	// refuted with the real quote → accepted, evicts the file's results.
	raw, _ = json.Marshal(map[string]string{"key": key[:8], "status": "refuted",
		"locator": src, "evidence": "mu.Lock() // pending guarded until ack"})
	out, isErr := r.Run(context.Background(), raw)
	if isErr {
		t.Fatalf("verified refutation rejected: %q", out)
	}
	if !strings.Contains(out, "open items remaining: 0") {
		t.Errorf("open count wrong: %q", out)
	}
	if ed.IsResident(src, 0, 0) {
		t.Error("ruling must trigger eviction (residency dropped)")
	}

	// fixed without a named test → rejected.
	f2 := ledger.Finding{File: src, Lens: "lost-updates", Fn: "inc", SnippetHead: "y", Verdict: "PRESENT"}
	l.Rule(f2, ledger.StatusOpen, "on checklist")
	raw, _ = json.Marshal(map[string]string{"key": f2.Key()[:8], "status": "fixed"})
	if out, isErr := r.Run(context.Background(), raw); !isErr || !strings.Contains(out, "test") {
		t.Errorf("fixed without a pinning test must be rejected, got %q", out)
	}

	// unknown key → honest error.
	raw, _ = json.Marshal(map[string]string{"key": "ffffffff", "status": "fixed", "detail": "t"})
	if _, isErr := r.Run(context.Background(), raw); !isErr {
		t.Error("unknown key must error")
	}
}

func TestCloseRefusesWithOpenItems(t *testing.T) {
	l := newLedger(t)
	f := ledger.Finding{File: "a.go", Lens: "serialization", Fn: "run", SnippetHead: "z", Verdict: "PRESENT"}
	l.Rule(f, ledger.StatusOpen, "on checklist")

	fs := &fakeScout{replies: []string{"should not be dispatched"}}
	c := &CloseTool{Ledger: l, Scout: fs}
	out, isErr := c.Run(context.Background(), json.RawMessage(`{}`))
	if !isErr || !strings.Contains(out, "REFUSED") {
		t.Errorf("close must refuse with open items, got %q", out)
	}
	if len(fs.objectives) != 0 {
		t.Error("refused close must not dispatch the scout")
	}

	// Rule it, close proceeds with record + skeleton.
	l.Rule(f, ledger.StatusFixed, "pinned by TestRun")
	out, isErr = c.Run(context.Background(), json.RawMessage(`{}`))
	if isErr {
		t.Fatal(out)
	}
	if !strings.Contains(out, "CLOSE SKELETON") || !strings.Contains(out, "STATUS:") {
		t.Errorf("close must return the skeleton, got:\n%s", out)
	}
	obj := fs.objectives[0]
	for _, want := range []string{"go vet ./...", "git diff", "git status --short", "size 0"} {
		if !strings.Contains(obj, want) {
			t.Errorf("close objective missing %q", want)
		}
	}
}

func TestTailRenderCapsAndClears(t *testing.T) {
	l := newLedger(t)
	tail := TailRender(l)
	if tail() != "" {
		t.Error("empty checklist renders nothing")
	}
	f := ledger.Finding{File: "a.go", Lens: "lost-updates", Fn: "inc", SnippetHead: "w", Verdict: "PRESENT"}
	l.Rule(f, ledger.StatusOpen, "on checklist")
	if out := tail(); !strings.Contains(out, "OPEN CHECKLIST (1") || !strings.Contains(out, f.Key()[:8]) {
		t.Errorf("tail render wrong: %q", out)
	}
	l.Rule(f, ledger.StatusFixed, "pinned")
	if tail() != "" {
		t.Error("ruled items leave the recited ledger")
	}
}
