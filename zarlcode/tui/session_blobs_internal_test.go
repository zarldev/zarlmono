package tui

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestWorkingSet_DiffBodiesRoundTrip(t *testing.T) {
	ws := NewWorkingSet("/ws")
	ws.RecordDiff("a.go", "--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old\n+new\n")
	ws.RecordDiff("b.go", "--- a/b.go\n+++ b/b.go\n@@ -0,0 +1 @@\n+added\n")

	bodies := ws.DiffBodies()
	if len(bodies) != 2 {
		t.Fatalf("DiffBodies = %d entries, want 2", len(bodies))
	}

	// Replay into a fresh working set and confirm the Files dock view
	// reconstructs the same changed files.
	restored := NewWorkingSet("/ws")
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

func TestRunState_UsageSnapshotRoundTrip(t *testing.T) {
	var rs RunState
	rs.sessionTurns = 4
	rs.sessionToolCalls = 11
	rs.sessionIn = 2000
	rs.sessionOut = 800
	rs.sessionCached = 500
	rs.sessionInParent = 1500
	rs.sessionOutParent = 700
	rs.sessionCachedParent = 400
	rs.sessionCostUSD = 1.23
	rs.sessionCostParentUSD = 1.00
	rs.sessionCacheSavedUSD = 0.45

	snap := rs.UsageSnapshot()
	blob, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded SessionUsageSnapshot
	if err := json.Unmarshal(blob, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var fresh RunState
	fresh.RestoreUsage(decoded)
	if !reflect.DeepEqual(fresh.UsageSnapshot(), snap) {
		t.Errorf("usage round trip mismatch: %+v != %+v", fresh.UsageSnapshot(), snap)
	}
}

func TestEncodePlanJSON_EmptyIsNull(t *testing.T) {
	got, err := encodePlanJSON(code.Plan{})
	if err != nil {
		t.Fatalf("encode empty plan: %v", err)
	}
	if string(got) != "null" {
		t.Errorf("empty plan = %q, want null", got)
	}
	plan := code.Plan{Steps: []code.PlanStep{{Text: "do it", Status: code.StepStatuses.COMPLETED}}}
	got, err = encodePlanJSON(plan)
	if err != nil {
		t.Fatalf("encode plan: %v", err)
	}
	if string(got) == "null" || len(got) == 0 {
		t.Errorf("non-empty plan should marshal, got %q", got)
	}
}

func TestDecodeSavedSession_HydratesAuxBlobs(t *testing.T) {
	plan := code.Plan{
		Explanation: "the plan",
		Steps: []code.PlanStep{
			{Text: "first", Status: code.StepStatuses.COMPLETED},
			{Text: "second", Status: code.StepStatuses.INPROGRESS},
		},
	}
	usage := SessionUsageSnapshot{Turns: 3, ToolCalls: 9, In: 100, Out: 50, Cached: 20}
	diff := map[string]string{"foo.go": "--- a\n+++ b\n@@ -1 +1 @@\n-x\n+y\n"}

	planJSON, _ := json.Marshal(plan)
	usageJSON, _ := json.Marshal(usage)
	diffJSON, _ := json.Marshal(diff)

	rec := db.SessionRecord{
		ID:             "s1",
		HistoryJSON:    []byte(`[{"role":"user","content":"hi"}]`),
		PlanJSON:       planJSON,
		LastUsageJSON:  usageJSON,
		DiffBodiesJSON: diffJSON,
	}
	s, err := decodeSavedSession(rec)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(s.History) != 1 {
		t.Fatalf("history = %d, want 1", len(s.History))
	}
	if s.Plan.Explanation != "the plan" || len(s.Plan.Steps) != 2 || s.Plan.Steps[1].Status != code.StepStatuses.INPROGRESS {
		t.Errorf("plan not hydrated: %+v", s.Plan)
	}
	if !reflect.DeepEqual(s.Usage, usage) {
		t.Errorf("usage not hydrated: %+v", s.Usage)
	}
	if s.DiffBodies["foo.go"] != diff["foo.go"] {
		t.Errorf("diff bodies not hydrated: %+v", s.DiffBodies)
	}
}

// A corrupt auxiliary blob must not block resuming the conversation.
func TestDecodeSavedSession_CorruptBlobStillLoadsHistory(t *testing.T) {
	rec := db.SessionRecord{
		ID:            "s2",
		HistoryJSON:   []byte(`[{"role":"user","content":"hi"},{"role":"assistant","content":"yo"}]`),
		PlanJSON:      []byte(`{ this is not json `),
		LastUsageJSON: []byte(`also broken`),
	}
	s, err := decodeSavedSession(rec)
	if err != nil {
		t.Fatalf("a corrupt aux blob must not fail the load: %v", err)
	}
	if len(s.History) != 2 {
		t.Errorf("history should still load, got %d", len(s.History))
	}
	if len(s.Plan.Steps) != 0 {
		t.Errorf("corrupt plan should decode to empty, got %+v", s.Plan)
	}
}

func TestRunState_RestoreOldUsageSnapshotWithoutCostFields(t *testing.T) {
	blob := []byte(`{"turns":3,"tool_calls":9,"in":100,"out":50,"cached":20,"in_parent":80,"out_parent":40,"cached_parent":10}`)
	var snap SessionUsageSnapshot
	if err := json.Unmarshal(blob, &snap); err != nil {
		t.Fatalf("unmarshal old snapshot: %v", err)
	}
	var rs RunState
	rs.RestoreUsage(snap)
	if rs.sessionTurns != 3 || rs.sessionIn != 100 || rs.sessionOut != 50 || rs.sessionCached != 20 {
		t.Fatalf("tokens not restored from old snapshot: %+v", rs.UsageSnapshot())
	}
	if rs.sessionCost() != 0 || rs.sessionCostParent() != 0 || rs.cacheSaved() != 0 {
		t.Fatalf("old snapshot should restore zero cost fields: cost=%v parent=%v saved=%v", rs.sessionCost(), rs.sessionCostParent(), rs.cacheSaved())
	}
}

func TestSaveSessionCmdUsesCapturedSnapshot(t *testing.T) {
	m := New()
	m.settings = newTestSettings(t)
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	m.live = engine.NewLiveRunner(nil, ws, "model-a")
	m.live.RestoreHistory([]llm.Message{{Role: llm.RoleUser, Content: "before"}})
	m.session.Provider = "provider-a"
	m.session.Model = "model-a"

	cmd := m.saveSessionCmd()
	capturedID := m.session.ID
	m.live.RestoreHistory([]llm.Message{{Role: llm.RoleUser, Content: "after"}})
	m.session.Provider = "provider-b"
	m.session.Model = "model-b"
	if msg := cmd(); msg != nil {
		t.Fatalf("save command message = %#v", msg)
	}

	rec, err := m.settings.Store.GetSession(t.Context(), capturedID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if rec.Provider != "provider-a" || rec.Model != "model-a" {
		t.Fatalf("saved target = %s/%s, want provider-a/model-a", rec.Provider, rec.Model)
	}
	var history []llm.Message
	if err := json.Unmarshal(rec.HistoryJSON, &history); err != nil {
		t.Fatalf("decode history: %v", err)
	}
	if len(history) != 1 || history[0].Content != "before" {
		t.Fatalf("saved history = %+v, want captured history", history)
	}
	activeID, err := m.settings.Store.GetSettingExact(t.Context(), m.settings.WorkspaceRoot(), activeSessionKey)
	if err != nil {
		t.Fatalf("get active session: %v", err)
	}
	if activeID != capturedID {
		t.Fatalf("active session = %q, want %q", activeID, capturedID)
	}
}
