package sleepinhibit_test

import (
	"runtime"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/sleepinhibit"
)

func TestAcquireClose(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("sleep inhibition is not supported on this OS")
	}
	inhibitor, err := sleepinhibit.Acquire(t.Context())
	if err != nil {
		t.Skipf("OS sleep inhibitor is unavailable: %v", err)
	}
	if err := inhibitor.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
	if err := inhibitor.Close(); err != nil {
		t.Fatalf("second Close(): %v", err)
	}
}
