package engine

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/db"
)

// ErrRequestControlDrift means the applied policy changed after the prompt was
// rendered but before its prepared request reached durable capture.
var ErrRequestControlDrift = errors.New("request control observation changed during preparation")

type requestControlObservation struct {
	policyLabel string
	generation  uint64
}

// ReadRequestControls returns independently owned controls from durable replay.
// It never interprets them as live policy or permissions.
func ReadRequestControls(ctx context.Context, store *db.Store, sessionID string) ([]runner.ReplayControl, error) {
	replay, err := store.ReadSessionReplay(ctx, sessionID)
	if errors.Is(err, db.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read request controls: %w", err)
	}
	return decodeRequestControls(replay)
}

type requestControlState struct {
	hydrated bool
	baseline string
	notified map[string]bool
}

func (b *sessionHistoryBuffer) captureRequest(
	ctx context.Context,
	store *db.Store,
	sessionID string,
	request llm.CompletionRequest,
	requestJSON []byte,
	rendered requestControlObservation,
	current requestControlObservation,
	observer func(runner.ReplayControl),
) error {
	b.mu.Lock()
	if err := b.hydrateControlLocked(ctx, store, sessionID); err != nil {
		b.mu.Unlock()
		return err
	}
	if current != rendered {
		b.mu.Unlock()
		return ErrRequestControlDrift
	}

	var messages [][]byte
	control, err := requestControl(request, rendered.policyLabel)
	if err != nil {
		b.mu.Unlock()
		return err
	}
	control.TurnID = string(taskscope.IDFrom(ctx))
	if control.SurfaceDigest != b.control.baseline {
		data, err := runner.MarshalReplayMessage(runner.ReplayMessage{
			Kind: runner.ReplayOccurrenceKinds.CONTROL, Control: control,
		})
		if err != nil {
			b.mu.Unlock()
			return fmt.Errorf("encode request control: %w", err)
		}
		messages = [][]byte{data}
		b.control.baseline = control.SurfaceDigest
	}

	b.pending = append(b.pending, db.SessionHistoryBatch{
		ID: uuid.NewString(), Messages: messages, RequestJSON: requestJSON,
	})
	if err := store.SaveSessionHistory(ctx, sessionID, b.pending); err != nil {
		b.mu.Unlock()
		return err
	}
	controls, err := b.unnotifiedControlsLocked(observer != nil)
	if err != nil {
		b.mu.Unlock()
		return err
	}
	b.mu.Unlock()
	if observer != nil {
		for _, control := range controls {
			observer(control.Clone())
		}
	}
	return nil
}

func (b *sessionHistoryBuffer) hydrateControlLocked(ctx context.Context, store *db.Store, sessionID string) error {
	if b.control.hydrated {
		return nil
	}
	replay, err := store.ReadSessionReplay(ctx, sessionID)
	if errors.Is(err, db.ErrNotFound) {
		replay = nil
	} else if err != nil {
		return fmt.Errorf("hydrate request controls: %w", err)
	}
	for _, batch := range b.pending {
		replay = append(replay, batch.Messages...)
	}
	controls, err := decodeRequestControls(replay)
	if err != nil {
		return err
	}
	if len(controls) != 0 {
		b.control.baseline = controls[len(controls)-1].SurfaceDigest
	}
	b.control.hydrated = true
	return nil
}

func decodeRequestControls(replay [][]byte) ([]runner.ReplayControl, error) {
	var controls []runner.ReplayControl
	for _, data := range replay {
		record, err := runner.UnmarshalReplayMessage(data)
		if err != nil {
			return nil, fmt.Errorf("decode request control replay: %w", err)
		}
		if record.IsControl() {
			controls = append(controls, record.Control.Clone())
		}
	}
	return controls, nil
}

func (b *sessionHistoryBuffer) unnotifiedControlsLocked(notify bool) ([]runner.ReplayControl, error) {
	if notify && b.control.notified == nil {
		b.control.notified = make(map[string]bool)
	}
	var controls []runner.ReplayControl
	for _, batch := range b.pending {
		for _, data := range batch.Messages {
			record, err := runner.UnmarshalReplayMessage(data)
			if err != nil {
				return nil, fmt.Errorf("decode pending request control: %w", err)
			}
			if notify && record.IsControl() && !b.control.notified[record.Control.ID] {
				controls = append(controls, record.Control.Clone())
			}
		}
	}
	for _, control := range controls {
		b.control.notified[control.ID] = true
	}
	return controls, nil
}

func requestControl(request llm.CompletionRequest, policyLabel string) (runner.ReplayControl, error) {
	if policyLabel == "" || len(policyLabel) > 128 {
		return runner.ReplayControl{}, errors.New("invalid request control policy label")
	}
	systemHash := sha256.New()
	for _, message := range request.Messages {
		if message.Role == llm.RoleSystem {
			writeDigestField(systemHash, message.Content)
		}
	}
	control := runner.ReplayControl{
		Version: 1, ID: uuid.NewString(), PolicyLabel: policyLabel,
		SystemDigest:    hex.EncodeToString(systemHash.Sum(nil)),
		ToolDefinitions: make([]runner.ControlToolDefinition, len(request.Tools)),
	}
	for i, tool := range request.Tools {
		definition, err := json.Marshal(tool)
		if err != nil {
			return runner.ReplayControl{}, fmt.Errorf("encode prepared tool definition %q: %w", tool.Function.Name, err)
		}
		sum := sha256.Sum256(definition)
		control.ToolDefinitions[i] = runner.ControlToolDefinition{
			Name: tool.Function.Name, DefinitionHash: hex.EncodeToString(sum[:]),
		}
	}
	surfaceHash := sha256.New()
	writeDigestField(surfaceHash, "request-control.v1")
	writeDigestField(surfaceHash, control.PolicyLabel)
	writeDigestField(surfaceHash, control.SystemDigest)
	for _, definition := range control.ToolDefinitions {
		writeDigestField(surfaceHash, definition.Name)
		writeDigestField(surfaceHash, definition.DefinitionHash)
	}
	control.SurfaceDigest = hex.EncodeToString(surfaceHash.Sum(nil))
	return control, nil
}

func writeDigestField(digest hash.Hash, value string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = digest.Write(size[:])
	_, _ = digest.Write([]byte(value))
}
