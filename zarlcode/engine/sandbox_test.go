package engine_test

import (
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/agent/sandbox"
)

func TestRequestedSandboxFailsClosed(t *testing.T) {
	unavailable := errors.New("platform unavailable")
	for _, failAt := range []int{1, 2, 0} {
		calls := 0
		sb, err := engine.ShellSandbox(true, t.TempDir(), sandbox.Policy{}, func(sandbox.Policy) (*sandbox.Sandbox, error) {
			calls++
			if calls == failAt {
				return nil, unavailable
			}
			return &sandbox.Sandbox{}, nil
		})
		if failAt != 0 && (!errors.Is(err, unavailable) || sb != nil) {
			t.Fatalf("failAt=%d sandbox=%v err=%v", failAt, sb, err)
		}
		if failAt == 0 && (err != nil || sb == nil) {
			t.Fatalf("sandbox=%v err=%v", sb, err)
		}
	}
	sb, err := engine.ShellSandbox(false, t.TempDir(), sandbox.Policy{}, func(sandbox.Policy) (*sandbox.Sandbox, error) {
		t.Fatal("explicit disable invoked platform")
		return nil, unavailable
	})
	if err != nil || sb != nil {
		t.Fatalf("disabled sandbox=%v err=%v", sb, err)
	}
}
