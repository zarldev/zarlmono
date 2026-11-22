package backends

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/options"
)

// ErrRouteProtocolNotFound reports that an exact provider/alias pair has no policy.
var ErrRouteProtocolNotFound = errors.New("route protocol not found")

// RouteProtocolStore persists concrete wire protocols by configured provider name
// and exact opaque alias. Keys are nonempty UTF-8, without normalization. Missing
// reads return ErrRouteProtocolNotFound; deletes are idempotent. Unknown is not a
// stored policy. This optional capability does not widen Store.
type RouteProtocolStore interface {
	GetRouteProtocol(ctx context.Context, provider, alias string) (llm.ModelProtocol, error)
	SetRouteProtocol(ctx context.Context, provider, alias string, protocol llm.ModelProtocol) error
	DeleteRouteProtocol(ctx context.Context, provider, alias string) error
}

// WithRouteProtocolStore supplies durable exact-alias policy for future builds.
// Without it, builds use runtime overrides or Chat Completions, and explicit
// policy operations return ErrNoStore.
func WithRouteProtocolStore(store RouteProtocolStore) options.Option[ProviderRegistry] {
	return func(r *ProviderRegistry) { r.routeProtocols = store }
}

// GetRouteProtocol reads the exact configured LiteLLM provider/alias policy.
// Absence is ErrRouteProtocolNotFound, not a resolved default.
func (r *ProviderRegistry) GetRouteProtocol(ctx context.Context, provider, alias string) (llm.ModelProtocol, error) {
	if r.routeProtocols == nil {
		return llm.ModelProtocol{}, ErrNoStore
	}
	if err := r.validateRouteProtocolKey(provider, alias); err != nil {
		return llm.ModelProtocol{}, err
	}
	protocol, err := r.routeProtocols.GetRouteProtocol(ctx, provider, alias)
	if err != nil {
		return llm.ModelProtocol{}, fmt.Errorf("get route protocol: %w", err)
	}
	if err := validateRouteProtocol(protocol); err != nil {
		return llm.ModelProtocol{}, err
	}
	return protocol, nil
}

// SetRouteProtocol sets a concrete protocol for future builds of this exact
// LiteLLM provider/alias. It does not rebuild existing providers or active turns.
func (r *ProviderRegistry) SetRouteProtocol(ctx context.Context, provider, alias string, protocol llm.ModelProtocol) error {
	if r.routeProtocols == nil {
		return ErrNoStore
	}
	if err := r.validateRouteProtocolKey(provider, alias); err != nil {
		return err
	}
	if err := validateRouteProtocol(protocol); err != nil {
		return err
	}
	if err := r.routeProtocols.SetRouteProtocol(ctx, provider, alias, protocol); err != nil {
		return fmt.Errorf("set route protocol: %w", err)
	}
	return nil
}

// DeleteRouteProtocol clears this exact LiteLLM provider/alias policy. Deleting
// an absent policy succeeds; future builds then use the ordinary default.
func (r *ProviderRegistry) DeleteRouteProtocol(ctx context.Context, provider, alias string) error {
	if r.routeProtocols == nil {
		return ErrNoStore
	}
	if err := r.validateRouteProtocolKey(provider, alias); err != nil {
		return err
	}
	if err := r.routeProtocols.DeleteRouteProtocol(ctx, provider, alias); err != nil {
		return fmt.Errorf("delete route protocol: %w", err)
	}
	return nil
}

func (r *ProviderRegistry) validateRouteProtocolKey(provider, alias string) error {
	if provider == "" || alias == "" || !utf8.ValidString(provider) || !utf8.ValidString(alias) {
		return fmt.Errorf("%w: route protocol keys require nonempty UTF-8", ErrInvalidProviderDef)
	}
	def, err := r.Parse(provider)
	if err != nil {
		return err
	}
	if def.AdapterType != AdapterTypes.LITELLM {
		return fmt.Errorf("%w: route protocol requires LiteLLM adapter", ErrInvalidProviderDef)
	}
	return nil
}

func validateRouteProtocol(protocol llm.ModelProtocol) error {
	switch protocol {
	case llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS, llm.ModelProtocols.PROTOCOLRESPONSES,
		llm.ModelProtocols.PROTOCOLMESSAGES, llm.ModelProtocols.PROTOCOLGENERATECONTENT:
		return nil
	default:
		return fmt.Errorf("%w: unsupported LiteLLM protocol", ErrInvalidProviderDef)
	}
}
