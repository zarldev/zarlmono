package zlog

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"
)

type recordingHandler struct {
	enabled bool
	err     error
	records *[]slog.Record
	attrs   *[]slog.Attr
	groups  *[]string
}

func (h recordingHandler) Enabled(context.Context, slog.Level) bool { return h.enabled }
func (h recordingHandler) Handle(_ context.Context, record slog.Record) error {
	*h.records = append(*h.records, record)
	return h.err
}
func (h recordingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	*h.attrs = append(*h.attrs, attrs...)
	return h
}
func (h recordingHandler) WithGroup(name string) slog.Handler {
	*h.groups = append(*h.groups, name)
	return h
}

func TestMultiHandlerDispatchAndErrors(t *testing.T) {
	t.Parallel()

	firstErr := errors.New("first")
	secondErr := errors.New("second")
	var firstRecords, secondRecords []slog.Record
	var attrs []slog.Attr
	var groups []string
	handler := multiHandler{handlers: []slog.Handler{
		recordingHandler{enabled: true, err: firstErr, records: &firstRecords, attrs: &attrs, groups: &groups},
		recordingHandler{enabled: true, err: secondErr, records: &secondRecords, attrs: &attrs, groups: &groups},
	}}

	if !handler.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("Enabled = false, want true")
	}
	derived := handler.WithAttrs([]slog.Attr{slog.String("component", "test")}).WithGroup("request")
	err := derived.Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelInfo, "message", 0))
	if !errors.Is(err, firstErr) || !errors.Is(err, secondErr) {
		t.Fatalf("Handle error = %v, want both child errors", err)
	}
	if len(firstRecords) != 1 || len(secondRecords) != 1 {
		t.Fatalf("record counts = %d, %d; want 1, 1", len(firstRecords), len(secondRecords))
	}
	if len(attrs) != 2 || len(groups) != 2 {
		t.Fatalf("derived calls attrs=%d groups=%d, want 2 each", len(attrs), len(groups))
	}
}

func TestMultiHandlerEmptyIsDisabled(t *testing.T) {
	t.Parallel()
	if (multiHandler{}).Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("empty multiHandler is enabled")
	}
}
