package drive

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTerrainBlockListsDeclarationsWithLineNumbers(t *testing.T) {
	dir := t.TempDir()
	src := "package foo\n\nfunc Bar() int { return 1 }\n\ntype Baz struct{}\n"
	if err := os.WriteFile(filepath.Join(dir, "foo.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := terrainBlock(dir, []string{"foo.go"}, 1<<20)
	if !strings.Contains(got, "== foo.go") {
		t.Fatalf("missing file header:\n%s", got)
	}
	if !strings.Contains(got, "3: func Bar() int") || !strings.Contains(got, "5: type Baz struct{}") {
		t.Fatalf("missing numbered declarations:\n%s", got)
	}
}

func TestTerrainBlockHonorsBudget(t *testing.T) {
	dir := t.TempDir()
	var files []string
	for _, n := range []string{"a.go", "b.go"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("package p\n\nfunc F() {}\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		files = append(files, n)
	}
	got := terrainBlock(dir, files, 1) // force truncation after the first file
	if !strings.Contains(got, "(terrain budget)") {
		t.Fatalf("expected truncation note:\n%s", got)
	}
}

func TestTargetExistsAndSplit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "real.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !targetExists(dir, "real.go:Sym") {
		t.Fatal("expected existing file target to pass")
	}
	if targetExists(dir, "transaction_manager.c:handle_lost_updates") {
		t.Fatal("expected confabulated target to fail the existence floor")
	}
	if p, s := splitTarget("a/b.go:Func"); p != "a/b.go" || s != "Func" {
		t.Fatalf("splitTarget = %q %q", p, s)
	}
}

func TestTerrainBlockNamesEveryFileEvenPastBudget(t *testing.T) {
	// Spin-4 measured failure: a 24KB budget over a 64-file repo dropped 57
	// files WHOLE — 4 of 6 injected defects sat in files RECON never saw.
	// Past the budget, declarations degrade but every file stays NAMED.
	dir := t.TempDir()
	var files []string
	for i := 0; i < 8; i++ {
		name := fmt.Sprintf("f%d.go", i)
		content := "package x\n"
		for j := 0; j < 40; j++ {
			content += fmt.Sprintf("func F%d_%d_%s() {}\n", i, j, strings.Repeat("x", 60))
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		files = append(files, name)
	}
	block := terrainBlock(dir, files, 4*1024) // far too small for 8 full decl maps
	for _, f := range files {
		if !strings.Contains(block, "== "+f+"\n") {
			t.Fatalf("file %s dropped from terrain — silent coverage cap", f)
		}
	}
	if !strings.Contains(block, "terrain budget") {
		t.Fatalf("truncation must be named, got:\n%s", block)
	}
	if len(block) > 4*1024+len(files)*80 {
		t.Fatalf("degraded terrain still far over budget: %d bytes", len(block))
	}
}
