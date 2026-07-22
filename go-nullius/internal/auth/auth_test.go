package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fake home with a CC-shaped store. All token values are fabricated.
func homeWithCC(t *testing.T, access, refresh string, expiresAt int64) string {
	t.Helper()
	h := t.TempDir()
	dir := filepath.Join(h, ".claude")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken": access, "refreshToken": refresh, "expiresAt": expiresAt,
			"scopes": []string{"user:inference"}, "subscriptionType": "team",
		},
	})
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestLoadPrecedence(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-test-fake")

	// CC store present → wins over env.
	h := homeWithCC(t, "cc-access-fake", "cc-refresh-fake", 9999999999999)
	r := &Resolver{HomeDir: h}
	c, err := r.Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Source != "claude-code" || c.AccessToken != "cc-access-fake" || !c.OAuth() {
		t.Errorf("want claude-code oauth credential, got source=%s", c.Source)
	}

	// Own store present → wins over CC.
	own := filepath.Join(h, ".config", "go-nullius")
	if err := os.MkdirAll(own, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(ownStore{AccessToken: "own-access-fake", RefreshToken: "own-refresh-fake", ExpiresAt: 1})
	if err := os.WriteFile(filepath.Join(own, "credentials.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	c, err = r.Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Source != "nullius-store" || c.AccessToken != "own-access-fake" {
		t.Errorf("want nullius-store credential, got source=%s", c.Source)
	}

	// No stores → env fallback, marked non-OAuth.
	r2 := &Resolver{HomeDir: t.TempDir()}
	c, err = r2.Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Source != "env" || c.OAuth() || c.Expired(time.Now()) {
		t.Errorf("want non-expiring env credential, got source=%s oauth=%v", c.Source, c.OAuth())
	}

	// Nothing at all → honest error.
	t.Setenv("ANTHROPIC_API_KEY", "")
	if _, err := r2.Load(); err == nil {
		t.Error("want error when no credential source exists")
	}
}

func TestExpiredMargin(t *testing.T) {
	now := time.Now()
	c := &Credentials{ExpiresAt: now.Add(4 * time.Minute).UnixMilli()}
	if !c.Expired(now) {
		t.Error("token inside the 5min margin must count as expired")
	}
	c.ExpiresAt = now.Add(10 * time.Minute).UnixMilli()
	if c.Expired(now) {
		t.Error("token beyond the margin must not count as expired")
	}
}

func TestRefreshRotatesAndPersistsToOwnStoreOnly(t *testing.T) {
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if err := json.NewDecoder(req.Body).Decode(&gotBody); err != nil {
			t.Error(err)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "new-access-fake", "refresh_token": "new-refresh-fake", "expires_in": 3600,
		})
	}))
	defer srv.Close()

	h := homeWithCC(t, "cc-access-fake", "cc-refresh-fake", 1)
	ccPath := filepath.Join(h, ".claude", ".credentials.json")
	ccBefore, _ := os.ReadFile(ccPath)

	r := &Resolver{HomeDir: h, TokenEndpoint: srv.URL, ClientID: "test-client"}
	c, err := r.Load()
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := r.Refresh(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}

	if gotBody["grant_type"] != "refresh_token" || gotBody["client_id"] != "test-client" ||
		gotBody["refresh_token"] != "cc-refresh-fake" {
		t.Errorf("refresh body wrong: %v", gotBody)
	}
	if fresh.AccessToken != "new-access-fake" || fresh.RefreshToken != "new-refresh-fake" {
		t.Error("rotated pair not adopted")
	}
	if fresh.ExpiresAt <= time.Now().UnixMilli() {
		t.Error("expiry not projected forward from expires_in")
	}

	// New pair landed in OUR store; CC's file is byte-identical.
	reloaded, err := r.Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Source != "nullius-store" || reloaded.AccessToken != "new-access-fake" {
		t.Errorf("refreshed pair not persisted to own store, got source=%s", reloaded.Source)
	}
	ccAfter, _ := os.ReadFile(ccPath)
	if string(ccBefore) != string(ccAfter) {
		t.Error("Claude Code's credential file was modified — must never be written")
	}
	fi, err := os.Stat(filepath.Join(h, ".config", "go-nullius", "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("own store perms = %o, want 600", fi.Mode().Perm())
	}
}

func TestRefreshKeepsOldRefreshTokenWhenNotRotated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"access_token": "new-access-fake", "expires_in": 60})
	}))
	defer srv.Close()
	r := &Resolver{HomeDir: t.TempDir(), TokenEndpoint: srv.URL}
	fresh, err := r.Refresh(context.Background(), &Credentials{RefreshToken: "keep-me-fake", Source: "claude-code"})
	if err != nil {
		t.Fatal(err)
	}
	if fresh.RefreshToken != "keep-me-fake" {
		t.Error("non-rotating response must keep the prior refresh token")
	}
}

func TestRefreshErrorPaths(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
	}))
	defer srv.Close()
	r := &Resolver{HomeDir: t.TempDir(), TokenEndpoint: srv.URL}

	if _, err := r.Refresh(context.Background(), &Credentials{Source: "env"}); err == nil {
		t.Error("refresh without a refresh token must error")
	}
	if _, err := r.Refresh(context.Background(), &Credentials{RefreshToken: "x-fake"}); err == nil {
		t.Error("non-200 from token endpoint must error")
	}
}
