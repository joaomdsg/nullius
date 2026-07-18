// Visible acceptance test — the DoD floor. The hidden conformance suite is
// far larger; passing this file alone proves only the basic flow.
package accept_test

import (
	"bytes"
	"testing"

	ledger "github.com/go-via/ledger"
)

func amt(t *testing.T, s string) ledger.Amount {
	t.Helper()
	a, err := ledger.ParseAmount(s, "EUR")
	if err != nil {
		t.Fatalf("ParseAmount(%q): %v", s, err)
	}
	return a
}

func TestAcceptBasicFlow(t *testing.T) {
	l := ledger.New()
	for _, id := range []string{"cash", "revenue"} {
		if err := l.CreateAccount(id, "EUR"); err != nil {
			t.Fatalf("CreateAccount(%s): %v", id, err)
		}
	}

	id, err := l.Post(ledger.Tx{Key: "k1", Memo: "sale", Entries: []ledger.Entry{
		{Account: "cash", Amount: amt(t, "10.00")},
		{Account: "revenue", Amount: amt(t, "-10.00")},
	}})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}

	b, err := l.Balance("cash")
	if err != nil || b.Units != 1000 {
		t.Fatalf("Balance(cash) = %+v, %v; want 1000 units", b, err)
	}

	// Idempotent replay returns the original ID.
	id2, err := l.Post(ledger.Tx{Key: "k1", Entries: []ledger.Entry{
		{Account: "cash", Amount: amt(t, "99.99")},
		{Account: "revenue", Amount: amt(t, "-99.99")},
	}})
	if err != nil || id2 != id {
		t.Fatalf("replay = %v, %v; want %v, nil", id2, err, id)
	}
	if b, _ := l.Balance("cash"); b.Units != 1000 {
		t.Fatalf("replay changed balance: %d", b.Units)
	}

	if err := l.Void(id, "v1"); err != nil {
		t.Fatalf("Void: %v", err)
	}
	if b, _ := l.Balance("cash"); b.Units != 0 {
		t.Fatalf("balance after void = %d; want 0", b.Units)
	}
	if j := l.Journal(0, 0); len(j) != 2 || j[1].Voids != id {
		t.Fatalf("journal = %+v; want 2 entries, second voiding %v", j, id)
	}

	var buf bytes.Buffer
	if err := l.Export(&buf); err != nil {
		t.Fatalf("Export: %v", err)
	}
	l2, err := ledger.Import(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	var buf2 bytes.Buffer
	if err := l2.Export(&buf2); err != nil {
		t.Fatalf("re-Export: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), buf2.Bytes()) {
		t.Fatalf("round-trip not byte-identical:\n%q\nvs\n%q", buf.Bytes(), buf2.Bytes())
	}
}
