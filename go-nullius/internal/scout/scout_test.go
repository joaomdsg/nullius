package scout

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-nullius/internal/telemetry"
)

// fakeBin writes a shell script standing in for the go-nullius binary.
func fakeBin(t *testing.T, dir, script string) string {
	t.Helper()
	p := filepath.Join(dir, "fake-nullius")
	if err := os.WriteFile(p, []byte("#!/bin/bash\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func input(t *testing.T, objective string) json.RawMessage {
	raw, err := json.Marshal(map[string]any{"objective": objective})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestScoutSuccessAndStatsFold(t *testing.T) {
	dir := t.TempDir()
	nd := filepath.Join(dir, ".nullius")
	// The fake child extracts its --session arg and writes a stats file,
	// like the real binary does, then prints its report.
	bin := fakeBin(t, dir, `
while [[ $# -gt 0 ]]; do [[ $1 == --session ]] && SID=$2; shift; done
mkdir -p `+nd+`
cat > `+nd+`/stats-$SID.json <<EOF
{"leader":{"input_tokens":100,"output_tokens":40,"cache_read_tokens":7,"cache_creation_tokens":3,"requests":2}}
EOF
echo "REPORT: exit 0, all green"`)

	stats := telemetry.New(nd, "parent")
	s := &Tool{Bin: bin, Dir: dir, Model: "haiku", NulliusDir: nd, Stats: stats}
	out, isErr := s.Run(context.Background(), input(t, "run the suite"))
	if isErr || !strings.Contains(out, "REPORT: exit 0") {
		t.Fatalf("scout run failed: %q", out)
	}
	if stats.ScoutRuns != 1 || stats.Scouts.InputTokens != 100 || stats.Scouts.OutputTokens != 40 || stats.Scouts.Requests != 2 {
		t.Errorf("child stats not folded: %+v", stats.Scouts)
	}
}

func TestScoutRetriesOnRateLimit(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "attempted")
	bin := fakeBin(t, dir, `
if [[ ! -f `+marker+` ]]; then touch `+marker+`; echo "error 429 rate limit" >&2; exit 1; fi
echo "REPORT after retry"`)
	s := &Tool{Bin: bin, Dir: dir, Model: "haiku", NulliusDir: dir, Backoff: 10 * time.Millisecond}
	out, isErr := s.Run(context.Background(), input(t, "q"))
	if isErr || !strings.Contains(out, "REPORT after retry") {
		t.Errorf("retryable failure must be retried, got %q isErr=%v", out, isErr)
	}
}

func TestScoutNonRetryableFailsFast(t *testing.T) {
	dir := t.TempDir()
	count := filepath.Join(dir, "count")
	bin := fakeBin(t, dir, `echo x >> `+count+`; echo "fatal: bad dispatch" >&2; exit 2`)
	s := &Tool{Bin: bin, Dir: dir, Model: "haiku", NulliusDir: dir, Backoff: 10 * time.Millisecond}
	out, isErr := s.Run(context.Background(), input(t, "q"))
	if !isErr || !strings.Contains(out, "bad dispatch") {
		t.Errorf("non-retryable failure must surface stderr, got %q", out)
	}
	raw, _ := os.ReadFile(count)
	if got := strings.Count(string(raw), "x"); got != 1 {
		t.Errorf("non-retryable error was attempted %d times, want 1", got)
	}
}

func TestScoutByteCap(t *testing.T) {
	dir := t.TempDir()
	bin := fakeBin(t, dir, `head -c 100000 /dev/zero | tr '\0' 'y'`)
	s := &Tool{Bin: bin, Dir: dir, Model: "haiku", NulliusDir: dir}
	out, isErr := s.Run(context.Background(), input(t, "q"))
	if isErr || !strings.Contains(out, "[scout report truncated") {
		t.Errorf("oversized report must be capped with a marker, isErr=%v len=%d", isErr, len(out))
	}
	if len(out) > MaxReportBytes+128 {
		t.Errorf("capped report still %d bytes", len(out))
	}
}

func TestScoutIdleKill(t *testing.T) {
	dir := t.TempDir()
	bin := fakeBin(t, dir, `sleep 30; echo never`)
	s := &Tool{Bin: bin, Dir: dir, Model: "haiku", NulliusDir: dir,
		Idle: 400 * time.Millisecond, Backoff: 10 * time.Millisecond}
	start := time.Now()
	out, isErr := s.Run(context.Background(), input(t, "q"))
	if !isErr || !strings.Contains(out, "idle-killed") {
		t.Errorf("silent child must be idle-killed, got %q", out)
	}
	if time.Since(start) > 10*time.Second {
		t.Error("idle-kill took too long (watchdog not firing)")
	}
}
