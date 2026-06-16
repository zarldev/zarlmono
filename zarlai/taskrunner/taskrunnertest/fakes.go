// Package taskrunnertest provides shared test fakes for taskrunner consumers.
package taskrunnertest

import (
	"context"

	"github.com/zarldev/zarlmono/zarlai/service"
	"github.com/zarldev/zarlmono/zarlai/taskrunner"
	"github.com/zarldev/zarlmono/zkit/agent/profile"
)

// FakeOverrideStore is an in-memory profile.OverrideStore for tests.
type FakeOverrideStore struct {
	Rows map[profile.Name]profile.Override
	Err  error // injected error, returned by Get when non-nil
}

// NewFakeOverrideStore constructs an empty store.
func NewFakeOverrideStore() *FakeOverrideStore {
	return &FakeOverrideStore{Rows: map[profile.Name]profile.Override{}}
}

// Get returns the override or zero-value + nil.
func (f *FakeOverrideStore) Get(ctx context.Context, name profile.Name) (profile.Override, error) {
	if f.Err != nil {
		return profile.Override{}, f.Err
	}
	return f.Rows[name], nil
}

// FakeProfileRegistry is a deterministic ProfileRegistry for Runner tests.
type FakeProfileRegistry struct {
	ByName map[profile.Name]taskrunner.ResolvedProfile
	Gates  map[profile.Name]taskrunner.GateSpec
	Err    error
}

func NewFakeProfileRegistry() *FakeProfileRegistry {
	return &FakeProfileRegistry{
		ByName: map[profile.Name]taskrunner.ResolvedProfile{},
		Gates:  taskrunner.BuiltinToolGates(),
	}
}

// Resolve returns the configured profile, falling back to the default
// like the real registry does.
func (f *FakeProfileRegistry) Resolve(ctx context.Context, name profile.Name) (taskrunner.ResolvedProfile, error) {
	if f.Err != nil {
		return taskrunner.ResolvedProfile{}, f.Err
	}
	if rp, ok := f.ByName[name]; ok {
		return rp, nil
	}
	if rp, ok := f.ByName[profile.NameDefault]; ok {
		return rp, nil
	}
	return taskrunner.ResolvedProfile{}, profile.ErrNotFound
}

// List returns the builtin profile set.
func (f *FakeProfileRegistry) List(ctx context.Context) ([]profile.Profile, error) {
	return profile.Builtin(), nil
}

// GateFor returns the configured gate spec for the profile.
func (f *FakeProfileRegistry) GateFor(ctx context.Context, name profile.Name) (taskrunner.GateSpec, error) {
	return f.Gates[name], nil
}

// FakeChatClientFactory records the model it was called with.
type FakeChatClientFactory struct {
	Calls  []string
	Client service.ChatClient
}

func (f *FakeChatClientFactory) Build(model string) service.ChatClient {
	f.Calls = append(f.Calls, model)
	return f.Client
}
