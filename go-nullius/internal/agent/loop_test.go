package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

// fakeStreamer replays scripted messages and captures every params sent.
type fakeStreamer struct {
	script []*anthropic.Message
	calls  []anthropic.MessageNewParams
}

func (f *fakeStreamer) Stream(_ context.Context, p anthropic.MessageNewParams, _ func(anthropic.MessageStreamEventUnion)) (*anthropic.Message, error) {
	f.calls = append(f.calls, p)
	if len(f.calls) > len(f.script) {
		panic("fake script exhausted")
	}
	return f.script[len(f.calls)-1], nil
}

func msgJSON(t *testing.T, s string) *anthropic.Message {
	t.Helper()
	var m anthropic.Message
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return &m
}

func endTurn(t *testing.T, text string) *anthropic.Message {
	return msgJSON(t, `{"id":"m_end","type":"message","role":"assistant",
		"content":[{"type":"text","text":"`+text+`"}],
		"model":"test","stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}`)
}

// echoTool returns its "s" input; it also signals a channel so the test
// can prove parallel tool_use blocks run concurrently.
type echoTool struct {
	started chan string
	release chan struct{}
}

func (e *echoTool) Name() string { return "echo" }
func (e *echoTool) Def() anthropic.ToolUnionParam {
	return anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
		Name: "echo",
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{"s": map[string]any{"type": "string"}},
		},
	}}
}
func (e *echoTool) Run(_ context.Context, input json.RawMessage) (string, bool) {
	var in struct{ S string }
	if err := json.Unmarshal(input, &in); err != nil {
		return err.Error(), true
	}
	if e.started != nil {
		e.started <- in.S
		<-e.release
	}
	return "echo:" + in.S, false
}

