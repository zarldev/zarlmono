//go:build goexperiment.jsonv2

package tools_test

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/toolkit"
)

type runtimeInteger int64

var _ jsonv2.UnmarshalerFrom = (*runtimeInteger)(nil)

func (v *runtimeInteger) UnmarshalJSONFrom(d *jsontext.Decoder) error {
	raw, err := d.ReadValue()
	if err != nil {
		return err
	}
	if string(raw) != "42.00" {
		return fmt.Errorf("streaming integer lexeme changed: %s", raw)
	}
	*v++
	return nil
}

type runtimeValue int

var errRuntimeDecoder = errors.New("runtime decoder sentinel")

func (runtimeValue) UnmarshalJSONFrom(*jsontext.Decoder) error { return errRuntimeDecoder }

type runtimePriority []string

func (v *runtimePriority) UnmarshalJSONFrom(d *jsontext.Decoder) error {
	if d.PeekKind() == '0' {
		return errors.ErrUnsupported
	}
	raw, err := d.ReadValue()
	if err != nil {
		return err
	}
	*v = append(*v, "from:"+string(raw))
	return nil
}
func (v *runtimePriority) UnmarshalJSON(raw []byte) error {
	*v = append(*v, "json:"+string(raw))
	return nil
}
func (*runtimePriority) UnmarshalText([]byte) error { return errRuntimeDecoder }

type runtimeKey string

func (v runtimeKey) AppendText(dst []byte) ([]byte, error) { return append(dst, string(v)...), nil }

type RuntimePromoted struct{ runtimeInteger }
type RuntimeDeep struct {
	Deep json.Number `json:"foo"`
}
type RuntimeEarly struct{ RuntimeDeep }
type RuntimeLater struct {
	Shallow int `json:"Foo"`
}
type RuntimeEmbed struct {
	N int `json:"n"`
}
type RuntimeRepeat struct {
	N int
}
type RuntimeLeft struct{ RuntimeRepeat }
type RuntimeRight struct{ RuntimeRepeat }
type RuntimeFallback struct {
	Values map[string]int `json:",embed"`
}
type runtimePrivate struct {
	N int `json:"n"`
}
type runtimeMethods struct {
	N int `json:"n"`
}

func (*runtimeMethods) UnmarshalJSONFrom(*jsontext.Decoder) error { return errRuntimeDecoder }

type RuntimeFormat struct {
	Lost int `json:"lost,format:'a,b,case:strict'"`
}

// runtimeCompare binds strict, map, and toolkit behavior to the actually selected
// ordinary decoder. normalized differs only for intentional integer accommodation;
// all metadata/custom-boundary oracles use identical raw documents.
func runtimeCompare[T any](t *testing.T, raw, normalized string) {
	t.Helper()
	var want T
	wantErr := json.Unmarshal([]byte(normalized), &want)
	got, err := tools.DecodeJSONArgs[T](json.RawMessage(raw))
	if !reflect.DeepEqual(got, want) || (err == nil) != (wantErr == nil) || reflect.TypeOf(err) != reflect.TypeOf(wantErr) {
		t.Fatalf("strict raw=%s: got=%#v %v; ordinary=%#v %v", raw, got, err, want, wantErr)
	}
	if errors.Is(wantErr, errRuntimeDecoder) && !errors.Is(err, errRuntimeDecoder) {
		t.Fatalf("strict lost sentinel: %v", err)
	}
	called := false
	tool := toolkit.Tool[T, T]{Func: func(_ context.Context, args T) (T, error) { called = true; return args, nil }}
	result, toolErr := tool.Call(t.Context(), json.RawMessage(raw))
	if (toolErr == nil) != (wantErr == nil) || called != (wantErr == nil) || (toolErr == nil && !reflect.DeepEqual(result, want)) {
		t.Fatalf("toolkit got=%#v %v called=%v; ordinary=%#v %v", result, toolErr, called, want, wantErr)
	}
	if errors.Is(wantErr, errRuntimeDecoder) && !errors.Is(toolErr, errRuntimeDecoder) {
		t.Fatalf("toolkit lost sentinel: %v", toolErr)
	}
	// The map API's input has already lost duplicate keys/order. Compare only
	// object documents after its ordinary marshal boundary, using UseNumber.
	if raw == normalized && strings.HasPrefix(strings.TrimSpace(raw), "{") && json.Valid([]byte(raw)) {
		var params tools.ToolParameters
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&params); err != nil {
			t.Fatal(err)
		}
		mapped, err := tools.DecodeArgs[T](params)
		wire, marshalErr := json.Marshal(params)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		var mapWant T
		mapErr := json.Unmarshal(wire, &mapWant)
		if !reflect.DeepEqual(mapped, mapWant) || (err == nil) != (mapErr == nil) {
			t.Fatalf("map wire=%s: got=%#v %v; ordinary=%#v %v", wire, mapped, err, mapWant, mapErr)
		}
		if mapErr != nil {
			var validation *tools.Error
			if !errors.As(err, &validation) || validation.Kind != tools.Kinds.VALIDATION {
				t.Fatalf("map error not Validation: %v", err)
			}
		}
	}
}

