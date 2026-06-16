package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/zarldev/zarlmono/zkit/zhttp"
)

// ServerInfo identifies a server during the MCP initialize handshake.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ToolHandler executes a registered tool. args is the raw JSON of the
// caller's arguments map; the handler decodes whatever shape it
// expects. Return a [CallResult] for both success and tool-level
// failures (set IsError); only return a non-nil error for transport-
// level / unrecoverable problems, which surface as JSON-RPC errors.
type ToolHandler func(ctx context.Context, args json.RawMessage) (CallResult, error)

// Server is a Model Context Protocol server. Construct with
// [NewServer], register tools via [Server.RegisterTool], publish
// notifications via [Server.Notify], expose over HTTP with
// [Server.Handler] (or [Server.ListenAndServe]).
//
// Transport layout:
//   - POST <endpoint>          — JSON-RPC request, JSON response.
//   - GET  <endpoint>          — SSE stream of server-pushed
//     notifications. Stays open until the client disconnects.
//
// Concurrency: tool registration, notification, and request dispatch
// are all safe for concurrent use.
type Server struct {
	info ServerInfo

	mu        sync.RWMutex
	tools     map[string]registeredTool
	toolOrder []string

	subM sync.Mutex
	subs []chan rpcNotification
}

type registeredTool struct {
	def     ToolDef
	handler ToolHandler
}

// NewServer returns an empty server identifying itself with name +
// version during the MCP initialize handshake.
func NewServer(name, version string) *Server {
	return &Server{
		info:  ServerInfo{Name: name, Version: version},
		tools: map[string]registeredTool{},
	}
}

// RegisterTool adds a tool. The handler runs in a background goroutine
// per request, so it may block on I/O. Re-registering an existing
// name replaces the entry.
func (s *Server) RegisterTool(def ToolDef, handler ToolHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.tools[def.Name]; !exists {
		s.toolOrder = append(s.toolOrder, def.Name)
	}
	s.tools[def.Name] = registeredTool{def: def, handler: handler}
}

// UnregisterTool removes a tool. Returns true on success, false when
// no tool with that name is registered.
func (s *Server) UnregisterTool(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tools[name]; !ok {
		return false
	}
	delete(s.tools, name)
	for i, n := range s.toolOrder {
		if n == name {
			s.toolOrder = append(s.toolOrder[:i], s.toolOrder[i+1:]...)
			break
		}
	}
	return true
}

// Tools returns a snapshot of registered tool definitions in
// registration order. Useful for tests; not consumed by the server.
func (s *Server) Tools() []ToolDef {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ToolDef, 0, len(s.toolOrder))
	for _, name := range s.toolOrder {
		out = append(out, s.tools[name].def)
	}
	return out
}

// Notify pushes a JSON-RPC notification to every connected SSE
// subscriber. params is marshalled as the notification's payload.
// Method names typically follow MCP convention: "notifications/<...>".
//
// Send is best-effort and non-blocking: if a subscriber's buffer is
// full, the notification is dropped for that subscriber to keep the
// caller cheap. Subscribers that need lossless delivery should
// reconnect or run a side-channel.
func (s *Server) Notify(method string, params any) {
	n := rpcNotification{
		JSONRPC: jsonrpcVersion,
		Method:  method,
		Params:  params,
	}
	s.subM.Lock()
	subs := append([]chan rpcNotification(nil), s.subs...)
	s.subM.Unlock()
	for _, ch := range subs {
		select {
		case ch <- n:
		default:
			// subscriber's buffer is full — drop.
		}
	}
}

// Handler returns the http.Handler that speaks MCP's streamable-HTTP
// protocol. Mount on any [http.ServeMux]:
//
//	mux := http.NewServeMux()
//	mux.Handle("/mcp", srv.Handler())
//	http.ListenAndServe(":8080", mux)
//
// The handler dispatches POST as a JSON-RPC request and GET (with
// Accept: text/event-stream) as an SSE subscription.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.serveHTTP)
}

