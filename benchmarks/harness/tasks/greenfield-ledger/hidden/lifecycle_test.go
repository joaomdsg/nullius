// Invariant group: lifecycle-legality. Traces to the Void, Journal,
// CreateAccount, and atomicity clauses.
package hidden

import (
	"errors"
	"math"
	"strings"
	"testing"

	ledger "github.com/go-via/ledger"
)

func TestVoid_Lifecycle(t *testing.T) {
	l := newLedger(t)
	id := mustPost(t, l, "P", ledger.Entry{Account: "e1", Amount: units(700, "EUR")},
		ledger.Entry{Account: "e3", Amount: units(-700, "EUR")})

	if err := l.Void(id, "V"); err != nil {
		t.Fatalf("Void: %v", err)
	}
	if got := balanceUnits(t, l, "e1"); got != 0 {
		t.Fatalf("balance after void = %d; want 0", got)
	}
	j := l.Journal(0, 0)
	if len(j) != 2 {
		t.Fatalf("journal length = %d; want 2 (append-only compensation, no deletion)", len(j))
	}
	v := j[1]
	if v.Voids != id || v.Memo != "" || v.Seq != 2 || v.ID != ledger.TxID("tx-2") {
		t.Fatalf("void entry = %+v; want Voids=%v Memo=\"\" Seq=2 ID=tx-2", v, id)
	}
	// Compensation is the EXACT negation, same order.
	if len(v.Entries) != 2 || v.Entries[0].Account != "e1" || v.Entries[0].Amount.Units != -700 ||
		v.Entries[1].Account != "e3" || v.Entries[1].Amount.Units != 700 {
		t.Fatalf("compensating entries = %+v", v.Entries)
	}
	// The original journal entry is untouched.
	if j[0].ID != id || j[0].Entries[0].Amount.Units != 700 {
		t.Fatalf("original entry mutated: %+v", j[0])
	}

	before := snapshot(t, l)
	if err := l.Void(id, "V2"); !errors.Is(err, ledger.ErrAlreadyVoided) {
		t.Fatalf("double void err = %v; want ErrAlreadyVoided", err)
	}
	wantUnchanged(t, l, before, "double void")
	// A void cannot be voided: its TxID is not voidable.
	if err := l.Void(ledger.TxID("tx-2"), "V3"); !errors.Is(err, ledger.ErrAlreadyVoided) && !errors.Is(err, ledger.ErrUnknownTx) {
		t.Fatalf("void-of-void err = %v; want ErrAlreadyVoided or ErrUnknownTx per contract (only posts can be voided)", err)
	}
	if err := l.Void(ledger.TxID("tx-99"), "V4"); !errors.Is(err, ledger.ErrUnknownTx) {
		t.Fatalf("unknown tx err = %v; want ErrUnknownTx", err)
	}
}

func TestJournal_SeqAndPagination(t *testing.T) {
	l := newLedger(t)
	for i := 0; i < 5; i++ {
		mustPost(t, l, "k"+strings.Repeat("x", i+1), pair(int64(i+1))...)
	}
	all := l.Journal(0, 0)
	if len(all) != 5 {
		t.Fatalf("Journal(0,0) len = %d; want 5", len(all))
	}
	for i, e := range all {
		if e.Seq != uint64(i+1) || e.ID != ledger.TxID("tx-"+string(rune('1'+i))) {
			t.Fatalf("entry %d: Seq=%d ID=%s; want Seq=%d ID=tx-%d", i, e.Seq, e.ID, i+1, i+1)
		}
	}
	if got := l.Journal(2, 2); len(got) != 2 || got[0].Seq != 3 || got[1].Seq != 4 {
		t.Fatalf("Journal(2,2) = %+v; want Seq 3,4", got)
	}
	if got := l.Journal(5, 0); len(got) != 0 {
		t.Fatalf("Journal(5,0) len = %d; want 0", len(got))
	}
	if got := l.Journal(0, -3); len(got) != 5 {
		t.Fatalf("Journal(0,-3) len = %d; want 5 (limit<=0 = no cap)", len(got))
	}
	// Returned slices are the caller's: mutation must not leak in.
	got := l.Journal(0, 1)
	got[0].Entries[0].Amount.Units = 424242
	if fresh := l.Journal(0, 1); fresh[0].Entries[0].Amount.Units == 424242 {
		t.Fatal("Journal returns aliased internal state")
	}
}

