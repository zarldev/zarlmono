package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/dynamic"
)

// ErrMCPActivations identifies invalid or unsupported saved MCP selection data.
var ErrMCPActivations = errors.New("invalid MCP activation state")

// MCPActivations is an independently owned, validated set of selection hints.
// It contains no connection configuration, approval, or executable authority.
// Its zero value is the empty selection used by legacy sessions.
type MCPActivations struct {
	refs []dynamic.MCPToolRef
}

type mcpActivationEnvelope struct {
	Version int                  `json:"version"`
	Tools   []dynamic.MCPToolRef `json:"tools"`
}

// DecodeMCPActivations parses a bounded persisted selection. Empty legacy data
// means no desired tools. Invalid data is rejected, never repaired or activated.
func DecodeMCPActivations(data []byte) (MCPActivations, error) {
	if len(data) == 0 {
		return MCPActivations{}, nil
	}
	if len(data) > 32*1024 {
		return MCPActivations{}, fmt.Errorf("%w: blob exceeds 32 KiB", ErrMCPActivations)
	}
	var envelope mcpActivationEnvelope
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return MCPActivations{}, fmt.Errorf("%w: decode: %w", ErrMCPActivations, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return MCPActivations{}, fmt.Errorf("%w: trailing data", ErrMCPActivations)
	}
	if envelope.Version != 1 {
		return MCPActivations{}, fmt.Errorf("%w: unsupported version %d", ErrMCPActivations, envelope.Version)
	}
	if err := dynamic.ValidateMCPToolRefs(envelope.Tools); err != nil {
		return MCPActivations{}, fmt.Errorf("%w: %w", ErrMCPActivations, err)
	}
	return MCPActivations{refs: envelope.Tools}, nil
}

// Encode returns the versioned persisted representation of these selection hints.
func (s MCPActivations) Encode() ([]byte, error) {
	return json.Marshal(mcpActivationEnvelope{Version: 1, Tools: s.refs})
}

// MCPActivations snapshots the session's desired references, including dormant
// selections. It never connects a server or exposes tool definitions.
func (l *LiveRunner) MCPActivations() MCPActivations {
	l.publication.Lock()
	defer l.publication.Unlock()
	l.mu.Lock()
	exposure := l.mcpExposure
	l.mu.Unlock()
	if exposure == nil { // MCP is an optional composition-time capability.
		return MCPActivations{}
	}
	return MCPActivations{refs: exposure.Snapshot()}
}

// RestoreMCPActivations replaces current selection hints under exclusive runtime
// admission. It cannot merge sessions or restore an approval from history.
func (r *RuntimeReservation) RestoreMCPActivations(state MCPActivations) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active || r.dispatchOnly {
		return ErrRuntimeBusy
	}
	return r.owner.restoreMCPActivations(state)
}

// RestoreSessionContext publishes legacy context and selection hints under the
// active route. Unsupported context returns rewind.ErrTarget without publication.
func (r *RuntimeReservation) RestoreSessionContext(messages []llm.Message, state MCPActivations) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.restoreOrdinarySession(messages, state, nil, func() error { return nil })
}

// StoredSessionRestore describes one ordinary conversation activation. Target is
// used only when UseSavedTarget selects an already-constructed saved route.
type StoredSessionRestore struct {
	ID             string
	Context        []llm.Message
	MCPActivations MCPActivations
	Target         TargetUpdate
	UseSavedTarget bool
}

// RestoreStoredSession validates the selected route and commits its workspace
// preferences before atomically publishing context, target and selection hints.
// Any admission, validation or persistence error leaves live state unchanged.
func (r *RuntimeReservation) RestoreStoredSession(ctx context.Context, session StoredSessionRestore, preferences *prefs.Service) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var target *TargetUpdate
	if session.UseSavedTarget {
		target = &session.Target
	}
	return r.restoreOrdinarySession(session.Context, session.MCPActivations, target, func() error {
		if session.UseSavedTarget {
			return preferences.SetSessionSelection(ctx, prefs.ModelSelection{Provider: session.Target.Spec.Name, Model: session.Target.Spec.Model}, session.ID)
		}
		return preferences.SetSetting(ctx, prefs.ScopeWorkspace, prefs.KeyActiveSession, session.ID)
	})
}

func (r *RuntimeReservation) restoreOrdinarySession(messages []llm.Message, state MCPActivations, update *TargetUpdate, persist func() error) error {
	if !r.active || r.dispatchOnly || (update != nil && !r.targetMutable) {
		return ErrRuntimeBusy
	}
	r.owner.mu.Lock()
	provider := r.owner.target.Spec.Name
	exposure := r.owner.mcpExposure
	r.owner.mu.Unlock()
	if update != nil {
		provider = update.Spec.Name
	}
	if err := rewind.ValidateLegacyContext(messages, provider); err != nil {
		return fmt.Errorf("%w: saved context is incompatible with the selected route", rewind.ErrTarget)
	}
	if exposure == nil && len(state.refs) != 0 {
		return fmt.Errorf("%w: MCP is not configured in this runtime", ErrMCPActivations)
	}
	context := llm.CloneMessages(messages)
	r.owner.context.mu.Lock()
	defer r.owner.context.mu.Unlock()
	r.owner.publication.Lock()
	defer r.owner.publication.Unlock()
	if err := persist(); err != nil {
		return fmt.Errorf("select saved session: %w", err)
	}
	if exposure != nil {
		// MCPActivations is already validated by DecodeMCPActivations; Replace
		// only rejects malformed references and cannot reject this semantic state.
		_ = exposure.Replace(state.refs)
	}
	r.owner.context.context = context
	r.owner.context.exact = false
	r.owner.mu.Lock()
	if update != nil {
		r.owner.target.Provider, r.owner.target.Spec = update.Provider, update.Spec
		r.owner.target.Model = update.Spec.Model
		r.owner.target.Window = update.Window
		if r.owner.target.Window <= 0 {
			r.owner.target.Window = LiveContextWindow
		}
	}
	r.owner.rotateProgramOutputsLocked()
	r.owner.mu.Unlock()
	return nil
}

func (l *LiveRunner) restoreMCPActivations(state MCPActivations) error {
	l.mu.Lock()
	exposure := l.mcpExposure
	l.mu.Unlock()
	if exposure == nil {
		if len(state.refs) != 0 {
			return fmt.Errorf("%w: MCP is not configured in this runtime", ErrMCPActivations)
		}
		return nil
	}
	return exposure.Replace(state.refs)
}
