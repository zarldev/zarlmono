package runner_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

func TestWithSinkRejectsNil(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Fatal("WithSink(nil) did not panic")
		}
	}()
	_ = runner.WithSink(nil)
}
