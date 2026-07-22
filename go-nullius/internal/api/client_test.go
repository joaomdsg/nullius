package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"go-nullius/internal/auth"
)

func fakeMessageJSON(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id": "msg_test", "type": "message", "role": "assistant",
		"content":     []map[string]any{{"type": "text", "text": "ok"}},
		"model":       "claude-haiku-4-5",
		"stop_reason": "end_turn",
		"usage":       map[string]any{"input_tokens": 1, "output_tokens": 1},
	})
}

// homeWithOwnStore builds a fake home whose nullius store holds a
// fabricated OAuth pair with the given expiry.
func homeWithOwnStore(t *testing.T, access string, expiresAt int64) string {
	t.Helper()
	h := t.TempDir()
	dir := filepath.Join(h, ".config", "go-nullius")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{
		"access_token": access, "refresh_token": "refresh-fake", "expires_at": expiresAt,
	})
	if err := os.WriteFile(filepath.Join(dir, "credentials.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return h
}

func params() anthropic.MessageNewParams {
	return anthropic.MessageNewParams{
		Model:     "claude-haiku-4-5",
		MaxTokens: 16,
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("hi"))},
	}
}

func TestOAuthHeadersOnMessages(t *testing.T) {
	var gotAuth, gotBeta, gotAPIKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotBeta = r.Header.Get("anthropic-beta")
		gotAPIKey = r.Header.Get("x-api-key")
		fakeMessageJSON(w)
	}))
	defer srv.Close()

	res := &auth.Resolver{HomeDir: homeWithOwnStore(t, "tok-fake", 9999999999999)}
	c, err := New(res, option.WithBaseURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	msg, err := c.Message(context.Background(), params())
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer tok-fake" {
		t.Errorf("Authorization = %q, want Bearer tok-fake", gotAuth)
	}
	if !strings.Contains(gotBeta, OAuthBeta) {
		t.Errorf("anthropic-beta = %q, must contain %s", gotBeta, OAuthBeta)
	}
	if gotAPIKey != "" {
		t.Error("x-api-key must be absent on the OAuth path")
	}
	if msg.StopReason != anthropic.StopReasonEndTurn {
		t.Errorf("stop_reason = %v", msg.StopReason)
	}
}

func TestAPIKeyPath(t *testing.T) {
	var gotAPIKey, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("x-api-key")
		gotAuth = r.Header.Get("Authorization")
		fakeMessageJSON(w)
	}))
	defer srv.Close()

	t.Setenv("ANTHROPIC_API_KEY", "sk-env-fake")
	res := &auth.Resolver{HomeDir: t.TempDir()} // no stores → env
	c, err := New(res, option.WithBaseURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Message(context.Background(), params()); err != nil {
		t.Fatal(err)
	}
	if gotAPIKey != "sk-env-fake" || gotAuth != "" {
		t.Errorf("api-key path headers wrong: x-api-key=%q auth=%q", gotAPIKey, gotAuth)
	}
}

func TestRefreshOn401ThenRetry(t *testing.T) {
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "tok-rotated", "refresh_token": "refresh-rotated", "expires_in": 3600,
		})
	})
	mux.HandleFunc("/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(401)
			w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"expired"}}`))
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok-rotated" {
			t.Errorf("retry used %q, want rotated token", got)
		}
		fakeMessageJSON(w)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	res := &auth.Resolver{
		HomeDir:       homeWithOwnStore(t, "tok-stale", 9999999999999), // not expired locally, dead server-side
		TokenEndpoint: srv.URL + "/v1/oauth/token",
	}
	c, err := New(res, option.WithBaseURL(srv.URL), option.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	msg, err := c.Message(context.Background(), params())
	if err != nil {
		t.Fatalf("refresh-on-401 retry failed: %v", err)
	}
	if calls.Load() != 2 || msg.StopReason != anthropic.StopReasonEndTurn {
		t.Errorf("calls=%d stop=%v, want 2 calls ending clean", calls.Load(), msg.StopReason)
	}
}

func TestProactiveRefreshWhenExpired(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "tok-fresh", "refresh_token": "r2", "expires_in": 3600,
		})
	})
	mux.HandleFunc("/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok-fresh" {
			t.Errorf("expired token was not proactively refreshed: %q", got)
		}
		fakeMessageJSON(w)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	res := &auth.Resolver{
		HomeDir:       homeWithOwnStore(t, "tok-old", 1), // long expired
		TokenEndpoint: srv.URL + "/v1/oauth/token",
	}
	c, err := New(res, option.WithBaseURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Message(context.Background(), params()); err != nil {
		t.Fatal(err)
	}
}

// TestLiveStreamCanary: one real streamed haiku turn over the resolved
// credential (approved). Gated by NULLIUS_LIVE_CANARY=1. Asserts the
// stream accumulates to a clean end_turn with non-empty text; logs usage
// counts only — never credential material.
func TestLiveStreamCanary(t *testing.T) {
	if os.Getenv("NULLIUS_LIVE_CANARY") != "1" {
		t.Skip("set NULLIUS_LIVE_CANARY=1 for a live streamed turn")
	}
	c, err := New(&auth.Resolver{})
	if err != nil {
		t.Fatal(err)
	}
	events := 0
	p := params()
	p.Messages = []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("Reply with exactly: ok"))}
	msg, err := c.Stream(context.Background(), p, func(anthropic.MessageStreamEventUnion) { events++ })
	if err != nil {
		t.Fatalf("live stream failed: %v", err)
	}
	var text string
	for _, b := range msg.Content {
		if tb, ok := b.AsAny().(anthropic.TextBlock); ok {
			text += tb.Text
		}
	}
	t.Logf("stop=%s events=%d in=%d out=%d text_len=%d",
		msg.StopReason, events, msg.Usage.InputTokens, msg.Usage.OutputTokens, len(text))
	if text == "" || msg.StopReason != anthropic.StopReasonEndTurn {
		t.Errorf("want non-empty text with end_turn, got stop=%s text=%q", msg.StopReason, text)
	}
}
