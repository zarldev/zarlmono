package tui_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestOrdinaryResumeRejectsIncompatibleActiveRouteWithoutPublication(t *testing.T) {
	f := newBeforeFixture(t)
	f.provider.check = func(context.Context) {}
	settleRewindTurn(t, f, "current transcript")
	id := f.ui.SessionIdentity()
	record, err := f.store.GetSession(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	record.ContextJSON, err = json.Marshal([]llm.Message{{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.ContentTypeImage, Image: &llm.ImageData{DataURI: "data:image/png;base64,YQ==", MIMEType: "image/png"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.SaveSession(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	f.live.SetProviderSpec(f.provider, engine.ProviderSpec{Name: "claude-code", Model: "sonnet"})
	before := f.live.ContextSnapshot()
	thread := f.ui.CanonicalThread()
	if err := f.ui.ResumeSavedSession(t.Context(), record.ID); !errors.Is(err, rewind.ErrTarget) {
		t.Fatalf("incompatible route was not rejected: %v", err)
	}
	active, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
	if err != nil || active != id || f.ui.SessionIdentity() != id || !reflect.DeepEqual(f.ui.CanonicalThread(), thread) || !reflect.DeepEqual(f.live.ContextSnapshot(), before) {
		t.Fatal("incompatible resume mutated active state")
	}
}
