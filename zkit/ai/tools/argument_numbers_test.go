package tools_test

import (
	"encoding/json"
	"math"
	"strconv"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestNumberParameterConversions(t *testing.T) {
	t.Parallel()
	maxInt := int(^uint(0) >> 1)
	minInt := -maxInt - 1
	tests := []struct {
		raw  string
		want int
	}{
		{"42", 42}, {"42.0", 42}, {"42e0", 42}, {"4.2999e1", 42}, {"-42.999", -42},
		{"0.999999999999999999999999999999", 0}, {"-0.999999999999999999999999999999", 0},
		{"-0", 0}, {"-0e9999999999999999999999999", 0}, {"1e-9999999999999999999999999", 0},
		{"1e9999999999999999999999999", -77}, {"-1e9999999999999999999999999", -77},
		{strconv.Itoa(maxInt), maxInt}, {strconv.Itoa(minInt), minInt},
		{strconv.Itoa(maxInt) + ".999", maxInt}, {strconv.Itoa(minInt) + ".999", minInt},
		{"18446744073709551616", -77}, {"-18446744073709551616", -77},
		{"", -77}, {"01", -77}, {"+1", -77}, {"1 ", -77}, {" 1", -77}, {"NaN", -77}, {"Inf", -77},
		{"0x10", -77}, {"1/2", -77}, {"1.", -77}, {".1", -77}, {"1e", -77}, {"--1", -77},
	}
	if strconv.IntSize == 64 {
		tests = append(tests, struct {
			raw  string
			want int
		}{"9007199254740993", 9007199254740993})
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			t.Parallel()
			params := tools.ToolParameters{"n": json.Number(tt.raw)}
			if got := params.Int("n", -77); got != tt.want {
				t.Fatalf("Int(%q)=%d want %d", tt.raw, got, tt.want)
			}
		})
	}
	floats := []struct {
		raw  string
		want float64
	}{
		{"42.125", 42.125}, {"42e0", 42}, {"1e-999999999999999999999", 0}, {"1e999999999999999999999", -77},
		{"NaN", -77}, {"Inf", -77}, {"0x1p2", -77}, {"01", -77}, {"+1", -77}, {"1 ", -77}, {"", -77},
	}
	for _, tt := range floats {
		t.Run("float/"+tt.raw, func(t *testing.T) {
			t.Parallel()
			if got := (tools.ToolParameters{"n": json.Number(tt.raw)}).Float("n", -77); got != tt.want {
				t.Fatalf("Float=%v want %v", got, tt.want)
			}
		})
	}
	if got := (tools.ToolParameters{"n": json.Number("-0")}).Float("n", 1); !math.Signbit(got) {
		t.Fatal("lost negative float zero")
	}
	// Preserve the existing int/float64/string branches, including string syntax.
	legacy := tools.ToolParameters{"i": 7, "f": 8.75, "s": "+9", "nan": "NaN"}
	if legacy.Int("i", 0) != 7 || legacy.Int("f", 0) != 8 || legacy.Int("s", 0) != 9 || legacy.Float("i", 0) != 7 || legacy.Float("f", 0) != 8.75 || !math.IsNaN(legacy.Float("nan", 0)) {
		t.Fatal("legacy conversions changed")
	}
}

func TestIntegerArgumentWidths(t *testing.T) {
	t.Parallel()
	checkIntegerTokens[int8](t, "int8", 127, -128, []string{"127.0", "1.27e2"}, []string{"-128.0", "-1.28e2"}, []string{"128.0", "-129e0"})
	checkIntegerTokens[int16](t, "int16", 32767, -32768, []string{"32767.0"}, []string{"-32768e0"}, []string{"32768.0", "-32769e0"})
	checkIntegerTokens[int32](t, "int32", 2147483647, -2147483648, []string{"2147483647.0"}, []string{"-2147483648e0"}, []string{"2147483648.0", "-2147483649e0"})
	checkIntegerTokens[int64](t, "int64", 9223372036854775807, -9223372036854775808, []string{"9223372036854775807.0", "9.223372036854775807e18"}, []string{"-9223372036854775808e0"}, []string{"9223372036854775808.0", "-9223372036854775809e0"})
	checkIntegerTokens[int](t, "int", int(^uint(0)>>1), -int(^uint(0)>>1)-1, []string{strconv.Itoa(int(^uint(0)>>1)) + ".0"}, []string{strconv.Itoa(-int(^uint(0)>>1)-1) + "e0"}, nil)
	checkIntegerTokens[uint8](t, "uint8", 255, 0, []string{"255.0", "2.55e2"}, []string{"0.0", "0e9999999999999999999999999"}, []string{"256.0", "-0", "-0.0", "-0e0", "-1e-9999999999999999999999999"})
	checkIntegerTokens[uint16](t, "uint16", 65535, 0, []string{"65535.0"}, []string{"0e0"}, []string{"65536.0", "-1.0"})
	checkIntegerTokens[uint32](t, "uint32", 4294967295, 0, []string{"4294967295.0"}, []string{"0.0"}, []string{"4294967296.0"})
	checkIntegerTokens[uint64](t, "uint64", 18446744073709551615, 0, []string{"18446744073709551615.0", "1.8446744073709551615e19"}, []string{"0e0"}, []string{"18446744073709551616.0"})
	checkIntegerTokens[uint](t, "uint", ^uint(0), 0, []string{strconv.FormatUint(uint64(^uint(0)), 10) + ".0"}, []string{"0.0"}, []string{"-0.0"})
	checkIntegerTokens[uintptr](t, "uintptr", ^uintptr(0), 0, []string{strconv.FormatUint(uint64(^uintptr(0)), 10) + "e0"}, []string{"0.0"}, []string{"-0.0"})
}

func checkIntegerTokens[T comparable](t *testing.T, name string, high, low T, highTokens, lowTokens, rejected []string) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		t.Parallel()
		for _, group := range []struct {
			tokens []string
			want   T
		}{{highTokens, high}, {lowTokens, low}} {
			for _, raw := range group.tokens {
				got, err := tools.DecodeJSONArgs[T](json.RawMessage(raw))
				if err != nil || got != group.want {
					t.Errorf("%s: got %v, %v want %v", raw, got, err, group.want)
				}
			}
		}
		rejected = append(rejected, "42.000000000000000000000000000001", "1e-999999999999999999999999", "1e999999999999999999999999", "1.5", "1e-1")
		for _, raw := range rejected {
			if _, err := tools.DecodeJSONArgs[T](json.RawMessage(raw)); err == nil {
				t.Errorf("accepted %s", raw)
			}
		}
	})
}
