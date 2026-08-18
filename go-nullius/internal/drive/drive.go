package drive

import (
	"context"
	"fmt"
	"os"
	"strings"

	"go-nullius/internal/mandate"
)

// Drive runs the phase machine from s.Phase until it blocks (INTERVIEW
// awaiting an answer, RULE refusing to advance past an undisposed suspect)
// or reaches DONE. Every phase is idempotent and its result persisted to
// state.json before Drive looks at the next one, so a killed `drive` is
// always safe to rerun — it resumes at the saved phase, never redoing
// completed work (DESIGN-mandates.md §4).
func (m *Machine) Drive(ctx context.Context) (*mandate.State, error) {
	root, slug := m.cfg.Root, m.cfg.Slug
	s, err := mandate.LoadState(root, slug)
	if err != nil {
		return nil, err
	}
	doc, err := mandate.ReadDoc(root, slug)
	if err != nil {
		return nil, err
	}
	p := mandate.Paths(root, slug)

	persist := func() error {
		if err := s.Save(root); err != nil {
			return err
		}
		return mandate.WriteDoc(root, slug, doc)
	}

	// stepDone persists the just-completed phase's result and advances the
	// pointer, then honors a test-injected "simulated kill" hook.
	stepDone := func(justCompleted string) (stop bool, err error) {
		s.Phase = nextPhase(justCompleted)
		if err := persist(); err != nil {
			return true, err
		}
		if m.cfg.OnPhaseDone != nil && m.cfg.OnPhaseDone(justCompleted) {
			return true, nil
		}
		return false, nil
	}

	for {
		switch s.Phase {
		case PhaseInit:
			if stop, err := stepDone(PhaseInit); stop || err != nil {
				return s, err
			}

		case PhaseRecon:
			recon := m.runRecon(ctx, s)
			s.ReconTargets = recon.Targets
			s.ReconNotes = recon.Notes
			if stop, err := stepDone(PhaseRecon); stop || err != nil {
				return s, err
			}

		case PhaseGate:
			recon := ReconResult{Targets: s.ReconTargets, Notes: s.ReconNotes}
			gate, gerr := m.runGate(ctx, s, recon, doc)
			if gerr != nil {
				// Halt with the phase pointer still at GATE — a rerun
				// retries the dispatch instead of marching on without a
				// contract or interview.
				doc.Status = fmt.Sprintf("GATE · dispatch failed — fix transport and rerun `nullius drive` · %v", gerr)
				if perr := persist(); perr != nil {
					return s, perr
				}
				return s, gerr
			}
			s.Mode = gate.Mode
			s.Contract = gate.Contract
			doc.Contract = gate.Contract
			if len(gate.Cards) > 0 {
				appended, err := mandate.AppendCards(doc.Interview, gate.Cards)
				if err != nil {
					return s, fmt.Errorf("drive: GATE: %w", err)
				}
				doc.Interview = appended
			}
			s.ReconNotes = append(s.ReconNotes, gate.Notes...)
			if stop, err := stepDone(PhaseGate); stop || err != nil {
				return s, err
			}

		case PhaseInterview:
			out, err := m.runInterview(s, &doc, nil) // cards already appended at GATE
			if err != nil {
				return s, err
			}
			s.Ledger = append(s.Ledger, out.LedgerEntries...)
			if err := mandate.WriteLedger(p.Ledger, s.Ledger); err != nil {
				return s, err
			}
			s.Interview = mandate.Interview{Open: out.Open, Blocking: out.BlockingIDs, Answered: out.Answered}
			doc.Status = statusBanner(PhaseInterview, len(out.Open), len(out.BlockingIDs))
			if out.Blocked {
				if err := persist(); err != nil {
					return s, err
				}
				return s, nil
			}
			if stop, err := stepDone(PhaseInterview); stop || err != nil {
				return s, err
			}

		case PhaseHunt:
			recon := ReconResult{Targets: s.ReconTargets}
			entries := m.runHunt(ctx, s, recon)
			s.Checklist = entries
			if err := mandate.WriteChecklist(p.Checklist, entries); err != nil {
				return s, err
			}
			if stop, err := stepDone(PhaseHunt); stop || err != nil {
				return s, err
			}

		case PhaseRule:
			ruled := m.runRule(ctx, s, s.Checklist, s.Contract, s.RulePatches)
			s.Checklist = ruled.Entries
			s.Plans = toPlanRecords(ruled.Plans)
			s.Patches = toPatchRecords(ruled.Patches)
			if err := mandate.WriteChecklist(p.Checklist, s.Checklist); err != nil {
				return s, err
			}
			if err := writePlanFiles(p, ruled.Plans); err != nil {
				return s, err
			}
			if ok, undisposed := mandate.AllDisposed(s.Checklist); !ok {
				// UNHUNTED stragglers can never be disposed by RULE (they are
				// not suspects) — ONE bounded re-hunt of exactly those
				// targets, then RULE re-runs. Persisted bound: a resumed
				// drive never re-earns it.
				if s.HuntReentries < 1 && hasUnhunted(undisposed) {
					s.HuntReentries++
					s.Checklist = m.rehuntUnhunted(ctx, s, s.Checklist)
					if err := mandate.WriteChecklist(p.Checklist, s.Checklist); err != nil {
						return s, err
					}
					doc.Status = "RULE · re-hunting uncovered targets (bounded, 1x)"
					if err := persist(); err != nil {
						return s, err
					}
					continue
				}
				doc.Status = fmt.Sprintf("RULE · %d suspect(s) undisposed — cannot advance · rerun `nullius drive`", len(undisposed))
				if perr := persist(); perr != nil {
					return s, perr
				}
				return s, fmt.Errorf("drive: %d suspect(s) left undisposed at RULE — refusing to advance", len(undisposed))
			}
			if stop, err := stepDone(PhaseRule); stop || err != nil {
				return s, err
			}

		case PhaseExecute:
			results := m.runExecute(ctx, s, fromPlanRecords(s.Plans), fromPatchRecords(s.Patches))
			s.ExecResults = toExecRecords(results)
			if stop, err := stepDone(PhaseExecute); stop || err != nil {
				return s, err
			}

		case PhaseAudit:
			changed, _ := changedFilesSince(ctx, root)
			s.Checklist = m.runAudit(ctx, s, s.Checklist, changed)
			if err := mandate.WriteChecklist(p.Checklist, s.Checklist); err != nil {
				return s, err
			}
			if stop, err := stepDone(PhaseAudit); stop || err != nil {
				return s, err
			}

		case PhaseClose:
			record, ok := m.runClose(ctx, root, m.cfg.buildCmd(), m.cfg.testCmd())
			if err := os.WriteFile(p.CloseMD, []byte(record), 0o644); err != nil {
				return s, err
			}
			if !ok {
				// A failed suite must never ratify into an unqualified DONE
				// report. Phase stays CLOSE — fix, then rerun `nullius drive`
				// to re-verify from clean.
				doc.Status = "CLOSE · verification FAILED — see close.md · fix and rerun `nullius drive`"
				if perr := persist(); perr != nil {
					return s, perr
				}
				return s, fmt.Errorf("drive: CLOSE verification failed — refusing to ratify (see close.md)")
			}
			if stop, err := stepDone(PhaseClose); stop || err != nil {
				return s, err
			}

		case PhaseRatify:
			report := renderReport(slug, RatifyInput{
				Mode:        s.Mode,
				Entries:     s.Checklist,
				ExecResults: fromExecRecords(s.ExecResults),
				Notes:       s.ReconNotes,
				Ledger:      s.Ledger,
			})
			if err := os.WriteFile(p.ReportMD, []byte(report), 0o644); err != nil {
				return s, err
			}
			if !strings.Contains(doc.Ratification, ratificationBanner) {
				doc.Ratification = strings.TrimSpace(doc.Ratification + "\n\n" + ratificationBanner)
			}
			if hasObjection(doc.Ratification) {
				s.Phase = PhaseRule
				doc.Status = statusBanner(PhaseRule, 0, 0)
				if err := persist(); err != nil {
					return s, err
				}
				continue
			}
			doc.Status = "RATIFY · done · see report.md"
			if stop, err := stepDone(PhaseRatify); stop || err != nil {
				return s, err
			}

		case PhaseDone:
			if err := persist(); err != nil {
				return s, err
			}
			return s, nil

		default:
			return s, fmt.Errorf("drive: unknown phase %q", s.Phase)
		}
	}
}

