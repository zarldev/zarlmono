package service

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sort"
	"sync"

	"github.com/zarldev/zarlmono/zkit/skills"
)

// skillVector pairs a skill's stable ID with the embedding of its
// description. ID is stored (not name, not pointer) so rename-while-
// scoring or delete-while-scoring are harmless — the resolve step at
// the end queries the live store.
type skillVector struct {
	id             string
	profileBinding string
	embedding      []float32
}

// SkillSelector ranks enabled skills by cosine similarity against the
// user's current message / task prompt, filters by profile binding,
// and returns the markdown bodies to inject into the system prompt.
//
// Mirrors ToolSelector's shape — same index-version trick, same
// always-on concept (skills whose markdown ships on every turn).
type SkillSelector struct {
	source   skills.SkillSource
	embedder Embedder
	topK     int
	alwaysOn []string // skill names (stable human identifiers)

	mu           sync.Mutex
	indexVersion int64
	index        []skillVector
}

// SkillSelectorOption configures a SkillSelector at construction.
type SkillSelectorOption func(*SkillSelector)

// WithSkillSelectorTopK sets how many semantically-ranked skills to
// include per turn. Default: 3. Higher values raise recall at the cost
// of prompt tokens — keep modest; skills bodies are typically larger
// than tool descriptions.
func WithSkillSelectorTopK(k int) SkillSelectorOption {
	return func(s *SkillSelector) { s.topK = k }
}

// WithAlwaysOnSkills names skills that inject on every turn regardless
// of similarity ranking. Use sparingly — each one adds its full markdown
// to the prompt unconditionally.
func WithAlwaysOnSkills(names ...string) SkillSelectorOption {
	return func(s *SkillSelector) { s.alwaysOn = append(s.alwaysOn, names...) }
}

func NewSkillSelector(source skills.SkillSource, embedder Embedder, opts ...SkillSelectorOption) *SkillSelector {
	s := &SkillSelector{
		source:   source,
		embedder: embedder,
		topK:     3,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// EnsureIndex rebuilds the embedding index when the source has
// advanced past the indexed version. Cheap no-op otherwise.
func (s *SkillSelector) EnsureIndex(ctx context.Context) error {
	s.mu.Lock()
	if s.index != nil && s.indexVersion == s.source.Version() {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	enabled := s.source.EnabledSkills()
	targetVersion := s.source.Version()

	idx := make([]skillVector, 0, len(enabled))
	for _, sk := range enabled {
		text := sk.Name + ": " + sk.Description
		vec, err := s.embedder.Embed(ctx, text)
		if err != nil {
			return fmt.Errorf("embed skill %s: %w", sk.Name, err)
		}
		idx = append(idx, skillVector{id: sk.ID, profileBinding: sk.ProfileBinding, embedding: vec})
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.indexVersion > targetVersion {
		return nil
	}
	s.index = idx
	s.indexVersion = targetVersion
	return nil
}

// Select returns the skills most relevant to userMessage that apply to
// the given profile (global skills always pass the filter). The
// always-on set is unioned in regardless of similarity. When the
// embedder fails or no skills are enabled, returns the always-on set
// alone (embedder failures propagate).
func (s *SkillSelector) Select(ctx context.Context, profile, userMessage string) ([]skills.Skill, error) {
	if err := s.EnsureIndex(ctx); err != nil {
		return nil, fmt.Errorf("skill selector: %w", err)
	}

	s.mu.Lock()
	idx := s.index
	s.mu.Unlock()

	// Scoring pass: similarity within the profile-filtered subset.
	selectedIDs := make(map[string]bool, s.topK+len(s.alwaysOn))
	if len(idx) > 0 && userMessage != "" {
		query, err := s.embedder.Embed(ctx, userMessage)
		if err != nil {
			return nil, fmt.Errorf("skill selector: embed user message: %w", err)
		}
		type scored struct {
			id    string
			score float32
		}
		ranked := make([]scored, 0, len(idx))
		for _, sv := range idx {
			if !matchesProfile(sv.profileBinding, profile) {
				continue
			}
			ranked = append(ranked, scored{id: sv.id, score: CosineSimilarity(query, sv.embedding)})
		}
		sort.Slice(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
		for i := 0; i < len(ranked) && i < s.topK; i++ {
			selectedIDs[ranked[i].id] = true
		}
	}

	// Resolve IDs (and always-on by name) to current Skill records via
	// the live store — handles rename/delete-between-index-and-resolve
	// without returning stale data.
	live := s.source.EnabledSkills()
	byID := make(map[string]skills.Skill, len(live))
	byName := make(map[string]skills.Skill, len(live))
	for _, sk := range live {
		byID[sk.ID] = sk
		byName[sk.Name] = sk
	}

	// Deterministic order: identical `selectedIDs` sets must serialize
	// identically so llama-server's prefix cache stays valid. Map
	// iteration randomises order, which silently invalidates the
	// cached system prompt past the first divergent skill.
	out := make([]skills.Skill, 0, len(selectedIDs)+len(s.alwaysOn))
	seen := make(map[string]bool, len(selectedIDs)+len(s.alwaysOn))
	for _, id := range slices.Sorted(maps.Keys(selectedIDs)) {
		if sk, ok := byID[id]; ok && matchesProfile(sk.ProfileBinding, profile) && !seen[sk.ID] {
			out = append(out, sk)
			seen[sk.ID] = true
		}
	}
	for _, name := range s.alwaysOn {
		sk, ok := byName[name]
		if !ok {
			slog.Warn("always-on skill not enabled", "name", name)
			continue
		}
		if !matchesProfile(sk.ProfileBinding, profile) {
			continue
		}
		if !seen[sk.ID] {
			out = append(out, sk)
			seen[sk.ID] = true
		}
	}
	return out, nil
}

// matchesProfile is the binding-filter rule: a global skill (empty
// binding) applies to every profile; a bound skill only applies when
// its binding matches the active profile exactly.
func matchesProfile(binding, profile string) bool {
	if binding == "" {
		return true
	}
	return binding == profile
}
