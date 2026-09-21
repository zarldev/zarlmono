package rewind_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestExactResumePreservesOpaqueContext(t *testing.T) {
	t.Parallel()
	target := rewind.Target{Provider: "openai", Model: "historical"}
	messages := []llm.Message{{Role: llm.RoleUser, Parts: []llm.ContentPart{llm.TextPart("raw\xff\x00")}},
		{Role: llm.RoleAssistant, Content: "~zarl-history-bytes:literal"}}
	data, err := rewind.EncodeResume(1, messages, target, "turn", 1, rewind.InitialContinuation{})
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(data) {
		t.Fatal("resume is not valid storage JSON")
	}
	state, err := rewind.DecodeResume(data)
	if err != nil || !reflect.DeepEqual(state.Context, messages) || state.Target != target {
		t.Fatalf("resume changed opaque context: %v", err)
	}
}

func TestExactResumeRejectsInvalidDirectJSON(t *testing.T) {
	t.Parallel()
	target := rewind.Target{Provider: "openai", Model: "historical"}
	data, err := rewind.EncodeResume(1, []llm.Message{{Role: llm.RoleUser, Content: "canary"}}, target, "turn", 1, rewind.InitialContinuation{})
	if err != nil {
		t.Fatal(err)
	}
	for _, replacement := range [][]byte{[]byte("raw\xff"), []byte(`\ud800`)} {
		bad := bytes.Replace(data, []byte("canary"), replacement, 1)
		if _, err := rewind.DecodeResume(bad); !errors.Is(err, rewind.ErrInvalid) {
			t.Fatalf("direct JSON admission = %v", err)
		}
	}
	target.Model = "invalid\xff"
	if _, err := rewind.EncodeResume(1, []llm.Message{{Role: llm.RoleUser, Content: "valid"}}, target, "turn", 1, rewind.InitialContinuation{}); !errors.Is(err, rewind.ErrInvalid) {
		t.Fatalf("metadata admission = %v", err)
	}
}

func TestHistoryBranchPreservesOpaqueResume(t *testing.T) {
	input := captureInput(t)
	input.Context[1].Content = "raw\xff\x00"
	store, checkpoint := persistHistoryCheckpoint(t, input, input.Context)
	branch, err := checkpoint.PrepareBranch("opaque-child", "child", input.Transcript.Revision(), input.Target)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateCheckpointBranch(t.Context(), branch); err != nil {
		t.Fatal(err)
	}
	child, err := store.GetSession(t.Context(), "opaque-child")
	if err != nil {
		t.Fatal(err)
	}
	state, err := rewind.DecodeResume(child.ContextJSON)
	if err != nil || !reflect.DeepEqual(state.Context, input.Context) {
		t.Fatalf("stored branch resume changed opaque bytes: %v", err)
	}
}
