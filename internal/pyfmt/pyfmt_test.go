package pyfmt

import (
	"math/big"
	"testing"
)

// Expected values were produced by CPython 3.9.
func TestTC(t *testing.T) {
	cases := map[float64]string{
		0: "0:00.000", -3: "0:00.000", 59.9996: "1:00.000", 59.9994: "0:59.999", 61.5: "1:01.500",
		3600: "1:00:00.000", 3725.0625: "1:02:05.062", 2828.033008: "47:08.033",
	}
	for in, want := range cases {
		if got := TC(in); got != want {
			t.Errorf("TC(%v) = %s, want %s", in, got, want)
		}
	}
}

func TestRoundHalfEven(t *testing.T) {
	if Round(0.0625, 3) != 0.062 || Round(2.675, 2) != 2.67 || Round(1729.3666666, 6) != 1729.366667 {
		t.Fatal("Round")
	}
	if RoundInt(2.5) != 2 || RoundInt(3.5) != 4 || RoundInt(-0.5) != 0 {
		t.Fatal("RoundInt")
	}
	if RoundRat(big.NewRat(5, 2)) != 2 || RoundRat(big.NewRat(7, 2)) != 4 || RoundRat(big.NewRat(-5, 2)) != -2 {
		t.Fatal("RoundRat")
	}
}

func TestReprAndG(t *testing.T) {
	for in, want := range map[float64]string{0: "0.0", 600: "600.0", 1e-5: "1e-05", 0.0001: "0.0001",
		1e16: "1e+16", 2115.733: "2115.733", 1.0 / 3: "0.3333333333333333"} {
		if got := Repr(in); got != want {
			t.Errorf("Repr(%v) = %s, want %s", in, got, want)
		}
	}
	for in, want := range map[float64]string{-32: "-32", 0.08: "0.08", 30: "30", 30000.0 / 1001: "29.97",
		100000: "100000", 1e6: "1e+06", 0.00001: "1e-05"} {
		if got := G(in); got != want {
			t.Errorf("G(%v) = %s, want %s", in, got, want)
		}
	}
}

func TestLimitDenominator(t *testing.T) {
	// Fraction(2115.733).limit_denominator(100000) == Fraction(2115733, 1000)
	r := new(big.Rat).SetFloat64(2115.733)
	if got := LimitDenominator(r, 100000); got.Cmp(big.NewRat(2115733, 1000)) != 0 {
		t.Fatalf("got %v", got)
	}
	// Fraction(math.pi).limit_denominator(1000) == Fraction(355, 113)
	if got := LimitDenominator(new(big.Rat).SetFloat64(3.141592653589793), 1000); got.Cmp(big.NewRat(355, 113)) != 0 {
		t.Fatalf("got %v", got)
	}
}
