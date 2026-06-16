package llm_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// Property order is load-bearing for grammar-constrained sampling: llama.cpp
// emits GBNF rules in schema document order, so "rationale before the enum"
// only holds if serialization preserves it. Go marshals maps alphabetically,
// which is exactly the wrong order for rationale/action — PropertyOrder
// exists to pin it.
func TestSchemaMarshalPropertyOrder(t *testing.T) {
	s := llm.Schema{
		Type: "object",
		Properties: map[string]llm.Schema{
			"rationale": {Type: "string"},
			"action":    {Type: "string", Enum: []any{"retry", "stop"}},
		},
		Required:             []string{"rationale", "action"},
		AdditionalProperties: false,
		PropertyOrder:        []string{"rationale", "action"},
	}

	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := string(data)

	ri, ai := strings.Index(out, `"rationale"`), strings.Index(out, `"action"`)
	if ri < 0 || ai < 0 {
		t.Fatalf("marshalled schema missing properties: %s", out)
	}
	if ri > ai {
		t.Errorf("rationale serialized after action — chain-of-thought slot inverted: %s", out)
	}

	// The ordered path must still produce a complete, valid schema.
	var round llm.Schema
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatalf("round-trip unmarshal: %v", err)
	}
	if round.Type != "object" || len(round.Properties) != 2 || len(round.Required) != 2 {
		t.Errorf("round-trip lost fields: %+v", round)
	}
	if round.AdditionalProperties != false {
		t.Errorf("round-trip lost additionalProperties: %v", round.AdditionalProperties)
	}
}

// Properties left out of PropertyOrder must survive (appended, sorted), and
// names in the order list that don't exist must not invent keys.
func TestSchemaMarshalPropertyOrderPartial(t *testing.T) {
	s := llm.Schema{
		Type: "object",
		Properties: map[string]llm.Schema{
			"a": {Type: "string"},
			"b": {Type: "string"},
			"z": {Type: "string"},
		},
		PropertyOrder: []string{"z", "ghost"},
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := string(data)
	if strings.Contains(out, "ghost") {
		t.Errorf("order entry without a property leaked into output: %s", out)
	}
	zi, aiIdx, bi := strings.Index(out, `"z"`), strings.Index(out, `"a"`), strings.Index(out, `"b"`)
	if zi < 0 || aiIdx < 0 || bi < 0 {
		t.Fatalf("missing properties: %s", out)
	}
	if zi >= aiIdx || aiIdx >= bi {
		t.Errorf("order = z@%d a@%d b@%d, want z first then a,b sorted: %s", zi, aiIdx, bi, out)
	}
}
