package drive

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleGo = `package sample

import "sync"

var mu sync.Mutex

func Get(m map[string]int, k string) int {
	return m[k]
}

func Set(m map[string]int, k string, v int) {
	m[k] = v
}
`

func writeSample(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.go")
	if err := os.WriteFile(path, []byte(sampleGo), 0o644); err != nil {
		t.Fatalf("write sample: %v", err)
	}
	return path
}

func TestEnclosingWindowFindsOwnFunctionOnly(t *testing.T) {
	path := writeSample(t)
	// line 12 is inside Set's body ("m[k] = v")
	text, start, end, err := EnclosingWindow(path, 12)
	if err != nil {
		t.Fatalf("EnclosingWindow: %v", err)
	}
	if !strings.Contains(text, "func Set(") {
		t.Fatalf("expected Set's own body in window, got: %s", text)
	}
	if strings.Contains(text, "func Get(") {
		t.Fatalf("window leaked a sibling function (the cautionary tale): %s", text)
	}
	if start > 12 || end < 12 {
		t.Fatalf("window [%d,%d] doesn't bound line 12", start, end)
	}
}

func TestEnclosingWindowFallsBackForNonFuncLine(t *testing.T) {
	path := writeSample(t)
	// line 5 is the package-level var declaration, outside any func body.
	text, start, end, err := EnclosingWindow(path, 5)
	if err != nil {
		t.Fatalf("EnclosingWindow: %v", err)
	}
	if text == "" {
		t.Fatal("expected fallback radius window, got empty text")
	}
	if start < 1 || end < start {
		t.Fatalf("invalid fallback range [%d,%d]", start, end)
	}
}

func TestHasFuncDeclDetectsFunctions(t *testing.T) {
	path := writeSample(t)
	if !HasFuncDecl(path) {
		t.Fatal("expected HasFuncDecl true for a file with func declarations")
	}
}

func TestHasFuncDeclFalseForNonGoOrDeclFree(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(path, []byte("just prose"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if HasFuncDecl(path) {
		t.Fatal("expected HasFuncDecl false for non-Go file")
	}

	goPath := filepath.Join(dir, "vars.go")
	if err := os.WriteFile(goPath, []byte("package vars\n\nvar X = 1\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if HasFuncDecl(goPath) {
		t.Fatal("expected HasFuncDecl false for a file with no func decls")
	}
}
