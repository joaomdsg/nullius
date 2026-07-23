// Package api is go-nullius's transport seam. It targets OpenAI-compatible
// servers (local llama.cpp / vLLM / LM Studio, or any /v1/chat/completions
// endpoint) while keeping anthropic-sdk-go types as the agent's internal
// vocabulary: MessageNewParams in, *anthropic.Message out. The anthropic
// request is translated to a chat-completions call and the response is
// bridged back through the SDK's own JSON unmarshal, so the raw shadow
// fields the loop relies on (AsAny, ToParam, tool-input .Raw()) populate
// exactly as they would from a real Anthropic response.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	openai "github.com/sashabaranov/go-openai"
)

// Client is an OpenAI-compatible transport implementing agent.Streamer.
type Client struct {
	oai   *openai.Client
	model string // request model id sent when params carry none
	// RetryBase is the first 429 backoff step (doubles per retry, capped
	// at retryMax attempts). Overridable for tests; zero means default.
	RetryBase time.Duration
}

// retryMax bounds 429 retries: base 15s doubling → 15+30+60+120+240+300*2 ≈
// 17.5min of patience before a throttle is declared fatal. Free-tier
// windows (Zen) reset well inside that.
const (
	retryMax     = 7
	retryBaseDef = 15 * time.Second
	retryCap     = 5 * time.Minute
)

// New builds a client for an OpenAI-compatible endpoint. apiKey may be a
// placeholder ("not-needed") for auth-less local servers; model is the
// server's model id used when a request carries no explicit model.
func New(baseURL, apiKey, model string) *Client {
	cfg := openai.DefaultConfig(apiKey)
	cfg.BaseURL = baseURL
	return &Client{oai: openai.NewClientWithConfig(cfg), model: model}
}

// Stream satisfies agent.Streamer. It runs one non-streaming completion
// (onEvent, used only for live token rendering, is nil in headless runs)
// and bridges the result into an *anthropic.Message. Duration is bounded
// by ctx, not a fixed timeout — local models can be slow.
func (c *Client) Stream(ctx context.Context, params anthropic.MessageNewParams, onEvent func(anthropic.MessageStreamEventUnion)) (*anthropic.Message, error) {
	req := c.toRequest(params)
	base := c.RetryBase
	if base <= 0 {
		base = retryBaseDef
	}
	var lastErr error
	for attempt := 0; attempt <= retryMax; attempt++ {
		if attempt > 0 {
			wait := base << (attempt - 1)
			if wait > retryCap {
				wait = retryCap
			}
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("chat completion: %w (aborted during 429 backoff: %v)", ctx.Err(), lastErr)
			case <-time.After(wait):
			}
		}
		resp, err := c.oai.CreateChatCompletion(ctx, req)
		if err == nil {
			return toMessage(&resp)
		}
		lastErr = err
		// 429 is a transient throttle (free-tier providers burst-limit);
		// everything else is fatal — retrying a 401/400 burns time.
		var apiErr *openai.APIError
		if !errors.As(err, &apiErr) || apiErr.HTTPStatusCode != 429 {
			return nil, fmt.Errorf("chat completion: %w", err)
		}
	}
	return nil, fmt.Errorf("chat completion: rate-limited through %d retries: %w", retryMax, lastErr)
}

