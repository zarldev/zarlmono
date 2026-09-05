package toolparse_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm/toolparse"
)

func FuzzParseArtifact(f *testing.F) {
	for _, seed := range []string{
		"",
		"plain prose",
		`<tool_calls>[{"name":"read","arguments":{"path":"README.md"}}]</tool_calls>`,
		`{"tool_calls":[{"id":"call-1","type":"function","function":{"name":"write","arguments":"{\"path\":\"x\"}"}}]}`,
		"```json\n[{\"name\":\"grep\",\"arguments\":{\"pattern\":\"x\"}}]\n```",
		`<tool_calls>[{"name":"broken","arguments":{"x":1}]`,
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 64<<10 {
			t.Skip()
		}

		first := toolparse.ParseArtifact(input)
		second := toolparse.ParseArtifact(input)
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("ParseArtifact is not deterministic: first=%#v second=%#v", first, second)
		}
		if !first.HighConfidence {
			return
		}

		ids := make(map[string]struct{}, len(first.Calls))
		for _, call := range first.Calls {
			if call.ID == "" {
				t.Fatal("high-confidence call has an empty ID")
			}
			if _, duplicate := ids[call.ID]; duplicate {
				t.Fatalf("high-confidence calls contain duplicate ID %q", call.ID)
			}
			ids[call.ID] = struct{}{}
			if call.Function.Name == "" {
				t.Fatal("high-confidence call has an empty function name")
			}
			if !json.Valid([]byte(call.Function.Arguments)) {
				t.Fatalf("high-confidence call arguments are not valid JSON: %q", call.Function.Arguments)
			}
		}
	})
}
