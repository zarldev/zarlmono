//go:build linux

package dynamic_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/dynamic"
)

func TestBinaryStopsDescendantsHoldingPipes(t *testing.T) {
	for _, mode := range []struct{ describe, earlyExit bool }{{}, {describe: true}, {earlyExit: true}, {describe: true, earlyExit: true}} {
		describe := mode.describe
		name := "execute"
		if describe {
			name = "describe"
		}
		if mode.earlyExit {
			name += "_leader_exit"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			pidPath := filepath.Join(dir, "child.pid")
			path := filepath.Join(dir, "tool")
			ending := "wait \"$child\"\n"
			timeout := 250 * time.Millisecond
			wantErr := context.DeadlineExceeded
			if mode.earlyExit {
				ending = "exit 0\n"
				timeout = 30 * time.Second
				wantErr = exec.ErrWaitDelay
			}
			body := "#!/bin/sh\nsleep 60 &\nchild=$!\nprintf '%s' \"$child\" > '" + pidPath + "'\n" + ending
			if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
				t.Fatal(err)
			}
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
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				if describe {
					_, err := dynamic.DescribeBinary(ctx, path, timeout)
					done <- err
					return
				}
				tool := dynamic.NewBinaryTool(tools.ToolSpec{Name: "fixture"}, path, dynamic.WithBinaryTimeout(timeout))
				result, err := tool.Execute(ctx, tools.ToolCall{ID: "fixture"})
				if err == nil {
					if result.Success {
						err = errors.New("unexpected successful execution")
					} else if !strings.Contains(result.Error, wantErr.Error()) {
						err = errors.New("missing deadline cause: " + result.Error)
					}
				}
				done <- err
			}()
			select {
			case err := <-done:
				if describe {
					if !errors.Is(err, wantErr) {
						t.Fatalf("describe error = %v, want deadline", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				cancel()
				killChild()
				<-done
				t.Fatal("binary call remained blocked on descendant-held pipes")
			}
			data, err := os.ReadFile(pidPath)
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.NewTimer(time.Second)
			defer deadline.Stop()
			tick := time.NewTicker(time.Millisecond)
			defer tick.Stop()
			for {
				stat, err := os.ReadFile("/proc/" + string(data) + "/stat")
				if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				_, state, _ := strings.Cut(string(stat), ") ")
				if strings.HasPrefix(state, "Z ") || strings.HasPrefix(state, "X ") {
					break
				}
				select {
				case <-tick.C:
				case <-deadline.C:
					t.Fatalf("descendant remains alive after invocation returned: %s", stat)
				}
			}
		})
	}
}
