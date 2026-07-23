package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

// capture serves one canned chat-completions response and records the
// request body the transport actually sent.
func capture(t *testing.T, respBody string) (*Client, *[]byte) {
	t.Helper()
	var got []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL+"/v1", "not-needed", "test-model"), &got
}

// A tool_calls response must bridge to stop_reason=tool_use with a
// tool_use block whose raw input survives for the loop's .Raw() read.
func TestBridgeToolCall(t *testing.T) {
	resp := `{"id":"cc-1","model":"m","choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":"",
	"tool_calls":[{"id":"call_9","type":"function","function":{"name":"run_bash","arguments":"{\"cmd\":\"ls\"}"}}]}}],
	"usage":{"prompt_tokens":11,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":4}}}`
	c, _ := capture(t, resp)
	msg, err := c.Stream(context.Background(), anthropic.MessageNewParams{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if msg.StopReason != anthropic.StopReasonToolUse {
		t.Fatalf("stop_reason = %q, want tool_use", msg.StopReason)
	}
	var tu anthropic.ToolUseBlock
	for _, b := range msg.Content {
		if x, ok := b.AsAny().(anthropic.ToolUseBlock); ok {
			tu = x
		}
	}
	if tu.Name != "run_bash" || tu.ID != "call_9" {
		t.Fatalf("tool_use = %+v, want run_bash/call_9", tu)
	}
	// The raw input must round-trip — runTools reads tu.JSON.Input.Raw().
	if raw := tu.JSON.Input.Raw(); !strings.Contains(raw, `"cmd"`) {
		t.Fatalf("tool input raw = %q, want it to carry cmd", raw)
	}
	if msg.Usage.InputTokens != 11 || msg.Usage.OutputTokens != 7 || msg.Usage.CacheReadInputTokens != 4 {
		t.Fatalf("usage = %+v, want 11/7/cache4", msg.Usage)
	}
}

// A plain text stop response bridges to end_turn with the text intact.
func TestBridgeEndTurn(t *testing.T) {
	resp := `{"id":"cc-2","model":"m","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"done"}}],
	"usage":{"prompt_tokens":3,"completion_tokens":1}}`
	c, _ := capture(t, resp)
	msg, err := c.Stream(context.Background(), anthropic.MessageNewParams{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if msg.StopReason != anthropic.StopReasonEndTurn {
		t.Fatalf("stop_reason = %q, want end_turn", msg.StopReason)
	}
	if got := textOf(msg); got != "done" {
		t.Fatalf("text = %q, want done", got)
	}
}

// The request translation must: concatenate System into a system message,
// map a tool_result user turn to a tool-role message (not a user message),
// carry the assistant tool_calls, and translate tool defs to functions.
func TestRequestTranslation(t *testing.T) {
	resp := `{"id":"x","model":"m","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],"usage":{}}`
	c, gotp := capture(t, resp)

	params := anthropic.MessageNewParams{
		Model:     "test-model",
		MaxTokens: 128,
		System: []anthropic.TextBlockParam{{
			Text:         "RULES",
			CacheControl: anthropic.NewCacheControlEphemeralParam(), // must be silently dropped
		}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock("hi")),
			{Role: anthropic.MessageParamRoleAssistant, Content: []anthropic.ContentBlockParamUnion{
				{OfToolUse: &anthropic.ToolUseBlockParam{ID: "t1", Name: "read", Input: map[string]any{"p": "a.go"}}},
			}},
			anthropic.NewUserMessage(anthropic.NewToolResultBlock("t1", "file body", false)),
		},
		Tools: []anthropic.ToolUnionParam{{OfTool: &anthropic.ToolParam{
			Name:        "read",
			InputSchema: anthropic.ToolInputSchemaParam{Properties: map[string]any{"p": map[string]any{"type": "string"}}},
		}}},
	}
	if _, err := c.Stream(context.Background(), params, nil); err != nil {
		t.Fatal(err)
	}

	var sent struct {
		Model     string `json:"model"`
		MaxTokens int    `json:"max_tokens"`
		Messages  []struct {
			Role       string `json:"role"`
			Content    string `json:"content"`
			ToolCallID string `json:"tool_call_id"`
			ToolCalls  []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"messages"`
		Tools []struct {
			Type     string `json:"type"`
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(*gotp, &sent); err != nil {
		t.Fatalf("unmarshal sent body: %v\n%s", err, *gotp)
	}
	if sent.Model != "test-model" || sent.MaxTokens != 128 {
		t.Fatalf("model/max = %q/%d", sent.Model, sent.MaxTokens)
	}
	// system, user(hi), assistant(tool_calls), tool(result) — 4 messages,
	// no user message for the pure tool_result turn.
	if len(sent.Messages) != 4 {
		t.Fatalf("got %d messages, want 4: %s", len(sent.Messages), *gotp)
	}
	if sent.Messages[0].Role != "system" || sent.Messages[0].Content != "RULES" {
		t.Fatalf("msg0 = %+v, want system/RULES", sent.Messages[0])
	}
	if sent.Messages[2].Role != "assistant" || len(sent.Messages[2].ToolCalls) != 1 ||
		sent.Messages[2].ToolCalls[0].ID != "t1" {
		t.Fatalf("msg2 tool_calls wrong: %+v", sent.Messages[2])
	}
	if sent.Messages[3].Role != "tool" || sent.Messages[3].ToolCallID != "t1" ||
		sent.Messages[3].Content != "file body" {
		t.Fatalf("msg3 = %+v, want tool/t1/file body", sent.Messages[3])
	}
	if len(sent.Tools) != 1 || sent.Tools[0].Type != "function" || sent.Tools[0].Function.Name != "read" {
		t.Fatalf("tools wrong: %+v", sent.Tools)
	}
}

// textOf mirrors the loop's text extraction for assertions.
func textOf(msg *anthropic.Message) string {
	var sb strings.Builder
	for _, b := range msg.Content {
		if tb, ok := b.AsAny().(anthropic.TextBlock); ok {
			sb.WriteString(tb.Text)
		}
	}
	return sb.String()
}
