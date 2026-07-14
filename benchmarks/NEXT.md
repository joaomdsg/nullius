# Open experiments — preregistered

Predictions written BEFORE the runs, so the runs can embarrass them.
Each entry: motivation · prediction · command.

## 1. Post-gcc plain-fable baseline — RESOLVED 2026-07-14 ✓

Benchmark 7's headline ratio rested on an n=2, high-variance baseline
($8.49–$17.09, 4–5/6), one rep of which predated the gcc/race repair.
Finished n=3 on the repaired image (arm `plain-fable-5-low[-postgcc]`).

**Result (adopted as the baseline):** mean **$12.36** ($8.49 / $13.17 /
$15.42), fix-rate **4.33/6** (4 / 4 / 5), 1/3 race-clean, blind 4.33/6,
wall ~14 min. nullius $6.23/5.75 = **50.4% of baseline cost at strictly
better quality** — fix-rate (5.75 vs 4.33) and race hygiene (4/4 vs 1/3)
the separators.

Prediction confirmed on all three headline axes (predicted $11–15 →
$12.36; 4–5/6 → 4.33; 40–55% → 50%). Sub-prediction wrong: no single
recurring miss — statesess & action missed in 2/3 reps each, sse in 1/3;
cas/subscription/ttl fixed every rep. Caveats: a 4th rep was scrubbed
(exit 1, smoke failed, judge hit the monthly spend limit); the original
07-14 rep1 ($13.44/5) billed $0.99 of an anomalous opus subagent dispatch
(plain arm should be fable-solo) and was replaced by a clean fable-solo
re-run ($13.17/4, 0 subagents) — the swap lowered mean fixed 4.67→4.33.

## 2. Benchmark 8 — greenfield with a hidden property oracle

Everything measured so far is defect archaeology. Greenfield flips the
lenses from hunting to design (same taxonomy, opposite tense) and its
signature failure is vacuous success — green suites that force nothing —
which rule 7 (name the change that would fail each test) exists to kill.
Needs a new task: build a feature from scratch against a hidden
adversarial property suite the builder never sees.

**Prediction:** nullius ≈ plain-fable quality at ~1.5× cheaper (the
read-avoidance lever is moot when there is nothing to not-hold; the
remaining edge is scout-absorbed verification). Its advantage
concentrates in exactly two columns: race-cleanliness and fault-path
coverage of the brand-new code. Plain mid-tier wins on cost wherever the
oracle is soft — and that is a finding, not a failure.

Task to build: `tasks/<greenfield>/` with SEED_DIR skeleton + HIDDEN_DIR
property suite, per harness README.

## 3. Benchmark 9 — sonnet as the nullius leader

Splits benchmark 7's two lessons and makes them fight. "Judgment doesn't
delegate downtier" was measured against the v6 ceremony — but the lenses
were written AFTER, and they mechanize detection ("an entrypoint whose
body takes no lock IS the finding" is almost grep). The unmeasured
question: did the lenses de-skill detection only, or repair too? The
measured crux says detection-only: both sonnet runs FOUND the
double-dispose and botched the same fix the same way (`sync.Once` where
Shutdown's signal-then-dispose sequencing demands a guarded bool —
interpreting the constraint took the tier).

**Prediction (n=3):** caught 5–6/6 (lenses mechanize detection) · fixed
~4/6 · ≥1 regression across the three reps (the subtle-fix signature) ·
$3.5–5.5/rep. Either branch wins: confirmed → a real budget tier for
detection-heavy work, fable kept for constraint-laden fixes; refuted →
"judgment doesn't delegate downtier" gets a headstone and nullius gets
absurdly cheap.

```sh
CONTAINER=1 JUDGE=1 LEAN_MODEL=claude-sonnet-5 LEAN_EFFORT=high \
  ./run.sh tasks/vialite-todo nullius --reps 3
```

## 4. Lens generalization — second seeded-defect oracle (owed)

Benchmark 7's own top threat: the four lenses were derived from and
validated on vialite-todo's six defects. A second brownfield task with
DISJOINT defect classes (e.g. resource leaks, ordering/causality,
idempotency, auth boundaries) tests whether the lens METHOD generalizes
or only its instances do.

**Prediction:** the four existing lenses catch ≤half of a disjoint
defect set; the method (each miss → a new quoted-mechanism lens)
converges within 2–3 iterations as it did on vialite (3/6 → 6/6 in four
reps). If convergence needs per-task lens tuning every time, the honest
claim shrinks to "a lens-derivation method", not "a lens set".
