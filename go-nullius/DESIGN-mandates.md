# DESIGN — nullius CLI (mandate-driven reshape of go-nullius)

Status: DRAFT for review · 2026-07-24
Supersedes the fix-only scope of DESIGN-deterministic.md; the deterministic
state-machine core survives, the AST lens machinery becomes a pitted arm.

## 1. Thesis

Cost = turns × residency. Measured (bench record):
- Plain-fable $17.09 run: $10.16 cache reads vs $3.28 output — the bill is
  churn, not reasoning. 88–93% of orchestrated-run cost is the orchestrator's
  own resident context.
- nullius-low 6/6 at $6.17 vs plain-high 6/6 at $23.34 — quality holds when
  bulk lives in throwaway cheap contexts.
- Prevention beats compaction: $10.50/5-of-6 vs $14.56/4-of-6.
- Context rot: 15–20pt accuracy loss from position alone; cache reads refund
  dollars, never attention.

Architecture that follows: **no resident frontier session, ever**. The
frontier tier appears only in one-shot, curated dispatches — each receives a
small hand-built context, rules, and dies. The turn-by-turn loop runs in
three near-free places: the mechanical Go driver (free), haiku scouts (~4%
of spend), a sonnet craftsman executing pinned plans (~6%). Frontier total
per mandate: 2 fat dispatches (GATE, RULE) + bounded micro-rulings.

Files are the protocol; the CLI is the driver; the user interacts by
editing one file.

## 2. Reshape map (go-nullius → nullius CLI)

KEEP (proven, generalizes):
- Phase state machine w/ fail-closed defaults (machine.go): every phase
  returns a usable default on model failure; gates shrink, never grow.
- Mechanical mode backstop (any function in terrain ⇒ FULL).
- Enclosing-window extraction for model prompts (judge.go) — the 6-line
  radius bug (refuter cleared a defect by citing a sibling fn) is the
  cautionary tale; windows are function-scoped.
- Corroborate filters where mechanical: decisive-line validity, pair
  discrimination w/ bounded smart escalation, refuter evidence gate.
- Drain safety net: snapshot → write → non-empty-diff gate → build →
  touched-pkg -race → revert+retry(1).
- Bounded audit (frozen lens set over changed files, seen-set, ≤2 rounds).
- Caller retry discipline (grammar-500 unconstrained retry).
- Lens library promotion (confirmed-derived → seeded, re-gated).

DEMOTE TO PITTED ARM (`--recon=ast`):
- AST enumerate + witness-gated derived lenses. Vialite bench: 1/6 recall,
  recon single-theme fixation; recon-derived catches were luck across runs.
  Not deleted — pitted (see §8). Default arm is scout recon (§4 RECON).

NEW:
- Mandate file protocol (§3), interview cards (§5), adapters (§6),
  EXECUTE via craftsman plans (§7), RATIFY phase.
- Modes generalize: FIX / FEATURE / BUILD mandates, not fix-only.

DESCOPED (unchanged): `--driver` agentic mode. cmd/go-nullius v0 vestigial.

## 3. File protocol

```
.nullius/
  terrain.md                  # repo-level, commit-stamped, shared (exists today)
  lenses/                     # promoted lens library
  mandates/<slug>/
    mandate.md                # THE user surface — the only file a user edits
    state.json                # phase pointer, watermarks, budgets, receipts
    checklist.md              # hunter verdicts → ruled dispositions (recited by rewrite)
    ledger.md                 # gap ledger: ASSUMED / PROVISIONAL + escape layer
    plans/NN-<target>.md      # pinned plan per change: mechanism, test name, blast radius
    close.md                  # verbatim scout record: suite + vet + lint + surface diff
    report.md                 # STATUS/FACTS/RISKS/UNKNOWN/ASSUMED — doubles as ratification
```

`mandate.md` layout (machine-maintained banner + four human sections):

```markdown
> STATUS: HUNT · 2 questions open (0 blocking) · rerun `nullius drive`   ← driver-owned line 1

## INTENT        ← user writes
## CONTRACT      ← GATE drafts, user edits; verbatim into every build dispatch
## INTERVIEW     ← driver appends cards (§5); user edits answers inline
## RATIFICATION  ← close-time ledger flush; user objects by editing
```

