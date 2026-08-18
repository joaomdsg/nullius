package drive

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-nullius/internal/dispatch"
	"go-nullius/internal/mandate"
)

// seqAdapter answers each dispatch Objective with the next response in a
// configured sequence, repeating the last entry once exhausted, and records
// every call so tests can assert dispatch counts (e.g. across a resumed
// drive, a completed phase must never be re-dispatched).
type seqAdapter struct {
	responses map[string][]string
	calls     map[string]int
	log       []string
}

func (a *seqAdapter) Name() string { return "seq" }

func (a *seqAdapter) Dispatch(ctx context.Context, req dispatch.Request) (dispatch.Response, error) {
	if a.calls == nil {
		a.calls = map[string]int{}
	}
	a.log = append(a.log, req.Objective)
	seq := a.responses[req.Objective]
	if len(seq) == 0 {
		return dispatch.Response{Text: `{"targets":[]}`}, nil
	}
	idx := a.calls[req.Objective]
	if idx >= len(seq) {
		idx = len(seq) - 1
	}
	a.calls[req.Objective]++
	return dispatch.Response{Text: seq[idx]}, nil
}

func (a *seqAdapter) count(objective string) int { return a.calls[objective] }

// fixedWriter overwrites the same set of files every time it's asked to
// write — enough to script a single-shot "apply the fix" craftsman.
type fixedWriter struct {
	files map[string]string
	calls int
}

func (w *fixedWriter) Write(ctx context.Context, dir, objective string) (string, error) {
	w.calls++
	for rel, content := range w.files {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			return "", err
		}
	}
	return "", nil
}

const vulnBuggy = `package sample

import "sync"

var mu sync.Mutex
var data = map[string]int{}

func Get(k string) int {
	return data[k]
}

func Set(k string, v int) {
	data[k] = v
}
`

const vulnFixed = `package sample

import "sync"

var mu sync.Mutex
var data = map[string]int{}

func Get(k string) int {
	mu.Lock()
	defer mu.Unlock()
	return data[k]
}

func Set(k string, v int) {
	mu.Lock()
	defer mu.Unlock()
	data[k] = v
}
`

const vulnRaceTest = `package sample

import (
	"sync"
	"testing"
)

func TestConcurrentAccess(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); Set("k", 1) }()
		go func() { defer wg.Done(); Get("k") }()
	}
	wg.Wait()
}
`

// seededDefectRepo builds a real git repo with one seeded serialization
// defect (Set/Get race on data without mu), buildable and testable for
// real via go/git — the hermetic FIX-mode fixture.
func seededDefectRepo(t *testing.T) (dir, vulnPath string) {
	t.Helper()
	dir = initGitRepo(t) // reuses the go.mod/main.go/git init from execute_test.go
	vulnPath = filepath.Join(dir, "vuln.go")
	if err := os.WriteFile(vulnPath, []byte(vulnBuggy), 0o644); err != nil {
		t.Fatalf("write vuln.go: %v", err)
	}
	run(t, dir, "git", "add", "-A")
	run(t, dir, "git", "commit", "-q", "-m", "seed defect")
	return dir, vulnPath
}

