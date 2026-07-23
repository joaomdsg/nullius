// Package caller is the typed model-call substrate for the deterministic
// orchestrator: one request in, one grammar-constrained JSON answer out.
//
// Every judgment call the state machine makes goes through Ask. The design
// invariant is that the model can only DISCRIMINATE, never act: a request
// carries NO tools, so the model has no side-effect channel — all effects are
// Go's. The reply is constrained by a GBNF grammar (llama.cpp's strongest
// "forbid anything but this shape" control) and then strict-decoded
// (DisallowUnknownFields) into the caller's typed struct. A reply that does
// not satisfy the schema is re-asked up to parseRetries times with the parse
// error appended; on exhaustion Ask returns an error wrapping ErrExhausted so
// the consumer can apply its own schema-typed fallback (CANT_TELL /
// all-lenses / templated report) — that fallback is schema-specific and thus
// belongs to the consumer, not this generic transport.
package caller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Tier selects which model endpoint answers a call.
type Tier int

const (
	Fast  Tier = iota // bulk / cheap discrimination
	Smart             // escalated hard discrimination
)

func (t Tier) String() string {
	switch t {
	case Fast:
		return "fast"
	case Smart:
		return "smart"
	default:
		return fmt.Sprintf("tier(%d)", int(t))
	}
}

// GBNF is a grammar in llama.cpp's GBNF notation. Empty means unconstrained.
type GBNF string

// Endpoint is one OpenAI-compatible chat endpoint (a llama.cpp server). BaseURL
// includes the version prefix, e.g. "http://host:8080/v1".
type Endpoint struct {
	BaseURL string
	Model   string
}

// Caller is the judgment-call substrate. Consumers depend on this interface;
// HTTPCaller is the production implementation.
type Caller interface {
	Ask(ctx context.Context, tier Tier, prompt string, grammar GBNF, out any, opts ...AskOption) error
}

// ErrExhausted is wrapped by Ask when the model never produced a
// schema-satisfying reply within the retry budget. Consumers detect it with
// errors.Is and apply their schema-typed fallback.
var ErrExhausted = errors.New("caller: schema not satisfied after retries")

// ErrGrammarCrash marks a DETERMINISTIC server-side grammar failure — llama.cpp
// intermittently 5xx's on a valid GBNF with "grammar" in the error body (observed:
// "Unexpected empty grammar stack"). Retrying the SAME grammar crashes identically, so
// backing off is wasted; post fails fast with this and Ask retries ONCE unconstrained
// (strict-decode still enforces the schema). Consumers detect it with errors.Is.
var ErrGrammarCrash = errors.New("caller: server-side grammar crash")

const (
	// defaultMaxTokens is verdict-sized (design: verdict 400 + headroom).
	// Consumers raise it per schema via WithMaxTokens (plan 1500, report 3000).
	defaultMaxTokens = 512
	// maxPromptTokens is the INPUT wall: a request whose estimated prompt
	// exceeds it is refused before dialing, so a runaway prompt cannot be sent.
	maxPromptTokens = 200_000
	// parseRetries is how many times a schema-invalid reply is re-asked (with
	// the parse error appended) before Ask gives up with ErrExhausted.
	parseRetries = 2
	// transportRetryMax bounds backoff retries on 429 / 5xx.
	transportRetryMax = 5
)

// AskOption tunes a single Ask call without widening the core 5-arg contract.
type AskOption func(*askConfig)

type askConfig struct{ maxTokens int }

// WithMaxTokens overrides the output cap for one call (design: verdict 400 /
// plan 1500 / report 3000). Non-positive values are ignored.
func WithMaxTokens(n int) AskOption {
	return func(c *askConfig) {
		if n > 0 {
			c.maxTokens = n
		}
	}
}

// HTTPCaller talks to per-tier llama.cpp endpoints over the OpenAI-compatible
// chat API. It is safe for concurrent use (no per-call mutable state).
type HTTPCaller struct {
	apiKey    string
	endpoints map[Tier]Endpoint
	HTTP      *http.Client
	RetryBase time.Duration // base backoff between transport retries
}

// New builds a caller over a copy of the given per-tier endpoints.
func New(apiKey string, endpoints map[Tier]Endpoint) *HTTPCaller {
	m := make(map[Tier]Endpoint, len(endpoints))
	for k, v := range endpoints {
		m[k] = v
	}
	return &HTTPCaller{
		apiKey:    apiKey,
		endpoints: m,
		HTTP:      &http.Client{},
		RetryBase: 500 * time.Millisecond,
	}
}

// wire shapes — deliberately minimal; no Tools field exists on the request, so
// the model has no side-effect channel by construction.
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens"`
	Temperature float64       `json:"temperature"`
	Stream      bool          `json:"stream"`
	Grammar     string        `json:"grammar,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
			// ReasoningContent: llama.cpp reasoning models route grammar-constrained
			// tokens through the reasoning channel, leaving Content empty (verified live,
			// 2026-07-23, minimax-m2.7 + qwen3.6). Since every judgment call carries a
			// GBNF grammar, the constrained JSON lands here — we fall back to it when
			// Content is empty. (Without a grammar, Content populates normally and this
			// stays the chain-of-thought, which we ignore.)
			ReasoningContent string `json:"reasoning_content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

