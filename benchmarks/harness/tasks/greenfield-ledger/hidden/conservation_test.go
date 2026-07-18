// Invariant group: conservation. Traces to the package-level global
// invariant: after every exported call — success or error — the sum of all
// account balances per currency is exactly zero.
package hidden

import (
	"fmt"
	"math/rand"
	"testing"

	ledger "github.com/go-via/ledger"
)

func checkConservation(t *testing.T, l *ledger.Ledger, accounts map[string]string, ctx string) {
	t.Helper()
	sums := map[string]int64{}
	for id, cur := range accounts {
		b, err := l.Balance(id)
		if err != nil {
			t.Fatalf("%s: Balance(%s): %v", ctx, id, err)
		}
		if b.Currency != cur {
			t.Fatalf("%s: Balance(%s).Currency = %q; want %q", ctx, id, b.Currency, cur)
		}
		sums[cur] += b.Units
	}
	for cur, s := range sums {
		if s != 0 {
			t.Fatalf("%s: conservation broken: %s sums to %d", ctx, cur, s)
		}
	}
}

func TestConservation_PropertySequences(t *testing.T) {
	rng := rand.New(rand.NewSource(1789))
	accounts := map[string]string{}
	l := ledger.New()
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("eur%d", i)
		accounts[id] = "EUR"
		if err := l.CreateAccount(id, "EUR"); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("usd%d", i)
		accounts[id] = "USD"
		if err := l.CreateAccount(id, "USD"); err != nil {
			t.Fatal(err)
		}
	}

	var posted []ledger.TxID
	voided := map[ledger.TxID]bool{}
	for op := 0; op < 300; op++ {
		switch k := rng.Intn(10); {
		case k < 6: // balanced post, sometimes multi-entry, sometimes doomed
			cur, n := "EUR", 4
			if rng.Intn(2) == 0 {
				cur, n = "USD", 3
			}
			legs := 2 + rng.Intn(3)
			entries := make([]ledger.Entry, 0, legs)
			var sum int64
			for e := 0; e < legs-1; e++ {
				u := rng.Int63n(20001) - 10000
				if u == 0 {
					u = 1
				}
				sum += u
				entries = append(entries, ledger.Entry{
					Account: fmt.Sprintf("%s%d", map[string]string{"EUR": "eur", "USD": "usd"}[cur], rng.Intn(n)),
					Amount:  ledger.Amount{Units: u, Currency: cur}})
			}
			last := ledger.Entry{
				Account: fmt.Sprintf("%s%d", map[string]string{"EUR": "eur", "USD": "usd"}[cur], rng.Intn(n)),
				Amount:  ledger.Amount{Units: -sum, Currency: cur}}
			if -sum == 0 {
				last.Amount.Units = 7 // deliberately unbalanced → must be rejected
			}
			entries = append(entries, last)
			id, err := l.Post(ledger.Tx{Key: fmt.Sprintf("op%d", op), Entries: entries})
			if err == nil {
				posted = append(posted, id)
			}
		case k < 8 && len(posted) > 0: // void something, possibly already voided
			id := posted[rng.Intn(len(posted))]
			if err := l.Void(id, fmt.Sprintf("v%d", op)); err == nil {
				voided[id] = true
			}
		default: // replay an old key with a divergent payload
			if op > 0 {
				l.Post(ledger.Tx{Key: fmt.Sprintf("op%d", rng.Intn(op)),
					Entries: pair(int64(rng.Intn(100) + 1))})
			}
		}
		checkConservation(t, l, accounts, fmt.Sprintf("after op %d", op))
	}
	if len(posted) < 50 {
		t.Fatalf("property run degenerate: only %d accepted posts", len(posted))
	}
}

func TestConservation_BalancedMultiCurrencyPost(t *testing.T) {
	// A single transaction may span currencies if it balances PER currency.
	l := newLedger(t)
	mustPost(t, l, "mc",
		ledger.Entry{Account: "e1", Amount: units(300, "EUR")},
		ledger.Entry{Account: "e2", Amount: units(-300, "EUR")},
		ledger.Entry{Account: "u1", Amount: units(77, "USD")},
		ledger.Entry{Account: "u2", Amount: units(-77, "USD")})
	for acc, want := range map[string]int64{"e1": 300, "e2": -300, "u1": 77, "u2": -77} {
		if got := balanceUnits(t, l, acc); got != want {
			t.Fatalf("balance %s = %d; want %d", acc, got, want)
		}
	}
	accounts := map[string]string{"e1": "EUR", "e2": "EUR", "e3": "EUR", "u1": "USD", "u2": "USD"}
	checkConservation(t, l, accounts, "after multi-currency post")
	// And it round-trips.
	l2 := reimport(t, l)
	checkConservation(t, l2, accounts, "after multi-currency round-trip")
}

func TestConservation_SurvivesRoundTrip(t *testing.T) {
	l := newLedger(t)
	mustPost(t, l, "a", pair(12345)...)
	id := mustPost(t, l, "b", pair(-777)...)
	if err := l.Void(id, "vb"); err != nil {
		t.Fatal(err)
	}
	l2 := reimport(t, l)
	accounts := map[string]string{"e1": "EUR", "e2": "EUR", "e3": "EUR", "u1": "USD", "u2": "USD"}
	checkConservation(t, l2, accounts, "after import")
	if got := balanceUnits(t, l2, "e1"); got != 12345 {
		t.Fatalf("imported balance = %d; want 12345", got)
	}
}
