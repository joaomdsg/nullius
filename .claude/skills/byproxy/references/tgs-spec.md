# TGS — Telegraphic-Grok-Schema v0.2

Inter-agent message protocol. Grok-level density, schema-level reliability.
Design rule: **compress the channels, never the scratchpads.** TGS governs
messages between agents only. Reasoning inside each agent stays full-fat.

---

## 1. Core rules

1. **Schema carries roles, not grammar.** Every message = labeled fields.
   Field label replaces the syntax that grammar would provide. No prose
   between fields.
2. **Grok inside fields.** Drop articles, copulas, auxiliaries, politeness.
   Noun-first. `mutex parse.go:88` not `there is a mutex in parse.go at line 88`.
3. **Machine output is quoted, never paraphrased — and bounded.** Compiler
   errors, test assertions, stack frames go in `VERBATIM:` raw. Paraphrase =
   information loss at the cheapest model. Telegraph the prose, quote the
   machine. Output over ~40 lines: quote the load-bearing slice (failing
   assertion + top ~5 frames + surrounding context) and mark the elision
   with a pointer, e.g. `[... 120 lines elided — full log at path:line ...]`.
   Elide, never paraphrase.
4. **Absence is reported.** `UNKNOWN:` and `RULED-OUT:` are mandatory where
   defined. Confident silence is the failure mode; visible gaps are cheap.
5. **One message, one purpose.** Dispatch, report, escalate, redirect —
   never mixed.
6. **Identifiers are sacred.** Paths, symbols, line numbers, versions: exact,
   full, never abbreviated. Compression applies to connective tissue only.
7. **Ambiguity beats brevity nowhere.** If a grok phrase can parse two ways,
   add the one word that fixes it. One clarifying round-trip costs ~10× the
   tokens saved.

## 2. Field vocabulary

Uppercase label, colon, content. `|` separates alternatives/items inline.
Multi-item fields may use one line per item.

| Field | Used in | Content |
|---|---|---|
| `TASK:` | dispatch | imperative, one action |
| `SCOPE:` | dispatch | files/dirs/symbols allowed. `only` suffix = hard fence |
| `CONTEXT:` | dispatch | facts recipient needs, nothing else |
| `DONE-WHEN:` | dispatch | mechanical exit condition (test green, file exists) |
| `ESCALATE-IF:` | dispatch (builder) | deterministic triggers, `\|`-separated. dispatch sets concrete bounds (diff size, tool calls); an unbounded dispatch is a spec bug |
| `REPORT:` | dispatch (explorer) | fields expected back |
| `FACTS:` | report | findings. one per line if >2 |
| `RISKS:` | report | hazards spotted, even off-question |
| `UNKNOWN:` | report | what was not checked / could not determine |
| `RULED-OUT:` | report | dead ends, so orchestrator never re-dispatches them |
| `VERBATIM:` | report/escalate | raw quoted lines, fenced if multiline |
| `BLOCKED:` | escalate | what stops progress |
| `TRIED:` | escalate | attempts made, `\|`-separated |
| `NEED:` | escalate | decision/info requested from orchestrator |
| `STATUS:` | report/heartbeat | `done \| partial \| fail` |
| `DIAG:` | redirect | orchestrator's diagnosis, one line |
| `FIX:` | redirect | corrective instruction |

Extend vocabulary only when a role recurs ≥3× and no existing field fits.
New field = protocol change, not per-message improvisation.

## 3. Message types

### 3.1 Dispatch → explorer
```
TASK: run TestParseHeader. locate failure cause
SCOPE: pkg/lex, pkg/parse only
REPORT: FACTS, VERBATIM(failing assertion + top 3 frames), UNKNOWN, RULED-OUT
```

### 3.2 Explorer → orchestrator
```
STATUS: done
FACTS: TestParseHeader fail. off-by-one token count lex.go:31. parse.go clean
VERBATIM:
  --- FAIL: TestParseHeader (0.01s)
  parse_test.go:42: want 3 tokens, got 2
  lex.go:31 → parse.go:88
RISKS: lex.go:29 comment says "intentional skip" — fix may break TestComment
UNKNOWN: TestComment not run
RULED-OUT: parse.go logic. input fixture. build tags
```

### 3.3 Dispatch → builder
```
TASK: fix off-by-one token count lex.go:31
SCOPE: lex.go only
CONTEXT: comment lex.go:29 claims intentional skip — preserve comment intent or update it
DONE-WHEN: TestParseHeader + TestComment green
ESCALATE-IF: 2 consecutive fail runs | fix requires touching parse.go | diff >30 lines
```

### 3.4 Builder → orchestrator (escalation)
```
STATUS: fail
BLOCKED: TestComment breaks when skip removed
TRIED: conditional skip | counter offset
VERBATIM:
  --- FAIL: TestComment (0.00s)
  lex_test.go:77: comment token leaked to output
NEED: decide — comments as tokens, or filter at parse layer
```

### 3.5 Orchestrator → builder (redirect)
```
DIAG: skip serves comment filtering. removal correct but filter must move
FIX: emit comment tokens in lex. filter in parse.go:emit(). update TestComment expectation
SCOPE: lex.go, parse.go
DONE-WHEN: both tests green
```

### 3.6 Recon dispatch (peripheral vision — one per task, before narrow interrogation)
```
TASK: recon pkg/lex. report anomalies, surprises, smells
SCOPE: pkg/lex read-only
REPORT: FACTS, RISKS, UNKNOWN
```

## 4. Grok grammar (inside fields)

- Drop: articles, copulas ("is/are"), auxiliaries ("has been"), hedges,
  politeness, subject pronouns when obvious.
- Keep: negation words (always explicit — `no test coverage`, never implied),
  numbers, units, exact identifiers.
- Order: topic first, then predicate. `mutex parse.go:88` / `test cover none`.
- `|` = list separator. `→` = causal/flow. `?` suffix = unconfirmed.
- Never grok-compress inside `VERBATIM:`.

## 5. Compliance & anti-patterns

- Message with prose outside fields → malformed. Recipient may answer
  `NEED: resend TGS` at sender's cost.
- Paraphrased error message → protocol violation (rule 3).
- Report without `UNKNOWN:` → treated as incomplete, not as "nothing unknown".
  Explicit `UNKNOWN: none` required.
- Abbreviated path (`lex.go` when two exist) → violation of rule 6.
- Do not use TGS in reasoning/scratchpad. Do not use TGS with humans unless
  the human asked.

## 6. Rationale (one paragraph, for the humans)

BPE makes function words cheap and identifiers expensive, so freeform
compression has ~10–20% ceiling over disciplined telegraphic style — while
each ambiguity costs a round-trip worth ~10× the savings. TGS takes the
density of grok but moves the disambiguation burden from grammar (which grok
deletes) to fixed field labels (which cost 1–2 tokens and parse
deterministically). VERBATIM and UNKNOWN exist because the cheapest model in
the loop must never be the one deciding what information survives.
