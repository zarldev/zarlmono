package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

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

// RestoreSessionContext publishes validated selection hints together with a
// legacy session context while runtime admission is exclusively reserved.
func (r *RuntimeReservation) RestoreSessionContext(messages []llm.Message, state MCPActivations) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active || r.dispatchOnly {
		return ErrRuntimeBusy
	}
	r.owner.context.mu.Lock()
	defer r.owner.context.mu.Unlock()
	r.owner.publication.Lock()
	defer r.owner.publication.Unlock()
	if err := r.owner.restoreMCPActivations(state); err != nil {
		return err
	}
	r.owner.context.context = llm.CloneMessages(messages)
	r.owner.context.exact = false
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
