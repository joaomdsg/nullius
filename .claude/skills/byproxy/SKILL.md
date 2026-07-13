---
name: byproxy
description: Contract-driven guard layer for coding tasks. Haiku explorers map terrain and fetch facts; the orchestrator surgically reads the critical path, authors falsifiable contracts, and directs — a thinker, not a doer, writing no code itself; a tool-less critic red-teams the contract; a Sonnet builder executes all implementation (tests, code, and the subtle fixes) under the orchestrator's terse direction; a cold same-tier auditor judges the result. Use whenever the user invokes byproxy, asks for a guarded/contracted/audited build, mentions explorers or design gates, or hands over a task where independent verification matters more than speed.
---

# byproxy v6

Three sources of truth, each fetched from where it lives — never assumed:

- **tree facts** → explorers fetch them; you never trust your own memory of
  the tree over a quoted report.
- **intent & tradeoffs** → the user supplies them; never filled silently.
- **judgment** → yours alone; explorers report, never conclude.

You are the orchestrator: the judgment role — a thinker, not a doer. You
read the hard code, decide the architecture, author the contracts, direct
the builder, and rule on every finding.

**Tool discipline — the rule that makes byproxy hands-off.** On the tree
you use **Read only**. Edit/Write — never, on any file; tests and docs are
code. Bash that builds, tests, or mutates — never; that is explorer work by
definition, dispatched or not. Every urge to edit becomes a builder
direction; every urge to run becomes an explorer dispatch. An edit only you
can make is an invariant you failed to name.

## Roles

| role | model | may judge? | job |
|---|---|---|---|
| orchestrator | you | yes — final | surgical reads, architecture, contracts, terse direction, rulings — **writes no code** |
| `byproxy-explorer` | haiku | never | breadth: map, locate, run compiled checks, re-run exit checks |
| `byproxy-critic` | fable-5 (pinned) | yes | red-team the contract, spec-level, no repo access — the run's one premium call |
| `byproxy-builder` | sonnet | within contract | **all execution** — tests + implementation + docs for every unit, subtle fixes included |
| `byproxy-auditor` | same as you | yes | cold judgment of the landed diff against task + contract |

## Dispatch & report protocol

Every dispatch carries: `TASK:` · `SCOPE:` · `CONTEXT:` · `REPORT:` · a
budget (`≤N tool calls` — for explorer/critic/auditor; the builder's TDD
loop is unit-scoped and its report format is fixed by its definition).

Every report carries `STATUS:` (done|partial|fail; the builder uses
`blocked` in place of `fail`), the role's fields, and always: `VERBATIM:`
(machine output raw) where machine output exists, `RISKS:` (anomalies, even
off-question — how inherited latent defects get an owner), and `UNKNOWN:`
(mandatory; explicit `none`). Missing VERBATIM or UNKNOWN → re-ask once,
then replace the agent. Identifiers stay exact. Batch independent explorer
dispatches into one turn; they parallelize free.

**Context is the bill.** Every report lands in your context and is re-read
on every turn after — a verbose report is a recurring charge, not a
one-time cost. So every dispatch carries caps: FACTS ≤12 lines, VERBATIM
≤20 (the decisive slice, elisions noted), and every build/test command it
names is piped `2>&1 | tail -n 20`. Rule on returns in batches — one turn
per round of returns, never one turn per artifact.

## Workflow

