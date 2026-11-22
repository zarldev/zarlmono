package modelsdev

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/cache"
	"github.com/zarldev/zarlmono/zkit/options"
	"github.com/zarldev/zarlmono/zkit/zhttp"
)

const (
	defaultBaseURL = "https://models.dev/api.json"
	providerGoogle = "google"
	snapshotKey    = "models.dev"
	defaultTTL     = 6 * time.Hour
	snapshotSchema = 4
)

// fetchClient is the shared client for models.dev/api.json. zhttp's
// defaults give retry + backoff + timeout + Retry-After honouring on
// transient failures.
var fetchClient = zhttp.NewClient(zhttp.WithUserAgent("zarlmono/1"))

// Intrinsic is provider-independent model metadata parsed from models.dev.
type Intrinsic struct {
	ContextWindow    int
	MaxOutputTokens  int
	SupportsTools    bool
	SupportsVision   bool
	SupportsThinking bool
	SupportsVideo    bool
}

// Pricing is a provider-specific model price in USD per million tokens.
type Pricing struct {
	InputCostPerMTok  float64
	OutputCostPerMTok float64
}

// ModelInformation is native catalogue evidence, not effective route or protocol
// support. Reasoning describes the catalogue's reasoning assertion only; it says
// nothing about request controls, displayable output, or replay. Zero fields mean
// unknown. All fields are values, safe to retain independently of the source.
type ModelInformation struct {
	Tools      llm.Support
	Reasoning  llm.Support
	Modalities llm.DirectionalModalities
	Limits     llm.ModelLimits
}

// ModelRecord couples native catalogue evidence with its snapshot provenance.
// It is a value snapshot with no borrowed mutable state.
type ModelRecord struct {
	Information ModelInformation
	Provenance  llm.MetadataProvenance
}

// Entry retains authoritative native Evidence and its derived compatibility
// views. On ingestion only Evidence is trusted; Intrinsic, Pricing, and
// Information are recomputed, never used to infer missing assertions.
type Entry struct {
	Intrinsic
	Pricing
	Information ModelInformation
	Evidence    Evidence
}

// Snapshot is the cached whole-fetch blob. Its maps belong to the cache or its
// caller; Source clones both map levels when loading or publishing a snapshot.
type Snapshot struct {
	Schema    int                         `json:"schema"`
	FetchedAt time.Time                   `json:"fetched_at"`
	Revision  string                      `json:"revision"`
	Entries   map[string]map[string]Entry `json:"entries"`
}

// Source looks up model info from a cached models.dev snapshot,
// refreshing on TTL expiry. The zero value is not usable; construct
// with [New].
type Source struct {
	store   cache.Cache[string, Snapshot]
	ttl     time.Duration
	baseURL string

	// mu owns the process-local snapshot. Lookups are on UI/render hot paths;
	// once Warm or the first lookup has loaded the immutable snapshot, readers
	// must not reread and unmarshal the whole file cache for every model field.
	mu        sync.RWMutex
	snapshot  Snapshot
	hasSnap   bool
	refreshMu sync.Mutex
	retryAt   time.Time
}

// WithTTL sets the duration after which a cached snapshot is
// considered stale and re-fetched. Default is 6h.
func WithTTL(d time.Duration) options.Option[Source] {
	return func(s *Source) { s.ttl = d }
}

// WithBaseURL overrides the models.dev endpoint (test seam).
func WithBaseURL(url string) options.Option[Source] {
	return func(s *Source) { s.baseURL = url }
}

