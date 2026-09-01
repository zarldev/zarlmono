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

// ErrEmptyVector is returned when a search vector or an upserted point vector
// has no coordinates.
var ErrEmptyVector = errors.New("qdrant: vector is empty")

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
	ID      PointID
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
// URL construction starts from a validated [Endpoint]. Request path segments
// are escaped before they are joined to the endpoint path.
type Client struct {
	baseURL *url.URL
	http    *zhttp.Client
}

// NewClient creates a Qdrant client backed by the default [zhttp.Client] —
// 30 s per-request timeout and 3-attempt retry on transient failures.
func NewClient(endpoint Endpoint) *Client {
	return NewClientWithZHTTP(endpoint, zhttp.NewClient())
}

// NewClientWithZHTTP creates a Qdrant client backed by a caller-supplied
// [zhttp.Client]. This constructor is useful when the caller needs a custom
// retry policy, longer timeout, or a stub transport for tests.
func NewClientWithZHTTP(endpoint Endpoint, h *zhttp.Client) *Client {
	baseURL := endpoint.url
	return &Client{baseURL: &baseURL, http: h}
}

func (c *Client) collectionURL(name CollectionName, trailing ...string) string {
	parts := append([]string{"collections", string(name)}, trailing...)
	return c.buildURL(parts...)
}

func (c *Client) collectionURLWithQuery(name CollectionName, query string, trailing ...string) string {
	parts := append([]string{"collections", string(name)}, trailing...)
	u := c.joinPath(parts...)
	u.RawQuery = query
	return u.String()
}

// buildURL joins path segments onto the validated endpoint and escapes each
// segment exactly once.
func (c *Client) buildURL(segments ...string) string {
	return c.joinPath(segments...).String()
}

func (c *Client) joinPath(segments ...string) *url.URL {
	u := *c.baseURL
	pathParts := append([]string{strings.TrimSuffix(u.Path, "/")}, segments...)
	escapedParts := make([]string, len(segments)+1)
	escapedParts[0] = strings.TrimSuffix(u.EscapedPath(), "/")
	for index, segment := range segments {
		escapedParts[index+1] = url.PathEscape(segment)
	}
	u.Path = strings.Join(pathParts, "/")
	u.RawPath = strings.Join(escapedParts, "/")
	return &u
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

type createCollectionRequest struct {
	Vectors vectorConfig `json:"vectors"`
}

type vectorConfig struct {
	Size     Dimension `json:"size"`
	Distance Distance  `json:"distance"`
}

// EnsureCollectionExists creates the collection when it does not exist. It does
// not inspect or reconcile an existing collection's configuration.
func (c *Client) EnsureCollectionExists(ctx context.Context, name CollectionName, config CollectionConfig) error {
	resp, err := c.do(ctx, http.MethodGet, c.collectionURL(name), nil)
	if err != nil {
		return fmt.Errorf("check collection: %w", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	if resp.StatusCode != http.StatusNotFound {
		if err := checkStatus(resp); err != nil {
			return fmt.Errorf("check collection: %w", err)
		}
	}

	body := createCollectionRequest{
		Vectors: vectorConfig{Size: config.Dimension, Distance: config.Distance},
	}
	resp, err = c.do(ctx, http.MethodPut, c.collectionURL(name), body)
	if err != nil {
		return fmt.Errorf("create collection: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusConflict {
		return nil
	}
	if err := checkStatus(resp); err != nil {
		return fmt.Errorf("create collection: %w", err)
	}
	return nil
}

type wirePoint struct {
	ID      PointID   `json:"id"`
	Vector  []float32 `json:"vector"`
	Payload Payload   `json:"payload,omitempty"`
}

type upsertRequest struct {
	Points []wirePoint `json:"points"`
}

// Upsert inserts or updates points in the collection.
func (c *Client) Upsert(ctx context.Context, collection CollectionName, points []Point) error {
	wps := make([]wirePoint, len(points))
	for _, point := range points {
		if len(point.Vector) == 0 {
			return ErrEmptyVector
		}
	}
	for i, p := range points {
		wps[i] = wirePoint(p)
	}
	body := upsertRequest{Points: wps}

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

type searchWireRequest struct {
	Vector      []float32 `json:"vector"`
	Limit       Limit     `json:"limit"`
	WithPayload bool      `json:"with_payload"`
	WithVector  bool      `json:"with_vector"`
	Filter      *Filter   `json:"filter,omitempty"`
}

type searchResult struct {
	Result []struct {
		ID      PointID   `json:"id"`
		Score   float32   `json:"score"`
		Payload Payload   `json:"payload"`
		Vector  []float32 `json:"vector"`
	} `json:"result"`
}

// SearchRequest captures every knob a Search call takes. Filter is
// optional; nil means no filtering. New fields land here without
// breaking callers.
type SearchRequest struct {
	Collection CollectionName
	Vector     []float32
	Filter     *Filter
	Limit      Limit
}

// Search returns the top req.Limit nearest neighbours to req.Vector.
func (c *Client) Search(ctx context.Context, req SearchRequest) ([]ScoredPoint, error) {
	if len(req.Vector) == 0 {
		return nil, ErrEmptyVector
	}
	body := searchWireRequest{
		Vector:      req.Vector,
		Limit:       req.Limit,
		WithPayload: true,
		WithVector:  true,
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

type deleteRequest struct {
	Filter Filter `json:"filter"`
}

type deleteByIDRequest struct {
	Points []PointID `json:"points"`
}

// Delete removes all points matching filter from the collection.
// wait=true forces Qdrant to apply the operation before returning —
// without it the default is async and a subsequent search can still
// return the "deleted" point for up to a few seconds.
func (c *Client) Delete(ctx context.Context, collection CollectionName, filter Filter) error {
	body := deleteRequest{Filter: filter}
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
func (c *Client) DeleteByID(ctx context.Context, collection CollectionName, id PointID) error {
	body := deleteByIDRequest{Points: []PointID{id}}
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

type scrollWireRequest struct {
	Filter      *Filter       `json:"filter,omitempty"`
	Limit       Limit         `json:"limit"`
	Offset      *ScrollCursor `json:"offset,omitempty"`
	WithPayload bool          `json:"with_payload"`
	WithVector  bool          `json:"with_vector"`
}

type scrollResult struct {
	Result struct {
		Points []struct {
			ID      PointID `json:"id"`
			Payload Payload `json:"payload"`
		} `json:"points"`
		NextPageOffset *ScrollCursor `json:"next_page_offset"`
	} `json:"result"`
}

// ScrollRequest captures every knob a Scroll call takes. Filter and Cursor are
// optional; pass the previous response's non-nil cursor to continue.
type ScrollRequest struct {
	Collection CollectionName
	Filter     *Filter
	Limit      Limit
	Cursor     *ScrollCursor
}

// Scroll pages through points in a collection without a query vector.
// It returns the points, a nil cursor when exhausted, and an error.
func (c *Client) Scroll(ctx context.Context, req ScrollRequest) ([]Point, *ScrollCursor, error) {
	body := scrollWireRequest{
		Filter:      req.Filter,
		Limit:       req.Limit,
		Offset:      req.Cursor,
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
