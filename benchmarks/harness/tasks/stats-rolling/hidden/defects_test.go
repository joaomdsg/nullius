package stats

import (
	"math"
	"testing"
)

// tolerance helpers -- float comparisons are never done with ==.
func closeAbs(t *testing.T, got, want, tol float64, what string) {
	t.Helper()
	if math.IsNaN(got) || math.Abs(got-want) > tol {
		t.Errorf("%s = %v, want %v (abs tol %v)", what, got, want, tol)
	}
}

func closeRel(t *testing.T, got, want, relTol float64, what string) {
	t.Helper()
	if math.IsNaN(got) || math.Abs(got-want) > relTol*math.Abs(want) {
		t.Errorf("%s = %v, want %v (rel tol %v)", what, got, want, relTol)
	}
}

// guard turns a panic inside fn into a test failure rather than a crash of
// the whole test binary.
func guard(t *testing.T, what string, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%s panicked: %v", what, r)
		}
	}()
	fn()
}

// TestDefectWindowAliasing catches defect id: window-aliasing.
//
// The Moving* functions read the caller's backing array; none of them may
// write through it. Correct behavior: the input is untouched, and a second
// call on the same slice yields identical output. Long inputs matter because
// a cumulative-sum ("prefix sum") rolling-mean shortcut is only worth taking
// on long inputs, and accumulating those sums in place destroys the caller's
// data on the first call.
func TestDefectWindowAliasing(t *testing.T) {
	// --- Long input: 600 elements, d[i] = i%10 ---------------------------
	// Windows of 3 starting at 0: (0+1+2)/3=1, (1+2+3)/3=2, (2+3+4)/3=3.
	// Provenance: arithmetic on the documented contract, confirmed against
	// the pristine upstream implementation.
	const n = 600
	long := make(Float64Data, n)
	for i := range long {
		long[i] = float64(i % 10)
	}
	longSnap := append(Float64Data(nil), long...)

	var lFirst, lSecond []float64
	guard(t, "MovingAverage(long)", func() {
		var err error
		lFirst, err = MovingAverage(long, 3)
		if err != nil {
			t.Fatalf("MovingAverage(long,3) error: %v", err)
		}
		lSecond, err = MovingAverage(long, 3)
		if err != nil {
			t.Fatalf("MovingAverage(long,3) 2nd call error: %v", err)
		}
	})

	for i := range longSnap {
		if long[i] != longSnap[i] {
			t.Fatalf("MovingAverage mutated input[%d]: got %v, want %v (head now %v)",
				i, long[i], longSnap[i], long[:8])
		}
	}
	if len(lFirst) != n-3+1 {
		t.Fatalf("MovingAverage(long,3) len = %d, want %d", len(lFirst), n-3+1)
	}
	for i, want := range []float64{1, 2, 3} {
		closeAbs(t, lFirst[i], want, 1e-12, "MovingAverage(long,3) leading element")
	}
	if len(lFirst) != len(lSecond) {
		t.Fatalf("MovingAverage(long,3) length differs between calls: %d vs %d", len(lFirst), len(lSecond))
	}
	for i := range lFirst {
		if math.Abs(lFirst[i]-lSecond[i]) > 1e-9 {
			t.Fatalf("MovingAverage(long,3) not idempotent at %d: %v then %v", i, lFirst[i], lSecond[i])
		}
	}

	// --- Short inputs: every Moving* function, unsorted so an in-place
	// sort or accumulation is observable. -------------------------------
	orig := []float64{5, 1, 4, 2, 3, 9, 6}
	snapshot := append([]float64(nil), orig...)

	type call struct {
		name string
		fn   func(Float64Data, int) ([]float64, error)
	}
	calls := []call{
		{"MovingMedian", MovingMedian},
		{"MovingMin", MovingMin},
		{"MovingMax", MovingMax},
		{"MovingSum", MovingSum},
		{"MovingAverage", MovingAverage},
		{"MovingStdDev", MovingStdDev},
	}

	for _, c := range calls {
		c := c
		t.Run(c.name, func(t *testing.T) {
			input := append([]float64(nil), snapshot...)

			var first, second []float64
			guard(t, c.name, func() {
				var err error
				first, err = c.fn(Float64Data(input), 3)
				if err != nil {
					t.Fatalf("%s returned unexpected error: %v", c.name, err)
				}
				second, err = c.fn(Float64Data(input), 3)
				if err != nil {
					t.Fatalf("%s (2nd call) returned unexpected error: %v", c.name, err)
				}
			})

			for i := range snapshot {
				if input[i] != snapshot[i] {
					t.Errorf("%s mutated input[%d]: got %v, want %v (input now %v)",
						c.name, i, input[i], snapshot[i], input)
				}
			}

			if len(first) != len(second) {
				t.Fatalf("%s length differs between calls: %d vs %d", c.name, len(first), len(second))
			}
			for i := range first {
				if math.Abs(first[i]-second[i]) > 1e-12 {
					t.Errorf("%s not idempotent at %d: %v then %v", c.name, i, first[i], second[i])
				}
			}
		})
	}

	// Value check on MovingMedian, derived by hand from the documented
	// contract (medians of the trailing windows of the input above):
	// [5 1 4]->4, [1 4 2]->2, [4 2 3]->3, [2 3 9]->3, [3 9 6]->6.
	input := append([]float64(nil), snapshot...)
	got, err := MovingMedian(Float64Data(input), 3)
	if err != nil {
		t.Fatalf("MovingMedian error: %v", err)
	}
	want := []float64{4, 2, 3, 3, 6}
	if len(got) != len(want) {
		t.Fatalf("MovingMedian len = %d, want %d (got %v)", len(got), len(want), got)
	}
	for i := range want {
		closeAbs(t, got[i], want[i], 1e-12, "MovingMedian element")
	}
}

