// Package rewind owns exact, application-versioned conversation checkpoints.
// Capture and branch preparation do not establish runtime quiescence or restore
// files. The runtime/UI must hold admission and persistence barriers around them.
package rewind

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"slices"

	"github.com/zarldev/zarlmono/zarlcode/draft"
	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
)

// FormatVersion identifies the exact conversation-only checkpoint envelope.
const FormatVersion uint64 = 1

// ErrInvalid means an exact checkpoint cannot be decoded or admitted. Errors
// deliberately exclude private prompt/context bytes and parser diagnostics.
var ErrInvalid = errors.New("invalid conversation checkpoint")

// ErrUnavailable means no exact checkpoint exists, rather than an empty context.
var ErrUnavailable = errors.New("conversation checkpoint unavailable")

// ErrAttachments means the selected prompt cannot be replayed as text in M1.
var ErrAttachments = errors.New("rewind of attachment-bearing prompts is unavailable")

// ErrTarget means the saved provider/model target is not the prepared target.
var ErrTarget = errors.New("checkpoint requires its saved provider and model")

// FilesUnchangedNotice is a truthful continuation warning, not historical text.
const FilesUnchangedNotice = "Conversation-only rewind: files were not restored and may differ from this earlier conversation. Shell, MCP, network, Git, process, and database side effects were not undone. Recheck the current workspace before relying on earlier changes or verification."

// Target records non-secret context-affecting target policy. Credentials,
// endpoints, approval grants and tool/process handles are deliberately absent.
// Resolve credentials through the current vault and retain current security policy.
type Target struct {
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	Window      int    `json:"window"`
	Reserve     int    `json:"reserve"`
	PlanMode    bool   `json:"plan_mode"`
	CodexEffort string `json:"codex_effort"`
}

// InitialContinuation identifies the durable branch creation that established
// an exact head with no settled turn. Its zero value means no such provenance.
// The application must match it to the session's integrity-checked branch row;
// neither notice text nor transcript shape establishes this identity.
type InitialContinuation struct {
	CheckpointID       string `json:"checkpoint_id"`
	CheckpointChecksum string `json:"checkpoint_checksum"`
}

// Matches reports whether this initial head belongs to the verified branch.
func (i InitialContinuation) Matches(branch db.SessionBranch) bool {
	return i.CheckpointID != "" && i.CheckpointID == branch.SourceCheckpointID &&
		i.CheckpointChecksum != "" && i.CheckpointChecksum == branch.CheckpointChecksum
}

func validBoundary(revision uint64, empty bool, turnID string, watermark uint64, initial InitialContinuation) bool {
	if initial != (InitialContinuation{}) {
		return initial.CheckpointID != "" && initial.CheckpointChecksum != "" && turnID == "" && watermark == 0
	}
	if turnID == "" || watermark == 0 {
		return turnID == "" && watermark == 0 && revision == 0 && empty
	}
	return watermark <= revision
}

// Boundary binds a BEFORE checkpoint to one top-level prompt, never a rendered
// row. SettledTurnID and EventWatermark bind canonical event application to the
// completed engine context; the runtime is responsible for that observation.
type Boundary struct {
	PromptID            string              `json:"prompt_id"`
	PromptText          string              `json:"prompt_text"`
	HasAttachments      bool                `json:"has_attachments"`
	Attachments         []llm.ContentPart   `json:"attachments,omitempty"`
	SettledTurnID       string              `json:"settled_turn_id"`
	EventWatermark      uint64              `json:"event_watermark"`
	InitialContinuation InitialContinuation `json:"initial_continuation,omitzero"`
}

// CaptureInput is the state observed under a runtime reservation. ToolCallIDs
// contains only full-output records actually retained for this canonical prefix.
// Plan is historical intent, not proof of present workspace state.
type CaptureInput struct {
	ID          string
	SessionID   string
	Workspace   string
	Boundary    Boundary
	Transcript  transcript.Checkpoint
	Context     []llm.Message
	Target      Target
	Plan        code.Plan
	ToolCallIDs []string
}

type payload struct {
	Version     uint64        `json:"version"`
	ID          string        `json:"id"`
	SourceID    string        `json:"source_id"`
	Workspace   string        `json:"workspace"`
	Boundary    Boundary      `json:"boundary"`
	Revision    uint64        `json:"revision"`
	Records     []savedRecord `json:"records"`
	Context     []llm.Message `json:"context"`
	Target      Target        `json:"target"`
	Plan        code.Plan     `json:"plan"`
	ToolCallIDs []string      `json:"tool_call_ids"`
}

// Checkpoint owns immutable serialized state and a strictly validated canonical
// snapshot. Copies may share private immutable data; all exported snapshots own
// their mutable bytes. Its zero value is unavailable, not an initial checkpoint.
type Checkpoint struct {
	record    db.SessionCheckpoint
	state     payload
	canonical transcript.Checkpoint
	valid     bool
}

