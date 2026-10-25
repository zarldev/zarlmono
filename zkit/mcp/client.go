package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ToolDef is the MCP wire format for a tool definition. InputSchema is
// kept as raw JSON-shaped data so features like anyOf, array items,
// minimum/maximum survive through to the LLM without a lossy round-trip.
//
// Distinct from any tool-registry "Tool" type — this is the protocol
// surface, the bridge to a typed Tool interface lives in a tools-aware
// package.
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// Client talks to an MCP server using JSON-RPC 2.0. The wire transport
// is pluggable — HTTP for remote servers, stdio for local subprocess
// servers.
type Client struct {
	transport Transport
	nextID    atomic.Int64
	negotiate bool
	readyGate chan struct{}
	protocolM sync.Mutex
	protocol  string
	ready     bool

	subM     sync.Mutex
	subs     map[string][]subscription // method → handlers
	subAny   []anySubscription         // method-agnostic catch-all handlers
	subWired bool                      // true once OnNotification is registered with transport
}

// subscription is a live Subscribe registration. nextSubID makes each
// handle unique so Unsubscribe can find-and-remove in O(n).
type subscription struct {
	id      int64
	handler NotificationHandler
}

// anySubscription is the same shape but for SubscribeAny — the handler
// also receives the method name, since it doesn't know it ahead of time.
type anySubscription struct {
	id      int64
	handler AnyNotificationHandler
}

var nextSubID atomic.Int64

// NotificationHandler receives the `params` payload of a JSON-RPC
// notification pushed by the server. The handler must not block — long
// work should be dispatched to a goroutine owned by the caller.
type NotificationHandler func(params json.RawMessage)

// AnyNotificationHandler receives every server-pushed notification
// regardless of method. The method name is passed alongside the params
// so a single handler can route or format by method. Same blocking
// constraint as NotificationHandler.
type AnyNotificationHandler func(method string, params json.RawMessage)

// NewClient creates a Client that talks to an MCP server over
// HTTP with no per-dial address policy — appropriate for callers
// that have other guards in place or are connecting to a trusted
// internal endpoint. Most callers should use
// NewClientWithDialPolicy instead, which closes the DNS-rebinding
// window between config-time URL validation and the actual TCP
// dial.
func NewClient(baseURL, authToken string) *Client {
	return newNegotiatingClient(newHTTPTransport(baseURL, authToken, nil, false))
}

// NewClientWithDialPolicy is like [NewClient] but installs policy
// as the per-dial address filter on the underlying HTTP
// transport. Every dial (first connect, reconnect, redirect)
// re-resolves the hostname and rejects any resolved IP for which
// policy returns false — closing the rebinding gap a one-shot
// URL validation can't.
func NewClientWithDialPolicy(baseURL, authToken string, policy AddrPolicy) *Client {
	return newNegotiatingClient(newHTTPTransport(baseURL, authToken, policy, false))
}

// NewLoopbackClient connects to an operator-approved local HTTP endpoint.
// Every dial must resolve to loopback; proxies and redirects are disabled so
// the endpoint cannot expand its authority to another service. The caller owns
// approval and credential-transport validation before constructing the client.
func NewLoopbackClient(baseURL, authToken string) *Client {
	return newNegotiatingClient(newHTTPTransport(baseURL, authToken,
		func(ip netip.Addr) bool { return ip.Unmap().IsLoopback() }, true))
}

// stdioInitTimeout caps the initialize handshake at construction
// time. A misbehaving subprocess that starts cleanly but never
// replies on stdout used to block NewStdioClient forever — the
// constructor passed context.Background() with no deadline. Most
// real servers respond in milliseconds; 10s is a generous cap
// that's still bounded.
const stdioInitTimeout = 10 * time.Second

// NewStdioClient creates a Client that launches command+args as a
// subprocess and talks JSON-RPC 2.0 over stdin/stdout. Env is merged on
// top of the parent process environment. The initialize handshake is
// bounded by [stdioInitTimeout] — use [NewStdioClientContext] to
// pass an explicit caller context.
func NewStdioClient(command string, args []string, env map[string]string) (*Client, error) {
	ctx, cancel := context.WithTimeout(context.Background(), stdioInitTimeout)
	defer cancel()
	return NewStdioClientContext(ctx, command, args, env)
}

