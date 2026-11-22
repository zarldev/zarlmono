package guardrails

import (
	"encoding/json"
	"math/big"
	"reflect"
	"strings"
)

// schemaDecimal represents coefficient * 10^exponent. Only the exponent
// itself uses big.Int: numeric magnitude must never determine allocation.
// A nonzero coefficient has neither leading nor trailing zeros.
type schemaDecimal struct {
	negative    bool
	coefficient string
	exponent    big.Int
}

func parseSchemaDecimal(s string) (schemaDecimal, bool) {
	var d schemaDecimal
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

// schemaEnumEqual widens only Number comparisons to mathematical decimal
// equality. All other operands retain Go equality, when their actual runtime
// values (including interface-bearing structs) are comparable.
func schemaEnumEqual(a, b any) bool {
	_, aNumber := a.(json.Number)
	_, bNumber := b.(json.Number)
	if aNumber || bNumber {
		x, xOK := schemaNumericDecimal(a)
		y, yOK := schemaNumericDecimal(b)
		if !xOK || !yOK {
			return false
		}
		if x.coefficient == "" || y.coefficient == "" {
			return x.coefficient == y.coefficient
		}
		return x.negative == y.negative && x.coefficient == y.coefficient && x.exponent.Cmp(&y.exponent) == 0
	}
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return reflect.ValueOf(a).Comparable() && reflect.ValueOf(b).Comparable() && a == b
}

func schemaNumericDecimal(value any) (schemaDecimal, bool) {
	switch value := value.(type) {
	case json.Number:
		return parseSchemaDecimal(string(value))
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, uintptr, float32, float64:
		// encoding/json supplies the legacy scalar's decimal representation,
		// rejecting non-finite floats. Strings never enter this conversion.
		raw, err := json.Marshal(value)
		if err == nil {
			return parseSchemaDecimal(string(raw))
		}
	}
	return schemaDecimal{}, false
}
