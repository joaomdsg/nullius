package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// A recording fake for the terrain scout: captures the objective it was
// dispatched and returns a canned digest (or an error).
type fakeScout struct {
	gotObjective string
	gotTier      string
	reply        string
	isErr        bool
}

func (f *fakeScout) Run(_ context.Context, in json.RawMessage) (string, bool) {
	var m struct {
		Objective string `json:"objective"`
		Tier      string `json:"tier"`
	}
	_ = json.Unmarshal(in, &m)
	f.gotObjective = m.Objective
	f.gotTier = m.Tier
	return f.reply, f.isErr
}

// orient runs ONE fast scout to rapidly map the terrain and prepends the
// result to the mandate, so the leader opens oriented instead of guessing
// its workspace (the validation-run defect: the leader hallucinated a path
// and hunted the wrong repo). The workspace dir MUST be stated explicitly
// and the terrain dispatched on the FAST tier.
func TestOrientPrependsTerrainAndDir(t *testing.T) {
	fs := &fakeScout{reply: "loop.go:runTools mutates l.msgs under no lock — CONFIRM target"}
	dir := "/home/jgonc/Personal/repos/via-v2"
	mandate := "Hunt for concurrency defects."

	out := orient(context.Background(), fs, dir, mandate)

	// The workspace dir is stated so the leader never searches for it.
	if !strings.Contains(out, dir) {
		t.Fatalf("workspace dir not stated in oriented prompt:\n%s", out)
	}
	// The terrain digest is carried into the leader's opening context.
	if !strings.Contains(out, "loop.go:runTools") {
		t.Fatalf("terrain digest not prepended:\n%s", out)
	}
	// The original mandate survives verbatim.
	if !strings.Contains(out, mandate) {
		t.Fatalf("mandate lost:\n%s", out)
	}
	// The anti-wander instruction is the actual defect fix: the leader
	// must be told NOT to search the filesystem for its repo.
	if !strings.Contains(out, "never search the filesystem") {
		t.Fatalf("anti-wander instruction missing — leader may guess its path again:\n%s", out)
	}
	// The terrain scout ran on the fast tier and was told the dir + mandate.
	if fs.gotTier != "fast" {
		t.Errorf("terrain scout tier = %q, want fast", fs.gotTier)
	}
	if !strings.Contains(fs.gotObjective, dir) || !strings.Contains(fs.gotObjective, mandate) {
		t.Errorf("terrain objective missing dir or mandate: %q", fs.gotObjective)
	}
}

// If the terrain scout errors (or returns empty), orientation must still
// hand the leader the workspace dir and the mandate — never drop them —
// and tell it to map terrain itself.
func TestOrientFallbackOnScoutError(t *testing.T) {
	fs := &fakeScout{reply: "scout: 3 attempts exhausted", isErr: true}
	dir := "/work/repo"
	mandate := "Find fault-survival bugs."

	out := orient(context.Background(), fs, dir, mandate)

	if !strings.Contains(out, dir) || !strings.Contains(out, mandate) {
		t.Fatalf("fallback dropped dir or mandate:\n%s", out)
	}
	// The failed digest text must NOT masquerade as a real terrain map.
	if strings.Contains(out, "pre-mapped the terrain to orient") {
		t.Fatalf("error digest presented as a valid terrain map:\n%s", out)
	}
}