// ListenAndServe starts an HTTP server on addr with the MCP handler
// mounted at path. Convenience for the smallest possible deployment;
// for anything more (TLS, middleware, multiple endpoints) wire
// [Server.Handler] into your own [http.Server].
func (s *Server) ListenAndServe(addr, path string) error {
	mux := http.NewServeMux()
	mux.Handle(path, s.Handler())
	return zhttp.NewServer(addr, mux).ListenAndServe()
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.servePOST(w, r)
	case http.MethodGet:
		s.serveSSE(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// servePOST handles a single JSON-RPC request and writes the response
// as application/json.
// maxRPCBodyBytes caps the JSON-RPC request body. 4 MB is generous
// for MCP — payloads are tool definitions or small tool-call inputs.
// Tools that need to ship large blobs should pass references (path /
// URL) rather than inlining the bytes. Spelled long-form so the
// constant doesn't have to be decoded mentally at the call site.
const maxRPCBodyBytes = 4 * 1024 * 1024

func (s *Server) servePOST(w http.ResponseWriter, r *http.Request) {
	var req rpcRequest
	// Strict, bounded decode. Earlier this handler accepted any-size
	// body with bare json.NewDecoder — the easiest DoS surface on
	// the MCP server.
	if err := zhttp.DecodeJSON(r, &req, maxRPCBodyBytes); err != nil {
		// Parse error has no client-supplied ID to echo. Per spec the
		// ID member is null; using nil RawMessage emits "id":null
		// since the ID field is no longer omitempty in the response —
		// actually we want it null on error. RawMessage of "null"
		// renders explicitly.
		writeRPCError(w, json.RawMessage("null"), -32700, "parse error: "+err.Error())
		return
	}
	if req.JSONRPC == "" {
		req.JSONRPC = jsonrpcVersion
	}
	resp, ok := s.dispatch(r.Context(), req)
	if !ok {
		// Notification — JSON-RPC 2.0 §4.1: the Server MUST NOT reply.
		// 204 No Content makes the contract visible to callers that
		// inspect status; a bare 200 with empty body would work but
		// hides the "this was a notification" signal.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// serveSSE opens a Server-Sent Events stream and forwards every
// notification published via [Server.Notify] until the client
// disconnects.
func (s *Server) serveSSE(w http.ResponseWriter, r *http.Request) {
	if !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		http.Error(w, "GET requires Accept: text/event-stream", http.StatusNotAcceptable)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch := s.subscribe()
	defer s.unsubscribe(ch)

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case n := <-ch:
			payload, err := json.Marshal(n)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// subscribe registers a buffered channel for SSE delivery. Buffer
// size 16 absorbs short bursts; lossless delivery is not guaranteed —
// see [Server.Notify].
func (s *Server) subscribe() chan rpcNotification {
	ch := make(chan rpcNotification, 16)
	s.subM.Lock()
	s.subs = append(s.subs, ch)
	s.subM.Unlock()
	return ch
}

func (s *Server) unsubscribe(target chan rpcNotification) {
	s.subM.Lock()
	defer s.subM.Unlock()
	for i, ch := range s.subs {
		if ch == target {
			s.subs = append(s.subs[:i], s.subs[i+1:]...)
			close(ch)
			return
		}
	}
}

// dispatch routes a JSON-RPC request to the matching method handler.
// The second return is the standard "should we respond?" gate —
// false for JSON-RPC notifications (no "id" member in the inbound
// request), true for everything else. Transports use this to
// suppress the response write entirely on notifications, satisfying
// the spec's MUST-NOT-respond rule (§4.1).
//
// Notification detection is structural (req.IsNotification()) and
// independent of method name — any well-formed inbound request
// without an id is a notification, whether it's the canonical
// notifications/initialized or a future notifications/* the spec
// adds. Method-name-only dispatch was the previous shape and it
// broke as soon as a non-notifications method arrived without an id.
func (s *Server) dispatch(ctx context.Context, req rpcRequest) (rpcResponse, bool) {
	if req.IsNotification() {
		// Drop notifications silently. The method-specific handler may
		// still want to do work (notifications/initialized has no body
		// to act on, but a future notifications/cancelled would), but
		// no response is ever written to the wire.
		return rpcResponse{}, false
	}
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req), true
	case "tools/list":
		return s.handleToolsList(req), true
	case "tools/call":
		return s.handleToolsCall(ctx, req), true
	}
	return rpcErrorResponse(req.ID, -32601, "method not found: "+req.Method), true
}

// initializeResult is the body of an initialize response. Mirrors the
// MCP spec's required fields.
type initializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ServerInfo      ServerInfo     `json:"serverInfo"`
}

func (s *Server) handleInitialize(req rpcRequest) rpcResponse {
	result := initializeResult{
		ProtocolVersion: "2024-11-05",
		Capabilities: map[string]any{
			"tools": map[string]any{},
		},
		ServerInfo: s.info,
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return rpcErrorResponse(req.ID, -32603, "marshal initialize result: "+err.Error())
	}
	return rpcResponse{JSONRPC: jsonrpcVersion, ID: req.ID, Result: raw}
}

func (s *Server) handleToolsList(req rpcRequest) rpcResponse {
	defs := s.Tools()
	raw, err := json.Marshal(listResult{Tools: defs})
	if err != nil {
		return rpcErrorResponse(req.ID, -32603, "marshal tools/list result: "+err.Error())
	}
	return rpcResponse{JSONRPC: jsonrpcVersion, ID: req.ID, Result: raw}
}

func (s *Server) handleToolsCall(ctx context.Context, req rpcRequest) rpcResponse {
	params, err := decodeCallParams(req.Params)
	if err != nil {
		return rpcErrorResponse(req.ID, -32602, "invalid params: "+err.Error())
	}
	s.mu.RLock()
	tool, ok := s.tools[params.Name]
	s.mu.RUnlock()
	if !ok {
		return rpcErrorResponse(req.ID, -32602, "unknown tool: "+params.Name)
	}

	args, _ := json.Marshal(params.Arguments)
	result, err := tool.handler(ctx, args)
	if err != nil {
		// Tool-handler errors flow back to the client as a JSON-RPC
		// error (transport-level failure). Use IsError on CallResult
		// for tool-reported business failures.
		return rpcErrorResponse(req.ID, -32000, err.Error())
	}
	raw, err := marshalCallResult(result)
	if err != nil {
		return rpcErrorResponse(req.ID, -32603, "marshal tools/call result: "+err.Error())
	}
	return rpcResponse{JSONRPC: jsonrpcVersion, ID: req.ID, Result: raw}
}

// decodeCallParams pulls a callParams out of a request's untyped Params.
// Goes through json round-trip because rpcRequest.Params is `any` (the
// transport doesn't decode params; the dispatcher does, by method).
func decodeCallParams(raw any) (callParams, error) {
	if raw == nil {
		return callParams{}, errors.New("missing params")
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return callParams{}, err
	}
	var p callParams
	if err := json.Unmarshal(b, &p); err != nil {
		return callParams{}, err
	}
	if p.Name == "" {
		return callParams{}, errors.New("missing tool name")
	}
	if p.Arguments == nil {
		p.Arguments = map[string]any{}
	}
	return p, nil
}

// rpcErrorResponse builds a JSON-RPC error response. id is echoed
// verbatim from the request (or json.RawMessage("null") on parse-
// error paths where no client-supplied id is available).
func rpcErrorResponse(id json.RawMessage, code int, message string) rpcResponse {
	return rpcResponse{
		JSONRPC: jsonrpcVersion,
		ID:      id,
		Error:   &rpcError{Code: code, Message: message},
	}
}

// writeRPCError writes a JSON-RPC error response to an http.ResponseWriter.
func writeRPCError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rpcErrorResponse(id, code, message))
}
