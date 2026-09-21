// Package draft defines the persisted, user-authored composer draft boundary.
package draft

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// MaxTextBytes is the largest composer draft accepted by Encode and Decode.
const MaxTextBytes = 256 * 1024

// MaxAttachments bounds the number of durable parts in one prompt.
const MaxAttachments = 64

// MaxAttachmentBytes bounds serialized attachments independently of history length.
const MaxAttachmentBytes = 64 << 20

// ErrTooLarge reports draft text or attachments beyond their storage bounds.
var ErrTooLarge = errors.New("draft exceeds text or attachment storage bounds")

type document struct {
	Text        string            `json:"text"`
	Attachments []llm.ContentPart `json:"attachments,omitempty"`
}

// Encode serializes text for session pending_json. Empty text uses the database's
// empty-array sentinel.
func Encode(text string) ([]byte, error) {
	return EncodeWithAttachments(text, nil)
}

// EncodeWithAttachments saves owned prompt parts with the unsubmitted draft.
func EncodeWithAttachments(text string, attachments []llm.ContentPart) ([]byte, error) {
	if len(text) > MaxTextBytes {
		return nil, ErrTooLarge
	}
	if err := ValidateAttachments(attachments); err != nil {
		return nil, err
	}
	if text == "" && len(attachments) == 0 {
		return []byte("[]"), nil
	}
	return json.Marshal(document{Text: text, Attachments: attachments})
}

// Decode parses a persisted draft. Empty data and the database's [] sentinel
// mean no draft. Nonempty draft objects reject unknown fields and trailing data.
func Decode(data []byte) (string, error) {
	value, err := decode(data)
	return value.Text, err
}

// DecodeAttachments returns independent saved prompt parts after draft validation.
func DecodeAttachments(data []byte) ([]llm.ContentPart, error) {
	value, err := decode(data)
	return value.Attachments, err
}

func decode(data []byte) (document, error) {
	if len(data) > MaxAttachmentBytes+6*MaxTextBytes+1024 {
		return document{}, ErrTooLarge
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("[]")) {
		return document{}, nil
	}
	if bytes.Equal(data, []byte("null")) {
		return document{}, errors.New("draft must be an object")
	}
	var value document
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return document{}, err
	}
	if err := decoder.Decode(&struct{}{}); err == nil {
		return document{}, errors.New("draft contains multiple JSON values")
	} else if !errors.Is(err, io.EOF) {
		return document{}, err
	}
	if len(value.Text) > MaxTextBytes {
		return document{}, ErrTooLarge
	}
	if err := ValidateAttachments(value.Attachments); err != nil {
		return document{}, err
	}
	return value, nil
}

// ValidateAttachments rejects missing bytes, inconsistent discriminators, remote
// media and oversized durable parts. It never fetches or repairs content.
func ValidateAttachments(parts []llm.ContentPart) error {
	if len(parts) > MaxAttachments {
		return ErrTooLarge
	}
	data, err := json.Marshal(parts)
	if err != nil {
		return err
	}
	if len(data) > MaxAttachmentBytes {
		return ErrTooLarge
	}
	for _, part := range parts {
		valid := false
		switch part.Type {
		case llm.ContentTypeText:
			valid = part.Text != "" && part.Image == nil && part.Audio == nil && part.Video == nil
		case llm.ContentTypeImage:
			valid = part.Image != nil && embeddedMedia(part.Image.DataURI, "image", part.Image.MIMEType) && part.Image.URL == "" && part.Text == "" && part.Audio == nil && part.Video == nil
		case llm.ContentTypeAudio:
			valid = part.Audio != nil && embeddedMedia(part.Audio.DataURI, "audio", "") && part.Text == "" && part.Image == nil && part.Video == nil
		case llm.ContentTypeVideo:
			valid = part.Video != nil && embeddedMedia(part.Video.DataURI, "video", part.Video.MIMEType) && part.Video.URL == "" && part.Text == "" && part.Image == nil && part.Audio == nil
		}
		if !valid {
			return errors.New("draft attachment has missing or inconsistent content")
		}
	}
	return nil
}

func embeddedMedia(uri, kind, mime string) bool {
	prefix, encoded, ok := strings.Cut(uri, ";base64,")
	if !ok || !strings.HasPrefix(prefix, "data:"+kind+"/") || encoded == "" {
		return false
	}
	if mime != "" && mime != strings.TrimPrefix(prefix, "data:") {
		return false
	}
	_, err := base64.StdEncoding.DecodeString(encoded)
	return err == nil
}