// TestDefectCopySliceLength catches defect id: copyslice-cap-not-len.
//
// A defensive sorted copy must be sized by the input's LENGTH. Sizing it by
// capacity instead pads the copy with zero values, and sorting then drags
// those zeros to the front and shifts every rank. Only slices whose length is
// smaller than their capacity (the common `make([]float64, 0, n)` + append
// buffer) can tell the two apart.
func TestDefectCopySliceLength(t *testing.T) {
	// len 5, cap 8. Sorted: {10,20,30,40,50}; rank = 0.5*(5-1) = 2 -> 30.
	// Provenance: pristine upstream Percentile on the same values.
	buf := make([]float64, 0, 8)
	buf = append(buf, 10, 20, 30, 40, 50)
	shortLen := Float64Data(buf)
	if len(shortLen) >= cap(shortLen) {
		t.Fatalf("test setup: need len < cap, got len %d cap %d", len(shortLen), cap(shortLen))
	}

	guard(t, "Percentile(len<cap)", func() {
		got, err := Percentile(shortLen, 50)
		if err != nil {
			t.Fatalf("Percentile(len<cap, 50) err = %v", err)
		}
		closeAbs(t, got, 30, 1e-12, "Percentile({10,20,30,40,50} len 5 cap 8, 50)")
	})
	// p=100 on the same buffer -> rank 4 -> c[4] = 50.
	guard(t, "Percentile(len<cap,100)", func() {
		got, err := Percentile(shortLen, 100)
		if err != nil {
			t.Fatalf("Percentile(len<cap, 100) err = %v", err)
		}
		closeAbs(t, got, 50, 1e-12, "Percentile({10,20,30,40,50} len 5 cap 8, 100)")
	})

	// Same values in an exactly-sized slice: passes under either sizing rule,
	// so this is the specificity control.
	guard(t, "Percentile(len==cap)", func() {
		got, err := Percentile(Float64Data{10, 20, 30, 40, 50}, 50)
		if err != nil {
			t.Fatalf("Percentile(len==cap, 50) err = %v", err)
		}
		closeAbs(t, got, 30, 1e-12, "Percentile({10,20,30,40,50}, 50)")
	})

	// Median of {3,1,2,5} -- sorted {1,2,3,5}, mean of the two middle values
	// (2+3)/2 = 2.5. Exact arithmetic.
	four := Float64Data{3, 1, 2, 5}
	guard(t, "Median", func() {
		got, err := Median(four)
		if err != nil {
			t.Fatalf("Median returned error %v", err)
		}
		closeAbs(t, got, 2.5, 1e-12, "Median{3,1,2,5}")
	})

	// The internal helper itself, checked directly, and the input must not
	// have been sorted in place.
	c := sortedCopy(four)
	if c.Len() != four.Len() {
		t.Errorf("sortedCopy len = %d, want %d", c.Len(), four.Len())
	}
	sortedWant := []float64{1, 2, 3, 5}
	for i := 0; i < c.Len() && i < len(sortedWant); i++ {
		closeAbs(t, c[i], sortedWant[i], 1e-12, "sortedCopy element")
	}
	origWant := []float64{3, 1, 2, 5}
	for i := range origWant {
		if four[i] != origWant[i] {
			t.Errorf("sortedCopy mutated input[%d]: %v, want %v", i, four[i], origWant[i])
		}
	}
}

