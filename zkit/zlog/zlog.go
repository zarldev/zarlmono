// Package zlog configures structured slog output independently for log files
// and stdout. Files default to JSON for ingestion and never receive ANSI colour
// by default. Stdout defaults to tint's human-oriented console format, with
// colour enabled only when stdout is a terminal. Use [FormatText] for plain,
// newline-delimited key=value output.
package zlog

import (
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/term"

	"github.com/zarldev/zarlmono/zkit/filesystem"
	"github.com/zarldev/zarlmono/zkit/options"
)

// Config holds zlog setup parameters. FileFormat and StdoutFormat configure
// each destination independently. A zero format is resolved for compatibility
// using the deprecated JSONOutput field.
type Config struct {
	Level        slog.Level
	AddSource    bool
	TimeFormat   string
	LogDir       string
	FilePrefix   string
	FileFormat   OutputFormat
	Stdout       bool
	StdoutFormat OutputFormat
	Stdlib       bool
	FS           filesystem.ReadWriteFileFS

	// JSONOutput is retained for source compatibility with direct Config users.
	//
	// Deprecated: use FileFormat and StdoutFormat.
	JSONOutput bool
}

// DefaultConfig returns debug logging with source annotations, JSON file
// output, console-formatted stdout, and standard-library redirection disabled.
func DefaultConfig() Config {
	return Config{
		Level:        slog.LevelDebug,
		AddSource:    true,
		TimeFormat:   "2006-01-02 15:04:05.000",
		LogDir:       "logs",
		FilePrefix:   "app",
		FileFormat:   FormatJSON(),
		Stdout:       true,
		StdoutFormat: FormatConsole(),
	}
}

// WithLevel sets the slog level filter.
func WithLevel(level slog.Level) options.Option[Config] {
	return func(config *Config) { config.Level = level }
}

// WithAddSource toggles slog's source-location annotation. Default true.
func WithAddSource(enabled bool) options.Option[Config] {
	return func(config *Config) { config.AddSource = enabled }
}

// WithTimeFormat sets the time format used by console handlers.
func WithTimeFormat(format string) options.Option[Config] {
	return func(config *Config) { config.TimeFormat = format }
}

// WithLogDir sets the directory where log files are written.
func WithLogDir(dir string) options.Option[Config] {
	return func(config *Config) { config.LogDir = dir }
}

// WithFilePrefix sets the file-name prefix; the timestamp is appended.
func WithFilePrefix(prefix string) options.Option[Config] {
	return func(config *Config) { config.FilePrefix = prefix }
}

// WithFileFormat sets the log-file format. Console format is permitted but
// colour is disabled so files do not contain ANSI escape sequences.
func WithFileFormat(format OutputFormat) options.Option[Config] {
	return func(config *Config) { config.FileFormat = format }
}

// WithStdoutFormat sets the stdout log format. Console colour is enabled only
// when stdout is a terminal.
func WithStdoutFormat(format OutputFormat) options.Option[Config] {
	return func(config *Config) { config.StdoutFormat = format }
}

// WithJSONOutput selects compatible formats for both destinations. When
// enabled both are JSON; when disabled files are plain text and stdout uses
// console presentation.
//
// Deprecated: use [WithFileFormat] and [WithStdoutFormat].
func WithJSONOutput(enabled bool) options.Option[Config] {
	return func(config *Config) {
		config.JSONOutput = enabled
		if enabled {
			config.FileFormat = FormatJSON()
			config.StdoutFormat = FormatJSON()
			return
		}
		config.FileFormat = FormatText()
		config.StdoutFormat = FormatConsole()
	}
}

// WithFS injects a custom filesystem.
func WithFS(fs filesystem.ReadWriteFileFS) options.Option[Config] {
	return func(config *Config) { config.FS = fs }
}

// WithStdout enables or disables stdout logging. Default true. TUI consumers
// should disable it while an alternate screen owns stdout.
func WithStdout(enabled bool) options.Option[Config] {
	return func(config *Config) { config.Stdout = enabled }
}

