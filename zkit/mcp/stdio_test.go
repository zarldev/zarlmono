package mcp_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/mcp"
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// buildFakeStdioServer compiles the tiny test server under
// ./testdata/stdiosrv and returns the resulting binary path. The binary
// speaks enough of the MCP protocol (initialize, tools/list, tools/call)
// to round-trip through the real stdioTransport.
func buildFakeStdioServer(t *testing.T) string {
	t.Helper()
	src := filepath.Join("testdata", "stdiosrv", "main.go")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("test fixture missing: %v", err)
	}
	bin := filepath.Join(t.TempDir(), "stdiosrv")
	out, err := exec.Command("go", "build", "-o", bin, "./testdata/stdiosrv").CombinedOutput()
	if err != nil {
		t.Fatalf("build fake server: %v\n%s", err, out)
	}
	return bin
}

func TestStdioTransportDiscoverAndCall(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("no go toolchain available: %v", err)
	}
	bin := buildFakeStdioServer(t)

	c, err := mcp.NewStdioClient(bin, nil, nil)
	if err != nil {
		t.Fatalf("NewStdioClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	defs, err := c.Discover(t.Context())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(defs) != 1 || defs[0].Name != "echo" {
		t.Fatalf("discovered = %+v, want single echo tool", defs)
	}

	got, err := c.Call(t.Context(), defs[0].Name, map[string]any{"message": "hello stdio"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	text := got.FirstText()
	if !strings.Contains(text, "hello stdio") {
		t.Errorf("first text = %q, want it to contain %q", text, "hello stdio")
	}
}

func TestStdioTransportDoesNotInheritParentEnvironment(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("no go toolchain available: %v", err)
	}
	const key = "ZARL_MCP_STDIO_SECRET_SHOULD_NOT_LEAK"
	t.Setenv(key, "super-secret")
	bin := buildFakeStdioServer(t)

	c, err := mcp.NewStdioClient(bin, nil, nil)
	if err != nil {
		t.Fatalf("NewStdioClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	got, err := c.Call(t.Context(), "echo", map[string]any{"message": "env:" + key})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if text := got.FirstText(); text != "echo: " {
		t.Fatalf("child saw parent secret env; first text = %q", text)
	}
}

func TestStdioTransportPassesExplicitEnvironment(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("no go toolchain available: %v", err)
	}
	const key = "ZARL_MCP_STDIO_EXPLICIT_ENV"
	bin := buildFakeStdioServer(t)

	c, err := mcp.NewStdioClient(bin, nil, map[string]string{key: "allowed"})
	if err != nil {
		t.Fatalf("NewStdioClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	got, err := c.Call(t.Context(), "echo", map[string]any{"message": "env:" + key})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if text := got.FirstText(); text != "echo: allowed" {
		t.Fatalf("first text = %q, want explicit env value", text)
	}
}

func TestStdioCallCancellationUnblocksBlockedWrite(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("no go toolchain available: %v", err)
	}
	bin := buildFakeStdioServer(t)
	pidFile := filepath.Join(t.TempDir(), "stdio.pid")
	goroutines := runtime.NumGoroutine()
	client, err := mcp.NewStdioClient(bin, nil, map[string]string{
		"ZARL_MCP_STDIO_MODE":     "stop-reading-after-init",
		"ZARL_MCP_STDIO_PID_FILE": pidFile,
	})
	if err != nil {
		t.Fatalf("NewStdioClient: %v", err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = client.Close()
		}
	})

	pidBytes, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read fixture pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil {
		t.Fatalf("parse fixture pid %q: %v", pidBytes, err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	callDone := make(chan error, 1)
	go func() {
		_, callErr := client.Call(ctx, "echo", map[string]any{
			"message": strings.Repeat("x", 16*1024*1024),
		})
		callDone <- callErr
	}()

	select {
	case err := <-callDone:
		if err == nil {
			t.Fatal("Call unexpectedly succeeded after blocked transport shutdown")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("Call remained blocked writing to the stopped stdio server")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- client.Close() }()
	select {
	case err := <-closeDone:
		closed = true
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close remained blocked after write cancellation")
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		processErr := syscall.Kill(pid, 0)
		processExited := errors.Is(processErr, syscall.ESRCH)
		runtime.GC()
		goroutinesExited := runtime.NumGoroutine() <= goroutines
		if processExited && goroutinesExited {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stdio cleanup leaked resources: process error=%v, goroutines=%d (baseline %d)", processErr, runtime.NumGoroutine(), goroutines)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStdioStderrRedactsSecrets(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("no go toolchain available: %v", err)
	}
	bin := buildFakeStdioServer(t)
	var logs lockedBuffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	client, err := mcp.NewStdioClient(bin, nil, nil)
	if err != nil {
		t.Fatalf("NewStdioClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	secret := "API_KEY=sk-1234567890"
	if _, err := client.Call(t.Context(), "echo", map[string]any{"message": "stderr:" + secret}); err != nil {
		t.Fatalf("Call: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(logs.String(), "API_KEY=REDACTED") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := logs.String(); strings.Contains(got, "sk-1234567890") || !strings.Contains(got, "API_KEY=REDACTED") {
		t.Fatalf("stderr log = %q, want redacted secret", got)
	}
}

func TestStdioStderrRedactsCredentialForms(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("no go toolchain available: %v", err)
	}
	cases := []struct {
		name       string
		input      string
		secret     string
		want       string
		wantSecret bool
	}{
		{name: "api key colon", input: "api_key: abc123secret", secret: "abc123secret", want: "api_key: REDACTED"},
		{name: "AWS assignment", input: "aws_access_key=AKIAABCDEFGHIJKLMNOP", secret: "AKIAABCDEFGHIJKLMNOP", want: "aws_access_key=REDACTED"},
		{name: "secret token", input: "secret_token=topsecret", secret: "topsecret", want: "secret_token=REDACTED"},
		{name: "password", input: "password=hunter2", secret: "hunter2", want: "password=REDACTED"},
		{name: "passwd", input: "passwd: swordfish", secret: "swordfish", want: "passwd: REDACTED"},
		{name: "bearer", input: "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.payload.signature", secret: "eyJhbGciOiJIUzI1NiJ9.payload.signature", want: "Authorization: REDACTED"},
		{name: "ordinary text", input: "the password policy is documented", secret: "password policy", want: "password policy", wantSecret: true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			bin := buildFakeStdioServer(t)
			var logs lockedBuffer
			oldLogger := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
			t.Cleanup(func() { slog.SetDefault(oldLogger) })
			client, err := mcp.NewStdioClient(bin, nil, nil)
			if err != nil {
				t.Fatalf("NewStdioClient: %v", err)
			}
			t.Cleanup(func() { _ = client.Close() })
			if _, err := client.Call(t.Context(), "echo", map[string]any{"message": "stderr:" + tt.input}); err != nil {
				t.Fatalf("Call: %v", err)
			}
			deadline := time.Now().Add(3 * time.Second)
			for !strings.Contains(logs.String(), tt.want) && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			got := logs.String()
			if !strings.Contains(got, tt.want) {
				t.Fatalf("stderr log = %q, want %q", got, tt.want)
			}
			if !tt.wantSecret && strings.Contains(got, tt.secret) {
				t.Fatalf("stderr log leaked %q: %q", tt.secret, got)
			}
		})
	}
}

func TestStdioStderrCapsLongLines(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("no go toolchain available: %v", err)
	}
	bin := buildFakeStdioServer(t)
	logPath := filepath.Join(t.TempDir(), "stderr.log")
	file, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create log: %v", err)
	}
	t.Cleanup(func() { _ = file.Close() })
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(file, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	client, err := mcp.NewStdioClient(bin, nil, nil)
	if err != nil {
		t.Fatalf("NewStdioClient: %v", err)
	}
	long := strings.Repeat("x", 5000)
	if _, err := client.Call(t.Context(), "echo", map[string]any{"message": "stderr:" + long}); err != nil {
		t.Fatalf("Call: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := file.Sync(); err != nil {
			t.Fatalf("sync log: %v", err)
		}
		body, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatalf("read log: %v", err)
		}
		if strings.Contains(string(body), "[truncated]") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stderr log does not contain truncation marker: %q", body)
		}
		time.Sleep(time.Millisecond)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
