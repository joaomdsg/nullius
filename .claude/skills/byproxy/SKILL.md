---
name: byproxy
description: Guard-layer for coding tasks — you build directly, and cheap read-only explorers (byproxy-explorer) guard the work with facts by proxy - recon before, a compiled design gate before building, an independent audit after. Use whenever the user invokes byproxy, asks for a guarded/audited build, mentions explorers or design gates, or hands over a task where independent verification of design and result matters more than speed.
---

# byproxy

You build. Explorers guard. The one rule that survives everything: **the
model doing the judging never gathers its own evidence, and the model
gathering evidence never judges.** You (the capable model) design, write
code, and rule on findings. Explorers (Haiku, read-only) fetch facts,
execute checks, and audit results — they report and quote, never conclude.

Every claim that matters arrives as an explorer report with machine output
quoted VERBATIM and gaps declared under UNKNOWN. Confident silence is the
failure mode; visible gaps are cheap.

## Report protocol

Explorer dispatches carry: `TASK:` (one action) · `SCOPE:` (files/dirs
allowed) · `CONTEXT:` (facts the explorer needs, nothing else) · `REPORT:`
(fields expected back) · a budget (`≤N tool calls`).

Explorer reports carry: `STATUS: done|partial|fail` · `FACTS:` ·
`VERBATIM:` (raw quoted machine output, never paraphrased) · `RISKS:`
(anomalies, even off-question) · `UNKNOWN:` (mandatory — explicit `none`
required) · optional `RULED-OUT:`.

A report missing VERBATIM or UNKNOWN is incomplete — re-ask once, then
replace the agent. Identifiers stay exact and full. Batch all independent
explorer dispatches into one turn; they are read-only and parallelize free.

## Workflow

```
1. RECON    fan out parallel explorers: house patterns, existing symbols,
            test layout, the specific files the task touches. unfamiliar
            terrain → one open recon sweep. their FACTS are your context.
2. DESIGN   draft the plan: units, per-unit test list (assert-level),
            API surface, files touched.
3. GATE     compile the design into falsifiable checks (taxonomy below).
            one explorer (mode: check) executes them mechanically —
            pass|fail + VERBATIM per check. you judge the results and
            amend the design before any code exists.
4. BUILD    you write it, one unit at a time, guarded TDD:
            tests first → run → quote the failure VERBATIM and confirm it
            fails for the right reason (missing behavior, not test bug) →
            minimal implementation to green → prune (every line: would
            deleting it fail a test? no → delete).
5. AUDIT    per landed unit, one explorer (mode: audit) — independent, it
            sees the diff cold: re-run the unit's exit check, uncovered
            exports/branches, code no test forces, risky lines quoted.
            you rule on each finding: fix now | queue | accept (say why).
6. CLOSE    final audit = full suite + vet, quoted VERBATIM. then report
            to the user: STATUS / FACTS / RISKS / UNKNOWN — including
            what was NOT tested. never ship unqualified success.
```

Small task → collapse to GATE + BUILD + final AUDIT. Don't manufacture
ceremony; never skip the audit.

## The design gate — compiled checks, not opinions

Never ask an explorer to critique a design — that's judging. Instead you
derive concrete checks it can answer by grep-and-quote, at least one per
pattern below (the taxonomy is fixed; it encodes the failure modes that
proved expensive, not the ones you happen to worry about):

1. **write-only API** — for each thing stored/produced: which listed test
   retrieves/observes it? (`does any test assert the response BODY of
   GET /uploads/{id}, not just the status?`)
2. **mandated-untested code** — for each design-mandated branch/behavior:
   which listed test forces it? (`does any test in unit 2's list exercise
   the eviction branch?`)
3. **trivially-passing test** — could a listed test pass on the bare
   existing tree? (`does a bare mux already return the 404 this test
   asserts?` — explorer can run it)
4. **collision** — do the design's new symbols/routes/patterns clash with
   existing ones? (`symbol Broadcast absent from pkg/hub? quote the
   existing mux registration pattern verbatim`)

The explorer answers pass|fail with quotes. Anomalies beyond the checklist
land in its RISKS — that channel is your coverage for the failure modes
you didn't think to check.

## Build discipline (guarded TDD)

- Test only through the public API; no mocks for domain logic — mock only
  genuine I/O boundaries (network, disk, clock, randomness).
- The red quote is mandatory: no implementation before a right-reason
  failure is on the record.
- All green includes pre-existing tests — a broken neighbor is root-caused,
  never suppressed.
- Wiring/orchestration code needs no unit test — build/vet/run it.
- Smallest diff that satisfies the exit check. No drive-by refactors.

## Audit independence

You never audit your own diff — proximity to the code you just wrote is
exactly the blind spot the audit exists to remove. The auditor gets the
unit's exit check and SCOPE, not your reasoning or your confidence. Its
findings come back as facts; the verdicts are yours, and every accepted
risk is written down in the CLOSE.

## Economics

Explorers are Haiku — roughly 1/15 the price of the top tier — and every
guard here is grep-and-quote work sized for them. The full guard layer
(recon + gate + audits) costs a fraction of one build mistake; the measured
history behind that claim lives in `benchmarks/` (data generated at commit
8f7ed5c, under this project's earlier architecture — read it before
trusting or extending it).
