package runner_test

import (
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestReplayOccurrenceCompatibilityAndAdmission(t *testing.T) {
	for _, tc := range []struct {
		name, wire          string
		admitted, execution bool
	}{
		{"legacy", `{"message":{"role":"assistant","content":"old"}}`, true, false},
		{"old-envelope", `{"encoding":"byte-strings.v1","value":{"message":{"role":"assistant","content":"old"}}}`, true, false},
		{"interrupted", `{"interrupted":true,"message":{"role":"assistant","content":"partial"}}`, false, false},
		{"execution", `{"kind":"tool_execution","message":{},"tool":{"ToolCallID":"child","ToolName":"read"}}`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record, err := runner.UnmarshalReplayMessage([]byte(tc.wire))
			if err != nil {
				t.Fatal(err)
			}
			if record.AdmittedToModelContext() != tc.admitted || record.IsToolExecution() != tc.execution {
				t.Fatalf("admission = %+v", record)
			}
		})
	}
	for _, wire := range []string{
		`{"kind":"future","message":{}}`,
		`{"message":{},"future":true}`,
		`{"kind":"tool_execution","message":{}}`,
		`{"kind":"tool_execution","message":{"role":"tool"},"tool":{}}`,
		`{"kind":"tool_execution","interrupted":true,"message":{},"tool":{}}`,
		`{"kind":"tool_execution","raw_tool_calls":[{}],"message":{},"tool":{}}`,
		`{"message":{}} {}`,
	} {
		if _, err := runner.UnmarshalReplayMessage([]byte(wire)); err == nil {
			t.Errorf("accepted malformed occurrence %s", wire)
		}
	}
	original := runner.ReplayMessage{Kind: runner.ReplayOccurrenceKinds.TOOLEXECUTION, Tool: &runner.ToolOutput{
		ExecutionID: "execution", ParentExecutionID: "parent", TaskID: "task", Attempt: 2, Dispatched: new(false),
		ToolCallID: "same", ToolName: "read", Args: string([]byte{0xff}), Output: "~zarl-history-bytes:literal",
		Parts: []llm.ContentPart{llm.TextPart(string([]byte{0xfe}))},
	}}
	data, err := runner.MarshalReplayMessage(original)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := runner.UnmarshalReplayMessage(data)
	if err != nil || !reflect.DeepEqual(decoded, original) {
		t.Fatalf("byte-preserving execution round trip = %+v, %v", decoded, err)
	}
	*decoded.Tool.Dispatched = true
	if *original.Tool.Dispatched {
		t.Fatal("decoded metadata aliases original")
	}
}