// Capture encodes a new immutable checkpoint without saving or admitting a turn.
// Persist Record successfully before releasing the reservation into dispatch.
func Capture(input CaptureInput) (Checkpoint, error) {
	records, err := input.Transcript.Records()
	if err != nil {
		return Checkpoint{}, ErrUnavailable
	}
	context := llm.CloneMessages(input.Context)
	if context == nil {
		context = []llm.Message{}
	}
	state := payload{
		Version: FormatVersion, ID: input.ID, SourceID: input.SessionID, Workspace: input.Workspace,
		Boundary: input.Boundary, Revision: input.Transcript.Revision(), Records: saveRecords(records),
		Context: context, Target: input.Target, Plan: clonePlan(input.Plan), ToolCallIDs: slices.Clone(input.ToolCallIDs),
	}
	state.Boundary.Attachments = llm.CloneContentParts(input.Boundary.Attachments)
	// Legacy snapshots serialize context directly as JSON strings.
	if !validStrings(reflect.ValueOf(state)) {
		return Checkpoint{}, ErrInvalid
	}
	if err := validatePayload(state); err != nil {
		return Checkpoint{}, err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return Checkpoint{}, ErrInvalid
	}
	if len(data) > db.CheckpointPayloadLimit {
		return Checkpoint{}, db.ErrCheckpointQuota
	}
	return Checkpoint{
		record: db.SessionCheckpoint{
			SessionID: input.SessionID, ID: input.ID, SourceSessionID: input.SessionID, Workspace: input.Workspace,
			SourceRevision: state.Revision, BoundaryID: input.Boundary.PromptID, FormatVersion: FormatVersion, Payload: data,
		},
		state: state, canonical: input.Transcript, valid: true,
	}, nil
}

// Decode strictly admits an integrity-checked DB record. It never uses legacy
// transcript/context repair, guesses missing state, or rewrites unsupported data.
// The DB store must check integrity first; branch commit rechecks its checksum.
func Decode(record db.SessionCheckpoint) (Checkpoint, error) {
	if len(record.Payload) == 0 {
		return Checkpoint{}, ErrUnavailable
	}
	if record.FormatVersion != FormatVersion || len(record.Payload) > db.CheckpointPayloadLimit {
		return Checkpoint{}, ErrInvalid
	}
	var state payload
	if !strictJSON(record.Payload, &state) || validatePayload(state) != nil {
		return Checkpoint{}, ErrInvalid
	}
	if state.ID != record.ID || state.SourceID != record.SourceSessionID || state.Workspace != record.Workspace ||
		state.Revision != record.SourceRevision || state.Boundary.PromptID != record.BoundaryID {
		return Checkpoint{}, ErrInvalid
	}
	canonical, err := transcript.CheckpointFromRecords(state.Revision, restoreRecords(state.Records))
	if err != nil {
		return Checkpoint{}, ErrInvalid
	}
	record.Payload = bytes.Clone(record.Payload)
	return Checkpoint{record: record, state: state, canonical: canonical, valid: true}, nil
}

// Record returns an independent transport snapshot for durable storage.
func (c Checkpoint) Record() (db.SessionCheckpoint, error) {
	if !c.valid {
		return db.SessionCheckpoint{}, ErrUnavailable
	}
	record := c.record
	record.Payload = bytes.Clone(record.Payload)
	return record, nil
}

// Snapshot returns independently owned historical state for a reserved runtime
// transition. It does not imply that Target is currently available or built.
func (c Checkpoint) Snapshot() (CaptureInput, error) {
	if !c.valid {
		return CaptureInput{}, ErrUnavailable
	}
	boundary := c.state.Boundary
	boundary.Attachments = llm.CloneContentParts(boundary.Attachments)
	return CaptureInput{
		ID: c.record.ID, SessionID: c.record.SessionID, Workspace: c.state.Workspace,
		Boundary: boundary, Transcript: c.canonical, Context: llm.CloneMessages(c.state.Context),
		Target: c.state.Target, Plan: clonePlan(c.state.Plan), ToolCallIDs: slices.Clone(c.state.ToolCallIDs),
	}, nil
}