// hasUnhunted reports whether any undisposed entry is an UNHUNTED straggler
// — the one blocker RULE can never clear itself.
func hasUnhunted(entries []mandate.ChecklistEntry) bool {
	for _, e := range entries {
		if e.Verdict == mandate.Unhunted {
			return true
		}
	}
	return false
}

func writePlanFiles(p mandate.FilePaths, plans []Plan) error {
	_ = os.RemoveAll(p.PlansDir)
	if err := os.MkdirAll(p.PlansDir, 0o755); err != nil {
		return err
	}
	for i, pl := range plans {
		content := fmt.Sprintf("# plan — %s\n\nIntent: %s\nTest: %s\nSketch: %s\nBlast radius: %s\n",
			pl.Target, pl.Intent, pl.TestName, pl.TestSketch, pl.BlastRadius)
		if err := os.WriteFile(p.PlanPath(i+1, pl.Target), []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func toPlanRecords(plans []Plan) []mandate.PlanRecord {
	out := make([]mandate.PlanRecord, len(plans))
	for i, p := range plans {
		out[i] = mandate.PlanRecord{Target: p.Target, Intent: p.Intent, TestName: p.TestName, TestSketch: p.TestSketch, BlastRadius: p.BlastRadius}
	}
	return out
}

func fromPlanRecords(prs []mandate.PlanRecord) []Plan {
	out := make([]Plan, len(prs))
	for i, p := range prs {
		out[i] = Plan{Target: p.Target, Intent: p.Intent, TestName: p.TestName, TestSketch: p.TestSketch, BlastRadius: p.BlastRadius}
	}
	return out
}

func toPatchRecords(patches []Patch) []mandate.PatchRecord {
	out := make([]mandate.PatchRecord, len(patches))
	for i, p := range patches {
		out[i] = mandate.PatchRecord{Target: p.Target, Diff: p.Diff}
	}
	return out
}

func fromPatchRecords(prs []mandate.PatchRecord) []Patch {
	out := make([]Patch, len(prs))
	for i, p := range prs {
		out[i] = Patch{Target: p.Target, Diff: p.Diff}
	}
	return out
}

func toExecRecords(results []ExecResult) []mandate.ExecRecord {
	out := make([]mandate.ExecRecord, len(results))
	for i, r := range results {
		out[i] = mandate.ExecRecord{Target: r.Target, Status: r.Status, Detail: r.Detail, Diffstat: r.Diffstat}
	}
	return out
}

func fromExecRecords(recs []mandate.ExecRecord) []ExecResult {
	out := make([]ExecResult, len(recs))
	for i, r := range recs {
		out[i] = ExecResult{Target: r.Target, Status: r.Status, Detail: r.Detail, Diffstat: r.Diffstat}
	}
	return out
}
