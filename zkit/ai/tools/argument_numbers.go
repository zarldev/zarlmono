package tools

import (
	"encoding/json"
	"math/big"
	"strconv"
	"strings"
)

// argumentDecimal represents coefficient * 10^exponent. Only the exponent
// itself uses big.Int: numeric magnitude must never determine allocation.
// A nonzero coefficient has neither leading nor trailing zeros.
type argumentDecimal struct {
	negative    bool
	coefficient string
	exponent    big.Int
}

func parseArgumentDecimal(s string) (argumentDecimal, bool) {
	var d argumentDecimal
	if s == "" || strings.ContainsAny(s, " \t\r\n") || !json.Valid([]byte(s)) || s[0] != '-' && (s[0] < '0' || s[0] > '9') {
		return d, false
	}
	if s[0] == '-' {
		d.negative = true
		s = s[1:]
	}
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		d.exponent.SetString(s[i+1:], 10)
		s = s[:i]
	}
	if i := strings.IndexByte(s, '.'); i >= 0 {
		d.exponent.Sub(&d.exponent, big.NewInt(int64(len(s)-i-1)))
		s = s[:i] + s[i+1:]
	}
	s = strings.TrimLeft(s, "0")
	d.coefficient = strings.TrimRight(s, "0")
	if d.coefficient == "" {
		d.exponent.SetInt64(0)
	} else {
		d.exponent.Add(&d.exponent, big.NewInt(int64(len(s)-len(d.coefficient))))
	}
	return d, true
}

// integer bounds the decimal expansion before allocating, then lets strconv
// enforce the destination's exact signed/unsigned range. truncate is used only
// by ToolParameters.Int, not the typed argument boundary.
func (d argumentDecimal) integer(bits int, unsigned, truncate bool) (string, bool) {
	if unsigned && d.negative {
		return "", false // encoding/json rejects negative zero for unsigned too.
	}
	coefficient := d.coefficient
	if coefficient == "" {
		if d.negative {
			return "-0", true
		}
		return "0", true
	}
	if d.exponent.Sign() < 0 {
		if !truncate {
			return "", false
		}
		var places big.Int
		places.Neg(&d.exponent)
		if places.Cmp(big.NewInt(int64(len(coefficient)))) >= 0 {
			return "0", true
		}
		coefficient = coefficient[:len(coefficient)-int(places.Int64())]
	} else {
		// Bound expansion by this destination's decimal width, then check its
		// exact range below. Even a symbolic enormous exponent allocates no zeros.
		limit := uint64(1) << (bits - 1)
		if unsigned {
			limit = ^uint64(0) >> (64 - bits)
		}
		maxDigits := len(strconv.FormatUint(limit, 10))
		if len(coefficient) > maxDigits || d.exponent.Cmp(big.NewInt(int64(maxDigits-len(coefficient)))) > 0 {
			return "", false
		}
		coefficient += strings.Repeat("0", int(d.exponent.Int64()))
	}
	if d.negative {
		coefficient = "-" + coefficient
	}
	if unsigned {
		_, err := strconv.ParseUint(coefficient, 10, bits)
		return coefficient, err == nil
	}
	_, err := strconv.ParseInt(coefficient, 10, bits)
	return coefficient, err == nil
}
