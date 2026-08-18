package drive

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, dir string, argv ...string) string {
	t.Helper()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v: %s", strings.Join(argv, " "), err, out)
	}
	return string(out)
}

// initGitRepo builds a tiny buildable+testable Go module with one commit, so
// execute_test can exercise the real drain safety net against real git/go.
func initGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	run(t, dir, "git", "init", "-q")
	run(t, dir, "git", "config", "user.email", "test@example.com")
	run(t, dir, "git", "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module sample\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package sample\n\nfunc Add(a, b int) int { return a + b }\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	run(t, dir, "git", "add", "-A")
	run(t, dir, "git", "commit", "-q", "-m", "init")
	return dir
}

var goBuildTest = []string{"go", "build", "./..."}
var goTest = []string{"go", "test", "./...", "-race"}

// scriptedWriter applies a fixed sequence of file-content edits, one per
// call to Write — enough to script "write nothing", "write a bad build",
// "write a fix", etc. across drain retries.
type scriptedWriter struct {
	calls int
	steps []func(dir string) error
	err   error
}

func (w *scriptedWriter) Write(ctx context.Context, dir, objective string) (string, error) {
	if w.err != nil {
		return "", w.err
	}
	if w.calls < len(w.steps) {
		step := w.steps[w.calls]
		w.calls++
		if step == nil {
			return "", nil
		}
		return "", step(dir)
	}
	w.calls++
	return "", nil
}

func writeFile(rel, content string) func(dir string) error {
	return func(dir string) error {
		return os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644)
	}
}

func TestExecuteObjectiveHappyPath(t *testing.T) {
	dir := initGitRepo(t)
	w := &scriptedWriter{steps: []func(string) error{
		writeFile("extra.go", "package sample\n\nfunc Double(a int) int { return a * 2 }\n"),
	}}
	res := executeObjective(context.Background(), dir, "sample.go:Double", "add Double", w, goBuildTest, goTest)
	if res.Status != execDone {
		t.Fatalf("expected DONE, got %+v", res)
	}
	if res.Attempts != 1 {
		t.Fatalf("expected 1 attempt, got %d", res.Attempts)
	}
	if res.Diffstat == "" {
		t.Fatal("expected non-empty diffstat")
	}
}

func TestExecuteObjectiveEmptyDiffFails(t *testing.T) {
	dir := initGitRepo(t)
	w := &scriptedWriter{} // never writes anything
	res := executeObjective(context.Background(), dir, "sample.go:X", "do nothing", w, goBuildTest, goTest)
	if res.Status != execFailed {
		t.Fatalf("expected FAILED for empty diff, got %+v", res)
	}
	if res.Attempts != 2 {
		t.Fatalf("expected both retries exhausted, got %d attempts", res.Attempts)
	}
}

func TestExecuteObjectiveBuildFailureReverts(t *testing.T) {
	dir := initGitRepo(t)
	w := &scriptedWriter{steps: []func(string) error{
		writeFile("bad.go", "package sample\n\nfunc broken( {\n"),
	}}
	res := executeObjective(context.Background(), dir, "sample.go:X", "break the build", w, goBuildTest, goTest)
	if res.Status != execFailed {
		t.Fatalf("expected FAILED for build error, got %+v", res)
	}
	if !strings.Contains(res.Detail, "build failed") {
		t.Fatalf("expected build-failed detail, got %q", res.Detail)
	}
	if _, err := os.Stat(filepath.Join(dir, "bad.go")); !os.IsNotExist(err) {
		t.Fatal("expected bad.go to be reverted (removed as new untracked file)")
	}
}

func TestExecuteObjectiveTestFailureReverts(t *testing.T) {
	dir := initGitRepo(t)
	w := &scriptedWriter{steps: []func(string) error{
		writeFile("bad_test.go", "package sample\n\nimport \"testing\"\n\nfunc TestFails(t *testing.T) { t.Fatal(\"boom\") }\n"),
	}}
	res := executeObjective(context.Background(), dir, "sample.go:X", "add a failing test", w, goBuildTest, goTest)
	if res.Status != execFailed {
		t.Fatalf("expected FAILED for test failure, got %+v", res)
	}
	if !strings.Contains(res.Detail, "tests failed") {
		t.Fatalf("expected tests-failed detail, got %q", res.Detail)
	}
}

func TestExecuteObjectiveRetriesOnceAfterWriteError(t *testing.T) {
	dir := initGitRepo(t)
	errW := &errThenFixWriter{fix: writeFile("extra.go", "package sample\n\nfunc Triple(a int) int { return a * 3 }\n")}
	res := executeObjective(context.Background(), dir, "sample.go:Triple", "add Triple", errW, goBuildTest, goTest)
	if res.Status != execDone {
		t.Fatalf("expected DONE after one retry, got %+v", res)
	}
	if res.Attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", res.Attempts)
	}
}

type errThenFixWriter struct {
	calls int
	fix   func(dir string) error
}

func (w *errThenFixWriter) Write(ctx context.Context, dir, objective string) (string, error) {
	w.calls++
	if w.calls == 1 {
		return "", os.ErrInvalid
	}
	return "", w.fix(dir)
}
