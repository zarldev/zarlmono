package tui_test

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/db"
)

// Keep the real persistence guard enabled. The returned debounce commands are
// not executed: this measures foreground scheduling, not SQLite I/O or timers.
func BenchmarkPersistedToolPreviewUpdate(b *testing.B) {
	for _, history := range []int{100, 10000} {
		b.Run(strconv.Itoa(history), func(b *testing.B) {
			root := b.TempDir()
			store, err := db.Open(b.Context(), filepath.Join(root, "state.db"))
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = store.Close() })
			ui := tui.New()
			messages := make([]llm.Message, history)
			for i := range messages {
				messages[i] = llm.Message{Role: llm.RoleUser, Content: "synthetic history"}
			}
			ui.AddTranscriptMessages(messages)
			ui.Update(teasink.ToolStartedMsg{TaskID: "turn", ExecutionID: "call", ToolID: "call", ToolName: "bash"})
			ui.SetSettings(engine.NewSettings(store, nil, nil, root))
			ui.SetSessionIdentity("benchmark", "benchmark", false, time.Now())
			outputs := [2]string{"first preview", "second preview"}
			n := 0
			b.ReportAllocs()
			for b.Loop() {
				n++
				_, cmd := ui.Update(teasink.ToolOutputMsg{TaskID: "turn", ExecutionID: "call", ToolID: "call", Text: outputs[n%2]})
				if cmd == nil {
					b.Fatal("persistence scheduling was not exercised")
				}
			}
		})
	}
}
