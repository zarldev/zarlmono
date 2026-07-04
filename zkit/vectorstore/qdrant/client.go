// Package qdrant provides a thin REST client for the Qdrant vector
// database. vectorstore is currently Qdrant-only; an interface in the
// zkit/vectorstore/ root will be added when (and only when) a second
// backend is needed.
package qdrant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/zarldev/zarlmono/zkit/zhttp"
)

// Response-size caps. A misbehaving / hostile Qdrant filer can
// otherwise return arbitrarily large bodies and force the client to
// allocate without bound. Sized for typical RAG payloads — a single
// search returns tens of points; a scroll returns ~1k points by
// default. 16 MiB covers both with room.
const (
	qdrantResponseCapBytes = 16 * 1024 * 1024
	qdrantErrorBodyCap     = 4 * 1024
)

// ErrResponseTooLarge is returned when a Qdrant response body
// exceeds [qdrantResponseCapBytes].
var ErrResponseTooLarge = errors.New("qdrant: response exceeded size cap")

// Payload carries Qdrant point metadata. The underlying representation remains
// JSON-shaped for Qdrant compatibility, but the semantic type keeps payloads
// distinct from arbitrary option/argument maps in zkit APIs.
type Payload map[string]any

// Set adds or updates a payload value. Values should be JSON-serialisable.
func (p Payload) Set(key string, value any) { p[key] = value }

// Get retrieves a payload value.
func (p Payload) Get(key string) (any, bool) {
	v, ok := p[key]
	return v, ok
}

// Point is a vector with an ID and optional metadata.
type Point struct {
	ID      string
	Vector  []float32
	Payload Payload
}

// ScoredPoint is a Point returned from a search with a similarity score.
type ScoredPoint struct {
	Point
	Score float32
}

// Filter narrows a search or delete operation.
type Filter struct {
	Must []FieldCondition `json:"must,omitempty"`
}

// FieldCondition matches points where a payload field equals a value.
type FieldCondition struct {
	Key   string     `json:"key"`
	Match MatchValue `json:"match"`
}

// MatchValue holds the value to match against. Qdrant accepts strings,
// numbers, booleans — `any` matches the wire format's flexibility.
type MatchValue struct {
	Value any `json:"value"`
}

// Client is a thin REST client for the Qdrant vector database.
//
// HTTP transport is provided by [zhttp.Client] — retry on transient
// 5xx / 429 + network errors, Retry-After honour, and exponential
// backoff with jitter. Bodies are constructed from *bytes.Reader so
// retries can replay the JSON payload across attempts.
//
// URL construction goes through a parsed [*url.URL] base; each
// request path is joined via JoinPath rather than raw string
// concatenation. Earlier shape did `c.baseURL + path` — a baseURL
// with a trailing path, query string, or missing trailing-slash
// produced surprising endpoints (and unescaped query characters in a
// path component would have changed the request shape entirely).
type Client struct {
	baseURL *url.URL
	http    *zhttp.Client
}

// NewClient creates a Qdrant client backed by the default
// [zhttp.Client] — 30 s per-request timeout, 3-attempt retry on
// transient failures. An invalid baseURL becomes a parse-time error
// rather than failing on first request.
func NewClient(baseURL string) *Client {
	c, err := newClient(baseURL, zhttp.NewClient())
	if err != nil {
		// Preserve the previous always-construct-something behaviour;
		// callers that handed in junk will see request-time errors.
		return &Client{http: zhttp.NewClient()}
	}
	return c
}

// NewClientWithZHTTP creates a Qdrant client backed by a caller-
// supplied [zhttp.Client]. Useful when the caller needs a custom
// retry policy, longer timeout, or a stub transport for tests.
func NewClientWithZHTTP(baseURL string, h *zhttp.Client) *Client {
	c, err := newClient(baseURL, h)
	if err != nil {
		return &Client{http: h}
	}
	return c
}

