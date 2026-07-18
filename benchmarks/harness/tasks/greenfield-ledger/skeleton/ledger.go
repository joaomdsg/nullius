// Package ledger is a double-entry accounting core: accounts hold balances
// in integer minor units, transactions post as balanced entry sets, history
// is an immutable journal.
//
// The doc comments in this file are the contract. Every stated rule is a
// guarantee callers rely on. The package is NOT required to be safe for
// concurrent use; it IS required to be exact.
//
// Global invariant (conservation): after every exported call returns —
// success or error — the sum of all account balances per currency is
// exactly zero.
//
// Atomicity: any call that returns a non-nil error has changed nothing:
// no balance, no journal entry, no idempotency record, no account.
//
// Robustness: no exported function or method panics, for any input,
// including arbitrary bytes fed to Import and arbitrary strings fed to
// every other entrypoint. Failures are always returned as errors.
package ledger

import (
	"errors"
	"io"
)

// Sentinel errors. Implementations return these exact values (or wrap them
// such that errors.Is matches).
var (
	ErrCurrencyMismatch = errors.New("ledger: currency mismatch")
	ErrOverflow         = errors.New("ledger: amount overflow")
	ErrBadInput         = errors.New("ledger: bad input")
	ErrUnbalanced       = errors.New("ledger: entries do not balance")
	ErrUnknownAccount   = errors.New("ledger: unknown account")
	ErrUnknownTx        = errors.New("ledger: unknown transaction")
	ErrAlreadyVoided    = errors.New("ledger: transaction already voided")
	ErrCorruptImport    = errors.New("ledger: corrupt import")
)

// Amount is money as integer minor units of a currency. No floating point
// may be used to compute any Amount an exported API returns or stores.
//
// Currency is exactly 3 uppercase ASCII letters. All currencies have
// exponent 2 (two decimal places) in this system.
//
// Valid range for Units is [-(1<<63 - 1), 1<<63 - 1]: the value
// math.MinInt64 is invalid everywhere, so negation is always safe. Any
// operation or parse whose result would fall outside this range fails with
// ErrOverflow (operations) or ErrBadInput (parsing malformed text remains
// ErrBadInput; parsing a well-formed number outside the range is
// ErrOverflow).
type Amount struct {
	Units    int64
	Currency string
}

// ParseAmount parses a decimal string into an Amount of the given currency.
//
// Grammar (nothing else is accepted): an optional leading '-', then one or
// more ASCII digits, then optionally a '.' followed by EXACTLY two ASCII
// digits. No '+', no leading/trailing spaces, no grouping separators, no
// lone '.', no "-0.00"-forbidden — "-0.00" and "-0" ARE accepted and equal
// zero. Examples: "0", "12", "12.34", "-0.05".
//
// currency must be exactly 3 uppercase ASCII letters, else ErrBadInput.
// Malformed text → ErrBadInput. Well-formed but out of range → ErrOverflow.
func ParseAmount(s, currency string) (Amount, error) { panic("unimplemented") }

// String renders the amount in the same grammar ParseAmount accepts:
// minus sign for negative values, integer part with no leading zeros
// (except "0"), '.', exactly two digits. "-0.00" is rendered "0.00".
// ParseAmount(a.String(), a.Currency) round-trips every valid Amount.
func (a Amount) String() string { panic("unimplemented") }

// Add returns a+b. ErrCurrencyMismatch if currencies differ; ErrOverflow
// if the result leaves the valid range.
func (a Amount) Add(b Amount) (Amount, error) { panic("unimplemented") }

// Neg returns the amount negated. Always valid: MinInt64 never occurs.
func (a Amount) Neg() Amount { panic("unimplemented") }

// Allocate splits a into len(weights) parts that sum EXACTLY to a.
//
// Rules: weights must be non-empty, every weight >= 0, at least one > 0;
// otherwise ErrBadInput. Let W = sum(weights). Part i's exact share is
// a.Units * weights[i] / W as a rational. Each part is first assigned the
// share rounded toward zero; the leftover units (whose count is < number of
// nonzero remainders, each worth 1 minor unit carrying the sign of a) are
// then distributed one apiece to the parts with the LARGEST fractional
// remainder by absolute value, ties broken by LOWEST index. Zero-weight
// parts receive exactly zero. Intermediates MUST be computed exactly for
// every valid input — use big integers internally where a.Units*weight
// would overflow int64. Allocate itself never returns ErrOverflow: result
// parts cannot overflow since |part| <= |a|.
//
// Consequences the caller may rely on: sum(parts) == a exactly;
// Allocate(100 units, [1,1,1]) = [34,33,33]; Allocate(-100, [1,1,1]) =
// [-34,-33,-33]; Allocate(101, [3,3,1]) = [43,43,15] (index 2 has the
// largest fractional remainder, 101/7 ≈ 14.43).
func Allocate(a Amount, weights []int) ([]Amount, error) { panic("unimplemented") }

// Entry is one leg of a transaction: the amount is ADDED to the account's
// balance (debits/credits are just sign).
type Entry struct {
	Account string
	Amount  Amount
}

// Tx is a transaction to post. Key is the idempotency key: non-empty,
// at most 128 bytes, valid UTF-8. Memo: valid UTF-8, at most 500 bytes,
// may be empty. Entries: at least two, each with nonzero Units.
type Tx struct {
	Key     string
	Memo    string
	Entries []Entry
}

