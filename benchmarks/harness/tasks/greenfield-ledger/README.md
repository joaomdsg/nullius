# greenfield-ledger — FROZEN, first results in

**Results (2026-07-17, n=1/arm, suite frozen before any rep):** plain
fable-low **$3.98, 29/29 hidden + 6/6 groups, 25 turns**; cc-nullius
full-process **$7.08, 29/29 + 6/6, 57 turns** — preregistration
confirmed strong-form: on empty lens terrain the process is pure tax.
Same day, the plugin gained a terrain GATE (quoted absences → ceremony
stands down) + turn-economy doctrine; gated rerun **$4.54, 29/29 + 6/6,
12 turns, 3 dispatches, surface clean** — leader cost $3.67 below
plain's total. Rows carry cache.hit_rate (0.861 plain-era / 0.888 gated).

Greenfield benchmark on invariant classes DISJOINT from vialite-todo and
from the nullius lens library — a deliberately lens-hostile domain. The
task is single-threaded by contract: serialization, fault-survival,
scope-confinement, wake-predicate, TTL, and race lenses have zero terrain.
This benches what vialite could not: (a) write-heavy greenfield under the
diet, and (b) the hunt's ADAPTIVE claim — terrain scouts must surface
invariant classes the lens library does not name, and turn B must add
lenses for them (exactness, idempotent replay, lifecycle legality,
canonical encoding, hostile input). Deterministic domain → the hidden
suite needs no sleeps, no timing assumptions, no -race amplification
worries (a -race pass is still required but trivially).

**Preregistered expectations (frozen before any rep; suite hash committed
before rep 1 of ANY arm):**
1. The plugin's quality edge over plain should be SMALLER than on vialite —
   its lens library confers no advantage here; any residual edge is
   attributable to the diet + checklist discipline, not the lenses.
2. Discrimination risk disclosed up front: frontier models are decent at
   ledgers. The discriminating corners are chosen to be classic model
   weak spots (largest-remainder allocation, byte-canonical encoding,
   idempotent replay returning the ORIGINAL result, error paths that
   leave state untouched). If all arms ceiling anyway, the recorded
   finding is "greenfield at this size does not discriminate" — that
   outcome is bought knowingly.

## Six invariant groups (invariants.json; all non-vialite)

1. `conservation` — per-currency zero-sum after ANY operation sequence,
   including error paths, voids, and import (property tests, fixed seeds).
2. `exact-arithmetic` — integer minor units; Allocate largest-remainder
   exactness (sum == input, deterministic remainder placement); overflow
   rejected, never wrapped.
3. `idempotent-replay` — same key applies once and returns the ORIGINAL
   result (not an error, not a re-execution), regardless of payload drift.
4. `lifecycle-legality` — append-only journal; void-by-compensation;
   illegal transitions are typed errors with ZERO state mutation
   (all-or-nothing checked by full-state comparison before/after).
5. `canonical-roundtrip` — Export byte-determinism (same ledger → same
   bytes across construction orders that yield equal state);
   Import∘Export ≡ identity incl. re-Export byte-equality.
6. `hostile-input` — no panics ever (fuzz corpus, fixed seeds); typed
   errors per contract; truncated/corrupt imports rejected atomically.

## Status: built + hunted, awaiting freeze

- [x] prompt.md (the mandate)
- [x] `skeleton/` — go.mod, `ledger.go` (pinned API + doc-comment
      guarantees; audit-surfaced ambiguities closed in the contract text),
      `accept/` visible test
- [x] reference implementation (`reference/` — audited line-by-line
      against the contract: 16 suspects, all verified conforming)
- [x] `hidden/` — deterministic black-box suite, 6 files / 29 tests,
      seeded property + fuzz corpora
- [x] vacuousness check — 12 variants (2/group) in `variants/`;
      red-matrix run (g6 initially PASSED → its test strengthened to
      assert application, not just error; rerun pending freeze)
- [x] `invariants.json` — 6 groups → catchers, cross-listed per matrix
- [x] `score.sh` — visible DoD with accept-test integrity overlay (the
      skeleton's accept test is what scores; gutting the delivered copy
      earns nothing), hidden pass rate, per-group catchers WITH -race,
      exported-surface check, catcher-name regex sanitization
