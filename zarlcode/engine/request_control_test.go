package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/home"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/options"
)

type requestControlRecordingSink struct {
	liveRecordingSink
	mu          sync.Mutex
	controls    []runner.ReplayControl
	terminal    []runner.ConversationEnded
	diagnostics func()
}

func (s *requestControlRecordingSink) RequestControlRecorded(_ context.Context, control runner.ReplayControl) {
	s.mu.Lock()
	s.controls = append(s.controls, control.Clone())
	s.mu.Unlock()
}

func (s *requestControlRecordingSink) PromptDiagnostics([]string) {
	if s.diagnostics != nil {
		s.diagnostics()
	}
}

func (s *requestControlRecordingSink) OnConversationEnded(_ context.Context, event runner.ConversationEnded) {
	s.mu.Lock()
	s.terminal = append(s.terminal, event)
	s.mu.Unlock()
}

func (s *requestControlRecordingSink) snapshot() ([]runner.ReplayControl, []runner.ConversationEnded) {
	s.mu.Lock()
	defer s.mu.Unlock()
	controls := make([]runner.ReplayControl, len(s.controls))
	for i, control := range s.controls {
		controls[i] = control.Clone()
	}
	return controls, append([]runner.ConversationEnded(nil), s.terminal...)
}

func TestRequestControlCaptureDedupResumeAndProjection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := db.Open(t.Context(), t.TempDir()+"/controls.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	initializeControlSession(t, store, "session")

	workspace := t.TempDir()
	sink := &requestControlRecordingSink{}
	live := requestControlRunner(t, &requestRecordingProvider{}, workspace, engine.WithLiveSink(sink))
	for i, plan := range []bool{false, true, false, false} {
		live.SetPlanMode(plan)
		if err := live.RunRecordedTurn(t.Context(), "private prompt", nil, store, "session"); err != nil {
			t.Fatalf("turn %d: %v", i, err)
		}
	}

	controls, err := engine.ReadRequestControls(t.Context(), store, "session")
	if err != nil {
		t.Fatal(err)
	}
	projected, _ := sink.snapshot()
	if len(controls) != 3 || len(projected) != 3 {
		t.Fatalf("controls=%d projected=%d, want 3", len(controls), len(projected))
	}
	for i, want := range []string{"build", "plan", "build"} {
		if controls[i].PolicyLabel != want || projected[i].PolicyLabel != want {
			t.Fatalf("control %d labels = %q/%q, want %q", i, controls[i].PolicyLabel, projected[i].PolicyLabel, want)
		}
		if controls[i].TurnID == "" || projected[i].TurnID != controls[i].TurnID {
			t.Fatalf("control %d turn IDs = %q/%q", i, controls[i].TurnID, projected[i].TurnID)
		}
	}
	if controls[0].ID == controls[1].ID || controls[0].ID == controls[2].ID || controls[1].ID == controls[2].ID {
		t.Fatal("control occurrence IDs were reused")
	}
	if controls[0].SurfaceDigest == controls[1].SurfaceDigest || controls[0].SurfaceDigest != controls[2].SurfaceDigest {
		t.Fatalf("build/plan/build surfaces = %q %q %q", controls[0].SurfaceDigest, controls[1].SurfaceDigest, controls[2].SurfaceDigest)
	}

	resumedSink := &requestControlRecordingSink{}
	resumed := requestControlRunner(t, &requestRecordingProvider{}, workspace, engine.WithLiveSink(resumedSink))
	if err := resumed.RunRecordedTurn(t.Context(), "resumed private prompt", nil, store, "session"); err != nil {
		t.Fatal(err)
	}
	controls, err = engine.ReadRequestControls(t.Context(), store, "session")
	resumedProjected, _ := resumedSink.snapshot()
	if err != nil || len(controls) != 3 || len(resumedProjected) != 0 {
		t.Fatalf("resumed controls = %d projected=%d, %v", len(controls), len(resumedProjected), err)
	}
}

