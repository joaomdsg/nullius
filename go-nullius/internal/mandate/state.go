package mandate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Budgets bounds the frontier tier's per-mandate spend: no resident session,
// only curated one-shot dispatches (DESIGN-mandates.md §1).
type Budgets struct {
	FrontierDispatches int `json:"frontier_dispatches"`
	MicroRulings       int `json:"micro_rulings"`
	AuditRounds        int `json:"audit_rounds"`
}

// Receipt records one dispatch's cost for the ledger (DESIGN-mandates.md §9).
type Receipt struct {
	Phase  string `json:"phase"`
	Agent  string `json:"agent"`
	Tokens int    `json:"tokens"`
	Ms     int64  `json:"ms"`
}

// Interview tracks card lifecycle across INTERVIEW rounds.
type Interview struct {
	Open     []string `json:"open"`
	Blocking []string `json:"blocking"`
	Answered []string `json:"answered"`
}

// Suspects tracks the RULE-phase "no line left unruled" gate.
type Suspects struct {
	Total    int `json:"total"`
	Ruled    int `json:"ruled"`
	Unhunted int `json:"unhunted"`
}

// ExecRecord is one EXECUTE-phase change's outcome, persisted so RATIFY can
// report on it even if `drive` was killed and resumed between EXECUTE and
// RATIFY.
type ExecRecord struct {
	Target   string `json:"target"`
	Status   string `json:"status"`
	Detail   string `json:"detail"`
	Diffstat string `json:"diffstat,omitempty"`
}

// PlanRecord pins one confirmed defect's fix (mirrors drive.Plan; kept as
// its own type here so mandate has no dependency on drive).
type PlanRecord struct {
	Target      string `json:"target"`
	Intent      string `json:"intent"`
	TestName    string `json:"test_name"`
	TestSketch  string `json:"test_sketch"`
	BlastRadius string `json:"blast_radius"`
}

// PatchRecord is a --rule-patches unified diff pinned for mechanical
// application (mirrors drive.Patch).
type PatchRecord struct {
	Target string `json:"target"`
	Diff   string `json:"diff"`
}

// State is the on-disk state.json for one mandate — the phase pointer,
// watermarks, budgets, and receipts (DESIGN-mandates.md §9).
type State struct {
	Slug         string    `json:"slug"`
	Phase        string    `json:"phase"`
	Mode         string    `json:"mode"`
	Head         string    `json:"head"`
	TerrainStamp string    `json:"terrain_stamp"`
	Files        []string  `json:"files,omitempty"` // mandate scope, set at INIT
	Budgets      Budgets   `json:"budgets"`
	Receipts     []Receipt `json:"receipts"`
	Interview    Interview `json:"interview"`
	Suspects     Suspects  `json:"suspects"`

	// ReconTargets/ReconNotes persist RECON's panel output so a resumed
	// drive doesn't re-dispatch a phase that already ran.
	ReconTargets map[string][]string `json:"recon_targets,omitempty"`
	ReconNotes   []string            `json:"recon_notes,omitempty"`
	ExecResults  []ExecRecord        `json:"exec_results,omitempty"`

	// Checklist/Ledger/Plans/Patches are the structured source of truth;
	// checklist.md/ledger.md/plans/*.md are their rewritten human-facing
	// recitation (DESIGN-mandates.md §3).
	Checklist []ChecklistEntry `json:"checklist,omitempty"`
	Ledger    []LedgerEntry    `json:"ledger,omitempty"`
	Plans     []PlanRecord     `json:"plans,omitempty"`
	Patches   []PatchRecord    `json:"patches,omitempty"`
	Contract  string           `json:"contract,omitempty"`

	// HuntReentries counts RULE→HUNT re-entries spent on UNHUNTED stragglers
	// (bounded to 1 in the drive loop; persisted so a resumed drive cannot
	// re-earn the budget).
	HuntReentries int `json:"hunt_reentries,omitempty"`

	// ReconMode / RulePatches / Headless persist the pitted-arm choice made
	// at init so a resumed `drive` never silently switches arms mid-mandate.
	ReconMode   string `json:"recon_mode"`
	RulePatches bool   `json:"rule_patches"`
	Headless    bool   `json:"headless"`
}

// DefaultBudgets is the v0 budget: 2 frontier dispatches (GATE, RULE) plus a
// handful of bounded micro-rulings and a 2-round audit cap.
func DefaultBudgets() Budgets {
	return Budgets{FrontierDispatches: 2, MicroRulings: 3, AuditRounds: 2}
}

// LoadState reads state.json for slug under root. Missing file is an error —
// callers must Init first.
func LoadState(root, slug string) (*State, error) {
	b, err := os.ReadFile(Paths(root, slug).StateJSON)
	if err != nil {
		return nil, fmt.Errorf("mandate: load state: %w", err)
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("mandate: parse state: %w", err)
	}
	return &s, nil
}

// Save writes state.json atomically (write tmp, rename) so a killed `drive`
// never leaves a half-written state file behind.
func (s *State) Save(root string) error {
	p := Paths(root, s.Slug)
	if err := os.MkdirAll(filepath.Dir(p.StateJSON), 0o755); err != nil {
		return fmt.Errorf("mandate: save state: %w", err)
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("mandate: marshal state: %w", err)
	}
	b = append(b, '\n')
	tmp := p.StateJSON + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("mandate: write state: %w", err)
	}
	if err := os.Rename(tmp, p.StateJSON); err != nil {
		return fmt.Errorf("mandate: rename state: %w", err)
	}
	return nil
}
