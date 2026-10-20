//go:build linux

package mcp_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/mcp"
)

func TestStdioCloseStopsDescendants(t *testing.T) {
	for _, mode := range []string{"graceful", "forced", "initialize-cancel"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			pidFile, eofFile := filepath.Join(dir, "pids"), filepath.Join(dir, "eof")
			testCtx := t.Context()
			ctx, cancel := context.WithCancelCause(testCtx)
			cause := errors.New("cancel stdio peer initialization")
			defer cancel(cause)
			type outcome struct{ initErr, callErr, closeErr, repeatErr error }
			var got outcome
			done := make(chan struct{})
			go func() {
				defer close(done)
				client, err := mcp.NewStdioClientContext(ctx, os.Args[0], []string{"-test.run=^TestStdioDescendantPeer$"}, map[string]string{
					"ZARL_MCP_DESCENDANT_PEER": mode,
					"ZARL_MCP_DESCENDANT_PIDS": pidFile,
					"ZARL_MCP_DESCENDANT_EOF":  eofFile,
				})
				got.initErr = err
				if err != nil {
					return
				}
				cancel(cause) // successful clients must outlive their initialization context
				_, got.callErr = client.Discover(testCtx)
				got.closeErr = client.Close()
				got.repeatErr = client.Close()
			}()
			// Independently clean both known fixture PIDs even when the production
			// implementation fails to terminate descendants; always join our caller.
			defer func() {
				cancel(cause)
				data, _ := os.ReadFile(pidFile)
				for _, value := range strings.Fields(string(data)) {
					if pid, err := strconv.Atoi(value); err == nil {
						_ = syscall.Kill(pid, syscall.SIGKILL)
					}
				}
				<-done
			}()
			var pids []string
			ready := time.NewTicker(10 * time.Millisecond)
			defer ready.Stop()
			deadline := time.NewTimer(3 * time.Second)
			defer deadline.Stop()
		waitReady:
			for {
				data, _ := os.ReadFile(pidFile)
				pids = strings.Fields(string(data))
				if len(pids) == 2 {
					break
				}
				select {
				case <-ready.C:
				case <-done:
					// A fast peer can publish readiness and finish between the
					// preceding file read and this completion observation.
					data, _ := os.ReadFile(pidFile)
					pids = strings.Fields(string(data))
					if len(pids) == 2 {
						break waitReady
					}
					t.Fatalf("peer exited before readiness: %+v", got)
				case <-deadline.C:
					t.Error("peer did not become ready")
					break waitReady
				}
			}
			if len(pids) != 2 {
				return
			}
			if mode == "initialize-cancel" {
				cancel(cause)
			}
			select {
			case <-done:
			case <-time.After(4 * time.Second):
				t.Fatal("stdio cleanup did not return")
			}
			if mode == "initialize-cancel" {
				if !errors.Is(got.initErr, cause) {
					t.Fatalf("initialize = %v, want cancellation cause", got.initErr)
				}
			} else if got.initErr != nil || got.callErr != nil || got.closeErr != nil || got.repeatErr != nil {
				t.Fatalf("client lifecycle: %+v", got)
			}
			if _, err := os.Stat(eofFile); err != nil {
				t.Fatalf("peer did not receive graceful stdin EOF: %v", err)
			}
			assertStdioDescendantStopped(t, pids[1])
		})
	}
}

func assertStdioDescendantStopped(t *testing.T, pid string) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		stat, err := os.ReadFile("/proc/" + pid + "/stat")
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		_, state, _ := strings.Cut(string(stat), ") ")
		if strings.HasPrefix(state, "Z ") || strings.HasPrefix(state, "X ") {
			return // orphan reaping belongs to the host's init
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatalf("descendant still running after Close: %s", stat)
		}
	}
}

// TestStdioDescendantPeer runs only as an explicitly selected subprocess peer.
func TestStdioDescendantPeer(t *testing.T) {
	mode := os.Getenv("ZARL_MCP_DESCENDANT_PEER")
	if mode == "" {
		return
	}
	child := exec.Command("sleep", "60")
	child.Stdout, child.Stderr = os.Stdout, os.Stderr
	if err := child.Start(); err != nil {
		os.Exit(2)
	}
	data := fmt.Sprintf("%d %d", os.Getpid(), child.Process.Pid)
	if err := os.WriteFile(os.Getenv("ZARL_MCP_DESCENDANT_PIDS"), []byte(data), 0o600); err != nil {
		_ = child.Process.Kill()
		_ = child.Wait()
		os.Exit(3)
	}
	// The test owner takes responsibility for the descendant when this peer
	// exits. Intentionally retain stdout/stderr to reproduce inherited pipes.
	scan, enc := bufio.NewScanner(os.Stdin), json.NewEncoder(os.Stdout)
	for scan.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(scan.Bytes(), &request) != nil || len(request.ID) == 0 || mode == "initialize-cancel" {
			continue
		}
		response := map[string]any{"jsonrpc": "2.0", "id": request.ID}
		switch request.Method {
		case "server/discover":
			response["error"] = map[string]any{"code": -32601, "message": "method not found"}
		case "initialize":
			response["result"] = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "peer", "version": "1"}}
		default:
			response["result"] = map[string]any{"tools": []any{}}
		}
		_ = enc.Encode(response)
	}
	_ = os.WriteFile(os.Getenv("ZARL_MCP_DESCENDANT_EOF"), []byte("eof"), 0o600)
	if mode == "forced" {
		_ = child.Wait()
	}
	os.Exit(0)
}