// NewStdioClientContext is NewStdioClient with caller-provided ctx.
// The ctx is used only for the initialize handshake; once
// construction returns successfully, the client's lifetime is
// independent of ctx. A cancelled / deadline-exceeded ctx during
// initialize tears the subprocess down before returning so a hung
// server cannot leak processes.
func NewStdioClientContext(ctx context.Context, command string, args []string, env map[string]string) (*Client, error) {
	t, err := newStdioTransport(command, args, env)
	if err != nil {
		return nil, err
	}
	c := newNegotiatingClient(t)
	// Stdio servers generally require an `initialize` handshake before any
	// other calls will succeed. HTTP servers we've connected to are
	// tolerant of skipping it; we do it here to keep stdio reliable.
	if err := c.ensureReady(ctx); err != nil {
		_ = t.Close()
		return nil, fmt.Errorf("mcp initialize: %w", err)
	}
	return c, nil
}

// NewClientWithTransport wraps an arbitrary transport.
func NewClientWithTransport(t Transport) *Client {
	return &Client{transport: t}
}

// Subscribe registers handler for server-pushed notifications with the
// given JSON-RPC method. Returns a cancel function that unregisters
// the handler. Transports that don't surface notifications (the current
// HTTP transport) silently swallow pushes — the cancel still works but
// the handler was never going to fire.
func (c *Client) Subscribe(method string, handler NotificationHandler) func() {
	c.subM.Lock()
	if c.subs == nil {
		c.subs = make(map[string][]subscription)
	}
	if !c.subWired {
		if nr, ok := c.transport.(NotificationReceiver); ok {
			nr.OnNotification(c.dispatchNotification)
		}
		c.subWired = true
	}
	subID := nextSubID.Add(1)
	c.subs[method] = append(c.subs[method], subscription{id: subID, handler: handler})
	c.subM.Unlock()

	return func() {
		c.subM.Lock()
		defer c.subM.Unlock()
		list := c.subs[method]
		for i, s := range list {
			if s.id == subID {
				c.subs[method] = append(list[:i], list[i+1:]...)
				return
			}
		}
	}
}

// SubscribeAny registers handler for every server-pushed notification
// regardless of method. The bridge between MCP servers and an agent's
// inject queue uses this — the agent doesn't know up-front which
// notification methods a given server will publish (long-running task
// completion, resource updates, custom server events), and a
// per-method Subscribe would miss anything not enumerated.
//
// Same blocking constraint as Subscribe — the handler runs on the
// transport's reader goroutine. Push to a channel or call a
// non-blocking enqueue function.
//
// Returns a cancel function that unregisters the handler.
func (c *Client) SubscribeAny(handler AnyNotificationHandler) func() {
	c.subM.Lock()
	if !c.subWired {
		if nr, ok := c.transport.(NotificationReceiver); ok {
			nr.OnNotification(c.dispatchNotification)
		}
		c.subWired = true
	}
	subID := nextSubID.Add(1)
	c.subAny = append(c.subAny, anySubscription{id: subID, handler: handler})
	c.subM.Unlock()

	return func() {
		c.subM.Lock()
		defer c.subM.Unlock()
		for i, s := range c.subAny {
			if s.id == subID {
				c.subAny = append(c.subAny[:i], c.subAny[i+1:]...)
				return
			}
		}
	}
}

// dispatchNotification is the transport-level hook registered on first
// Subscribe / SubscribeAny. Fans a single incoming notification out to
// every method-specific handler AND every catch-all handler. Runs on
// the transport's reader goroutine, so handlers must not block.
func (c *Client) dispatchNotification(method string, params json.RawMessage) {
	c.subM.Lock()
	handlers := append([]subscription(nil), c.subs[method]...)
	anyHandlers := append([]anySubscription(nil), c.subAny...)
	c.subM.Unlock()
	for _, s := range handlers {
		s.handler(params)
	}
	for _, s := range anyHandlers {
		s.handler(method, params)
	}
}

// Close releases transport resources (e.g. terminates the stdio subprocess).
func (c *Client) Close() error {
	return c.transport.Close()
}

func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if err := c.ensureReady(ctx); err != nil {
		return nil, err
	}
	c.protocolM.Lock()
	modern := c.protocol == protocolVersion20260728
	c.protocolM.Unlock()
	if modern {
		if method == methodToolsCall {
			if err := c.ensureSchemaHeaders(ctx); err != nil {
				return nil, err
			}
		}
		prepared, err := paramsFor20260728(params)
		if err != nil {
			return nil, err
		}
		params = prepared
	}
	return c.rawCall(ctx, method, params, modern)
}

