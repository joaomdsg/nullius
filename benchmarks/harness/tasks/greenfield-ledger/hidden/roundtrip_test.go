// Invariant group: canonical-roundtrip. Traces to the Export and Import
// doc comments: byte-determinism, Import∘Export ≡ identity, byte-identical
// re-export, preserved idempotency behavior, exact escaping.
package hidden

import (
	"bytes"
	"strings"
	"testing"

	ledger "github.com/go-via/ledger"
)

func reimport(t *testing.T, l *ledger.Ledger) *ledger.Ledger {
	t.Helper()
	b := snapshot(t, l)
	l2, err := ledger.Import(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("Import: %v\nexport was:\n%s", err, b)
	}
	if b2 := snapshot(t, l2); !bytes.Equal(b, b2) {
		t.Fatalf("re-export not byte-identical\nfirst:  %q\nsecond: %q", b, b2)
	}
	return l2
}

func TestExport_Deterministic(t *testing.T) {
	build := func() *ledger.Ledger {
		l := newLedger(t)
		mustPost(t, l, "k1", pair(100)...)
		id := mustPost(t, l, "k2",
			ledger.Entry{Account: "u1", Amount: units(55, "USD")},
			ledger.Entry{Account: "u2", Amount: units(-55, "USD")})
		if err := l.Void(id, "vk2"); err != nil {
			t.Fatal(err)
		}
		return l
	}
	a, b := snapshot(t, build()), snapshot(t, build())
	if !bytes.Equal(a, b) {
		t.Fatalf("same op sequence, different bytes:\n%q\nvs\n%q", a, b)
	}
}

