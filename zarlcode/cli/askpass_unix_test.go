//go:build unix

package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/askpass"
	"github.com/zarldev/zarlmono/zarlcode/cli"
)

func TestAskpassCommandProtocol(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		var got askpass.Request
		sock := serveAskpass(t, func(conn net.Conn) error {
			if err := json.NewDecoder(conn).Decode(&got); err != nil {
				return err
			}
			return json.NewEncoder(conn).Encode(askpass.Response{Password: "secret value"})
		})
		code, stdout, stderr := executeAskpass(t, cli.AskpassCommand{Sock: sock}, " sudo ", " password: ")
		if code != 0 || stdout != "secret value\n" || stderr != "" {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
		if got.Prompt != "sudo   password:" {
			t.Fatalf("prompt = %q", got.Prompt)
		}
	})

	t.Run("malformed reply", func(t *testing.T) {
		sock := serveAskpass(t, func(conn net.Conn) error {
			var req askpass.Request
			if err := json.NewDecoder(conn).Decode(&req); err != nil {
				return err
			}
			_, err := conn.Write([]byte("not-json\n"))
			return err
		})
		code, stdout, stderr := executeAskpass(t, cli.AskpassCommand{Sock: sock})
		if code != 2 || stdout != "" || !strings.Contains(stderr, "zarlcode-askpass: recv:") {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
	})

	t.Run("server cancellation", func(t *testing.T) {
		sock := serveAskpass(t, func(conn net.Conn) error {
			var req askpass.Request
			if err := json.NewDecoder(conn).Decode(&req); err != nil {
				return err
			}
			return json.NewEncoder(conn).Encode(askpass.Response{Error: "cancelled"})
		})
		code, stdout, stderr := executeAskpass(t, cli.AskpassCommand{Sock: sock})
		if code != 2 || stdout != "" || stderr != "zarlcode-askpass: cancelled\n" {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
	})
}

func TestAskpassCommandCancellationAfterConnect(t *testing.T) {
	requestRead := make(chan struct{})
	sock := serveAskpass(t, func(conn net.Conn) error {
		var req askpass.Request
		if err := json.NewDecoder(conn).Decode(&req); err != nil {
			return err
		}
		close(requestRead)
		var one [1]byte
		_, err := conn.Read(one[:])
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	})

	ctx, cancel := context.WithCancel(t.Context())
	cancelled := make(chan struct{})
	go func() {
		defer close(cancelled)
		select {
		case <-requestRead:
			cancel()
		case <-ctx.Done():
		}
	}()

	var stdout, stderr bytes.Buffer
	code := (cli.AskpassCommand{Sock: sock}).Execute(ctx, nil, &stdout, &stderr)
	cancel()
	<-cancelled
	if code != 2 || stdout.String() != "" || stderr.String() != "zarlcode-askpass: recv: context canceled\n" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestAskpassCommandRequiresSocket(t *testing.T) {
	t.Setenv(askpass.EnvSock, "")
	code, stdout, stderr := executeAskpass(t, cli.AskpassCommand{})
	if code != 2 || stdout != "" || !strings.Contains(stderr, "ZARLCODE_ASKPASS_SOCK is unset") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func executeAskpass(t *testing.T, cmd cli.AskpassCommand, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := cmd.Execute(t.Context(), args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func serveAskpass(t *testing.T, handle func(net.Conn) error) string {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "askpass.sock")
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			err = handle(conn)
			_ = conn.Close()
		}
		done <- err
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		if err := <-done; err != nil {
			t.Errorf("askpass server: %v", err)
		}
	})
	return sock
}
