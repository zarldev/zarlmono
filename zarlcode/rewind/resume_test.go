package rewind_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestExactResumeOwnsSettlementAndTargetPolicy(t *testing.T) {
	t.Parallel()
	target := rewind.Target{Provider: "openai", Model: "historical", Window: 12000, Reserve: 2000, PlanMode: true, CodexEffort: "high"}
	messages := []llm.Message{{Role: llm.RoleUser, Content: "historical context"}}
	data, err := rewind.EncodeResume(12, messages, target, "settled-turn", 10, rewind.InitialContinuation{})
	if err != nil {
		t.Fatal(err)
	}
	state, err := rewind.DecodeResume(data)
	if err != nil {
		t.Fatal(err)
	}
	if state.Revision != 12 || state.SettledTurnID != "settled-turn" || state.EventWatermark != 10 || state.Target != target || !reflect.DeepEqual(state.Context, messages) {
		t.Fatal("exact head lost policy or explicit settlement")
	}
	messages[0].Content = "mutated"
	if state.Context[0].Content != "historical context" {
		t.Fatal("decoded context aliases input")
	}
	for _, scenario := range []string{"future watermark", "missing turn", "missing watermark", "unknown version", "unknown secret field"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			var value map[string]any
			if err := json.Unmarshal(data, &value); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "future watermark":
				value["event_watermark"] = 13
			case "missing turn":
				value["settled_turn_id"] = ""
			case "missing watermark":
				value["event_watermark"] = 0
			case "unknown version":
				value["rewind_resume_version"] = 99
			case "unknown secret field":
				value["api_key"] = "PRIVATE-canary"
			}
			bad, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := rewind.DecodeResume(bad); !errors.Is(err, rewind.ErrInvalid) {
				t.Fatalf("decode = %v", err)
			}
		})
	}
}

func TestExactResumeInitialBoundary(t *testing.T) {
	t.Parallel()
	data, err := rewind.EncodeResume(0, nil, rewind.Target{Provider: "openai", Model: "initial"}, "", 0, rewind.InitialContinuation{})
	if err != nil {
		t.Fatal(err)
	}
	state, err := rewind.DecodeResume(data)
	if err != nil || state.Context == nil || len(state.Context) != 0 || state.SettledTurnID != "" {
		t.Fatalf("initial head = %#v, %v", state, err)
	}
}
