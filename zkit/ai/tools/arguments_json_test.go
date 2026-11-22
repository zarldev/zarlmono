package tools_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type lexicalJSON struct {
	Raw   string
	Calls int
}

func (v *lexicalJSON) UnmarshalJSON(raw []byte) error { v.Raw = string(raw); v.Calls++; return nil }

type lexicalText struct {
	Raw   string
	Calls int
}

func (v *lexicalText) UnmarshalText(raw []byte) error { v.Raw = string(raw); v.Calls++; return nil }

type lexicalInteger int64

func (v *lexicalInteger) UnmarshalJSON(raw []byte) error {
	if string(raw) != "42.0" {
		return fmt.Errorf("custom lexeme changed: %s", raw)
	}
	*v = 123
	return nil
}

func TestDecodeJSONArgsTargetAware(t *testing.T) {
	t.Parallel()
	type nested struct {
		N int64 `json:"n"`
	}
	type args struct {
		I         int64                `json:"i"`
		U         uint64               `json:"u"`
		Float     float64              `json:"float"`
		Any       any                  `json:"any"`
		Map       map[string]any       `json:"map"`
		Number    json.Number          `json:"number"`
		Raw       json.RawMessage      `json:"raw"`
		Custom    lexicalJSON          `json:"custom"`
		CustomInt lexicalInteger       `json:"custom_int"`
		Text      lexicalText          `json:"text"`
		Pointer   *nested              `json:"pointer"`
		TypedMap  map[string][]*nested `json:"typed_map"`
		Array     [2]int               `json:"array"`
		Strings   int                  `json:"strings,string"`
	}
	raw := json.RawMessage(` {"i":9007199254740993.0,"u":18446744073709551615e0,"float":9007199254740993.0,"any":9007199254740993.0,"map":{"n":42e0},"number":42.00e+0,"raw":{"n":42.00e+0,"n":43e0},"custom":{"n":42.00e+0,"n":43e0},"custom_int":42.0,"text":"42.00e+0","pointer":{"n":42.0},"typed_map":{"x":[{"n":43e0},null]},"array":[1.0,2e0,1e999999999999999999999],"strings":"42"} `)
	original := bytes.Clone(raw)
	got, err := tools.DecodeJSONArgs[args](raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.I != 9007199254740993 || got.U != 18446744073709551615 || got.Float != 9007199254740992 || got.Any != float64(9007199254740992) || got.Map["n"] != float64(42) {
		t.Fatalf("numeric semantics=%+v", got)
	}
	if got.Number != "42.00e+0" || string(got.Raw) != `{"n":42.00e+0,"n":43e0}` || got.Custom.Raw != string(got.Raw) || got.Custom.Calls != 1 || got.CustomInt != 123 || got.Text.Raw != "42.00e+0" || got.Text.Calls != 1 {
		t.Fatalf("lexeme/custom semantics=%+v", got)
	}
	if got.Pointer.N != 42 || got.TypedMap["x"][0].N != 43 || got.TypedMap["x"][1] != nil || got.Array != [2]int{1, 2} || got.Strings != 42 {
		t.Fatalf("containers=%+v", got)
	}
	if !bytes.Equal(raw, original) {
		t.Fatal("mutated authoritative raw bytes")
	}
	for _, bad := range []string{`{"strings":42.0}`, `{"strings":"42.0"}`, `{"strings":"42e0"}`} {
		if _, err := tools.DecodeJSONArgs[args](json.RawMessage(bad)); err == nil {
			t.Errorf("accepted ,string violation %s", bad)
		}
	}
	// Root custom decoders observe duplicate order and spelling, not a rebuilt map.
	custom, err := tools.DecodeJSONArgs[lexicalJSON](json.RawMessage(`{"z":1e0,"a":2.0,"z":3e0}`))
	if err != nil || custom.Calls != 1 || custom.Raw != `{"z":1e0,"a":2.0,"z":3e0}` {
		t.Fatalf("root custom=%+v err=%v", custom, err)
	}
}

func TestDecodeArgsNumberCompatibilityAndErrors(t *testing.T) {
	t.Parallel()
	type args struct {
		N      int64       `json:"n"`
		Any    any         `json:"any"`
		Number json.Number `json:"number"`
	}
	params := tools.ToolParameters{"n": json.Number("9007199254740993.0"), "any": json.Number("42e0"), "number": json.Number("42.00e0")}
	got, err := tools.DecodeArgs[args](params)
	if err != nil || got.N != 9007199254740993 || got.Any != float64(42) || got.Number != "42.00e0" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	// Ordinary legacy floating integral map parameters still reach typed ints.
	got, err = tools.DecodeArgs[args](tools.ToolParameters{"n": float64(42)})
	if err != nil || got.N != 42 {
		t.Fatalf("legacy float=%+v %v", got, err)
	}
	for _, params := range []tools.ToolParameters{{"n": json.Number("42.1")}, {"n": json.Number("9223372036854775808")}, {"n": json.Number("NaN")}, {"n": make(chan int)}} {
		_, err := tools.DecodeArgs[args](params)
		var typed *tools.Error
		if !errors.As(err, &typed) || typed.Kind != tools.Kinds.VALIDATION {
			t.Errorf("want Validation, got %v", err)
		}
	}
	// Do not change json.Marshal's existing empty Number -> 0 behavior.
	got, err = tools.DecodeArgs[args](tools.ToolParameters{"n": json.Number("")})
	if err != nil || got.N != 0 {
		t.Fatalf("empty hand-built Number=%+v %v", got, err)
	}
}

func TestDecodeJSONArgsStrictAndOrdinaryErrors(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"", `{"n":42,}`, `{"n":42`, "{'n':42}", `{"n":42} {"n":43}`} {
		got, err := tools.DecodeJSONArgs[lexicalJSON](json.RawMessage(raw))
		var syntax *json.SyntaxError
		var validation *tools.Error
		if !errors.As(err, &syntax) || errors.As(err, &validation) || got.Calls != 0 {
			t.Errorf("%q: calls=%d err=%v", raw, got.Calls, err)
		}
	}
	_, err := tools.DecodeJSONArgs[int64](json.RawMessage(`42.1`))
	var mismatch *json.UnmarshalTypeError
	if !errors.As(err, &mismatch) {
		t.Fatalf("typed error=%v", err)
	}
	type withCustom struct {
		N      int         `json:"n"`
		Custom lexicalJSON `json:"custom"`
	}
	got, err := tools.DecodeJSONArgs[withCustom](json.RawMessage(`{"n":1.5,"custom":42.0}`))
	if !errors.As(err, &mismatch) || got.Custom.Calls != 1 || got.Custom.Raw != "42.0" {
		t.Fatalf("custom retried on failure: %+v %v", got, err)
	}
}

