package runner

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

const historyStringPrefix = "~zarl-history-bytes:"

// historyJSON keeps the storage-only byte encoding separate from provider JSON.
// Ordinary UTF-8 strings stay readable. Invalid strings and literal occurrences
// of the escape prefix are base64 encoded, including string map keys.
type historyJSON struct {
	Encoding string          `json:"encoding"`
	Value    json.RawMessage `json:"value"`
}

// MarshalReplayMessage preserves every string byte in a stored replay occurrence,
// including invalid UTF-8, without changing provider or ordinary message JSON.
func MarshalReplayMessage(record ReplayMessage) ([]byte, error) {
	return marshalHistoryJSON(record)
}

// UnmarshalReplayMessage restores an independently owned stored occurrence and
// accepts older plain occurrences without inventing bytes that were not recorded.
func UnmarshalReplayMessage(data []byte) (ReplayMessage, error) {
	var record ReplayMessage
	if err := unmarshalHistoryJSON(data, &record); err != nil {
		return ReplayMessage{}, err
	}
	if !record.Kind.IsValid() {
		return ReplayMessage{}, errors.New("unsupported replay occurrence kind")
	}
	if record.IsToolExecution() && (record.Tool == nil || record.Interrupted || len(record.RawToolCalls) != 0 || !reflect.ValueOf(record.Message).IsZero()) {
		return ReplayMessage{}, errors.New("invalid execution occurrence")
	}
	if err := validateObservation(record.Message); err != nil {
		return ReplayMessage{}, err
	}
	return record, nil
}

// MarshalHistoryRequest encodes the prepared runner request for immutable
// storage, preserving string bytes. It is not provider transport JSON.
func MarshalHistoryRequest(request llm.CompletionRequest) ([]byte, error) {
	return marshalHistoryJSON(request)
}

// UnmarshalHistoryRequest restores an independently owned prepared request from
// MarshalHistoryRequest, or from older plain request JSON.
func UnmarshalHistoryRequest(data []byte) (llm.CompletionRequest, error) {
	var request llm.CompletionRequest
	if err := unmarshalHistoryJSON(data, &request); err != nil {
		return llm.CompletionRequest{}, err
	}
	for _, message := range request.Messages {
		if err := validateObservation(message); err != nil {
			return llm.CompletionRequest{}, err
		}
	}
	return request, nil
}

func validateObservation(message llm.Message) error {
	origin := message.Observation
	if origin == (llm.ObservationProvenance{}) {
		return nil
	}
	if origin.Version != 1 || origin.ID == "" || message.Role != llm.RoleUser {
		return errors.New("unsupported or invalid host observation provenance")
	}
	return nil
}

func marshalHistoryJSON(value any) ([]byte, error) {
	// Let encoding/json reject unsupported/cyclic values before walking them.
	if _, err := json.Marshal(value); err != nil {
		return nil, fmt.Errorf("validate history JSON: %w", err)
	}
	encoded, err := transformHistoryStrings(reflect.ValueOf(value), true)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(encoded.Interface())
	if err != nil {
		return nil, fmt.Errorf("encode history value: %w", err)
	}
	return json.Marshal(historyJSON{Encoding: "byte-strings.v1", Value: data})
}

func unmarshalHistoryJSON(data []byte, target any) error {
	var envelope historyJSON
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("decode history envelope: %w", err)
	}
	if envelope.Encoding == "" {
		return decodeHistoryValue(data, target)
	}
	if envelope.Encoding != "byte-strings.v1" {
		return fmt.Errorf("unsupported history encoding %q", envelope.Encoding)
	}
	if err := decodeHistoryValue(data, &envelope); err != nil {
		return err
	}
	if err := decodeHistoryValue(envelope.Value, target); err != nil {
		return err
	}
	restored, err := transformHistoryStrings(reflect.ValueOf(target).Elem(), false)
	if err != nil {
		return err
	}
	reflect.ValueOf(target).Elem().Set(restored)
	return nil
}

func decodeHistoryValue(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode history value: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("history value has trailing JSON")
	}
	return nil
}