func TestDriveHermeticFixModeEndToEnd(t *testing.T) {
	dir, vulnPath := seededDefectRepo(t)

	if _, err := mandate.Scaffold(dir, "seeded-fix", "abc123", mandate.InitOptions{
		Intent:   "Fix the unguarded concurrent access to `data` in vuln.go.",
		Files:    []string{vulnPath},
		Headless: true,
	}); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}

	adapter := &seqAdapter{responses: map[string][]string{
		"recon/serialization": {`{"targets":["vuln.go:Get"]}`},
		"gate":                {`{"mode":"FIX","contract":"Every mutating access to data must hold mu.","cards":[]}`},
		"hunt/serialization":  {"V|vuln.go:Get|ABSENT|vuln.go:9|`data[k] = v without mu.Lock`\n"},
		"rule":                {`{"suspects":[{"target":"vuln.go:Get","disposition":"CONFIRMED","intent":"guard Get/Set with mu","test_name":"TestConcurrentAccess","test_sketch":"race two goroutines under -race","blast_radius":"vuln.go only"}]}`},
	}}
	craftsman := &fixedWriter{files: map[string]string{
		"vuln.go":      vulnFixed,
		"vuln_test.go": vulnRaceTest,
	}}

	m := New(Config{Root: dir, Slug: "seeded-fix", Adapter: adapter, Craftsman: craftsman, BuildCmd: goBuildTest, TestCmd: goTest})
	s, err := m.Drive(context.Background())
	if err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if s.Phase != PhaseDone {
		t.Fatalf("expected DONE, got phase %s", s.Phase)
	}
	if craftsman.calls == 0 {
		t.Fatal("expected the craftsman to be invoked")
	}

	p := mandate.Paths(dir, "seeded-fix")
	report, err := os.ReadFile(p.ReportMD)
	if err != nil {
		t.Fatalf("expected report.md to exist: %v", err)
	}
	if !strings.Contains(string(report), "CONFIRMED vuln.go:Get") {
		t.Fatalf("expected confirmed defect in report, got: %s", report)
	}
	if !strings.Contains(string(report), "EXECUTE vuln.go:Get: DONE") {
		t.Fatalf("expected a DONE execute record in report, got: %s", report)
	}
	if _, err := os.Stat(p.CloseMD); err != nil {
		t.Fatalf("expected close.md to exist: %v", err)
	}
	closeContent, _ := os.ReadFile(p.CloseMD)
	if !strings.Contains(string(closeContent), "## build (OK)") {
		t.Fatalf("expected a clean close record, got: %s", closeContent)
	}

	got, err := os.ReadFile(vulnPath)
	if err != nil {
		t.Fatalf("read vuln.go: %v", err)
	}
	if !strings.Contains(string(got), "mu.Lock()") {
		t.Fatalf("expected the fix to actually be applied to vuln.go, got: %s", got)
	}
}

func TestDriveResumabilityDoesNotRedoCompletedPhases(t *testing.T) {
	dir, vulnPath := seededDefectRepo(t)
	if _, err := mandate.Scaffold(dir, "resume-fix", "abc123", mandate.InitOptions{
		Intent: "Fix the race.", Files: []string{vulnPath}, Headless: true,
	}); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}

	adapter := &seqAdapter{responses: map[string][]string{
		"recon/serialization": {`{"targets":["vuln.go:Get"]}`},
		"gate":                {`{"mode":"FIX","contract":"c","cards":[]}`},
		"hunt/serialization":  {"V|vuln.go:Get|ABSENT|vuln.go:9|`no lock`\n"},
		"rule":                {`{"suspects":[{"target":"vuln.go:Get","disposition":"CONFIRMED","intent":"guard","test_name":"TestConcurrentAccess","test_sketch":"race","blast_radius":"vuln.go"}]}`},
	}}
	craftsman := &fixedWriter{files: map[string]string{"vuln.go": vulnFixed, "vuln_test.go": vulnRaceTest}}

	stopAfterHunt := Config{
		Root: dir, Slug: "resume-fix", Adapter: adapter, Craftsman: craftsman,
		BuildCmd: goBuildTest, TestCmd: goTest,
		OnPhaseDone: func(phase string) bool { return phase == PhaseHunt },
	}
	m1 := New(stopAfterHunt)
	s1, err := m1.Drive(context.Background())
	if err != nil {
		t.Fatalf("first Drive: %v", err)
	}
	if s1.Phase != PhaseRule {
		t.Fatalf("expected to stop at RULE (just after HUNT completed), got %s", s1.Phase)
	}
	reconCalls := adapter.count("recon/serialization")
	gateCalls := adapter.count("gate")
	huntCalls := adapter.count("hunt/serialization")
	if reconCalls != 1 || gateCalls != 1 || huntCalls != 1 {
		t.Fatalf("expected exactly 1 call each after first run, got recon=%d gate=%d hunt=%d", reconCalls, gateCalls, huntCalls)
	}

	// Simulate a fresh process: a brand-new Machine loading the same
	// on-disk state, same adapter instance so call counts are observable.
	m2 := New(Config{Root: dir, Slug: "resume-fix", Adapter: adapter, Craftsman: craftsman, BuildCmd: goBuildTest, TestCmd: goTest})
	s2, err := m2.Drive(context.Background())
	if err != nil {
		t.Fatalf("resumed Drive: %v", err)
	}
	if s2.Phase != PhaseDone {
		t.Fatalf("expected resumed run to reach DONE, got %s", s2.Phase)
	}
	if adapter.count("recon/serialization") != reconCalls {
		t.Fatalf("RECON was re-dispatched on resume: %d -> %d", reconCalls, adapter.count("recon/serialization"))
	}
	if adapter.count("gate") != gateCalls {
		t.Fatalf("GATE was re-dispatched on resume: %d -> %d", gateCalls, adapter.count("gate"))
	}
	if adapter.count("hunt/serialization") != huntCalls {
		t.Fatalf("HUNT was re-dispatched on resume: %d -> %d", huntCalls, adapter.count("hunt/serialization"))
	}
}

