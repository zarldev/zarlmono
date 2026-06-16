// Package zlog provides structured-logging setup using slog with optional
// file output and console formatting via tint.
//
// Configuration follows the canonical functional-options pattern from
// zkit/options:
//
//	logFile, err := zlog.Setup(
//	    zlog.WithLevel(slog.LevelInfo),
//	    zlog.WithLogDir("/var/log/myapp"),
//	    zlog.WithJSONOutput(true),
//	)
package zlog

import (
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/lmittmann/tint"

	"github.com/zarldev/zarlmono/zkit/filesystem"
	"github.com/zarldev/zarlmono/zkit/options"
)

// Config holds zlog setup parameters. Exported so callers that prefer
// to build a config directly (loaded from a file, derived from another
// config system) can still construct one and pass to SetupConfig.
type Config struct {
	Level      slog.Level
	AddSource  bool
	TimeFormat string
	LogDir     string
	FilePrefix string
	JSONOutput bool
	FS         filesystem.ReadWriteFileFS
	// Stdout selects whether log output is also tee'd to os.Stdout in
	// addition to the rotating file. Default true (matches the prior
	// behaviour). TUI consumers should disable this — stdout is the
	// alt-screen and any log line written there corrupts the layout.
	Stdout bool
	// Stdlib selects whether the standard library log package is redirected
	// to the same sink as slog. Default false to preserve existing callers;
	// TUI consumers should enable it after disabling Stdout so legacy log
	// bridges do not write over the alt-screen.
	Stdlib bool
}

// DefaultConfig returns the baseline log setup: debug level, source
// annotation on, console-formatted (tint), files written to ./logs/
// rotating by timestamp, tee'd to stdout.
func DefaultConfig() Config {
	return Config{
		Level:      slog.LevelDebug,
		AddSource:  true,
		TimeFormat: "2006-01-02 15:04:05.000",
		LogDir:     "logs",
		FilePrefix: "app",
		JSONOutput: false,
		FS:         nil,

		Stdout: true,
		Stdlib: false,
	}
}

// WithLevel sets the slog level filter.
func WithLevel(l slog.Level) options.Option[Config] {
	return func(c *Config) { c.Level = l }
}

// WithAddSource toggles slog's source-location annotation. Default true.
func WithAddSource(b bool) options.Option[Config] {
	return func(c *Config) { c.AddSource = b }
}

// WithTimeFormat sets the time-format string used by the console
// (tint) handler. No effect when JSONOutput is true.
func WithTimeFormat(f string) options.Option[Config] {
	return func(c *Config) { c.TimeFormat = f }
}

// WithLogDir sets the directory where log files are written.
func WithLogDir(dir string) options.Option[Config] {
	return func(c *Config) { c.LogDir = dir }
}

// WithFilePrefix sets the file-name prefix; the timestamp is appended.
func WithFilePrefix(prefix string) options.Option[Config] {
	return func(c *Config) { c.FilePrefix = prefix }
}

// WithJSONOutput selects between JSON output (slog.JSONHandler) and
// console output (tint). Default console.
func WithJSONOutput(json bool) options.Option[Config] {
	return func(c *Config) { c.JSONOutput = json }
}

// WithFS injects a custom filesystem — useful for tests (memfs) and
// for callers writing logs to non-OS backends (e.g. seaweedfs).
func WithFS(fs filesystem.ReadWriteFileFS) options.Option[Config] {
	return func(c *Config) { c.FS = fs }
}

// WithStdout enables / disables the stdout tee. Default on. TUI
// consumers should pass WithStdout(false): when bubbletea owns
// stdout for the alt-screen, log lines written there corrupt the
// rendered layout.
func WithStdout(b bool) options.Option[Config] {
	return func(c *Config) { c.Stdout = b }
}

// WithStdlib enables / disables redirecting the standard library log
// package to zlog's sink. Default off for backward compatibility.
func WithStdlib(b bool) options.Option[Config] {
	return func(c *Config) { c.Stdlib = b }
}

// SetStdlibOutput redirects the standard library log package. Prefer
// [WithStdlib] when using Setup; use this helper when a caller owns a
// custom writer but still wants stdlib-log policy centralized in zlog.
func SetStdlibOutput(w io.Writer) {
	if w == nil {
		return
	}
	log.SetOutput(w)
}

// Setup configures slog as the package default and returns the open
// log file. Apply Options to override DefaultConfig fields.
func Setup(opts ...options.Option[Config]) (filesystem.File, error) {
	cfg := DefaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	return SetupConfig(cfg)
}

// SetupConfig is the explicit-config variant of Setup for callers that
// build a Config directly. Setup wraps this.
func SetupConfig(cfg Config) (filesystem.File, error) {
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	logPath := filepath.Join(cfg.LogDir, fmt.Sprintf("%s_%s.log", cfg.FilePrefix, timestamp))

	var logFile filesystem.File
	if cfg.FS == nil {
		if err := os.MkdirAll(cfg.LogDir, filesystem.ModePublicDir); err != nil {
			return nil, fmt.Errorf("create log directory: %w", err)
		}
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, filesystem.ModePublicFile)
		if err != nil {
			return nil, fmt.Errorf("open log file: %w", err)
		}
		logFile = f
	} else {
		if err := cfg.FS.MkdirAll(cfg.LogDir, filesystem.ModePublicDir); err != nil {
			return nil, fmt.Errorf("create log directory: %w", err)
		}
		f, err := cfg.FS.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, filesystem.ModePublicFile)
		if err != nil {
			return nil, fmt.Errorf("open log file: %w", err)
		}
		logFile = f
	}

	var sink io.Writer = logFile
	if cfg.Stdout {
		sink = io.MultiWriter(os.Stdout, logFile)
	}

	var handler slog.Handler
	if cfg.JSONOutput {
		handler = slog.NewJSONHandler(sink, &slog.HandlerOptions{
			AddSource: cfg.AddSource,
			Level:     cfg.Level,
		})
	} else {
		handler = tint.NewHandler(sink, &tint.Options{
			AddSource:  cfg.AddSource,
			Level:      cfg.Level,
			TimeFormat: cfg.TimeFormat,
		})
	}
	slog.SetDefault(slog.New(handler))
	if cfg.Stdlib {
		SetStdlibOutput(sink)
	}
	return logFile, nil
}
