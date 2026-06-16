package engine

import (
	"context"
	"fmt"
	"iter"
	"sync"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// compositeSource presents a live union of two tool sources. The primary
// source wins on name collisions; secondary duplicates are skipped so untrusted
// external tools cannot shadow built-ins.
//
// The union and a name→source routing table are memoized and rebuilt only when
// an operand's Version() changes, so steady-state Tools/Execute calls don't
// re-walk both sources (which, for an MCP-backed secondary, can mean a network
// round-trip per call). Tools registered into either source after turn startup
// still become visible on the next iteration because registration bumps the
// source version, invalidating the cache. A source that does not report a
// version is treated as always-dirty: the cache is not used, preserving the
// original re-walk-every-call behaviour for it.
type compositeSource struct {
	primary   tools.Source
	secondary tools.Source

	mu     sync.Mutex
	union  []tools.Tool
	route  map[tools.ToolName]tools.Source
	verP   int
	verS   int
	cached bool
}

func newCompositeSource(primary, secondary tools.Source) tools.Source {
	if primary == nil {
		return secondary
	}
	if secondary == nil {
		return primary
	}
	return &compositeSource{primary: primary, secondary: secondary}
}

// snapshot returns the current deduped union and routing table, rebuilding them
// only when an operand has changed since the last build. The returned slice and
// map are never mutated after construction, so callers may read them without
// holding the lock.
func (s *compositeSource) snapshot(ctx context.Context) ([]tools.Tool, map[tools.ToolName]tools.Source) {
	s.mu.Lock()
	defer s.mu.Unlock()

	pv, pok := sourceVersion(s.primary)
	sv, sok := sourceVersion(s.secondary)
	if s.cached && pok && sok && pv == s.verP && sv == s.verS {
		return s.union, s.route
	}

	union := make([]tools.Tool, 0)
	route := map[tools.ToolName]tools.Source{}
	for t := range s.primary.Tools(ctx) {
		name := t.Definition().Name
		if _, dup := route[name]; dup {
			continue
		}
		route[name] = s.primary
		union = append(union, t)
	}
	for t := range s.secondary.Tools(ctx) {
		name := t.Definition().Name
		if _, dup := route[name]; dup {
			continue
		}
		route[name] = s.secondary
		union = append(union, t)
	}

	// Only memoize when both operands report a version; otherwise a silent
	// registration on an unversioned source would be masked by a stale cache.
	if pok && sok {
		s.union, s.route, s.verP, s.verS, s.cached = union, route, pv, sv, true
	} else {
		s.cached = false
	}
	return union, route
}

func (s *compositeSource) Tools(ctx context.Context) iter.Seq[tools.Tool] {
	union, _ := s.snapshot(ctx)
	return func(yield func(tools.Tool) bool) {
		for _, t := range union {
			if !yield(t) {
				return
			}
		}
	}
}

func (s *compositeSource) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	_, route := s.snapshot(ctx)
	if src, ok := route[call.ToolName]; ok {
		return src.Execute(ctx, call)
	}
	return nil, fmt.Errorf("tool not found: %s", call.ToolName)
}

// versioned is the optional capability a tool source exposes when it can report
// a monotonic revision that bumps on every tool add/remove (tools.Registry
// does). The composite uses it to decide when its cache is still valid.
type versioned interface {
	Version() int
}

func sourceVersion(src tools.Source) (int, bool) {
	if v, ok := src.(versioned); ok {
		return v.Version(), true
	}
	return 0, false
}
