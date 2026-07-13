# Open experiments — preregistered

Predictions written BEFORE the runs, so the runs can embarrass them.
Each entry: motivation · prediction · command. Blocked on: API credit
top-up (the pool exhausted mid-baseline on 2026-07-13).

## 1. Post-gcc plain-fable baseline (owed)

Benchmark 7's headline ratio rests on an n=2, high-variance baseline
($8.49–$17.09, 4–5/6), one rep of which predates the gcc/race repair.
Finish n=3 on the repaired image.

**Prediction:** mean lands $11–15; fix-rate 4–5/6 with the sse fault-path
defect missed in most reps; nullius's honest ratio settles at 40–55% of
baseline cost at superior quality (race hygiene and regressions staying
the separators).

```sh
CONTAINER=1 JUDGE=1 PLAIN_MODEL=claude-fable-5 PLAIN_EFFORT=low \
  LABEL=plain-fable-5-low-postgcc ./run.sh tasks/vialite-todo plain --reps 2
```

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