func TestExport_CanonicalShape(t *testing.T) {
	l := ledger.New()
	// Create out of byte order; export must sort accounts by id.
	for _, id := range []string{"zeta", "alpha", "mid"} {
		if err := l.CreateAccount(id, "EUR"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := l.Post(ledger.Tx{Key: "k", Memo: "hi", Entries: []ledger.Entry{
		{Account: "zeta", Amount: units(9, "EUR")},
		{Account: "alpha", Amount: units(-9, "EUR")},
	}}); err != nil {
		t.Fatal(err)
	}
	out := string(snapshot(t, l))
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	want := []string{
		"ledger/v1",
		"A|alpha|EUR",
		"A|mid|EUR",
		"A|zeta|EUR",
		"T|1|tx-1|k||hi|zeta:9:EUR;alpha:-9:EUR",
	}
	if len(lines) != len(want) {
		t.Fatalf("export lines = %q; want %q", lines, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("line %d = %q; want %q", i, lines[i], want[i])
		}
	}
	if !strings.HasSuffix(out, "\n") {
		t.Fatal("export must be newline-terminated")
	}
}

func TestExport_EmptyLedger(t *testing.T) {
	// A fresh ledger exports the header line and nothing else.
	l := ledger.New()
	if got := string(snapshot(t, l)); got != "ledger/v1\n" {
		t.Fatalf("empty export = %q; want %q", got, "ledger/v1\n")
	}
	l2, err := ledger.Import(strings.NewReader("ledger/v1\n"))
	if err != nil {
		t.Fatalf("import of empty export: %v", err)
	}
	if got := string(snapshot(t, l2)); got != "ledger/v1\n" {
		t.Fatalf("re-export of empty = %q", got)
	}
}

func TestExport_EscapeTableExactBytes(t *testing.T) {
	// The contract fixes the escape table byte-for-byte; a self-consistent
	// but different table must fail here.
	l := ledger.New()
	for _, id := range []string{"p|q", "x"} { // sorted: "p|q" < "x"
		if err := l.CreateAccount(id, "EUR"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := l.Post(ledger.Tx{Key: "k;1", Memo: "m:n\\o", Entries: []ledger.Entry{
		{Account: "p|q", Amount: units(5, "EUR")},
		{Account: "x", Amount: units(-5, "EUR")},
	}}); err != nil {
		t.Fatal(err)
	}
	want := "ledger/v1\n" +
		`A|p\pq|EUR` + "\n" +
		"A|x|EUR\n" +
		`T|1|tx-1|k\s1||m\cn\\o|p\pq:5:EUR;x:-5:EUR` + "\n"
	if got := string(snapshot(t, l)); got != want {
		t.Fatalf("canonical escaped export:\ngot  %q\nwant %q", got, want)
	}
}

func TestExport_EscapingRoundTrip(t *testing.T) {
	l := ledger.New()
	// Account ids exercising the escape table inside <entries>.
	hairy := []string{`a|b`, `a\b`, `a;b`, `a:b`, "a\nb", "üñïçødé", `\p`, `back\\slash`}
	for _, id := range hairy {
		if err := l.CreateAccount(id, "EUR"); err != nil {
			t.Fatalf("CreateAccount(%q): %v", id, err)
		}
	}
	memo := "m|e;m:o\\ with\nnewline ü"
	key := "k|e;y:\\\n2"
	if _, err := l.Post(ledger.Tx{Key: key, Memo: memo, Entries: []ledger.Entry{
		{Account: hairy[0], Amount: units(3, "EUR")},
		{Account: hairy[4], Amount: units(-3, "EUR")},
	}}); err != nil {
		t.Fatalf("Post hairy: %v", err)
	}
	out := snapshot(t, l)
	if bytes.Contains(out, []byte("m|e")) {
		t.Fatalf("raw '|' leaked into a field: %q", out)
	}
	l2 := reimport(t, l)
	j := l2.Journal(0, 0)
	if len(j) != 1 || j[0].Memo != memo {
		t.Fatalf("memo after round-trip = %q; want %q", j[0].Memo, memo)
	}
	if j[0].Entries[0].Account != hairy[0] || j[0].Entries[1].Account != hairy[4] {
		t.Fatalf("accounts after round-trip = %+v", j[0].Entries)
	}
	if got := balanceUnits(t, l2, hairy[0]); got != 3 {
		t.Fatalf("hairy balance = %d; want 3", got)
	}
	// The hairy KEY must also have survived: replaying it on the imported
	// ledger is a recognized no-op returning the original id.
	after := snapshot(t, l2)
	rid, err := l2.Post(ledger.Tx{Key: key, Entries: []ledger.Entry{
		{Account: hairy[0], Amount: units(1, "EUR")},
		{Account: hairy[4], Amount: units(-1, "EUR")},
	}})
	if err != nil || rid != j[0].ID {
		t.Fatalf("hairy-key replay after import = %v, %v; want %v, nil", rid, err, j[0].ID)
	}
	if b := snapshot(t, l2); !bytes.Equal(after, b) {
		t.Fatal("hairy-key replay after import mutated state")
	}
}

func TestImport_PreservesIdempotencyAndLifecycle(t *testing.T) {
	l := newLedger(t)
	orig := mustPost(t, l, "K", pair(250)...)
	vid := mustPost(t, l, "K2", pair(40)...)
	if err := l.Void(vid, "VK"); err != nil {
		t.Fatal(err)
	}
	l2 := reimport(t, l)

	// Post replay on the imported ledger returns the ORIGINAL id — and is a
	// true no-op: balances and journal untouched (a re-application would
	// keep conservation intact, so check state, not just sums).
	preReplay := snapshot(t, l2)
	id, err := l2.Post(ledger.Tx{Key: "K", Entries: pair(999)})
	if err != nil || id != orig {
		t.Fatalf("imported replay = %v, %v; want %v, nil", id, err, orig)
	}
	if got := balanceUnits(t, l2, "e1"); got != 250 {
		t.Fatalf("imported replay re-applied: e1 = %d; want 250", got)
	}
	if b := snapshot(t, l2); !bytes.Equal(preReplay, b) {
		t.Fatal("imported replay mutated state")
	}
	// Void replay key still recognized.
	if err := l2.Void(vid, "VK"); err != nil {
		t.Fatalf("imported void replay err = %v; want nil", err)
	}
	// Already-voided state preserved.
	if err := l2.Void(vid, "VK9"); err == nil {
		t.Fatal("imported ledger forgot voided state")
	}
	// Post/Void key namespace preserved across import.
	if _, err := l2.Post(ledger.Tx{Key: "VK", Entries: pair(1)}); err == nil {
		t.Fatal("imported ledger forgot void key in shared namespace")
	}
	// Seq continues, not restarts.
	nid := mustPost(t, l2, "K3", pair(5)...)
	if nid != ledger.TxID("tx-4") {
		t.Fatalf("post-import TxID = %v; want tx-4 (seq continues)", nid)
	}
}