type JSONPromoted struct {
	Promoted int `json:"promoted"`
}
type JSONConflictA struct {
	Conflict int
}
type JSONConflictB struct {
	Conflict json.Number
}
type JSONRepeated struct {
	Repeated int
}
type JSONLeft struct{ JSONRepeated }
type JSONRight struct{ JSONRepeated }
type JSONTagged struct {
	Chosen int `json:"Chosen"`
}
type JSONUntagged struct{ Chosen json.Number }
type jsonPrivate struct {
	Private int `json:"private"`
}
type JSONRecursive struct {
	*JSONRecursive
	R int `json:"r"`
}

func TestDecodeJSONArgsFieldResolution(t *testing.T) {
	t.Parallel()
	type fields struct {
		JSONPromoted
		JSONConflictA
		JSONConflictB
		JSONLeft
		JSONRight
		JSONTagged
		JSONUntagged
		jsonPrivate
		JSONRecursive
		Ignored    int         `json:"-"`
		Invalid    int         `json:"bad\\name"` //nolint:staticcheck // Exercise encoding/json's ignored invalid tag name.
		Upper      int         `json:"N"`
		Lower      json.Number `json:"n"`
		FoldFirst  int         `json:"First"`
		FoldSecond json.Number `json:"FIRST"`
		S          int         `json:"s,string"`
		Unicode    int         `json:"ſ"`
	}
	raw := json.RawMessage(`{"promoted":42.0,"conflict":1.5,"repeated":1.5,"Chosen":43e0,"private":44.0,"r":45e0,"Ignored":1.5,"Invalid":46.0,"N":47.0,"n":48.00,"first":49e0,"FIRST":50.00,"s":"51","S":"52","S":null,"ſ":53.0,"PROMOTED":54e0,"promoted":55.0}`)
	got, err := tools.DecodeJSONArgs[fields](raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Promoted != 55 || got.JSONConflictA.Conflict != 0 || got.JSONConflictB.Conflict != "" || got.JSONLeft.Repeated != 0 || got.JSONRight.Repeated != 0 || got.JSONTagged.Chosen != 43 || got.JSONUntagged.Chosen != "" || got.Private != 44 || got.R != 45 || got.Ignored != 0 || got.Upper != 47 || got.Lower != "48.00" || got.FoldFirst != 49 || got.FoldSecond != "50.00" || got.S != 52 || got.Unicode != 53 {
		t.Fatalf("resolved=%+v", got)
	}
	// Compare ordinary decode for the same field-resolution cases after changing
	// only integer-target tokens; Number fields keep their source spelling.
	ordinary := strings.NewReplacer("42.0", "42", "43e0", "43", "44.0", "44", "45e0", "45", "46.0", "46", "47.0", "47", "49e0", "49", "53.0", "53", "54e0", "54", "55.0", "55").Replace(string(raw))
	var want fields
	if err := json.Unmarshal([]byte(ordinary), &want); err != nil { //nolint:musttag // Anonymous field selection is the behavior under test.
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%+v want=%+v", got, want)
	}
	// Unicode simple folding must include long-s/S and Kelvin-sign/K.
	unicodeGot, err := tools.DecodeJSONArgs[struct {
		S int
		K int
	}](json.RawMessage(`{"ſ":42.0,"K":43e0}`))
	if err != nil || unicodeGot.S != 42 || unicodeGot.K != 43 {
		t.Fatalf("unicode=%+v %v", unicodeGot, err)
	}
}

type integerAlias int16

type lexicalContainer map[string]int

func (v *lexicalContainer) UnmarshalJSON(raw []byte) error {
	if string(raw) != `{"n":42.00e0}` {
		return fmt.Errorf("custom container lexeme changed: %s", raw)
	}
	*v = lexicalContainer{"calls": 1}
	return nil
}

func TestDecodeJSONArgsNestedOpaqueAndIntegerContainers(t *testing.T) {
	t.Parallel()
	type args struct {
		Int     *integerAlias              `json:"int"`
		Array   *[2]uint8                  `json:"array"`
		Map     map[int][]integerAlias     `json:"map"`
		Numbers []json.Number              `json:"numbers"`
		Raws    map[string]json.RawMessage `json:"raws"`
		Custom  []*lexicalContainer        `json:"custom"`
		JSON    map[string]lexicalJSON     `json:"json"`
	}
	raw := json.RawMessage(`{"int":42.0,"array":[43e0,44.00],"map":{"1":[45.0,46e0]},"numbers":[47.00,4.8e1],"raws":{"a":{"n":49.00,"n":50e0}},"custom":[{"n":42.00e0},null],"json":{"x":51.00e0}}`)
	got, err := tools.DecodeJSONArgs[args](raw)
	if err != nil {
		t.Fatal(err)
	}
	if *got.Int != 42 || *got.Array != [2]uint8{43, 44} || !reflect.DeepEqual(got.Map[1], []integerAlias{45, 46}) {
		t.Fatalf("integer containers=%+v", got)
	}
	if !reflect.DeepEqual(got.Numbers, []json.Number{"47.00", "4.8e1"}) || string(got.Raws["a"]) != `{"n":49.00,"n":50e0}` || (*got.Custom[0])["calls"] != 1 || got.Custom[1] != nil || got.JSON["x"].Raw != "51.00e0" || got.JSON["x"].Calls != 1 {
		t.Fatalf("opaque containers=%+v", got)
	}
	// Unconstrained destinations retain ordinary float64 failure semantics.
	for _, raw := range []string{`1e99999999999999999999999`, `{"n":1e99999999999999999999999}`} {
		_, err := tools.DecodeJSONArgs[any](json.RawMessage(raw))
		var mismatch *json.UnmarshalTypeError
		if !errors.As(err, &mismatch) {
			t.Errorf("ordinary any accepted overflow: %s %v", raw, err)
		}
	}
	number, err := tools.DecodeJSONArgs[json.Number](json.RawMessage(`1e99999999999999999999999`))
	if err != nil || number != "1e99999999999999999999999" {
		t.Fatalf("Number range/spelling=%s %v", number, err)
	}
}