// collectionURL composes the full Qdrant URL for a collection-rooted
// path. The collection name and any trailing segments are passed
// individually to url.URL.JoinPath which URL-escapes each one — so
// slashes / spaces / "?" / "#" / "%" / etc. in an attacker-influenced
// name can't reshape the request URL.
//
// Earlier shape concatenated raw `baseURL + "/collections/" + PathEscape(name)`,
// which dropped baseURL semantics (trailing path, query, fragment)
// and meant any trailing segment passed by the caller had to be
// pre-escaped at the call site (which it wasn't).
func (c *Client) collectionURL(name string, trailing ...string) string {
	parts := append([]string{"collections", name}, trailing...)
	return c.buildURL(parts...)
}

// collectionURLWithQuery is collectionURL with an explicit query
// string. The query is set verbatim — callers responsible for
// composing it safely (only used internally with hard-coded values
// like "wait=true").
func (c *Client) collectionURLWithQuery(name, query string, trailing ...string) string {
	base := c.collectionURL(name, trailing...)
	if u, err := url.Parse(base); err == nil {
		u.RawQuery = query
		return u.String()
	}
	return base + "?" + query
}

func newClient(baseURL string, h *zhttp.Client) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse qdrant baseURL %q: %w", baseURL, err)
	}
	return &Client{baseURL: u, http: h}, nil
}

// buildURL joins path segments onto the base URL. Each segment is
// passed individually to url.URL.JoinPath so reserved characters in
// a (possibly attacker-influenced) segment get escaped instead of
// reshaping the request URL.
func (c *Client) buildURL(segments ...string) string {
	if c.baseURL == nil {
		return strings.Join(segments, "/")
	}
	return c.baseURL.JoinPath(segments...).String()
}

func (c *Client) do(ctx context.Context, method, urlStr string, body any) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		r = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, urlStr, r)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	return resp, nil
}

// checkStatus inspects a Qdrant response status. Error bodies are
// read with a small cap so a hostile filer can't blow client memory
// just by returning a non-2xx with gigabytes of body.
func checkStatus(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, qdrantErrorBodyCap))
	return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
}

