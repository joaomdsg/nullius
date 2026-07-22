package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

// fakeCloser signals one successful close, consumed once.
type fakeCloser struct{ closed bool }

func (f *fakeCloser) ConsumeClosed() bool {
	c := f.closed
	f.closed = false
	return c
}

// resetEditor records Reset calls; Sweep is a no-op.
type resetEditor struct{ resets int }

func (r *resetEditor) Sweep([]anthropic.MessageParam) int { return 0 }
func (r *resetEditor) Reset()                             { r.resets++ }

func TestCompactAfterClose(t *testing.T) {
	fs := &fakeStreamer{script: []*anthropic.Message{
		endTurn(t, "STATUS: closed. FACTS: record."),
		endTurn(t, "second mandate handled"),
	}}
	cl := &fakeCloser{}
	ed := &resetEditor{}
	l := New(Config{Model: "test", MaxTokens: 100, System: "RULES"}, fs, nil, nil)
	l.Closer = cl
	l.Editor = ed

	cl.closed = true // close tool "fired" during run 1
	if _, err := l.Run(context.Background(), "first mandate"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Run(context.Background(), "second mandate"); err != nil {
		t.Fatal(err)
	}

	// Second Run must start from compacted history: exactly ONE user message.
	msgs := fs.calls[1].Messages
	if len(msgs) != 1 {
		t.Fatalf("post-compact call carries %d messages, want 1 (compacted)", len(msgs))
	}
	if msgs[0].Role != anthropic.MessageParamRoleUser {
		t.Fatalf("compacted message role = %s, want user", msgs[0].Role)
	}
	text := msgs[0].Content[0].OfText.Text
	if !strings.HasPrefix(text, CompactPrefix) {
		t.Errorf("compacted prompt missing %q prefix: %q", CompactPrefix, text[:min(len(text), 60)])
	}
	if !strings.Contains(text, "STATUS: closed. FACTS: record.") {
		t.Error("close ledger not preserved verbatim in compact record")
	}
	if !strings.Contains(text, "second mandate") {
		t.Error("new mandate missing from compacted prompt")
	}
	if ed.resets != 1 {
		t.Errorf("editor Reset calls = %d, want 1 (residency must not survive compaction)", ed.resets)
	}
}

// A Run that ends in an ERROR after close armed the sentinel must drain
// it: otherwise the next Run's clean end compacts on a final report that
// is not a close ledger.
func TestErrorExitDrainsSentinel(t *testing.T) {
	maxTok := msgJSON(t, `{"id":"m_mt","type":"message","role":"assistant",
		"content":[{"type":"text","text":"truncated"}],
		"model":"test","stop_reason":"max_tokens","usage":{"input_tokens":10,"output_tokens":5}}`)
	fs := &fakeStreamer{script: []*anthropic.Message{
		maxTok,
		endTurn(t, "not a close ledger"),
		endTurn(t, "third"),
	}}
	cl := &fakeCloser{}
	l := New(Config{Model: "test", MaxTokens: 100, System: "RULES"}, fs, nil, nil)
	l.Closer = cl

	cl.closed = true // close armed, then the Run dies on max_tokens
	if _, err := l.Run(context.Background(), "one"); err == nil {
		t.Fatal("max_tokens Run did not error")
	}
	if _, err := l.Run(context.Background(), "two"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Run(context.Background(), "three"); err != nil {
		t.Fatal(err)
	}
	// Run 3 must NOT have been compacted: run 2's report was no ledger.
	if n := len(fs.calls[2].Messages); n == 1 {
		t.Error("stale sentinel survived an error exit — compacted on a non-close report")
	}
}

func TestNoCompactWithoutClose(t *testing.T) {
	fs := &fakeStreamer{script: []*anthropic.Message{
		endTurn(t, "no close here"),
		endTurn(t, "still going"),
	}}
	l := New(Config{Model: "test", MaxTokens: 100, System: "RULES"}, fs, nil, nil)
	l.Closer = &fakeCloser{} // never fires

	if _, err := l.Run(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Run(context.Background(), "two"); err != nil {
		t.Fatal(err)
	}
	// History must accumulate: user, assistant, user = 3 messages.
	if n := len(fs.calls[1].Messages); n != 3 {
		t.Errorf("second call carries %d messages, want 3 (no compaction without close)", n)
	}
}