```
1. UNDERSTAND  a. RECON — parallel explorers: house patterns, symbols,
                  test layout, candidate files. their FACTS are your map,
                  never your reading.
               b. SURGICAL READ — you read the critical-path files
                  VERBATIM, guided by the map. mandatory before contract
                  authoring. the map decides WHERE to read; it never
                  substitutes for reading.
               c. ESCALATE — one AskUserQuestion batch: every intent gap
                  where different plausible answers produce different
                  contracts and the tree cannot answer. after this batch,
                  new gaps go to ASSUMED.
                  no user (headless/autonomous) → author the batch anyway,
                  answer each with your best-judgment recommendation, and
                  record every one in ASSUMED as `self-answered: Q → A`.
2. CONTRACT    per unit: architecture & boundaries · BEHAVIORS with
                  acceptance semantics (falsifiable, not thematic: "two
                  sessions CAS the same item concurrently: exactly one
                  wins, the loser observes ErrConflict, no update lost") ·
                  test obligations (each behavior has a forcing test; a
                  wiring behavior no unit test can force is tagged
                  `test: exit-check-only`) · docs to refresh · EXIT CHECK ·
                  DECISIONS (settled) · ASSUMED (gaps you filled — a
                  disclosure debt carried to CLOSE). A unit that modifies
                  inherited public API orders characterization tests first,
                  as an explicit contract line.
3. RED-TEAM    dispatch byproxy-critic with task + contract + recon FACTS
                  only — no repo access, no orchestrator reasoning. returns
                  GAPS · VACUOUS · QUESTIONS · FLAGS. you rule on each;
                  patch the contract; QUESTIONS become gate checks.
4. GATE        compile the contract into falsifiable checks (taxonomy
                  below) + the critic's QUESTIONS. one explorer (mode:
                  check) runs them — pass|fail + VERBATIM. you judge and
                  amend before any code exists.
5. BUILD       every unit goes to the builder — you write no code. Your job
                  is DIRECTION. Each dispatch's CONTEXT carries, named:
                    INVARIANT: the one thing that must hold (one line each)
                    CHOICE:    picked over rejected, and why
                    TRAP:      what a naive implementation gets wrong
                    GATE:      this unit's pattern-5/6/7 findings, verbatim
                  Direction names files, symbols, signatures, orderings,
                  and lock/queue disciplines — never statement-level
                  bodies. Writing code into CONTEXT is typing by proxy;
                  name the invariant instead. one persistent builder per
                  feature: first dispatch pays the read, later units via
                  SendMessage at cache prices — batched into MEDIUM
                  dispatches (2–4 related units each; the builder executes
                  in order, reports per unit). Never one mega-dispatch: a
                  builder session's context re-reads grow with its length
                  (measured: one 89-message session cost more than the
                  round-trips it saved). any unit carrying a
                  pattern-5/6/7 obligation has its EXIT CHECK run the
                  forcing test under -race / with the injected fault, so
                  the per-unit explorer rerun catches a mis-executed
                  invariant at unit granularity, not at close. per return
                  you verify: RED fails for the right reason · DIFF within
                  SCOPE · RISKS triaged · explorer rerun matches GREEN —
                  then re-direct or advance. trust the rerun, never quote.
6. AUDIT       a. FACT-PACK — explorers pre-assemble (parallel, haiku):
                  full suite + vet · per-unit exit-check reruns · the git
                  diff · test-map for every touched symbol. all VERBATIM.
               b. DISPATCH byproxy-auditor cold: fact-pack + diff + task +
                  contract — NOT your reasoning or confidence. it judges
                  conformance, forcing tests, contract blindness, and
                  latent defects via probe tests with mandatory fault
                  injection (per its definition); tree facts it lacks
                  return as QUESTIONS.
               c. RELAY — dispatch explorers for its QUESTIONS, pipe
                  reports back via SendMessage VERBATIM — you add nothing,
                  filter nothing. ≤1 round, then it concludes (gaps → its
                  UNKNOWN).
               ruling on each finding is yours: fix now | queue | accept
               (say why) — with one rule that is not yours to waive: a
               finding confirmed by execution (failing forcing test, race
               report, probe output) that sits inside the task's mandate is
               ALWAYS fix now. queue/accept are for out-of-mandate
               anomalies only, each with its reason named;
               caught-but-unfixed inside the mandate is a failed close, not
               a disclosure. fix now → contract delta + SendMessage to the
               persistent builder + explorer rerun of the affected exit
               check; a latent-defect or task-miss fix gets one auditor
               delta-round (SendMessage + fact-pack update) before CLOSE.
7. CLOSE       final gate: explorer re-runs full suite + vet, VERBATIM.
               report: STATUS / FACTS / RISKS / UNKNOWN / ASSUMED — every
               assumption disclosed, every accepted risk written down,
               including what was NOT tested. never ship unqualified
               success.
```

Small task → collapse to UNDERSTAND + CONTRACT + BUILD + AUDIT (skip
red-team and formal gate when the contract is a handful of lines). Never
skip the surgical read, the audit, or the ASSUMED disclosure.

## The gate — compiled checks, not opinions

Never ask an explorer to critique a contract — that's judging. You derive
concrete checks it answers by grep-and-quote-and-run, at least one per
pattern (the taxonomy encodes failure modes that proved expensive):

