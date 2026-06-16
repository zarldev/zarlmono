package tools

import (
	"context"
	"maps"
	"sync"
	"sync/atomic"
)

// DescriptionStore maps tool names to human-authored override descriptions.
// Lookup is hot-path (fires on every tool-spec build that goes to an LLM)
// so implementations MUST be in-memory; persistence is the caller's job
// — load entries at startup via Load, and update via Set/Delete.
//
// The zero-value lookup semantics: (description "", ok=false) means "no
// override — use the tool's code-default description.".
type DescriptionStore interface {
	Description(name ToolName) (description string, ok bool)
}

// InvalidationBumper is whatever signals downstream caches (e.g. a
// Registry's spec cache) that descriptions have changed and their derived
// state is stale.
type InvalidationBumper interface {
	BumpVersion()
}

// MemoryDescriptionStore is a thread-safe map of name→description
// overrides. Populated at startup via Load; mutated in place by admin
// writes which also trigger registered InvalidationBumpers so caches
// regenerate against the new text.
type MemoryDescriptionStore struct {
	mu       sync.RWMutex
	entries  map[ToolName]string
	bumpers  []InvalidationBumper
	revision atomic.Int64
}

// NewMemoryDescriptionStore creates an empty store. Seed with Load.
func NewMemoryDescriptionStore() *MemoryDescriptionStore {
	return &MemoryDescriptionStore{entries: make(map[ToolName]string)}
}

// AddBumper registers a cache that must be invalidated on any change.
// Safe to call multiple times; bump order matches add order.
func (s *MemoryDescriptionStore) AddBumper(b InvalidationBumper) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bumpers = append(s.bumpers, b)
}

// Load replaces the store contents with the given set — used at startup
// after a full repository read. Triggers one bump regardless of entry
// count (cheaper than one per row).
func (s *MemoryDescriptionStore) Load(entries map[ToolName]string) {
	s.mu.Lock()
	s.entries = make(map[ToolName]string, len(entries))
	maps.Copy(s.entries, entries)
	bumpers := append([]InvalidationBumper(nil), s.bumpers...)
	s.mu.Unlock()
	s.revision.Add(1)
	for _, b := range bumpers {
		b.BumpVersion()
	}
}

// Set records or replaces an override and notifies invalidation hooks.
func (s *MemoryDescriptionStore) Set(name ToolName, description string) {
	s.mu.Lock()
	s.entries[name] = description
	bumpers := append([]InvalidationBumper(nil), s.bumpers...)
	s.mu.Unlock()
	s.revision.Add(1)
	for _, b := range bumpers {
		b.BumpVersion()
	}
}

// Delete removes an override; subsequent Description lookups return
// ok=false and the caller falls back to the code default. No-op (no
// bump) when the name has no entry to remove.
func (s *MemoryDescriptionStore) Delete(name ToolName) {
	s.mu.Lock()
	_, present := s.entries[name]
	if present {
		delete(s.entries, name)
	}
	bumpers := append([]InvalidationBumper(nil), s.bumpers...)
	s.mu.Unlock()
	if !present {
		return
	}
	s.revision.Add(1)
	for _, b := range bumpers {
		b.BumpVersion()
	}
}

// Description satisfies DescriptionStore.
func (s *MemoryDescriptionStore) Description(name ToolName) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.entries[name]
	return d, ok
}

// Revision returns a monotonically-increasing counter that bumps on any
// mutation. Useful for tests or observers detecting "did anything change
// since I last looked?" without holding the mutex or rescanning the map.
func (s *MemoryDescriptionStore) Revision() int64 { return s.revision.Load() }

// overriddenTool wraps a Tool so its Definition()'s Description is
// replaced by the store's current override each time Definition() is
// called. Looking up *per call* (rather than freezing at wrap time) is
// what makes live admin edits take effect without re-wiring — the next
// LLM turn that builds the tool spec sees the new description
// immediately. Execute is forwarded verbatim; only the LLM-facing text
// changes.
type overriddenTool struct {
	inner Tool
	store DescriptionStore
	name  ToolName // cached so Definition() doesn't re-resolve from inner
}

// Definition returns the underlying tool's spec with the override applied
// when present.
func (o overriddenTool) Definition() ToolSpec {
	def := o.inner.Definition()
	if desc, ok := o.store.Description(o.name); ok {
		def.Description = desc
	}
	return def
}

// Execute forwards to the underlying tool unchanged.
func (o overriddenTool) Execute(ctx context.Context, call ToolCall) (*ToolResult, error) {
	return o.inner.Execute(ctx, call)
}

// applyDescriptionOverride returns a live-looking-up wrapper around t.
// No override is required to exist *now* — the wrapper consults the
// store on every Definition() call, so a description added via the
// admin UI after wrap time takes effect on the next tool-spec build.
func applyDescriptionOverride(t Tool, store DescriptionStore) Tool {
	if t == nil || store == nil {
		return t
	}
	return overriddenTool{inner: t, store: store, name: t.Definition().Name}
}

// WrapDescriptionOverrides returns a new slice where each tool is wrapped
// with a live-looking-up override wrapper. Use this for tool collections
// that aren't held by a Registry (e.g. a per-call slice assembled by a
// caller that doesn't own a Registry) — Registry has its own
// SetDescriptionStore for the in-registry case.
func WrapDescriptionOverrides(tools []Tool, store DescriptionStore) []Tool {
	if store == nil || len(tools) == 0 {
		return tools
	}
	out := make([]Tool, len(tools))
	for i, t := range tools {
		out[i] = applyDescriptionOverride(t, store)
	}
	return out
}

// UnwrapDescriptionOverride returns the original tool if t is an override
// wrapper, else t itself. Admin surfaces use this to read a tool's
// code-default description without going through the override.
func UnwrapDescriptionOverride(t Tool) Tool {
	if o, ok := t.(overriddenTool); ok {
		return o.inner
	}
	return t
}
