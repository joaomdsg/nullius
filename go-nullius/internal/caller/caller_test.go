package caller

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// a verdict is the smallest real schema the caller carries.
type verdict struct {
	Stands       bool `json:"stands"`
	RefutingLine *int `json:"refuting_line"`
}

// okServer returns a llama.cpp-shaped chat completion whose message content is
// the given (already-JSON) string, and records every request body it saw.
func okServer(t *testing.T, content string) (*httptest.Server, *[][]byte) {
	t.Helper()
	var bodies [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, b)
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"choices": []map[string]any{
				{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": content}},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv, &bodies
}

func newTestCaller(fastURL, smartURL string) *HTTPCaller {
	c := New("", map[Tier]Endpoint{
		Fast:  {BaseURL: fastURL + "/v1", Model: "qwen"},
		Smart: {BaseURL: smartURL + "/v1", Model: "glm"},
	})
	c.RetryBase = time.Millisecond // fast test
	return c
}

func decodeReq(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("request body not JSON: %v", err)
	}
	return m
}

func TestAskHappyPath(t *testing.T) {
	srv, bodies := okServer(t, `{"stands":true,"refuting_line":null}`)
	c := newTestCaller(srv.URL, srv.URL)

	var v verdict
	if err := c.Ask(context.Background(), Fast, "is this a defect?", GBNF("root ::= object"), &v); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if !v.Stands || v.RefutingLine != nil {
		t.Fatalf("decoded wrong: %+v", v)
	}
	if len(*bodies) != 1 {
		t.Fatalf("want 1 request, got %d", len(*bodies))
	}
	m := decodeReq(t, (*bodies)[0])
	// The model must have NO side-effect channel: never any tools.
	if _, ok := m["tools"]; ok {
		t.Error("request carried a tools field — the model must not be able to act")
	}
	if m["grammar"] != "root ::= object" {
		t.Errorf("grammar not forwarded: %v", m["grammar"])
	}
	if m["model"] != "qwen" {
		t.Errorf("Fast tier used model %v, want qwen", m["model"])
	}
	if m["temperature"] != float64(0) {
		t.Errorf("temperature=%v, want 0 (deterministic)", m["temperature"])
	}
	if m["stream"] != false {
		t.Errorf("stream=%v, want false (one request, one answer)", m["stream"])
	}
	if m["max_tokens"] != float64(defaultMaxTokens) {
		t.Errorf("max_tokens=%v, want default %d", m["max_tokens"], defaultMaxTokens)
	}
}

func TestAskStrictDecodeRejectsUnknownField(t *testing.T) {
	// A reply with a field not in the schema must be rejected, not silently
	// accepted — strict decode is the schema gate.
	srv, bodies := okServer(t, `{"stands":true,"refuting_line":null,"hallucinated":"x"}`)
	c := newTestCaller(srv.URL, srv.URL)

	var v verdict
	err := c.Ask(context.Background(), Fast, "p", "", &v)
	if !errors.Is(err, ErrExhausted) {
		t.Fatalf("unknown field must exhaust retries with ErrExhausted, got: %v", err)
	}
	if len(*bodies) != parseRetries+1 {
		t.Fatalf("want %d attempts (1 + %d retries), got %d", parseRetries+1, parseRetries, len(*bodies))
	}
}

func TestAskRetryThenSucceed(t *testing.T) {
	// First reply is not valid JSON; the caller re-asks WITH the parse error
	// appended and the second (valid) reply is accepted.
	var calls int
	var bodies [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, b)
		calls++
		content := `not json at all`
		if calls >= 2 {
			content = `{"stands":false,"refuting_line":42}`
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": content}}},
		})
	}))
	defer srv.Close()
	c := newTestCaller(srv.URL, srv.URL)

	var v verdict
	if err := c.Ask(context.Background(), Fast, "p", "", &v); err != nil {
		t.Fatalf("second reply valid, Ask should succeed: %v", err)
	}
	if calls != 2 {
		t.Fatalf("want 2 calls, got %d", calls)
	}
	if v.Stands || v.RefutingLine == nil || *v.RefutingLine != 42 {
		t.Fatalf("decoded wrong: %+v", v)
	}
	// The retry must carry the prior bad output + the parse error, so the model
	// can correct itself rather than repeat the mistake blind.
	m := decodeReq(t, bodies[1])
	msgs, _ := json.Marshal(m["messages"])
	if !strings.Contains(string(msgs), "did not satisfy") {
		t.Errorf("retry request did not append the parse error: %s", msgs)
	}
}

