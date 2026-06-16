package service

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"math"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

// toolVector pairs a tool name with the embedding of its description
// plus the distinctive trigger keywords extracted from that
// description. Name is stored rather than *Tool so the selector
// never holds a reference that goes stale when the registry
// unregisters the tool mid-session — lookup happens through
// Registry.Get at Select time.
//
// triggers is a small set of whole-word keywords pulled from the
// tool's name and the imperative opening of its description — used
// for keyword-match selection, which runs alongside the embedding
// rank to catch cases where the user's phrasing literally echoes the
// tool's description but the embedding similarity falls just outside
// topN (common with small embedding models on close-scoring lists).
type toolVector struct {
	name      string
	embedding []float32
	triggers  []string
}

// ToolSelector ranks registered tools by cosine similarity of the user's
// message embedding against each tool's description embedding. The index
// is built lazily and rebuilt when the Registry version changes.
type ToolSelector struct {
	registry *tools.Registry
	embedder Embedder
	topN     int
	alwaysOn []string

	mu           sync.Mutex
	indexVersion int
	index        []toolVector
}

// ToolSelectorOption configures a ToolSelector at construction.
type ToolSelectorOption func(*ToolSelector)

// WithToolSelectorTopN sets the number of top-ranked tools to include.
// Default: 15. Lower values tighten prompt budget; higher values trade
// tokens for recall on ambiguous turns.
func WithToolSelectorTopN(n int) ToolSelectorOption {
	return func(s *ToolSelector) { s.topN = n }
}

// WithAlwaysOnTools names tools that ship on every Chat call regardless
// of similarity ranking — used for low-cost, high-utility capabilities
// (time, gesture, chart rendering) whose descriptions may not match user
// keywords but whose presence is essential for baseline behaviour.
func WithAlwaysOnTools(names ...string) ToolSelectorOption {
	return func(s *ToolSelector) { s.alwaysOn = append(s.alwaysOn, names...) }
}

