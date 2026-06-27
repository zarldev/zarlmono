package trace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// JSONLExporter writes one JSON-encoded Event per line to an io.Writer. It is
// safe for concurrent Export calls.
type JSONLExporter struct {
	mu sync.Mutex
	w  io.Writer
}

// NewJSONLExporter returns an exporter that writes trace events to w.
func NewJSONLExporter(w io.Writer) *JSONLExporter { return &JSONLExporter{w: w} }

// Export writes event as one JSON line.
func (e *JSONLExporter) Export(ctx context.Context, event Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if e == nil || e.w == nil {
		return errors.New("export trace event: writer is nil")
	}
	line, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal trace event: %w", err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, err := e.w.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("write trace event: %w", err)
	}
	return nil
}
