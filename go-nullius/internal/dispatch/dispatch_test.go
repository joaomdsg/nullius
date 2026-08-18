package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"go-nullius/internal/caller"
)

// fakeCaller implements caller.Caller for hermetic APIAdapter tests.
type fakeCaller struct {
	gotTier   caller.Tier
	gotPrompt string
	reply     map[string]any
	err       error
}

func (f *fakeCaller) Ask(ctx context.Context, tier caller.Tier, prompt string, grammar caller.GBNF, out any, opts ...caller.AskOption) error {
	f.gotTier = tier
	f.gotPrompt = prompt
	if f.err != nil {
		return f.err
	}
	b, err := json.Marshal(f.reply)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func TestAPIAdapterMapsTierAndRoundTripsJSON(t *testing.T) {
	fc := &fakeCaller{reply: map[string]any{"mode": "FIX", "count": float64(3)}}
	a := APIAdapter{Caller: fc}

	resp, err := a.Dispatch(context.Background(), Request{Tier: TierFrontier, Prompt: "rule this", MaxTokens: 100})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if fc.gotTier != caller.Smart {
		t.Fatalf("expected TierFrontier -> caller.Smart, got %v", fc.gotTier)
	}
	if fc.gotPrompt != "rule this" {
		t.Fatalf("prompt not forwarded: %q", fc.gotPrompt)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(resp.Text), &got); err != nil {
		t.Fatalf("response text not valid JSON: %v (%s)", err, resp.Text)
	}
	if got["mode"] != "FIX" {
		t.Fatalf("round trip mismatch: %+v", got)
	}

	if _, err := a.Dispatch(context.Background(), Request{Tier: TierScout, Prompt: "scout"}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if fc.gotTier != caller.Fast {
		t.Fatalf("expected TierScout -> caller.Fast, got %v", fc.gotTier)
	}
}

func TestAPIAdapterPropagatesError(t *testing.T) {
	fc := &fakeCaller{err: errors.New("boom")}
	a := APIAdapter{Caller: fc}
	if _, err := a.Dispatch(context.Background(), Request{Prompt: "x"}); err == nil {
		t.Fatal("expected error to propagate")
	}
}

func fakeShellScript(t *testing.T, output string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script fakes require a POSIX shell")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "fake")
	script := "#!/bin/sh\ncat <<'EOF'\n" + output + "\nEOF\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake script: %v", err)
	}
	return path
}

func TestClaudeAdapterShellsBinary(t *testing.T) {
	bin := fakeShellScript(t, `{"ok":true}`)
	a := ClaudeAdapter{Bin: bin}
	resp, err := a.Dispatch(context.Background(), Request{Prompt: "hello"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if !strings.Contains(resp.Text, "ok") {
		t.Fatalf("unexpected output: %q", resp.Text)
	}
	if a.Name() != "claude" {
		t.Fatalf("Name() = %q", a.Name())
	}
}

func TestPiAdapterShellsBinary(t *testing.T) {
	bin := fakeShellScript(t, `{"ok":true}`)
	a := PiAdapter{Bin: bin}
	resp, err := a.Dispatch(context.Background(), Request{Prompt: "hello"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if !strings.Contains(resp.Text, "ok") {
		t.Fatalf("unexpected output: %q", resp.Text)
	}
}

func TestNewPiUnavailableErrorsClearlyWhenNotOnPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	a := NewPi()
	if _, err := a.Dispatch(context.Background(), Request{Prompt: "x"}); err == nil {
		t.Fatal("expected unavailable adapter to error")
	} else if !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("expected clear unavailable error, got: %v", err)
	}
}

func TestFactoryUnknownAdapterErrors(t *testing.T) {
	if _, err := New("nope", Options{}); err == nil {
		t.Fatal("expected error for unknown adapter name")
	}
}

func TestFactoryAPIRequiresCaller(t *testing.T) {
	if _, err := New("api", Options{}); err == nil {
		t.Fatal("expected error when api adapter has no caller.Caller")
	}
}

func TestFactoryBuildsEachKnownAdapter(t *testing.T) {
	fc := &fakeCaller{reply: map[string]any{}}
	if a, err := New("api", Options{Caller: fc}); err != nil || a.Name() != "api" {
		t.Fatalf("api: %v %v", a, err)
	}
	if a, err := New("claude", Options{}); err != nil || a.Name() != "claude" {
		t.Fatalf("claude: %v %v", a, err)
	}
	if a, err := New("pi", Options{}); err != nil || a.Name() != "pi" {
		t.Fatalf("pi: %v %v", a, err)
	}
}

func TestAPIAdapterSurfacesUsageTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"choices":[{"message":{"content":"{\"a\":1}"}}],"usage":{"total_tokens":37}}`)
	}))
	defer srv.Close()
	c := caller.New("", map[caller.Tier]caller.Endpoint{caller.Fast: {BaseURL: srv.URL, Model: "m"}})
	a := APIAdapter{Caller: c}
	resp, err := a.Dispatch(context.Background(), Request{Tier: TierScout, Objective: "x", Prompt: "p"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if resp.Tokens != 37 {
		t.Fatalf("resp.Tokens = %d, want 37 (receipts must carry real usage)", resp.Tokens)
	}
}