func newNegotiatingClient(transport Transport) *Client {
	client := &Client{transport: transport, negotiate: true, readyGate: make(chan struct{}, 1)}
	if httpTransport, ok := transport.(*httpTransport); ok {
		httpTransport.ensureReady = client.ensureReady
		httpTransport.invalidateSession = client.invalidateHTTPSession
	}
	return client
}

func (c *Client) invalidateHTTPSession(expired string) {
	c.protocolM.Lock()
	if transport, ok := c.transport.(*httpTransport); ok {
		if !transport.clearExpiredSession(expired) {
			c.protocolM.Unlock()
			return
		}
	}
	c.protocol = ""
	c.ready = false
	c.protocolM.Unlock()
}

func (c *Client) rawCall(ctx context.Context, method string, params any, modern bool) (json.RawMessage, error) {
	// Ordinary requests use increasing IDs. Negotiation uses reserved negative
	// IDs under the readiness gate, keeping the namespaces distinct.
	var id int64
	switch method {
	case "server/discover":
		id = -1
	case methodInitialize:
		id = -2
	default:
		id = c.nextID.Add(1)
	}
	rawID := strconv.AppendInt(nil, id, 10)
	resp, err := c.transport.Call(ctx, rpcRequest{
		JSONRPC: jsonrpcVersion,
		ID:      rawID,
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return nil, err
	}
	if resp.JSONRPC != jsonrpcVersion || string(resp.ID) != string(rawID) || (resp.Error == nil) == (len(resp.Result) == 0 || string(resp.Result) == "null") {
		return nil, errors.New("invalid MCP response envelope")
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	if modern {
		if err := validateCompleteResult(resp.Result); err != nil {
			return nil, err
		}
	}
	return resp.Result, nil
}

type initializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ClientInfo      map[string]any `json:"clientInfo"`
}

// ClientInfo identifies this client to MCP servers during initialize.
// Override at construction time if a server cares about caller identity.
var ClientInfo = map[string]any{"name": "mcp-go-client", "version": "0.1.0"}

// initialize performs the MCP handshake. It is idempotent per Client so
// it's safe to call multiple times; servers may return different
// capabilities on each call, but we don't inspect them.
func (c *Client) initialize(ctx context.Context) error {
	raw, err := c.rawCall(ctx, methodInitialize, initializeParams{ProtocolVersion: protocolVersion20251125, Capabilities: map[string]any{}, ClientInfo: ClientInfo}, false)
	if err != nil {
		return err
	}
	result, err := validateInitializeResult(raw)
	if err != nil {
		return err
	}
	if transport, ok := c.transport.(*httpTransport); ok {
		transport.setProtocol(result.ProtocolVersion)
	}
	if notifier, ok := c.transport.(interface {
		Notify(context.Context, rpcNotification) error
	}); ok {
		if err := notifier.Notify(ctx, rpcNotification{JSONRPC: jsonrpcVersion, Method: "notifications/initialized"}); err != nil {
			return fmt.Errorf("send initialized: %w", err)
		}
	}
	c.setReady(result.ProtocolVersion)
	return nil
}

// listResult is the result of a tools/list call.
type listResult struct {
	Tools      []ToolDef `json:"tools"`
	NextCursor string    `json:"nextCursor,omitempty"`
}

// Discover calls tools/list and returns the available tool definitions.
// Each ToolDef gets a default empty-object InputSchema if the server
// returned nil, so consumers can pass it to a JSON-Schema validator
// without nil checks.
func (c *Client) Discover(ctx context.Context) ([]ToolDef, error) {
	var tools []ToolDef
	cursor := ""
	seen := make(map[string]bool)
	for range 128 {
		var params any
		if cursor != "" {
			params = map[string]string{"cursor": cursor}
		}
		raw, err := c.call(ctx, "tools/list", params)
		if err != nil {
			return nil, fmt.Errorf("tools/list: %w", err)
		}
		var page listResult
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, fmt.Errorf("parse tools/list page: %w", err)
		}
		if len(tools)+len(page.Tools) > 10000 {
			return nil, errors.New("MCP tool discovery exceeds definition limit")
		}
		tools = append(tools, page.Tools...)
		if page.NextCursor == "" {
			combined, err := json.Marshal(listResult{Tools: tools})
			if err != nil {
				return nil, fmt.Errorf("combine tool definitions: %w", err)
			}
			return c.decodeDiscoveredTools(combined, c.modernProtocol())
		}
		if seen[page.NextCursor] {
			return nil, errors.New("MCP tool discovery repeated a cursor")
		}
		seen[page.NextCursor] = true
		cursor = page.NextCursor
	}
	return nil, errors.New("MCP tool discovery exceeds page limit")
}

