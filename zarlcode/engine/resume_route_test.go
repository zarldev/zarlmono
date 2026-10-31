package engine_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestOrdinaryRestoreRejectsMetadataUnsupportedByActiveRoute(t *testing.T) {
	provider := &requestRecordingProvider{}
	live := reservationRunner(t, provider)
	live.SetProviderSpec(provider, engine.ProviderSpec{Name: "claude-code", Model: "sonnet"})
	live.RestoreContext([]llm.Message{{Role: llm.RoleUser, Content: "active context"}})
	before := live.ContextSnapshot()
	reservation, err := live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer reservation.Release()
	messages := []llm.Message{{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.ContentTypeImage, Image: &llm.ImageData{DataURI: "data:image/png;base64,YQ==", MIMEType: "image/png"}}}}}
	if err := reservation.RestoreSessionContext(messages, engine.MCPActivations{}); !errors.Is(err, rewind.ErrTarget) {
		t.Fatalf("incompatible context accepted: %v", err)
	}
	if !reflect.DeepEqual(live.ContextSnapshot(), before) {
		t.Fatal("rejected context was published")
	}
	// Ordinary plain text remains portable between providers.
	if err := reservation.RestoreSessionContext([]llm.Message{{Role: llm.RoleUser, Content: "portable"}}, engine.MCPActivations{}); err != nil {
		t.Fatal(err)
	}
}
