package backends

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/zarldev/zarlmono/zkit/options"
)

var (
	// ErrRouteCachePolicyNotFound means the exact provider/alias has no saved policy.
	ErrRouteCachePolicyNotFound = errors.New("route cache policy not found")
	// ErrInvalidRouteCachePolicy means a policy or stored representation is malformed.
	ErrInvalidRouteCachePolicy = errors.New("invalid route cache policy")
	// ErrRouteCachePolicyVersion means a stored policy uses an unsupported version.
	ErrRouteCachePolicyVersion = errors.New("unsupported route cache policy version")
)

// RouteCachePolicy is comparable alias-specific cache marker intent, not evidence
// of upstream caching. The zero value inherits the saved alias policy at runtime.
type RouteCachePolicy struct {
	Mode                  RouteCacheMode
	StaticTTL, RollingTTL RouteCacheTTL
	System, Tools         bool
}

// ValidateRouteCachePolicy checks the generic value shape; adapter-specific
// protocol and marker support are checked when the policy is applied.
func ValidateRouteCachePolicy(policy RouteCachePolicy) error {
	switch policy.Mode {
	case RouteCacheModes.CACHEINHERIT, RouteCacheModes.CACHEDEFAULT, RouteCacheModes.CACHEDISABLED, RouteCacheModes.CACHEEXPLICIT:
	default:
		return fmt.Errorf("%w: unknown mode", ErrInvalidRouteCachePolicy)
	}
	switch policy.StaticTTL {
	case RouteCacheTTLs.CACHETTLDEFAULT, RouteCacheTTLs.CACHETTL5M, RouteCacheTTLs.CACHETTL1H:
	default:
		return fmt.Errorf("%w: unknown static lifetime", ErrInvalidRouteCachePolicy)
	}
	switch policy.RollingTTL {
	case RouteCacheTTLs.CACHETTLDEFAULT, RouteCacheTTLs.CACHETTL5M, RouteCacheTTLs.CACHETTL1H:
	default:
		return fmt.Errorf("%w: unknown rolling lifetime", ErrInvalidRouteCachePolicy)
	}
	if policy.Mode != RouteCacheModes.CACHEEXPLICIT && (policy.StaticTTL != RouteCacheTTLs.CACHETTLDEFAULT || policy.RollingTTL != RouteCacheTTLs.CACHETTLDEFAULT || policy.System || policy.Tools) {
		return fmt.Errorf("%w: non-explicit policy carries marker settings", ErrInvalidRouteCachePolicy)
	}
	return nil
}

// RouteCachePolicyStore persists saved non-inherited policies by exact opaque
// nonempty UTF-8 provider and alias. Missing reads return ErrRouteCachePolicyNotFound;
// deletion is idempotent. It is an optional capability, independent of Store.
type RouteCachePolicyStore interface {
	GetRouteCachePolicy(context.Context, string, string) (RouteCachePolicy, error)
	SetRouteCachePolicy(context.Context, string, string, RouteCachePolicy) error
	DeleteRouteCachePolicy(context.Context, string, string) error
}

// WithRouteCachePolicyStore supplies an optional exact-alias durable policy store.
func WithRouteCachePolicyStore(store RouteCachePolicyStore) options.Option[ProviderRegistry] {
	return func(r *ProviderRegistry) { r.routeCachePolicies = store }
}

// GetRouteCachePolicy reads a saved exact-alias policy, without resolving defaults.
func (r *ProviderRegistry) GetRouteCachePolicy(ctx context.Context, provider, alias string) (RouteCachePolicy, error) {
	if r.routeCachePolicies == nil {
		return RouteCachePolicy{}, ErrNoStore
	}
	if err := r.validateRouteCachePolicyKey(provider, alias); err != nil {
		return RouteCachePolicy{}, err
	}
	p, err := r.routeCachePolicies.GetRouteCachePolicy(ctx, provider, alias)
	if err != nil {
		return RouteCachePolicy{}, fmt.Errorf("get route cache policy: %w", err)
	}
	if err := validateStoredRouteCachePolicy(p); err != nil {
		return RouteCachePolicy{}, err
	}
	return p, nil
}

// SetRouteCachePolicy saves a non-inherited exact-alias policy without rebuilding a provider.
func (r *ProviderRegistry) SetRouteCachePolicy(ctx context.Context, provider, alias string, p RouteCachePolicy) error {
	if r.routeCachePolicies == nil {
		return ErrNoStore
	}
	if err := r.validateRouteCachePolicyKey(provider, alias); err != nil {
		return err
	}
	if err := validateStoredRouteCachePolicy(p); err != nil {
		return err
	}
	if err := r.routeCachePolicies.SetRouteCachePolicy(ctx, provider, alias, p); err != nil {
		return fmt.Errorf("set route cache policy: %w", err)
	}
	return nil
}

// DeleteRouteCachePolicy clears an exact-alias policy; absent rows succeed.
func (r *ProviderRegistry) DeleteRouteCachePolicy(ctx context.Context, provider, alias string) error {
	if r.routeCachePolicies == nil {
		return ErrNoStore
	}
	if err := r.validateRouteCachePolicyKey(provider, alias); err != nil {
		return err
	}
	if err := r.routeCachePolicies.DeleteRouteCachePolicy(ctx, provider, alias); err != nil {
		return fmt.Errorf("delete route cache policy: %w", err)
	}
	return nil
}

func validateStoredRouteCachePolicy(p RouteCachePolicy) error {
	if err := ValidateRouteCachePolicy(p); err != nil {
		return err
	}
	if p.Mode == RouteCacheModes.CACHEINHERIT {
		return fmt.Errorf("%w: inherited policy is not stored", ErrInvalidRouteCachePolicy)
	}
	return nil
}

func (r *ProviderRegistry) validateRouteCachePolicyKey(provider, alias string) error {
	if provider == "" || alias == "" || !utf8.ValidString(provider) || !utf8.ValidString(alias) {
		return fmt.Errorf("%w: keys require nonempty UTF-8", ErrInvalidRouteCachePolicy)
	}
	d, err := r.Parse(provider)
	if err != nil {
		return err
	}
	if d.AdapterType != AdapterTypes.ANTHROPICCOMPATIBLE && d.AdapterType != AdapterTypes.LITELLM {
		return fmt.Errorf("%w: adapter does not support route cache policy", ErrInvalidRouteCachePolicy)
	}
	return nil
}
