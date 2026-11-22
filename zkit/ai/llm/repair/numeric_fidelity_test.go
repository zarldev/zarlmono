package repair_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm/repair"
)

func TestRepairAcceptsSyntaxBeyondFloatRange(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{`1e99999999999999999999999999999`, `{"n":1e99999999999999999999999999999,"n":9007199254740993}`, `{"n":1e-99999999999999999999999999999}`} {
		got, ok := repair.Repair([]byte(raw))
		if !ok || !bytes.Equal(got, []byte(raw)) {
			t.Fatalf("Repair(%s)=%s,%t", raw, got, ok)
		}
		var document json.RawMessage
		if err := repair.Unmarshal([]byte(raw), &document); err != nil || string(document) != raw {
			t.Fatalf("raw=%s err=%v", document, err)
		}
	}
	// The generic Unmarshal decode policy is intentionally unchanged.
	var value any
	if err := repair.Unmarshal([]byte(`{"n":9007199254740993}`), &value); err != nil || value.(map[string]any)["n"] != float64(9007199254740992) {
		t.Fatalf("default decode=%v %v", value, err)
	}
	err := repair.Unmarshal([]byte(`{"n":1e99999999999999999999}`), &value)
	var mismatch *json.UnmarshalTypeError
	if !errors.As(err, &mismatch) {
		t.Fatalf("ordinary float overflow=%v", err)
	}
}
