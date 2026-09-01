package tui_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestWorkingSet_DiffBodiesRoundTrip(t *testing.T) {
	ws := tui.NewWorkingSet("/ws")
	ws.RecordDiff("a.go", "--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old\n+new\n")
	ws.RecordDiff("b.go", "--- a/b.go\n+++ b/b.go\n@@ -0,0 +1 @@\n+added\n")

	bodies := ws.DiffBodies()
	if len(bodies) != 2 {
		t.Fatalf("DiffBodies = %d entries, want 2", len(bodies))
	}

	// Replay into a fresh working set and confirm the Files dock view
	// reconstructs the same changed files.
	restored := tui.NewWorkingSet("/ws")
	restored.RestoreDiffBodies(bodies, time.Unix(1000, 0))
	files := restored.FilesChangedThisSession()
	if len(files) != 2 {
		t.Fatalf("restored files = %d, want 2", len(files))
	}
	got := map[string]bool{}
	for _, f := range files {
		got[f.Path] = true
	}
	if !got["a.go"] || !got["b.go"] {
		t.Errorf("restored files missing entries: %v", got)
	}
	// The diff body must survive so the viewer can render it.
	muts := restored.MutationsForFile("a.go")
	if len(muts) != 1 || muts[0].Diff != bodies["a.go"] {
		t.Errorf("restored diff body for a.go not preserved: %+v", muts)
	}
}

func TestRunStateUsageSnapshotRoundTrip(t *testing.T) {
	snap := tui.SessionUsageSnapshot{Turns: 4, ToolCalls: 11, In: 2000, Out: 800, Cached: 500, InParent: 1500, OutParent: 700, CachedParent: 400, CostUSD: 1.23, CostParentUSD: 1, CacheSavedUSD: .45}
	var rs tui.RunState
	rs.RestoreUsage(snap)
	blob, err := json.Marshal(rs.UsageSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	var decoded tui.SessionUsageSnapshot
	if err := json.Unmarshal(blob, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, snap) {
		t.Fatalf("usage round trip=%+v want %+v", decoded, snap)
	}
}