func TestAskTierRouting(t *testing.T) {
	fast, fb := okServer(t, `{"stands":true,"refuting_line":null}`)
	smart, sb := okServer(t, `{"stands":true,"refuting_line":null}`)
	c := newTestCaller(fast.URL, smart.URL)

	var v verdict
	if err := c.Ask(context.Background(), Smart, "p", "", &v); err != nil {
		t.Fatal(err)
	}
	if len(*sb) != 1 || len(*fb) != 0 {
		t.Fatalf("Smart tier hit wrong endpoint: fast=%d smart=%d", len(*fb), len(*sb))
	}
	if decodeReq(t, (*sb)[0])["model"] != "glm" {
		t.Error("Smart tier used the wrong model")
	}
}

func TestAskUnknownTier(t *testing.T) {
	c := New("", map[Tier]Endpoint{Fast: {BaseURL: "http://x/v1", Model: "m"}})
	var v verdict
	if err := c.Ask(context.Background(), Smart, "p", "", &v); err == nil {
		t.Fatal("Ask on an unconfigured tier must error, not silently pick another")
	}
}

func TestAskWithMaxTokens(t *testing.T) {
	srv, bodies := okServer(t, `{"stands":true,"refuting_line":null}`)
	c := newTestCaller(srv.URL, srv.URL)
	var v verdict
	if err := c.Ask(context.Background(), Fast, "p", "", &v, WithMaxTokens(1500)); err != nil {
		t.Fatal(err)
	}
	if decodeReq(t, (*bodies)[0])["max_tokens"] != float64(1500) {
		t.Errorf("WithMaxTokens ignored: %v", decodeReq(t, (*bodies)[0])["max_tokens"])
	}
}

func TestAskPromptWall(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer srv.Close()
	c := newTestCaller(srv.URL, srv.URL)

	huge := strings.Repeat("x", maxPromptTokens*5)
	var v verdict
	err := c.Ask(context.Background(), Fast, huge, "", &v)
	if err == nil || !strings.Contains(err.Error(), "prompt-size wall") {
		t.Fatalf("oversized prompt: err=%v, want prompt-size wall", err)
	}
	if called {
		t.Error("oversized prompt was sent — the wall must fire before dialing")
	}
}

func TestAskReadsReasoningContentWhenContentEmpty(t *testing.T) {
	// Live finding (2026-07-23): grammar-constrained reasoning models (minimax-m2.7,
	// qwen3.6) put the constrained JSON in reasoning_content and leave content empty.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","reasoning_content":"{\"stands\":true,\"refuting_line\":null}"}}]}`))
	}))
	defer srv.Close()
	c := newTestCaller(srv.URL, srv.URL)

	var v verdict
	if err := c.Ask(context.Background(), Fast, "p", GBNF("root ::= object"), &v); err != nil {
		t.Fatalf("empty content + reasoning_content JSON must decode: %v", err)
	}
	if !v.Stands {
		t.Fatalf("did not read reasoning_content: %+v", v)
	}
}

func TestAskPrefersContentOverReasoning(t *testing.T) {
	// When content IS populated, reasoning_content (the chain-of-thought) is ignored.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"stands\":false,\"refuting_line\":7}","reasoning_content":"let me think... {\"stands\":true}"}}]}`))
	}))
	defer srv.Close()
	c := newTestCaller(srv.URL, srv.URL)

	var v verdict
	if err := c.Ask(context.Background(), Fast, "p", "", &v); err != nil {
		t.Fatal(err)
	}
	if v.Stands || v.RefutingLine == nil || *v.RefutingLine != 7 {
		t.Fatalf("must prefer content, not reasoning: %+v", v)
	}
}