func (c *HTTPCaller) endpoint(t Tier) (Endpoint, error) {
	ep, ok := c.endpoints[t]
	if !ok || ep.BaseURL == "" {
		return Endpoint{}, fmt.Errorf("caller: no endpoint configured for tier %s", t)
	}
	return ep, nil
}

// Ask sends one prompt to the tier's endpoint, constrains the reply to grammar,
// strict-decodes it into out, and re-asks (with the parse error appended) up to
// parseRetries times. Transport (429/5xx) and context errors return directly —
// they are not fixable by re-asking. Schema exhaustion wraps ErrExhausted.
func (c *HTTPCaller) Ask(ctx context.Context, tier Tier, prompt string, grammar GBNF, out any, opts ...AskOption) error {
	cfg := askConfig{maxTokens: defaultMaxTokens}
	for _, o := range opts {
		o(&cfg)
	}
	ep, err := c.endpoint(tier)
	if err != nil {
		return err
	}
	if est := estimateTokens(prompt); est > maxPromptTokens {
		return fmt.Errorf("caller: prompt-size wall: est %d tokens > %d", est, maxPromptTokens)
	}

	msgs := []chatMessage{{Role: "user", Content: prompt}}
	g := string(grammar)
	var lastErr error
	for attempt := 0; attempt <= parseRetries; attempt++ {
		content, err := c.post(ctx, ep, msgs, g, cfg.maxTokens)
		if err != nil {
			if g != "" && errors.Is(err, ErrGrammarCrash) {
				// Server-side grammar crash is deterministic: drop the grammar and retry
				// unconstrained. strict-decode below still enforces the schema, and g stays
				// "" for the remaining parseRetries so we never re-trip the same crash.
				g = ""
				content, err = c.post(ctx, ep, msgs, g, cfg.maxTokens)
			}
			if err != nil {
				return err // transport / context error: re-asking cannot help
			}
		}
		dec := json.NewDecoder(strings.NewReader(content))
		dec.DisallowUnknownFields()
		if derr := dec.Decode(out); derr != nil {
			lastErr = derr
		} else if dec.More() {
			lastErr = errors.New("trailing content after JSON value")
		} else {
			return nil
		}
		// feed the model its own bad output + the reason, so it can correct.
		msgs = append(msgs,
			chatMessage{Role: "assistant", Content: content},
			chatMessage{Role: "user", Content: "Your previous reply did not satisfy the required JSON schema: " + lastErr.Error() + ". Reply again with ONLY the JSON value, no prose, no code fence."},
		)
	}
	return fmt.Errorf("%w: %v", ErrExhausted, lastErr)
}

// post performs one chat completion with transport-level backoff retries on
// 429/5xx, returning the assistant message content.
func (c *HTTPCaller) post(ctx context.Context, ep Endpoint, msgs []chatMessage, grammar string, maxTokens int) (string, error) {
	body, err := json.Marshal(chatRequest{
		Model:       ep.Model,
		Messages:    msgs,
		MaxTokens:   maxTokens,
		Temperature: 0,
		Stream:      false,
		Grammar:     grammar,
	})
	if err != nil {
		return "", err
	}
	url := strings.TrimRight(ep.BaseURL, "/") + "/chat/completions"
	base := c.RetryBase
	if base <= 0 {
		base = 500 * time.Millisecond
	}

	var lastErr error
	for attempt := 0; attempt < transportRetryMax; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")
		if c.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.apiKey)
		}

		resp, err := c.HTTP.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			lastErr = err
		} else {
			data, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			switch {
			case resp.StatusCode == http.StatusOK:
				var cr chatResponse
				if err := json.Unmarshal(data, &cr); err != nil {
					return "", fmt.Errorf("caller: decode response envelope: %w", err)
				}
				if len(cr.Choices) == 0 {
					return "", errors.New("caller: response had no choices")
				}
				msg := cr.Choices[0].Message
				if strings.TrimSpace(msg.Content) == "" {
					return msg.ReasoningContent, nil // grammar-constrained reasoning model
				}
				return msg.Content, nil
			case resp.StatusCode >= 500 && grammar != "" && strings.Contains(strings.ToLower(string(data)), "grammar"):
				// Deterministic grammar crash: retrying the same grammar will crash the
				// same way, so fail fast — Ask retries unconstrained. Not retried here.
				return "", fmt.Errorf("%w: transport %d: %s", ErrGrammarCrash, resp.StatusCode, snip(data))
			case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
				lastErr = fmt.Errorf("caller: transport %d: %s", resp.StatusCode, snip(data))
			default:
				return "", fmt.Errorf("caller: transport %d: %s", resp.StatusCode, snip(data))
			}
		}

		if attempt == transportRetryMax-1 {
			break // exhausted: return now instead of sleeping a backoff nobody waits on
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(backoff(base, attempt)):
		}
	}
	return "", fmt.Errorf("caller: transport exhausted after %d attempts: %w", transportRetryMax, lastErr)
}

func backoff(base time.Duration, attempt int) time.Duration {
	d := base << attempt // base * 2^attempt
	if cap := 30 * time.Second; d > cap {
		return cap
	}
	return d
}

// estimateTokens is the same cheap ~4-bytes/token + 15% headroom heuristic the
// transport layer uses elsewhere — no tokenizer dependency.
func estimateTokens(s string) int {
	return len(s) / 4 * 115 / 100
}

func snip(b []byte) string {
	const max = 200
	s := strings.TrimSpace(string(b))
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
