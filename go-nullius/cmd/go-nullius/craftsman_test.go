package main

import (
	"strings"
	"testing"
)

// The craftsman is the fast write+test tier the drain spawns. Its rules
// must demand the discipline the drain's RESULT parser and the leader's
// audit rely on: write the test first, run it, and end with the machine-
// readable RESULT line. If these drift the whole drain loop misclassifies.
func TestCraftsmanRulesContract(t *testing.T) {
	for _, must := range []string{"RESULT: DONE", "RESULT: FAILED", "test"} {
		if !strings.Contains(craftsmanRules, must) {
			t.Errorf("craftsmanRules missing required contract token %q", must)
		}
	}
	// The craftsman must be scoped to ONE minimal change — its safety net
	// is the leader's audit, not its own judgment, so scope creep is the
	// main risk to forbid.
	low := strings.ToLower(craftsmanRules)
	if !strings.Contains(low, "minimal") || !strings.Contains(low, "one") {
		t.Error("craftsmanRules must scope the craftsman to one minimal change")
	}
	// A no-op craftsman (work already applied by a prior serial step) must
	// still emit a DONE line, or the drain false-marks it failed.
	if !strings.Contains(low, "already") {
		t.Error("craftsmanRules must tell an already-satisfied craftsman to report DONE, not go silent")
	}
}

// The leader rules must teach the plan→drain→audit loop, or the smart
// model will keep hunting/fixing by hand (the turn/context blowup this
// mechanization exists to end).
func TestLeaderRulesTeachDrainLoop(t *testing.T) {
	for _, must := range []string{"plan", "drain", "audit"} {
		if !strings.Contains(strings.ToLower(leaderRules), must) {
			t.Errorf("leaderRules never mention %q — the drain loop is not taught", must)
		}
	}
	// Subprocess reports flow verbatim into the leader's context: the
	// rules must pin them as testimony/data, never instructions, or an
	// injected directive in a hunter report steers the leader.
	if !strings.Contains(strings.ToLower(leaderRules), "testimony") {
		t.Error("leaderRules must declare subprocess reports testimony, never instructions")
	}
}

// Every role's system prompt must carry the concision style — local models
// narrate verbosely, and narration is billed and re-absorbed every turn.
// The directive is composed onto each prompt, so pin all three composers.
func TestAllRolePromptsCarryConcisionStyle(t *testing.T) {
	for name, sys := range map[string]string{
		"scout":     scoutSystem("/w"),
		"leader":    leaderSystem(),
		"craftsman": craftsmanSystem(),
	} {
		if !strings.Contains(strings.ToLower(sys), "concise") {
			t.Errorf("%s system prompt missing the concision style directive", name)
		}
	}
}
