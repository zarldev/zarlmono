// Package draft defines the persisted, user-authored composer draft boundary.
package draft

import (
	"bytes"
	"encoding/json"
	"errors"
)

// MaxTextBytes is the largest composer draft accepted by Encode and Decode.
const MaxTextBytes = 256 * 1024

// ErrTooLarge reports draft text beyond MaxTextBytes.
var ErrTooLarge = errors.New("draft text exceeds 256 KiB")

type document struct {
	Text string `json:"text"`
}

// Encode serializes text for session pending_json. Empty text uses the legacy
// empty-array representation so empty-session cleanup remains compatible.
func Encode(text string) ([]byte, error) {
	if len(text) > MaxTextBytes {
		return nil, ErrTooLarge
	}
	if text == "" {
		return []byte("[]"), nil
	}
	return json.Marshal(document{Text: text})
}

// Decode parses a persisted draft. Empty, null, and the legacy [] value mean
// no draft. Unknown fields are ignored for forward compatibility.
func Decode(data []byte) (string, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) || bytes.Equal(data, []byte("[]")) {
		return "", nil
	}
	var value document
	if err := json.Unmarshal(data, &value); err != nil {
		return "", err
	}
	if len(value.Text) > MaxTextBytes {
		return "", ErrTooLarge
	}
	return value.Text, nil
}