// PrepareBranch derives every retained canonical/context byte from this saved
// checkpoint, prefills (but never submits) its prompt and attachments, and clears future
// diff/usage/operational state. preparedTarget must already have been built with
// current credentials/security policy. The caller adds FilesUnchangedNotice as a
// new continuation notice, not by altering any retained historical record.
func (c Checkpoint) PrepareBranch(childID, label string, sourceHead uint64, preparedTarget Target) (db.CheckpointBranch, error) {
	if !c.valid || c.record.Checksum == "" {
		return db.CheckpointBranch{}, ErrUnavailable
	}
	if c.state.Boundary.HasAttachments && len(c.state.Boundary.Attachments) == 0 {
		return db.CheckpointBranch{}, ErrAttachments
	}
	if c.state.Target != preparedTarget {
		return db.CheckpointBranch{}, ErrTarget
	}
	pending, err := draft.EncodeWithAttachments(c.state.Boundary.PromptText, c.state.Boundary.Attachments)
	if err != nil {
		return db.CheckpointBranch{}, ErrInvalid
	}
	var contextJSON []byte
	if c.record.FormatVersion == HistoryFormatVersion {
		contextJSON, err = EncodeResume(c.state.Revision, c.state.Context, c.state.Target,
			c.state.Boundary.SettledTurnID, c.state.Boundary.EventWatermark, c.state.Boundary.InitialContinuation)
	} else {
		contextJSON, err = json.Marshal(c.state.Context)
	}
	if err != nil {
		return db.CheckpointBranch{}, ErrInvalid
	}
	planJSON, err := json.Marshal(c.state.Plan)
	if err != nil {
		return db.CheckpointBranch{}, ErrInvalid
	}
	thread, err := c.canonical.Restore()
	if err != nil {
		return db.CheckpointBranch{}, ErrInvalid
	}
	entries := make([]db.TranscriptEntry, len(c.state.Records))
	for i, record := range c.state.Records {
		entries[i] = db.TranscriptEntry{
			Sequence: record.Sequence, EntryID: record.ID, ParentID: record.ParentID, TurnID: record.TurnID,
			Kind: record.Kind, Revision: record.Revision, PayloadJSON: bytes.Clone(record.Payload),
		}
	}
	var replaySuffix [][]byte
	if c.record.FormatVersion == HistoryFormatVersion {
		notice, err := runner.MarshalReplayMessage(runner.ReplayMessage{Message: llm.Message{Role: llm.RoleUser, Content: FilesUnchangedNotice}})
		if err != nil {
			return db.CheckpointBranch{}, ErrInvalid
		}
		replaySuffix = [][]byte{notice}
	}
	return db.CheckpointBranch{
		SourceSessionID: c.record.SessionID, Workspace: c.state.Workspace, ExpectedSourceRevision: sourceHead,
		CheckpointID: c.record.ID, CheckpointChecksum: c.record.Checksum,
		Child: db.SessionRecord{
			ID: childID, Workspace: c.state.Workspace, Label: label, Provider: c.state.Target.Provider, Model: c.state.Target.Model,
			ContextJSON: contextJSON, PendingJSON: pending, PlanJSON: planJSON, MessageCount: thread.MessageCount(),
		},
		Entries: entries, ToolCallIDs: slices.Clone(c.state.ToolCallIDs), ReplaySuffix: replaySuffix,
	}, nil
}

func validatePayload(state payload) error {
	// Canonical replay stores context with the runner's byte-preserving codec;
	// the remaining boundary metadata still uses ordinary JSON strings.
	metadata := state
	metadata.Context = nil
	if state.Boundary.HasAttachments && len(state.Boundary.Attachments) == 0 {
		return ErrAttachments
	}
	if state.Boundary.HasAttachments != (len(state.Boundary.Attachments) != 0) || draft.ValidateAttachments(state.Boundary.Attachments) != nil {
		return ErrInvalid
	}
	if state.Version != FormatVersion || state.ID == "" || state.SourceID == "" || state.Workspace == "" ||
		state.Boundary.PromptID == "" || len(state.Boundary.PromptText) > draft.MaxTextBytes ||
		state.Context == nil || state.Records == nil || state.Target.Provider == "" || state.Target.Model == "" ||
		state.Target.Window < 0 || state.Target.Reserve < 0 || !validStrings(reflect.ValueOf(metadata)) {
		return ErrInvalid
	}
	if !validBoundary(state.Revision, len(state.Records) == 0 && len(state.Context) == 0,
		state.Boundary.SettledTurnID, state.Boundary.EventWatermark, state.Boundary.InitialContinuation) {
		return ErrInvalid
	}
	for i, record := range state.Records {
		if record.Sequence != uint64(i)+1 || !validJSONText(record.Payload) {
			return ErrInvalid
		}
	}
	if _, err := transcript.CheckpointFromRecords(state.Revision, restoreRecords(state.Records)); err != nil {
		return ErrInvalid
	}
	for _, step := range state.Plan.Steps {
		if !step.Status.IsValid() {
			return ErrInvalid
		}
	}
	return validateTargetContext(state.Context, state.Target)
}

func clonePlan(plan code.Plan) code.Plan {
	plan.Steps = slices.Clone(plan.Steps)
	return plan
}
