//go:build linux

package claudecode_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/claudecode"
)

func TestProviderStopsDescendantsHoldingPipes(t *testing.T) {
	for _, mode := range []string{"cancel", "early_stop", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			stopEarly := mode == "early_stop"
			timeout := 30 * time.Second
			if mode == "timeout" {
				timeout = 250 * time.Millisecond
			}
			dir := t.TempDir()
			pidPath := filepath.Join(dir, "child.pid")
			script := filepath.Join(dir, "claude")
			body := "#!/bin/sh\nsleep 60 &\nchild=$!\nprintf '%s' \"$child\" > '" + pidPath + "'\nprintf '%s\\n' '" + textEvent("ready") + "'\nwait \"$child\"\n"
			if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
				t.Fatal(err)
			}
			provider := claudecode.NewProvider(claudecode.StaticTokenSource{T: claudecode.Token{Access: "test-token"}}, claudecode.WithBinaryPath(script), claudecode.WithTimeout(timeout))
			ctx, cancel := context.WithCancelCause(t.Context())
			cause := errors.New("cancel descendant fixture")
			defer cancel(cause)
			type result struct {
				ready bool
				err   error
			}
			done := make(chan result, 1)
			go func() {
				var got result
				provider.Complete(ctx, llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "wait"}}})(func(chunk llm.CompletionChunk, err error) bool {
					if err != nil {
						got.err = err
						return true
					}
					if chunk.Content == "ready" {
						got.ready = true
						if stopEarly {
							return false
						}
						if mode == "cancel" {
							cancel(cause)
						}
					}
					return true
				})
				done <- got
			}()
			// Independent fixture cleanup also releases a broken implementation's
			// inherited pipes, so the test always joins its provider goroutine.
			killChild := func() {
				data, err := os.ReadFile(pidPath)
				if err == nil {
					pid, parseErr := strconv.Atoi(string(data))
					if parseErr == nil {
						_ = syscall.Kill(pid, syscall.SIGKILL)
					}
				}
			}
			defer killChild()
			var got result
			select {
			case got = <-done:
			case <-time.After(3 * time.Second):
				cancel(cause)
				killChild()
				<-done
				t.Fatal("provider did not return after cancellation/early stop with descendant-held pipes")
			}
			if !got.ready {
				t.Fatalf("provider did not reach ready event: %v", got.err)
			}
			if stopEarly && got.err != nil {
				t.Fatalf("early stop yielded an error: %v", got.err)
			}
			wantErr := cause
			if mode == "timeout" {
				wantErr = context.DeadlineExceeded
			}
			if !stopEarly && !errors.Is(got.err, wantErr) {
				t.Fatalf("terminal error = %v, want cancellation cause", got.err)
			}
			data, err := os.ReadFile(pidPath)
			if err != nil {
				t.Fatal(err)
			}
			stat, err := os.ReadFile("/proc/" + string(data) + "/stat")
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			// An orphan may remain a zombie until the host's init reaps it.
			_, state, found := strings.Cut(string(stat), ") ")
			if !found || (!strings.HasPrefix(state, "Z ") && !strings.HasPrefix(state, "X ")) {
				t.Fatalf("descendant still running after provider returned: %s", stat)
			}
		})
	}
}
