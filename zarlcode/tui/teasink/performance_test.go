package teasink_test

import (
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestSinkForwardsOwnedPerformanceTimings(t *testing.T) {
	send, snapshot := identityRecordingSend()
	sink := teasink.New(send)
	t.Cleanup(sink.Close)
	first := time.Duration(0)
	usage := &llm.Usage{CachedTokens: 12}
	sink.OnContent(t.Context(), runner.Content{TaskID: "root", Delta: "before"})
	sink.OnProviderAttemptSettled(t.Context(), runner.ProviderAttemptSettled{TaskID: "root", Attempt: 1, Duration: 3 * time.Second, CallbackDuration: time.Second, TimeToFirstOutput: &first, Usage: usage})
	first, usage.CachedTokens = time.Hour, 999
	sink.OnProviderAttemptSettled(t.Context(), runner.ProviderAttemptSettled{TaskID: "root", Attempt: 2})
	sink.OnIterationCompleted(t.Context(), runner.IterationCompleted{TaskID: "root", RequestPreparationDuration: time.Second, ToolDispatchDuration: 2 * time.Second})
	sink.Drain()
	messages := snapshot()
	if len(messages) != 4 {
		t.Fatalf("messages=%d", len(messages))
	}
	if _, ok := messages[0].(teasink.ContentMsg); !ok {
		t.Fatalf("content not flushed: %T", messages[0])
	}
	attempt := messages[1].(teasink.ProviderAttemptSettledMsg)
	if attempt.Duration != 3*time.Second || attempt.CallbackDuration != time.Second || attempt.TimeToFirstOutput == nil || *attempt.TimeToFirstOutput != 0 || attempt.Usage.CachedTokens != 12 {
		t.Fatalf("attempt=%+v", attempt)
	}
	if messages[2].(teasink.ProviderAttemptSettledMsg).TimeToFirstOutput != nil {
		t.Fatal("missing output became measured zero")
	}
	iteration := messages[3].(teasink.IterationCompletedMsg)
	if iteration.RequestPreparationDuration != time.Second || iteration.ToolDispatchDuration != 2*time.Second {
		t.Fatalf("iteration=%+v", iteration)
	}
}
