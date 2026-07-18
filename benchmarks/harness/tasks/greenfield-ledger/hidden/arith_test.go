// Invariant group: exact-arithmetic. Traces to the Amount, ParseAmount,
// String, Add, Neg, and Allocate doc comments.
package hidden

import (
	"errors"
	"math"
	"math/rand"
	"testing"

	ledger "github.com/go-via/ledger"
)

func TestParseAmount_Grammar(t *testing.T) {
	valid := []struct {
		in   string
		want int64
	}{
		{"0", 0}, {"12", 1200}, {"12.34", 1234}, {"-0.05", -5},
		{"-0", 0}, {"-0.00", 0}, {"007", 700}, // grammar: one or more digits
		{"92233720368547758.07", math.MaxInt64},
		{"-92233720368547758.07", -math.MaxInt64},
	}
	for _, c := range valid {
		a, err := ledger.ParseAmount(c.in, "EUR")
		if err != nil || a.Units != c.want || a.Currency != "EUR" {
			t.Errorf("ParseAmount(%q) = %+v, %v; want %d EUR", c.in, a, err, c.want)
		}
	}
	badInput := []string{
		"", ".", "12.", "12.3", "12.345", "+12.00", " 12.00", "12.00 ",
		"1,200.00", "12..00", "-", "-.05", "12.3a", "0x10", "1e2", "١٢",
	}
	for _, in := range badInput {
		if _, err := ledger.ParseAmount(in, "EUR"); !errors.Is(err, ledger.ErrBadInput) {
			t.Errorf("ParseAmount(%q) err = %v; want ErrBadInput", in, err)
		}
	}
	// Well-formed but out of range → ErrOverflow (incl. MinInt64 itself).
	for _, in := range []string{"92233720368547758.08", "-92233720368547758.08", "99999999999999999999.00"} {
		if _, err := ledger.ParseAmount(in, "EUR"); !errors.Is(err, ledger.ErrOverflow) {
			t.Errorf("ParseAmount(%q) err = %v; want ErrOverflow", in, err)
		}
	}
	// Bad currency codes.
	for _, cur := range []string{"", "EU", "EURO", "eur", "EUr", "EU1", "€UR"} {
		if _, err := ledger.ParseAmount("1.00", cur); !errors.Is(err, ledger.ErrBadInput) {
			t.Errorf("ParseAmount(1.00, %q) err = %v; want ErrBadInput", cur, err)
		}
	}
}

func TestAmount_StringRoundTrip(t *testing.T) {
	cases := []struct {
		units int64
		want  string
	}{
		{0, "0.00"}, {5, "0.05"}, {-5, "-0.05"}, {1234, "12.34"},
		{1200, "12.00"}, {-1234, "-12.34"},
		{math.MaxInt64, "92233720368547758.07"},
		{-math.MaxInt64, "-92233720368547758.07"},
	}
	for _, c := range cases {
		got := units(c.units, "EUR").String()
		if got != c.want {
			t.Errorf("String(%d) = %q; want %q", c.units, got, c.want)
		}
	}
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 500; i++ {
		u := rng.Int63() - rng.Int63() // spans negatives; MinInt64 unreachable
		a := units(u, "EUR")
		back, err := ledger.ParseAmount(a.String(), "EUR")
		if err != nil || back.Units != u {
			t.Fatalf("round-trip %d via %q = %d, %v", u, a.String(), back.Units, err)
		}
	}
}

