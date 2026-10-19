package runner_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestReplayControlCodecAndAdmission(t *testing.T) {
	digestA := strings.Repeat("a", 64)
	digestB := strings.Repeat("b", 64)
	want := runner.ReplayMessage{
		Kind: runner.ReplayOccurrenceKinds.CONTROL,
		Control: runner.ReplayControl{
			Version: 1, ID: "control-1", TurnID: "turn-1", PolicyLabel: "verify",
			SystemDigest: digestA, SurfaceDigest: digestB,
			ToolDefinitions: []runner.ControlToolDefinition{{Name: "read", DefinitionHash: digestA}},
		},
	}
	data, err := runner.MarshalReplayMessage(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := runner.UnmarshalReplayMessage(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) || got.AdmittedToModelContext() || !got.IsControl() {
		t.Fatalf("control round trip = %+v", got)
	}
	got.Control.ToolDefinitions[0].Name = "changed"
	if want.Control.ToolDefinitions[0].Name != "read" {
		t.Fatal("decoded control aliases input")
	}
}

func TestReplayControlRejectsMixedAndInvalidPayloads(t *testing.T) {
	digest := strings.Repeat("a", 64)
	control := runner.ReplayControl{
		Version: 1, ID: "control-1", PolicyLabel: "build",
		SystemDigest: digest, SurfaceDigest: digest,
	}
	for _, record := range []runner.ReplayMessage{
		{Control: control, Message: llm.Message{Role: llm.RoleUser, Content: "message"}},
		{Kind: runner.ReplayOccurrenceKinds.TOOLEXECUTION, Tool: &runner.ToolOutput{}, Control: control},
		{Kind: runner.ReplayOccurrenceKinds.CONTROL, Control: control, Message: llm.Message{Role: llm.RoleSystem}},
		{Kind: runner.ReplayOccurrenceKinds.CONTROL, Control: control, Interrupted: true},
		{Kind: runner.ReplayOccurrenceKinds.CONTROL, Control: runner.ReplayControl{Version: 2}},
		{Kind: runner.ReplayOccurrenceKinds.CONTROL, Control: runner.ReplayControl{
			Version: 1, ID: "control-bad-turn", TurnID: "raw\ncontent", PolicyLabel: "build",
			SystemDigest: digest, SurfaceDigest: digest,
		}},
	} {
		if _, err := runner.MarshalReplayMessage(record); err == nil {
			t.Fatalf("accepted mixed control: %+v", record)
		}
	}

	for _, wire := range []string{
		`{"kind":"control","message":{},"control":{"version":1,"id":"x","policy_label":"build","system_digest":"bad","surface_digest":"bad"}}`,
		`{"kind":"control","message":{"role":"system"},"control":{"version":1,"id":"x","policy_label":"build","system_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","surface_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}`,
	} {
		if _, err := runner.UnmarshalReplayMessage([]byte(wire)); err == nil {
			t.Fatalf("accepted invalid wire: %s", wire)
		}
	}

	legacy, err := runner.UnmarshalReplayMessage([]byte(`{"message":{"role":"assistant","content":"legacy"}}`))
	if err != nil || legacy.Kind != runner.ReplayOccurrenceKinds.MESSAGE || !legacy.AdmittedToModelContext() {
		t.Fatalf("legacy message = %+v, %v", legacy, err)
	}
}