func (c *Client) modernProtocol() bool {
	c.protocolM.Lock()
	defer c.protocolM.Unlock()
	return c.protocol == protocolVersion20260728
}

func (c *Client) ensureSchemaHeaders(ctx context.Context) error {
	transport, ok := c.transport.(*httpTransport)
	if !ok || transport.schemaHeadersReady() {
		return nil
	}
	_, err := c.Discover(ctx)
	return err
}

func (c *Client) decodeDiscoveredTools(raw json.RawMessage, modern bool) ([]ToolDef, error) {
	var result listResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parse tools/list result: %w", err)
	}

	bindings := make(map[string][]schemaHeaderBinding)
	invalidBindings := make(map[string]bool)
	tools := make([]ToolDef, 0, len(result.Tools))
	for _, tool := range result.Tools {
		if tool.InputSchema == nil {
			tool.InputSchema = map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}
		}
		if modern {
			toolBindings, err := schemaHeaderBindings(tool.InputSchema)
			if err != nil {
				invalidBindings[tool.Name] = true
				continue
			}
			bindings[tool.Name] = toolBindings
		}
		tools = append(tools, tool)
	}
	if modern {
		if transport, ok := c.transport.(*httpTransport); ok {
			transport.setSchemaHeaders(bindings, invalidBindings)
		}
	}
	return tools, nil
}

// callParams are the params for a tools/call request.
type callParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// Content is the sealed interface for an MCP tool result content item.
// MCP servers may return text, image, audio, or resource items in any
// combination and order; consumers switch on the concrete type.
type Content interface {
	// ContentType returns the wire-format discriminator ("text",
	// "image", "audio", "resource"). Provided so callers can route
	// without a type switch when convenient.
	ContentType() string
	isContent()
}

// TextContent is plain UTF-8 text — the most common content type.
type TextContent struct {
	Text string `json:"text"`
}

// ContentType returns "text".
func (TextContent) ContentType() string { return "text" }
func (TextContent) isContent()          {}

// ImageContent is a base64-encoded image with a MIME type.
type ImageContent struct {
	Data     string `json:"data"`
	MIMEType string `json:"mimeType"`
}

// ContentType returns "image".
func (ImageContent) ContentType() string { return "image" }
func (ImageContent) isContent()          {}

// AudioContent is a base64-encoded audio clip with a MIME type.
type AudioContent struct {
	Data     string `json:"data"`
	MIMEType string `json:"mimeType"`
}

// ContentType returns "audio".
func (AudioContent) ContentType() string { return "audio" }
func (AudioContent) isContent()          {}

// ResourceContent is a reference to a server-side resource. URI is
// always populated; Text or Blob carry the inlined content when the
// server included it (servers may embed text directly or link by URI
// alone, leaving fetch to a follow-up resources/read call).
type ResourceContent struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"` // base64
}

// ContentType returns "resource".
func (ResourceContent) ContentType() string { return "resource" }
func (ResourceContent) isContent()          {}

// CallResult is what tools/call returns. Content carries every item
// the server emitted — text + image + audio + resource — in arrival
// order. IsError is the MCP-level error flag: when true, Content
// typically contains a TextContent describing the failure.
type CallResult struct {
	Content []Content
	IsError bool
}

// FirstText returns the text from the first TextContent in the result,
// or "" if none. Convenience for callers that only care about text.
func (r CallResult) FirstText() string {
	for _, c := range r.Content {
		if t, ok := c.(TextContent); ok {
			return t.Text
		}
	}
	return ""
}

