package checks

import (
	"math/big"
	"strings"
)

// Dec is an exact decimal: the value as a rational plus the text it was read
// from, so explanations can quote values the way the message wrote them.
// Money and quantities never touch float64.
type Dec struct {
	R    *big.Rat
	Text string
}

// ParseDec reads a value the way Python's Decimal(str(value)) does for the
// inputs FIX carries: surrounding whitespace is ignored, every underscore is
// dropped, an optional sign, digits with an optional point, an optional
// exponent. NaN and Infinity are not finite and so, as in the Python checks,
// count as "no value". ok is false for anything else.
func ParseDec(s string) (Dec, bool) {
	t := strings.TrimSpace(strings.ReplaceAll(s, "_", ""))
	if t == "" {
		return Dec{}, false
	}
	i := 0
	if t[0] == '+' || t[0] == '-' {
		i++
	}
	intDigits, fracDigits := 0, 0
	for i < len(t) && isDigit(t[i]) {
		i++
		intDigits++
	}
	if i < len(t) && t[i] == '.' {
		i++
		for i < len(t) && isDigit(t[i]) {
			i++
			fracDigits++
		}
	}
	if intDigits+fracDigits == 0 {
		return Dec{}, false
	}
	if i < len(t) && (t[i] == 'e' || t[i] == 'E') {
		i++
		if i < len(t) && (t[i] == '+' || t[i] == '-') {
			i++
		}
		expDigits := 0
		for i < len(t) && isDigit(t[i]) {
			i++
			expDigits++
		}
		if expDigits == 0 {
			return Dec{}, false
		}
	}
	if i != len(t) {
		return Dec{}, false
	}
	r, ok := new(big.Rat).SetString(t)
	if !ok {
		return Dec{}, false
	}
	return Dec{R: r, Text: s}, true
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// DecOf returns the decimal of a field value, or ok=false if it is absent or
// not a finite number.
func DecOf(value string, present bool) (Dec, bool) {
	if !present {
		return Dec{}, false
	}
	return ParseDec(value)
}

// PyInt parses the way Python's int() does on text: ASCII whitespace around
// it, an optional sign, digits with single underscores between them.
func PyInt(s string) (int, bool) {
	t := strings.Trim(s, " \t\n\r\x0b\x0c")
	if t == "" {
		return 0, false
	}
	neg := false
	if t[0] == '+' || t[0] == '-' {
		neg = t[0] == '-'
		t = t[1:]
	}
	if t == "" || t[0] == '_' || t[len(t)-1] == '_' || strings.Contains(t, "__") {
		return 0, false
	}
	n := 0
	for i := 0; i < len(t); i++ {
		c := t[i]
		if c == '_' {
			continue
		}
		if !isDigit(c) {
			return 0, false
		}
		n = n*10 + int(c-'0')
		if n > 1<<40 {
			return 0, false
		}
	}
	if neg {
		n = -n
	}
	return n, true
}

// intOfDec is Python's int(Decimal): truncation toward zero.
func intOfDec(value string, present bool) (int, bool) {
	d, ok := DecOf(value, present)
	if !ok {
		return 0, false
	}
	q := new(big.Int).Quo(d.R.Num(), d.R.Denom())
	if !q.IsInt64() {
		return 0, false
	}
	return int(q.Int64()), true
}

// FormatRat renders r with exactly scale decimal places (rounded half-even,
// as Python's quantize does by default).
func FormatRat(r *big.Rat, scale int) string {
	if r == nil {
		return ""
	}
	mult := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
	num := new(big.Int).Mul(r.Num(), mult)
	den := r.Denom()
	q, m := new(big.Int).QuoRem(num, den, new(big.Int))
	// half-even rounding on the remainder
	twice := new(big.Int).Mul(new(big.Int).Abs(m), big.NewInt(2))
	cmp := twice.Cmp(den)
	if cmp > 0 || (cmp == 0 && q.Bit(0) == 1) {
		if num.Sign() < 0 {
			q.Sub(q, big.NewInt(1))
		} else {
			q.Add(q, big.NewInt(1))
		}
	}
	neg := q.Sign() < 0
	digits := new(big.Int).Abs(q).String()
	if scale > 0 {
		for len(digits) <= scale {
			digits = "0" + digits
		}
		digits = digits[:len(digits)-scale] + "." + digits[len(digits)-scale:]
	}
	if neg {
		digits = "-" + digits
	}
	return digits
}

// ratText renders an exact computed value compactly (as few places as it
// needs, up to 10) for explanations.
func ratText(r *big.Rat) string {
	if r.IsInt() {
		return r.Num().String()
	}
	s := FormatRat(r, 10)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}
