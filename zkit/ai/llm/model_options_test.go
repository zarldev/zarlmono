package llm_test

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestModelOptionsCloneOwnsNestedJSONLikeValues(t *testing.T) {
	nestedMap := map[string]any{"value": "original"}
	nestedSlice := []any{nestedMap, []any{"tail"}}
	options := llm.ModelOptions{
		"nested": nestedSlice,
		"number": json.Number("12.5"),
	}

	clone := options.Clone()
	nestedMap["value"] = "changed"
	nestedSlice[1].([]any)[0] = "changed"

	got := clone["nested"].([]any)
	if got[0].(map[string]any)["value"] != "original" || got[1].([]any)[0] != "tail" {
		t.Fatalf("clone aliases nested values: %#v", clone)
	}
}

func TestModelOptionsCloneRejectsUnsupportedValues(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Clone did not reject unsupported value")
		}
	}()
	_ = (llm.ModelOptions{"bytes": []byte("not JSON-like")}).Clone()
}

func TestModelOptionsClonesCanBeMutatedConcurrently(t *testing.T) {
	options := llm.ModelOptions{"nested": map[string]any{"items": []any{"original"}}}
	first := options.Clone()
	second := options.Clone()

	var wg sync.WaitGroup
	for i, clone := range []llm.ModelOptions{first, second} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			items := clone["nested"].(map[string]any)["items"].([]any)
			for range 1000 {
				items[0] = i
			}
		}()
	}
	wg.Wait()

	if got := options["nested"].(map[string]any)["items"].([]any)[0]; got != "original" {
		t.Fatalf("concurrent clone mutation reached source: %v", got)
	}
}
