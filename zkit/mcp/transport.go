// Package mcp provides a generic Model Context Protocol client.
//
// The wire transport is pluggable — HTTP for remote servers, stdio for
// local subprocess servers. The Client speaks JSON-RPC 2.0 and exposes
// tool discovery / invocation; consumers that need to bridge MCP-listed
// tools into a tool registry should use a wrapper from a tools-aware
// package (the zkit/mcp package itself stays generic).
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
)

// Transport sends a JSON-RPC 2.0 request and receives a matching
// response. Implementations must be safe for concurrent use and
// correlate responses with their originating requests by the ID
// embedded in rpcRequest (compared as JSON bytes — String / Number
// / null all preserved verbatim).
type Transport interface {
	Call(ctx context.Context, req rpcRequest) (rpcResponse, error)
	Close() error
}

// NotificationReceiver is optionally implemented by transports that
// surface server-pushed JSON-RPC notifications (stdio does; HTTP
// doesn't). The registered handler is invoked for every incoming
// notification — Client multiplexes by method over a single handler.
type NotificationReceiver interface {
	OnNotification(handler func(method string, params json.RawMessage))
}

// rpcNotificationIn is an incoming JSON-RPC 2.0 notification. Params
// stays as RawMessage so handlers can decode the provider-specific
// payload without the transport interpreting it.
type rpcNotificationIn struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// rpcRequest is a JSON-RPC 2.0 request. ID is [json.RawMessage] —
// kept as raw bytes so a missing "id" member (len(ID) == 0,
// indicating a NOTIFICATION per spec §4.1) is distinguishable from
// the perfectly-valid numeric ID 0, and so String / Number / null
// IDs all round-trip without the server inventing a value the
// client never sent.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  any             `json:"params,omitempty"`
}

// IsNotification reports whether this request is a JSON-RPC
// notification — defined by the spec as a request with no "id"
// member. The server MUST NOT respond to a notification, so dispatch
// uses this to suppress the response on the way out.
func (r rpcRequest) IsNotification() bool {
	return len(r.ID) == 0
}

// rpcNotification is a JSON-RPC 2.0 notification (fire-and-forget, no ID).
type rpcNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// rpcResponse is a JSON-RPC 2.0 response. ID echoes the originating
// request's ID verbatim — see [rpcRequest.ID] for why this is raw
// bytes rather than a typed integer.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError is a JSON-RPC 2.0 error object.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}