// WithStdlib controls redirection of the standard library log package to the
// same destinations as slog. Its byte output does not use slog formatting.
func WithStdlib(enabled bool) options.Option[Config] {
	return func(config *Config) { config.Stdlib = enabled }
}

// SetStdlibOutput redirects the standard library log package. Prefer
// [WithStdlib] when using Setup.
func SetStdlibOutput(writer io.Writer) {
	if writer != nil {
		log.SetOutput(writer)
	}
}

// Setup configures slog as the package default and returns the open log file.
func Setup(opts ...options.Option[Config]) (filesystem.File, error) {
	config := DefaultConfig()
	for _, option := range opts {
		option(&config)
	}
	return SetupConfig(config)
}

// SetupConfig configures slog from an explicit Config and returns the open log
// file. The caller owns the returned file and must close it.
func SetupConfig(config Config) (filesystem.File, error) {
	resolveFormats(&config)
	if !validOutputFormat(config.FileFormat) {
		return nil, fmt.Errorf("file format: %w: %d", ErrInvalidOutputFormat, config.FileFormat)
	}
	if config.Stdout && !validOutputFormat(config.StdoutFormat) {
		return nil, fmt.Errorf("stdout format: %w: %d", ErrInvalidOutputFormat, config.StdoutFormat)
	}

	logFile, err := openLogFile(config)
	if err != nil {
		return nil, err
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = logFile.Close()
		}
	}()

	fileHandler, err := newHandler(logFile, config.FileFormat, handlerOptions{
		level: config.Level, addSource: config.AddSource,
		timeFormat: config.TimeFormat, noColor: true,
	})
	if err != nil {
		return nil, fmt.Errorf("create file log handler: %w", err)
	}

	handlers := []slog.Handler{fileHandler}
	var stdlibWriter io.Writer = logFile
	if config.Stdout {
		stdoutHandler, handlerErr := newHandler(os.Stdout, config.StdoutFormat, handlerOptions{
			level: config.Level, addSource: config.AddSource,
			timeFormat: config.TimeFormat, noColor: !term.IsTerminal(int(os.Stdout.Fd())),
		})
		if handlerErr != nil {
			return nil, fmt.Errorf("create stdout log handler: %w", handlerErr)
		}
		handlers = append(handlers, stdoutHandler)
		stdlibWriter = io.MultiWriter(logFile, os.Stdout)
	}

	slog.SetDefault(slog.New(multiHandler{handlers: handlers}))
	if config.Stdlib {
		SetStdlibOutput(stdlibWriter)
	}
	closeOnError = false
	return logFile, nil
}

func resolveFormats(config *Config) {
	if config.FileFormat == formatUnspecified {
		if config.JSONOutput {
			config.FileFormat = FormatJSON()
		} else {
			config.FileFormat = FormatText()
		}
	}
	if config.StdoutFormat == formatUnspecified {
		if config.JSONOutput {
			config.StdoutFormat = FormatJSON()
		} else {
			config.StdoutFormat = FormatConsole()
		}
	}
}

func openLogFile(config Config) (filesystem.File, error) {
	logPath := filepath.Join(config.LogDir, fmt.Sprintf("%s_%s.log", config.FilePrefix, time.Now().Format("2006-01-02_15-04-05")))
	if config.FS == nil {
		if err := os.MkdirAll(config.LogDir, filesystem.ModePublicDir); err != nil {
			return nil, fmt.Errorf("create log directory: %w", err)
		}
		file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, filesystem.ModePublicFile)
		if err != nil {
			return nil, fmt.Errorf("open log file: %w", err)
		}
		return file, nil
	}
	if err := config.FS.MkdirAll(config.LogDir, filesystem.ModePublicDir); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	file, err := config.FS.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, filesystem.ModePublicFile)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	return file, nil
}