Recitation is load-bearing, not bookkeeping: checklist.md and ledger.md are
REWRITTEN (not appended) at every phase transition — edge-placed restatement
is the measured drift counter.

## 4. Phase machine (`nullius drive`)

Every phase idempotent, stamped in state.json; `drive` always safe to rerun.
Interrupt, edit files, rerun — that is the whole DX contract.

| # | Phase     | Tier            | Consumes → Produces | Fail-closed default |
|---|-----------|-----------------|---------------------|---------------------|
| 0 | INIT      | mechanical      | `nullius init <slug>` → tree, HEAD stamp, build/test cmd detection, diffstat vs terrain stamp | n/a |
| 1 | RECON     | haiku panel     | mandate + terrain drift → targets per lens family + QUOTED absences | panel member fails → its absence recorded as UNKNOWN, never silently empty |
| 2 | GATE      | frontier ×1     | mandate + terrain digest → FULL/BUILD ruling, scope, interview cards | mechanical backstop: any pre-existing fn in scope ⇒ FULL |
| 3 | INTERVIEW | human, async    | cards in mandate.md → answers | unanswered layer-2 → PROVISIONAL(rec); headless → ASSUMED(rec) |
| 4 | HUNT      | haiku fan-out   | one hunter per lens × its exact targets → checklist.md verdicts + quotes | hunter fails → targets marked UNHUNTED, blocks RULE |
| 5 | RULE      | frontier ×1     | checklist + mechanically-extracted decisive windows + mandate → disposition per suspect + plan (or patch, §7) per fix | undisposed suspect ⇒ driver refuses to advance ("no line left unruled" is mechanical) |
| 6 | EXECUTE   | craftsman/driver| plans → diffs (tests-first ratchet); patches → applied mechanically + tests | drain safety net per change |
| 7 | AUDIT     | mech + haiku ≤2 | frozen lenses over changed files → fresh suspects → frontier MICRO-ruling (windows only) | residual at ruled target = RISK |
| 8 | CLOSE     | haiku scout ×1  | clean tree → suite+vet+lint verbatim + surface diff → close.md | no runnable suite → named in RISKS, never silent degrade |
| 9 | RATIFY    | mech (+ frontier micro if needed) | ledger flush → report.md; layer-2 STAND UNLESS OBJECTED (user edits file; drive detects, re-enters loop); layer-3 block | objection any time evaporates consent |

RECON is a FIXED PANEL by construction — one scout per lens family plus an
entrypoint/state map, dispatched in parallel. Never one pass that picks a
theme (the 1/6-recall failure). Multi-theme coverage is guaranteed by the
driver, not hoped for from the model. Terrain sharpens aim, never coverage:
a core lens hunts whenever its terrain exists.

RULE is where the money goes and the repo never goes: the driver extracts
function-scoped windows for every suspect; the frontier model sees
checklist + windows + mandate, nothing else. One dispatch, batched.

## 5. Interview cards

