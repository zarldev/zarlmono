package tools

import (
	"encoding"
	"encoding/json"
	"reflect"
	"strings"
)

// DecodeJSONArgs strictly decodes one complete JSON document into T. Ordinary
// integer destinations also accept exactly integral decimal/exponent tokens
// within their range (42.0, 42e0). Float, interface, json.Number, RawMessage,
// custom JSON/text unmarshaler subtrees and ,string fields retain encoding/json
// semantics and spelling. It neither repairs JSON nor wraps errors as Validation.
func DecodeJSONArgs[T any](raw json.RawMessage) (T, error) {
	var out T
	if !json.Valid(raw) {
		// Obtain encoding/json's ordinary syntax error without invoking T's
		// custom decoders on an invalid document.
		var document json.RawMessage
		return out, json.Unmarshal(raw, &document)
	}
	view := argumentJSON{raw: raw}
	view.walk(0, reflect.TypeFor[T]())
	prepared := raw
	if len(view.edits) > 0 {
		prepared = make([]byte, 0, len(raw))
		start := 0
		for _, edit := range view.edits {
			prepared = append(prepared, raw[start:edit.start]...)
			prepared = append(prepared, edit.number...)
			start = edit.end
		}
		prepared = append(prepared, raw[start:]...)
	}
	err := json.Unmarshal(prepared, &out)
	return out, err
}

type argumentNumberEdit struct {
	start, end int
	number     string
}

type argumentJSON struct {
	raw   []byte
	edits []argumentNumberEdit
}

// walk visits validated token spans and destination types only. No destination
// values are constructed; custom decoding happens once, in the final Unmarshal.
func (v *argumentJSON) walk(start int, target reflect.Type) int {
	start = v.space(start)
	for target.Kind() == reflect.Pointer {
		if argumentCustomDecoder(target) {
			return v.end(start)
		}
		target = target.Elem()
	}
	if argumentCustomDecoder(target) || target == reflect.TypeFor[json.Number]() {
		return v.end(start)
	}
	switch target.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		end := v.end(start)
		token := string(v.raw[start:end])
		if !strings.ContainsAny(token, ".eE") {
			return v.end(start)
		}
		d, ok := parseArgumentDecimal(token)
		if ok {
			unsigned := target.Kind() >= reflect.Uint && target.Kind() <= reflect.Uintptr
			if number, ok := d.integer(target.Bits(), unsigned, false); ok {
				v.edits = append(v.edits, argumentNumberEdit{start: start, end: end, number: number})
			}
		}
	case reflect.Struct:
		if v.raw[start] == '{' {
			fields := argumentFields(target)
			if fields.opaque {
				return v.end(start)
			}
			return v.object(start, fields, nil)
		}
	case reflect.Map:
		if v.raw[start] == '{' {
			return v.object(start, argumentStruct{}, target.Elem())
		}
	case reflect.Slice, reflect.Array:
		if v.raw[start] == '[' {
			pos := v.space(start + 1)
			for index := 0; v.raw[pos] != ']'; index++ {
				if target.Kind() == reflect.Slice || index < target.Len() {
					pos = v.walk(pos, target.Elem())
				} else {
					pos = v.end(pos)
				}
				pos = v.space(pos)
				if v.raw[pos] == ',' {
					pos = v.space(pos + 1)
				}
			}
			return pos + 1
		}
	}
	return v.end(start)
}

func argumentCustomDecoder(t reflect.Type) bool {
	return argumentStreamingDecoder(t) || t.Implements(reflect.TypeFor[json.Unmarshaler]()) ||
		t.Implements(reflect.TypeFor[encoding.TextUnmarshaler]()) ||
		reflect.PointerTo(t).Implements(reflect.TypeFor[json.Unmarshaler]()) ||
		reflect.PointerTo(t).Implements(reflect.TypeFor[encoding.TextUnmarshaler]())
}

func (v *argumentJSON) object(start int, fields argumentStruct, element reflect.Type) int {
	pos := v.space(start + 1)
	for v.raw[pos] != '}' {
		keyEnd := v.end(pos)
		var key string
		_ = json.Unmarshal(v.raw[pos:keyEnd], &key) // validated JSON string
		pos = v.space(v.space(keyEnd) + 1)          // colon
		target := element
		if target == nil {
			target = argumentFieldType(fields, key)
		}
		if target == nil {
			pos = v.end(pos)
		} else {
			pos = v.walk(pos, target)
		}
		pos = v.space(pos)
		if v.raw[pos] == ',' {
			pos = v.space(pos + 1)
		}
	}
	return pos + 1
}

func (v *argumentJSON) space(pos int) int {
	for pos < len(v.raw) && strings.ContainsRune(" \t\r\n", rune(v.raw[pos])) {
		pos++
	}
	return pos
}

// end skips a validated value without materializing its subtree. Strings and
// containers are scanned in-place, retaining duplicate keys and member order.
func (v *argumentJSON) end(pos int) int {
	if v.raw[pos] == '"' {
		for pos++; ; pos++ {
			switch v.raw[pos] {
			case '\\':
				pos++
			case '"':
				return pos + 1
			}
		}
	}
	if v.raw[pos] == '{' || v.raw[pos] == '[' {
		depth := 0
		for ; ; pos++ {
			switch v.raw[pos] {
			case '"':
				pos = v.end(pos) - 1
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					return pos + 1
				}
			}
		}
	}
	for pos < len(v.raw) && !strings.ContainsRune(",]} \t\r\n", rune(v.raw[pos])) {
		pos++
	}
	return pos
}