func TestDecodeJSONArgsRuntimeStreamingBoundaries(t *testing.T) {
	t.Parallel()
	runtimeCompare[runtimeInteger](t, `42.00`, `42.00`)
	runtimeCompare[*runtimeInteger](t, `42.00`, `42.00`)
	runtimeCompare[***runtimeInteger](t, `42.00`, `42.00`)
	runtimeCompare[runtimeValue](t, `42.00`, `42.00`)
	runtimeCompare[*runtimeValue](t, `42.00`, `42.00`)
	runtimeCompare[RuntimePromoted](t, `42.00`, `42.00`)
	type containers struct {
		N runtimeInteger            `json:"n"`
		P ***runtimeInteger         `json:"p"`
		M map[string]runtimeInteger `json:"m"`
		S []*runtimeInteger         `json:"s"`
		A [2]runtimeInteger         `json:"a"`
	}
	runtimeCompare[containers](t, `{"n":42.00,"p":42.00,"m":{"x":42.00},"s":[42.00,null],"a":[42.00,42.00,99.00]}`, `{"n":42.00,"p":42.00,"m":{"x":42.00},"s":[42.00,null],"a":[42.00,42.00,99.00]}`)
	runtimeCompare[runtimePriority](t, `"42.00"`, `"42.00"`)
	runtimeCompare[runtimePriority](t, `42.00`, `42.00`)
	runtimeCompare[struct {
		V runtimeValue   `json:"v"`
		N runtimeInteger `json:"n"`
	}](t, `{"v":42.00,"n":42.00}`, `{"v":42.00,"n":42.00}`)
	for _, raw := range []string{`42.00 1`, `{"n":42.00,}`, `{"n":42.00} {}`} {
		runtimeCompare[runtimeInteger](t, raw, raw)
		runtimeCompare[containers](t, raw, raw)
	}
	// Assert priority explicitly as well as through the ordinary-runtime oracle.
	priority, err := tools.DecodeJSONArgs[runtimePriority](json.RawMessage(`"42.00"`))
	if err != nil || !reflect.DeepEqual(priority, runtimePriority{`from:"42.00"`}) {
		t.Fatalf("priority=%v %v", priority, err)
	}
	fallback, err := tools.DecodeJSONArgs[runtimePriority](json.RawMessage(`42.00`))
	if err != nil || !reflect.DeepEqual(fallback, runtimePriority{"json:42.00"}) {
		t.Fatalf("fallback=%v %v", fallback, err)
	}
}

