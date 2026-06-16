// Package main is a tiny MCP stdio server used only by mcp/stdio_test.go. It
// supports just enough of the protocol to round-trip a discover+call:
// initialize, tools/list, and tools/call for an echo tool.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Result  interface{} `json:"result,omitempty"`
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	enc := json.NewEncoder(os.Stdout)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		if req.ID == 0 {
			// Notification (e.g. notifications/initialized) — ignore.
			continue
		}
		switch req.Method {
		case "initialize":
			_ = enc.Encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "fake", "version": "0"},
			}})
		case "tools/list":
			_ = enc.Encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
				"tools": []map[string]any{{
					"name":        "echo",
					"description": "echoes back the message",
					"inputSchema": map[string]any{
						"type":       "object",
						"properties": map[string]any{"message": map[string]any{"type": "string"}},
						"required":   []string{"message"},
					},
				}},
			}})
		case "tools/call":
			var p struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			msg, _ := p.Arguments["message"].(string)
			if strings.HasPrefix(msg, "env:") {
				key := strings.TrimPrefix(msg, "env:")
				msg = os.Getenv(key)
			}
			_ = enc.Encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
				"content": []map[string]any{{"type": "text", "text": fmt.Sprintf("echo: %s", msg)}},
			}})
		default:
			_ = enc.Encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: nil})
		}
	}
}