// decodeResponse parses a successful JSON response from resp.Body,
// bounded by [qdrantResponseCapBytes]. Returns
// [ErrResponseTooLarge] if the server tries to push past the cap.
func decodeResponse(resp *http.Response, out any) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, qdrantResponseCapBytes+1))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if int64(len(body)) > qdrantResponseCapBytes {
		return fmt.Errorf("%w (cap %d)", ErrResponseTooLarge, qdrantResponseCapBytes)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// EnsureCollection creates the collection if it does not exist.
// Idempotent — a second call with the same name is a no-op.
func (c *Client) EnsureCollection(ctx context.Context, name string, vectorSize int) error {
	resp, err := c.do(ctx, http.MethodGet, c.collectionURL(name), nil)
	if err != nil {
		return fmt.Errorf("check collection: %w", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}

	payload := map[string]any{
		"vectors": map[string]any{
			"size":     vectorSize,
			"distance": "Cosine",
		},
	}
	resp, err = c.do(ctx, http.MethodPut, c.collectionURL(name), payload)
	if err != nil {
		return fmt.Errorf("create collection: %w", err)
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return fmt.Errorf("create collection: %w", err)
	}
	return nil
}

type wirePoint struct {
	ID      string    `json:"id"`
	Vector  []float32 `json:"vector"`
	Payload Payload   `json:"payload,omitempty"`
}

// Upsert inserts or updates points in the collection.
func (c *Client) Upsert(ctx context.Context, collection string, points []Point) error {
	wps := make([]wirePoint, len(points))
	for i, p := range points {
		wps[i] = wirePoint(p)
	}
	body := map[string]any{"points": wps}

	resp, err := c.do(ctx, http.MethodPut, c.collectionURL(collection, "points"), body)
	if err != nil {
		return fmt.Errorf("upsert: %w", err)
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return fmt.Errorf("upsert: %w", err)
	}
	return nil
}

type searchRequest struct {
	Vector      []float32 `json:"vector"`
	Limit       int       `json:"limit"`
	WithPayload bool      `json:"with_payload"`
	Filter      *Filter   `json:"filter,omitempty"`
}

type searchResult struct {
	Result []struct {
		ID      string    `json:"id"`
		Score   float32   `json:"score"`
		Payload Payload   `json:"payload"`
		Vector  []float32 `json:"vector"`
	} `json:"result"`
}

// SearchRequest captures every knob a Search call takes. Filter is
// optional; nil means no filtering. New fields land here without
// breaking callers.
type SearchRequest struct {
	Collection string
	Vector     []float32
	Filter     *Filter
	Limit      int
}

// Search returns the top req.Limit nearest neighbours to req.Vector.
func (c *Client) Search(ctx context.Context, req SearchRequest) ([]ScoredPoint, error) {
	body := searchRequest{
		Vector:      req.Vector,
		Limit:       req.Limit,
		WithPayload: true,
		Filter:      req.Filter,
	}

	resp, err := c.do(ctx, http.MethodPost, c.collectionURL(req.Collection, "points", "search"), body)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	var result searchResult
	if err := decodeResponse(resp, &result); err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	out := make([]ScoredPoint, len(result.Result))
	for i, r := range result.Result {
		out[i] = ScoredPoint{
			Point: Point{ID: r.ID, Vector: r.Vector, Payload: r.Payload},
			Score: r.Score,
		}
	}
	return out, nil
}

// Delete removes all points matching filter from the collection.
// wait=true forces Qdrant to apply the operation before returning —
// without it the default is async and a subsequent search can still
// return the "deleted" point for up to a few seconds.
func (c *Client) Delete(ctx context.Context, collection string, filter Filter) error {
	body := map[string]any{"filter": filter}
	resp, err := c.do(ctx, http.MethodPost,
		c.collectionURLWithQuery(collection, "wait=true", "points", "delete"), body)
	if err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	return nil
}

// DeleteByID removes a single point by its ID. See Delete for why
// wait=true is passed.
func (c *Client) DeleteByID(ctx context.Context, collection, id string) error {
	body := map[string]any{"points": []string{id}}
	resp, err := c.do(ctx, http.MethodPost,
		c.collectionURLWithQuery(collection, "wait=true", "points", "delete"), body)
	if err != nil {
		return fmt.Errorf("delete by id: %w", err)
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return fmt.Errorf("delete by id: %w", err)
	}
	return nil
}

type scrollRequest struct {
	Filter      *Filter `json:"filter,omitempty"`
	Limit       int     `json:"limit"`
	Offset      any     `json:"offset,omitempty"`
	WithPayload bool    `json:"with_payload"`
	WithVector  bool    `json:"with_vector"`
}

type scrollResult struct {
	Result struct {
		Points []struct {
			ID      string  `json:"id"`
			Payload Payload `json:"payload"`
		} `json:"points"`
		NextPageOffset any `json:"next_page_offset"`
	} `json:"result"`
}

// ScrollRequest captures every knob a Scroll call takes. Filter is
// optional (nil = no filtering). Offset is nil on the first call;
// pass the previous response's next-page offset to continue.
type ScrollRequest struct {
	Collection string
	Filter     *Filter
	Limit      int
	Offset     any
}

// Scroll pages through points in a collection without a query vector.
// Returns the points, next-page offset (nil when exhausted), and an error.
func (c *Client) Scroll(ctx context.Context, req ScrollRequest) ([]Point, any, error) {
	body := scrollRequest{
		Filter:      req.Filter,
		Limit:       req.Limit,
		Offset:      req.Offset,
		WithPayload: true,
		WithVector:  false,
	}

	resp, err := c.do(ctx, http.MethodPost, c.collectionURL(req.Collection, "points", "scroll"), body)
	if err != nil {
		return nil, nil, fmt.Errorf("scroll: %w", err)
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return nil, nil, fmt.Errorf("scroll: %w", err)
	}

	var result scrollResult
	if err := decodeResponse(resp, &result); err != nil {
		return nil, nil, fmt.Errorf("scroll: %w", err)
	}

	points := make([]Point, len(result.Result.Points))
	for i, p := range result.Result.Points {
		points[i] = Point{ID: p.ID, Payload: p.Payload}
	}
	return points, result.Result.NextPageOffset, nil
}
