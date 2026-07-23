package leader

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// scriptedCraft plays a scripted reply per call and can mutate the
// workspace as a real craftsman would — the drain's mechanical evidence
// layer (diff verification, revert, retry) is meaningless against a fake
// that never touches disk.
type scriptedCraft struct {
	mu      sync.Mutex
	calls   []string
	replies []string
	effects []func() // optional per-call workspace mutation
}

func (f *scriptedCraft) Run(_ context.Context, in json.RawMessage) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := len(f.calls)
	f.calls = append(f.calls, string(in))
	if i < len(f.effects) && f.effects[i] != nil {
		f.effects[i]()
	}
	if i < len(f.replies) {
		return f.replies[i], false
	}
	return "RESULT: FAILED script exhausted", false
}

// A DONE claim with an empty diff is a lie: the drain must fail it
// mechanically instead of trusting the RESULT string — this is the
// false-green hole at the tier boundary.
func TestDrainFailsDoneClaimWithEmptyDiff(t *testing.T) {
	dir := gitRepo(t)
	led := newLedger(t)
	led.AddStep("a.txt:thing", "change it", "asserts change")

	craft := &scriptedCraft{replies: []string{
		"RESULT: DONE totally fixed it, trust me",
		"RESULT: DONE for real this time",
	}}
	d := &DrainTool{Ledger: led, Craftsman: craft, Dir: dir}
	out, _ := d.Run(context.Background(), json.RawMessage(`{}`))

	steps := led.SnapshotSteps()
	if steps[0].Status != "failed" {
		t.Fatalf("empty-diff DONE must fail: %+v", steps[0])
	}
	if !strings.Contains(out, "wrote nothing") {
		t.Errorf("summary must name the empty-diff lie: %q", out)
	}
	// One retry with the failure fed back, then surface.
	if len(craft.calls) != 2 {
		t.Fatalf("want retry-once (2 calls), got %d", len(craft.calls))
	}
	if !strings.Contains(craft.calls[1], "PRIOR ATTEMPT FAILED") {
		t.Errorf("retry brief must carry the failure: %q", craft.calls[1])
	}
}

// already-satisfied is the legitimate empty-diff DONE (a prior serial step
// did the work) — it must pass, not be punished by the diff check.
func TestDrainAllowsAlreadySatisfiedEmptyDiff(t *testing.T) {
	dir := gitRepo(t)
	led := newLedger(t)
	led.AddStep("a.txt:thing", "change it", "asserts change")

	craft := &scriptedCraft{replies: []string{"RESULT: DONE already-satisfied verified mutex present"}}
	d := &DrainTool{Ledger: led, Craftsman: craft, Dir: dir}
	d.Run(context.Background(), json.RawMessage(`{}`))

	if s := led.SnapshotSteps()[0]; s.Status != "done" {
		t.Fatalf("already-satisfied must stay done: %+v", s)
	}
}

// A failed step's partial edits are reverted so the next serial craftsman
// starts clean.
func TestDrainRevertsFailedStepEdits(t *testing.T) {
	dir := gitRepo(t)
	led := newLedger(t)
	led.AddStep("a.txt:thing", "change it", "asserts change")

	junk := filepath.Join(dir, "junk.go")
	craft := &scriptedCraft{
		replies: []string{"RESULT: FAILED could not", "RESULT: FAILED still no"},
		effects: []func(){
			func() { os.WriteFile(junk, []byte("partial"), 0o644) },
			func() { os.WriteFile(junk, []byte("partial2"), 0o644) },
		},
	}
	d := &DrainTool{Ledger: led, Craftsman: craft, Dir: dir}
	d.Run(context.Background(), json.RawMessage(`{}`))

	if _, err := os.Stat(junk); !os.IsNotExist(err) {
		t.Error("failed step's partial edits must be reverted")
	}
}

// First attempt fails, retry succeeds with a real write: step lands done
// and the summary carries the diffstat evidence.
func TestDrainRetryOnceRecovers(t *testing.T) {
	dir := gitRepo(t)
	led := newLedger(t)
	led.AddStep("a.txt:thing", "change it", "asserts change")

	craft := &scriptedCraft{
		replies: []string{"RESULT: FAILED syntax error", "RESULT: DONE fixed, test green EXIT:0"},
		effects: []func(){nil, func() {
			os.WriteFile(filepath.Join(dir, "a.txt"), []byte("fixed\n"), 0o644)
		}},
	}
	d := &DrainTool{Ledger: led, Craftsman: craft, Dir: dir}
	out, _ := d.Run(context.Background(), json.RawMessage(`{}`))

	if s := led.SnapshotSteps()[0]; s.Status != "done" {
		t.Fatalf("recovered retry must be done: %+v", s)
	}
	if !strings.Contains(out, "a.txt") {
		t.Errorf("summary must carry the diffstat evidence: %q", out)
	}
}

// The default mechanical verification builds the WHOLE module before the
// package test: a step that breaks a package it does not test in (import
// cycle, renamed symbol) must fail its own drain step, not surface at
// close.
func TestGoTestStepBuildsWholeModule(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module drainmod\n\ngo 1.22\n")
	write("good/good.go", "package good\n\nfunc Ok() int { return 1 }\n")
	write("broken/broken.go", "package broken\n\nfunc Boom() { undefinedSymbol() }\n")

	// The touched package (good) is fine; the module is not.
	out, err := goTestStep(context.Background(), dir, "good")
	if err == nil || !strings.Contains(err.Error(), "module-wide build RED") {
		t.Fatalf("broken sibling package must fail the step; err=%v out=%s", err, out)
	}

	// Fix the sibling: whole-module build green, package test runs.
	write("broken/broken.go", "package broken\n\nfunc Boom() {}\n")
	if _, err := goTestStep(context.Background(), dir, "good"); err != nil {
		t.Fatalf("clean module must pass: %v", err)
	}
}

// A DONE with a real diff still gets the mechanical test run; a red run
// overrides the claim.
func TestDrainMechanicalTestRunOverridesDone(t *testing.T) {
	dir := gitRepo(t)
	led := newLedger(t)
	led.AddStep("a.txt:thing", "change it", "asserts change")

	craft := &scriptedCraft{
		replies: []string{"RESULT: DONE trust me", "RESULT: DONE trust me again"},
		effects: []func(){
			func() { os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x\n"), 0o644) },
			func() { os.WriteFile(filepath.Join(dir, "a.txt"), []byte("y\n"), 0o644) },
		},
	}
	var ran int
	d := &DrainTool{Ledger: led, Craftsman: craft, Dir: dir,
		TestCmd: func(_ context.Context, _ string, _ string) (string, error) {
			ran++
			return "--- FAIL: TestThing", errors.New("exit 1")
		}}
	out, _ := d.Run(context.Background(), json.RawMessage(`{}`))

	if ran == 0 {
		t.Fatal("mechanical test run never invoked on DONE-with-diff")
	}
	if s := led.SnapshotSteps()[0]; s.Status != "failed" {
		t.Fatalf("red mechanical run must override DONE: %+v", s)
	}
	if !strings.Contains(out, "FAIL") {
		t.Errorf("summary must carry the red test evidence: %q", out)
	}
}
