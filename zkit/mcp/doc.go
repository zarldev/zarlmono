// Package mcp implements Model Context Protocol client/server primitives and
// transports.
//
// It owns the JSON-RPC protocol surface plus stdio and HTTP transports. Agent
// tool registration for connecting to remote MCP servers lives in
// zkit/ai/tools/dynamic; this package stays focused on protocol and transport
// mechanics.
package mcp
