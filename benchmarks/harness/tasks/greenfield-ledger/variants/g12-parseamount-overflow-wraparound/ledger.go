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
package ledger

import (
	"bufio"
	"errors"
	"io"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
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

const maxUnits = int64(math.MaxInt64)
const minUnits = -maxUnits

func validCurrency(c string) bool {
	if len(c) != 3 {
		return false
	}
	for i := 0; i < 3; i++ {
		if c[i] < 'A' || c[i] > 'Z' {
			return false
		}
	}
	return true
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
func ParseAmount(s, currency string) (Amount, error) {
	if !validCurrency(currency) {
		return Amount{}, ErrBadInput
	}
	if s == "" {
		return Amount{}, ErrBadInput
	}
	neg := false
	i := 0
	if s[0] == '-' {
		neg = true
		i = 1
	}
	digitsStart := i
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == digitsStart {
		// no digits in integer part
		return Amount{}, ErrBadInput
	}
	intPart := s[digitsStart:i]
	fracPart := "00"
	if i < len(s) {
		if s[i] != '.' {
			return Amount{}, ErrBadInput
		}
		i++
		fracStart := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i-fracStart != 2 {
			return Amount{}, ErrBadInput
		}
		fracPart = s[fracStart:i]
	}
	if i != len(s) {
		return Amount{}, ErrBadInput
	}

	// Combine into big integer of minor units, then range-check.
	big1 := new(big.Int)
	if _, ok := big1.SetString(intPart, 10); !ok {
		return Amount{}, ErrBadInput
	}
	hundred := big.NewInt(100)
	big1.Mul(big1, hundred)
	fracVal, ok := new(big.Int).SetString(fracPart, 10)
	if !ok {
		return Amount{}, ErrBadInput
	}
	big1.Add(big1, fracVal)
	if neg {
		big1.Neg(big1)
	}

	return Amount{Units: big1.Int64(), Currency: currency}, nil
}

// String renders the amount in the same grammar ParseAmount accepts:
// minus sign for negative values, integer part with no leading zeros
// (except "0"), '.', exactly two digits. "-0.00" is rendered "0.00".
// ParseAmount(a.String(), a.Currency) round-trips every valid Amount.
func (a Amount) String() string {
	neg := a.Units < 0
	u := a.Units
	if neg {
		u = -u
	}
	intPart := u / 100
	frac := u % 100
	var b strings.Builder
	if neg && u != 0 {
		b.WriteByte('-')
	}
	b.WriteString(strconv.FormatInt(intPart, 10))
	b.WriteByte('.')
	if frac < 10 {
		b.WriteByte('0')
	}
	b.WriteString(strconv.FormatInt(frac, 10))
	return b.String()
}

// Add returns a+b. ErrCurrencyMismatch if currencies differ; ErrOverflow
// if the result leaves the valid range.
func (a Amount) Add(b Amount) (Amount, error) {
	if a.Currency != b.Currency {
		return Amount{}, ErrCurrencyMismatch
	}
	sum := a.Units + b.Units
	// overflow check via big to be exact and simple
	bigSum := new(big.Int).Add(big.NewInt(a.Units), big.NewInt(b.Units))
	if bigSum.Cmp(big.NewInt(minUnits)) < 0 || bigSum.Cmp(big.NewInt(maxUnits)) > 0 {
		return Amount{}, ErrOverflow
	}
	return Amount{Units: sum, Currency: a.Currency}, nil
}

// Neg returns the amount negated. Always valid: MinInt64 never occurs.
func (a Amount) Neg() Amount {
	return Amount{Units: -a.Units, Currency: a.Currency}
}

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
func Allocate(a Amount, weights []int) ([]Amount, error) {
	n := len(weights)
	if n == 0 {
		return nil, ErrBadInput
	}
	wSum := new(big.Int)
	anyPositive := false
	for _, w := range weights {
		if w < 0 {
			return nil, ErrBadInput
		}
		if w > 0 {
			anyPositive = true
		}
		wSum.Add(wSum, big.NewInt(int64(w)))
	}
	if !anyPositive {
		return nil, ErrBadInput
	}

	aBig := big.NewInt(a.Units)
	parts := make([]int64, n)
	rems := make([]*big.Int, n) // signed remainder (sign of dividend, per Go's QuoRem)

	for i, w := range weights {
		if w == 0 {
			parts[i] = 0
			rems[i] = big.NewInt(0)
			continue
		}
		prod := new(big.Int).Mul(aBig, big.NewInt(int64(w)))
		q, r := new(big.Int), new(big.Int)
		q.QuoRem(prod, wSum, r) // truncated toward zero
		parts[i] = q.Int64()
		rems[i] = r
	}

	sumParts := new(big.Int)
	for _, p := range parts {
		sumParts.Add(sumParts, big.NewInt(p))
	}
	leftover := new(big.Int).Sub(aBig, sumParts)
	leftoverN := leftover.Int64()
	need := leftoverN
	sign := int64(1)
	if a.Units < 0 {
		sign = -1
	}
	if need < 0 {
		need = -need
	}

	type ir struct {
		idx int
		abs *big.Int
	}
	order := make([]ir, n)
	for i := range weights {
		order[i] = ir{i, new(big.Int).Abs(rems[i])}
	}
	sort.SliceStable(order, func(i, j int) bool {
		c := order[i].abs.Cmp(order[j].abs)
		if c != 0 {
			return c > 0
		}
		return order[i].idx < order[j].idx
	})

	for k := int64(0); k < need; k++ {
		idx := order[k].idx
		parts[idx] += sign
	}

	result := make([]Amount, n)
	for i, w := range weights {
		if w == 0 {
			result[i] = Amount{Units: 0, Currency: a.Currency}
		} else {
			result[i] = Amount{Units: parts[i], Currency: a.Currency}
		}
	}
	return result, nil
}

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

// txRecord is the internal bookkeeping for a journal entry.
type txRecord struct {
	entry  JournalEntry
	isVoid bool
	voided bool // only meaningful for posts
}

// keyKind distinguishes what an idempotency key was used for.
type keyKind int

const (
	keyKindPost keyKind = iota
	keyKindVoid
)

type keyRecord struct {
	kind keyKind
	txID TxID // for posts: the resulting TxID
}

// account is internal per-account state.
type account struct {
	currency string
	balance  int64
}

// Ledger is the accounting core. The zero value is not usable; call New.
type Ledger struct {
	accounts map[string]*account
	journal  []JournalEntry
	byID     map[TxID]*txRecord
	keys     map[string]keyRecord
	seq      uint64
}

// New returns an empty ledger.
func New() *Ledger {
	return &Ledger{
		accounts: make(map[string]*account),
		byID:     make(map[TxID]*txRecord),
		keys:     make(map[string]keyRecord),
	}
}

func validID(id string) bool {
	return id != "" && len(id) <= 128 && utf8.ValidString(id)
}

func validKey(k string) bool {
	return k != "" && len(k) <= 128 && utf8.ValidString(k)
}

func validMemo(m string) bool {
	return len(m) <= 500 && utf8.ValidString(m)
}

// CreateAccount registers an account. id: non-empty, at most 128 bytes,
// valid UTF-8. currency: exactly 3 uppercase ASCII letters. Violations →
// ErrBadInput. An id already registered → ErrBadInput (accounts are never
// re-created, updated, or deleted). New accounts start at balance zero.
func (l *Ledger) CreateAccount(id, currency string) error {
	if !validID(id) || !validCurrency(currency) {
		return ErrBadInput
	}
	if _, exists := l.accounts[id]; exists {
		return ErrBadInput
	}
	l.accounts[id] = &account{currency: currency, balance: 0}
	return nil
}

// validateTxFields checks the Tx field contract (Key/Memo/Entries shape),
// independent of ledger state.
func validateTxFields(tx Tx) error {
	if !validKey(tx.Key) {
		return ErrBadInput
	}
	if !validMemo(tx.Memo) {
		return ErrBadInput
	}
	if len(tx.Entries) < 2 {
		return ErrBadInput
	}
	for _, e := range tx.Entries {
		if e.Amount.Units == 0 {
			return ErrBadInput
		}
		if e.Amount.Units == minUnitsInt64Min() {
			return ErrBadInput
		}
	}
	return nil
}

func minUnitsInt64Min() int64 {
	return math.MinInt64
}

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
func (l *Ledger) Post(tx Tx) (TxID, error) {
	if err := validateTxFields(tx); err != nil {
		return "", err
	}

	if kr, ok := l.keys[tx.Key]; ok {
		if kr.kind == keyKindPost {
			return kr.txID, nil
		}
		return "", ErrBadInput // key belongs to an accepted void
	}

	// Validate accounts/currency and check per-currency balance.
	sums := make(map[string]*big.Int)
	for _, e := range tx.Entries {
		acct, ok := l.accounts[e.Account]
		if !ok {
			return "", ErrUnknownAccount
		}
		if acct.currency != e.Amount.Currency {
			return "", ErrCurrencyMismatch
		}
		if e.Amount.Units == 0 {
			return "", ErrBadInput
		}
		cur := e.Amount.Currency
		if sums[cur] == nil {
			sums[cur] = new(big.Int)
		}
		sums[cur].Add(sums[cur], big.NewInt(e.Amount.Units))
	}
	for _, s := range sums {
		if s.Sign() != 0 {
			return "", ErrUnbalanced
		}
	}

	// Compute new balances exactly with big.Int to detect overflow.
	newBalances := make(map[string]int64)
	seen := make(map[string]bool)
	for _, e := range tx.Entries {
		if seen[e.Account] {
			continue
		}
		seen[e.Account] = true
		acct := l.accounts[e.Account]
		total := new(big.Int).SetInt64(acct.balance)
		for _, e2 := range tx.Entries {
			if e2.Account == e.Account {
				total.Add(total, big.NewInt(e2.Amount.Units))
			}
		}
		if total.Cmp(big.NewInt(minUnits)) < 0 || total.Cmp(big.NewInt(maxUnits)) > 0 {
			return "", ErrOverflow
		}
		newBalances[e.Account] = total.Int64()
	}

	// Commit.
	l.seq++
	id := TxID("tx-" + strconv.FormatUint(l.seq, 10))
	entriesCopy := append([]Entry(nil), tx.Entries...)
	je := JournalEntry{ID: id, Seq: l.seq, Memo: tx.Memo, Entries: entriesCopy, Voids: ""}
	l.journal = append(l.journal, je)
	l.byID[id] = &txRecord{entry: je, isVoid: false, voided: false}
	l.keys[tx.Key] = keyRecord{kind: keyKindPost, txID: id}
	for acctID, bal := range newBalances {
		l.accounts[acctID].balance = bal
	}
	return id, nil
}

// Void reverses a posted transaction by appending a compensating journal
// entry (the exact negation of the original entries; Memo of the void
// entry is ""). key is the idempotency key of the VOID operation, same
// field contract as Tx.Key.
//
// Unknown id → ErrUnknownTx. id already voided → ErrAlreadyVoided (a void
// cannot be voided; only posts can). Replaying an accepted void's key →
// no-op, nil error. Reusing an accepted Post's key → ErrBadInput.
func (l *Ledger) Void(id TxID, key string) error {
	if !validKey(key) {
		return ErrBadInput
	}

	if kr, ok := l.keys[key]; ok {
		if kr.kind == keyKindVoid {
			return nil
		}
		return ErrBadInput // key belongs to an accepted post
	}

	rec, ok := l.byID[id]
	if !ok {
		return ErrUnknownTx
	}
	if rec.isVoid || rec.voided {
		return ErrAlreadyVoided
	}

	// Compute negated entries and new balances (no float/overflow possible:
	// negation of an in-range Units value is always in range).
	negEntries := make([]Entry, len(rec.entry.Entries))
	newBalances := make(map[string]int64)
	seen := make(map[string]bool)
	for i, e := range rec.entry.Entries {
		negEntries[i] = Entry{Account: e.Account, Amount: e.Amount.Neg()}
	}
	for _, e := range negEntries {
		if seen[e.Account] {
			continue
		}
		seen[e.Account] = true
		acct := l.accounts[e.Account]
		total := new(big.Int).SetInt64(acct.balance)
		for _, e2 := range negEntries {
			if e2.Account == e.Account {
				total.Add(total, big.NewInt(e2.Amount.Units))
			}
		}
		if total.Cmp(big.NewInt(minUnits)) < 0 || total.Cmp(big.NewInt(maxUnits)) > 0 {
			return ErrOverflow
		}
		newBalances[e.Account] = total.Int64()
	}

	l.seq++
	voidID := TxID("tx-" + strconv.FormatUint(l.seq, 10))
	je := JournalEntry{ID: voidID, Seq: l.seq, Memo: "", Entries: negEntries, Voids: id}
	l.journal = append(l.journal, je)
	l.byID[voidID] = &txRecord{entry: je, isVoid: true}
	rec.voided = true
	l.keys[key] = keyRecord{kind: keyKindVoid, txID: voidID}
	for acctID, bal := range newBalances {
		l.accounts[acctID].balance = bal
	}
	return nil
}

// Balance returns the account's current balance (its currency, zero Units
// if never posted to). Unknown account → ErrUnknownAccount.
func (l *Ledger) Balance(account string) (Amount, error) {
	acct, ok := l.accounts[account]
	if !ok {
		return Amount{}, ErrUnknownAccount
	}
	return Amount{Units: acct.balance, Currency: acct.currency}, nil
}

// Journal returns the journal entries with Seq > afterSeq, in ascending
// Seq order. limit > 0 caps the count; limit <= 0 means no cap. The
// returned slices are the caller's: mutating them must not affect the
// ledger.
func (l *Ledger) Journal(afterSeq uint64, limit int) []JournalEntry {
	var out []JournalEntry
	for _, je := range l.journal {
		if je.Seq <= afterSeq {
			continue
		}
		out = append(out, copyJournalEntry(je))
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func copyJournalEntry(je JournalEntry) JournalEntry {
	je.Entries = append([]Entry(nil), je.Entries...)
	return je
}

// ---- v1 canonical encoding --------------------------------------------

func escapeField(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			b.WriteString(`\\`)
		case '|':
			b.WriteString(`\p`)
		case ';':
			b.WriteString(`\s`)
		case ':':
			b.WriteString(`\c`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// unescapeField reverses escapeField. Returns ok=false on any invalid
// escape sequence or any raw occurrence of a character that the canonical
// encoding always escapes.
func unescapeField(s string) (string, bool) {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		c := s[i]
		if c == '\\' {
			if i+1 >= len(s) {
				return "", false
			}
			switch s[i+1] {
			case '\\':
				b.WriteByte('\\')
			case 'p':
				b.WriteByte('|')
			case 's':
				b.WriteByte(';')
			case 'c':
				b.WriteByte(':')
			case 'n':
				b.WriteByte('\n')
			default:
				return "", false
			}
			i += 2
			continue
		}
		if c == '|' || c == ';' || c == ':' || c == '\n' {
			return "", false
		}
		b.WriteByte(c)
		i++
	}
	return b.String(), true
}

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
//	<key>, <memo>, and account ids inside <entries> are escaped: '\'
//	→ "\\", '|' → "\p", ';' → "\s", ':' → "\c", '\n' → "\n". Keys of
//	accepted voids are recorded on their void lines; nothing else is
//	encoded.
//
// Export never fails on a valid ledger; it returns only errors from w.
func (l *Ledger) Export(w io.Writer) error {
	bw := bufio.NewWriter(w)
	if _, err := bw.WriteString("ledger/v1\n"); err != nil {
		return err
	}

	ids := make([]string, 0, len(l.accounts))
	for id := range l.accounts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		acct := l.accounts[id]
		line := "A|" + escapeField(id) + "|" + acct.currency + "\n"
		if _, err := bw.WriteString(line); err != nil {
			return err
		}
	}

	// key by txID for the key that produced each journal entry.
	keyByTxID := make(map[TxID]string, len(l.keys))
	for k, kr := range l.keys {
		keyByTxID[kr.txID] = k
	}

	for _, je := range l.journal {
		var entriesParts []string
		for _, e := range je.Entries {
			entriesParts = append(entriesParts, escapeField(e.Account)+":"+strconv.FormatInt(e.Amount.Units, 10)+":"+e.Amount.Currency)
		}
		key := keyByTxID[je.ID]
		line := "T|" + strconv.FormatUint(je.Seq, 10) + "|" + escapeField(string(je.ID)) + "|" +
			escapeField(key) + "|" + escapeField(string(je.Voids)) + "|" + escapeField(je.Memo) + "|" +
			strings.Join(entriesParts, ";") + "\n"
		if _, err := bw.WriteString(line); err != nil {
			return err
		}
	}

	return bw.Flush()
}

// splitEscaped splits s on raw (unescaped) occurrences of sep, which must
// be one of the characters the canonical encoding escapes. Because every
// literal occurrence of sep in a field is guaranteed escaped, a plain
// byte-wise scan for an un-backslash-preceded sep is a safe and correct
// tokenizer; consumers still run unescapeField on each token to validate
// and decode it.
func splitEscaped(s string, sep byte) []string {
	var out []string
	start := 0
	i := 0
	for i < len(s) {
		if s[i] == '\\' && i+1 < len(s) {
			i += 2
			continue
		}
		if s[i] == sep {
			out = append(out, s[start:i])
			start = i + 1
			i++
			continue
		}
		i++
	}
	out = append(out, s[start:])
	return out
}

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
func Import(r io.Reader) (*Ledger, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		return nil, ErrCorruptImport
	}
	text := string(data[:len(data)-1]) // drop the final '\n'
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || lines[0] != "ledger/v1" {
		return nil, ErrCorruptImport
	}

	l := New()

	idx := 1
	lastAcctID := ""
	haveLastAcct := false
	for idx < len(lines) {
		fields := splitEscaped(lines[idx], '|')
		if len(fields) == 0 {
			return nil, ErrCorruptImport
		}
		if fields[0] != "A" {
			break
		}
		if len(fields) != 3 {
			return nil, ErrCorruptImport
		}
		id, ok := unescapeField(fields[1])
		if !ok {
			return nil, ErrCorruptImport
		}
		currency := fields[2]
		if !validID(id) || !validCurrency(currency) {
			return nil, ErrCorruptImport
		}
		if haveLastAcct && id <= lastAcctID {
			return nil, ErrCorruptImport // not strictly sorted -> non-canonical or duplicate
		}
		if _, exists := l.accounts[id]; exists {
			return nil, ErrCorruptImport
		}
		l.accounts[id] = &account{currency: currency, balance: 0}
		lastAcctID = id
		haveLastAcct = true
		idx++
	}

	var expectSeq uint64 = 1
	for idx < len(lines) {
		line := lines[idx]
		fields := splitEscaped(line, '|')
		if len(fields) == 0 || fields[0] != "T" {
			return nil, ErrCorruptImport
		}
		if len(fields) != 7 {
			return nil, ErrCorruptImport
		}
		seqStr, idStr, keyStr, voidsStr, memoStr, entriesStr := fields[1], fields[2], fields[3], fields[4], fields[5], fields[6]

		seq, serr := strconv.ParseUint(seqStr, 10, 64)
		if serr != nil {
			return nil, ErrCorruptImport
		}
		if seq != expectSeq {
			return nil, ErrCorruptImport
		}

		idDec, ok := unescapeField(idStr)
		if !ok {
			return nil, ErrCorruptImport
		}
		txID := TxID(idDec)
		if txID != TxID("tx-"+strconv.FormatUint(seq, 10)) {
			return nil, ErrCorruptImport
		}
		if _, exists := l.byID[txID]; exists {
			return nil, ErrCorruptImport
		}

		key, ok := unescapeField(keyStr)
		if !ok || !validKey(key) {
			return nil, ErrCorruptImport
		}
		if _, exists := l.keys[key]; exists {
			return nil, ErrCorruptImport
		}

		voidsDec, ok := unescapeField(voidsStr)
		if !ok {
			return nil, ErrCorruptImport
		}
		voids := TxID(voidsDec)

		memo, ok := unescapeField(memoStr)
		if !ok || !validMemo(memo) {
			return nil, ErrCorruptImport
		}

		var entryTokens []string
		if entriesStr != "" {
			entryTokens = splitEscaped(entriesStr, ';')
		}
		if len(entryTokens) < 2 {
			return nil, ErrCorruptImport
		}
		entries := make([]Entry, 0, len(entryTokens))
		for _, tok := range entryTokens {
			sub := splitEscaped(tok, ':')
			if len(sub) != 3 {
				return nil, ErrCorruptImport
			}
			acctID, ok := unescapeField(sub[0])
			if !ok || !validID(acctID) {
				return nil, ErrCorruptImport
			}
			unitsVal, uerr := strconv.ParseInt(sub[1], 10, 64)
			if uerr != nil {
				return nil, ErrCorruptImport
			}
			if unitsVal == 0 || unitsVal == minUnitsInt64Min() {
				return nil, ErrCorruptImport
			}
			currency := sub[2]
			if !validCurrency(currency) {
				return nil, ErrCorruptImport
			}
			acct, exists := l.accounts[acctID]
			if !exists {
				return nil, ErrCorruptImport
			}
			if acct.currency != currency {
				return nil, ErrCorruptImport
			}
			entries = append(entries, Entry{Account: acctID, Amount: Amount{Units: unitsVal, Currency: currency}})
		}

		// balance check per currency
		sums := make(map[string]*big.Int)
		for _, e := range entries {
			if sums[e.Amount.Currency] == nil {
				sums[e.Amount.Currency] = new(big.Int)
			}
			sums[e.Amount.Currency].Add(sums[e.Amount.Currency], big.NewInt(e.Amount.Units))
		}
		for _, s := range sums {
			if s.Sign() != 0 {
				return nil, ErrCorruptImport
			}
		}

		if voids == "" {
			// Post-like entry.
			if memo != "" && len(memo) > 500 {
				return nil, ErrCorruptImport
			}
			newBalances, overflowed := applyEntries(l, entries)
			if overflowed {
				return nil, ErrCorruptImport
			}
			je := JournalEntry{ID: txID, Seq: seq, Memo: memo, Entries: entries, Voids: ""}
			l.journal = append(l.journal, je)
			l.byID[txID] = &txRecord{entry: je, isVoid: false, voided: false}
			l.keys[key] = keyRecord{kind: keyKindPost, txID: txID}
			for a, b := range newBalances {
				l.accounts[a].balance = b
			}
		} else {
			if memo != "" {
				return nil, ErrCorruptImport
			}
			target, exists := l.byID[voids]
			if !exists {
				return nil, ErrCorruptImport
			}
			if target.isVoid || target.voided {
				return nil, ErrCorruptImport
			}
			// entries must be exact negation of target's entries, same order.
			if len(entries) != len(target.entry.Entries) {
				return nil, ErrCorruptImport
			}
			for i, e := range entries {
				want := target.entry.Entries[i]
				if e.Account != want.Account || e.Amount.Currency != want.Amount.Currency || e.Amount.Units != -want.Amount.Units {
					return nil, ErrCorruptImport
				}
			}
			newBalances, overflowed := applyEntries(l, entries)
			if overflowed {
				return nil, ErrCorruptImport
			}
			je := JournalEntry{ID: txID, Seq: seq, Memo: "", Entries: entries, Voids: voids}
			l.journal = append(l.journal, je)
			l.byID[txID] = &txRecord{entry: je, isVoid: true}
			target.voided = true
			l.keys[key] = keyRecord{kind: keyKindVoid, txID: txID}
			for a, b := range newBalances {
				l.accounts[a].balance = b
			}
		}

		expectSeq++
		idx++
	}

	if idx != len(lines) {
		return nil, ErrCorruptImport
	}
	l.seq = expectSeq - 1
	return l, nil
}

// applyEntries computes the resulting balances after adding entries to the
// current ledger state, without mutating it. overflowed=true means the
// caller must treat this as corrupt/invalid.
func applyEntries(l *Ledger, entries []Entry) (map[string]int64, bool) {
	newBalances := make(map[string]int64)
	seen := make(map[string]bool)
	for _, e := range entries {
		if seen[e.Account] {
			continue
		}
		seen[e.Account] = true
		acct := l.accounts[e.Account]
		total := new(big.Int).SetInt64(acct.balance)
		for _, e2 := range entries {
			if e2.Account == e.Account {
				total.Add(total, big.NewInt(e2.Amount.Units))
			}
		}
		if total.Cmp(big.NewInt(minUnits)) < 0 || total.Cmp(big.NewInt(maxUnits)) > 0 {
			return nil, true
		}
		newBalances[e.Account] = total.Int64()
	}
	return newBalances, false
}
