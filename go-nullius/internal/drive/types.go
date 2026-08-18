// Package drive implements the mandate-driven phase machine
// (DESIGN-mandates.md §4): INIT→RECON→GATE→INTERVIEW→HUNT→RULE→EXECUTE→
// AUDIT→CLOSE→RATIFY. No resident frontier session — the frontier tier
// appears only as one-shot curated dispatches (GATE, RULE, bounded
// micro-rulings); every phase is idempotent and stamped in state.json so
// `drive` is always safe to interrupt and rerun.
package drive

import (
	"go-nullius/internal/dispatch"
	"go-nullius/internal/mandate"
)

// Lenses is the fixed RECON/HUNT panel (cc-nullius/skills/nullius/SKILL.md
// "Lenses:"; nullius-lens-hunter.md frontmatter) — one scout per family,
// dispatched in parallel, never a single pass that picks a theme (the
// 1/6-recall failure DESIGN-mandates.md §2 cites).
var Lenses = []string{
	"serialization",
	"fault-survival",
	"scope-confinement",
	"wake-predicates",
	"lost-updates",
	"lifecycle-races",
	"swallowed-errors",
	"resource-release",
}

// lensSemantics pins each lens's MEANING into every prompt that names it —
// small local models free-associate on the bare name (observed live:
// "serialization" hunted as data encoding/marshaling in a locking mandate).
var lensSemantics = map[string]string{
	"serialization":        "concurrency serialization — a lock/mutex/single-writer mechanism in the mutating entrypoint's OWN body. This is NOT data encoding/marshaling/JSON.",
	"fault-survival":       "state that must survive a failure — anything cleared or overwritten BEFORE its confirming write, send, or flush lands: queues, buffers, retry/pending state.",
	"scope-confinement":    "fan-out/broadcast confinement — every fan-out or broadcast site must carry an explicit scope argument; an unscoped broadcast leaks one scope's change to all.",
	"wake-predicates":      "wake/notify conditions — a predicate that can evaluate false when it must hold, or that reads state outside the writer's lock.",
	"lost-updates":         "read-modify-write on shared state where a concurrent writer's change can be silently overwritten (no lock/CAS/version across the whole cycle).",
	"lifecycle-races":      "background sweeps/TTL/dispose racing live use; shutdown or unregister racing in-flight work.",
	"swallowed-errors":     "error values dropped, ignored, or logged-only on mutating paths — failure paths that report success.",
	"resource-release":     "acquired resources (connections, files, goroutines, tickers, subscriptions, registrations) with a missing or conditional release path.",
	"entrypoint-state-map": "every mutating entrypoint and every piece of shared mutable state in scope, named as path:symbol.",
}

// lensBrief renders the semantics line injected into recon/hunt prompts;
// unknown lenses get nothing rather than a fabricated meaning.
func lensBrief(lens string) string {
	if s, ok := lensSemantics[lens]; ok {
		return "Lens meaning: " + s + "\n"
	}
	return ""
}

// Phase names, in machine order.
const (
	PhaseInit      = "INIT"
	PhaseRecon     = "RECON"
	PhaseGate      = "GATE"
	PhaseInterview = "INTERVIEW"
	PhaseHunt      = "HUNT"
	PhaseRule      = "RULE"
	PhaseExecute   = "EXECUTE"
	PhaseAudit     = "AUDIT"
	PhaseClose     = "CLOSE"
	PhaseRatify    = "RATIFY"
	PhaseDone      = "DONE"
)

var phaseOrder = []string{
	PhaseInit, PhaseRecon, PhaseGate, PhaseInterview, PhaseHunt,
	PhaseRule, PhaseExecute, PhaseAudit, PhaseClose, PhaseRatify, PhaseDone,
}

func nextPhase(p string) string {
	for i, name := range phaseOrder {
		if name == p && i+1 < len(phaseOrder) {
			return phaseOrder[i+1]
		}
	}
	return PhaseDone
}

// Config wires one mandate's drive run.
type Config struct {
	Root      string
	Slug      string
	Adapter   dispatch.Adapter
	Craftsman Writer // nil disables EXECUTE (plans reported, never applied)
	BuildCmd  []string
	TestCmd   []string
	// OnPhaseDone, if set, is called after each phase completes and its
	// state is persisted; returning true stops Drive immediately —
	// resumability tests use this to simulate a kill mid-run without an
	// actual process kill.
	OnPhaseDone func(phase string) bool
}

func (c Config) buildCmd() []string {
	if len(c.BuildCmd) > 0 {
		return c.BuildCmd
	}
	return []string{"go", "build", "./..."}
}

func (c Config) testCmd() []string {
	if len(c.TestCmd) > 0 {
		return c.TestCmd
	}
	return []string{"go", "test", "./...", "-race"}
}

// Machine drives one mandate through the phase table.
type Machine struct {
	cfg Config
}

// New builds a Machine for an already-scaffolded mandate (see
// mandate.Scaffold for INIT).
func New(cfg Config) *Machine {
	return &Machine{cfg: cfg}
}

// runCtx threads the loaded mandate.md doc + state.json through one phase
// call; phases mutate doc/state in place and the driver persists both after
// each phase.
type runCtx struct {
	root string
	slug string
	doc  mandate.Doc
}
