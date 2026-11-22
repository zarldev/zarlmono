package litellm

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrModelInfoNotCached means this source has not yet had a successful refresh.
// A denied or failed first refresh leaves it in this state.
var ErrModelInfoNotCached = errors.New("litellm model info not cached")

// ModelSource is a caller-owned memory cache bound to one configured provider
// (and thus that provider's credential/tenant scope). It retains at most one
// bounded last-good snapshot. It has no disk persistence, automatic network I/O,
// background workers, or timers. Do not copy it after use. The bound provider
// must not be reconfigured concurrently or repurposed for another tenant.
//
// Concurrent refreshes serialize; queued callers can cancel without waiting for
// an active fetch. Snapshot reads do not wait for network I/O. Failed or denied
// refreshes preserve the previous observation and expiry without extension.
type ModelSource struct {
	provider   *Provider
	ttl        time.Duration
	refreshing chan struct{}
	mu         sync.Mutex
	snapshot   ModelInfoSnapshot
	cached     bool
}

// NewModelSource assembles an empty cache with a trusted configured provider and
// TTL. It performs no I/O. TTL is measured from each successful observation; a
// nonpositive TTL makes that observation immediately stale, not immortal.
func NewModelSource(provider *Provider, ttl time.Duration) *ModelSource {
	return &ModelSource{provider: provider, ttl: ttl, refreshing: make(chan struct{}, 1)}
}

// Refresh explicitly fetches enrichment with the caller's context. Success
// replaces the last-good snapshot and returns an independently owned copy with
// its expiry set. Failure returns an empty snapshot and the sanitized fetch error;
// callers can still read the previous (possibly stale) snapshot via Snapshot.
func (s *ModelSource) Refresh(ctx context.Context) (ModelInfoSnapshot, error) {
	if cause := context.Cause(ctx); cause != nil {
		return ModelInfoSnapshot{}, cause
	}
	select {
	case s.refreshing <- struct{}{}:
		defer func() { <-s.refreshing }()
	case <-ctx.Done():
		return ModelInfoSnapshot{}, context.Cause(ctx)
	}
	snapshot, err := s.provider.ModelInfo(ctx)
	if err != nil {
		return ModelInfoSnapshot{}, err
	}
	snapshot.Provenance.ExpiresAt = snapshot.Provenance.ObservedAt.Add(s.ttl)
	s.mu.Lock()
	s.snapshot = snapshot
	s.cached = true
	s.mu.Unlock()
	return snapshot.Clone(), nil
}

// Snapshot returns an independently deep-owned last-good snapshot, or
// ErrModelInfoNotCached before the first successful refresh. It never performs
// network/disk I/O or extends TTL. Stale snapshots remain available: callers must
// compare Provenance.ExpiresAt with their own clock before using the evidence.
func (s *ModelSource) Snapshot() (ModelInfoSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.cached {
		return ModelInfoSnapshot{}, ErrModelInfoNotCached
	}
	return s.snapshot.Clone(), nil
}