Generated by GATE (post-terrain — a mapped terrain asks "X and Y both
mutate this state; which owns it?", a cold mandate only guesses). The
driver REJECTS malformed cards mechanically (same spirit as the diet
governor). Grammar:

```markdown
### Q2 — Who owns retry state? ·· blocks: nothing (proceeding on B)
**Found:** `queue.go:141` clears `pending` before `flush()` confirms; `sweep.go:77` also writes it.
**Why you:** both orderings are implementable; the mandate text doesn't pick one.
- [ ] A. flush owns it — sweep must skip in-flight entries
- [x] B. sweep owns it — flush re-reads before send  ← recommended, PROVISIONAL
- [ ] C. something else: _______
```

Rules (all evidence-backed):
- Cap 4 per round — past ~4 users satisfice; a careless answer carries
  false authority, worse than ASSUMED.
- A card earns its slot with finding + why-code-can't-decide +
  recommendation, or the driver rules it ASSUMED instead.
- Recommendation PRE-CHECKED: the zero-effort path is read-nod-continue.
- `blocks:` header = escape analysis verdict. Layer-2 (revertible in
  worktree) proceeds PROVISIONAL; layer-3 (escapes: exported API, wire
  format, shared DB, spends, dep lock-in) blocks. Unclassifiable → layer-3.
- Answers are testimony: a factual claim in an answer gets a scout
  verification like any hunter quote. Scope-changing answers → delta-RECON
  (cheap: terrain is commit-stamped) + GATE re-rule.
- Each card teaches one concrete thing about the user's own codebase —
  the interview doubles as the terrain brief. Bite-sized is a format
  contract, not a hope.

Later gaps join ledger.md, never a dribble; flushed once at RATIFY.

## 6. Dispatch adapters

The driver shells one-shot sessions; adapters normalize:

- `claude` — `claude -p` (haiku scouts / sonnet craftsman / frontier rulings)
- `pi` — `pi -p --mode json --no-session`
- `api` — raw Anthropic API for schema-constrained rulings (reuses the
  existing caller: grammar-500 unconstrained retry, backoff)

Every dispatch carries: objective, output format, exact paths, boundaries.
Workers see none of the mandate history except what the driver curates in.
Receipts (tokens, duration, verdict) land in state.json for the cost ledger.

## 7. Writes policy

- **Craftsman (sonnet) executes pinned plans** — the default for every
  change. Plans pin mechanism + test name + sketch + blast radius (PlanOut
  shape): sonnet found the double-dispose 5/5 and botched the fix 5/5
  identically — an unpinned plan re-creates that hazard. Tests-first
  ratchet + drain safety net per change. Batched builds → one craftsman
  dispatch per self-contained plan, parallel where blast radii are disjoint.
- **Patch-in-ruling** (`--rule-patches`, PITTED, default off): for trivial
  fixes the RULE dispatch may emit a unified diff instead of a plan; the
  DRIVER applies it mechanically and runs the tests. Rationale to test: the
  frontier never opens an editing session — a 5-line patch is a few hundred
  output tokens inside a dispatch already paid for, vs a cold craftsman
  dispatch's fixed absorption overhead. Suspicion to test against: patch
  quality without the craftsman's local read of surrounding code.

## 8. Pitted arms (A/B, one variable each, vialite + one greenfield task)

| Arm | A | B | Metric |
|-----|---|---|--------|
| recon | scout panel (default) | `--recon=ast` (witness-gated AST lenses) | recall/6, $ |
| recon-both | scout panel | `--recon=both` (union, deduped) | marginal recall per $ |
| writes | craftsman-only | `--rule-patches` for trivial fixes | $ per fix, audit-caught regressions |
| interview | headless (all ASSUMED) | interactive cards | quality delta, wall-clock, answered/4 |

## 9. v0 scope

1. Go targets only (build/vet/-race machinery exists). FIX-mode mandates
   first; FEATURE next (skeleton → hunt new code before close); BUILD last.
2. CLI: `nullius init <slug>` · `nullius drive [<slug>]` (runs until block
   or done; `--headless`) · `nullius status`. Nothing else — answering is
   editing the file and rerunning drive.
3. state.json (sketch):

```json
{
  "slug": "retry-ownership",
  "phase": "HUNT",
  "mode": "FULL",
  "head": "2b8acd8",
  "terrain_stamp": "2b8acd8",
  "budgets": {"frontier_dispatches": 2, "micro_rulings": 3, "audit_rounds": 2},
  "receipts": [{"phase":"RECON","agent":"scout/serialization","tokens":38935,"ms":45047}],
  "interview": {"open": ["Q2"], "blocking": [], "answered": ["Q1"]},
  "suspects": {"total": 14, "ruled": 9, "unhunted": 0}
}
```

4. Non-Go lens panels are the open R&D item (ROADMAP #3) — the scout-recon
   default is the portability bet: panels are prompt-defined, not
   AST-defined, so they travel to TS/SQL; the AST arm stays Go-only.

## Open questions (ledgered, not blocking)

- GATE and RULE frontier tier: fable vs opus per dispatch — cost ladder
  suggests per-dispatch effort selection (ROADMAP #15/#16 economics apply
  even without a resident session: effort set at dispatch, never switched).
- Parallel EXECUTE: disjoint-blast-radius detection — file-level overlap
  first, package-level later.
- `nullius watch` (mtime daemon rerunning drive) — DX sugar, post-v0.
