package runner

import (
	"context"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
)

// CompactionSettled reports one LLM-backed compaction attempt, even if it failed
// or made no history change. Its usage is NOT included in ConversationEnded's
// TotalUsage; consumers settle it separately using Inference's target. Inference
// owns its evidence independently of the compactor and terminal TaskResult.
type CompactionSettled struct {
	TaskID    taskscope.ID
	Depth     int
	Inference compact.Inference
	Err       error
}

// OnCompactionSettled discards the event.
func (NopSink) OnCompactionSettled(context.Context, CompactionSettled) {}

// OnCompactionSettled forwards the event under the sink mutex.
func (s *SyncSink) OnCompactionSettled(ctx context.Context, e CompactionSettled) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sink.OnCompactionSettled(ctx, e)
}

func (t *taskRun) settleCompaction(ctx context.Context, observation *compact.Inference, err error) {
	if observation == nil {
		return
	}
	owned := observation.Clone()
	t.compactionObservations = append(t.compactionObservations, owned)
	t.r.sink.OnCompactionSettled(ctx, CompactionSettled{
		TaskID: t.spec.ID, Depth: t.spec.Depth, Inference: owned.Clone(), Err: err,
	})
}

func cloneCompactionObservations(observations []compact.Inference) []compact.Inference {
	if observations == nil {
		return nil
	}
	clones := make([]compact.Inference, len(observations))
	for i, observation := range observations {
		clones[i] = observation.Clone()
	}
	return clones
}