// TxID identifies an accepted transaction. IDs are deterministic:
// "tx-<seq>" where <seq> is the journal sequence number assigned to the
// transaction's journal entry (see JournalEntry.Seq).
type TxID string

// JournalEntry is one immutable journal record. Seq starts at 1 and
// increases by exactly 1 per accepted operation (post or void). For a
// post, Voids is "". For a void, Voids is the TxID being voided and
// Entries are the exact negation of the voided transaction's entries.
type JournalEntry struct {
	ID      TxID
	Seq     uint64
	Memo    string
	Entries []Entry
	Voids   TxID
}

// Ledger is the accounting core. The zero value is not usable; call New.
type Ledger struct {
	// unexported fields
}

// New returns an empty ledger.
func New() *Ledger { panic("unimplemented") }

// CreateAccount registers an account. id: non-empty, at most 128 bytes,
// valid UTF-8. currency: exactly 3 uppercase ASCII letters. Violations →
// ErrBadInput. An id already registered → ErrBadInput (accounts are never
// re-created, updated, or deleted). New accounts start at balance zero.
func (l *Ledger) CreateAccount(id, currency string) error { panic("unimplemented") }

// Post applies a transaction atomically.
//
// Validation order is not specified, but ALL of the following hold on
// success: tx.Key/Memo/Entries satisfy the Tx field contracts
// (ErrBadInput otherwise); every entry's account exists (ErrUnknownAccount)
// and its amount's currency equals that account's currency
// (ErrCurrencyMismatch); per currency, the entries sum to exactly zero
// (ErrUnbalanced); no balance overflows (ErrOverflow).
//
// Idempotency: if tx.Key equals the key of a PREVIOUSLY ACCEPTED Post,
// Post is a no-op that returns the original TxID and a nil error — even if
// the replayed payload differs. Keys of rejected posts are not recorded.
// Post and Void keys share one namespace: reusing an accepted Void's key
// in Post is ErrBadInput.
//
// On success the transaction is appended to the journal with the next Seq
// and every entry's amount is added to its account's balance.
func (l *Ledger) Post(tx Tx) (TxID, error) { panic("unimplemented") }

// Void reverses a posted transaction by appending a compensating journal
// entry (the exact negation of the original entries; Memo of the void
// entry is ""). key is the idempotency key of the VOID operation, same
// field contract as Tx.Key.
//
// Unknown id → ErrUnknownTx. id already voided → ErrAlreadyVoided (a void
// cannot be voided; only posts can). Replaying an accepted void's key →
// no-op, nil error. Reusing an accepted Post's key → ErrBadInput.
func (l *Ledger) Void(id TxID, key string) error { panic("unimplemented") }

// Balance returns the account's current balance (its currency, zero Units
// if never posted to). Unknown account → ErrUnknownAccount.
func (l *Ledger) Balance(account string) (Amount, error) { panic("unimplemented") }

// Journal returns the journal entries with Seq > afterSeq, in ascending
// Seq order. limit > 0 caps the count; limit <= 0 means no cap. The
// returned slices are the caller's: mutating them must not affect the
// ledger.
func (l *Ledger) Journal(afterSeq uint64, limit int) []JournalEntry { panic("unimplemented") }

// Export writes the complete ledger state in the canonical v1 encoding.
// The encoding is byte-deterministic: two ledgers that accepted the same
// operation sequence export identical bytes, and Import followed by Export
// reproduces its input byte-for-byte.
//
// Canonical v1 encoding, line-based, '\n' terminated, UTF-8:
//
//	line 1:               "ledger/v1"
//	one line per account, sorted by account id (byte order):
//	                      "A|<id>|<currency>"
//	one line per journal entry, ascending Seq:
//	                      "T|<seq>|<id>|<key>|<voids>|<memo>|<entries>"
//	<voids> is the voided TxID or "" for posts. <entries> is
//	";"-joined "<account>:<units>:<currency>" in the exact order posted.
//	Every variable-content field — <id>, <key>, <voids>, <memo>, the
//	account id in "A" lines, and account ids inside <entries> — is
//	escaped with this exact table: '\' → "\\", '|' → "\p", ';' → "\s",
//	':' → "\c", newline → "\n". (Seq, units, and currency codes cannot
//	contain reserved bytes and are written raw.) Keys of accepted voids
//	are recorded on their void lines; nothing else is encoded.
//
// Export never fails on a valid ledger; it returns only errors from w.
func (l *Ledger) Export(w io.Writer) error { panic("unimplemented") }

// Import reconstructs a ledger from the canonical v1 encoding. The result
// is equivalent to the original: same accounts, balances, journal,
// idempotency behavior (all recorded keys still dedupe), and its Export is
// byte-identical to the imported bytes.
//
// Any deviation from the canonical encoding — bad header, unknown line
// tag, wrong field count, non-canonical ordering, unparseable numbers,
// entries that do not balance, references to unknown accounts or
// transactions, duplicate seq/ids/keys, trailing garbage, truncation —
// fails with ErrCorruptImport and returns a nil ledger. Errors from r are
// returned as-is.
func Import(r io.Reader) (*Ledger, error) { panic("unimplemented") }
