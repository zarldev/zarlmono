package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zarlai/repository/gen"
)

// SensorKind discriminates which runtime class materializes an approved
// proposal. Kept as string constants rather than an enum to match the
// schema's VARCHAR(32) storage.
const (
	SensorKindPoll            = "poll"
	SensorKindHassState       = "hass_state"
	SensorKindMcpNotification = "mcp_notification"
)

// SensorProposal is the in-memory shape of a row in sensor_proposals.
//
// Different kinds populate different subsets of the optional fields:
//   - poll             → ToolName, ToolArgs, IntervalSeconds
//   - hass_state       → EntityID
//   - mcp_notification → ToolName (as provider), EntityID (as JSON-RPC method)
//
// ToolArgs is map[string]any because repository can't import service
// (service -> repository via face recognition). A tiny adapter in main.go
// bridges to service.Arguments at the taskrunner boundary.
type SensorProposal struct {
	ID              string
	Kind            string
	ToolName        string
	ToolArgs        map[string]any
	IntervalSeconds int
	EntityID        string
	Rationale       string
	Status          string // pending, approved, rejected
	CreatedAt       string
}

type SensorProposalRepo struct {
	q *gen.Queries
}

func NewSensorProposalRepo(q *gen.Queries) *SensorProposalRepo {
	return &SensorProposalRepo{q: q}
}

// CreatePoll inserts a poll-kind proposal: the agent wants ToolName to be
// invoked every IntervalSeconds with ToolArgs and the result broadcast on
// change.
func (r *SensorProposalRepo) CreatePoll(ctx context.Context, toolName string, args map[string]any, intervalSeconds int, rationale string) (SensorProposal, error) {
	id := uuid.New().String()
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return SensorProposal{}, fmt.Errorf("marshal args: %w", err)
	}
	err = r.q.InsertSensorProposal(ctx, gen.InsertSensorProposalParams{
		ID:              id,
		Kind:            SensorKindPoll,
		ToolName:        sql.NullString{String: toolName, Valid: true},
		ToolArgs:        argsJSON,
		IntervalSeconds: sql.NullInt32{Int32: int32(intervalSeconds), Valid: true},
		Rationale:       rationale,
	})
	if err != nil {
		return SensorProposal{}, fmt.Errorf("insert sensor proposal (poll): %w", err)
	}
	return SensorProposal{
		ID:              id,
		Kind:            SensorKindPoll,
		ToolName:        toolName,
		ToolArgs:        args,
		IntervalSeconds: intervalSeconds,
		Rationale:       rationale,
		Status:          "pending",
	}, nil
}

// CreateHassState inserts a hass_state-kind proposal: a reactive sensor
// that watches a single Home Assistant entity and broadcasts on state
// change. No polling; no tool invocation.
func (r *SensorProposalRepo) CreateHassState(ctx context.Context, entityID, rationale string) (SensorProposal, error) {
	id := uuid.New().String()
	err := r.q.InsertSensorProposal(ctx, gen.InsertSensorProposalParams{
		ID:        id,
		Kind:      SensorKindHassState,
		EntityID:  sql.NullString{String: entityID, Valid: true},
		Rationale: rationale,
	})
	if err != nil {
		return SensorProposal{}, fmt.Errorf("insert sensor proposal (hass_state): %w", err)
	}
	return SensorProposal{
		ID:        id,
		Kind:      SensorKindHassState,
		EntityID:  entityID,
		Rationale: rationale,
		Status:    "pending",
	}, nil
}

// CreateMcpNotification inserts an mcp_notification-kind proposal: a
// reactive sensor that subscribes to a JSON-RPC notification method on an
// MCP provider. ToolName stores the provider name; EntityID stores the
// method (repurposed columns documented in the migration).
func (r *SensorProposalRepo) CreateMcpNotification(ctx context.Context, provider, method, rationale string) (SensorProposal, error) {
	id := uuid.New().String()
	err := r.q.InsertSensorProposal(ctx, gen.InsertSensorProposalParams{
		ID:        id,
		Kind:      SensorKindMcpNotification,
		ToolName:  sql.NullString{String: provider, Valid: true},
		EntityID:  sql.NullString{String: method, Valid: true},
		Rationale: rationale,
	})
	if err != nil {
		return SensorProposal{}, fmt.Errorf("insert sensor proposal (mcp_notification): %w", err)
	}
	return SensorProposal{
		ID:        id,
		Kind:      SensorKindMcpNotification,
		ToolName:  provider,
		EntityID:  method,
		Rationale: rationale,
		Status:    "pending",
	}, nil
}

