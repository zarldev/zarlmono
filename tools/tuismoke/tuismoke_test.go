package tuismoke_test

import (
	"io"
	"testing"

	"github.com/zarldev/zarlmono/tools/tuismoke"
)

func TestRunRejectsUnboundedTimeout(t *testing.T) {
	t.Parallel()
	if err := tuismoke.Run(t.Context(), ".", "", 0, io.Discard); err == nil {
		t.Fatal("unbounded timeout accepted")
	}
}
