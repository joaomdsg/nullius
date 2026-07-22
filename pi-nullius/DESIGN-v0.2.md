# pi-nullius v0.2 — design

Scope: incremental redesign of the v0.1 extension. The concurrency core
(single-flight hunt, await-free tool_call hook, closure-confined scouts)
audited clean and is retained unchanged. v0.2 targets the confirmed holes
from the 2026-07-22 review, grouped under three principles:

1. **The harness must never manufacture false confidence.** Exit codes,
   truncation, scout deaths, and refuter crashes must all be visible to
   the leader. A governor that silently converts failure into success is
   worse than no governor.
2. **Rulings are durable state.** The checklist is the run's judgment
   record; wiping it on re-hunt loses paid-for work and re-bills scouts.
3. **Language neutrality by table, not by rewrite.** Every language
   assumption moves into per-language data (packs, path tables, command
   tables); the engine stays generic. The deterministic layer is
   recall-oriented and honest about what it cannot see — precision is
   the hunter/refuter stages' job (that split is the architecture's
   strength; lean into it rather than making the scanner clever).

---

## 0. Evidence base

Repo-wide sweep of FINDINGS.md, benchmarks/, cc-nullius, archive/, and
bin/ (2026-07-22). What the measurements pin, and which section they feed:

- **Quoted-mechanism discipline IS the quality mechanism.** Every miss
  in the lens arc v1→v4 was a trusted-claim failure; hunter clearance on
  a non-covering mechanism shipped the always-true predicate (5/6);
  demanding leader-read lines restored 6/6 (FINDINGS.md:34-41). →
  rule()'s quote-verify stays load-bearing; §8's close is scout-run,
  never self-reported.
- **Turns are the cost.** Orchestrator is 88-93% of run cost; variance
  is turn count; the diet's real product is fewer, cheaper turns
  (2026-07-10 bench; agri: nullius-solo leanest in turns). → every
  governor interaction must cost ≤1 turn; denials that force
  regeneration are anti-doctrine (§6).
- **Post-generation denial double-bills.** cc-nullius removed size-cap
  hooks for exactly this (ROADMAP.md:24-36): a hook fires after the
  model has already paid the output tokens. pi's tool_call hook has the
  same timing → the identical-resend waive re-pays the full edit bytes.
  → §6 replaces resend-waive with a one-call waive.
- **Ceremony never beat plain runs** (byproxy-v6 refuted twice, $20.56
  → 3/6; greenfield full-process +78% cost for identical quality). →
  §2's BUILD short-circuit; no new ceremony anywhere.
- **False-greens are the failure class that matters**: 0-byte-file
  self-report, "all green under -race" with three failing tests,
  false-green broken build on the port A/B. → §1 exit preservation,
  §4 scout exit codes, §8 close-from-clean.
- **The diff-capture bug**: new `*_test.go` files silently dropped from
  judged diffs made craft look worse than it was (tests 1→8 when
  re-measured). → §8's surface diff explicitly includes NEW files.
- **Effort inversion**: nullius-low matches plain-HIGH quality at 26%
  cost; raising effort under nullius doubles cost for the same score.
  → §7 documents low-effort leader as the recommended default.
