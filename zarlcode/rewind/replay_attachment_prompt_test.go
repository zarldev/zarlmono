package rewind_test

import (
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestResumeAdmitsRunnerAttachmentPrompt(t *testing.T) {
	t.Parallel()
	for _, provider := range []string{"openai", "openai-codex"} {
		t.Run(provider, func(t *testing.T) {
			t.Parallel()
			messages := []llm.Message{{Role: llm.RoleUser, Content: "inspect",
				Parts: []llm.ContentPart{llm.TextPart("inspect"), llm.TextPart("  file bytes\n"),
					llm.ImagePartFromDataURI("data:image/png;base64,eA==", "image/png")}}}
			data, err := rewind.EncodeResume(1, messages, rewind.Target{Provider: provider, Model: "saved-model"}, "turn", 1, rewind.InitialContinuation{})
			if err != nil {
				t.Fatal(err)
			}
			state, err := rewind.DecodeResume(data)
			if err != nil || !reflect.DeepEqual(state.Context, messages) {
				t.Fatalf("attachment representation changed: %v", err)
			}
		})
	}
}