// toRequest translates anthropic MessageNewParams into a chat-completions
// request. Anthropic-only fields (cache_control, output_config/effort) have
// no OpenAI equivalent and are intentionally dropped.
func (c *Client) toRequest(p anthropic.MessageNewParams) openai.ChatCompletionRequest {
	var msgs []openai.ChatCompletionMessage

	var sys strings.Builder
	for _, s := range p.System {
		sys.WriteString(s.Text)
	}
	if sys.Len() > 0 {
		msgs = append(msgs, openai.ChatCompletionMessage{
			Role: openai.ChatMessageRoleSystem, Content: sys.String(),
		})
	}

	for _, m := range p.Messages {
		var text strings.Builder
		var toolCalls []openai.ToolCall
		for _, b := range m.Content {
			switch {
			case b.OfText != nil:
				text.WriteString(b.OfText.Text)
			case b.OfToolUse != nil:
				args, _ := json.Marshal(b.OfToolUse.Input)
				toolCalls = append(toolCalls, openai.ToolCall{
					ID:   b.OfToolUse.ID,
					Type: openai.ToolTypeFunction,
					Function: openai.FunctionCall{
						Name:      b.OfToolUse.Name,
						Arguments: string(args),
					},
				})
			case b.OfToolResult != nil:
				// A tool_result becomes its own tool-role message, emitted
				// in place so it follows the assistant tool_calls that
				// produced it (OpenAI ordering requirement).
				var rc strings.Builder
				for _, cu := range b.OfToolResult.Content {
					if cu.OfText != nil {
						rc.WriteString(cu.OfText.Text)
					}
				}
				msgs = append(msgs, openai.ChatCompletionMessage{
					Role:       openai.ChatMessageRoleTool,
					ToolCallID: b.OfToolResult.ToolUseID,
					Content:    rc.String(),
				})
			}
		}
		// Emit the user/assistant turn itself only if it carries text or
		// tool calls; a user turn that was PURELY tool_result blocks is
		// already fully represented by the tool messages above.
		if text.Len() > 0 || len(toolCalls) > 0 {
			msgs = append(msgs, openai.ChatCompletionMessage{
				Role:      roleOf(m.Role),
				Content:   text.String(),
				ToolCalls: toolCalls,
			})
		}
	}

	var tools []openai.Tool
	for _, t := range p.Tools {
		if t.OfTool == nil {
			continue
		}
		tp := t.OfTool
		schema := map[string]any{
			"type":       "object",
			"properties": tp.InputSchema.Properties,
		}
		if len(tp.InputSchema.Required) > 0 {
			schema["required"] = tp.InputSchema.Required
		}
		tools = append(tools, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        tp.Name,
				Description: tp.Description.Or(""),
				Parameters:  schema,
			},
		})
	}

	model := string(p.Model)
	if model == "" {
		model = c.model
	}
	return openai.ChatCompletionRequest{
		Model:     model,
		MaxTokens: int(p.MaxTokens),
		Messages:  msgs,
		Tools:     tools,
	}
}

func roleOf(r anthropic.MessageParamRole) string {
	if r == anthropic.MessageParamRoleAssistant {
		return openai.ChatMessageRoleAssistant
	}
	return openai.ChatMessageRoleUser
}

// toMessage bridges a chat-completions response into an *anthropic.Message
// by rendering it as Anthropic wire JSON and unmarshaling through the SDK,
// which populates the raw shadow fields the loop depends on.
func toMessage(resp *openai.ChatCompletionResponse) (*anthropic.Message, error) {
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("response carried no choices")
	}
	ch := resp.Choices[0]

	content := []map[string]any{}
	if ch.Message.Content != "" {
		content = append(content, map[string]any{"type": "text", "text": ch.Message.Content})
	}
	for _, tc := range ch.Message.ToolCalls {
		input := json.RawMessage("{}")
		if strings.TrimSpace(tc.Function.Arguments) != "" {
			input = json.RawMessage(tc.Function.Arguments)
		}
		content = append(content, map[string]any{
			"type":  "tool_use",
			"id":    tc.ID,
			"name":  tc.Function.Name,
			"input": input,
		})
	}

	// Map finish_reason → stop_reason. Tool calls always mean tool_use,
	// even when a server also reports "stop".
	stop := "end_turn"
	switch ch.FinishReason {
	case openai.FinishReasonLength:
		stop = "max_tokens"
	case openai.FinishReasonToolCalls:
		stop = "tool_use"
	}
	if len(ch.Message.ToolCalls) > 0 {
		stop = "tool_use"
	}

	cacheRead := 0
	if resp.Usage.PromptTokensDetails != nil {
		cacheRead = resp.Usage.PromptTokensDetails.CachedTokens
	}

	id := resp.ID
	if id == "" {
		id = "msg_local"
	}
	wire := map[string]any{
		"id":            id,
		"type":          "message",
		"role":          "assistant",
		"model":         resp.Model,
		"content":       content,
		"stop_reason":   stop,
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":                resp.Usage.PromptTokens,
			"output_tokens":               resp.Usage.CompletionTokens,
			"cache_read_input_tokens":     cacheRead,
			"cache_creation_input_tokens": 0,
		},
	}
	blob, err := json.Marshal(wire)
	if err != nil {
		return nil, err
	}
	var msg anthropic.Message
	if err := json.Unmarshal(blob, &msg); err != nil {
		return nil, fmt.Errorf("bridge unmarshal: %w", err)
	}
	return &msg, nil
}
