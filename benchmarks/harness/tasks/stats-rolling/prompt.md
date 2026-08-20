# Task: windowed statistics for `montanaflynn/stats`

You own this package (module `github.com/montanaflynn/stats`, a single
root package `stats`). It is a dependency-free numerical library used by
downstream services; the whole tree is in scope.

## Motivation

Callers doing time-series work currently get exactly one windowed helper:
`MovingAverage` (and `MovingStdDev`) in `rolling.go`. Everyone else is
hand-rolling their own sliding windows on top of `Median`, `Percentile` and
`StandardDeviation`, and each of them gets the edge cases slightly wrong.
Two teams have now shipped incidents from home-grown window code. Bring the
windowed family into the library so there is one implementation to trust.

## Required API surface

Add to `rolling.go` (package-level functions, plus the matching
`Float64Data` methods that the file's existing entries have):

- `RollingMedian(input Float64Data, window int) ([]float64, error)`
- `RollingStdDev(input Float64Data, window int) ([]float64, error)`
- `RollingPercentile(input Float64Data, window int, percent float64) ([]float64, error)`
- `type Window` — a small value type wrapping a slice and a window size,
  with a way to iterate or materialize the successive windows, so the three
  functions above (and future ones) share one windowing implementation
  instead of three copies of the same loop.

Semantics follow `MovingAverage`: trailing window, only fully-populated
windows produce output (`len(input)-window+1` entries), entry `i` covers
`input[i : i+window]`. Reuse the package's existing machinery — `Median`,
`Percentile`, `StandardDeviationSample`, the `errors.go` sentinels, and the
helpers in `util.go` — rather than reimplementing them. Bounds and empty
input must return the same sentinel errors the existing rolling functions
return, and errors from the underlying statistic must reach the caller.

## Backward compatibility

Non-negotiable: no existing exported symbol changes name, signature, or
behavior. Downstream services pin this package by module path, not version.

## Definition of done

- The new functions and the `Window` type exist with the signatures above,
  each with a godoc comment in the style of the file's neighbors.
- New tests covering the new code, including the window-size and empty-input
  boundaries.
- The existing test suite stays green: `go test ./...`.
- `go vet ./...` is clean.
