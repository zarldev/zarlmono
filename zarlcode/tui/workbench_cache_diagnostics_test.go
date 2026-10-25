package tui_test

import (
	"context"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestWorkbenchCacheDiagnosticsAreOwnedAndRevisioned(t *testing.T) {
	fixture := newBeforeFixture(t)
	fixture.provider.check = func(context.Context) {}
	workbench := startWorkbench(t, fixture)
	initial, err := workbench.Request(t.Context(), tui.WorkbenchCommand{Action: "snapshot"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workbench.Request(t.Context(), tui.WorkbenchCommand{Action: "submit", Text: "diagnose task usage"}); err != nil {
		t.Fatal(err)
	}
	settled := awaitWorkbench(t, workbench, func(snapshot tui.WorkbenchSnapshot) bool { return snapshot.State == "ready" })
	if settled.Usage.CacheDiagnosticsVersion != 1 || len(settled.Usage.CacheTargets) != 1 {
		t.Fatalf("missing physical-target diagnostics: %+v", settled.Usage)
	}
	target := settled.Usage.CacheTargets[0]
	if target.Provider != settled.Provider || target.Model != settled.Model || target.SettledTasks != 1 || target.MissingUsageTasks != 1 {
		t.Fatalf("unexpected unreported usage attribution: %+v", target)
	}
	if target.CacheReadShareKnown || target.KnownCostUSD != 0 {
		t.Fatalf("unreported usage invented a read share or cost: %+v", target)
	}
	if initial.Revision == settled.Revision {
		t.Fatal("diagnostics did not affect workbench revision")
	}
	settled.Usage.CacheTargets[0].Model = "caller-mutated"
	again, err := workbench.Request(t.Context(), tui.WorkbenchCommand{Action: "snapshot"})
	if err != nil {
		t.Fatal(err)
	}
	if again.Usage.CacheTargets[0].Model != target.Model {
		t.Fatal("workbench snapshot leaked mutable usage state")
	}
}
