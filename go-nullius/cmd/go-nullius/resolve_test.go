package main

import "testing"

// resolveModel maps the leader/scout tier aliases to their endpoints — the
// mapping the two-tier orchestrator relies on to send judgment to smart and
// bulk to fast. A raw id falls back to the fast endpoint's base URL.
func TestResolveModel(t *testing.T) {
	t.Setenv("NULLIUS_BASE_URL", "") // ensure no override leaks in

	if lm := resolveModel("fast"); lm.id != "qwen3.6" || lm.baseURL != "http://192.168.11.41:8081/v1" {
		t.Errorf("fast → %+v", lm)
	}
	if lm := resolveModel("smart"); lm.id != "minimax-m2.7" || lm.baseURL != "http://192.168.11.41:8080/v1" {
		t.Errorf("smart → %+v", lm)
	}
	// Hosted Zen aliases. Researched 2026-07-23: deepseek-v4-flash is the
	// smartest free coder, nemotron-3-ultra the fastest — names deceive.
	if lm := resolveModel("zen-smart"); lm.id != "deepseek-v4-flash-free" || lm.baseURL != "https://opencode.ai/zen/v1" {
		t.Errorf("zen-smart → %+v", lm)
	}
	if lm := resolveModel("zen-fast"); lm.id != "nemotron-3-ultra-free" || lm.baseURL != "https://opencode.ai/zen/v1" {
		t.Errorf("zen-fast → %+v", lm)
	}
	if lm := resolveModel("zen-laguna"); lm.id != "laguna-s-2.1-free" {
		t.Errorf("zen-laguna → %+v", lm)
	}
	if lm := resolveModel("zen-mini"); lm.id != "north-mini-code-free" {
		t.Errorf("zen-mini → %+v", lm)
	}
	// A raw (non-alias) id keeps the id and defaults to the fast base URL.
	if lm := resolveModel("some-raw-id"); lm.id != "some-raw-id" || lm.baseURL != "http://192.168.11.41:8081/v1" {
		t.Errorf("raw id → %+v", lm)
	}
}

func TestEnvIntOr(t *testing.T) {
	if got := envIntOr("NULLIUS_NOPE_XYZ", 3); got != 3 {
		t.Errorf("unset → %d, want default 3", got)
	}
	t.Setenv("NULLIUS_MAX_FAST_TEST", "7")
	if got := envIntOr("NULLIUS_MAX_FAST_TEST", 3); got != 7 {
		t.Errorf("set=7 → %d, want 7", got)
	}
	t.Setenv("NULLIUS_MAX_FAST_TEST", "notanint")
	if got := envIntOr("NULLIUS_MAX_FAST_TEST", 3); got != 3 {
		t.Errorf("garbage → %d, want default 3", got)
	}
}

// The concurrent-slot cap is read from NULLIUS_MAX_FAST_SLOTS (renamed
// from NULLIUS_MAX_FAST). Pin the live key name so the flag default can't
// silently revert to the old env var.
func TestMaxFastSlotsEnvKey(t *testing.T) {
	t.Setenv("NULLIUS_MAX_FAST_SLOTS", "5")
	if got := envIntOr("NULLIUS_MAX_FAST_SLOTS", 3); got != 5 {
		t.Errorf("NULLIUS_MAX_FAST_SLOTS=5 → %d, want 5", got)
	}
}

func TestResolveModelBaseURLOverride(t *testing.T) {
	t.Setenv("NULLIUS_BASE_URL", "http://localhost:9999/v1")
	if lm := resolveModel("smart"); lm.baseURL != "http://localhost:9999/v1" || lm.id != "minimax-m2.7" {
		t.Errorf("override → %+v", lm)
	}
}
