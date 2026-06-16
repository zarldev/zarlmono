package taskrunner

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/zarldev/zarlmono/zkit/agent/profile"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

// ResolvedProfile pairs the shared profile resolution (persona +
// execution settings from zkit/agent/profile) with the gated tool
// snapshot for one task run.
type ResolvedProfile struct {
	profile.Resolved
	Tools []tools.Tool
}

// GateSpec describes which live-registry tools a profile's task source
// includes. Tools names individual tools by semantic name; Providers
// opts the profile into every tool registered under a provider (e.g.
// "obsidian") so an MCP server can rename tools without breaking the
// gate. Both lists empty means dynamic mode: every registry tool except
// the task tools, plus action tools.
type GateSpec struct {
	Tools     []tools.ToolName
	Providers []string
}

// ToolNamesOverrideStore loads the runtime tool-name override for a
// profile — the admin-edited list stored alongside the zkit profile
// override. A nil/empty slice means "no override".
type ToolNamesOverrideStore interface {
	ToolNames(ctx context.Context, name profile.Name) ([]tools.ToolName, error)
}

// ProfileRegistry resolves profiles on task start. Persona and
// execution settings come from the shared zkit profile registry; tool
// gating happens here, against the live tool registry, when the task
// source is assembled.
type ProfileRegistry interface {
	Resolve(ctx context.Context, name profile.Name) (ResolvedProfile, error)
	List(ctx context.Context) ([]profile.Profile, error)
	// GateFor returns the override-merged gate spec for a profile —
	// what Resolve will filter the live registry with.
	GateFor(ctx context.Context, name profile.Name) (GateSpec, error)
}

// profileRegistry composes the zkit profile registry with the
// registry-level tool gate.
type profileRegistry struct {
	profiles     profile.Registry
	gates        map[profile.Name]GateSpec
	overrides    ToolNamesOverrideStore
	toolRegistry *tools.Registry
	actionTools  []tools.Tool

	warnedOnce sync.Map
}

// NewProfileRegistry constructs a ProfileRegistry. profiles supplies
// persona/execution resolution (builtin defs + operator overrides);
// gates carries the per-profile tool gate specs; overrides supplies
// runtime tool-name overrides merged at resolve time so admin-panel
// changes take effect without a restart.
func NewProfileRegistry(
	profiles profile.Registry,
	gates map[profile.Name]GateSpec,
	overrides ToolNamesOverrideStore,
	registry *tools.Registry,
	actionTools []tools.Tool,
) ProfileRegistry {
	return &profileRegistry{
		profiles:     profiles,
		gates:        gates,
		overrides:    overrides,
		toolRegistry: registry,
		actionTools:  actionTools,
	}
}

// Resolve merges profile settings via the zkit registry (default
// fallback, operator overrides, iteration clamp), then gates the live
// tool registry down to the profile's set.
func (r *profileRegistry) Resolve(ctx context.Context, name profile.Name) (ResolvedProfile, error) {
	resolved, err := r.profiles.Resolve(ctx, name)
	if err != nil {
		return ResolvedProfile{}, err
	}
	gate, err := r.GateFor(ctx, resolved.Name)
	if err != nil {
		return ResolvedProfile{}, err
	}
	gated := r.filterTools(resolved.Name, gate)
	if len(gated) == 0 {
		return ResolvedProfile{}, ErrProfileNoTools
	}
	return ResolvedProfile{Resolved: resolved, Tools: gated}, nil
}

// List delegates to the zkit registry.
func (r *profileRegistry) List(ctx context.Context) ([]profile.Profile, error) {
	return r.profiles.List(ctx)
}

// GateFor returns the profile's gate spec with the runtime tool-name
// override applied. An override replaces the named-tool list; the
// provider list always comes from the code-defined spec.
func (r *profileRegistry) GateFor(ctx context.Context, name profile.Name) (GateSpec, error) {
	gate := r.gates[name]
	if r.overrides == nil {
		return gate, nil
	}
	names, err := r.overrides.ToolNames(ctx, name)
	if err != nil {
		return GateSpec{}, fmt.Errorf("load tool-name override for %q: %w", name, err)
	}
	if len(names) > 0 {
		gate.Tools = names
	}
	return gate, nil
}

// filterTools applies the gate to the live tool set. An empty gate
// (no names, no providers) is dynamic mode: every registry tool except
// start_task/schedule_task, plus action tools. Otherwise: the union of
// named tools and every tool registered under a gated provider.
// Unknown names are logged once per profile and dropped.
func (r *profileRegistry) filterTools(name profile.Name, gate GateSpec) []tools.Tool {
	if len(gate.Tools) == 0 && len(gate.Providers) == 0 {
		return r.dynamicDefaultTools()
	}
	by := r.toolIndex()
	seen := make(map[string]bool)
	out := make([]tools.Tool, 0, len(gate.Tools))
	var missing []string
	for _, n := range gate.Tools {
		tool, ok := by[n]
		if !ok {
			missing = append(missing, string(n))
			continue
		}
		if seen[tool.Definition().Name.String()] {
			continue
		}
		seen[tool.Definition().Name.String()] = true
		out = append(out, tool)
	}
	for _, provider := range gate.Providers {
		for _, tool := range r.toolRegistry.ToolsByProvider(provider) {
			toolName := tool.Definition().Name.String()
			if seen[toolName] {
				continue
			}
			seen[toolName] = true
			out = append(out, tool)
		}
	}
	if len(missing) > 0 {
		if _, warned := r.warnedOnce.LoadOrStore(name, true); !warned {
			slog.Warn("profile gate names tools not in registry, dropping", "profile", name, "missing", missing)
		}
	}
	return out
}

func (r *profileRegistry) dynamicDefaultTools() []tools.Tool {
	excluded := map[string]bool{"start_task": true, "schedule_task": true}
	registryTools := r.allRegistryTools()
	out := make([]tools.Tool, 0, len(registryTools)+len(r.actionTools))
	for _, t := range registryTools {
		if excluded[t.Definition().Name.String()] {
			continue
		}
		out = append(out, t)
	}
	out = append(out, r.actionTools...)
	return out
}

func (r *profileRegistry) toolIndex() map[tools.ToolName]tools.Tool {
	by := map[tools.ToolName]tools.Tool{}
	for _, t := range r.allRegistryTools() {
		by[t.Definition().Name] = t
	}
	for _, t := range r.actionTools {
		by[t.Definition().Name] = t
	}
	return by
}

// allRegistryTools snapshots the live registry into a slice. The shared
// registry exposes tools via an iterator rather than a slice accessor.
func (r *profileRegistry) allRegistryTools() []tools.Tool {
	var all []tools.Tool
	for t := range r.toolRegistry.Tools(context.Background()) {
		all = append(all, t)
	}
	return all
}