// AllText returns the joined text of every TextContent in the result,
// separated by blank lines. Non-text content is skipped.
func (r CallResult) AllText() string {
	var parts []string
	for _, c := range r.Content {
		if t, ok := c.(TextContent); ok {
			parts = append(parts, t.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// UnmarshalJSON decodes the typed Content slice. Each item is dispatched
// by its `type` discriminator; unknown types are skipped silently so a
// future MCP content type doesn't break callers that don't yet
// understand it.
func (r *CallResult) UnmarshalJSON(data []byte) error {
	var raw struct {
		Content []json.RawMessage `json:"content"`
		IsError bool              `json:"isError,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	r.IsError = raw.IsError
	r.Content = r.Content[:0]
	for _, item := range raw.Content {
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(item, &probe); err != nil {
			return fmt.Errorf("decode content type: %w", err)
		}
		switch probe.Type {
		case contentTypeText:
			var t TextContent
			if err := json.Unmarshal(item, &t); err != nil {
				return fmt.Errorf("decode text content: %w", err)
			}
			r.Content = append(r.Content, t)
		case contentTypeImage:
			var i ImageContent
			if err := json.Unmarshal(item, &i); err != nil {
				return fmt.Errorf("decode image content: %w", err)
			}
			r.Content = append(r.Content, i)
		case contentTypeAudio:
			var a AudioContent
			if err := json.Unmarshal(item, &a); err != nil {
				return fmt.Errorf("decode audio content: %w", err)
			}
			r.Content = append(r.Content, a)
		case contentTypeResource:
			var rc ResourceContent
			if err := json.Unmarshal(item, &rc); err != nil {
				return fmt.Errorf("decode resource content: %w", err)
			}
			r.Content = append(r.Content, rc)
		default:
			// Unknown content type — skip silently. Logged at debug
			// would be appropriate but the protocol package doesn't
			// take a logger dependency.
		}
	}
	return nil
}

// readResourceParams are the params for a resources/read request.
type readResourceParams struct {
	URI string `json:"uri"`
}

// readResourceResult is the result of a resources/read call.
type readResourceResult struct {
	Contents []json.RawMessage `json:"contents"`
}

// ReadResource fetches the contents of a resource referenced by URI.
// MCP servers may return ResourceContent items with URI alone (no
// inline Text/Blob); ReadResource resolves those to actual content.
//
// The returned slice is the dispatched-on-type Content variants: a
// resource read can yield text, image, audio, or further resource
// references (rare). Most calls return a single TextContent or
// ImageContent.
func (c *Client) ReadResource(ctx context.Context, uri string) ([]Content, error) {
	raw, err := c.call(ctx, methodResourcesRead, readResourceParams{URI: uri})
	if err != nil {
		return nil, fmt.Errorf("resources/read %s: %w", uri, err)
	}
	var result readResourceResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parse resources/read result: %w", err)
	}
	out := make([]Content, 0, len(result.Contents))
	for _, item := range result.Contents {
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(item, &probe); err != nil {
			return nil, fmt.Errorf("decode content type: %w", err)
		}
		switch probe.Type {
		case contentTypeText:
			var t TextContent
			if err := json.Unmarshal(item, &t); err != nil {
				return nil, fmt.Errorf("decode text content: %w", err)
			}
			out = append(out, t)
		case contentTypeImage:
			var i ImageContent
			if err := json.Unmarshal(item, &i); err != nil {
				return nil, fmt.Errorf("decode image content: %w", err)
			}
			out = append(out, i)
		case contentTypeAudio:
			var a AudioContent
			if err := json.Unmarshal(item, &a); err != nil {
				return nil, fmt.Errorf("decode audio content: %w", err)
			}
			out = append(out, a)
		case contentTypeResource:
			var rc ResourceContent
			if err := json.Unmarshal(item, &rc); err != nil {
				return nil, fmt.Errorf("decode resource content: %w", err)
			}
			out = append(out, rc)
		default:
			// Unknown content type — skip silently.
		}
	}
	return out, nil
}

// Call invokes a tool by name with the given arguments. The returned
// CallResult carries every content item (text/image/audio/resource) the
// server produced, plus an IsError flag for tool-reported failures
// (distinct from transport errors, which are returned as the second
// value).
func (c *Client) Call(ctx context.Context, name string, args map[string]any) (CallResult, error) {
	raw, err := c.call(ctx, methodToolsCall, callParams{Name: name, Arguments: args})
	if err != nil {
		return CallResult{}, fmt.Errorf("tools/call %s: %w", name, err)
	}
	var result CallResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return CallResult{}, fmt.Errorf("parse tools/call result: %w", err)
	}
	return result, nil
}
