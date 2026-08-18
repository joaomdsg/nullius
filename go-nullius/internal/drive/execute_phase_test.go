package drive

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-nullius/internal/mandate"
)

func TestRunExecuteNoCraftsmanReportsPlansNotApplied(t *testing.T) {
	m := New(Config{Root: "/nonexistent"})
	results := m.runExecute(context.Background(), &mandate.State{}, []Plan{{Target: "foo.go:Bar", Intent: "fix it"}}, nil)
	if len(results) != 1 || results[0].Status != execFailed {
		t.Fatalf("expected reported-not-applied failure, got %+v", results)
	}
	if !strings.Contains(results[0].Detail, "not applied") {
		t.Fatalf("expected 'not applied' detail, got %q", results[0].Detail)
	}
}

func TestRunExecuteAppliesPlanViaCraftsman(t *testing.T) {
	dir := initGitRepo(t)
	w := &scriptedWriter{steps: []func(string) error{
		writeFile("extra.go", "package sample\n\nfunc Quad(a int) int { return a * 4 }\n"),
	}}
	m := New(Config{Root: dir, Craftsman: w, BuildCmd: goBuildTest, TestCmd: goTest})
	results := m.runExecute(context.Background(), &mandate.State{}, []Plan{{Target: "sample.go:Quad", Intent: "add Quad"}}, nil)
	if len(results) != 1 || results[0].Status != execDone {
		t.Fatalf("expected DONE, got %+v", results)
	}
}

func TestApplyPatchMechanicallyAppliesAndVerifies(t *testing.T) {
	dir := initGitRepo(t)

	// Produce a real unified diff by editing, diffing, then reverting.
	mainPath := filepath.Join(dir, "main.go")
	original, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	modified := strings.Replace(string(original), "func Add(a, b int) int { return a + b }",
		"func Add(a, b int) int { return a + b }\n\nfunc Sub(a, b int) int { return a - b }", 1)
	if err := os.WriteFile(mainPath, []byte(modified), 0o644); err != nil {
		t.Fatalf("write modified: %v", err)
	}
	diff := run(t, dir, "git", "diff")
	run(t, dir, "git", "checkout", "--", "main.go")

	res := applyPatch(context.Background(), dir, Patch{Target: "main.go:Sub", Diff: diff}, goBuildTest, goTest)
	if res.Status != execDone {
		t.Fatalf("expected DONE, got %+v", res)
	}
	got, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if !strings.Contains(string(got), "func Sub(") {
		t.Fatalf("expected patch applied to main.go, got: %s", got)
	}
}

func TestApplyPatchFailsCleanlyOnGarbageDiff(t *testing.T) {
	dir := initGitRepo(t)
	res := applyPatch(context.Background(), dir, Patch{Target: "x", Diff: "not a real diff"}, goBuildTest, goTest)
	if res.Status != execFailed {
		t.Fatalf("expected FAILED for a garbage diff, got %+v", res)
	}
}
