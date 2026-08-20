# Hidden ground-truth suite: stats-rolling

`defects_test.go` is copied into the module root at scoring time and compiles as
`package stats`. One test function per injected defect:

- `TestDefectWindowAliasing` — **window-aliasing**: Moving* reducers must not write through the caller's backing array; input unchanged + two calls identical.
- `TestDefectCopySliceLength` — **copyslice-cap-not-len**: `copyslice`/`sortedCopy` must allocate LENGTH n, not length 0 / cap n, or `copy()` moves nothing.
- `TestDefectPercentileRankBoundary` — **percentile-rank-boundary**: `rank = (p/100)*(n-1)` interpolation at p ∈ {0,1,50,99,100} on even-length inputs (odd-length midrange cases are specificity controls).
- `TestDefectVarianceStability` — **variance-onepass**: two-pass sum-of-squared-deviations; naive `E[x^2]-E[x]^2` loses the answer at magnitude 1e9. Covers Variance/PopulationVariance/SampleVariance/StdDev{Population,Sample}/SEM/MovingStdDev.
- `TestDefectDiffEmptyInput` — **diff-len-underflow**: `Diff`/`PercentChange` on empty input return `ErrEmptyInput`, never `make([]float64, -1)` panic (subtests use `recover()`).
- `TestDefectErrorPropagation` — **err-swallowed-chain**: percentile/sorted-copy chain returns the package sentinel (`ErrEmptyInput`, `ErrBounds`) instead of a swallowed zero value.

All expected values are derived from exact arithmetic against the pristine
upstream implementation (`github.com/montanaflynn/stats` @ v0.12.4, commit
5badb5a); every float comparison uses an explicit tolerance. No randomness,
timing, goroutines, or filesystem access; no inter-test ordering dependence.
