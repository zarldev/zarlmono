package rewind_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestCachePolicyRejectsPresentNullAndIncompleteObjects(t *testing.T) {
	t.Parallel()
	input := captureInput(t)
	input.Target = rewind.Target{Provider: "anthropic", Model: "sonnet"}
	input.Context = []llm.Message{{Role: llm.RoleUser, Content: "hello"}}
	record := captureRecord(t, input)
	resume, err := rewind.EncodeResume(input.Transcript.Revision(), input.Context, input.Target,
		input.Boundary.SettledTurnID, input.Boundary.EventWatermark, rewind.InitialContinuation{})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"cache_pin", "cache_intent"} {
		for _, tc := range []struct{ name, value string }{
			{"null", `null`},
			{"empty object", `{}`},
			{"null flag", `{"mode":"disabled","static_ttl":"default","rolling_ttl":"default","system":null,"tools":false}`},
			{"missing flag", `{"mode":"disabled","static_ttl":"default","rolling_ttl":"default","system":false}`},
			{"explicit zero", `{"mode":"","static_ttl":"","rolling_ttl":"","system":false,"tools":false}`},
		} {
			t.Run(field+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				prefix := []byte(`"target":{`)
				replacement := []byte(`"target":{"` + field + `":` + tc.value + `,`)
				bad := record
				bad.Payload = bytes.Replace(record.Payload, prefix, replacement, 1)
				if bytes.Equal(bad.Payload, record.Payload) {
					t.Fatal("checkpoint fixture was not mutated")
				}
				if _, err := rewind.Decode(bad); !errors.Is(err, rewind.ErrInvalid) {
					t.Errorf("checkpoint admitted invalid policy: %v", err)
				}
				badResume := bytes.Replace(resume, prefix, replacement, 1)
				if _, err := rewind.DecodeResume(badResume); !errors.Is(err, rewind.ErrInvalid) {
					t.Errorf("resume admitted invalid policy: %v", err)
				}
			})
		}
	}
}
