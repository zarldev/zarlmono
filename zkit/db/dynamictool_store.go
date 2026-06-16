package db

import (
	"context"
	"fmt"
	"time"

	"github.com/zarldev/zarlmono/zkit/db/gen"
)

// DynamicToolRow is the persisted shape of one dynamic-tool registration.
// spec_json is the marshalled tools.ToolSpec (opaque to the store —
// the caller decodes it back into the runtime shape). binary_path is
// absolute on disk; the registry execs the binary on every tool call.
type DynamicToolRow struct {
	Name       string
	SpecJSON   []byte
	BinaryPath string
}

// ListDynamicTools returns the dynamic-tool registrations for a
// workspace, in stable name order. Empty workspace="" is the global
// slot — reserved for "shared across all workspaces" semantics
// later; nothing writes there today.
func (s *Store) ListDynamicTools(ctx context.Context, workspace string) ([]DynamicToolRow, error) {
	rows, err := s.q.ListDynamicTools(ctx, workspace)
	if err != nil {
		return nil, fmt.Errorf("list dynamic tools for %q: %w", workspace, err)
	}
	out := make([]DynamicToolRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, DynamicToolRow{
			Name:       r.Name,
			SpecJSON:   []byte(r.SpecJson),
			BinaryPath: r.BinaryPath,
		})
	}
	return out, nil
}

// UpsertDynamicTool writes one registration row, replacing in place
// on (workspace, name) conflict. Used by both fresh /new_tool
// registrations and the one-time manifest.json → sqlite migration
// importer.
func (s *Store) UpsertDynamicTool(ctx context.Context, workspace string, row DynamicToolRow) error {
	if err := s.q.UpsertDynamicTool(ctx, gen.UpsertDynamicToolParams{
		Workspace:  workspace,
		Name:       row.Name,
		SpecJson:   string(row.SpecJSON),
		BinaryPath: row.BinaryPath,
		UpdatedAt:  time.Now().Unix(),
	}); err != nil {
		return fmt.Errorf("upsert dynamic tool %q/%q: %w", workspace, row.Name, err)
	}
	return nil
}

// DeleteDynamicTool removes one registration row. No-op when absent.
func (s *Store) DeleteDynamicTool(ctx context.Context, workspace, name string) error {
	if err := s.q.DeleteDynamicTool(ctx, gen.DeleteDynamicToolParams{
		Workspace: workspace,
		Name:      name,
	}); err != nil {
		return fmt.Errorf("delete dynamic tool %q/%q: %w", workspace, name, err)
	}
	return nil
}
