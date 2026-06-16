package grpc

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/zarldev/zarlmono/zarlai/repository"
	zarlv1 "github.com/zarldev/zarlmono/zarlai/transport/grpc/gen/zarl/v1"
)

var workspaceNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

func (s *AdminServer) ListWorkspaces(ctx context.Context, req *connect.Request[zarlv1.ListWorkspacesRequest]) (*connect.Response[zarlv1.ListWorkspacesResponse], error) {
	if s.workspaces == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("workspaces repository not configured"))
	}
	rows, err := s.workspaces.List(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("list workspaces: %w", err))
	}
	out := make([]*zarlv1.WorkspaceInfo, 0, len(rows))
	for _, w := range rows {
		out = append(out, workspaceToProto(w))
	}
	return connect.NewResponse(&zarlv1.ListWorkspacesResponse{Workspaces: out}), nil
}

func (s *AdminServer) UpsertWorkspace(ctx context.Context, req *connect.Request[zarlv1.UpsertWorkspaceRequest]) (*connect.Response[zarlv1.UpsertWorkspaceResponse], error) {
	m := req.Msg
	if !workspaceNameRe.MatchString(m.Name) {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid workspace name %q (must match %s)", m.Name, workspaceNameRe))
	}
	if m.Root == "" || !filepath.IsAbs(m.Root) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("root must be a non-empty absolute path"))
	}
	if s.workspaces == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("workspaces repository not configured"))
	}
	// Materialise the root so greenfield workspaces (non-existent path at
	// upsert time) are ready for the coder's bash/write tools on the next
	// task. MkdirAll is a no-op when the path already exists as a directory;
	// a permission failure or collision with an existing file surfaces as
	// InvalidArgument so the operator can fix the path before saving.
	if err := os.MkdirAll(m.Root, 0o755); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("create workspace root %q: %w", m.Root, err))
	}
	w := repository.Workspace{
		Name:          m.Name,
		Root:          m.Root,
		DefaultBranch: m.DefaultBranch,
		Description:   m.Description,
	}
	if err := s.workspaces.Upsert(ctx, w); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	stored, err := s.workspaces.Get(ctx, m.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&zarlv1.UpsertWorkspaceResponse{Workspace: workspaceToProto(stored)}), nil
}

func (s *AdminServer) DeleteWorkspace(ctx context.Context, req *connect.Request[zarlv1.DeleteWorkspaceRequest]) (*connect.Response[zarlv1.DeleteWorkspaceResponse], error) {
	if s.workspaces == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("workspaces repository not configured"))
	}
	err := s.workspaces.Delete(ctx, req.Msg.Name)
	if errors.Is(err, repository.ErrDefaultWorkspaceProtected) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&zarlv1.DeleteWorkspaceResponse{}), nil
}

func workspaceToProto(w repository.Workspace) *zarlv1.WorkspaceInfo {
	rootExists := false
	if st, err := os.Stat(w.Root); err == nil && st.IsDir() {
		rootExists = true
	}
	return &zarlv1.WorkspaceInfo{
		Name:          w.Name,
		Root:          w.Root,
		DefaultBranch: w.DefaultBranch,
		Description:   w.Description,
		RootExists:    rootExists,
		UpdatedAt:     timestamppb.New(w.UpdatedAt),
	}
}
