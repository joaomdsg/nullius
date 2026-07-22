package auth

import (
	"os"
	"testing"
	"time"
)

// TestLiveReadCanary verifies the real credential resolution path against
// this machine's actual stores. Gated: NULLIUS_LIVE_CANARY=1. It reports
// only source, presence booleans, and expiry — never token values.
//
// The live REFRESH exercise is deliberately NOT here: the endpoint rotates
// refresh tokens, and speculatively consuming Claude Code's would risk
// invalidating its stored copy. Refresh is exercised on a real 401 by the
// api client (stage 2), where it is actually needed.
func TestLiveReadCanary(t *testing.T) {
	if os.Getenv("NULLIUS_LIVE_CANARY") != "1" {
		t.Skip("set NULLIUS_LIVE_CANARY=1 to run against real credential stores")
	}
	r := &Resolver{}
	c, err := r.Load()
	if err != nil {
		t.Fatalf("live resolution failed: %v", err)
	}
	t.Logf("source=%s oauth=%v access_token_present=%v refresh_token_present=%v expired=%v expires_in=%s",
		c.Source, c.OAuth(), c.AccessToken != "", c.RefreshToken != "", c.Expired(time.Now()),
		time.Until(time.UnixMilli(c.ExpiresAt)).Round(time.Minute))
	if c.AccessToken == "" {
		t.Error("resolved credential has empty access token")
	}
	if c.OAuth() && c.Expired(time.Now()) && c.RefreshToken == "" {
		t.Error("expired OAuth token with no refresh token — cannot proceed to stage 2")
	}
}