func (r *SensorProposalRepo) List(ctx context.Context) ([]SensorProposal, error) {
	rows, err := r.q.ListSensorProposals(ctx)
	if err != nil {
		return nil, fmt.Errorf("list sensor proposals: %w", err)
	}
	out := make([]SensorProposal, 0, len(rows))
	for _, row := range rows {
		p, err := listRowToProposal(row)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// ListApproved returns every proposal marked approved — used at startup to
// restore every active agent-proposed sensor.
func (r *SensorProposalRepo) ListApproved(ctx context.Context) ([]SensorProposal, error) {
	rows, err := r.q.ListApprovedSensorProposals(ctx)
	if err != nil {
		return nil, fmt.Errorf("list approved sensor proposals: %w", err)
	}
	out := make([]SensorProposal, 0, len(rows))
	for _, row := range rows {
		p, err := approvedRowToProposal(row)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (r *SensorProposalRepo) Get(ctx context.Context, id string) (SensorProposal, error) {
	row, err := r.q.GetSensorProposal(ctx, id)
	if err != nil {
		return SensorProposal{}, fmt.Errorf("get sensor proposal: %w", err)
	}
	return getRowToProposal(row)
}

func (r *SensorProposalRepo) SetStatus(ctx context.Context, id, status string) error {
	return r.q.UpdateSensorProposalStatus(ctx, gen.UpdateSensorProposalStatusParams{Status: status, ID: id})
}

func (r *SensorProposalRepo) CountPending(ctx context.Context) (int64, error) {
	return r.q.CountPendingSensorProposals(ctx)
}

// proposalRow is the shape every sqlc query above projects. Keeping
// the converters route through a struct (rather than 9 positional
// args) means new columns can be added without touching three
// signatures.
type proposalRow struct {
	ID              string
	Kind            string
	ToolName        sql.NullString
	ToolArgs        []byte
	IntervalSeconds sql.NullInt32
	EntityID        sql.NullString
	Rationale       string
	Status          string
	CreatedAt       string
}

// Converters for the three distinct row types sqlc emits (one per query
// shape). They share the same projected columns so the body is
// identical — kept as separate functions because Go's type system can't
// unify the row structs without generics we don't need here.

func listRowToProposal(row gen.ListSensorProposalsRow) (SensorProposal, error) {
	return buildProposal(proposalRow{
		ID: row.ID, Kind: row.Kind,
		ToolName: row.ToolName, ToolArgs: row.ToolArgs,
		IntervalSeconds: row.IntervalSeconds, EntityID: row.EntityID,
		Rationale: row.Rationale, Status: row.Status,
		CreatedAt: row.CreatedAt.Format(timeFormat),
	})
}

func approvedRowToProposal(row gen.ListApprovedSensorProposalsRow) (SensorProposal, error) {
	return buildProposal(proposalRow{
		ID: row.ID, Kind: row.Kind,
		ToolName: row.ToolName, ToolArgs: row.ToolArgs,
		IntervalSeconds: row.IntervalSeconds, EntityID: row.EntityID,
		Rationale: row.Rationale, Status: row.Status,
		CreatedAt: row.CreatedAt.Format(timeFormat),
	})
}

func getRowToProposal(row gen.GetSensorProposalRow) (SensorProposal, error) {
	return buildProposal(proposalRow{
		ID: row.ID, Kind: row.Kind,
		ToolName: row.ToolName, ToolArgs: row.ToolArgs,
		IntervalSeconds: row.IntervalSeconds, EntityID: row.EntityID,
		Rationale: row.Rationale, Status: row.Status,
		CreatedAt: row.CreatedAt.Format(timeFormat),
	})
}

func buildProposal(row proposalRow) (SensorProposal, error) {
	var args map[string]any
	if len(row.ToolArgs) > 0 {
		if err := json.Unmarshal(row.ToolArgs, &args); err != nil {
			return SensorProposal{}, fmt.Errorf("unmarshal args for %s: %w", row.ID, err)
		}
	}
	return SensorProposal{
		ID:              row.ID,
		Kind:            row.Kind,
		ToolName:        row.ToolName.String,
		ToolArgs:        args,
		IntervalSeconds: int(row.IntervalSeconds.Int32),
		EntityID:        row.EntityID.String,
		Rationale:       row.Rationale,
		Status:          row.Status,
		CreatedAt:       row.CreatedAt,
	}, nil
}
