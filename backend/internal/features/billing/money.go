package billing

import (
	"math"
	"math/big"
	"strconv"
)

// NanoPerUSD is the number of nano-dollars in one dollar.
const NanoPerUSD int64 = 1_000_000_000

// USDToNano converts dollars to nano-dollars, rounding to the nearest integer.
// Non-positive, NaN and infinite inputs yield 0; huge inputs saturate.
func USDToNano(usd float64) int64 {
	if math.IsNaN(usd) || math.IsInf(usd, 0) || usd <= 0 {
		return 0
	}
	v := math.Round(usd * float64(NanoPerUSD))
	if v >= float64(math.MaxInt64) {
		return math.MaxInt64
	}
	return int64(v)
}

// NanoToUSD converts nano-dollars to a floating dollar value.
func NanoToUSD(n int64) float64 { return float64(n) / float64(NanoPerUSD) }

// formatFloat renders the shortest decimal form of f.
func formatFloat(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// satAdd adds without overflowing.
func satAdd(a, b int64) int64 {
	if b > 0 && a > math.MaxInt64-b {
		return math.MaxInt64
	}
	if b < 0 && a < math.MinInt64-b {
		return math.MinInt64
	}
	return a + b
}

func maxI64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func minI64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func clampNonNeg(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

// mulDiv computes round-half-up(a*b/c) for non-negative a, b and positive c
// without intermediate overflow. Results beyond int64 saturate.
func mulDiv(a, b, c int64) int64 {
	if a <= 0 || b <= 0 || c <= 0 {
		return 0
	}
	x := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	x.Add(x, big.NewInt(c/2))
	x.Quo(x, big.NewInt(c))
	if !x.IsInt64() {
		return math.MaxInt64
	}
	return x.Int64()
}

// tokenAmount is the charge for tokens at rate nano-dollars per million
// tokens: (tokens*rate + 500000) / 1000000, zero when either is not positive.
func tokenAmount(tokens, rate int64) int64 {
	if tokens <= 0 || rate <= 0 {
		return 0
	}
	x := new(big.Int).Mul(big.NewInt(tokens), big.NewInt(rate))
	x.Add(x, big.NewInt(500000))
	x.Quo(x, big.NewInt(1000000))
	if !x.IsInt64() {
		return math.MaxInt64
	}
	return x.Int64()
}

// unitAmount is count*rate with saturation (per-call and per-second prices).
func unitAmount(count, rate int64) int64 {
	if count <= 0 || rate <= 0 {
		return 0
	}
	if count > math.MaxInt64/rate {
		return math.MaxInt64
	}
	return count * rate
}

// ratio is an exact fraction used for rate multipliers.
type ratio struct{ num, den int64 }

var one = ratio{1, 1}

// mul multiplies two fractions.
func (r ratio) mul(o ratio) ratio {
	n := new(big.Int).Mul(big.NewInt(r.num), big.NewInt(o.num))
	d := new(big.Int).Mul(big.NewInt(r.den), big.NewInt(o.den))
	g := new(big.Int).GCD(nil, nil, n, d)
	if g.Sign() > 0 {
		n.Quo(n, g)
		d.Quo(d, g)
	}
	if !n.IsInt64() || !d.IsInt64() {
		return r
	}
	return ratio{n.Int64(), d.Int64()}
}

func (r ratio) isOne() bool { return r.num == r.den }

// float is the multiplier as a number (snapshots).
func (r ratio) float() float64 {
	if r.den == 0 {
		return 1
	}
	return float64(r.num) / float64(r.den)
}

// apply scales a unit price: round-half-up(rate * num / den).
func (r ratio) apply(rate int64) int64 {
	if rate <= 0 {
		return 0
	}
	if r.isOne() || r.den == 0 {
		return rate
	}
	return mulDiv(rate, r.num, r.den)
}
