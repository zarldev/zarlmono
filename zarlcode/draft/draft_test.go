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

func TestEncodeUsesEmptyArraySentinelForEmptyText(t *testing.T) {
	t.Parallel()

	got, err := draft.Encode("")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `[]` {
		t.Fatalf("Encode(\"\") = %s", got)
	}
}

func TestDecodeEmptyRepresentations(t *testing.T) {
	t.Parallel()

	for _, value := range [][]byte{nil, {}, []byte(" "), []byte("[]")} {
		got, err := draft.Decode(value)
		if err != nil || got != "" {
			t.Fatalf("Decode(%q) = (%q, %v)", value, got, err)
		}
	}
}

func TestDecodeRejectsInvalidNonemptyValues(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		`null`,
		`{"text":"draft","extra":true}`,
		`{"text":"draft"} {}`,
	} {
		if _, err := draft.Decode([]byte(value)); err == nil {
			t.Fatalf("Decode(%q) succeeded, want error", value)
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
