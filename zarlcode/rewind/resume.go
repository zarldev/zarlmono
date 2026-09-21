package rewind

import (
	"encoding/json"
	"reflect"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// ResumeState is the versioned exact head of a continuation branch. Revision
// binds it to the canonical transcript committed in the same transaction. It
// independently owns target policy; checkpoint eviction cannot break resume.
// Credentials and endpoints must never be added to this representation.
type ResumeState struct {
	Version             uint64              `json:"rewind_resume_version"`
	Revision            uint64              `json:"revision"`
	Context             []llm.Message       `json:"context"`
	Target              Target              `json:"target"`
	SettledTurnID       string              `json:"settled_turn_id"`
	EventWatermark      uint64              `json:"event_watermark"`
	InitialContinuation InitialContinuation `json:"initial_continuation,omitzero"`
}

// EncodeResume saves an exact branch head, without legacy context repair.
func EncodeResume(revision uint64, messages []llm.Message, target Target, settledTurnID string, eventWatermark uint64, initial InitialContinuation) ([]byte, error) {
	if messages == nil {
		messages = []llm.Message{}
	}
	state := ResumeState{Version: 1, Revision: revision, Context: messages, Target: target,
		SettledTurnID: settledTurnID, EventWatermark: eventWatermark, InitialContinuation: initial}
	if err := validateTargetContext(messages, target); err != nil {
		return nil, err
	}
	if !validResume(state) {
		return nil, ErrInvalid
	}
	if !validStrings(reflect.ValueOf(messages)) {
		return encodeByteResume(state)
	}
	data, err := json.Marshal(state)
	if err != nil {
		return nil, ErrInvalid
	}
	return data, nil
}

// DecodeResume admits only the exact versioned branch representation. Errors
// exclude private context bytes; callers must not fall back to legacy repair.
func DecodeResume(data []byte) (ResumeState, error) {
	var header struct {
		Version uint64 `json:"rewind_resume_version"`
	}
	if json.Unmarshal(data, &header) != nil {
		return ResumeState{}, ErrInvalid
	}
	if header.Version == 2 {
		return decodeByteResume(data)
	}
	var state ResumeState
	if !strictJSON(data, &state) || !validResume(state) {
		return ResumeState{}, ErrInvalid
	}
	return state, nil
}

func validResume(state ResumeState) bool {
	metadata := state
	metadata.Context = nil
	return (state.Version == 1 || state.Version == 2) && validStrings(reflect.ValueOf(metadata)) && state.Context != nil && state.Target.Provider != "" &&
		state.Target.Model != "" && state.Target.Window >= 0 && state.Target.Reserve >= 0 &&
		validBoundary(state.Revision, len(state.Context) == 0, state.SettledTurnID, state.EventWatermark, state.InitialContinuation) &&
		validateTargetContext(state.Context, state.Target) == nil
}
