// Package auth resolves API credentials for go-nullius.
//
// Precedence: go-nullius's own store (tokens it refreshed itself) →
// Claude Code's store (~/.claude/.credentials.json) → ANTHROPIC_API_KEY.
//
// The token endpoint rotates refresh tokens, so a refresh performed with
// Claude Code's refresh token may invalidate Claude Code's stored copy.
// To keep that blast radius contained, refreshed pairs are persisted ONLY
// to our own store; Claude Code's file is never written.
//
// Token values must never be logged or printed — errors carry sources and
// shapes, never contents.
package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// DefaultClientID is Claude Code's public OAuth client identifier — the
// client the stored refresh tokens were granted to. Overridable via
// NULLIUS_OAUTH_CLIENT_ID. Verified empirically at the refresh canary.
const DefaultClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"

// DefaultTokenEndpoint is the subscription OAuth refresh endpoint
// (verified against pi's source; see BUILD-STATE.md).
const DefaultTokenEndpoint = "https://platform.claude.com/v1/oauth/token"

// expiryMargin: treat a token expiring within this window as expired,
// so a request never departs with a token that dies in flight.
const expiryMargin = 5 * time.Minute

// Credentials is a resolved auth identity. Exactly one of the two modes
// holds: OAuth (AccessToken + RefreshToken + ExpiresAt) or API key
// (AccessToken only, ExpiresAt == 0).
type Credentials struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    int64  // unix ms; 0 = does not expire (API key)
	Source       string // "nullius-store" | "claude-code" | "env"
}

// OAuth reports whether this credential rides the subscription OAuth path
// (Bearer + anthropic-beta: oauth-2025-04-20) rather than x-api-key.
func (c *Credentials) OAuth() bool { return c.Source != "env" }

// Expired reports whether the access token is expired or inside the
// safety margin. API keys never expire.
func (c *Credentials) Expired(now time.Time) bool {
	if c.ExpiresAt == 0 {
		return false
	}
	return now.UnixMilli() >= c.ExpiresAt-expiryMargin.Milliseconds()
}

// Resolver loads and refreshes credentials. Paths and endpoint are
// injectable for tests; zero values fall back to real locations.
type Resolver struct {
	HomeDir       string // "" → os.UserHomeDir()
	TokenEndpoint string // "" → DefaultTokenEndpoint
	ClientID      string // "" → env NULLIUS_OAUTH_CLIENT_ID → DefaultClientID
	HTTPClient    *http.Client
}

func (r *Resolver) home() (string, error) {
	if r.HomeDir != "" {
		return r.HomeDir, nil
	}
	return os.UserHomeDir()
}

func (r *Resolver) ownStorePath() (string, error) {
	h, err := r.home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".config", "go-nullius", "credentials.json"), nil
}

func (r *Resolver) ccStorePath() (string, error) {
	h, err := r.home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".claude", ".credentials.json"), nil
}

func (r *Resolver) clientID() string {
	if r.ClientID != "" {
		return r.ClientID
	}
	if v := os.Getenv("NULLIUS_OAUTH_CLIENT_ID"); v != "" {
		return v
	}
	return DefaultClientID
}

// ccStore mirrors Claude Code's credential file shape (keys only; values
// are read into memory to authenticate and never printed).
type ccStore struct {
	ClaudeAiOauth struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresAt    int64  `json:"expiresAt"` // ms
	} `json:"claudeAiOauth"`
}

// ownStore is go-nullius's persisted token pair (post-refresh).
type ownStore struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"` // ms
}

// Load resolves credentials by precedence. A store that exists but is
// unreadable/corrupt is skipped with the error folded into the final
// failure message (sources only, never contents).
func (r *Resolver) Load() (*Credentials, error) {
	var probs []string

	if p, err := r.ownStorePath(); err == nil {
		if c, err := loadJSON[ownStore](p); err == nil && c.AccessToken != "" {
			return &Credentials{
				AccessToken: c.AccessToken, RefreshToken: c.RefreshToken,
				ExpiresAt: c.ExpiresAt, Source: "nullius-store",
			}, nil
		} else if err != nil && !os.IsNotExist(err) {
			probs = append(probs, fmt.Sprintf("nullius store %s: unreadable", p))
		}
	}

	if p, err := r.ccStorePath(); err == nil {
		if c, err := loadJSON[ccStore](p); err == nil && c.ClaudeAiOauth.AccessToken != "" {
			return &Credentials{
				AccessToken:  c.ClaudeAiOauth.AccessToken,
				RefreshToken: c.ClaudeAiOauth.RefreshToken,
				ExpiresAt:    c.ClaudeAiOauth.ExpiresAt,
				Source:       "claude-code",
			}, nil
		} else if err != nil && !os.IsNotExist(err) {
			probs = append(probs, fmt.Sprintf("claude-code store %s: unreadable", p))
		}
	}

	if k := os.Getenv("ANTHROPIC_API_KEY"); k != "" {
		return &Credentials{AccessToken: k, Source: "env"}, nil
	}

	return nil, fmt.Errorf("no credentials: no usable OAuth store and ANTHROPIC_API_KEY unset (%v)", probs)
}

func loadJSON[T any](path string) (*T, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &v, nil
}

// refreshResp is the token endpoint's success body.
type refreshResp struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"` // seconds
}

// Refresh exchanges the refresh token for a new pair, persists it to
// go-nullius's own store (0600), and returns the new credentials.
// Claude Code's store is never written.
func (r *Resolver) Refresh(ctx context.Context, c *Credentials) (*Credentials, error) {
	if c.RefreshToken == "" {
		return nil, fmt.Errorf("refresh: credential from %s has no refresh token", c.Source)
	}
	endpoint := r.TokenEndpoint
	if endpoint == "" {
		endpoint = DefaultTokenEndpoint
	}
	body, err := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"client_id":     r.clientID(),
		"refresh_token": c.RefreshToken,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	hc := r.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("refresh: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Body may describe the failure; cap it and never echo tokens
		// (the request carried them, the error body should not).
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("refresh: token endpoint returned %d: %s", resp.StatusCode, snippet)
	}
	var tr refreshResp
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tr); err != nil {
		return nil, fmt.Errorf("refresh: decode response: %w", err)
	}
	if tr.AccessToken == "" {
		return nil, fmt.Errorf("refresh: response missing access_token")
	}

	fresh := &Credentials{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second).UnixMilli(),
		Source:       "nullius-store",
	}
	if fresh.RefreshToken == "" {
		// Endpoint chose not to rotate; keep using the old one.
		fresh.RefreshToken = c.RefreshToken
	}
	if err := r.saveOwn(fresh); err != nil {
		// The new pair is live server-side; losing it orphans the grant
		// chain. Surface loudly rather than pretend the refresh failed.
		return fresh, fmt.Errorf("refresh succeeded but persisting to own store failed: %w", err)
	}
	return fresh, nil
}

func (r *Resolver) saveOwn(c *Credentials) error {
	p, err := r.ownStorePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(ownStore{
		AccessToken: c.AccessToken, RefreshToken: c.RefreshToken, ExpiresAt: c.ExpiresAt,
	}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".cred-*.json")
	if err != nil {
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), p)
}
