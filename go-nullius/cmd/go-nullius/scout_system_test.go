package main

import (
	"strings"
	"testing"
)

// A scout child runs on the fast tier with thinking OFF, so it pattern-
// matches its prompt literally. If the prompt never names the workspace,
// the model falls back to the conventional /workspace sandbox path, every
// read/bash fails, and the leader compensates by absorbing the repo into
// its OWN context (measured: the local run self-read 20+ files and blew
// the smart window at turn 25). scoutSystem MUST ground the scout in its
// real dir and forbid the /workspace reflex explicitly.
func TestScoutSystemGroundsWorkspaceDir(t *testing.T) {
	dir := "/home/jgonc/Personal/repos/via-v2-zenb"
	s := scoutSystem(dir)

	if !strings.Contains(s, scoutRules) {
		t.Fatal("base scout rules dropped from grounded system prompt")
	}
	if !strings.Contains(s, dir) {
		t.Fatalf("workspace dir not grounded in scout prompt:\n%s", s)
	}
	// The explicit anti-reflex: name /workspace so the model is told NOT to
	// probe it, rather than left to invent it.
	if !strings.Contains(s, "/workspace") {
		t.Fatalf("missing explicit anti-/workspace instruction:\n%s", s)
	}
}
