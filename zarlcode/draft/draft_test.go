package draft_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/draft"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	t.Parallel()

	encoded, err := draft.Encode("unfinished prompt\nwith context")
	if err != nil {
		t.Fatal(err)
	}
	got, err := draft.Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got != "unfinished prompt\nwith context" {
		t.Fatalf("Decode() = %q", got)
	}
}

func TestDecodeEmptyAndLegacyValues(t *testing.T) {
	t.Parallel()

	for _, value := range [][]byte{nil, {}, []byte("null"), []byte("[]")} {
		got, err := draft.Decode(value)
		if err != nil || got != "" {
			t.Fatalf("Decode(%q) = (%q, %v)", value, got, err)
		}
	}
}

func TestDraftSizeBound(t *testing.T) {
	t.Parallel()

	_, err := draft.Encode(strings.Repeat("x", draft.MaxTextBytes+1))
	if !errors.Is(err, draft.ErrTooLarge) {
		t.Fatalf("Encode() error = %v", err)
	}
}