func TestDriveRefusesToAdvancePastUndisposedSuspect(t *testing.T) {
	dir, vulnPath := seededDefectRepo(t)
	if _, err := mandate.Scaffold(dir, "undisposed", "abc123", mandate.InitOptions{
		Intent: "Fix the race.", Files: []string{vulnPath}, Headless: true,
	}); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	adapter := &seqAdapter{responses: map[string][]string{
		"recon/serialization": {`{"targets":["vuln.go:Get"]}`},
		"gate":                {`{"mode":"FIX","contract":"c","cards":[]}`},
		"hunt/serialization":  {"V|vuln.go:Get|ABSENT|vuln.go:9|`no lock`\n"},
		"rule":                {`{"suspects":[]}`}, // model omits the only suspect entirely
	}}
	m := New(Config{Root: dir, Slug: "undisposed", Adapter: adapter, BuildCmd: goBuildTest, TestCmd: goTest})
	s, err := m.Drive(context.Background())
	if err == nil {
		t.Fatal("expected Drive to error when a suspect is left undisposed")
	}
	if !strings.Contains(err.Error(), "undisposed") {
		t.Fatalf("expected an 'undisposed' error, got: %v", err)
	}
	if s.Phase != PhaseRule {
		t.Fatalf("expected phase to remain RULE (refused to advance), got %s", s.Phase)
	}

	reloaded, err := mandate.LoadState(dir, "undisposed")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if reloaded.Phase != PhaseRule {
		t.Fatalf("expected persisted phase to remain RULE, got %s", reloaded.Phase)
	}
	ok, undisposed := mandate.AllDisposed(reloaded.Checklist)
	if ok || len(undisposed) != 1 {
		t.Fatalf("expected 1 undisposed suspect persisted, got ok=%v undisposed=%+v", ok, undisposed)
	}
}

