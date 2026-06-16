package service

import (
	"maps"
	"strings"
	"sync"
	"sync/atomic"
)

// PromptTemplateStore resolves a template key to the current (possibly
// operator-edited) content, falling back to the code-default when the
// operator hasn't overridden. Intentionally narrow interface so every
// caller can accept a small dependency.
type PromptTemplateStore interface {
	// Render looks up key, substitutes {{placeholders}} from vars, and
	// returns the rendered string. Falls back to the code-default when
	// the operator hasn't overridden.
	Render(key string, vars map[string]string) string
	// Raw returns the current content for key (override if present,
	// else code default, else empty). Admin surfaces use this to show
	// what the operator would be editing.
	Raw(key string) string
	// Default returns the code default unchanged by any operator
	// override. Admin's "reset" button compares against this.
	Default(key string) string
}

// MemoryPromptTemplateStore holds code-defaults + operator overrides
// in memory. Overrides are populated at startup from the repo and on
// admin writes; defaults are registered by code owners so there is a
// single source of truth per key even when the DB is fresh.
type MemoryPromptTemplateStore struct {
	mu        sync.RWMutex
	defaults  map[string]string
	overrides map[string]string
	bumpers   []InvalidationBumper
	version   atomic.Int64
}

func NewMemoryPromptTemplateStore() *MemoryPromptTemplateStore {
	return &MemoryPromptTemplateStore{
		defaults:  make(map[string]string),
		overrides: make(map[string]string),
	}
}

// RegisterDefault records the code-default for a template key. Called
// by package-level init code in each module that owns a template.
// Re-registering the same key replaces the default (tests or hot
// reconfig paths use this).
func (s *MemoryPromptTemplateStore) RegisterDefault(key, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.defaults[key] = content
}

// AddBumper registers a downstream cache that needs invalidation when
// overrides change — e.g. anything that has pre-rendered a template
// and cached the result.
func (s *MemoryPromptTemplateStore) AddBumper(b InvalidationBumper) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bumpers = append(s.bumpers, b)
}

// LoadOverrides replaces the override set — called at startup after
// reading all rows from prompt_templates, and again after any admin
// write. Bumps downstream caches once.
func (s *MemoryPromptTemplateStore) LoadOverrides(entries map[string]string) {
	s.mu.Lock()
	s.overrides = make(map[string]string, len(entries))
	maps.Copy(s.overrides, entries)
	bumpers := append([]InvalidationBumper(nil), s.bumpers...)
	s.mu.Unlock()
	s.version.Add(1)
	for _, b := range bumpers {
		b.BumpVersion()
	}
}

// SetOverride upserts a single override and bumps. Used when the admin
// saves one template at a time without a full reload.
func (s *MemoryPromptTemplateStore) SetOverride(key, content string) {
	s.mu.Lock()
	s.overrides[key] = content
	bumpers := append([]InvalidationBumper(nil), s.bumpers...)
	s.mu.Unlock()
	s.version.Add(1)
	for _, b := range bumpers {
		b.BumpVersion()
	}
}

// ClearOverride removes an override so the code default takes effect
// again (admin "reset to default" button).
func (s *MemoryPromptTemplateStore) ClearOverride(key string) {
	s.mu.Lock()
	delete(s.overrides, key)
	bumpers := append([]InvalidationBumper(nil), s.bumpers...)
	s.mu.Unlock()
	s.version.Add(1)
	for _, b := range bumpers {
		b.BumpVersion()
	}
}

// Version returns the monotonic counter; downstream caches compare
// against their last-seen value to detect change.
func (s *MemoryPromptTemplateStore) Version() int64 { return s.version.Load() }

func (s *MemoryPromptTemplateStore) Raw(key string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if v, ok := s.overrides[key]; ok {
		return v
	}
	return s.defaults[key]
}

func (s *MemoryPromptTemplateStore) Default(key string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.defaults[key]
}

// HasOverride reports whether the operator has saved a non-default for
// this key. Admin list views use this to show the "edited" pill.
func (s *MemoryPromptTemplateStore) HasOverride(key string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.overrides[key]
	return ok
}

// AllKeys returns every known template key (union of default and
// override sets). Admin list view renders from this so operators see
// every template that exists in code even before they've edited it.
func (s *MemoryPromptTemplateStore) AllKeys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := make(map[string]bool, len(s.defaults)+len(s.overrides))
	out := make([]string, 0, len(s.defaults)+len(s.overrides))
	for k := range s.defaults {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	for k := range s.overrides {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// Render substitutes {{key}} placeholders in the template with the
// corresponding value from vars. Missing vars are replaced with empty
// string — callers that need strictness can check Raw(key) contents
// first. Simple mustache-style substitution; no nesting, no logic.
func (s *MemoryPromptTemplateStore) Render(key string, vars map[string]string) string {
	raw := s.Raw(key)
	if raw == "" || len(vars) == 0 {
		return raw
	}
	out := raw
	for k, v := range vars {
		out = strings.ReplaceAll(out, "{{"+k+"}}", v)
	}
	return out
}