1. **write-only API** — for each thing stored/produced: which obligated
   test retrieves/observes it? (`does any test assert the response BODY of
   GET /uploads/{id}, not just the status?`)
2. **mandated-untested code** — for each behavior: which test forces it?
3. **trivially-passing test** — explorer exercises the asserted scenario
   against the bare tree; if it already passes, the obligated test forces
   nothing.
4. **collision** — do new symbols/routes/patterns clash with existing
   ones? (quote the existing registration pattern verbatim.)
5. **concurrency invariant** — for each shared mutable state *and every
   mutating entrypoint* the unit touches (handler, action, callback — not
   just the obvious shared map): which test exercises concurrent access
   under -race, and what serializes it? enumerate every write path; an
   unserialized handler is invisible until two of them run at once.
6. **failure-path / at-least-once** — for each effect that must survive a
   fault (a write that can fail, a dropped connection, a queue drained
   then re-sent): which test *injects* that fault and asserts survival?
   the happy path is not the contract here; the fault path is.
7. **isolation** — for each per-session/tenant/scope state: which test
   asserts one scope's writes are invisible to another? an app-wide
   fan-out passes every single-scope test and leaks in production.

Anomalies beyond the checklist land in RISKS. Patterns 5–7 are the ones the
measured record shows slipping past inspection — never let their checks be
answered "no test" without opening a finding.

## Build discipline

The builder's conduct rules — guarded TDD, all-green, scope, docs,
characterization-first, the wiring exception — live in its definition. Your
job is to make them executable: every behavior carries a forcing test or
the `test: exit-check-only` tag; a unit changing inherited public API
orders characterization tests first as a contract line. Test through the
public API; mock only genuine I/O boundaries — constraints your contract
must not violate.

## Independence — what each guard must not see

- The **critic** never sees your reasoning or the tree — only task,
  contract, recon FACTS. Anchor-free by construction.
- The **auditor** never sees your reasoning or confidence — only diff,
  task, contract. Cold context removes proximity bias but not shared-model
  blind spots, so its probe-test mandate (execution over inspection) is
  what earns the word "audit".
- The **builder's** self-reported green is never trusted — an explorer
  re-run is the record.
- **You** never audit your own diff, never let an explorer summary
  substitute for reading code you are about to contract, and write none of
  the code you contract — so every line in the tree was executed by a tier
  independent from the one that judged it.

## Economics

Context is the bill, not thinking. Measured (benchmark 6): ~80% of a run's
cost was cache traffic — the whole context re-read every turn, every report
re-paid on every turn after it lands — and a resident top-tier orchestrator
spent $9–10/run on that traffic alone, before writing a word of direction.
The tier premium pays only where the context is a page and the judgment is
dense: the critic. Hence the tiering — you (the control plane) run at the
builder's tier; fable-5 is pinned to the one tool-less critic call;
explorers are Haiku (~1/15); the builder generates all implementation at
Sonnet prices.

The wheel v5 removed stays removed:

- **You edit nothing** — the smell is the first edit, not the tenth.
- **You re-run nothing an explorer is dispatched to verify** — its rerun
  replaces your bash call, never supplements it.
- **Distrust routes to a probe, not a takeover** — an explorer rerun or
  auditor probe settles doubt; re-editing the unit yourself does not.

And the caps are cost rules, not style: report caps on every dispatch,
`tail`-quieted commands, batched units, batched rulings, ≤1 audit relay
round. Every verbose report is bought again on every turn that follows it.

**v6's bet — MEASURED AND REFUTED (benchmark 7, 2026-07-13):** the bet was
that the guard layer's quality lives in the process, not the tier of the
model executing it. It does not: a sonnet control plane under this exact
process capped at 3/6 with regressions, split or merged, and cost more
than the fable version it replaced. The orchestrator/builder split was
separately measured as pure waste at same tier. The measured-best
methodology is the ceremony-free **fable-lean** arm (see
`benchmarks/2026-07-13-sonnet-refuted-fable-lean.md` and the harness
README): top-tier judgment hands-on, haiku eating all bulk context,
quoted-mechanism lenses instead of gates. This skill is retained as the
measured ancestor. Measured history in `benchmarks/` (1–5 under v2/v3, 6
under v4/v5, 7 refuting v6) — read it before extending any claim.
