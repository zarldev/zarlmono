package grpc

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"github.com/zarldev/zarlmono/zarlai/repository"
	"github.com/zarldev/zarlmono/zarlai/taskrunner"
	zarlv1 "github.com/zarldev/zarlmono/zarlai/transport/grpc/gen/zarl/v1"
	"github.com/zarldev/zarlmono/zkit/agent/profile"
)

// Subagent profiles — per-role toolsets + model defaults, plus the
// per-profile overrides stored in the DB.

func (s *AdminServer) ListProfiles(ctx context.Context, req *connect.Request[zarlv1.ListProfilesRequest]) (*connect.Response[zarlv1.ListProfilesResponse], error) {
	profiles, err := s.profileRegistry.List(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("list profiles: %w", err))
	}
	overrides, err := s.profileOverrides.List(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("list overrides: %w", err))
	}

	out := make([]*zarlv1.ProfileInfo, 0, len(profiles))
	for _, p := range profiles {
		gate, err := s.profileRegistry.GateFor(ctx, p.Name)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("gate for %q: %w", p.Name, err))
		}
		info := &zarlv1.ProfileInfo{
			Name:                 string(p.Name),
			ToolNames:            toolNamesForGate(gate),
			DefaultModel:         p.Model,
			DefaultPromptPrefix:  p.PromptPrefix,
			DefaultMaxIterations: int32(p.MaxIterations),
		}
		if o, ok := overrides[string(p.Name)]; ok {
			info.Override = repoOverrideToProto(o)
		}
		out = append(out, info)
	}
	return connect.NewResponse(&zarlv1.ListProfilesResponse{Profiles: out}), nil
}

func (s *AdminServer) GetProfileOverride(ctx context.Context, req *connect.Request[zarlv1.GetProfileOverrideRequest]) (*connect.Response[zarlv1.GetProfileOverrideResponse], error) {
	if err := s.validateProfileName(ctx, req.Msg.ProfileName); err != nil {
		return nil, err
	}
	o, err := s.profileOverrides.Get(ctx, req.Msg.ProfileName)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&zarlv1.GetProfileOverrideResponse{Override: repoOverrideToProto(o)}), nil
}

func (s *AdminServer) UpsertProfileOverride(ctx context.Context, req *connect.Request[zarlv1.UpsertProfileOverrideRequest]) (*connect.Response[zarlv1.UpsertProfileOverrideResponse], error) {
	if err := s.validateProfileName(ctx, req.Msg.ProfileName); err != nil {
		return nil, err
	}
	if err := s.validateMaxIterationsCap(ctx, req.Msg.ProfileName, req.Msg.Override); err != nil {
		return nil, err
	}

	override := protoOverrideToRepo(req.Msg.Override)
	if err := s.profileOverrides.Upsert(ctx, req.Msg.ProfileName, override); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	all, err := s.ListProfiles(ctx, connect.NewRequest(&zarlv1.ListProfilesRequest{}))
	if err != nil {
		return nil, err
	}
	for _, p := range all.Msg.Profiles {
		if p.Name == req.Msg.ProfileName {
			return connect.NewResponse(&zarlv1.UpsertProfileOverrideResponse{Profile: p}), nil
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, profile.ErrNotFound)
}

func (s *AdminServer) DeleteProfileOverride(ctx context.Context, req *connect.Request[zarlv1.DeleteProfileOverrideRequest]) (*connect.Response[zarlv1.DeleteProfileOverrideResponse], error) {
	if err := s.validateProfileName(ctx, req.Msg.ProfileName); err != nil {
		return nil, err
	}
	if err := s.profileOverrides.Delete(ctx, req.Msg.ProfileName); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&zarlv1.DeleteProfileOverrideResponse{}), nil
}

func (s *AdminServer) validateProfileName(ctx context.Context, name string) error {
	profiles, err := s.profileRegistry.List(ctx)
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	for _, p := range profiles {
		if string(p.Name) == name {
			return nil
		}
	}
	return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unknown profile %q", name))
}

func (s *AdminServer) validateMaxIterationsCap(ctx context.Context, name string, o *zarlv1.ProfileOverride) error {
	if o == nil || o.MaxIterations == nil {
		return nil
	}
	profiles, err := s.profileRegistry.List(ctx)
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("list profiles: %w", err))
	}
	for _, p := range profiles {
		if string(p.Name) == name && *o.MaxIterations > int32(p.MaxIterations) {
			return connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("max_iterations %d exceeds profile cap %d", *o.MaxIterations, p.MaxIterations))
		}
	}
	return nil
}

func toolNamesForGate(gate taskrunner.GateSpec) []string {
	names := make([]string, 0, len(gate.Tools))
	for _, n := range gate.Tools {
		names = append(names, string(n))
	}
	return names
}

func repoOverrideToProto(o repository.TaskProfileOverride) *zarlv1.ProfileOverride {
	return &zarlv1.ProfileOverride{
		Model:         o.Model,
		PromptPrefix:  o.PromptPrefix,
		MaxIterations: o.MaxIterations,
		ToolNames:     o.ToolNames,
	}
}

func protoOverrideToRepo(o *zarlv1.ProfileOverride) repository.TaskProfileOverride {
	if o == nil {
		return repository.TaskProfileOverride{}
	}
	return repository.TaskProfileOverride{
		Model:         o.Model,
		PromptPrefix:  o.PromptPrefix,
		MaxIterations: o.MaxIterations,
		ToolNames:     o.ToolNames,
	}
}
