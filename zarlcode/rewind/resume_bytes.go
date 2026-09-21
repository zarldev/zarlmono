package rewind

import (
	"encoding/json"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// byteResumeState keeps ordinary boundary metadata separate from opaque context
// strings. Version 1 remains the direct-JSON representation for UTF-8 contexts.
type byteResumeState struct {
	Version             uint64              `json:"rewind_resume_version"`
	Revision            uint64              `json:"revision"`
	Context             []json.RawMessage   `json:"context"`
	Target              Target              `json:"target"`
	SettledTurnID       string              `json:"settled_turn_id"`
	EventWatermark      uint64              `json:"event_watermark"`
	InitialContinuation InitialContinuation `json:"initial_continuation,omitzero"`
}

func encodeByteResume(state ResumeState) ([]byte, error) {
	saved := byteResumeState{Version: 2, Revision: state.Revision, Target: state.Target,
		SettledTurnID: state.SettledTurnID, EventWatermark: state.EventWatermark,
		InitialContinuation: state.InitialContinuation, Context: make([]json.RawMessage, len(state.Context))}
	for i, message := range state.Context {
		data, err := runner.MarshalReplayMessage(runner.ReplayMessage{Message: message})
		if err != nil {
			return nil, ErrInvalid
		}
		saved.Context[i] = data
	}
	data, err := json.Marshal(saved)
	if err != nil {
		return nil, ErrInvalid
	}
	return data, nil
}

func decodeByteResume(data []byte) (ResumeState, error) {
	var saved byteResumeState
	if !strictJSON(data, &saved) || saved.Version != 2 || saved.Context == nil {
		return ResumeState{}, ErrInvalid
	}
	state := ResumeState{Version: saved.Version, Revision: saved.Revision, Target: saved.Target,
		SettledTurnID: saved.SettledTurnID, EventWatermark: saved.EventWatermark,
		InitialContinuation: saved.InitialContinuation, Context: make([]llm.Message, len(saved.Context))}
	for i, data := range saved.Context {
		record, err := runner.UnmarshalReplayMessage(data)
		if err != nil || !record.AdmittedToModelContext() {
			return ResumeState{}, ErrInvalid
		}
		state.Context[i] = record.Message
	}
	if !validResume(state) {
		return ResumeState{}, ErrInvalid
	}
	return state, nil
}
