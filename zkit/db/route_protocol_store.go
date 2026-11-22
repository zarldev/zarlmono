package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/db/gen"
)

var _ backends.RouteProtocolStore = (*Store)(nil)

// GetRouteProtocol reads an exact provider/alias policy, returning
// backends.ErrRouteProtocolNotFound for absence. Stored representations are
// parsed here; malformed or unknown protocols never become a default policy.
func (s *Store) GetRouteProtocol(ctx context.Context, provider, alias string) (llm.ModelProtocol, error) {
	if err := validateRouteProtocolKeys(provider, alias); err != nil {
		return llm.ModelProtocol{}, err
	}
	raw, err := s.read.GetRouteProtocol(ctx, gen.GetRouteProtocolParams{Provider: provider, Alias: alias})
	if errors.Is(err, sql.ErrNoRows) {
		return llm.ModelProtocol{}, backends.ErrRouteProtocolNotFound
	}
	if err != nil {
		return llm.ModelProtocol{}, fmt.Errorf("get route protocol: %w", err)
	}
	protocol, err := llm.ParseModelProtocol(raw)
	if err != nil {
		// The parser error includes raw storage content; expose only a safe identity.
		return llm.ModelProtocol{}, fmt.Errorf("%w: parse stored route protocol", backends.ErrInvalidProviderDef)
	}
	if err := validateStoredRouteProtocol(protocol); err != nil {
		return llm.ModelProtocol{}, err
	}
	return protocol, nil
}

// SetRouteProtocol durably upserts a concrete protocol using exact nonempty
// UTF-8 keys. The registry owns the configured LiteLLM definition check; this
// table also supports built-in providers without a custom provider row.
func (s *Store) SetRouteProtocol(ctx context.Context, provider, alias string, protocol llm.ModelProtocol) error {
	if err := validateRouteProtocolKeys(provider, alias); err != nil {
		return err
	}
	if err := validateStoredRouteProtocol(protocol); err != nil {
		return err
	}
	if err := s.q.UpsertRouteProtocol(ctx, gen.UpsertRouteProtocolParams{Provider: provider, Alias: alias, Protocol: protocol.String()}); err != nil {
		return fmt.Errorf("set route protocol: %w", err)
	}
	return nil
}

// DeleteRouteProtocol clears the exact provider/alias policy. Absence succeeds.
func (s *Store) DeleteRouteProtocol(ctx context.Context, provider, alias string) error {
	if err := validateRouteProtocolKeys(provider, alias); err != nil {
		return err
	}
	if err := s.q.DeleteRouteProtocol(ctx, gen.DeleteRouteProtocolParams{Provider: provider, Alias: alias}); err != nil {
		return fmt.Errorf("delete route protocol: %w", err)
	}
	return nil
}

func validateRouteProtocolKeys(provider, alias string) error {
	if provider == "" || alias == "" || !utf8.ValidString(provider) || !utf8.ValidString(alias) {
		return fmt.Errorf("%w: route protocol keys require nonempty UTF-8", backends.ErrInvalidProviderDef)
	}
	return nil
}

func validateStoredRouteProtocol(protocol llm.ModelProtocol) error {
	switch protocol {
	case llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS, llm.ModelProtocols.PROTOCOLRESPONSES,
		llm.ModelProtocols.PROTOCOLMESSAGES, llm.ModelProtocols.PROTOCOLGENERATECONTENT:
		return nil
	default:
		return fmt.Errorf("%w: stored route protocol must be concrete", backends.ErrInvalidProviderDef)
	}
}
