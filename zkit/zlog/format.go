package zlog

import (
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/lmittmann/tint"
)

// ErrInvalidOutputFormat indicates an unknown output format.
var ErrInvalidOutputFormat = errors.New("invalid log output format")

// OutputFormat selects the encoding and presentation used by a log destination.
// Construct values with [FormatJSON], [FormatText], or [FormatConsole].
type OutputFormat uint8

const (
	formatUnspecified OutputFormat = iota
	formatJSON
	formatText
	formatConsole
)

// FormatJSON returns the structured, newline-delimited JSON format recommended
// for log files and ingestion systems.
func FormatJSON() OutputFormat { return formatJSON }

// FormatText returns slog's newline-delimited key=value text format. It never
// adds ANSI terminal escape sequences.
func FormatText() OutputFormat { return formatText }

// FormatConsole returns tint's human-oriented console format. Setup enables
// colour only when writing stdout to a terminal; file output remains uncoloured.
func FormatConsole() OutputFormat { return formatConsole }

type handlerOptions struct {
	level      slog.Leveler
	addSource  bool
	timeFormat string
	noColor    bool
}

func newHandler(writer io.Writer, format OutputFormat, opts handlerOptions) (slog.Handler, error) {
	slogOpts := &slog.HandlerOptions{Level: opts.level, AddSource: opts.addSource}

	switch format {
	case formatJSON:
		return slog.NewJSONHandler(writer, slogOpts), nil
	case formatText:
		return slog.NewTextHandler(writer, slogOpts), nil
	case formatConsole:
		return tint.NewHandler(writer, &tint.Options{
			Level:      opts.level,
			AddSource:  opts.addSource,
			TimeFormat: opts.timeFormat,
			NoColor:    opts.noColor,
		}), nil
	default:
		return nil, fmt.Errorf("%w: %d", ErrInvalidOutputFormat, format)
	}
}

func validOutputFormat(format OutputFormat) bool {
	return format == formatJSON || format == formatText || format == formatConsole
}