func TestParallelToolUseSingleResultMessage(t *testing.T) {
	toolMsg := msgJSON(t, `{"id":"m_tu","type":"message","role":"assistant","content":[
		{"type":"tool_use","id":"tu_1","name":"echo","input":{"s":"a"}},
		{"type":"tool_use","id":"tu_2","name":"echo","input":{"s":"b"}}],
		"model":"test","stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":5}}`)
	fs := &fakeStreamer{script: []*anthropic.Message{toolMsg, endTurn(t, "done")}}

	tool := &echoTool{started: make(chan string, 2), release: make(chan struct{})}
	l := New(Config{Model: "test", MaxTokens: 100, System: "RULES"}, fs, []Tool{tool}, nil)

	// Prove concurrency: both Runs must start before either finishes.
	go func() {
		seen := map[string]bool{}
		for len(seen) < 2 {
			select {
			case s := <-tool.started:
				seen[s] = true
			case <-time.After(5 * time.Second):
				t.Error("tool_use blocks did not run concurrently")
				close(tool.release)
				return
			}
		}
		close(tool.release)
	}()

	out, err := l.Run(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if out != "done" {
		t.Errorf("out = %q", out)
	}

	// Second API call must carry: [user, assistant(tool_use), user(2 results)].
	last := fs.calls[1].Messages
	if len(last) != 3 {
		t.Fatalf("second call has %d messages, want 3", len(last))
	}
	raw, _ := json.Marshal(last[2])
	s := string(raw)
	if strings.Count(s, `"tool_result"`) != 2 {
		t.Errorf("ALL tool results must ride ONE user message, got: %s", s)
	}
	if !(strings.Index(s, "tu_1") < strings.Index(s, "tu_2")) {
		t.Error("tool results out of block order")
	}
	if !strings.Contains(s, "echo:a") || !strings.Contains(s, "echo:b") {
		t.Errorf("tool outputs missing: %s", s)
	}

	// System prompt frozen with a cache breakpoint on every call.
	for i, c := range fs.calls {
		sysRaw, _ := json.Marshal(c.System)
		if !strings.Contains(string(sysRaw), "RULES") || !strings.Contains(string(sysRaw), "ephemeral") {
			t.Errorf("call %d: system prompt missing rules or cache_control: %s", i, sysRaw)
		}
	}
}

func TestUnknownToolBecomesErrorResult(t *testing.T) {
	toolMsg := msgJSON(t, `{"id":"m","type":"message","role":"assistant","content":[
		{"type":"tool_use","id":"tu_x","name":"nope","input":{}}],
		"model":"test","stop_reason":"tool_use","usage":{"input_tokens":1,"output_tokens":1}}`)
	fs := &fakeStreamer{script: []*anthropic.Message{toolMsg, endTurn(t, "ok")}}
	l := New(Config{Model: "test", MaxTokens: 100}, fs, nil, nil)
	if _, err := l.Run(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(fs.calls[1].Messages[2])
	if !strings.Contains(string(raw), `"is_error":true`) || !strings.Contains(string(raw), "unknown tool: nope") {
		t.Errorf("unknown tool must produce is_error tool_result, got %s", raw)
	}
}

func TestPauseTurnContinues(t *testing.T) {
	pause := msgJSON(t, `{"id":"m_p","type":"message","role":"assistant",
		"content":[{"type":"text","text":"partial"}],
		"model":"test","stop_reason":"pause_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	fs := &fakeStreamer{script: []*anthropic.Message{pause, endTurn(t, "full")}}
	l := New(Config{Model: "test", MaxTokens: 100}, fs, nil, nil)
	out, err := l.Run(context.Background(), "go")
	if err != nil || out != "full" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if len(fs.calls) != 2 || len(fs.calls[1].Messages) != 2 {
		t.Errorf("pause_turn must resend with the partial assistant turn appended")
	}
}

func TestTerminalStops(t *testing.T) {
	mk := func(stop string) *anthropic.Message {
		return msgJSON(t, `{"id":"m","type":"message","role":"assistant",
			"content":[{"type":"text","text":"partial"}],
			"model":"test","stop_reason":"`+stop+`","usage":{"input_tokens":1,"output_tokens":1}}`)
	}
	for _, tc := range []struct {
		stop, wantErrSub string
	}{
		{"refusal", "refused"},
		{"max_tokens", "truncated"},
		{"model_context_window_exceeded", "context window"},
	} {
		fs := &fakeStreamer{script: []*anthropic.Message{mk(tc.stop)}}
		l := New(Config{Model: "test", MaxTokens: 100}, fs, nil, nil)
		_, err := l.Run(context.Background(), "go")
		if err == nil || !strings.Contains(err.Error(), tc.wantErrSub) {
			t.Errorf("stop=%s: err=%v, want substring %q", tc.stop, err, tc.wantErrSub)
		}
	}
}

func TestTurnCap(t *testing.T) {
	pause := msgJSON(t, `{"id":"m","type":"message","role":"assistant",
		"content":[{"type":"text","text":"x"}],
		"model":"test","stop_reason":"pause_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	script := make([]*anthropic.Message, 3)
	for i := range script {
		script[i] = pause
	}
	fs := &fakeStreamer{script: script}
	l := New(Config{Model: "test", MaxTokens: 100, MaxTurns: 3}, fs, nil, nil)
	_, err := l.Run(context.Background(), "go")
	if err == nil || !strings.Contains(err.Error(), "turn cap") {
		t.Errorf("want turn-cap error, got %v", err)
	}
}

// Guard against a regression where concurrent result writes race: -race
// plus many blocks would catch an unsynchronized slice.
func TestManyParallelTools(t *testing.T) {
	blocks := make([]string, 0, 16)
	for i := 0; i < 16; i++ {
		blocks = append(blocks, `{"type":"tool_use","id":"tu_`+string(rune('a'+i))+`","name":"count","input":{}}`)
	}
	toolMsg := msgJSON(t, `{"id":"m","type":"message","role":"assistant","content":[`+
		strings.Join(blocks, ",")+`],"model":"test","stop_reason":"tool_use",
		"usage":{"input_tokens":1,"output_tokens":1}}`)
	fs := &fakeStreamer{script: []*anthropic.Message{toolMsg, endTurn(t, "ok")}}

	var n atomic.Int32
	l := New(Config{Model: "test", MaxTokens: 100}, fs, []Tool{countTool{&n}}, nil)
	if _, err := l.Run(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 16 {
		t.Errorf("ran %d tools, want 16", n.Load())
	}
	raw, _ := json.Marshal(fs.calls[1].Messages[2])
	if strings.Count(string(raw), `"tool_result"`) != 16 {
		t.Error("all 16 results must ride one user message")
	}
}

type sweepSpy struct{ calls int }

func (s *sweepSpy) Sweep([]anthropic.MessageParam) int { s.calls++; return 1 }

func TestLedgerTailRecitedAtEdgeOnly(t *testing.T) {
	toolMsg := msgJSON(t, `{"id":"m","type":"message","role":"assistant","content":[
		{"type":"tool_use","id":"tu_1","name":"count","input":{}}],
		"model":"test","stop_reason":"tool_use","usage":{"input_tokens":1,"output_tokens":1}}`)
	fs := &fakeStreamer{script: []*anthropic.Message{toolMsg, endTurn(t, "ok")}}

	var n atomic.Int32
	l := New(Config{Model: "test", MaxTokens: 100}, fs, []Tool{countTool{&n}}, nil)
	renders := 0
	l.Tail = func() string { renders++; return "open: item-1" }
	spy := &sweepSpy{}
	l.Editor = spy

	if _, err := l.Run(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if spy.calls != 2 {
		t.Errorf("editor must sweep before every call, swept %d of 2", spy.calls)
	}
	// Every call: the ledger appears EXACTLY once, in the final user message.
	for i, c := range fs.calls {
		raw, _ := json.Marshal(c.Messages)
		if got := strings.Count(string(raw), "item-1"); got != 1 {
			t.Errorf("call %d: ledger rendered %d times in history, want exactly 1 (edge only)", i, got)
		}
		lastRaw, _ := json.Marshal(c.Messages[len(c.Messages)-1])
		if !strings.Contains(string(lastRaw), "item-1") {
			t.Errorf("call %d: ledger not at the context edge", i)
		}
	}
}

type countTool struct{ n *atomic.Int32 }

func (c countTool) Name() string { return "count" }
func (c countTool) Def() anthropic.ToolUnionParam {
	return anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
		Name:        "count",
		InputSchema: anthropic.ToolInputSchemaParam{Properties: map[string]any{}},
	}}
}
func (c countTool) Run(context.Context, json.RawMessage) (string, bool) {
	c.n.Add(1)
	return "ok", false
}
