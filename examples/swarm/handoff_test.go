package main_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	experiment "github.com/zarldev/zarlmono/examples/swarm"
	"github.com/zarldev/zarlmono/zkit/agent/board"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	boardtools "github.com/zarldev/zarlmono/zkit/agent/tools/board"
)

// The researcher cannot post until the implementer has observed its absence.
// The implementer cannot finish that response until the research is published.
// These callback barriers force a late finding within one activation, without
// reaching into the swarm or changing the script's evidence.
type handoffSink struct {
	runner.NopSink
	mu          sync.Mutex
	researcher  taskscope.ID
	implementer taskscope.ID
	waiting     taskscope.ID
	refreshed   bool
	ready       chan struct{}
	posted      chan struct{}
	readyOnce   sync.Once
	postOnce    sync.Once
}

func (s *handoffSink) OnConversationStarted(_ context.Context, event runner.ConversationStarted) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch event.AgentName {
	case "researcher":
		s.researcher = event.TaskID
	case "implementer":
		s.implementer = event.TaskID
	}
}

func (s *handoffSink) OnToolStarted(ctx context.Context, event runner.ToolStarted) {
	s.mu.Lock()
	posting := event.TaskID == s.researcher && event.ToolName == boardtools.PostName.String()
	if event.TaskID == s.waiting && event.ToolName == boardtools.ReadName.String() {
		s.refreshed = true
	}
	s.mu.Unlock()
	if posting {
		select {
		case <-s.ready:
		case <-ctx.Done():
		}
	}
}

func (s *handoffSink) OnToolCompleted(_ context.Context, event runner.ToolCompleted) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if event.TaskID == s.researcher && event.ToolName == boardtools.PostName.String() {
		s.postOnce.Do(func() { close(s.posted) })
	}
	if event.TaskID != s.implementer || event.ToolName != boardtools.ListName.String() {
		return
	}
	page, ok := event.Result.(board.Page)
	if ok && len(page.Posts) == 1 && page.Posts[0].Author == "host" {
		s.waiting = event.TaskID
	}
}

func (s *handoffSink) OnContent(ctx context.Context, event runner.Content) {
	s.mu.Lock()
	waiting := event.TaskID == s.waiting && strings.HasPrefix(event.Delta, "Prerequisite not yet available")
	s.mu.Unlock()
	if waiting {
		s.readyOnce.Do(func() { close(s.ready) })
		select {
		case <-s.posted:
		case <-ctx.Done():
		}
	}
}

func TestLatePrerequisiteRefreshesWithinSameActivation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sink := &handoffSink{ready: make(chan struct{}), posted: make(chan struct{})}
		report, err := experiment.RunExperiment(t.Context(), experiment.ExperimentLimits(), sink)
		if err != nil {
			t.Fatal(err)
		}
		// RunExperiment has joined every callback before returning.
		if sink.waiting == "" || !sink.refreshed || !report.Complete || !report.ShutdownJoined {
			t.Fatalf("late prerequisite not handled in the same turn: waiting=%s refreshed=%t report=%+v", sink.waiting, sink.refreshed, report)
		}
	})
}
