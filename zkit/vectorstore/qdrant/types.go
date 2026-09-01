package qdrant

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
)

var (
	// ErrInvalidEndpoint reports an endpoint that is not an absolute HTTP(S) URL.
	ErrInvalidEndpoint = errors.New("invalid qdrant endpoint")
	// ErrInvalidCollectionName reports a name outside Qdrant's create-collection grammar.
	ErrInvalidCollectionName = errors.New("invalid qdrant collection name")
)

// Endpoint is a validated canonical absolute HTTP(S) Qdrant base URL. Its zero
// value has an empty string representation.
type Endpoint struct {
	url url.URL
}

// ParseEndpoint validates raw and returns a canonical Qdrant endpoint.
func ParseEndpoint(raw string) (Endpoint, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Endpoint{}, fmt.Errorf("%w %q: %w", ErrInvalidEndpoint, raw, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Endpoint{}, fmt.Errorf("%w %q: absolute HTTP or HTTPS URL required", ErrInvalidEndpoint, raw)
	}
	return Endpoint{url: *u}, nil
}

// String returns the endpoint URL.
func (e Endpoint) String() string { return e.url.String() }

// CollectionName identifies a Qdrant collection.
type CollectionName string

// ParseCollectionName validates the Qdrant create-collection grammar: 1–255
// bytes and no filesystem-unsafe characters.
func ParseCollectionName(raw string) (CollectionName, error) {
	if len(raw) == 0 || len(raw) > 255 {
		return "", ErrInvalidCollectionName
	}
	for _, char := range raw {
		switch char {
		case '<', '>', ':', '"', '/', '\\', '|', '?', '*', '\x00', '\x1f':
			return "", ErrInvalidCollectionName
		}
	}
	return CollectionName(raw), nil
}

// PointID identifies a Qdrant point as either a string or uint64. Its zero
// value is the empty string ID.
type PointID struct {
	stringValue string
	uint64Value uint64
	numeric     bool
}

// StringPointID returns a string point ID.
func StringPointID(value string) PointID {
	return PointID{stringValue: value}
}

// Uint64PointID returns a uint64 point ID.
func Uint64PointID(value uint64) PointID {
	return PointID{uint64Value: value, numeric: true}
}

// String returns the caller-visible string representation of the ID.
func (id PointID) String() string {
	if id.numeric {
		return strconv.FormatUint(id.uint64Value, 10)
	}
	return id.stringValue
}

// StringValue returns the string ID and whether this PointID is a string.
func (id PointID) StringValue() (string, bool) {
	return id.stringValue, !id.numeric
}

// Uint64 returns the numeric ID and whether this PointID is numeric.
func (id PointID) Uint64() (uint64, bool) {
	return id.uint64Value, id.numeric
}

// MarshalJSON preserves whether the ID is a string or uint64.
func (id PointID) MarshalJSON() ([]byte, error) {
	if id.numeric {
		return []byte(strconv.FormatUint(id.uint64Value, 10)), nil
	}
	return json.Marshal(id.stringValue)
}

// UnmarshalJSON decodes Qdrant's string-or-uint64 point ID without passing a
// number through float64.
func (id *PointID) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) > 0 && data[0] == '"' {
		var value string
		if err := json.Unmarshal(data, &value); err != nil {
			return fmt.Errorf("decode qdrant point ID string: %w", err)
		}
		*id = StringPointID(value)
		return nil
	}
	value, err := strconv.ParseUint(string(data), 10, 64)
	if err != nil {
		return fmt.Errorf("decode qdrant point ID uint64: %w", err)
	}
	*id = Uint64PointID(value)
	return nil
}

// Dimension is a vector dimension.
type Dimension uint

// Limit is a result limit. Its zero value asks Qdrant to use the endpoint's
// default page size.
type Limit uint

// ScrollCursor is a Qdrant point ID returned by Scroll for use as the next
// request's offset. A nil *ScrollCursor means no cursor.
type ScrollCursor struct {
	stringValue string
	uint64Value uint64
	numeric     bool
}

// StringScrollCursor returns a string scroll cursor.
func StringScrollCursor(value string) ScrollCursor {
	return ScrollCursor{stringValue: value}
}

// Uint64ScrollCursor returns a uint64 scroll cursor.
func Uint64ScrollCursor(value uint64) ScrollCursor {
	return ScrollCursor{uint64Value: value, numeric: true}
}

// String returns the caller-visible string representation of the cursor.
func (cursor ScrollCursor) String() string {
	if cursor.numeric {
		return strconv.FormatUint(cursor.uint64Value, 10)
	}
	return cursor.stringValue
}

// StringValue returns the string cursor and whether this cursor is a string.
func (cursor ScrollCursor) StringValue() (string, bool) {
	return cursor.stringValue, !cursor.numeric
}

// Uint64 returns the numeric cursor and whether this cursor is numeric.
func (cursor ScrollCursor) Uint64() (uint64, bool) {
	return cursor.uint64Value, cursor.numeric
}

// MarshalJSON preserves whether the cursor is a string or uint64.
func (cursor ScrollCursor) MarshalJSON() ([]byte, error) {
	if cursor.numeric {
		return []byte(strconv.FormatUint(cursor.uint64Value, 10)), nil
	}
	return json.Marshal(cursor.stringValue)
}

// UnmarshalJSON decodes Qdrant's string-or-uint64 scroll cursor without passing
// a number through float64.
func (cursor *ScrollCursor) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) > 0 && data[0] == '"' {
		var value string
		if err := json.Unmarshal(data, &value); err != nil {
			return fmt.Errorf("decode qdrant scroll cursor string: %w", err)
		}
		*cursor = StringScrollCursor(value)
		return nil
	}
	value, err := strconv.ParseUint(string(data), 10, 64)
	if err != nil {
		return fmt.Errorf("decode qdrant scroll cursor uint64: %w", err)
	}
	*cursor = Uint64ScrollCursor(value)
	return nil
}

// CollectionConfig describes the vector configuration used when creating a
// collection.
type CollectionConfig struct {
	Dimension Dimension
	Distance  Distance
}
