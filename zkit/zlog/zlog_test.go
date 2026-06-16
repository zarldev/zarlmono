package zlog_test

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/zlog"
)

func TestSetupConfigRedirectsStdlibWhenEnabled(t *testing.T) {
	oldWriter := log.Writer()
	t.Cleanup(func() { log.SetOutput(oldWriter) })

	dir := t.TempDir()
	file, err := zlog.Setup(
		zlog.WithLogDir(dir),
		zlog.WithFilePrefix("test"),
		zlog.WithStdout(false),
		zlog.WithStdlib(true),
	)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	log.Print("stdlib marker")
	if err := file.Close(); err != nil {
		t.Fatalf("close log file: %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "test_*.log"))
	if err != nil {
		t.Fatalf("glob log file: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("log files = %v, want one", matches)
	}
	body, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if !strings.Contains(string(body), "stdlib marker") {
		t.Fatalf("log file body = %q, want stdlib marker", body)
	}
}

func TestDefaultConfigDoesNotRedirectStdlib(t *testing.T) {
	t.Parallel()

	cfg := zlog.DefaultConfig()
	if cfg.Stdlib {
		t.Fatal("DefaultConfig().Stdlib = true, want false")
	}
}
