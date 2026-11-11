// Package money formats rupiah amounts. Stdlib only, plain data in and out.
package money

import "strconv"

// Prefix is the currency symbol with its trailing space.
const Prefix = "Rp "

// group writes the digits of |v| with sep between thousands; neg reports the sign.
func group(v int64, sep string) (d string, neg bool) {
	d = strconv.FormatInt(v, 10)
	if v < 0 {
		d, neg = d[1:], true
	}
	for i := len(d) - 3; i > 0; i -= 3 {
		d = d[:i] + sep + d[i:]
	}
	return d, neg
}

// Format is the UI form: the sign before the symbol, "-Rp 1.000".
func Format(v int64, sep string) string {
	d, neg := group(v, sep)
	if neg {
		return "-" + Prefix + d
	}
	return Prefix + d
}

// Message is the form used in sent messages: dot separators and the sign after the symbol, "Rp -1.000".
func Message(v int64) string {
	d, neg := group(v, ".")
	if neg {
		return Prefix + "-" + d
	}
	return Prefix + d
}