// Reflection is confined to this serialization boundary. It copies exported
// storage fields and dynamic JSON values, preserving their concrete types so
// typed codecs still handle their representations. It never changes borrowed input.
func transformHistoryStrings(value reflect.Value, encode bool) (reflect.Value, error) {
	if value.Type() == reflect.TypeFor[llm.Schema]() {
		schema, _ := value.Interface().(llm.Schema) // exact type selected above
		return transformHistorySchema(schema, encode)
	}
	switch value.Kind() {
	case reflect.String:
		text := value.String()
		if encode {
			if !utf8.ValidString(text) || strings.HasPrefix(text, historyStringPrefix) {
				text = historyStringPrefix + base64.StdEncoding.EncodeToString([]byte(text))
			}
		} else if payload, ok := strings.CutPrefix(text, historyStringPrefix); ok {
			data, err := base64.StdEncoding.DecodeString(payload)
			if err != nil {
				return reflect.Value{}, fmt.Errorf("decode history string bytes: %w", err)
			}
			text = string(data)
		}
		result := reflect.New(value.Type()).Elem()
		result.SetString(text)
		return result, nil
	case reflect.Pointer, reflect.Interface:
		if value.IsNil() {
			return value, nil
		}
		element, err := transformHistoryStrings(value.Elem(), encode)
		if err != nil {
			return reflect.Value{}, err
		}
		if value.Kind() == reflect.Interface {
			result := reflect.New(value.Type()).Elem()
			result.Set(element)
			return result, nil
		}
		result := reflect.New(value.Type().Elem())
		result.Elem().Set(element)
		return result, nil
	case reflect.Struct:
		result := reflect.New(value.Type()).Elem()
		result.Set(value)
		for i := range value.NumField() {
			field := value.Type().Field(i)
			if !field.IsExported() || field.Tag.Get("json") == "-" {
				continue
			}
			transformed, err := transformHistoryStrings(value.Field(i), encode)
			if err != nil {
				return reflect.Value{}, err
			}
			result.Field(i).Set(transformed)
		}
		return result, nil
	case reflect.Slice, reflect.Array:
		if value.Kind() == reflect.Slice && value.IsNil() {
			return value, nil
		}
		result := reflect.New(value.Type()).Elem()
		if value.Kind() == reflect.Slice {
			result = reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		}
		for i := range value.Len() {
			transformed, err := transformHistoryStrings(value.Index(i), encode)
			if err != nil {
				return reflect.Value{}, err
			}
			result.Index(i).Set(transformed)
		}
		return result, nil
	case reflect.Map:
		if value.IsNil() {
			return value, nil
		}
		result := reflect.MakeMapWithSize(value.Type(), value.Len())
		iter := value.MapRange()
		for iter.Next() {
			key, err := transformHistoryStrings(iter.Key(), encode)
			if err != nil {
				return reflect.Value{}, err
			}
			item, err := transformHistoryStrings(iter.Value(), encode)
			if err != nil {
				return reflect.Value{}, err
			}
			result.SetMapIndex(key, item)
		}
		return result, nil
	default:
		return value, nil
	}
}

// Schema's provider codec expresses property order only as JSON key order, which
// its decoder does not retain. Keep that field in a storage-only extension. The
// reserved key cannot collide with user extensions: those keys are escaped first.
func transformHistorySchema(schema llm.Schema, encode bool) (reflect.Value, error) {
	const orderKey = historyStringPrefix + "schema-order"
	type wire llm.Schema
	if !encode {
		if order, ok := schema.Extra[orderKey]; ok {
			delete(schema.Extra, orderKey)
			if len(schema.Extra) == 0 {
				schema.Extra = nil
			}
			items, ok := order.([]any)
			if !ok {
				return reflect.Value{}, errors.New("history schema order is not an array")
			}
			schema.PropertyOrder = make([]string, len(items))
			for i, item := range items {
				text, ok := item.(string)
				if !ok {
					return reflect.Value{}, errors.New("history schema order is not a string")
				}
				schema.PropertyOrder[i] = text
			}
		}
	}
	value, err := transformHistoryStrings(reflect.ValueOf(wire(schema)), encode)
	if err != nil {
		return reflect.Value{}, err
	}
	transformed, _ := value.Interface().(wire) // transformation retains the input type
	result := llm.Schema(transformed)
	if encode && result.PropertyOrder != nil {
		if result.Extra == nil {
			result.Extra = make(map[string]any)
		}
		result.Extra[orderKey] = result.PropertyOrder
	}
	return reflect.ValueOf(result), nil
}
