package rewind

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

// validStrings checks external state before encoding/json can replace malformed
// UTF-8. Reflection here walks the closed payload representation, not runtime
// dependencies. Opaque []byte fields are checked as JSON at their own boundary.
func validStrings(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.String:
		return utf8.ValidString(value.String())
	case reflect.Pointer:
		if value.IsNil() {
			return true
		}
		return validStrings(value.Elem())
	case reflect.Struct:
		for i := range value.NumField() {
			if !validStrings(value.Field(i)) {
				return false
			}
		}
	case reflect.Slice:
		if value.Type().Elem().Kind() == reflect.Uint8 {
			return true
		}
		for i := range value.Len() {
			if !validStrings(value.Index(i)) {
				return false
			}
		}
	}
	return true
}

// validJSONText rejects invalid UTF-8 and unpaired escaped UTF-16 surrogates.
// encoding/json would otherwise silently replace both with U+FFFD.
func validJSONText(data []byte) bool {
	if !utf8.Valid(data) || !json.Valid(data) {
		return false
	}
	inString := false
	for i := 0; i < len(data); i++ {
		if data[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || data[i] != '\\' {
			continue
		}
		i++
		if data[i] != 'u' {
			continue
		}
		n, err := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n < 0xd800 || n > 0xdbff {
			continue
		}
		if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
			return false
		}
		low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return false
		}
		i += 6
	}
	return true
}

func uniqueJSONText(data []byte) bool {
	if !validJSONText(data) {
		return false
	}
	tokens := json.NewDecoder(bytes.NewReader(data))
	if !uniqueJSONValue(tokens, 0) {
		return false
	}
	if _, err := tokens.Token(); err != io.EOF {
		return false
	}
	return true
}

func strictJSON(data []byte, target any) bool {
	if !uniqueJSONText(data) {
		return false
	}
	if !requiredJSONFields(data, reflect.TypeOf(target).Elem()) {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return false
	}
	return decoder.Decode(&struct{}{}) == io.EOF
}

func uniqueJSONValue(decoder *json.Decoder, depth int) bool {
	if depth > 64 {
		return false
	}
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return true
	}
	switch delimiter {
	case '{':
		keys := make(map[string]bool)
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return false
			}
			name, ok := key.(string)
			if !ok || keys[name] {
				return false
			}
			keys[name] = true
			if !uniqueJSONValue(decoder, depth+1) {
				return false
			}
		}
	case '[':
		for decoder.More() {
			if !uniqueJSONValue(decoder, depth+1) {
				return false
			}
		}
	default:
		return false
	}
	_, err = decoder.Token()
	return err == nil
}

// Struct tags define the versioned shape: every non-optional field must be
// present, even when its value is zero. Null is allowed only for slices and
// optional pointer fields, never as an implicit zero scalar/struct.
func requiredJSONFields(data []byte, kind reflect.Type) bool {
	if bytes.Equal(data, []byte("null")) {
		return kind.Kind() == reflect.Slice || kind.Kind() == reflect.Pointer
	}
	if kind.Kind() == reflect.Pointer {
		return requiredJSONFields(data, kind.Elem())
	}
	switch kind.Kind() {
	case reflect.Struct:
		// Generated semantic enums own their scalar JSON representation.
		if kind.Implements(reflect.TypeFor[json.Marshaler]()) {
			value := reflect.New(kind).Interface()
			if json.Unmarshal(data, value) != nil {
				return false
			}
			canonical, err := json.Marshal(value)
			return err == nil && bytes.Equal(bytes.TrimSpace(data), canonical)
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(data, &fields) != nil || fields == nil {
			return false
		}
		for i := range kind.NumField() {
			field := kind.Field(i)
			name, options, _ := strings.Cut(field.Tag.Get("json"), ",")
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			value, exists := fields[name]
			if !exists {
				if options == "omitempty" || options == "omitzero" {
					continue
				}
				return false
			}
			if !requiredJSONFields(value, field.Type) {
				return false
			}
			delete(fields, name)
		}
		return len(fields) == 0
	case reflect.Slice:
		if kind.Elem().Kind() == reflect.Uint8 {
			return true
		}
		var entries []json.RawMessage
		if json.Unmarshal(data, &entries) != nil {
			return false
		}
		for _, entry := range entries {
			if !requiredJSONFields(entry, kind.Elem()) {
				return false
			}
		}
	}
	return true
}
