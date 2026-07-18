# Task: build `ledger` — a double-entry accounting core, from scratch

You are starting a new in-house library, **ledger** (module
`github.com/go-via/ledger`, at the repository root). Nothing exists yet but
a module file, a pinned public API you must implement, and a small visible
acceptance test. You own every line you ship.

`ledger` is the money core a billing system will sit on: accounts hold
balances in minor units, transactions post as balanced sets of entries,
history is an immutable journal. It is **not** a concurrent component: the
contract requires correctness for single-goroutine use only. What it must
be is *exact* — money code is where "almost right" is wrong.

## The public API is a contract

`ledger.go` declares the exported API: types, signatures, and doc comments
stating each guarantee, including the exact arithmetic rules. **Implement
exactly that surface.** Add unexported code and files freely; never remove,
rename, or change the signature of anything exported.

## What "correct" means here

- **Conservation.** Every accepted transaction's entries sum to exactly
  zero per currency. At any point in any sequence of operations, the sum
  of all account balances per currency is exactly zero. No operation may
  ever break this — not an error path, not a void, not an import.
- **Exact arithmetic.** Amounts are integer minor units — no floats
  anywhere money flows. `Allocate` splits an amount by integer weights
  with the largest-remainder rule and MUST return parts that sum exactly
  to the input, distributing remainder cents deterministically as the doc
  comment specifies. Overflow is detected and rejected, never wrapped.
- **Idempotent application.** `Post` and `Void` take an idempotency key.
  The same key replayed — same payload or different — applies exactly
  once and returns the original result; a replay is never a double-post
  and never an error.
- **Lifecycle legality.** A transaction is `posted` then possibly
  `voided`; voiding reverses its effect via a compensating journal entry.
  There is no update and no delete: the journal is append-only, and every
  illegal transition (void twice, void unknown, post to a missing or
  mismatched-currency account) is a typed error that leaves ALL state
  untouched.
- **Canonical round-trip.** `Export` writes a canonical, byte-deterministic
  encoding (same ledger → same bytes, always). `Import(Export(l))`
  reconstructs an equivalent ledger: same balances, same journal, same
  idempotency behavior, and its own Export is byte-identical.
- **Hostile input, typed errors.** Malformed input — unparseable amounts,
  unknown currencies, empty/oversized fields, invalid UTF-8 in memos,
  corrupt or truncated import bytes — returns the documented typed error
  and never panics, never partially applies.

## Definition of done

- `go build ./...` and `go vet ./...` clean.
- The visible acceptance test passes: `go test -count=1 ./accept/`.
- Your own tests pin every guarantee above — for each rule the doc
  comments state, a test that fails if it is broken.
- Ship a report: STATUS / FACTS / RISKS / UNKNOWN / ASSUMED. Any guarantee
  you could not meet or verify is RISKS, named plainly — an undisclosed
  gap is worse than a disclosed one.

There is no user to ask. Decide, record decisions in ASSUMED, and ship.
