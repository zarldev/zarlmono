package harness

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"unicode/utf8"
)

// Validate the representation before encoding/json can accept duplicate or
// case-folded keys, or silently repair malformed Unicode in candidate guidance.
func validateExperimentJSON(data []byte) error {
	if !utf8.Valid(data) || !json.Valid(data) {
		return errors.New("expected one valid UTF-8 JSON value")
	}
	if err := validateSurrogates(data); err != nil {
		return err
	}
	fields, err := experimentObject(data, []string{"version", "repeats", "candidates"})
	if err != nil {
		return err
	}
	var candidates []json.RawMessage
	if err := json.Unmarshal(fields["candidates"], &candidates); err != nil {
		return fmt.Errorf("decode candidates: %w", err)
	}
	for i, c := range candidates {
		if _, err := experimentObject(c, []string{"label", "guidance"}); err != nil {
			return fmt.Errorf("candidate %d: %w", i+1, err)
		}
	}
	return nil
}

func experimentObject(data []byte, keys []string) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	token, err := dec.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("expected an object")
	}
	fields := make(map[string]json.RawMessage, len(keys))
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok || !slices.Contains(keys, key) {
			return nil, fmt.Errorf("unknown field %q", token)
		}
		if _, exists := fields[key]; exists {
			return nil, fmt.Errorf("duplicate field %q", key)
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		fields[key] = value
	}
	return fields, nil
}

// Called only after json.Valid: escape lengths and hex syntax are established.
// Escaped backslashes are skipped, so a literal "\\ud800" remains valid text.
func validateSurrogates(data []byte) error {
	for i := 0; i < len(data); i++ {
		if data[i] != '\\' {
			continue
		}
		i++
		if data[i] != 'u' {
			continue
		}
		code, err := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		if err != nil {
			return err
		}
		i += 4
		switch {
		case code >= 0xdc00 && code <= 0xdfff:
			return errors.New("unpaired low Unicode surrogate")
		case code >= 0xd800 && code <= 0xdbff:
			if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
				return errors.New("unpaired high Unicode surrogate")
			}
			low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return errors.New("invalid Unicode surrogate pair")
			}
			i += 6
		}
	}
	return nil
}
