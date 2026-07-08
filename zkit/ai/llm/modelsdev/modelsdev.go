package modelsdev

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/zarldev/zarlmono/zkit/cache"
	"github.com/zarldev/zarlmono/zkit/options"
	"github.com/zarldev/zarlmono/zkit/zhttp"
)

const (
	defaultBaseURL = "https://models.dev/api.json"
	providerGoogle = "google"
	snapshotKey    = "models.dev"
	defaultTTL     = 6 * time.Hour
)

// fetchClient is the shared client for models.dev/api.json. zhttp's
// defaults give retry + backoff + timeout + Retry-After honouring on
// transient failures.
var fetchClient = zhttp.NewClient(zhttp.WithUserAgent("zarlmono/1"))

// Entry is a single model's metadata parsed from models.dev.
type Entry struct {
	InputCostPerMTok  float64
	OutputCostPerMTok float64
	ContextWindow     int
	MaxOutputTokens   int
	SupportsTools     bool
	SupportsVision    bool
	SupportsThinking  bool
	SupportsVideo     bool
}

// Snapshot is the cached whole-fetch blob.
type Snapshot struct {
	FetchedAt time.Time                   `json:"fetched_at"`
	Entries   map[string]map[string]Entry `json:"entries"`
}

// Source looks up model info from a cached models.dev snapshot,
// refreshing on TTL expiry. The zero value is not usable; construct
// with [New].
type Source struct {
	store   cache.Cache[string, Snapshot]
	ttl     time.Duration
	baseURL string
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
	snap, gerr := s.store.Get(ctx, snapshotKey)
	gotFromCache := gerr == nil
	if gotFromCache && time.Since(snap.FetchedAt) <= s.ttl {
		return snap, true, nil
	}
	fresh, ferr := s.fetch(ctx)
	if ferr != nil {
		// Serve stale if we had one; otherwise no usable snapshot.
		return snap, gotFromCache, ferr
	}
	if serr := s.store.Set(ctx, snapshotKey, fresh); serr != nil {
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
		for _, m := range p.Models {
			if m.ID == "" {
				continue
			}
			e := Entry{
				ContextWindow:    m.Limit.Context,
				MaxOutputTokens:  m.Limit.Output,
				SupportsTools:    m.ToolCall,
				SupportsThinking: m.Reasoning,
				SupportsVision:   hasVision(m.Modalities.Input),
				SupportsVideo:    hasVideo(m.Modalities.Input),
			}
			if m.Cost.Input > 0 || m.Cost.Output > 0 {
				e.InputCostPerMTok = m.Cost.Input
				e.OutputCostPerMTok = m.Cost.Output
			}
			models[m.ID] = e
		}
		entries[providerKey] = models
	}
	return Snapshot{
		FetchedAt: time.Now(),
		Entries:   entries,
	}, nil
}

func hasVision(inputs []string) bool {
	for _, m := range inputs {
		if strings.EqualFold(m, "image") {
			return true
		}
	}
	return false
}

func hasVideo(inputs []string) bool {
	for _, m := range inputs {
		if strings.EqualFold(m, "video") {
			return true
		}
	}
	return false
}

// --- wire types for deserialisation ---

type providerWire struct {
	Models []modelWire `json:"models"`
}

type modelWire struct {
	ID         string         `json:"id"`
	ToolCall   bool           `json:"tool_call"`
	Reasoning  bool           `json:"reasoning"`
	Modalities modalitiesWire `json:"modalities"`
	Limit      limitWire      `json:"limit"`
	Cost       costWire       `json:"cost"`
}

type modalitiesWire struct {
	Input  []string `json:"input"`
	Output []string `json:"output"`
}

type limitWire struct {
	Context int `json:"context"`
	Output  int `json:"output"`
}

type costWire struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
}
