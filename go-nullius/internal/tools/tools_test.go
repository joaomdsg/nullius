package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-nullius/internal/governor"
)

func j(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestReadRangedAndCapped(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "big.txt")
	var sb strings.Builder
	for i := 1; i <= MaxReadLines+500; i++ {
		fmt.Fprintf(&sb, "line %d\n", i)
	}
	if err := os.WriteFile(p, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &ReadTool{Tr: NewTracker()}

	out, isErr := r.Run(context.Background(), j(t, map[string]any{"path": p, "offset": 10, "limit": 2}))
	if isErr || !strings.Contains(out, "10\tline 10") || !strings.Contains(out, "11\tline 11") ||
		strings.Contains(out, "line 12") {
		t.Errorf("ranged read wrong:\n%s", out)
	}

	out, isErr = r.Run(context.Background(), j(t, map[string]any{"path": p}))
	if isErr || !strings.Contains(out, "[truncated at line") {
		t.Error("whole read of an oversized file must carry the truncation marker")
	}
	if strings.Contains(out, fmt.Sprintf("line %d\n", MaxReadLines+1)) {
		t.Error("whole read exceeded the line cap")
	}

	if _, isErr := r.Run(context.Background(), j(t, map[string]any{"path": p + ".nope"})); !isErr {
		t.Error("missing file must be an error result")
	}
}

func TestReadDupServedResident(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &ReadTool{Tr: NewTracker(), Ed: governor.NewEditor()}

	in := j(t, map[string]any{"path": p, "offset": 1, "limit": 10})
	out, isErr := r.Run(context.Background(), in)
	if isErr || !strings.Contains(out, "alpha") {
		t.Fatalf("first read failed: %q", out)
	}
	// Identical repeat → pointer to the resident copy, not the bytes.
	out, isErr = r.Run(context.Background(), in)
	if isErr || !strings.Contains(out, "[dup-read") || strings.Contains(out, "alpha") {
		t.Errorf("identical re-read must serve the resident marker, got %q", out)
	}
	// A different range is not a dup.
	out, _ = r.Run(context.Background(), j(t, map[string]any{"path": p, "offset": 2, "limit": 10}))
	if !strings.Contains(out, "beta") {
		t.Errorf("different range must hit disk, got %q", out)
	}
	// File changed → residency invalidated, bytes served again.
	if err := os.WriteFile(p, []byte("gamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, time.Now(), time.Now().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	out, isErr = r.Run(context.Background(), in)
	if isErr || !strings.Contains(out, "gamma") {
		t.Errorf("changed file must be re-read from disk, got %q", out)
	}
}

func TestEditStalenessAndUniqueness(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("aaa\nbbb\naaa\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tr := NewTracker()
	r := &ReadTool{Tr: tr}
	e := &EditTool{Tr: tr}

	// Edit before read → rejected.
	if out, isErr := e.Run(context.Background(), j(t, map[string]any{"path": p, "old_string": "bbb", "new_string": "B"})); !isErr {
		t.Errorf("edit before read must fail, got %q", out)
	}

	r.Run(context.Background(), j(t, map[string]any{"path": p}))

	// Ambiguous match → rejected with count.
	if out, isErr := e.Run(context.Background(), j(t, map[string]any{"path": p, "old_string": "aaa", "new_string": "A"})); !isErr || !strings.Contains(out, "2 times") {
		t.Errorf("ambiguous edit must fail with the match count, got %q", out)
	}

	// replace_all works.
	if _, isErr := e.Run(context.Background(), j(t, map[string]any{"path": p, "old_string": "aaa", "new_string": "A", "replace_all": true})); isErr {
		t.Error("replace_all edit failed")
	}
	raw, _ := os.ReadFile(p)
	if string(raw) != "A\nbbb\nA\n" {
		t.Errorf("content after replace_all = %q", raw)
	}

	// External change after our write → stale.
	if err := os.Chtimes(p, time.Now(), time.Now().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if out, isErr := e.Run(context.Background(), j(t, map[string]any{"path": p, "old_string": "bbb", "new_string": "B"})); !isErr || !strings.Contains(out, "changed on disk") {
		t.Errorf("stale edit must be rejected, got %q", out)
	}

	// Creation path.
	np := filepath.Join(dir, "new.txt")
	if _, isErr := e.Run(context.Background(), j(t, map[string]any{"path": np, "old_string": "", "new_string": "hello"})); isErr {
		t.Error("file creation via empty old_string failed")
	}
	raw, _ = os.ReadFile(np)
	if string(raw) != "hello" {
		t.Errorf("created content = %q", raw)
	}
	// And creating over an existing file is refused.
	if _, isErr := e.Run(context.Background(), j(t, map[string]any{"path": np, "old_string": "", "new_string": "x"})); !isErr {
		t.Error("empty old_string on an existing file must be rejected")
	}
}

func TestBashRealExitCodeAndCap(t *testing.T) {
	b := &BashTool{Dir: t.TempDir()}

	out, isErr := b.Run(context.Background(), j(t, map[string]any{"command": "echo ok"}))
	if isErr || !strings.Contains(out, "exit_code: 0") || !strings.Contains(out, "ok") {
		t.Errorf("simple command: %q", out)
	}

	// Real exit code, not an error result.
	out, isErr = b.Run(context.Background(), j(t, map[string]any{"command": "exit 3"}))
	if isErr || !strings.Contains(out, "exit_code: 3") {
		t.Errorf("nonzero exit must surface the REAL code as data: %q isErr=%v", out, isErr)
	}

	// Output cap with marker; the command is NOT rewritten.
	out, isErr = b.Run(context.Background(), j(t, map[string]any{"command": "head -c 100000 /dev/zero | tr '\\0' 'x'"}))
	if isErr || !strings.Contains(out, "[output truncated: ") {
		t.Errorf("oversized output must carry the truncation marker, got %d bytes isErr=%v", len(out), isErr)
	}
	if len(out) > MaxBashOutput+256 {
		t.Errorf("capped output still %d bytes", len(out))
	}
}

func TestBashTimeoutKillsProcessTree(t *testing.T) {
	b := &BashTool{Dir: t.TempDir()}
	start := time.Now()
	out, isErr := b.Run(context.Background(), j(t, map[string]any{"command": "sleep 30", "timeout_ms": 300}))
	if !isErr || !strings.Contains(out, "timed out") {
		t.Errorf("timeout must be an error result with a marker: %q", out)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("timeout did not kill the command promptly")
	}
}

func TestBashGovernorGate(t *testing.T) {
	b := &BashTool{Dir: t.TempDir()}
	out, isErr := b.Run(context.Background(), j(t, map[string]any{"command": "go test ./..."}))
	if !isErr || !strings.Contains(out, "governor:") {
		t.Errorf("heavy unbounded command must be denied with the routing reason, got %q", out)
	}
	// Bounded variant passes the gate (runs and fails fast in an empty dir — exit code is data).
	out, isErr = b.Run(context.Background(), j(t, map[string]any{"command": "go test ./... 2>&1 | tail -5"}))
	if isErr || strings.Contains(out, "governor:") {
		t.Errorf("bounded command must pass the gate, got %q isErr=%v", out, isErr)
	}
}
