package grpc_test

import (
	"context"
	"errors"
	"maps"
	"testing"

	"connectrpc.com/connect"
	"github.com/zarldev/zarlmono/zarlai/repository"
	"github.com/zarldev/zarlmono/zarlai/taskrunner/taskrunnertest"
	transportgrpc "github.com/zarldev/zarlmono/zarlai/transport/grpc"
	zarlv1 "github.com/zarldev/zarlmono/zarlai/transport/grpc/gen/zarl/v1"
	"github.com/zarldev/zarlmono/zkit/agent/profile"
)

// fakeProfileOverrideRepo is a minimal in-memory TaskProfileOverrideRepo substitute
// for tests that only exercise the profile endpoints.
type fakeProfileOverrideRepo struct {
	rows map[string]repository.TaskProfileOverride
}

func newFakeProfileOverrideRepo() *fakeProfileOverrideRepo {
	return &fakeProfileOverrideRepo{rows: map[string]repository.TaskProfileOverride{}}
}

func (f *fakeProfileOverrideRepo) Get(_ context.Context, profile string) (repository.TaskProfileOverride, error) {
	return f.rows[profile], nil
}

func (f *fakeProfileOverrideRepo) Upsert(_ context.Context, profile string, o repository.TaskProfileOverride) error {
	f.rows[profile] = o
	return nil
}

func (f *fakeProfileOverrideRepo) Delete(_ context.Context, profile string) error {
	delete(f.rows, profile)
	return nil
}

func (f *fakeProfileOverrideRepo) List(_ context.Context) (map[string]repository.TaskProfileOverride, error) {
	out := make(map[string]repository.TaskProfileOverride, len(f.rows))
	maps.Copy(out, f.rows)
	return out, nil
}

// adminForProfiles builds an AdminServer with only the profile fields populated.
func adminForProfiles(t *testing.T) *transportgrpc.AdminServer {
	t.Helper()
	registry := taskrunnertest.NewFakeProfileRegistry()
	overrides := newFakeProfileOverrideRepo()
	return transportgrpc.NewAdminServer(transportgrpc.AdminConfig{
		ProfileRegistry:  registry,
		ProfileOverrides: overrides,
	})
}

func TestListProfiles_returns_builtin_profiles(t *testing.T) {
	srv := adminForProfiles(t)
	resp, err := srv.ListProfiles(t.Context(), connect.NewRequest(&zarlv1.ListProfilesRequest{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len(resp.Msg.Profiles); got != 3 {
		t.Errorf("expected 3 profiles, got %d", got)
	}
	names := map[string]bool{}
	for _, p := range resp.Msg.Profiles {
		names[p.Name] = true
	}
	for _, want := range []string{"default", "researcher", "coder"} {
		if !names[want] {
			t.Errorf("missing profile %q in response", want)
		}
	}
}

func TestUpsertProfileOverride_rejects_unknown_profile(t *testing.T) {
	srv := adminForProfiles(t)
	_, err := srv.UpsertProfileOverride(t.Context(), connect.NewRequest(&zarlv1.UpsertProfileOverrideRequest{
		ProfileName: "nonexistent",
		Override:    &zarlv1.ProfileOverride{},
	}))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var connectErr *connect.Error
	if ok := asConnectError(err, &connectErr); !ok {
		t.Fatalf("expected connect error, got %T: %v", err, err)
	}
	if connectErr.Code() != connect.CodeInvalidArgument {
		t.Errorf("expected CodeInvalidArgument, got %v", connectErr.Code())
	}
}

func TestUpsertProfileOverride_rejects_max_iterations_over_cap(t *testing.T) {
	srv := adminForProfiles(t)
	// "researcher" has DefaultMaxIterations=20; try to set 99.
	over := int32(99)
	_, err := srv.UpsertProfileOverride(t.Context(), connect.NewRequest(&zarlv1.UpsertProfileOverrideRequest{
		ProfileName: string(profile.NameResearcher),
		Override: &zarlv1.ProfileOverride{
			MaxIterations: &over,
		},
	}))
	if err == nil {
		t.Fatal("expected error for over-cap max_iterations, got nil")
	}
	var connectErr *connect.Error
	if ok := asConnectError(err, &connectErr); !ok {
		t.Fatalf("expected connect error, got %T: %v", err, err)
	}
	if connectErr.Code() != connect.CodeInvalidArgument {
		t.Errorf("expected CodeInvalidArgument, got %v", connectErr.Code())
	}
}

func asConnectError(err error, target **connect.Error) bool {
	return errors.As(err, target)
}