// TestDefectPercentileRankBoundary catches defect id: percentile-rank-boundary.
//
// Percentile uses linear interpolation between closest ranks:
// rank = (p/100)*(n-1); k = int(rank); f = rank-k;
// result = c[k] + f*(c[k+1]-c[k]), with c[k] when k+1 == n.
// The rank formula is uniform: it does not change with the parity of n, nor
// inside the lower tail. All expected values below are from the pristine
// upstream implementation. Every slice is a literal (len == cap) so the
// copy-sizing defect cannot contribute.
func TestDefectPercentileRankBoundary(t *testing.T) {
	even4 := Float64Data{40, 10, 30, 20} // sorted: 10,20,30,40 (n=4)
	even2 := Float64Data{2, 1}           // sorted: 1,2         (n=2)
	even6 := Float64Data{12, 3, 5, 7, 8, 20}
	odd5 := Float64Data{3, 1, 5, 2, 4} // sorted: 1,2,3,4,5   (n=5)

	cases := []struct {
		name    string
		data    Float64Data
		percent float64
		want    float64
		wantErr error
	}{
		// Lower tail, even n -- rank must use n-1.
		// n=4, p=10 -> rank 0.3 -> 10 + 0.3*(20-10) = 13
		{"n4/p10", even4, 10, 13, nil},
		// n=4, p=1  -> rank 0.03 -> 10 + 0.03*10 = 10.3
		{"n4/p1", even4, 1, 10.3, nil},
		// n=4, p=30 -> rank 0.9 -> 10 + 0.9*10 = 19
		{"n4/p30", even4, 30, 19, nil},
		// n=2, p=50 -> rank 0.5 -> 1 + 0.5*(2-1) = 1.5
		{"n2/p50", even2, 50, 1.5, nil},
		// n=6, p=1 -> rank 0.05 -> 3 + 0.05*(5-3) = 3.1
		{"n6/p1", even6, 1, 3.1, nil},
		// n=6, p=10 -> rank 0.5 -> 3 + 0.5*(5-3) = 4
		{"n6/p10", even6, 10, 4, nil},

		// Midrange / upper tail and odd n: these agree under either rank
		// convention -- specificity controls.
		{"n6/p50", even6, 50, 7.5, nil},  // rank 2.5 -> 7 + 0.5*(8-7)
		{"n6/p99", even6, 99, 19.6, nil}, // rank 4.95 -> 12 + 0.95*8
		{"n6/p100", even6, 100, 20, nil}, // rank 5 -> c[5]
		{"n4/p50", even4, 50, 25, nil},   // rank 1.5 -> 20 + 0.5*10
		{"n4/p100", even4, 100, 40, nil}, // rank 3 -> c[3]
		{"n5/p10", odd5, 10, 1.4, nil},   // rank 0.4 -> 1 + 0.4*(2-1)
		{"n5/p50", odd5, 50, 3, nil},     // rank 2 -> c[2]
		{"n5/p100", odd5, 100, 5, nil},   // rank 4 -> c[4]
		{"n4/p0", even4, 0, math.NaN(), ErrBounds},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			guard(t, "Percentile", func() {
				got, err := Percentile(tc.data, tc.percent)
				if tc.wantErr != nil {
					if err != tc.wantErr {
						t.Fatalf("Percentile(%v, %v) err = %v, want %v", tc.data, tc.percent, err, tc.wantErr)
					}
					if !math.IsNaN(got) {
						t.Errorf("Percentile(%v, %v) = %v on error, want NaN", tc.data, tc.percent, got)
					}
					return
				}
				if err != nil {
					t.Fatalf("Percentile(%v, %v) unexpected err %v", tc.data, tc.percent, err)
				}
				closeAbs(t, got, tc.want, 1e-9, "Percentile")
			})
		})
	}
}

