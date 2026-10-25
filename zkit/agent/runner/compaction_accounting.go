package runner

import (
	"context"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
)

// CompactionSettled reports one LLM-backed compaction attempt, even if it failed
// or made no history change. Its usage is NOT included in ConversationEnded's
// TotalUsage; consumers settle it separately using Inference's target.
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