func TestDriveBlocksOnLayer3CardUntilAnswered(t *testing.T) {
	dir, vulnPath := seededDefectRepo(t)
	if _, err := mandate.Scaffold(dir, "interview-block", "abc123", mandate.InitOptions{
		Intent: "Fix the race.", Files: []string{vulnPath}, Headless: false,
	}); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	adapter := &seqAdapter{responses: map[string][]string{
		"recon/serialization": {`{"targets":["vuln.go:Get"]}`},
		"gate": {`{"mode":"FIX","contract":"c","cards":[
			{"question":"Who owns the lock?","blocks":"exported API surface","layer":3,"found":"f","why_you":"w",
			 "options":[{"text":"caller locks"},{"text":"Get/Set lock internally","recommended":true}]}
		]}`},
		"hunt/serialization": {"V|vuln.go:Get|ABSENT|vuln.go:9|`no lock`\n"},
		"rule":               {`{"suspects":[{"target":"vuln.go:Get","disposition":"CONFIRMED","intent":"guard","test_name":"T","test_sketch":"s","blast_radius":"b"}]}`},
	}}
	m := New(Config{Root: dir, Slug: "interview-block", Adapter: adapter, BuildCmd: goBuildTest, TestCmd: goTest})

	s, err := m.Drive(context.Background())
	if err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if s.Phase != PhaseInterview {
		t.Fatalf("expected to block at INTERVIEW, got %s", s.Phase)
	}
	if adapter.count("hunt/serialization") != 0 {
		t.Fatal("HUNT must not run while a layer-3 card is unanswered")
	}

	doc, err := mandate.ReadDoc(dir, "interview-block")
	if err != nil {
		t.Fatalf("ReadDoc: %v", err)
	}
	doc.Interview = strings.Replace(doc.Interview, "- [ ] A. caller locks", "- [x] A. caller locks", 1)
	doc.Interview = strings.Replace(doc.Interview,
		"- [x] B. Get/Set lock internally  ← recommended",
		"- [ ] B. Get/Set lock internally  ← recommended", 1)
	if err := mandate.WriteDoc(dir, "interview-block", doc); err != nil {
		t.Fatalf("WriteDoc: %v", err)
	}

	s2, err := m.Drive(context.Background())
	if err != nil {
		t.Fatalf("second Drive: %v", err)
	}
	if s2.Phase == PhaseInterview {
		t.Fatal("expected drive to proceed past INTERVIEW once the card is answered")
	}
	if adapter.count("hunt/serialization") != 1 {
		t.Fatalf("expected HUNT to run exactly once after unblocking, got %d", adapter.count("hunt/serialization"))
	}
}

func TestDriveCloseFailureBlocksRatify(t *testing.T) {
	dir, vulnPath := seededDefectRepo(t)
	if _, err := mandate.Scaffold(dir, "close-blocks", "abc123", mandate.InitOptions{
		Intent: "Fix the race.", Files: []string{vulnPath}, Headless: true,
	}); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	adapter := &seqAdapter{responses: map[string][]string{
		"recon/serialization": {`{"targets":["vuln.go:Get"]}`},
		"gate":                {`{"mode":"FIX","contract":"c","cards":[]}`},
		"hunt/serialization":  {"V|vuln.go:Get|PRESENT|vuln.go:9|`mu guards`\n"},
	}}
	m := New(Config{Root: dir, Slug: "close-blocks", Adapter: adapter, BuildCmd: goBuildTest, TestCmd: []string{"false"}})
	s, err := m.Drive(context.Background())
	if err == nil {
		t.Fatal("expected Drive to fail when CLOSE verification fails")
	}
	if s.Phase != PhaseClose {
		t.Fatalf("expected to halt at CLOSE, got %s", s.Phase)
	}
	p := mandate.Paths(dir, "close-blocks")
	if _, serr := os.Stat(p.ReportMD); serr == nil {
		t.Fatal("report.md must not exist after a failed close — an unqualified DONE over a failed suite is the doctrine violation")
	}
	closeContent, _ := os.ReadFile(p.CloseMD)
	if !strings.Contains(string(closeContent), "## test (FAILED)") {
		t.Fatalf("expected failed test recorded in close.md, got: %s", closeContent)
	}

	// Suite fixed → rerun resumes at CLOSE and completes.
	m2 := New(Config{Root: dir, Slug: "close-blocks", Adapter: adapter, BuildCmd: goBuildTest, TestCmd: goTest})
	s2, err := m2.Drive(context.Background())
	if err != nil {
		t.Fatalf("resumed Drive after fixing suite: %v", err)
	}
	if s2.Phase != PhaseDone {
		t.Fatalf("expected DONE after clean close, got %s", s2.Phase)
	}
}