func TestDecodeJSONArgsRuntimeFieldSelection(t *testing.T) {
	t.Parallel()
	type strict struct {
		Strict   int         `json:"Foo,case:strict"`
		Flexible json.Number `json:"foo"`
	}
	for _, raw := range []string{`{"FOO":42.00}`, `{"FoO":42.00,"foo":43.00}`, `{"foo":42.00,"foo":43.00}`} {
		runtimeCompare[strict](t, raw, raw)
	}
	runtimeCompare[strict](t, `{"Foo":42.00,"FOO":43.00}`, `{"Foo":42,"FOO":43.00}`)
	runtimeCompare[struct {
		N int `json:"n,case:strict"`
	}](t, `{"N":42.00}`, `{"N":42.00}`)
	runtimeCompare[struct {
		RuntimeEarly
		RuntimeLater
	}](t, `{"FOO":42.00}`, `{"FOO":42}`)
	runtimeCompare[struct {
		RuntimeEarly
		RuntimeLater
	}](t, `{"foo":42.00}`, `{"foo":42.00}`)
	type unicodeFields struct {
		Strict   int         `json:"K,case:strict"`
		Flexible json.Number `json:"k,case:ignore"`
		S        json.Number `json:"s"`
	}
	runtimeCompare[unicodeFields](t, `{"\u212a":42.00,"ſ":43.00}`, `{"\u212a":42.00,"ſ":43.00}`)
	type delimiters struct {
		I     int         `json:"foo_bar,case:ignore"`
		D     json.Number `json:"foo-bar"`
		Plain json.Number `json:"foobar"`
	}
	runtimeCompare[delimiters](t, `{"FOO-BAR":42.00,"FOOBAR":43.00}`, `{"FOO-BAR":42.00,"FOOBAR":43.00}`)
	runtimeCompare[delimiters](t, `{"FOO_BAR":42.00}`, `{"FOO_BAR":42}`)
}

func TestDecodeJSONArgsRuntimeTagGrammar(t *testing.T) {
	t.Parallel()
	type grammar struct {
		Wide       int         `json:"hello♥world"`
		Slash      int         `json:"a\\name"`                     //nolint:staticcheck // Malformed-name best-effort fallback is under test.
		Quote      json.Number `json:"'quoted'"`                    //nolint:staticcheck // Single-quoted names are not enabled by this runtime.
		Dollar     json.Number `json:"$a\\bad"`                     //nolint:staticcheck // Invalid initial option falls back to field name.
		Strict     int         `json:"Foo,case:strict,case:strict"` //nolint:staticcheck // Exercise the selected runtime's best-effort tag handling.
		Flexible   json.Number `json:"foo,case:ignore"`
		Both       int         `json:"both,case:strict,case:ignore"` //nolint:staticcheck // Exercise the selected runtime's best-effort tag handling.
		BrokenCase int         `json:"broken,case:'strict'"`         //nolint:staticcheck // Quoted case values are malformed and not applied.
	}
	runtimeCompare[grammar](t, `{"hello♥world":42.00,"a":43.00,"Quote":44.00,"'quoted'":45.00,"Dollar":46.00,"FOO":47.00,"BOTH":48.00,"BROKEN":49.00}`, `{"hello♥world":42,"a":43,"Quote":44.00,"'quoted'":45.00,"Dollar":46.00,"FOO":47.00,"BOTH":48,"BROKEN":49}`)
	// Malformed/unknown options are best effort, not an excuse to make the
	// entire object opaque and silently drop integer compatibility.
	type bestEffort struct {
		N int   `json:"n,inline,unknown,omitEmpty,case:invalid"` //nolint:staticcheck // Exercise the selected runtime's best-effort tag handling.
		C []int `json:"c,string,omitzero,omitempty"`             //nolint:staticcheck // Exercise the selected runtime's best-effort tag handling.
	}
	runtimeCompare[bestEffort](t, `{"n":42.00,"c":[43.00]}`, `{"n":42,"c":[43]}`)
	type nonspecial struct {
		E RuntimeEmbed   `json:",inline"`
		M map[string]int `json:",unknown"`
	}
	runtimeCompare[nonspecial](t, `{"n":42.00,"x":43.00}`, `{"n":42.00,"x":43.00}`)
	runtimeCompare[nonspecial](t, `{"E":{"n":42.00},"M":{"x":43.00}}`, `{"E":{"n":42},"M":{"x":43}}`)
}

