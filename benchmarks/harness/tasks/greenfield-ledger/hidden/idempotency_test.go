// Invariant group: idempotent-replay. Traces to the Post and Void
// idempotency clauses.
package hidden

import (
	"errors"
	"strings"
	"testing"

	ledger "github.com/go-via/ledger"
)

func TestPost_ReplayReturnsOriginal(t *testing.T) {
	l := newLedger(t)
	orig := mustPost(t, l, "K", pair(1000)...)
	before := snapshot(t, l)

	// Identical replay.
	id, err := l.Post(ledger.Tx{Key: "K", Entries: pair(1000)})
	if err != nil || id != orig {
		t.Fatalf("identical replay = %v, %v; want %v, nil", id, err, orig)
	}
	// Divergent payload replay: STILL the original result, nil error.
	id, err = l.Post(ledger.Tx{Key: "K", Memo: "different", Entries: pair(999999)})
	if err != nil || id != orig {
		t.Fatalf("divergent replay = %v, %v; want %v, nil", id, err, orig)
	}
	wantUnchanged(t, l, before, "replays")
	if got := balanceUnits(t, l, "e1"); got != 1000 {
		t.Fatalf("balance after replays = %d; want 1000 (applied exactly once)", got)
	}
	if j := l.Journal(0, 0); len(j) != 1 {
		t.Fatalf("journal has %d entries after replays; want 1", len(j))
	}
}

func TestPost_RejectedKeyNotRecorded(t *testing.T) {
	l := newLedger(t)
	// Unbalanced post with key R is rejected...
	_, err := l.Post(ledger.Tx{Key: "R", Entries: []ledger.Entry{
		{Account: "e1", Amount: units(5, "EUR")},
		{Account: "e2", Amount: units(-4, "EUR")},
	}})
	if !errors.Is(err, ledger.ErrUnbalanced) {
		t.Fatalf("unbalanced err = %v; want ErrUnbalanced", err)
	}
	// ...so key R must be free for a subsequent valid post — and that post
	// must actually APPLY (a stale recorded TxID returned with nil error is
	// exactly the bug this test exists to catch).
	id, err := l.Post(ledger.Tx{Key: "R", Entries: pair(100)})
	if err != nil {
		t.Fatalf("key of a REJECTED post was recorded: %v", err)
	}
	if got := balanceUnits(t, l, "e1"); got != 100 {
		t.Fatalf("post after rejected key did not apply: e1 = %d; want 100", got)
	}
	j := l.Journal(0, 0)
	if len(j) != 1 || j[0].ID != id {
		t.Fatalf("journal after reused key = %+v; want exactly the applied tx %v", j, id)
	}
}

func TestVoid_ReplayAndKeyNamespace(t *testing.T) {
	l := newLedger(t)
	id := mustPost(t, l, "P", pair(500)...)
	if err := l.Void(id, "V"); err != nil {
		t.Fatalf("Void: %v", err)
	}
	before := snapshot(t, l)
	// Replaying the accepted void key: no-op, nil error (NOT ErrAlreadyVoided).
	if err := l.Void(id, "V"); err != nil {
		t.Fatalf("void replay err = %v; want nil no-op", err)
	}
	wantUnchanged(t, l, before, "void replay")

	// One key namespace: a Post reusing accepted void key V → ErrBadInput.
	if _, err := l.Post(ledger.Tx{Key: "V", Entries: pair(1)}); !errors.Is(err, ledger.ErrBadInput) {
		t.Fatalf("Post with void's key err = %v; want ErrBadInput", err)
	}
	// And a Void reusing accepted post key P → ErrBadInput.
	id2 := mustPost(t, l, "P2", pair(2)...)
	if err := l.Void(id2, "P"); !errors.Is(err, ledger.ErrBadInput) {
		t.Fatalf("Void with post's key err = %v; want ErrBadInput", err)
	}
}

func TestVoid_KeyFieldContract(t *testing.T) {
	// Contract: Void's key has the "same field contract as Tx.Key".
	l := newLedger(t)
	id := mustPost(t, l, "P", pair(9)...)
	before := snapshot(t, l)
	for name, key := range map[string]string{
		"empty":        "",
		"oversized":    strings.Repeat("v", 129),
		"invalid utf8": "v\xff",
	} {
		if err := l.Void(id, key); !errors.Is(err, ledger.ErrBadInput) {
			t.Errorf("Void with %s key err = %v; want ErrBadInput", name, err)
		}
		wantUnchanged(t, l, before, "void bad key "+name)
	}
	if err := l.Void(id, strings.Repeat("v", 128)); err != nil {
		t.Fatalf("boundary 128-byte void key rejected: %v", err)
	}
}

func TestPost_KeyAndMemoFieldContracts(t *testing.T) {
	l := newLedger(t)
	long := strings.Repeat("k", 129)
	memo501 := strings.Repeat("m", 501)
	cases := []struct {
		name string
		tx   ledger.Tx
	}{
		{"empty key", ledger.Tx{Key: "", Entries: pair(1)}},
		{"key >128 bytes", ledger.Tx{Key: long, Entries: pair(1)}},
		{"key invalid utf8", ledger.Tx{Key: "k\xff", Entries: pair(1)}},
		{"memo >500 bytes", ledger.Tx{Key: "m1", Memo: memo501, Entries: pair(1)}},
		{"memo invalid utf8", ledger.Tx{Key: "m2", Memo: "\xc3(", Entries: pair(1)}},
		{"one entry", ledger.Tx{Key: "m3", Entries: pair(1)[:1]}},
		{"no entries", ledger.Tx{Key: "m4"}},
		{"zero-unit entry", ledger.Tx{Key: "m5", Entries: []ledger.Entry{
			{Account: "e1", Amount: units(0, "EUR")},
			{Account: "e2", Amount: units(0, "EUR")},
		}}},
	}
	for _, c := range cases {
		before := snapshot(t, l)
		if _, err := l.Post(c.tx); !errors.Is(err, ledger.ErrBadInput) {
			t.Errorf("%s: err = %v; want ErrBadInput", c.name, err)
		}
		wantUnchanged(t, l, before, c.name)
	}
	// Boundary acceptance: exactly 128-byte key, exactly 500-byte memo.
	if _, err := l.Post(ledger.Tx{Key: strings.Repeat("k", 128),
		Memo: strings.Repeat("m", 500), Entries: pair(1)}); err != nil {
		t.Fatalf("boundary-size key/memo rejected: %v", err)
	}
}
