// Hidden conformance suite for greenfield-ledger. Black-box: only the
// exported API is used. Every assertion traces to a sentence in the
// ledger.go doc-comment contract. Fully deterministic — no sleeps, no
// timing assumptions; seeded PRNGs only.
package hidden

import (
	"bytes"
	"testing"

	ledger "github.com/go-via/ledger"
)

func amt(t *testing.T, s, cur string) ledger.Amount {
	t.Helper()
	a, err := ledger.ParseAmount(s, cur)
	if err != nil {
		t.Fatalf("ParseAmount(%q,%q): %v", s, cur, err)
	}
	return a
}

func units(u int64, cur string) ledger.Amount { return ledger.Amount{Units: u, Currency: cur} }

// newLedger builds a ledger with standard test accounts:
// EUR: e1 e2 e3; USD: u1 u2.
func newLedger(t *testing.T) *ledger.Ledger {
	t.Helper()
	l := ledger.New()
	for _, a := range []struct{ id, cur string }{
		{"e1", "EUR"}, {"e2", "EUR"}, {"e3", "EUR"}, {"u1", "USD"}, {"u2", "USD"},
	} {
		if err := l.CreateAccount(a.id, a.cur); err != nil {
			t.Fatalf("CreateAccount(%s): %v", a.id, err)
		}
	}
	return l
}

func mustPost(t *testing.T, l *ledger.Ledger, key string, entries ...ledger.Entry) ledger.TxID {
	t.Helper()
	id, err := l.Post(ledger.Tx{Key: key, Entries: entries})
	if err != nil {
		t.Fatalf("Post(%s): %v", key, err)
	}
	return id
}

// pair is the canonical balanced two-entry transfer u units e1 -> e2.
func pair(u int64) []ledger.Entry {
	return []ledger.Entry{
		{Account: "e1", Amount: units(u, "EUR")},
		{Account: "e2", Amount: units(-u, "EUR")},
	}
}

// snapshot captures full observable state via the canonical export.
// Contract: Export is byte-deterministic, so state equality == byte equality.
func snapshot(t *testing.T, l *ledger.Ledger) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := l.Export(&b); err != nil {
		t.Fatalf("Export: %v", err)
	}
	return b.Bytes()
}

// wantUnchanged asserts a failing call changed nothing (atomicity clause).
func wantUnchanged(t *testing.T, l *ledger.Ledger, before []byte, op string) {
	t.Helper()
	if after := snapshot(t, l); !bytes.Equal(before, after) {
		t.Fatalf("%s: failing call mutated state\nbefore: %q\nafter:  %q", op, before, after)
	}
}

func balanceUnits(t *testing.T, l *ledger.Ledger, account string) int64 {
	t.Helper()
	b, err := l.Balance(account)
	if err != nil {
		t.Fatalf("Balance(%s): %v", account, err)
	}
	return b.Units
}
