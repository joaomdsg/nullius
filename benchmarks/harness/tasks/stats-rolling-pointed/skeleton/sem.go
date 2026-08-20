package stats

import "math"

// SEM calculates the standard error of the mean of a slice
// of floats, defined as the sample standard deviation divided
// by the square root of the sample size. This matches the
// behavior of Python's scipy.stats.sem with ddof=1.
func SEM(input Float64Data) (float64, error) {
	if input.Len() == 0 {
		return math.NaN(), ErrEmptyInput
	}

	n := float64(input.Len())

	var sum, sumsq float64
	for _, v := range input {
		sum += v
		sumsq += v * v
	}

	mean := sum / n
	variance := (sumsq/n - mean*mean) * n / (n - 1)

	return math.Sqrt(variance / n), nil
}

// SEM calculates the standard error of the mean of the data
func (f Float64Data) SEM() (float64, error) {
	return SEM(f)
}
