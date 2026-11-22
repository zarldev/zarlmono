package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/db/gen"
)

var _ backends.RouteCachePolicyStore = (*Store)(nil)

const (
	cacheSQLiteInteger = "integer"
	cacheSQLiteText    = "text"
)

// GetRouteCachePolicy reads a saved exact-alias policy. Storage types,
// version, enum spelling and shape are checked before returning typed intent.
func (s *Store) GetRouteCachePolicy(ctx context.Context, provider, alias string) (backends.RouteCachePolicy, error) {
	if err := validateRouteCachePolicyKeys(provider, alias); err != nil {
		return backends.RouteCachePolicy{}, err
	}
	row, err := s.read.GetRouteCachePolicy(ctx, gen.GetRouteCachePolicyParams{Provider: provider, Alias: alias})
	if errors.Is(err, sql.ErrNoRows) {
		return backends.RouteCachePolicy{}, backends.ErrRouteCachePolicyNotFound
	}
	if err != nil {
		return backends.RouteCachePolicy{}, fmt.Errorf("read route cache policy: %w", err)
	}
	if row.VersionType != cacheSQLiteInteger {
		return backends.RouteCachePolicy{}, fmt.Errorf("%w: stored version type", backends.ErrInvalidRouteCachePolicy)
	}
	if row.Version != "1" {
		return backends.RouteCachePolicy{}, backends.ErrRouteCachePolicyVersion
	}
	if row.ModeType != cacheSQLiteText || row.StaticTtlType != cacheSQLiteText || row.RollingTtlType != cacheSQLiteText || row.SystemMarkerType != cacheSQLiteInteger || row.ToolMarkerType != cacheSQLiteInteger {
		return backends.RouteCachePolicy{}, fmt.Errorf("%w: stored column type", backends.ErrInvalidRouteCachePolicy)
	}
	if (row.SystemMarker != "0" && row.SystemMarker != "1") || (row.ToolMarker != "0" && row.ToolMarker != "1") {
		return backends.RouteCachePolicy{}, fmt.Errorf("%w: stored marker flag", backends.ErrInvalidRouteCachePolicy)
	}
	// Check canonical spellings explicitly: generated parsers may accept numeric values.
	mode, err := backends.ParseRouteCacheMode(row.Mode)
	if err != nil || mode.String() != row.Mode {
		return backends.RouteCachePolicy{}, fmt.Errorf("%w: stored mode", backends.ErrInvalidRouteCachePolicy)
	}
	static, err := backends.ParseRouteCacheTTL(row.StaticTtl)
	if err != nil || static.String() != row.StaticTtl {
		return backends.RouteCachePolicy{}, fmt.Errorf("%w: stored static lifetime", backends.ErrInvalidRouteCachePolicy)
	}
	rolling, err := backends.ParseRouteCacheTTL(row.RollingTtl)
	if err != nil || rolling.String() != row.RollingTtl {
		return backends.RouteCachePolicy{}, fmt.Errorf("%w: stored rolling lifetime", backends.ErrInvalidRouteCachePolicy)
	}
	p := backends.RouteCachePolicy{Mode: mode, StaticTTL: static, RollingTTL: rolling, System: row.SystemMarker == "1", Tools: row.ToolMarker == "1"}
	if err := backends.ValidateRouteCachePolicy(p); err != nil {
		return backends.RouteCachePolicy{}, err
	}
	if p.Mode == backends.RouteCacheModes.CACHEINHERIT {
		return backends.RouteCachePolicy{}, fmt.Errorf("%w: stored inherited policy", backends.ErrInvalidRouteCachePolicy)
	}
	return p, nil
}

// SetRouteCachePolicy saves generic validated non-inherited intent by exact key.
func (s *Store) SetRouteCachePolicy(ctx context.Context, provider, alias string, p backends.RouteCachePolicy) error {
	if err := validateRouteCachePolicyKeys(provider, alias); err != nil {
		return err
	}
	if err := backends.ValidateRouteCachePolicy(p); err != nil {
		return err
	}
	if p.Mode == backends.RouteCacheModes.CACHEINHERIT {
		return fmt.Errorf("%w: inherited policy is not stored", backends.ErrInvalidRouteCachePolicy)
	}
	var system, tools int64
	if p.System {
		system = 1
	}
	if p.Tools {
		tools = 1
	}
	if err := s.q.UpsertRouteCachePolicy(ctx, gen.UpsertRouteCachePolicyParams{Provider: provider, Alias: alias, Mode: p.Mode.String(), StaticTtl: p.StaticTTL.String(), RollingTtl: p.RollingTTL.String(), SystemMarker: system, ToolMarker: tools}); err != nil {
		return fmt.Errorf("set route cache policy: %w", err)
	}
	return nil
}

// DeleteRouteCachePolicy clears the exact key, including if already absent.
func (s *Store) DeleteRouteCachePolicy(ctx context.Context, provider, alias string) error {
	if err := validateRouteCachePolicyKeys(provider, alias); err != nil {
		return err
	}
	if err := s.q.DeleteRouteCachePolicy(ctx, gen.DeleteRouteCachePolicyParams{Provider: provider, Alias: alias}); err != nil {
		return fmt.Errorf("delete route cache policy: %w", err)
	}
	return nil
}

func validateRouteCachePolicyKeys(provider, alias string) error {
	if provider == "" || alias == "" || !utf8.ValidString(provider) || !utf8.ValidString(alias) {
		return fmt.Errorf("%w: keys require nonempty UTF-8", backends.ErrInvalidRouteCachePolicy)
	}
	return nil
}
