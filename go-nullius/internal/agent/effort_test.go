package agent

import (
	"context"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

func TestEffortOnParams(t *testing.T) {
	fs := &fakeStreamer{script: []*anthropic.Message{endTurn(t, "ok")}}
	l := New(Config{Model: "test", MaxTokens: 100, System: "RULES", Effort: "low"}, fs, nil, nil)
	if _, err := l.Run(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if got := fs.calls[0].OutputConfig.Effort; got != anthropic.OutputConfigEffortLow {
		t.Errorf("params OutputConfig.Effort = %q, want low", got)
	}
}

// Flips red if Config.Effort stops reaching params (e.g. the wiring in
// main.go passes it but the loop drops it).
func TestNoEffortByDefault(t *testing.T) {
	fs := &fakeStreamer{script: []*anthropic.Message{endTurn(t, "ok")}}
	l := New(Config{Model: "test", MaxTokens: 100, System: "RULES"}, fs, nil, nil)
	if _, err := l.Run(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if got := fs.calls[0].OutputConfig.Effort; got != "" {
		t.Errorf("params OutputConfig.Effort = %q, want unset (API default)", got)
	}
}