func TestDriveRehuntsUnhuntedOnceThenRules(t *testing.T) {
	dir, vulnPath := seededDefectRepo(t)
	if _, err := mandate.Scaffold(dir, "rehunt", "abc123", mandate.InitOptions{
		Intent: "Fix the race.", Files: []string{vulnPath}, Headless: true,
	}); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	adapter := &seqAdapter{responses: map[string][]string{
		"recon/serialization": {`{"targets":["vuln.go:Get","vuln.go:Set"]}`},
		"gate":                {`{"mode":"FIX","contract":"c","cards":[]}`},
		// Covers only Get, on the first pass AND the in-hunt retry — Set
		// dead-ends UNHUNTED, which used to require manual state surgery.
		"hunt/serialization":   {"V|vuln.go:Get|ABSENT|vuln.go:9|`no lock`\n"},
		"rehunt/serialization": {"V|vuln.go:Set|ABSENT|vuln.go:13|`no lock`\n"},
		"rule": {
			`{"suspects":[{"target":"vuln.go:Get","disposition":"DISMISSED"}]}`,
			`{"suspects":[{"target":"vuln.go:Get","disposition":"DISMISSED"},{"target":"vuln.go:Set","disposition":"DISMISSED"}]}`,
		},
	}}
	m := New(Config{Root: dir, Slug: "rehunt", Adapter: adapter, BuildCmd: goBuildTest, TestCmd: goTest})
	s, err := m.Drive(context.Background())
	if err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if s.Phase != PhaseDone {
		t.Fatalf("expected DONE, got %s", s.Phase)
	}
	if got := adapter.count("rehunt/serialization"); got != 1 {
		t.Fatalf("expected exactly 1 rehunt dispatch, got %d", got)
	}
	if got := adapter.count("rule"); got != 2 {
		t.Fatalf("expected RULE to re-run after the rehunt, got %d rule dispatches", got)
	}
	if s.HuntReentries != 1 {
		t.Fatalf("expected HuntReentries=1 persisted, got %d", s.HuntReentries)
	}
}

func TestDriveRehuntBoundedHaltsOnSecondMiss(t *testing.T) {
	dir, vulnPath := seededDefectRepo(t)
	if _, err := mandate.Scaffold(dir, "rehunt-cap", "abc123", mandate.InitOptions{
		Intent: "Fix the race.", Files: []string{vulnPath}, Headless: true,
	}); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	adapter := &seqAdapter{responses: map[string][]string{
		"recon/serialization":  {`{"targets":["vuln.go:Get","vuln.go:Set"]}`},
		"gate":                 {`{"mode":"FIX","contract":"c","cards":[]}`},
		"hunt/serialization":   {"V|vuln.go:Get|ABSENT|vuln.go:9|`no lock`\n"},
		"rehunt/serialization": {`{"lines":[]}`}, // still never covers Set
		"rule":                 {`{"suspects":[{"target":"vuln.go:Get","disposition":"DISMISSED"}]}`},
	}}
	m := New(Config{Root: dir, Slug: "rehunt-cap", Adapter: adapter, BuildCmd: goBuildTest, TestCmd: goTest})
	s, err := m.Drive(context.Background())
	if err == nil {
		t.Fatal("expected Drive to halt: rehunt is bounded to ONE re-entry")
	}
	if !strings.Contains(err.Error(), "undisposed") {
		t.Fatalf("expected the undisposed-suspects halt, got: %v", err)
	}
	if s.Phase != PhaseRule {
		t.Fatalf("expected halt at RULE, got %s", s.Phase)
	}
	if s.HuntReentries != 1 {
		t.Fatalf("expected exactly one re-entry spent, got %d", s.HuntReentries)
	}
}
