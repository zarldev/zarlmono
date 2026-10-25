package tui_test

import (
	"bytes"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestLaunchLoggingPreservesOtherInstanceWrites(t *testing.T) {
	// Launch logging also redirects the standard logger through slog. Isolate
	// process-global logging state rather than importing the legacy logger to
	// restore it after exercising two launch instances.
	if os.Getenv("ZARLCODE_TEST_LAUNCH_LOGGING") != "1" {
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), executable, "-test.run=^TestLaunchLoggingPreservesOtherInstanceWrites$")
		cmd.Env = append(os.Environ(), "ZARLCODE_TEST_LAUNCH_LOGGING=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated launch logging: %v\n%s", err, output)
		}
		return
	}
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("HOME", cache)
	t.Setenv("LocalAppData", cache)

	path, first := tui.SetupLaunchLogging()
	t.Cleanup(func() { _ = first.Close() })
	if path == "" {
		t.Fatal("first launch did not open a log")
	}
	firstLogger := slog.Default()
	firstLogger.Error("first instance checkpoint failure")
	secondPath, second := tui.SetupLaunchLogging()
	t.Cleanup(func() { _ = second.Close() })
	if secondPath != path {
		t.Fatal("launches did not use the same log")
	}
	slog.Info("second instance started")
	firstLogger.Error("first instance later failure")
	slog.Info("second instance later message")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 4 || bytes.ContainsRune(data, '\x00') {
		t.Fatalf("log lost or overwrote entries: %q", data)
	}
	for i, want := range []string{"first instance checkpoint failure", "second instance started", "first instance later failure", "second instance later message"} {
		if !strings.Contains(lines[i], want) || !strings.Contains(lines[i], "pid="+strconv.Itoa(os.Getpid())) {
			t.Errorf("line %d = %q, want %q and process identity", i+1, lines[i], want)
		}
	}
}
