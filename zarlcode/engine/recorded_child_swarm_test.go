package engine_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
)

func TestRecordedChildSwarmActualCapture(t *testing.T) {
	store := recordedAccountingStore(t)
	var parents, children atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		child := false
		for _, message := range request.Messages {
			if message.Role == "user" && strings.Contains(string(message.Content), "recorded child assignment") {
				child = true
			}
		}
		if child {
			children.Add(1)
			completionFixture(w, "Read-only child report complete.", "")
			return
		}
		if parents.Add(1) == 1 {
			completionFixture(w, "", `{"prompt":"recorded child assignment","mode":"explore"}`)
			return
		}
		completionFixture(w, "Read-only parent report complete.", "")
	}))
	t.Cleanup(server.Close)
	sink := &childRateCardSink{}
	live := automaticFixtureRunner(t, server.URL, engine.WithReadOnlyTasks(), engine.WithLiveSink(sink))
	live.SetProviderSpec(live.RunTarget().Provider, engine.ProviderSpec{Name: "openai", Model: "fixture-model"})
	live.SetLimits(1024, 8, 2, 2)
	goal := engine.NewSwarm("capture root only")
	var rootID string
	if _, err := goal.AddRecorded("parent", live, "Delegate inspection and summarize; no changes.", func(ctx context.Context, a *engine.RecordedSwarmActivation) error {
		rootID = string(a.Spec.ID)
		return a.RunRecorded(ctx, store, "session").Err
	}); err != nil {
		t.Fatal(err)
	}
	startEngineSwarm(t, goal)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := goal.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	_, rows := recordedAccountingSnapshot(t, live, "session")
	if children.Load() != 1 || parents.Load() < 2 || len(rows) != int(parents.Load()) {
		t.Fatalf("parents=%d children=%d captured=%d", parents.Load(), children.Load(), len(rows))
	}
	for _, row := range rows {
		if got := decodeRecordedAccounting(t, row.PayloadJSON); got.TaskID != rootID {
			t.Fatal("child settlement captured as root")
		}
	}
	_, combinedRoots, capturedChildren, err := live.RecordedHistoryWithAccounting("session")
	if err != nil || len(capturedChildren) != 1 {
		t.Fatal("swarm child capture", err)
	}
	got, err := engine.DecodeRecordedChildProviderAccounting(capturedChildren[0].PayloadJSON)
	if err != nil || got.OwnerRequestBatchID != combinedRoots[0].RequestBatchID || got.ChildTaskID == rootID || got.Provider != "openai" || got.Model != "fixture-model" {
		t.Fatal("swarm identity", err)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	found := false
	for _, event := range sink.events {
		if event.Depth == 1 {
			found = true
		}
	}
	if !found {
		t.Fatal("child never settled")
	}
}
