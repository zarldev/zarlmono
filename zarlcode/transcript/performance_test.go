package transcript_test

import (
	"strconv"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/transcript"
)

func BenchmarkReducerPreviewHistory(b *testing.B) {
	for _, history := range []int{100, 10000, 100000} {
		b.Run(strconv.Itoa(history), func(b *testing.B) {
			builder := transcript.NewBuilder()
			for range history {
				builder.AddUser("synthetic history")
			}
			r := transcript.NewReducerFrom(builder.Thread())
			if _, err := r.Apply(transcript.ToolStarted{TurnID: "turn", ExecutionID: "call", ToolID: "call", Name: "bash"}); err != nil {
				b.Fatal(err)
			}
			text := [2]string{"first preview", "second preview"}
			n := 0
			b.ReportAllocs()
			for b.Loop() {
				n++
				if _, err := r.Apply(transcript.ToolOutputUpdated{ExecutionID: "call", ToolID: "call", Output: text[n%2]}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