func TestAmount_AddNeg(t *testing.T) {
	if got, err := units(100, "EUR").Add(units(-40, "EUR")); err != nil || got.Units != 60 {
		t.Errorf("100 + -40 = %+v, %v", got, err)
	}
	if _, err := units(1, "EUR").Add(units(1, "USD")); !errors.Is(err, ledger.ErrCurrencyMismatch) {
		t.Errorf("cross-currency Add err = %v; want ErrCurrencyMismatch", err)
	}
	if _, err := units(math.MaxInt64, "EUR").Add(units(1, "EUR")); !errors.Is(err, ledger.ErrOverflow) {
		t.Errorf("MaxInt64+1 err = %v; want ErrOverflow", err)
	}
	if _, err := units(-math.MaxInt64, "EUR").Add(units(-1, "EUR")); !errors.Is(err, ledger.ErrOverflow) {
		t.Errorf("-(Max)+-1 err = %v; want ErrOverflow (MinInt64 is invalid)", err)
	}
	if got := units(-42, "EUR").Neg(); got.Units != 42 || got.Currency != "EUR" {
		t.Errorf("Neg(-42) = %+v", got)
	}
}

func TestAllocate_WorkedExamples(t *testing.T) {
	cases := []struct {
		units   int64
		weights []int
		want    []int64
	}{
		{100, []int{1, 1, 1}, []int64{34, 33, 33}},
		{-100, []int{1, 1, 1}, []int64{-34, -33, -33}},
		{101, []int{3, 3, 1}, []int64{43, 43, 15}},
		{100, []int{1, 0, 1}, []int64{50, 0, 50}},   // zero weight → exactly zero
		{7, []int{1, 1, 1, 1, 1}, []int64{2, 2, 1, 1, 1}}, // ties → lowest index
		{0, []int{5, 3}, []int64{0, 0}},
		{1, []int{1, 1}, []int64{1, 0}},
		{-1, []int{1, 1}, []int64{-1, 0}},
	}
	for _, c := range cases {
		parts, err := ledger.Allocate(units(c.units, "EUR"), c.weights)
		if err != nil {
			t.Errorf("Allocate(%d, %v): %v", c.units, c.weights, err)
			continue
		}
		for i, p := range parts {
			if p.Units != c.want[i] || p.Currency != "EUR" {
				t.Errorf("Allocate(%d, %v) = %v; want %v", c.units, c.weights, parts, c.want)
				break
			}
		}
	}
	for _, w := range [][]int{nil, {}, {0, 0}, {1, -1}, {-1}} {
		if _, err := ledger.Allocate(units(10, "EUR"), w); !errors.Is(err, ledger.ErrBadInput) {
			t.Errorf("Allocate(10, %v) err = %v; want ErrBadInput", w, err)
		}
	}
}

func TestAllocate_ExactSum(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	for i := 0; i < 400; i++ {
		u := rng.Int63n(2_000_000_001) - 1_000_000_000
		n := 1 + rng.Intn(9)
		weights := make([]int, n)
		nonzero := false
		for j := range weights {
			weights[j] = rng.Intn(1000)
			nonzero = nonzero || weights[j] > 0
		}
		if !nonzero {
			weights[0] = 1
		}
		parts, err := ledger.Allocate(units(u, "EUR"), weights)
		if err != nil {
			t.Fatalf("Allocate(%d, %v): %v", u, weights, err)
		}
		var sum int64
		for j, p := range parts {
			sum += p.Units
			if weights[j] == 0 && p.Units != 0 {
				t.Fatalf("zero weight got %d units (weights %v)", p.Units, weights)
			}
			if u >= 0 && p.Units < 0 || u < 0 && p.Units > 0 {
				t.Fatalf("part sign flipped: %d from %d", p.Units, u)
			}
		}
		if sum != u {
			t.Fatalf("Allocate(%d, %v) sums to %d", u, weights, sum)
		}
	}
}

func TestAllocate_BigIntermediates(t *testing.T) {
	// a.Units * weight overflows int64; the contract requires exactness anyway.
	a := units(math.MaxInt64-1, "EUR")
	weights := []int{math.MaxInt32, math.MaxInt32 - 1, 1}
	parts, err := ledger.Allocate(a, weights)
	if err != nil {
		t.Fatalf("Allocate(big): %v (contract: intermediates must be exact, never ErrOverflow)", err)
	}
	var sum int64
	for _, p := range parts {
		sum += p.Units
	}
	if sum != a.Units {
		t.Fatalf("big-intermediate allocation sums to %d, want %d", sum, a.Units)
	}
}