// NewToolSelector constructs a selector that uses embedder to index
// registered tools. The index is not built until EnsureIndex or Select
// is first called, so startup stays cheap.
func NewToolSelector(registry *tools.Registry, embedder Embedder, opts ...ToolSelectorOption) *ToolSelector {
	s := &ToolSelector{
		registry: registry,
		embedder: embedder,
		topN:     15,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// EnsureIndex builds or rebuilds the tool embedding index if the registry
// has changed since the last build. Cheap when the registry is stable.
//
// The mutex is held only for the fast-path check and the final swap so that
// concurrent callers do not block during the (potentially slow) HTTP embedding
// calls. Two goroutines may race through the slow path; the one that finishes
// last discards its result if a newer version was already stamped.
func (s *ToolSelector) EnsureIndex(ctx context.Context) error {
	s.mu.Lock()
	if s.index != nil && s.indexVersion == s.registry.Version() {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	// Snapshot + target version taken back-to-back so the stamp below
	// matches the slice that was actually embedded. A concurrent Register
	// between these two reads produces a targetVersion newer than the
	// snapshot, and the final version check below discards the stale build.
	specs := s.registry.ToolSpecs()
	targetVersion := s.registry.Version()

	idx := make([]toolVector, 0, len(specs))
	for _, spec := range specs {
		name := spec.Name.String()
		text := name + ": " + spec.Description
		vec, err := s.embedder.Embed(ctx, text)
		if err != nil {
			return fmt.Errorf("embed %s: %w", name, err)
		}
		idx = append(idx, toolVector{
			name:      name,
			embedding: vec,
			triggers:  extractTriggers(name, spec.Description),
		})
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// Another concurrent builder may have stamped a newer version — leave
	// theirs in place rather than overwriting with our older snapshot.
	if s.indexVersion > targetVersion {
		return nil
	}
	s.index = idx
	s.indexVersion = targetVersion
	return nil
}

// Select returns the tool specs most relevant to userMessage plus the
// configured always-on set, de-duplicated. Never returns more than
// topN + len(alwaysOn) specs. When the embedder fails or no tools are
// registered, returns the always-on set alone (with nil error only for
// empty-registry — embedder failures propagate).
func (s *ToolSelector) Select(ctx context.Context, userMessage string) ([]llm.Tool, error) {
	if err := s.EnsureIndex(ctx); err != nil {
		return nil, fmt.Errorf("tool selector: %w", err)
	}

	selected := make(map[string]bool, s.topN+len(s.alwaysOn))
	for _, name := range s.alwaysOn {
		selected[name] = true
	}

	s.mu.Lock()
	idx := s.index
	s.mu.Unlock()

	if len(idx) > 0 && userMessage != "" {
		// Keyword-match pass. Tools whose distinctive trigger words
		// literally appear in the user message bypass the topN
		// cutoff. Catches the class of miss where the user says
		// "kick off a research task" and start_task's description
		// starts with "Kick off an autonomous background research
		// task" — literal match, but nomic-embed-text ranks it just
		// outside topN because several other tools score similarly
		// well. Embedding is fuzzy; literal word presence isn't.
		lowerMsg := strings.ToLower(userMessage)
		for _, tv := range idx {
			for _, trig := range tv.triggers {
				if wholeWordContains(lowerMsg, trig) {
					selected[tv.name] = true
					break
				}
			}
		}

		// Semantic rank pass fills any remaining topN slots.
		query, err := s.embedder.Embed(ctx, userMessage)
		if err != nil {
			return nil, fmt.Errorf("tool selector: embed user message: %w", err)
		}

		type scored struct {
			name  string
			score float32
		}
		ranked := make([]scored, 0, len(idx))
		for _, tv := range idx {
			ranked = append(ranked, scored{name: tv.name, score: CosineSimilarity(query, tv.embedding)})
		}
		sort.Slice(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })

		for i := 0; i < len(ranked) && i < s.topN; i++ {
			selected[ranked[i].name] = true
		}
	}

	// Deterministic order: identical `selected` sets across turns must
	// serialize identically so llama-server's prefix cache stays valid.
	// Map iteration randomises order, which silently invalidates the
	// cached system prompt past the first divergent tool.
	// Build the effective specs once (description overrides applied by the
	// registry) and index by name so the selected set resolves cheaply.
	byName := make(map[string]tools.ToolSpec)
	for _, sp := range s.registry.ToolSpecs() {
		byName[sp.Name.String()] = sp
	}

	out := make([]llm.Tool, 0, len(selected))
	for _, name := range slices.Sorted(maps.Keys(selected)) {
		sp, ok := byName[name]
		if !ok {
			// Silent skip for TOCTOU — tool unregistered between index build
			// and now is fine. But an always-on name that's never in the
			// registry is almost certainly a configuration mistake, so log
			// once so operators notice.
			if slices.Contains(s.alwaysOn, name) {
				slog.Warn("always-on tool not registered", "name", name)
			}
			continue
		}
		out = append(out, LLMToolFromSpec(sp))
	}
	return out, nil
}

// triggerStopwords is the set of words too common to be useful as a
// tool-identifying keyword. Removing them prevents a tool whose
// description opens with "Use this to…" from matching every turn that
// says "use".
var triggerStopwords = map[string]bool{
	// Articles, pronouns, copulas, common fillers.
	"a": true, "an": true, "the": true, "this": true, "that": true,
	"you": true, "your": true, "my": true, "me": true, "i": true,
	"is": true, "are": true, "was": true, "be": true, "been": true,
	"to": true, "for": true, "of": true, "in": true, "on": true,
	"with": true, "from": true, "at": true, "by": true, "as": true,
	// Generic imperative verbs that appear in half the tool
	// descriptions ("use", "call", "returns"). Filtered so they don't
	// trigger on every "use the X" kind of user phrasing.
	"use":     true,
	"call":    true,
	"run":     true,
	"get":     true,
	"set":     true,
	"make":    true,
	"do":      true,
	"go":      true,
	"return":  true,
	"returns": true,
	"and":     true,
	"or":      true,
	"when":    true,
	"if":      true,
	"then":    true,
	"it":      true,
	"its":     true,
	"not":     true,
	"no":      true,
	"yes":     true,
	"any":     true,
	"all":     true,
	"can":     true,
	"will":    true,
	"would":   true,
	"should":  true,
	"tool":    true, // nearly every description says "this tool"
	"tools":   true,
}

// extractTriggers pulls distinctive whole-word keywords from a tool's
// name and the imperative opening of its description. These are used
// for keyword-match selection alongside embedding similarity. Scoped
// narrow on purpose: the name (split on `_`) plus the first-sentence
// words of the description, minus stopwords. Longer-than-3-char only
// so "a", "to", "on" don't count.
func extractTriggers(name, description string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, 8)
	add := func(w string) {
		w = strings.ToLower(strings.TrimSpace(w))
		if len(w) < 4 || triggerStopwords[w] || seen[w] {
			return
		}
		seen[w] = true
		out = append(out, w)
	}
	// Tool name: snake_case segments. `start_task` → "start", "task".
	for seg := range strings.SplitSeq(name, "_") {
		add(seg)
	}
	// First sentence of the description (up to the first period /
	// newline). The imperative lead is where trigger verbs live
	// ("Kick off", "Search the live web"). Deeper paragraphs tend to
	// describe edge cases that match too broadly.
	lead := description
	for _, term := range []string{". ", "\n"} {
		if i := strings.Index(lead, term); i >= 0 {
			lead = lead[:i]
			break
		}
	}
	for w := range strings.FieldsSeq(lead) {
		// Strip trailing punctuation + quotes so "task." → "task" and
		// "'kick'" → "kick".
		w = strings.Trim(w, ".,;:!?\"'`()[]{}<>")
		add(w)
	}
	return out
}

// wholeWordContains reports whether s (assumed pre-lowercased)
// contains needle as a whole word — bounded by start/end or any
// non-alphanumeric character. Prevents "task" from matching "tasks"
// (debatable — lenient) or "multitask" (clear over-match).
func wholeWordContains(s, needle string) bool {
	i := 0
	for {
		idx := strings.Index(s[i:], needle)
		if idx < 0 {
			return false
		}
		abs := i + idx
		before := abs == 0 || !isAlnum(s[abs-1])
		afterIdx := abs + len(needle)
		after := afterIdx == len(s) || !isAlnum(s[afterIdx])
		if before && after {
			return true
		}
		i = abs + 1
	}
}

func isAlnum(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// CosineSimilarity computes the cosine of the angle between two vectors.
// Returns 0 when either vector has zero magnitude or lengths differ —
// both are degenerate inputs for retrieval (no signal to rank by), and
// conflating them simplifies the selection loop.
func CosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		av, bv := float64(a[i]), float64(b[i])
		dot += av * bv
		na += av * av
		nb += bv * bv
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(na) * math.Sqrt(nb)))
}
