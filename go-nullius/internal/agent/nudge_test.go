package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

// A model that ends the turn while work is unfinished (open checklist
// items, no close) must be nudged back into the loop instead of the Run
// returning early — this is the "qwen quit after dispatching the hunt,
// leaving 33 items unruled" failure the testdrive surfaced.
func TestUnfinishedNudgeContinues(t *testing.T) {
	// Three end_turns: the first two arrive with work still open, the
	// third after the work is "done".
	fs := &fakeStreamer{script: []*anthropic.Message{
		endTurn(t, "I dispatched the hunt."), // premature #1
		endTurn(t, ""),                       // premature #2 (empty, the real-world case)
		endTurn(t, "STATUS: closed."),        // genuine finish
	}}
	loop := New(Config{Model: "m", MaxTokens: 100, MaxTurns: 10}, fs, nil, nil)

	open := 2 // two nudges' worth of unfinished work, then it drains
	loop.Unfinished = func() string {
		if open == 0 {
			return ""
		}
		open--
		return "unruled items remain — rule or close before ending"
	}

	out, err := loop.Run(context.Background(), "hunt")
	if err != nil {
		t.Fatal(err)
	}
	if out != "STATUS: closed." {
		t.Fatalf("out = %q, want the finish reached only after nudges", out)
	}
	if len(fs.calls) != 3 {
		t.Fatalf("model called %d times, want 3 (two nudged re-prompts)", len(fs.calls))
	}
	// The nudge must have been appended as a user message before each retry.
	last := fs.calls[2].Messages
	found := false
	for _, m := range last {
		for _, b := range m.Content {
			if b.OfText != nil && strings.Contains(b.OfText.Text, "rule or close") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("nudge text was not injected into the conversation")
	}
}

// With no Unfinished hook (scout mode, BUILD mode) a plain end_turn ends
// the Run immediately — the guard must never fire when unset.
func TestNoUnfinishedHookEndsImmediately(t *testing.T) {
	fs := &fakeStreamer{script: []*anthropic.Message{endTurn(t, "done")}}
	loop := New(Config{Model: "m", MaxTokens: 100, MaxTurns: 10}, fs, nil, nil)
	out, err := loop.Run(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if out != "done" || len(fs.calls) != 1 {
		t.Fatalf("out=%q calls=%d, want done/1 (no nudge when hook unset)", out, len(fs.calls))
	}
}

// The nudge is bounded: a model that keeps bailing must not loop forever.
// After nudgeCap re-prompts, the Run returns rather than spinning to the
// turn cap.
func TestUnfinishedNudgeCapped(t *testing.T) {
	var script []*anthropic.Message
	for i := 0; i < nudgeCap+5; i++ {
		script = append(script, endTurn(t, "still not done"))
	}
	fs := &fakeStreamer{script: script}
	loop := New(Config{Model: "m", MaxTokens: 100, MaxTurns: 100}, fs, nil, nil)
	loop.Unfinished = func() string { return "always unfinished" } // never drains

	out, err := loop.Run(context.Background(), "hunt")
	if err != nil {
		t.Fatal(err)
	}
	if out != "still not done" {
		t.Fatalf("out = %q, want the final turn's text after the cap", out)
	}
	// 1 initial + nudgeCap re-prompts = nudgeCap+1 model calls, then it gives up.
	if len(fs.calls) != nudgeCap+1 {
		t.Fatalf("model called %d times, want %d (capped)", len(fs.calls), nudgeCap+1)
	}
}
