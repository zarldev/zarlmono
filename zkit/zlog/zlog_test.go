package zlog_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"log/slog"
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

func TestDefaultFormats(t *testing.T) {
	t.Parallel()
	config := zlog.DefaultConfig()
	if config.FileFormat != zlog.FormatJSON() {
		t.Fatalf("FileFormat = %v, want JSON", config.FileFormat)
	}
	if config.StdoutFormat != zlog.FormatConsole() {
		t.Fatalf("StdoutFormat = %v, want console", config.StdoutFormat)
	}
}

func TestSetupFileFormats(t *testing.T) {
	formats := map[string]zlog.OutputFormat{
		"json":    zlog.FormatJSON(),
		"text":    zlog.FormatText(),
		"console": zlog.FormatConsole(),
	}
	for name, format := range formats {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			file, err := zlog.Setup(
				zlog.WithLogDir(dir),
				zlog.WithFilePrefix(name),
				zlog.WithFileFormat(format),
				zlog.WithStdout(false),
				zlog.WithAddSource(false),
			)
			if err != nil {
				t.Fatalf("Setup: %v", err)
			}
			slog.Info("format marker", "answer", 42)
			if err := file.Close(); err != nil {
				t.Fatalf("close: %v", err)
			}
			matches, err := filepath.Glob(filepath.Join(dir, name+"_*.log"))
			if err != nil || len(matches) != 1 {
				t.Fatalf("log files = %v, err = %v", matches, err)
			}
			body, err := os.ReadFile(matches[0])
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if bytes.Contains(body, []byte{0x1b}) {
				t.Fatalf("file contains ANSI escape: %q", body)
			}
			if !bytes.Contains(body, []byte("format marker")) || !bytes.Contains(body, []byte("answer")) {
				t.Fatalf("file omitted record data: %q", body)
			}
			if name == "json" {
				var record map[string]any
				if err := json.Unmarshal(bytes.TrimSpace(body), &record); err != nil {
					t.Fatalf("JSON output: %v: %q", err, body)
				}
			}
		})
	}
}

func TestWithJSONOutputCompatibility(t *testing.T) {
	config := zlog.DefaultConfig()
	zlog.WithJSONOutput(false)(&config)
	if config.FileFormat != zlog.FormatText() || config.StdoutFormat != zlog.FormatConsole() {
		t.Fatalf("false formats = %v, %v", config.FileFormat, config.StdoutFormat)
	}
	zlog.WithJSONOutput(true)(&config)
	if config.FileFormat != zlog.FormatJSON() || config.StdoutFormat != zlog.FormatJSON() {
		t.Fatalf("true formats = %v, %v", config.FileFormat, config.StdoutFormat)
	}
}

func TestSetupRejectsInvalidFormatBeforeOpeningFile(t *testing.T) {
	dir := t.TempDir()
	config := zlog.DefaultConfig()
	config.LogDir = dir
	config.FileFormat = zlog.OutputFormat(255)
	_, err := zlog.SetupConfig(config)
	if !errors.Is(err, zlog.ErrInvalidOutputFormat) {
		t.Fatalf("error = %v, want ErrInvalidOutputFormat", err)
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("ReadDir: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("created files for invalid config: %v", entries)
	}
}
