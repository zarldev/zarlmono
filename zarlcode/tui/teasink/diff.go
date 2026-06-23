package teasink

import "github.com/zarldev/zarlmono/zkit/agent/diffrecorder"

// (write / edit). Diff is a unified diff. It is NOT a
// runner EventSink event — it's dispatched through the same pump
// (Sink.Diff) so it stays ordered with the tool events around it.
type DiffMsg struct {
	Path          string
	Diff          string
	Before        []byte
	BeforeMissing bool
	After         []byte
	AfterMissing  bool
}

// Diff dispatches a recorded file diff through the pump, flushing any
// pending content first so the diff lands after the chunks that preceded
// it — mirroring the OnTool* ordering discipline.
func (s *Sink) Diff(path, diff string) {
	s.flush()
	s.dispatch(DiffMsg{Path: path, Diff: diff})
}

// DiffEvent dispatches a rich diff recorder event through the pump, preserving
// the pre/post images needed by the TUI checkpoint model.
func (s *Sink) DiffEvent(e diffrecorder.DiffEvent) {
	s.flush()
	s.dispatch(DiffMsg{
		Path:          e.Path,
		Diff:          e.Diff,
		Before:        append([]byte(nil), e.Before...),
		BeforeMissing: e.BeforeMissing,
		After:         append([]byte(nil), e.After...),
		AfterMissing:  e.AfterMissing,
	})
}
