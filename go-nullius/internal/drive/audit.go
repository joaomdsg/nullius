package drive

import (
	"context"
	"path/filepath"
	"strings"

	"go-nullius/internal/mandate"
)

// AUDIT bounds: frozen lens set over changed files, seen-set, ≤2 rounds
// (DESIGN-mandates.md §4/§9 budgets.audit_rounds; mirrors go-nullius's
// auditReentry).
func auditKey(e mandate.ChecklistEntry) string { return e.Lens + "\x00" + e.Target }

// runAudit re-sweeps the frozen lens set over files EXECUTE actually
// touched, judging only fresh (never-before-seen) suspects and disposing
// them with a bounded micro-ruling. A residual hit at an already-ruled
// target is recorded as RISK, never silently dropped or re-processed
// (DESIGN-mandates.md §4 AUDIT fail-closed default).
func (m *Machine) runAudit(ctx context.Context, s *mandate.State, prior []mandate.ChecklistEntry, changedFiles []string) []mandate.ChecklistEntry {
	entries := append([]mandate.ChecklistEntry(nil), prior...)
	if len(changedFiles) == 0 {
		return entries
	}
	seen := map[string]bool{}
	for _, e := range entries {
		seen[auditKey(e)] = true
	}

	for round := 1; round <= s.Budgets.AuditRounds; round++ {
		recon := ReconResult{Targets: map[string][]string{}}
		for _, lens := range Lenses {
			recon.Targets[lens] = changedFiles
		}
		swept := m.runHuntTagged(ctx, s, recon, PhaseAudit, "audit-hunt")

		var fresh []mandate.ChecklistEntry
		residual := 0
		for _, e := range swept {
			if seen[auditKey(e)] {
				residual++
				continue
			}
			seen[auditKey(e)] = true
			if isSuspect(e) {
				fresh = append(fresh, e)
			}
		}
		if len(fresh) == 0 {
			return entries
		}
		if s.Budgets.MicroRulings <= 0 {
			for i := range fresh {
				fresh[i].Disposition = mandate.DispRisk
				fresh[i].Note = "audit micro-ruling budget exhausted"
			}
			entries = append(entries, fresh...)
			return entries
		}
		s.Budgets.MicroRulings--
		ruled := m.runRule(ctx, s, fresh, "audit micro-ruling: dispose fresh suspects only", false)
		// A micro-ruling that leaves fresh suspects undisposed (weak ruler,
		// parse failure) must not let them ride into CLOSE/RATIFY — there is
		// no AllDisposed gate after AUDIT. Force RISK, fail closed.
		for i, e := range ruled.Entries {
			if e.Disposition == mandate.DispUndisposed {
				ruled.Entries[i].Disposition = mandate.DispRisk
				ruled.Entries[i].Note = "undisposed after audit micro-ruling — forced RISK (fail closed)"
			}
		}
		entries = append(entries, ruled.Entries...)
		if len(ruled.Plans) > 0 && m.cfg.Craftsman != nil {
			m.runExecute(ctx, s, ruled.Plans, nil)
		}
	}
	return entries
}

// changedFilesSince lists SOURCE files touched relative to HEAD (tracked
// diff + untracked new files) — the input to AUDIT's re-sweep. The machine's
// own .nullius/ ledger and non-source files are never terrain (measured:
// AUDIT swept spin artifacts' state.json/close.md into 120 checklist
// entries).
func changedFilesSince(ctx context.Context, dir string) ([]string, error) {
	out, err := runCmd(ctx, dir, []string{"git", "diff", "--name-only", "HEAD"})
	if err != nil {
		return nil, err
	}
	var files []string
	add := func(f string) {
		f = strings.TrimSpace(f)
		if f == "" || strings.HasPrefix(f, ".nullius/") || !srcExts[filepath.Ext(f)] {
			return
		}
		files = append(files, f)
	}
	for _, f := range strings.Split(out, "\n") {
		add(f)
	}
	untracked, err := gitUntracked(ctx, dir)
	if err == nil {
		for f := range untracked {
			add(f)
		}
	}
	return files, nil
}