- [x] meta.env (SEED_DIR=skeleton, HIDDEN_DIR=hidden, TIMEOUT_S=5400)
- [x] plain arm pinned before any rep (see Run plan)
- [x] FROZEN 2026-07-17: sha256(hidden/* + skeleton/ledger.go +
      skeleton/accept/accept_test.go + invariants.json + score.sh +
      prompt.md) = `f0d2bcaf10d83670…`. Final red-matrix: reference
      PASS-ALL + race-clean; 12/12 variants fail with in-group catchers.
      NO suite/contract edits from here — a needed change voids all reps.

Six-hunter audit (2026-07-17, 54 suspects): fixed — Void-key contract
test, empty-ledger export, exact escape-table bytes, imported-replay
no-op state checks, balanced multi-currency post, Balance-currency
assertion, hairy-key replay after import, contract escape sentence
broadened to every variable-content field, no-panic clause added to
package doc, scorer fixes (regex sanitize, build-fail detection,
per-group -race, accept overlay). Accepted-no-fix — doc-comment gutting
passes the surface check (behavior is enforced by the suite; deleting
comments only hurts the deleting agent).

## The pinned API (draft, becomes skeleton/ledger.go)

```go
package ledger

// Amount: integer minor units + ISO-4217-style currency code. No floats.
type Amount struct { Units int64; Currency string }
func ParseAmount(s, currency string) (Amount, error)   // "12.34" → 1234 minor units
func (a Amount) Add(b Amount) (Amount, error)          // ErrCurrencyMismatch, ErrOverflow
func (a Amount) Neg() Amount
// Allocate splits a by integer weights, largest-remainder rule; parts sum
// exactly to a; remainder units go to the largest fractional remainders,
// ties broken by lowest index. len(weights)==0 or all-zero → ErrBadInput.
func Allocate(a Amount, weights []int) ([]Amount, error)

type Ledger struct{ /* unexported */ }
func New() *Ledger
func (l *Ledger) CreateAccount(id, currency string) error
type Entry struct { Account string; Amount Amount }
type Tx struct { Key string; Memo string; Entries []Entry }  // Key = idempotency key
type TxID string
func (l *Ledger) Post(tx Tx) (TxID, error)     // balanced per currency or ErrUnbalanced
func (l *Ledger) Void(id TxID, key string) error
func (l *Ledger) Balance(account string) (Amount, error)
type JournalEntry struct { ID TxID; Seq uint64; Memo string; Entries []Entry; Voids TxID }
func (l *Ledger) Journal(afterSeq uint64, limit int) []JournalEntry  // deterministic pagination
func (l *Ledger) Export(w io.Writer) error
func Import(r io.Reader) (*Ledger, error)

var (
    ErrCurrencyMismatch, ErrOverflow, ErrBadInput, ErrUnbalanced,
    ErrUnknownAccount, ErrUnknownTx, ErrAlreadyVoided, ErrCorruptImport error
)
```

Every ambiguity closed in doc comments before the suite is written:
Allocate tie-breaking, ParseAmount accepted grammar (sign, decimals per
currency exponent), replay-returns-original semantics, Journal ordering
(Seq assigned at accept time, strictly increasing), Export encoding fully
specified in the doc comment (field order, escaping, newline discipline) —
the suite tests the SENTENCE, never the reference implementation's whim.

## Run plan

1. Build skeleton + reference + suite; validate green-on-reference and
   red-on-every-broken-variant; commit + record suite hash.
2. One `plain` rep + one `cc-nullius` rep (JUDGE=0 — nothing seeded;
   quality-judge.sh optionally run manually on both diffs, blind).
   **Plain arm pinned before any rep: PLAIN_MODEL=claude-fable-5,
   PLAIN_EFFORT=low** — same model and effort as the cc-nullius arm, so
   the ONLY variable is the plugin (doctrine + governor + agents). Not
   the opus default: cross-model comparison would confound the result.
3. Score against preregistered expectations; record either way.
