package teasink_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

func TestModeChangedPreservesAppliedEventOrder(t *testing.T) {
	send, snapshot := recordingSend()
	sink := teasink.New(send)
	defer sink.Close()
	sink.OnContent(t.Context(), runner.Content{TaskID: "root", Delta: "investigating"})
	applied := engine.ModeChanged{TaskID: "root", Plan: true, Reason: "redesign", Generation: 3}
	sink.ModeChanged(applied)
	sink.OnContent(t.Context(), runner.Content{TaskID: "root", Delta: "planning"})
	sink.Drain()
	events := snapshot()
	if len(events) != 3 {
		t.Fatalf("events=%+v", events)
	}
	before, ok := events[0].(teasink.ContentMsg)
	if !ok || before.Delta != "investigating" {
		t.Fatalf("before=%+v", events[0])
	}
	change, ok := events[1].(teasink.ModeChangedMsg)
	if !ok || engine.ModeChanged(change) != applied {
		t.Fatalf("change=%+v", events[1])
	}
	after, ok := events[2].(teasink.ContentMsg)
	if !ok || after.Delta != "planning" {
		t.Fatalf("after=%+v", events[2])
	}
}