// TestDefectVarianceStability catches defect id: variance-onepass.
//
// SEM is the sample standard deviation divided by sqrt(n), and the sample
// standard deviation is shift-invariant: SEM({1e9 + d}) == SEM({d}). A naive
// one-pass E[x^2] - E[x]^2 cancels ~18 significant digits at magnitude 1e9
// and loses the answer entirely; the two-pass sum-of-squared-deviations form
// used by StandardDeviationSample is exact to rounding.
func TestDefectVarianceStability(t *testing.T) {
	const relTol = 1e-9

	// deltas 1..4: mean 2.5, SS = 2*(1.5^2+0.5^2) = 5, sample variance 5/3,
	// sample stddev sqrt(5/3), SEM = sqrt(5/3)/2 = 0.6454972243679028.
	// Provenance: pristine upstream SEM on the same input.
	big4 := Float64Data{1e9 + 1, 1e9 + 2, 1e9 + 3, 1e9 + 4}
	sem4, err := SEM(big4)
	if err != nil {
		t.Fatalf("SEM(1e9+1..4) error: %v", err)
	}
	closeRel(t, sem4, 0.6454972243679028, relTol, "SEM(1e9+1..4)")

	// deltas 0..9: mean 4.5, SS = 2*(4.5^2+3.5^2+2.5^2+1.5^2+0.5^2) = 82.5,
	// sample variance 82.5/9, SEM = sqrt(82.5/9)/sqrt(10).
	const base = 1e9
	big := make(Float64Data, 10)
	for i := range big {
		big[i] = base + float64(i)
	}
	sem10, err := SEM(big)
	if err != nil {
		t.Fatalf("SEM(1e9+0..9) error: %v", err)
	}
	closeRel(t, sem10, math.Sqrt(82.5/9.0)/math.Sqrt(10), relTol, "SEM(1e9+0..9)")

	// Shift invariance stated directly: the same deltas at magnitude 0.
	small := Float64Data{1, 2, 3, 4}
	semSmall, err := SEM(small)
	if err != nil {
		t.Fatalf("SEM(1..4) error: %v", err)
	}
	closeRel(t, semSmall, 0.6454972243679028, relTol, "SEM(1,2,3,4)")

	// Small-magnitude control: the naive form gets this right too, so it must
	// pass either way (specificity check).
	closeRel(t, semSmall, sem4, 1e-6, "SEM shift invariance (1..4 vs 1e9+1..4)")

	// Empty input still reports ErrEmptyInput with a NaN value.
	if got, err := SEM(Float64Data{}); err != ErrEmptyInput || !math.IsNaN(got) {
		t.Errorf("SEM(empty) = %v, %v; want NaN, ErrEmptyInput", got, err)
	}
}

// TestDefectDiffEmptyInput catches defect id: diff-len-underflow.
//
// Emptiness is a property of a slice's LENGTH, not its capacity. Diff and
// PercentChange must return ErrEmptyInput for any zero-length input,
// including a `make([]float64, 0, n)` buffer that has spare capacity.
func TestDefectDiffEmptyInput(t *testing.T) {
	spare := make([]float64, 0, 4)
	if len(spare) != 0 || cap(spare) == 0 {
		t.Fatalf("test setup: need len 0 with spare cap, got len %d cap %d", len(spare), cap(spare))
	}
	filled := make([]float64, 0, 4)
	filled = append(filled, 1, 2, 4)

	empties := []struct {
		name string
		data Float64Data
	}{
		{"nil", nil},
		{"emptyLiteral", Float64Data{}},
		{"spareCapacity", Float64Data(spare[:0])},
		{"truncatedToEmpty", Float64Data(filled[:0])},
	}

	for _, e := range empties {
		e := e
		t.Run("Diff/"+e.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Diff(%s) panicked instead of returning ErrEmptyInput: %v", e.name, r)
				}
			}()
			got, err := Diff(e.data)
			if err != ErrEmptyInput {
				t.Fatalf("Diff(%s) err = %v, want ErrEmptyInput (got value %v)", e.name, err, got)
			}
			if len(got) != 0 {
				t.Errorf("Diff(%s) = %v, want nil/empty", e.name, got)
			}
		})

		t.Run("PercentChange/"+e.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("PercentChange(%s) panicked instead of returning ErrEmptyInput: %v", e.name, r)
				}
			}()
			got, err := PercentChange(e.data)
			if err != ErrEmptyInput {
				t.Fatalf("PercentChange(%s) err = %v, want ErrEmptyInput (got value %v)", e.name, err, got)
			}
			if len(got) != 0 {
				t.Errorf("PercentChange(%s) = %v, want nil/empty", e.name, got)
			}
		})
	}

	// Single element: documented to return an empty slice, no error.
	t.Run("Diff/single", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Diff(single) panicked: %v", r)
			}
		}()
		got, err := Diff(Float64Data{42})
		if err != nil {
			t.Fatalf("Diff(single) err = %v, want nil", err)
		}
		if len(got) != 0 {
			t.Errorf("Diff(single) = %v, want empty", got)
		}
	})

	// Happy path so the empty-input guard cannot be satisfied by gutting Diff.
	t.Run("Diff/values", func(t *testing.T) {
		got, err := Diff(Float64Data{1, 2, 4, 8})
		if err != nil {
			t.Fatalf("Diff err = %v", err)
		}
		want := []float64{1, 2, 4} // 2-1, 4-2, 8-4
		if len(got) != len(want) {
			t.Fatalf("Diff len = %d, want %d (got %v)", len(got), len(want), got)
		}
		for i := range want {
			closeAbs(t, got[i], want[i], 1e-12, "Diff element")
		}
	})
}

