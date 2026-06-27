package trace

import "context"

// Exporter consumes trace events.
type Exporter interface {
	Export(ctx context.Context, event Event) error
}

// ExporterFunc adapts a function to Exporter.
type ExporterFunc func(context.Context, Event) error

// Export calls f itself.
func (f ExporterFunc) Export(ctx context.Context, event Event) error { return f(ctx, event) }
