package tui_test

import (
	"context"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestWorkbenchInitialModeRouteFailureBlocksDispatch(t *testing.T) {
	fixture := newBeforeFixture(t)
	fixture.provider.check = func(context.Context) { t.Error("old physical provider dispatched while route failed") }
	settings := engine.NewSettings(fixture.store, nil, nil, fixture.ws.Root())
	fixture.ui.SetSettings(settings)
	if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyMainBuildProvider, "unknown-mode-provider"); err != nil {
		t.Fatal(err)
	}
	workbench := startWorkbench(t, fixture)
	failed := awaitWorkbench(t, workbench, func(snapshot tui.WorkbenchSnapshot) bool { return snapshot.State == "route-error" })
	if fixture.live.RunTarget().Model != "saved-model" || failed.Error == "" {
		t.Fatalf("failed route replaced physical target or hid error: %+v", failed)
	}
	if _, err := workbench.Request(t.Context(), tui.WorkbenchCommand{Action: "submit", Text: "must not dispatch"}); err == nil {
		// The UI can reject via its error snapshot rather than a transport error.
		current, snapshotErr := workbench.Request(t.Context(), tui.WorkbenchCommand{Action: "snapshot"})
		if snapshotErr != nil || current.State != "route-error" {
			t.Fatalf("blocked route lost error state: %s, %v", current.State, snapshotErr)
		}
	}
	if _, err := workbench.Request(t.Context(), tui.WorkbenchCommand{Action: "mode", Text: "plan"}); err != nil {
		t.Fatalf("failed route blocked mode recovery: %v", err)
	}
	recovered := awaitWorkbench(t, workbench, func(snapshot tui.WorkbenchSnapshot) bool { return snapshot.State == "ready" && snapshot.Mode == "plan" })
	if fixture.live.RunTarget().Model != "saved-model" || recovered.Mode != "plan" {
		t.Fatal("inherited Plan route should retain the actual active target")
	}
}

func TestWorkbenchNoopModeCommandPreservesManualOverride(t *testing.T) {
	fixture := newBeforeFixture(t)
	fixture.provider.check = func(context.Context) {}
	workbench := startWorkbench(t, fixture)
	initial, err := workbench.Request(t.Context(), tui.WorkbenchCommand{Action: "snapshot"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workbench.Request(t.Context(), tui.WorkbenchCommand{Action: "mode", Text: initial.Mode}); err != nil {
		t.Fatal(err)
	}
	current, err := workbench.Request(t.Context(), tui.WorkbenchCommand{Action: "snapshot"})
	if err != nil || current.Model != initial.Model || strings.Contains(current.Error, "prepare") {
		t.Fatalf("no-op mode request altered target: %+v, %v", current, err)
	}
}