// TestDefectErrorPropagation catches defect id: err-swallowed-chain.
//
// A helper that fails inside a chained computation must not be able to make
// the outer function report a fabricated zero value. Quartile of a
// single-element input asks Median for two empty sub-slices; upstream lets
// those NaN results through and reports no error, so the caller can see that
// Q1/Q3 are undefined. Reporting Q1 = Q3 = 0 instead is a swallowed failure
// that silently claims a real answer.
func TestDefectErrorPropagation(t *testing.T) {
	empty := Float64Data{}
	data := Float64Data{1, 2, 3, 4}

	t.Run("QuartileSingle", func(t *testing.T) {
		// Provenance: pristine upstream Quartile(Float64Data{5}) returns
		// Quartiles{Q1: NaN, Q2: 5, Q3: NaN} with a nil error -- the empty
		// halves have no median, and that must not be reported as 0.
		qs, err := Quartile(Float64Data{5})
		if err != nil {
			t.Fatalf("Quartile({5}) err = %v, want nil", err)
		}
		if !math.IsNaN(qs.Q1) {
			t.Errorf("Quartile({5}).Q1 = %v, want NaN (median of an empty half)", qs.Q1)
		}
		closeAbs(t, qs.Q2, 5, 1e-12, "Quartile({5}).Q2")
		if !math.IsNaN(qs.Q3) {
			t.Errorf("Quartile({5}).Q3 = %v, want NaN (median of an empty half)", qs.Q3)
		}
	})

	t.Run("InterQuartileRangeSingle", func(t *testing.T) {
		// NaN - NaN = NaN; pristine returns (NaN, nil).
		got, err := InterQuartileRange(Float64Data{5})
		if err != nil {
			t.Fatalf("InterQuartileRange({5}) err = %v, want nil", err)
		}
		if !math.IsNaN(got) {
			t.Errorf("InterQuartileRange({5}) = %v, want NaN", got)
		}
	})

	t.Run("QuartileHappyPath", func(t *testing.T) {
		// sorted {1,2,3,4}: c1 = c2 = 2, Q1 = median{1,2} = 1.5,
		// Q2 = median{1,2,3,4} = 2.5, Q3 = median{3,4} = 3.5.
		qs, err := Quartile(data)
		if err != nil {
			t.Fatalf("Quartile(data) err = %v", err)
		}
		closeAbs(t, qs.Q1, 1.5, 1e-12, "Quartile{1,2,3,4}.Q1")
		closeAbs(t, qs.Q2, 2.5, 1e-12, "Quartile{1,2,3,4}.Q2")
		closeAbs(t, qs.Q3, 3.5, 1e-12, "Quartile{1,2,3,4}.Q3")
	})

	t.Run("QuartileEmpty", func(t *testing.T) {
		if _, err := Quartile(empty); err != ErrEmptyInput {
			t.Errorf("Quartile(empty) err = %v, want ErrEmptyInput", err)
		}
	})

	t.Run("MedianEmpty", func(t *testing.T) {
		got, err := Median(empty)
		if err != ErrEmptyInput {
			t.Fatalf("Median(empty) err = %v, want ErrEmptyInput (got value %v)", err, got)
		}
		if !math.IsNaN(got) {
			t.Errorf("Median(empty) = %v, want NaN", got)
		}
	})
}
