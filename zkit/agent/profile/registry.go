package profile

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// Override captures per-profile settings stored in a database. Pointer
// fields let callers express "field unset" — the Registry only applies
// non-nil values, so an Override can override one field without
// disturbing the others.
type Override struct {
	Model         *string
	PromptPrefix  *string
	MaxIterations *int32
}

// OverrideStore loads stored per-profile overrides. Implementations
// typically read from a database; an empty Override (zero value) means
// "no overrides for this profile.".
type OverrideStore interface {
	Get(ctx context.Context, name Name) (Override, error)
}

// Source loads profiles from persistent storage. The default
// implementation merges a fixed slice of code-defined profiles with
// overrides from an OverrideStore.
type Source interface {
	Get(ctx context.Context, name Name) (Profile, error)
	List(ctx context.Context) ([]Profile, error)
}

// staticSource merges builtin profiles with runtime overrides.
type staticSource struct {
	profiles  []Profile
	overrides OverrideStore
}

func (s *staticSource) Get(ctx context.Context, name Name) (Profile, error) {
	for _, p := range s.profiles {
		if p.Name != name {
			continue
		}
		if s.overrides != nil {
			o, err := s.overrides.Get(ctx, name)
			if err != nil {
				return Profile{}, fmt.Errorf("load override for %q: %w", name, err)
			}
			if o.Model != nil {
				p.Model = *o.Model
			}
			if o.PromptPrefix != nil {
				p.PromptPrefix = *o.PromptPrefix
			}
			if o.MaxIterations != nil && *o.MaxIterations > 0 {
				p.MaxIterations = int(*o.MaxIterations)
			}
		}
		return p, nil
	}
	return Profile{}, ErrNotFound
}

func (s *staticSource) List(ctx context.Context) ([]Profile, error) {
	out := make([]Profile, 0, len(s.profiles))
	for _, p := range s.profiles {
		merged, err := s.Get(ctx, p.Name)
		if err != nil {
			return nil, err
		}
		out = append(out, merged)
	}
	return out, nil
}

// Registry exposes profiles and resolves them on task start.
type Registry interface {
	Resolve(ctx context.Context, name Name) (Resolved, error)
	List(ctx context.Context) ([]Profile, error)
}

// registry is the concrete Registry implementation.
type registry struct {
	source   Source
	envModel string
	maxCap   int
}

// DefaultMaxIterations is the absolute ceiling Resolve clamps to. A
// profile setting MaxIterations to 0 or to a value greater than this
// is treated as "use the cap." Real coding sessions routinely go past
// 30 iterations (build / test / fix / rebuild loops, long bash
// recon), so the cap sits well above the typical default.
const DefaultMaxIterations = 60

// NewRegistry constructs a Registry from a static slice of profiles
// and an optional override store. Overrides are merged at resolve time
// so admin-panel changes take effect without a restart.
//
// envModel is the fallback model name for profiles whose Model field
// is empty (typically the runner's CHAT_MODEL env var).
func NewRegistry(
	profiles []Profile,
	overrides OverrideStore,
	envModel string,
) Registry {
	return &registry{
		source:   &staticSource{profiles: profiles, overrides: overrides},
		envModel: envModel,
		maxCap:   DefaultMaxIterations,
	}
}

// Resolve loads the profile and merges in operator overrides. On miss,
// logs a warning and falls back to NameDefault.
func (r *registry) Resolve(ctx context.Context, name Name) (Resolved, error) {
	p, err := r.source.Get(ctx, name)
	if errors.Is(err, ErrNotFound) {
		slog.WarnContext(ctx, "unknown profile, falling back to default", "requested", name)
		p, err = r.source.Get(ctx, NameDefault)
		if err != nil {
			return Resolved{}, fmt.Errorf("load default profile: %w", err)
		}
		name = NameDefault
	} else if err != nil {
		return Resolved{}, fmt.Errorf("load profile %q: %w", name, err)
	}

	maxIter := p.MaxIterations
	if maxIter <= 0 || maxIter > r.maxCap {
		maxIter = r.maxCap
	}

	return Resolved{
		Name:          name,
		Model:         CoalesceStr(p.Model, r.envModel),
		PromptPrefix:  p.PromptPrefix,
		MaxIterations: maxIter,
	}, nil
}

// List delegates to the source.
func (r *registry) List(ctx context.Context) ([]Profile, error) {
	return r.source.List(ctx)
}
