package teasink_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestSinkProjectsInputLifecycleWithoutRetainingContext(t *testing.T) {
	send, snapshot := recordingSend()
	sink := teasink.New(send)
	t.Cleanup(sink.Close)

	type contextKey struct{}
	ctx := context.WithValue(t.Context(), contextKey{}, "must not escape")
	message := llm.Message{Role: llm.RoleUser, Content: "evidence", Observation: llm.ObservationProvenance{Version: 1, ID: "child:1"}}
	reference := tools.AdmissionReference{Namespace: "spawn.completion", ID: "child:1"}
	sink.OnWaitingForInputs(ctx, runner.WaitingForInputs{TaskID: taskscope.ID("parent"), Waiting: true})
	sink.OnInputsAdmitted(ctx, runner.InputsAdmitted{TaskID: taskscope.ID("parent"), Messages: []llm.Message{message}, References: []tools.AdmissionReference{reference}})
	sink.OnWaitingForInputs(ctx, runner.WaitingForInputs{TaskID: taskscope.ID("parent"), Waiting: false})
	sink.Drain()

	messages := snapshot()
	if len(messages) != 3 {
		t.Fatalf("messages = %#v", messages)
	}
	if got, ok := messages[0].(teasink.WaitingForInputsMsg); !ok || got.TaskID != "parent" || !got.Waiting {
		t.Fatalf("waiting start = %#v", messages[0])
	}
	admitted, ok := messages[1].(teasink.InputsAdmittedMsg)
	if !ok || !reflect.DeepEqual(admitted.Messages, []llm.Message{message}) || !reflect.DeepEqual(admitted.References, []tools.AdmissionReference{reference}) {
		t.Fatalf("admission = %#v", messages[1])
	}
	message.Content = "mutated"
	reference.ID = "mutated"
	if admitted.Messages[0].Content != "evidence" || admitted.References[0].ID != "child:1" {
		t.Fatal("UI event retained mutable event input")
	}
	if got, ok := messages[2].(teasink.WaitingForInputsMsg); !ok || got.Waiting {
		t.Fatalf("waiting end = %#v", messages[2])
	}
}
