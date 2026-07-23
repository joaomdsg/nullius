package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

// Free-tier providers (Zen) throttle in bursts: a 429 mid-run is transient,
// not fatal. The client must back off and retry instead of killing a
// 40-turn session on the first throttle (measured: laguna run died turn 4).
func TestStreamRetriesOn429(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":{"message":"Provider rate limit exceeded","type":"rate_limit_error"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"x","object":"chat.completion","model":"m","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer srv.Close()

	c := New(srv.URL+"/v1", "", "m")
	c.RetryBase = time.Millisecond // fast test; production default is seconds

	msg, err := c.Stream(context.Background(), anthropic.MessageNewParams{
		Messages: []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("hi"))},
	}, nil)
	if err != nil {
		t.Fatalf("429s must be retried through, got: %v", err)
	}
	if calls != 3 {
		t.Fatalf("want 2 retries then success (3 calls), got %d", calls)
	}
	if len(msg.Content) == 0 {
		t.Fatal("no content bridged")
	}
}

// Non-429 errors stay fatal — retrying a 401 or a bad request just burns time.
func TestStreamDoesNotRetryOn401(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"Invalid API key"}}`))
	}))
	defer srv.Close()

	c := New(srv.URL+"/v1", "", "m")
	c.RetryBase = time.Millisecond
	_, err := c.Stream(context.Background(), anthropic.MessageNewParams{
		Messages: []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("hi"))},
	}, nil)
	if err == nil || calls != 1 {
		t.Fatalf("401 must fail immediately: err=%v calls=%d", err, calls)
	}
}

// A cancelled context aborts the backoff wait — no zombie retry loops.
func TestStreamRetryHonorsContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"rate limit"}}`))
	}))
	defer srv.Close()

	c := New(srv.URL+"/v1", "", "m")
	c.RetryBase = time.Hour // would hang forever if ctx were ignored
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := c.Stream(ctx, anthropic.MessageNewParams{
		Messages: []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("hi"))},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "context") {
		t.Fatalf("want context abort, got: %v", err)
	}
}