func TestCreateAccount_Contract(t *testing.T) {
	l := ledger.New()
	if err := l.CreateAccount("a", "EUR"); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if b, err := l.Balance("a"); err != nil || b.Units != 0 || b.Currency != "EUR" {
		t.Fatalf("new account balance = %+v, %v; want 0 EUR", b, err)
	}
	if err := l.CreateAccount("a", "EUR"); !errors.Is(err, ledger.ErrBadInput) {
		t.Fatalf("duplicate account err = %v; want ErrBadInput", err)
	}
	bad := []struct{ id, cur string }{
		{"", "EUR"}, {strings.Repeat("a", 129), "EUR"}, {"b\xff", "EUR"},
		{"b", "eur"}, {"b", "EU"}, {"b", "EURO"}, {"b", "EU1"},
	}
	for _, c := range bad {
		if err := l.CreateAccount(c.id, c.cur); !errors.Is(err, ledger.ErrBadInput) {
			t.Errorf("CreateAccount(%q,%q) err = %v; want ErrBadInput", c.id, c.cur, err)
		}
	}
	if _, err := l.Balance("nope"); !errors.Is(err, ledger.ErrUnknownAccount) {
		t.Fatalf("Balance(unknown) err = %v; want ErrUnknownAccount", err)
	}
}

func TestAtomicity_ErrorsChangeNothing(t *testing.T) {
	l := newLedger(t)
	mustPost(t, l, "seed", pair(100)...)
	before := snapshot(t, l)

	fails := []struct {
		name string
		op   func() error
	}{
		{"unknown account", func() error {
			_, err := l.Post(ledger.Tx{Key: "f1", Entries: []ledger.Entry{
				{Account: "ghost", Amount: units(1, "EUR")},
				{Account: "e1", Amount: units(-1, "EUR")}}})
			return err
		}},
		{"currency mismatch", func() error {
			_, err := l.Post(ledger.Tx{Key: "f2", Entries: []ledger.Entry{
				{Account: "e1", Amount: units(1, "USD")},
				{Account: "u1", Amount: units(-1, "USD")}}})
			return err
		}},
		{"unbalanced", func() error {
			_, err := l.Post(ledger.Tx{Key: "f3", Entries: []ledger.Entry{
				{Account: "e1", Amount: units(2, "EUR")},
				{Account: "e2", Amount: units(-1, "EUR")}}})
			return err
		}},
		{"cross-currency pair balanced per-currency is fine, so force imbalance in ONE currency", func() error {
			_, err := l.Post(ledger.Tx{Key: "f4", Entries: []ledger.Entry{
				{Account: "e1", Amount: units(5, "EUR")},
				{Account: "e2", Amount: units(-5, "EUR")},
				{Account: "u1", Amount: units(3, "USD")}}})
			return err
		}},
		{"void unknown", func() error { return l.Void("tx-77", "f5") }},
	}
	for _, f := range fails {
		if err := f.op(); err == nil {
			t.Fatalf("%s: expected error", f.name)
		}
		wantUnchanged(t, l, before, f.name)
	}
}

func TestPost_BalanceOverflowAtomic(t *testing.T) {
	l := newLedger(t)
	mustPost(t, l, "max",
		ledger.Entry{Account: "e1", Amount: units(math.MaxInt64, "EUR")},
		ledger.Entry{Account: "e2", Amount: units(-math.MaxInt64, "EUR")})
	before := snapshot(t, l)
	_, err := l.Post(ledger.Tx{Key: "over", Entries: pair(1)})
	if !errors.Is(err, ledger.ErrOverflow) {
		t.Fatalf("balance overflow err = %v; want ErrOverflow", err)
	}
	wantUnchanged(t, l, before, "overflowing post")
	// The rejected key must remain usable.
	if _, err := l.Post(ledger.Tx{Key: "over", Entries: pair(-1)}); err != nil {
		t.Fatalf("key of overflow-rejected post was recorded: %v", err)
	}
}
