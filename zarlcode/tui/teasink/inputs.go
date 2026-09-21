package teasink

import (
	"context"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// WaitingForInputsMsg updates live waiting without settling the conversation.
type WaitingForInputsMsg struct {
	TaskID  string
	Depth   int
	Waiting bool
}

// InputsAdmittedMsg projects successful input admission independently of tool
// lifecycle events and human steering. It never requests execution on replay.
type InputsAdmittedMsg struct {
	TaskID     string
	Depth      int
	Messages   []llm.Message
	References []tools.AdmissionReference
}

// OnWaitingForInputs flushes provisional output before publishing wait state.
func (s *Sink) OnWaitingForInputs(_ context.Context, event runner.WaitingForInputs) {
	s.flush()
	s.dispatch(WaitingForInputsMsg{TaskID: string(event.TaskID), Depth: event.Depth, Waiting: event.Waiting})
}

// OnInputsAdmitted copies admitted observations into the UI event stream.
func (s *Sink) OnInputsAdmitted(_ context.Context, event runner.InputsAdmitted) {
	s.flush()
	s.dispatch(InputsAdmittedMsg{TaskID: string(event.TaskID), Depth: event.Depth,
		Messages: llm.CloneMessages(event.Messages), References: append([]tools.AdmissionReference(nil), event.References...)})
}
