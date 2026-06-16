// Package skills is a versioned, hot-reloadable cache of agent
// capabilities ("skills"). Originally the runner-substrate's
// SkillSource/MemorySkillStore, lifted out so consumers can manage
// skill content (markdown bodies, profile bindings) independent of
// any specific runner implementation. Bump the version to invalidate
// downstream caches that key off skill content.
package skills

import (
	"slices"
	"sync"
	"sync/atomic"
)

// Skill is a capability guide that gets injected into the LLM's system
// prompt when relevant. Slim on purpose — persistence is the caller's
// concern (a repository owns the full row with timestamps, enabled
// flags, audit metadata); this struct holds only what the runner uses
// for prompt construction.
type Skill struct {
	ID             string
	Name           string
	Description    string
	Markdown       string
	ProfileBinding string // empty = global (any profile)
}

// SkillSource reads the current set of enabled skills. Version
// increments on any change so consumers (caches, runners) can detect
// staleness without rescanning.
type SkillSource interface {
	EnabledSkills() []Skill
	Version() int64
}

// InvalidationBumper signals downstream caches that the skill set
// changed and any pre-rendered output is stale. Structurally identical
// to the same interface in zkit/ai/tools and zkit/ai/prompts —
// duplicated here so each package declares the contract it consumes
// (Go's structural typing means the same concrete bumper satisfies
// all three).
type InvalidationBumper interface {
	BumpVersion()
}

// MemorySkillStore is the in-memory cache backing SkillSource. Loaded
// from a repository at startup; admin writes replace the cache via
// Load and bump the version.
type MemorySkillStore struct {
	mu      sync.RWMutex
	skills  []Skill
	version atomic.Int64
	bumpers []InvalidationBumper
}

// NewMemorySkillStore creates an empty store. Call Load to populate.
func NewMemorySkillStore() *MemorySkillStore {
	return &MemorySkillStore{}
}

// AddBumper registers a cache that must be invalidated on any skill
// set change. Safe to call multiple times.
func (s *MemorySkillStore) AddBumper(b InvalidationBumper) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bumpers = append(s.bumpers, b)
}

// Load replaces the store contents with a fresh snapshot. Called on
// startup after repo load and again on each admin write — the admin
// handler re-reads from the repo and calls Load with the new set.
// Triggers exactly one bump regardless of skill count. Bumpers fire
// after the lock is released so a slow bumper doesn't block other
// readers/writers.
func (s *MemorySkillStore) Load(skills []Skill) {
	s.mu.Lock()
	s.skills = slices.Clone(skills)
	bumpers := slices.Clone(s.bumpers)
	s.mu.Unlock()
	s.version.Add(1)
	for _, b := range bumpers {
		b.BumpVersion()
	}
}

// EnabledSkills returns a copy of the current skill set. Callers
// iterate freely without holding the store's lock.
func (s *MemorySkillStore) EnabledSkills() []Skill {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.skills)
}

// Version returns the monotonically-increasing cache version.
func (s *MemorySkillStore) Version() int64 { return s.version.Load() }

// Compile-time interface satisfaction check.
var _ SkillSource = (*MemorySkillStore)(nil)
