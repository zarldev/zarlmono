package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zarldev/zarlmono/zkit/db/gen"
)

// MCPServerRow is one configured MCP server, persisted globally so the shell
// can auto-connect it at startup. The sqlite encoding (JSON args/env, integer
// bool) lives in the List/Upsert method bodies.
type MCPServerRow struct {
	Name      string
	Transport string // "stdio" | "http"
	Command   string
	Args      []string
	Env       map[string]string
	BaseURL   string
	AuthToken string
	Enabled   bool
}

// ListMCPServers returns all configured MCP servers.
func (s *Store) ListMCPServers(ctx context.Context) ([]MCPServerRow, error) {
	rows, err := s.q.ListMCPServers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list mcp servers: %w", err)
	}
	out := make([]MCPServerRow, 0, len(rows))
	for _, r := range rows {
		row := MCPServerRow{
			Name:      r.Name,
			Transport: r.Transport,
			Command:   r.Command,
			BaseURL:   r.BaseUrl,
			AuthToken: r.AuthToken,
			Enabled:   r.Enabled != 0,
		}
		if r.Args != "" {
			_ = json.Unmarshal([]byte(r.Args), &row.Args)
		}
		if r.Env != "" {
			_ = json.Unmarshal([]byte(r.Env), &row.Env)
		}
		out = append(out, row)
	}
	return out, nil
}

// UpsertMCPServer inserts or replaces an MCP server config by name.
func (s *Store) UpsertMCPServer(ctx context.Context, row MCPServerRow) error {
	argsJSON := "[]"
	if len(row.Args) > 0 {
		if b, err := json.Marshal(row.Args); err == nil {
			argsJSON = string(b)
		}
	}
	envJSON := "{}"
	if len(row.Env) > 0 {
		if b, err := json.Marshal(row.Env); err == nil {
			envJSON = string(b)
		}
	}
	now := time.Now().Unix()
	return s.q.UpsertMCPServer(ctx, gen.UpsertMCPServerParams{
		Name:      row.Name,
		Transport: row.Transport,
		Command:   row.Command,
		Args:      argsJSON,
		Env:       envJSON,
		BaseUrl:   row.BaseURL,
		AuthToken: row.AuthToken,
		Enabled:   boolToInt(row.Enabled),
		CreatedAt: now,
		UpdatedAt: now,
	})
}

// DeleteMCPServer removes an MCP server config by name.
func (s *Store) DeleteMCPServer(ctx context.Context, name string) error {
	return s.q.DeleteMCPServer(ctx, name)
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func headlessRowToRecord(r gen.HeadlessRun) HeadlessRunRecord {
	rec := HeadlessRunRecord{
		ID:             r.ID,
		Workspace:      r.Workspace,
		BaseCommit:     stringFromNull(r.BaseCommit),
		Prompt:         r.Prompt,
		StartedAt:      time.Unix(r.StartedAt, 0),
		TerminalReason: stringFromNull(r.TerminalReason),
		Error:          stringFromNull(r.Error),
		FinalContent:   stringFromNull(r.FinalContent),
		FinalDiff:      stringFromNull(r.FinalDiff),
		Iterations:     int(int64FromNull(r.Iterations)),
		ToolCalls:      int(int64FromNull(r.ToolCalls)),
		TokensIn:       int64PtrFromNull(r.TokensIn),
		TokensOut:      int64PtrFromNull(r.TokensOut),
		Duration:       time.Duration(int64FromNull(r.DurationMs)) * time.Millisecond,
		Escalated:      r.Escalated != 0,
		Provider:       r.Provider,
		Model:          r.Model,
	}
	if r.EndedAt.Valid {
		t := time.Unix(r.EndedAt.Int64, 0)
		rec.EndedAt = &t
	}
	return rec
}

func headlessAttemptRowToRecord(r gen.HeadlessAttempt) HeadlessAttemptRecord {
	return HeadlessAttemptRecord{
		RunID:          r.RunID,
		AttemptNumber:  int(r.AttemptNumber),
		Prompt:         r.Prompt,
		TerminalReason: stringFromNull(r.TerminalReason),
		Error:          stringFromNull(r.Error),
		FinalContent:   stringFromNull(r.FinalContent),
		Iterations:     int(int64FromNull(r.Iterations)),
		ToolCalls:      int(int64FromNull(r.ToolCalls)),
		TokensIn:       int64PtrFromNull(r.TokensIn),
		TokensOut:      int64PtrFromNull(r.TokensOut),
		DecisionDone:   r.DecisionDone != 0,
		Feedback:       stringFromNull(r.Feedback),
		RecordedAt:     time.Unix(r.RecordedAt, 0),
	}
}

func headlessVerifierResultRowToRecord(r gen.HeadlessVerifierResult) HeadlessVerifierResultRecord {
	return HeadlessVerifierResultRecord{
		RunID:         r.RunID,
		AttemptNumber: int(r.AttemptNumber),
		Command:       r.Command,
		Skipped:       r.Skipped != 0,
		Success:       r.Success != 0,
		ExitCode:      int64PtrFromNull(r.ExitCode),
		Error:         stringFromNull(r.Error),
		OutputTail:    stringFromNull(r.OutputTail),
		Duration:      time.Duration(r.DurationMs) * time.Millisecond,
		RecordedAt:    time.Unix(r.RecordedAt, 0),
	}
}
