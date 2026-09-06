// Package draft defines the persisted, user-authored composer draft boundary.
package draft

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// MaxTextBytes is the largest composer draft accepted by Encode and Decode.
const MaxTextBytes = 256 * 1024

// ErrTooLarge reports draft text beyond MaxTextBytes.
var ErrTooLarge = errors.New("draft text exceeds 256 KiB")

type document struct {
	Text string `json:"text"`
}

// Encode serializes text for session pending_json. Empty text uses the database's
// empty-array sentinel.
func Encode(text string) ([]byte, error) {
	if len(text) > MaxTextBytes {
		return nil, ErrTooLarge
	}
	if text == "" {
		return []byte("[]"), nil
	}
	return json.Marshal(document{Text: text})
}

// Decode parses a persisted draft. Empty data and the database's [] sentinel
// mean no draft. Nonempty draft objects reject unknown fields and trailing data.
func Decode(data []byte) (string, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("[]")) {
		return "", nil
	}
	if bytes.Equal(data, []byte("null")) {
		return "", errors.New("draft must be an object")
	}

	var value document
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	if err := decoder.Decode(&struct{}{}); err == nil {
		return "", errors.New("draft contains multiple JSON values")
	} else if !errors.Is(err, io.EOF) {
		return "", err
	}
	if len(value.Text) > MaxTextBytes {
		return "", ErrTooLarge
	}
	return value.Text, nil
}
