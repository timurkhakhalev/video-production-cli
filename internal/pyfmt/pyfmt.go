// Package pyfmt reproduces the few Python formatting and rounding rules render.py output
// depends on, so the Go port prints byte-identical text.
package pyfmt

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// Round rounds like Python's round(x, ndigits) for floats: the exact binary value is
// rounded half-to-even in decimal.
func Round(x float64, ndigits int) float64 {
	if math.IsInf(x, 0) || math.IsNaN(x) {
		return x
	}
	v, _ := strconv.ParseFloat(strconv.FormatFloat(x, 'f', ndigits, 64), 64)
	return v
}

// RoundInt rounds like Python's round(x) for floats: half to even.
func RoundInt(x float64) int64 { return int64(math.RoundToEven(x)) }

// DivMod is Python's float divmod (CPython float_divmod).
func DivMod(x, y float64) (float64, float64) {
	mod := math.Mod(x, y)
	div := (x - mod) / y
	if mod != 0 {
		if (y < 0) != (mod < 0) {
			mod += y
			div -= 1
		}
	} else {
		mod = math.Copysign(0, y)
	}
	var floordiv float64
	if div != 0 {
		floordiv = math.Floor(div)
		if div-floordiv > 0.5 {
			floordiv += 1
		}
	} else {
		floordiv = math.Copysign(0, x/y)
	}
	return floordiv, mod
}

// TC is render.py's fmt_tc: m:ss.sss or h:mm:ss.sss.
func TC(seconds float64) string {
	seconds = Round(math.Max(0, seconds), 3)
	m, s := DivMod(seconds, 60)
	h, mi := int64(m)/60, int64(m)%60
	if h != 0 {
		return fmt.Sprintf("%d:%02d:%06.3f", h, mi, s)
	}
	return fmt.Sprintf("%d:%06.3f", mi, s)
}

// G is Python's "%g".
func G(x float64) string {
	switch {
	case math.IsInf(x, 1):
		return "inf"
	case math.IsInf(x, -1):
		return "-inf"
	case math.IsNaN(x):
		return "nan"
	}
	return strconv.FormatFloat(x, 'g', 6, 64)
}

// F is Python's "%.<prec>f", including inf/nan spelling.
func F(x float64, prec int) string {
	switch {
	case math.IsInf(x, 1):
		return "inf"
	case math.IsInf(x, -1):
		return "-inf"
	case math.IsNaN(x):
		return "nan"
	}
	return strconv.FormatFloat(x, 'f', prec, 64)
}

// Repr is Python's repr(float): shortest round-trip digits, fixed notation for
// 1e-4 <= |x| < 1e16 (always with a ".0"), scientific otherwise.
func Repr(x float64) string {
	switch {
	case math.IsInf(x, 1):
		return "inf"
	case math.IsInf(x, -1):
		return "-inf"
	case math.IsNaN(x):
		return "nan"
	}
	e := strconv.FormatFloat(x, 'e', -1, 64)
	exp, _ := strconv.Atoi(e[strings.IndexByte(e, 'e')+1:])
	if x == 0 || (exp >= -4 && exp < 16) {
		f := strconv.FormatFloat(x, 'f', -1, 64)
		if !strings.ContainsAny(f, ".") {
			f += ".0"
		}
		return f
	}
	return e
}

// ParseFloat is Python's float(str) for the inputs we accept (surrounding spaces allowed).
func ParseFloat(s string) (float64, error) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, fmt.Errorf("could not convert string to float: %q", s)
	}
	return v, nil
}

// LimitDenominator is fractions.Fraction.limit_denominator.
func LimitDenominator(r *big.Rat, max int64) *big.Rat {
	maxD := big.NewInt(max)
	if r.Denom().Cmp(maxD) <= 0 {
		return new(big.Rat).Set(r)
	}
	p0, q0, p1, q1 := big.NewInt(0), big.NewInt(1), big.NewInt(1), big.NewInt(0)
	n, d := new(big.Int).Set(r.Num()), new(big.Int).Set(r.Denom())
	a, q2, t := new(big.Int), new(big.Int), new(big.Int)
	for {
		floorDiv(a, n, d)
		q2.Mul(a, q1).Add(q2, q0)
		if q2.Cmp(maxD) > 0 {
			break
		}
		newP1 := new(big.Int).Mul(a, p1)
		newP1.Add(newP1, p0)
		p0, q0, p1, q1 = p1, q1, newP1, new(big.Int).Set(q2)
		t.Mul(a, d)
		n, d = d, new(big.Int).Sub(n, t)
	}
	k := new(big.Int).Sub(maxD, q0)
	floorDiv(k, k, q1)
	b1n := new(big.Int).Mul(k, p1)
	b1n.Add(b1n, p0)
	b1d := new(big.Int).Mul(k, q1)
	b1d.Add(b1d, q0)
	bound1 := new(big.Rat).SetFrac(b1n, b1d)
	bound2 := new(big.Rat).SetFrac(p1, q1)
	d2 := new(big.Rat).Sub(bound2, r)
	d1 := new(big.Rat).Sub(bound1, r)
	if d2.Abs(d2).Cmp(d1.Abs(d1)) <= 0 {
		return bound2
	}
	return bound1
}

// floorDiv is x // y for y > 0 (big.Int.Div is Euclidean, which equals floor there).
func floorDiv(z, x, y *big.Int) *big.Int { return z.Div(x, y) }

// RoundRat is round(Fraction): nearest integer, ties to even.
func RoundRat(r *big.Rat) int64 {
	num, den := r.Num(), r.Denom()
	fl, rem := new(big.Int), new(big.Int)
	fl.DivMod(num, den, rem) // den > 0, so floor
	rem.Mul(rem, big.NewInt(2))
	switch c := rem.Cmp(den); {
	case c < 0:
	case c > 0:
		fl.Add(fl, big.NewInt(1))
	default:
		if fl.Bit(0) == 1 {
			fl.Add(fl, big.NewInt(1))
		}
	}
	return fl.Int64()
}

// RatFloat converts a rational to the nearest float64, like float(Fraction).
func RatFloat(r *big.Rat) float64 {
	f, _ := r.Float64()
	return f
}