// New creates a Source backed by the supplied cache.Cache. A
// [cache.FileCache] (backed by OS filesystem) gives cross-restart
// persistence; [cache.MemoryCache] is fine for tests.
func New(store cache.Cache[string, Snapshot], opts ...options.Option[Source]) *Source {
	s := &Source{
		store:   store,
		ttl:     defaultTTL,
		baseURL: defaultBaseURL,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Lookup returns the models.dev entry for a (providerKey, model) pair.
// providerKey uses zarlcode's internal names ("openai", "anthropic",
// "gemini", "deepseek", "google-vertex", etc.) — the alias map
// translates to models.dev keys internally.
func (s *Source) Lookup(ctx context.Context, providerKey, model string) (Entry, bool) {
	snap, ok, _ := s.ensureSnapshot(ctx)
	if !ok {
		return Entry{}, false
	}
	key := providerAlias(providerKey)
	models, ok := snap.Entries[key]
	if !ok {
		return Entry{}, false
	}
	e, ok := models[model]
	return e, ok
}

// LookupCached returns an entry from the process-local snapshot only. It never
// reads the backing cache or performs network I/O, so it is safe on render and
// other latency-sensitive paths. ok is false until [Source.Warm] or a regular
// lookup has populated the snapshot.
func (s *Source) LookupCached(providerKey, model string) (Entry, bool) {
	s.mu.RLock()
	snap, ok := s.snapshot, s.hasSnap
	s.mu.RUnlock()
	if !ok {
		return Entry{}, false
	}
	models, ok := snap.Entries[providerAlias(providerKey)]
	if !ok {
		return Entry{}, false
	}
	entry, ok := models[model]
	return entry, ok
}

// LookupModelCached returns native evidence for exact catalogue provider and
// model keys from the process-local snapshot only. It does not apply provider
// aliases, normalize identities, seek consensus, read the backing cache, or use
// the network. Until Warm or a regular lookup populates the snapshot, ok is false.
// Stale evidence remains observable with its original observation and expiry
// timestamps; callers decide freshness. The returned value is safe to retain.
func (s *Source) LookupModelCached(providerKey string, model llm.ModelID) (ModelRecord, bool) {
	evidence, ok := s.LookupEvidenceCached(providerKey, string(model))
	if !ok {
		return ModelRecord{}, false
	}
	return ModelRecord{Information: information(evidence), Provenance: evidence.Provenance}, true
}

// LookupIntrinsic returns provider-independent metadata for a model name. It
// is used when an endpoint serves a model owned by another provider. Exact
// provider lookup remains authoritative for pricing; this method returns no
// match when matching catalogue entries disagree on intrinsic limits or
// capabilities.
func (s *Source) LookupIntrinsic(ctx context.Context, model string) (Intrinsic, bool) {
	snap, ok, _ := s.ensureSnapshot(ctx)
	if !ok {
		return Intrinsic{}, false
	}
	full := normalizeModelID(model)
	if full == "" {
		return Intrinsic{}, false
	}
	if entry, ok := intrinsicConsensus(snap, full, false); ok {
		return entry, true
	}
	base := modelBaseName(full)
	if base == full {
		return Intrinsic{}, false
	}
	return intrinsicConsensus(snap, base, true)
}

// LookupIntrinsicCached is the process-local, non-blocking counterpart to
// [Source.LookupIntrinsic]. It never reads the backing cache or performs
// network I/O.
func (s *Source) LookupIntrinsicCached(model string) (Intrinsic, bool) {
	s.mu.RLock()
	snap, ok := s.snapshot, s.hasSnap
	s.mu.RUnlock()
	if !ok {
		return Intrinsic{}, false
	}
	full := normalizeModelID(model)
	if full == "" {
		return Intrinsic{}, false
	}
	if entry, ok := intrinsicConsensus(snap, full, false); ok {
		return entry, true
	}
	base := modelBaseName(full)
	if base == full {
		return Intrinsic{}, false
	}
	return intrinsicConsensus(snap, base, true)
}

func intrinsicConsensus(snap Snapshot, model string, baseName bool) (Intrinsic, bool) {
	var consensus Intrinsic
	found := false
	for _, models := range snap.Entries {
		for id, entry := range models {
			candidate := normalizeModelID(id)
			if baseName {
				candidate = modelBaseName(candidate)
			}
			if candidate != model {
				continue
			}
			intrinsic := entry.Intrinsic
			if !found {
				consensus = intrinsic
				found = true
				continue
			}
			if intrinsic != consensus {
				return Intrinsic{}, false
			}
		}
	}
	return consensus, found
}

func normalizeModelID(model string) string {
	return strings.ToLower(strings.TrimSpace(model))
}

func modelBaseName(model string) string {
	if i := strings.LastIndexByte(model, '/'); i >= 0 && i+1 < len(model) {
		return model[i+1:]
	}
	return model
}

// Warm ensures a fresh-enough snapshot is cached, fetching one when the
// cache is empty or stale. Call it once at startup (in a
// lifecycle-managed goroutine) so subsequent Lookup calls are cache hits
// and never block the caller on HTTP. The returned error is for logging
// only — a failure is non-fatal, since Lookup serves a stale snapshot
// when one exists and the static tables when one doesn't.
func (s *Source) Warm(ctx context.Context) error {
	_, _, err := s.ensureSnapshot(ctx)
	return err
}

// ensureSnapshot returns the cached snapshot, fetching and caching a
// fresh one when the cache is empty or stale. ok reports whether the
// returned snapshot is usable: true when it came from cache or a
// successful fetch, false only when there is no cache and the fetch
// failed. err carries any fetch/cache failure so a caller (Warm) can log
// it even when a stale snapshot is still served.
func (s *Source) ensureSnapshot(ctx context.Context) (Snapshot, bool, error) {
	s.mu.RLock()
	snap, ok, retryAt := s.snapshot, s.hasSnap, s.retryAt
	s.mu.RUnlock()
	if ok && time.Since(snap.FetchedAt) <= s.ttl {
		return snap, true, nil
	}
	if ok && (time.Now().Before(retryAt) || !s.refreshMu.TryLock()) {
		return snap, true, nil
	}
	if !ok {
		s.refreshMu.Lock()
	}
	defer s.refreshMu.Unlock()

	s.mu.RLock()
	snap, ok = s.snapshot, s.hasSnap
	s.mu.RUnlock()
	if ok && time.Since(snap.FetchedAt) <= s.ttl {
		return snap, true, nil
	}

	cached, gotFromCache := snap, ok
	if !ok {
		var gerr error
		cached, gerr = s.store.Get(ctx, snapshotKey)
		gotFromCache = gerr == nil && cached.Schema == snapshotSchema
		if gotFromCache {
			cached = loadSnapshot(cached)
		}
		if gotFromCache && time.Since(cached.FetchedAt) <= s.ttl {
			s.mu.Lock()
			s.snapshot, s.hasSnap, s.retryAt = cached, true, time.Time{}
			s.mu.Unlock()
			return cached, true, nil
		}
	}
	fresh, ferr := s.fetch(ctx)
	if ferr != nil {
		// Serve stale if we had one in memory or on disk; otherwise no usable
		// snapshot. Keep it process-local so subsequent lookups do not repeat the
		// same disk read while the upstream is unavailable.
		if gotFromCache {
			s.mu.Lock()
			s.snapshot, s.hasSnap = cached, true
			s.retryAt = time.Now().Add(time.Minute)
			s.mu.Unlock()
		}
		return cached, gotFromCache, ferr
	}
	s.mu.Lock()
	s.snapshot, s.hasSnap, s.retryAt = fresh, true, time.Time{}
	s.mu.Unlock()
	if serr := s.store.Set(ctx, snapshotKey, cloneSnapshot(fresh)); serr != nil {
		return fresh, true, fmt.Errorf("modelsdev: cache snapshot: %w", serr)
	}
	return fresh, true, nil
}

// providerAlias maps zarlcode internal provider names to models.dev
// top-level keys. Custom/local providers not in the map return their
// own name unmodified (they won't match, but that's correct — they
// aren't in the index).
func providerAlias(name string) string {
	switch name {
	case "gemini", "google-vertex":
		return providerGoogle
	default:
		return name
	}
}

func (s *Source) fetch(ctx context.Context) (Snapshot, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL, nil)
	if err != nil {
		return Snapshot{}, fmt.Errorf("modelsdev: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := fetchClient.Do(ctx, req)
	if err != nil {
		return Snapshot{}, fmt.Errorf("modelsdev: fetch %s: %w", s.baseURL, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return Snapshot{}, fmt.Errorf("modelsdev: read body: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		preview := string(body)
		if len(preview) > 200 {
			preview = preview[:200] + "…"
		}
		return Snapshot{}, fmt.Errorf("modelsdev: status %d: %s", resp.StatusCode, preview)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return Snapshot{}, fmt.Errorf("modelsdev: decode top-level: %w", err)
	}
	entries := make(map[string]map[string]Entry, len(raw))
	for providerKey, blob := range raw {
		var p providerWire
		if err := json.Unmarshal(blob, &p); err != nil {
			continue // skip unparseable provider blocks
		}
		if len(p.Models) == 0 {
			continue
		}
		models := make(map[string]Entry, len(p.Models))
		for id, blob := range p.Models {
			// The outer decode guarantees valid JSON. Non-object records have no
			// model quantities to validate and must not discard valid neighbors.
			if bytes.TrimSpace(blob)[0] != '{' {
				continue
			}
			// Decode quantities first: malformed numeric representations invalidate
			// the entire refresh, never publish a partially decoded catalogue.
			var quantities struct {
				Limit limitWire `json:"limit"`
				Cost  costWire  `json:"cost"`
			}
			if err := json.Unmarshal(blob, &quantities); err != nil {
				return Snapshot{}, fmt.Errorf("modelsdev: decode quantities for %q/%q: %w", providerKey, id, err)
			}
			var m *modelWire
			// Other invalid records, nulls, and conflicting embedded identities
			// are skipped without manufacturing a replacement identity.
			if err := json.Unmarshal(blob, &m); err != nil || m == nil ||
				id == "" || (m.ID != "" && m.ID != id) {
				continue
			}
			models[id] = decodeEntry(*m)
		}
		entries[providerKey] = models
	}
	digest := sha256.Sum256(body)
	return Snapshot{
		Schema:    snapshotSchema,
		FetchedAt: time.Now(),
		Revision:  hex.EncodeToString(digest[:]),
		Entries:   entries,
	}, nil
}

// --- wire types for deserialisation ---

type providerWire struct {
	Models map[string]json.RawMessage `json:"models"`
}

type modelWire struct {
	ID         string         `json:"id"`
	ToolCall   *bool          `json:"tool_call"`
	Reasoning  *bool          `json:"reasoning"`
	Modalities modalitiesWire `json:"modalities"`
	Limit      limitWire      `json:"limit"`
	Cost       costWire       `json:"cost"`
}

type modalitiesWire struct {
	Input  modalityListWire `json:"input"`
	Output modalityListWire `json:"output"`
}

// modalityListWire preserves missing/null lists but rejects null list elements.
// encoding/json otherwise silently decodes those elements as empty strings.
type modalityListWire []string

func (values *modalityListWire) UnmarshalJSON(data []byte) error {
	var raw []*string
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("decode modality list: %w", err)
	}
	if raw == nil {
		*values = nil
		return nil
	}
	parsed := make(modalityListWire, len(raw))
	for i, value := range raw {
		if value == nil {
			return fmt.Errorf("null modality at index %d", i)
		}
		parsed[i] = *value
	}
	*values = parsed
	return nil
}

type limitWire struct {
	Input   int `json:"input"`
	Context int `json:"context"`
	Output  int `json:"output"`
}

type costWire struct {
	Input  *float64 `json:"input"`
	Output *float64 `json:"output"`
}