func TestAskTransportRetriesOn429(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limit"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": `{"stands":true,"refuting_line":null}`}}},
		})
	}))
	defer srv.Close()
	c := newTestCaller(srv.URL, srv.URL)

	var v verdict
	if err := c.Ask(context.Background(), Fast, "p", "", &v); err != nil {
		t.Fatalf("429 must be retried through: %v", err)
	}
	if calls != 3 {
		t.Fatalf("want 3 transport attempts, got %d", calls)
	}
}

// grammarOf extracts the "grammar" field from a recorded request body ("" if absent).
func grammarOf(t *testing.T, raw []byte) string {
	t.Helper()
	m := decodeReq(t, raw)
	g, _ := m["grammar"].(string)
	return g
}

func TestAskRetriesUnconstrainedOnGrammarCrash(t *testing.T) {
	var bodies [][]byte
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, b)
		calls++
		if calls == 1 {
			// llama.cpp's intermittent server-side grammar crash.
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"Unexpected empty grammar stack"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": `{"stands":true,"refuting_line":null}`}}},
		})
	}))
	defer srv.Close()
	c := newTestCaller(srv.URL, srv.URL)

	var v verdict
	if err := c.Ask(context.Background(), Fast, "p", "root ::= object", &v); err != nil {
		t.Fatalf("grammar crash must recover via unconstrained retry: %v", err)
	}
	if calls != 2 {
		t.Fatalf("want exactly 2 calls (crash + unconstrained retry), got %d", calls)
	}
	if g := grammarOf(t, bodies[0]); g != "root ::= object" {
		t.Fatalf("first call must carry the grammar, got %q", g)
	}
	if g := grammarOf(t, bodies[1]); g != "" {
		t.Fatalf("retry must be UNCONSTRAINED (empty grammar), got %q", g)
	}
	if !v.Stands {
		t.Fatal("decoded verdict wrong")
	}
}

func TestAskGrammarCrashDoesNotBackoffLoop(t *testing.T) {
	// A persistent grammar crash: the constrained call fast-fails (1 hit), then the
	// unconstrained retries fall through the generic 5xx backoff path (transportRetryMax).
	// The point: the CONSTRAINED grammar is tried exactly once, never in a backoff loop.
	var constrainedCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if grammarOf(t, b) != "" {
			constrainedCalls++
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"grammar stack empty"}`))
	}))
	defer srv.Close()
	c := newTestCaller(srv.URL, srv.URL)

	var v verdict
	if err := c.Ask(context.Background(), Fast, "p", "root ::= object", &v); err == nil {
		t.Fatal("persistent crash must return an error")
	}
	if constrainedCalls != 1 {
		t.Fatalf("constrained grammar must be tried exactly once, got %d", constrainedCalls)
	}
}

func TestAskGenericServerErrorKeepsGrammar(t *testing.T) {
	// A 5xx WITHOUT "grammar" in the body is NOT a grammar crash — it must take the normal
	// backoff-retry path with the grammar intact, never silently drop the constraint.
	var bodies [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, b)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal server error"}`))
	}))
	defer srv.Close()
	c := newTestCaller(srv.URL, srv.URL)

	var v verdict
	if err := c.Ask(context.Background(), Fast, "p", "root ::= object", &v); err == nil {
		t.Fatal("persistent 500 must error")
	}
	if len(bodies) != transportRetryMax {
		t.Fatalf("generic 500 must use %d backoff retries, got %d", transportRetryMax, len(bodies))
	}
	for i, b := range bodies {
		if g := grammarOf(t, b); g != "root ::= object" {
			t.Fatalf("call %d dropped the grammar on a non-grammar 500: %q", i, g)
		}
	}
}

func TestAskContextCancel(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // block until the test releases us; client-side cancel unblocks Ask on its own
	}))
	defer srv.Close()    // runs second (LIFO): the handler is already free to exit
	defer close(release) // runs first: let the blocked handler return so Close won't hang
	c := newTestCaller(srv.URL, srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	var v verdict
	err := c.Ask(ctx, Fast, "p", "", &v)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled ctx: err=%v, want context.Canceled", err)
	}
}