func TestDecodeJSONArgsRuntimeEmbeddedFallbacks(t *testing.T) {
	t.Parallel()
	type typed struct {
		Known json.Number    `json:"known"`
		Extra map[string]int `json:",embed"`
	}
	runtimeCompare[typed](t, `{"known":42.00,"x":43.00,"x":44e0}`, `{"known":42.00,"x":43,"x":44}`)
	runtimeCompare[struct {
		E RuntimeEmbed `json:",embed"`
	}](t, `{"n":42.00}`, `{"n":42}`)
	runtimeCompare[struct {
		E RuntimeEmbed `json:"named,embed"`
	}](t, `{"named":{"n":42.00},"n":43.00}`, `{"named":{"n":42},"n":43.00}`)
	runtimeCompare[struct {
		E RuntimeEmbed `json:",embed,case:strict"`
	}](t, `{"N":42.00}`, `{"N":42}`)
	runtimeCompare[struct {
		Extra map[string]runtimeInteger `json:",embed"`
	}](t, `{"x":42.00}`, `{"x":42.00}`)
	runtimeCompare[struct {
		Extra map[runtimeKey]int `json:",embed"`
	}](t, `{"Extra":{"x":42.00},"x":43.00}`, `{"Extra":{"x":42},"x":43.00}`)
	runtimeCompare[struct {
		E RuntimeEmbed `json:",embed,format:'ignored'"`
	}](t, `{"n":42.00}`, `{"n":42}`)
	runtimeCompare[struct {
		Raw jsontext.Value `json:",embed"`
		N   int            `json:"n"`
	}](t, `{"x":42.00,"y":{"n":43.00},"x":44.00}`, `{"x":42.00,"y":{"n":43.00},"x":44.00}`)
	runtimeCompare[struct {
		A map[string]int         `json:",embed"`
		B map[string]json.Number `json:",embed"`
	}](t, `{"x":42.00}`, `{"x":42.00}`)
	runtimeCompare[struct {
		RuntimeLeft
		RuntimeRight
	}](t, `{"n":42.00}`, `{"n":42.00}`)
	runtimeCompare[struct {
		RuntimeFallback
		Shallow map[string]json.Number `json:",embed"`
	}](t, `{"x":42.00}`, `{"x":42.00}`)
	runtimeCompare[struct{ *runtimePrivate }](t, `{"n":42.00}`, `{"n":42}`)
	runtimeCompare[struct {
		X *runtimeMethods `json:"x"`
	}](t, `{"x":{"n":42.00}}`, `{"x":{"n":42.00}}`)
	runtimeCompare[struct {
		runtimeMethods `json:"x"`
	}](t, `{"x":{"n":42.00}}`, `{"x":{"n":42.00}}`)
	// Invalid embedded scalar and non-string-key map retain ordinary fields.
	runtimeCompare[struct {
		N int         `json:",embed"`
		M map[int]int `json:",embed"`
	}](t, `{"N":42.00,"M":{"1":43.00}}`, `{"N":42,"M":{"1":43}}`)
}

func TestDecodeJSONArgsRuntimeUnsupportedFormats(t *testing.T) {
	t.Parallel()
	type format struct {
		Callback runtimeValue `json:"callback"`
		N        int          `json:"n,format:'x,y,case:strict,string'"`
	}
	runtimeCompare[format](t, `{"callback":42.00,"n":43.00}`, `{"callback":42.00,"n":43.00}`)
	type dominated struct {
		RuntimeFormat
		Lost     json.Number  `json:"lost"`
		Callback runtimeValue `json:"callback"`
	}
	runtimeCompare[dominated](t, `{"callback":42.00,"lost":43.00}`, `{"callback":42.00,"lost":43.00}`)
	type malformed struct {
		N int `json:"n,format:'unterminated,case:strict"` //nolint:staticcheck // Exercise the selected runtime's best-effort tag handling.
	}
	runtimeCompare[malformed](t, `{"N":42.00}`, `{"N":42.00}`)
	// The rejected quoted format still consumes its commas as one option;
	// apparent flags inside a valid format cannot change name selection.
	type quoted struct {
		N int `json:"n,format:'',case:strict"` //nolint:staticcheck // Exercise the selected runtime's best-effort tag handling.
		M int `json:"m,format:'unterminated"`  //nolint:staticcheck // Exercise the selected runtime's best-effort tag handling.
	}
	runtimeCompare[quoted](t, `{"N":42.00,"M":43.00}`, `{"N":42.00,"M":43}`)
}
