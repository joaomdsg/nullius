// Invariant group: hostile-input. Traces to the "Hostile input, typed
// errors" prompt clause and the Import strictness doc comment: no panics
// ever, ErrCorruptImport on any deviation, reader errors passed through.
package hidden

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	ledger "github.com/go-via/ledger"
)

// noPanic converts a panic into a test failure instead of killing the run.
func noPanic(t *testing.T, ctx string, f func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%s panicked: %v", ctx, r)
		}
	}()
	f()
}

const validExport = `ledger/v1
A|e1|EUR
A|e2|EUR
T|1|tx-1|k||m|e1:100:EUR;e2:-100:EUR
`

func TestImport_AcceptsCanonical(t *testing.T) {
	l, err := ledger.Import(strings.NewReader(validExport))
	if err != nil {
		t.Fatalf("canonical import rejected: %v", err)
	}
	if got := balanceUnits(t, l, "e1"); got != 100 {
		t.Fatalf("imported balance = %d; want 100", got)
	}
}

func TestImport_CorruptRejected(t *testing.T) {
	cases := []struct{ name, in string }{
		{"empty", ""},
		{"bad header", strings.Replace(validExport, "ledger/v1", "ledger/v2", 1)},
		{"missing header", strings.TrimPrefix(validExport, "ledger/v1\n")},
		{"unknown tag", validExport + "X|what|ever\n"},
		{"account field count", strings.Replace(validExport, "A|e1|EUR", "A|e1", 1)},
		{"bad currency", strings.Replace(validExport, "A|e1|EUR", "A|e1|eur", 1)},
		{"unsorted accounts", strings.Replace(validExport,
			"A|e1|EUR\nA|e2|EUR", "A|e2|EUR\nA|e1|EUR", 1)},
		{"duplicate account", strings.Replace(validExport, "A|e2|EUR", "A|e1|EUR", 1)},
		{"seq not from 1", strings.Replace(validExport, "T|1|tx-1", "T|2|tx-2", 1)},
		{"id/seq mismatch", strings.Replace(validExport, "tx-1", "tx-9", 1)},
		{"unbalanced entries", strings.Replace(validExport, "e2:-100:EUR", "e2:-99:EUR", 1)},
		{"unknown account in tx", strings.Replace(validExport, "e2:-100:EUR", "zz:-100:EUR", 1)},
		{"zero-unit entry", strings.Replace(validExport,
			"e1:100:EUR;e2:-100:EUR", "e1:0:EUR;e2:0:EUR", 1)},
		{"decimal units", strings.Replace(validExport, "e1:100:EUR", "e1:1.00:EUR", 1)},
		{"non-numeric units", strings.Replace(validExport, "e1:100:EUR", "e1:x:EUR", 1)},
		{"raw pipe in memo", strings.Replace(validExport, "|k||m|", "|k||m|m|", 1)},
		{"void of unknown tx", validExport + "T|2|tx-2|vk|tx-9||e2:100:EUR;e1:-100:EUR\n"},
		{"duplicate key", validExport + "T|2|tx-2|k||m2|e1:5:EUR;e2:-5:EUR\n"},
		{"duplicate seq", validExport + "T|1|tx-1|k2||m2|e1:5:EUR;e2:-5:EUR\n"},
		{"empty key on post", strings.Replace(validExport, "|k||m|", "|||m|", 1)},
		{"trailing garbage", validExport + "junk\n"},
		{"truncated (no final newline)", strings.TrimSuffix(validExport, "\n")},
		{"truncated mid-line", validExport[:len(validExport)-10]},
	}
	for _, c := range cases {
		noPanic(t, "Import/"+c.name, func() {
			l, err := ledger.Import(strings.NewReader(c.in))
			if !errors.Is(err, ledger.ErrCorruptImport) {
				t.Errorf("%s: err = %v; want ErrCorruptImport", c.name, err)
			}
			if l != nil {
				t.Errorf("%s: non-nil ledger on corrupt input", c.name)
			}
		})
	}
}

type failingReader struct{ err error }

func (f failingReader) Read([]byte) (int, error) { return 0, f.err }

func TestImport_ReaderErrorPassthrough(t *testing.T) {
	sentinel := errors.New("disk on fire")
	_, err := ledger.Import(failingReader{sentinel})
	if !errors.Is(err, sentinel) {
		t.Fatalf("reader error = %v; want passthrough of sentinel", err)
	}
}

func TestNoPanic_HostileInputs(t *testing.T) {
	rng := rand.New(rand.NewSource(4242))
	// ParseAmount fuzz-ish corpus.
	corpus := []string{"", ".", "..", "-", "--1", "\x00", "\xff\xfe", "9999999999999999999999999999.99",
		strings.Repeat("9", 400), "1\n2", "NaN", "Inf", "-Inf", "0.-1", "０.１０"}
	for i := 0; i < 300; i++ {
		b := make([]byte, rng.Intn(24))
		for j := range b {
			b[j] = byte(rng.Intn(256))
		}
		corpus = append(corpus, string(b))
	}
	for _, s := range corpus {
		noPanic(t, fmt.Sprintf("ParseAmount(%q)", s), func() {
			ledger.ParseAmount(s, "EUR")
			ledger.ParseAmount("1.00", s)
		})
	}
	// Import fuzz-ish corpus: random mutations of a valid export.
	base := []byte(validExport)
	for i := 0; i < 300; i++ {
		mut := append([]byte(nil), base...)
		for k := 0; k < 1+rng.Intn(4); k++ {
			switch rng.Intn(3) {
			case 0:
				if len(mut) > 0 {
					mut[rng.Intn(len(mut))] = byte(rng.Intn(256))
				}
			case 1:
				if len(mut) > 1 {
					cut := rng.Intn(len(mut))
					mut = mut[:cut]
				}
			case 2:
				pos := rng.Intn(len(mut) + 1)
				mut = append(mut[:pos:pos], append([]byte{byte(rng.Intn(256))}, mut[pos:]...)...)
			}
		}
		noPanic(t, fmt.Sprintf("Import(mutation %d)", i), func() {
			l, err := ledger.Import(bytes.NewReader(mut))
			if err == nil && l != nil {
				// Rarely a mutation stays canonical; if accepted it must re-export.
				var out bytes.Buffer
				if e := l.Export(&out); e != nil {
					t.Errorf("mutation %d: accepted but re-export failed: %v", i, e)
				}
			}
		})
	}
	// Ledger entrypoints under hostile field values.
	noPanic(t, "ledger ops", func() {
		l := ledger.New()
		l.CreateAccount(strings.Repeat("\xff", 8), "EUR")
		l.CreateAccount("ok", "EUR")
		l.Post(ledger.Tx{Key: "\x80\x81", Entries: pair(1)})
		l.Void(ledger.TxID(strings.Repeat("z", 1000)), "k")
		l.Balance(string([]byte{0}))
		l.Journal(^uint64(0), -1)
	})
}