- **Limit-kills must be distinct exits**, never result text
  (nullius-build's exit-75 envelope). → §4 scout retry classification.
- Memory-sourced, not located in repo (flagged, not load-bearing):
  bench-9 "lens-circularity" note — tailored lenses scored 6/6 but
  generalized prompts dropped to 3.7/6; supports §5.5's always-on lens
  library direction but needs its notebook found before citing.

## 1. Truthful bounding (diet.ts)

**Problem.** `boundCommand` rewrites to `{ cmd ; } 2>&1 | tail -n 30` —
the pipeline exits with tail's status (0). A failing build the governor
silently rewrote reports success. The rewrite is also invisible: the
leader believes it saw complete output. The wide-search denial teaches
the same trap (`append '2>&1 | tail -n 20'`) and disagrees with the
actual bound (30).

**Design.**

```sh
{ cmd ; } 2>&1 | tail -n 30; s=${PIPESTATUS[0]}
[ "$s" -ne 0 ] && echo "[nullius: command exited $s]"
echo "[nullius: output bounded to last 30 lines]"
exit "$s"
```

- Exit status preserved via `PIPESTATUS` (bash) — pi runs bash; if the
  shell is ever not bash, fall back to *not* bounding rather than
  bounding wrongly.
- Two marker lines make both the truncation and any failure visible in
  the leader's context at ~40 tokens cost.
- Denial text for wide searches updated to quote the same recipe.

**Classifier v2** (same file, still pure/testable):
- Target detection for `rg`/`grep`: a trailing argument is a *target*
  if it contains `/`, exists on disk, or ends in `.<ext>` — directory
  scoping (`rg foo src/`) is narrow; a dotted *pattern* alone is not.
- Flag class covers `-R` as well as `-r`.
- `HEAVY_RE` becomes a table of per-ecosystem command lists (see §5.4).

## 2. Durable hunt ledger (`.nullius/hunt.json`)

**Problem.** `checklist = []` rebuilds from scratch on every hunt;
`rule()` verdicts live only in that array. The advertised overflow flow
("rule these, then hunt again for the withheld") re-runs the whole
scan+hunter+refuter pipeline and re-surfaces already-ruled items.

**Design.**
- Finding identity: `sha1(file + lens + fn + normalized-snippet-head)` —
  content-derived, so it survives line drift; `file:line` stays as the
  display locator only.
- `.nullius/hunt.json` (gitignored, same dir as terrain cache) holds:
  - `findings[id] = { loc, lens, note, origin }` — the FULL pre-cap
    list, not just the surviving 40;
  - `rulings[id] = { verdict, evidence, at }` — written by `rule()`.
- Re-hunt becomes **incremental**:
  - scan runs as today (it is cheap and deterministic);
  - findings whose id has a ruling are filtered out *before* the
    hunter/refuter stages — ruled work is never re-billed;
  - **exception**: a `fixed` ruling whose finding re-appears in the scan
    is a regression signal — surfaced at the TOP of the checklist as
    `REGRESSED (was ruled fixed: <evidence>)`, never silently filtered.
- Overflow release is free: `hunt` with a live ledger and no scan drift
  releases the next 40 withheld findings without any scout spend.
- Ledger loads at session_start; a missing/corrupt file degrades to
  v0.1 behavior (fresh hunt), never blocks.

**BUILD short-circuit** (from cc-nullius's FULL/BUILD terrain gate;
measured: full ceremony on lens-hostile greenfield = +78% cost, same
quality). The deterministic scan IS pi-nullius's Turn A — when it
proves empty terrain, don't spend the hunter/refuter stages:

- If the scan yields zero findings AND zero unpackedFiles AND the repo
  has no pre-existing source in mandate scope (greenfield heuristic:
  scan.fnScanned below a threshold or an explicit `hunt({mode:
  "gate"})` arg), the hunt returns a BUILD ruling with the quoted
  absences (`0 findings across N files / M functions; 0 unscanned`),
  skips hunters and refuters, and instructs: build, then re-hunt YOUR
  OWN new code before close.
- Doubt → FULL, exactly as the doctrine gate: any finding, any
  unpacked file, any scan failure keeps the full pipeline.

## 3. Read-ledger v2 (index.ts)

**Problem.** Three leak paths: bash mutations never invalidate; the
first re-read of one range clears invalidation for the whole path while
other ranges stay stale-denied; compaction leaves the ledger claiming
"you hold it" for content the leader no longer holds.

**Design.** One mechanism kills all three: **mtime-stamped entries**.
- Ledger entry: `key → { mtimeMs }` captured at read time.
- On a re-read hit: `stat` the file; if `mtimeMs` differs from the
  entry, the file changed (edit tool, bash, formatter, git — anything)
  → allow the read and refresh the entry. `editedSinceRead` is deleted
  entirely.
- `session_before_compact` clears the ledger (and only the ledger —
  gate state and hunt ledger survive compaction by design: obligations
  and rulings are not context).
- Cost: one `statSync` per duplicate-read attempt — only on the denial
  path, which is rare by construction.

## 4. Scout integrity (index.ts)

- `runScout`: `child.on("close", (code) => finish(code ? new Error(`scout
  exited ${code}`) : undefined))`. A scout that printed a partial report
  then died is a failed scout, not a record.
- **Retry classification** (nullius-build's exit-75 lesson): a scout
  killed by rate/spend limits is RETRYABLE, a scout that failed is not.
  `runScoutRetry` inspects stderr/exit for limit signatures (429,
  overloaded, spend cap) and retries with backoff only those; genuine
  failures surface immediately instead of burning the retry budget.
- Refuter-batch failures: count them; findings whose refuter crashed
  enter the checklist tagged `UNREFUTED` (mirroring `UNVERIFIED`), and
  the summary line reports `refuters failed: N`. Fail-closed is kept;
  invisibility is not.
- `scanRepo` call wrapped: tree-sitter init failure degrades to
  "deterministic scan unavailable (<reason>) — scout-only hunt" with all
  source files routed through the `unpackedFiles` honesty path, instead
  of an uncaught throw.

## 5. Language layer v2

### 5.1 Function enumeration (lenses.ts fnQuery)

Add anonymous/expression forms per grammar; synthesize the display name
from the assignment LHS or enclosing symbol (`<anon in handler>`):

| lang | added node forms |
|------|------------------|
| ts/js | `arrow_function`, `function_expression` (when RHS of `variable_declarator` / `public_field_definition` / `pair`), `method_definition` retained |
| go | `func_literal` when RHS of `var`/`:=`/field; top-level only — literals inside a scanned body are already visible as parent text |
| python | `lambda` skipped (bodies are single expressions — nothing for body-regex lenses to bite); `async def` verified matched by `function_definition` |
| rust | `closure_expression` when bound via `let` |
| java | `constructor_declaration`, `lambda_expression` when bound via `variable_declarator` |

Rule of thumb encoded in a comment: enumerate a node iff its body can
outlive the enclosing scan unit's text (module-level or field-level
bindings); nested literals ride along as parent-body text.

### 5.2 Grammar coverage (EXT_TO_LANG / LANG_PACKS)

- `EXT_TO_LANG` extended to every grammar shipped in
  `tree-sitter-wasms` (c, cpp, c_sharp, ruby, bash, elixir, dart, …).
- New packs start minimal: `fnQuery` + `sharedLhs` only (the universal
  CALL/ASSIGN/BINARY node-type sets mostly transfer); `swallowRe`/
  `deferNodes` added per language as they are validated.
- A truly unmapped extension now lands in `unpackedFiles` (today it
  silently skips), so the hunt summary's "NOT scanned — hunt via scout"
  is honest for every file in the repo.
- **Grammar canary test**: for each pack, parse a 5-line fixture and
  assert the fnQuery matches ≥1 function — catches node-type drift
  across grammar upgrades mechanically (currently UNKNOWN territory).

### 5.3 One test-path oracle

Single `isTestPath` in gates.ts, imported by index.ts (the hunt filter
at index.ts:453 is deleted):

```
__tests__/ · tests?/ · *_test.<ext> · *.test.* · *.spec.* ·
test_*.py · conftest.py · *_spec.rb · conformance
```

Fixes both directions of the current bias: Go leaders stop tripping the
ratchet while writing `_test.go` files; TS hunts stop filling the
checklist with findings inside `.test.ts` files.

### 5.4 Command & source tables

- `HEAVY_RE` → generated from a per-ecosystem table; additions: `bun`,
  `deno test/task`, `./gradlew`, `sbt`, `mix test`, `composer`,
  `phpunit`, `rspec`, `rake`, `stack`/`cabal`, `npx vitest|jest|
  playwright`, `vitest`, `jest`, `swift test/build`, `zig build`.
- `isSourcePath` additions: `sh bash zsh lua zig ex exs erl hs ml mli
  clj cljs vue svelte dart sql tf r jl`.

### 5.5 Async-aware lenses (the accuracy gap that matters most)

The guard vocabulary is lock-era; for TS/JS/Python the races ARE async
races. Two additions, both recall-oriented (hunters adjudicate):

- Per-pack `asyncGuard` list joining `GUARD_CALLS` duty: ts/js —
  `await` in guard position, `queueMicrotask`, `navigator.locks`;
  python — `asyncio.Lock`, `async with`; rust — `tokio::sync`,
  `std::sync` (`Arc` alone is not a guard); go — channel send/recv
  (`<-`) recognized as a serialization mechanism in `hasGuard`.
- New lens `fire-and-forget`: a call known to return a promise/task
  (`.then(` without `await`/return, `asyncio.create_task` with no
  later `gather`/`await`, `tokio::spawn` handle dropped, bare `go `
  call whose function writes shared state) → AMBIGUOUS. Deterministic
  approximation is deliberately loose; the refuter stage exists to
  absorb the noise.
- Fault-survival stays textual (startIndex ordering) — documented as a
  known over-flag; control-flow awareness is out of scope for v0.2
  (the hunter reads the body anyway).

## 6. Gates v2 (gates.ts) — the waive redesign

**The identical-resend waive is anti-doctrine and goes.** pi's
tool_call hook fires after the model has generated the edit args, so a
denial has already billed the full output tokens — and the waive then
bills them AGAIN as a verbatim resend. cc-nullius removed its size-cap
hooks over exactly this measurement (post-generation deny = double
bill). pi keeps its gates (no craftsman tier exists to absorb the
discipline), but the override becomes one cheap call:

- New `waive(gate: "size" | "ratchet", declaration: string)` tool
  (~30 output tokens vs regenerating a 40+-line edit). Calling it arms
  a one-shot pass for the NEXT matching blocked edit. The declaration
  ("interface change: renaming the export across call sites") lands in
  the transcript and the telemetry (§10) — the educate-once property
  of the block survives, the double-bill does not.
- Denial texts updated: "…or call waive('size', why) and resend."
  (The resend after waive is unavoidable — pi cannot replay a blocked
  call — but the *decision* no longer costs a second generation of
  reasoning; a granted waive is mechanical.)
- A `size`-reason waive still falls through to the ratchet check — an
  oversized edit cannot dodge a due tests-first block.
- Ratchet threshold: align with cc-nullius (`NULLIUS_TESTS_FIRST`,
  default 4 there vs 3 here) — pick one measured default and share it.
- `rule()` evidence minimum raised 15 → 20 chars with an honest message
  (today 15–19-char evidence always fails the quote-scan with a
  misleading "quote not found").

## 8. Close protocol (new tool)

pi-nullius has hunt and rule but NO close — the doctrine's step 6 is
entirely un-mechanized, and false-greens are the measured failure class
(three separate incidents in §0). New `close` tool:

- Refuses while checklist items are open (`rule` every item first —
  same contract the hunt summary already states).
- Dispatches ONE scout: full test suite + the project's linters,
  verbatim tail, run from a clean state — plus the surface diff:
  `git diff --stat` vs the session's base commit AND
  `git status --short` so **new untracked files (especially new test
  files) are in the record** — the diff-capture bug made added tests
  invisible and mis-scored a whole bench arm.
- Surface check: every removed/renamed exported symbol in the diff
  listed verbatim (cc's droppedExports() regex transfers directly);
  each must be ruled a decision or it re-enters the loop.
- The close scout flags 0-byte source files and missing package/module
  declarations explicitly (the measured 0-byte false-green).
- Output is the STATUS/FACTS/RISKS/UNKNOWN/ASSUMED ledger skeleton,
  pre-filled with the verbatim record — the leader rules, the scout
  reports.

## 9. ctx-sentinel (port from cc-nullius)

pi-nullius shows ctx tokens in the status bar; nobody reads status bars
under pressure. Port the sentinel: at KNEE (128k default,
`NULLIUS_CTX_KNEE`) inject a nudge into the leader's context via the
message hooks — "past the attention knee: no new hunts; finish the
mandate, close, then compact" — re-nudged once per BAND (32k) so it
rides the context edge where attention actually lands. The
session_before_compact compactor already preserves the ledger; the
sentinel is what makes the leader USE it at the right moment.

## 10. Telemetry + toggles (port from cc-nullius)

- **Stats file is truth, not model self-report** (measured: the model
  mis-reports its own governor interactions). Persist counters —
  denies per rule, silent bounds, waives, escapes, scout
  runs/cost, ctx nudges — to `.nullius/stats-<session>.json`, written
  atomically (tmp + rename; two pi processes may share a repo). The
  in-memory `status()` reads through it; `/nullius` reports it.
- **Marker-file toggles**: `.nullius-quick` and `.nullius-off` with
  mtime auto-expiry (4h), matching cc-nullius semantics — env vars
  require a restart, marker files don't, and QUICK-with-expiry is the
  measured-safe escape (relaxes the governor, never the gate).

## 7. DX

- README documents `hunt`, `rule`, the auto-hunt, and the checklist
  contract — the two tools the discipline hinges on are currently
  undocumented.
- `package.json`: `"check": "tsc --noEmit"`, wired before `test` in a
  `verify` script. Types are currently stripped, never checked.
- Env knobs for the remaining hardcoded constants:
  `NULLIUS_SCOUT_IDLE` (120s — local models hit this), `NULLIUS_CAP`
  (checklist 40), advisor evidence caps.
- **pi contract canary**: a test that constructs a `tool_call` event,
  mutates `input` in a hook, and asserts the mutation is visible to the
  dispatch — pins the reference-passing behavior pi-nullius relies on
  (verified in pi 0.81.0 source, but it is behavior, not contract).
- Governor denials remain teach-the-route style (they audited well);
  the wide-search denial's recipe updated per §1.
- README documents the recommended leader effort: LOW. Measured:
  nullius-low matches plain-HIGH at 26% cost; raising effort under
  nullius doubled cost for the same 6/6 — the method substitutes for
  effort, not the other way around.
- 8th lens from the cc prose library — **boundaries** (off-by-one,
  `<` vs `<=`, nil-vs-empty) — added to the HUNTER_SYS vocabulary, not
  the deterministic scanner (no reliable node-level signature; hunters
  adjudicate candidates the other lenses' terrain already surfaces).

## Build order

1. §1 + §4 (truthful bounding, scout exit codes + retry classes,
   refuter visibility) — small, independent, kill the false-confidence
   class outright.
2. §3 read-ledger mtime rework — deletes code (`editedSinceRead`), adds
   one stat.
3. §6 waive tool + §7 quick items (evidence min, tsc check, README,
   env knobs, canary tests).
4. §8 close tool — mechanizes the doctrine step with the worst measured
   failure mode; §10 telemetry lands with it (close reads the stats).
5. §5.3 + §5.4 tables (shared isTestPath, command/source tables).
6. §2 durable hunt ledger + BUILD short-circuit — the largest piece;
   lands with its own tests (identity hashing, regression re-surfacing,
   overflow release, gate absences).
7. §9 ctx-sentinel + §10 marker-file toggles.
8. §5.1 + §5.2 fnQuery/grammar expansion — per-language, each behind a
   grammar canary fixture.
9. §5.5 async lenses last — needs bench validation (rev-9-style run)
   to confirm the noise stays inside the refuter stage's absorption.

Each step keeps `node --test` green; §5 steps add fixture-driven tests
per language pack.