func TestRequestControlDriftStopsProvider(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	if err := os.MkdirAll(filepath.Join(homeDir, ".zarlcode", home.PreferencesFile), 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(t.Context(), t.TempDir()+"/drift.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	initializeControlSession(t, store, "session")

	provider := &requestRecordingProvider{}
	sink := &requestControlRecordingSink{}
	live := requestControlRunner(t, provider, t.TempDir(), engine.WithLiveSink(sink))
	sink.diagnostics = func() { live.SetPlanMode(true) }
	if err := live.RunRecordedTurn(t.Context(), "must not dispatch", nil, store, "session"); err != nil {
		t.Fatal(err)
	}
	_, terminal := sink.snapshot()
	if len(provider.requests) != 0 {
		t.Fatal("provider called after control drift")
	}
	if len(terminal) != 1 || terminal[0].Reason != runner.TerminalError || !strings.Contains(terminal[0].Error, engine.ErrRequestControlDrift.Error()) {
		t.Fatalf("terminal events = %+v, want request-control drift", terminal)
	}
}

func TestRequestControlFailedCaptureRetriesSameControl(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := db.Open(t.Context(), t.TempDir()+"/retry.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	provider := &requestRecordingProvider{}
	live := requestControlRunner(t, provider, t.TempDir())
	if err := live.RunRecordedTurn(t.Context(), "retryable", nil, store, "session"); err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 0 {
		t.Fatal("provider called before request capture became durable")
	}
	pending := live.RecordedHistory("session")
	pendingControl := pendingControl(t, pending)
	controlID := pendingControl.ID
	if controlID == "" {
		t.Fatal("failed capture did not retain pending control")
	}

	initializeControlSession(t, store, "session")
	if err := live.RunRecordedTurn(t.Context(), "retryable", nil, store, "session"); err != nil {
		t.Fatal(err)
	}
	controls, err := engine.ReadRequestControls(t.Context(), store, "session")
	if err != nil || len(controls) != 1 || controls[0].ID != controlID || controls[0].TurnID != pendingControl.TurnID {
		t.Fatalf("retried controls = %+v, pending=%+v, %v", controls, pendingControl, err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(provider.requests))
	}
}

func pendingControl(t *testing.T, batches []db.SessionHistoryBatch) runner.ReplayControl {
	t.Helper()
	for _, batch := range batches {
		for _, data := range batch.Messages {
			record, err := runner.UnmarshalReplayMessage(data)
			if err != nil {
				t.Fatal(err)
			}
			if record.IsControl() {
				return record.Control
			}
		}
	}
	return runner.ReplayControl{}
}

func requestControlRunner(t *testing.T, provider llm.Provider, root string, opts ...options.Option[engine.LiveRunner]) *engine.LiveRunner {
	t.Helper()
	workspace, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	live := engine.NewLiveRunner(provider, workspace, "saved-model", opts...)
	live.SetProviderSpec(provider, engine.ProviderSpec{Name: "openai", Model: "saved-model", APIKey: "private-key-canary"})
	t.Cleanup(func() {
		if err := live.Close(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})
	return live
}

func initializeControlSession(t *testing.T, store *db.Store, sessionID string) {
	t.Helper()
	if err := store.SaveActiveSession(t.Context(), db.SessionRecord{ID: sessionID, Workspace: "/workspace"}); err != nil {
		t.Fatal(err)
	}
	ref, err := store.CaptureSessionHistory(t.Context(), sessionID, nil, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSessionCheckpoint(t.Context(), db.SessionCheckpoint{
		SessionID: sessionID, ID: "initial", SourceSessionID: sessionID,
		Workspace: "/workspace", BoundaryID: "prompt", FormatVersion: 2,
		Payload: []byte(`{}`), History: ref,
	}); err != nil {
		t.Fatal(err)
	}
}
