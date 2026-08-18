package drive

import (
	"context"
	"fmt"
	"strings"

	"go-nullius/internal/dispatch"
	"go-nullius/internal/mandate"
)

// reconLensOut is one RECON panel member's reply: named targets for its
// lens, or — if it found none — a quoted basis for the absence (never a
// bare "none", DESIGN-mandates.md §4/§5).
type reconLensOut struct {
	Targets []string `json:"targets"`
	Absent  string   `json:"absent_basis"`
}

// ReconResult is the fixed panel's combined output: one target list per
// lens family, plus per-lens notes (panel-member failure, empty result, or
// quoted absence).
type ReconResult struct {
	Targets map[string][]string
	Notes   []string
}

// runRecon dispatches ONE scout per lens family in the fixed panel — never
// a single pass that picks a theme (DESIGN-mandates.md §4: "RECON is a
// FIXED PANEL by construction... Multi-theme coverage is guaranteed by the
// driver, not hoped for from the model"). A panel member's failure records
// its lens as UNKNOWN rather than silently empty (fail-closed default).
func (m *Machine) runRecon(ctx context.Context, s *mandate.State) ReconResult {
	res := ReconResult{Targets: map[string][]string{}}
	terrain := terrainBlock(m.cfg.Root, scopeFiles(m.cfg.Root, s.Files), reconTerrainBudget)
	for _, lens := range Lenses {
		out, notes, err := m.dispatchReconLens(ctx, s, lens, terrain)
		if err != nil {
			res.Notes = append(res.Notes, fmt.Sprintf("RECON %s: panel member failed (%v) — recorded UNKNOWN", lens, err))
			res.Targets[lens] = nil
			continue
		}
		res.Targets[lens] = out.Targets
		res.Notes = append(res.Notes, notes...)
	}
	// Entrypoint/state map: a fixed extra panel member, same discipline —
	// every mutating entrypoint and shared mutable state, named not prosed.
	out, notes, err := m.dispatchReconLens(ctx, s, "entrypoint-state-map", terrain)
	if err != nil {
		res.Notes = append(res.Notes, fmt.Sprintf("RECON entrypoint-state-map: panel member failed (%v) — recorded UNKNOWN", err))
	} else {
		res.Targets["entrypoint-state-map"] = out.Targets
		res.Notes = append(res.Notes, notes...)
	}
	return res
}

// reconTerrainBudget bounds the inlined terrain map per RECON prompt.
// 24KB proved far too small in the wild (vialite's full decl map is ~42KB;
// the tail of the alphabet vanished) — 96KB ≈ 24k tokens, comfortable for
// the local tiers and still under the caller's 200k prompt wall.
const reconTerrainBudget = 96 * 1024

func (m *Machine) dispatchReconLens(ctx context.Context, s *mandate.State, lens, terrain string) (reconLensOut, []string, error) {
	req := dispatch.Request{
		Tier:      dispatch.TierScout,
		Objective: "recon/" + lens,
		Prompt:    reconPrompt(lens, terrain),
		MaxTokens: 1500,
	}
	resp, err := m.cfg.Adapter.Dispatch(ctx, req)
	s.Receipts = append(s.Receipts, mandate.Receipt{Phase: PhaseRecon, Agent: "scout/" + lens, Tokens: resp.Tokens, Ms: resp.Ms})
	if err != nil {
		return reconLensOut{}, nil, err
	}
	var out reconLensOut
	if err := extractJSON(resp.Text, &out); err != nil {
		return reconLensOut{}, nil, err
	}
	// Mechanical floor under scout testimony: a target naming a file that
	// is not in the repo is dropped, never hunted (observed: toolless
	// scouts confabulate plausible paths).
	var notes []string
	kept := out.Targets[:0]
	var dropped []string
	for _, t := range out.Targets {
		if targetExists(m.cfg.Root, t) {
			kept = append(kept, t)
		} else {
			dropped = append(dropped, t)
		}
	}
	out.Targets = kept
	if len(dropped) > 0 {
		notes = append(notes, fmt.Sprintf("RECON %s: dropped %d target(s) naming files not in the repo: %s", lens, len(dropped), strings.Join(dropped, ", ")))
	}
	if len(out.Targets) == 0 {
		basis := out.Absent
		if strings.TrimSpace(basis) == "" {
			basis = "no quoted basis given"
		}
		notes = append(notes, fmt.Sprintf("RECON %s: no targets — %s", lens, basis))
	}
	return out, notes, nil
}

func reconPrompt(lens, terrain string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are the RECON panel member for the %q lens.\n", lens)
	b.WriteString(lensBrief(lens))
	b.WriteString("Map the terrain below for this lens only. Name targets as \"path:symbol\", never prose.\n")
	b.WriteString("Pick ONLY from the files and declarations shown — a target naming a file not in the map is mechanically dropped.\n")
	b.WriteString("If this lens has zero targets, give a quoted, checkable basis for the absence (e.g. which declarations you ruled out and why) — never a bare \"none\".\n")
	b.WriteString("\nTerrain (file + declared symbols, line-numbered):\n")
	b.WriteString(terrain)
	b.WriteString("\nReply with ONLY a JSON object: {\"targets\": [\"path:symbol\", ...], \"absent_basis\": \"...\"}\n")
	return b.String()
}
