package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/mcp"
)

const serverFlag = "--stdio-server"

func main() {
	if len(os.Args) == 2 && os.Args[1] == serverFlag {
		if err := serveStdio(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate example executable: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := mcp.NewStdioClientContext(ctx, executable, []string{serverFlag}, nil)
	if err != nil {
		return fmt.Errorf("connect and initialize: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = client.Close()
		}
	}()
	fmt.Println("connected: initialization complete")

	definitions, err := client.Discover(ctx)
	if err != nil {
		return fmt.Errorf("discover tools: %w", err)
	}
	if len(definitions) != 1 {
		return fmt.Errorf("discover tools: got %d tools, want 1", len(definitions))
	}
	fmt.Printf("discovered: %s\n", definitions[0].Name)

	// Client.Call accepts arbitrary discovered tool arguments as a map.
	result, err := client.Call(ctx, definitions[0].Name, map[string]any{"message": "hello, MCP"})
	if err != nil {
		return fmt.Errorf("call %s: %w", definitions[0].Name, err)
	}
	fmt.Printf("result: %s\n", result.FirstText())

	if err := client.Close(); err != nil {
		return fmt.Errorf("disconnect: %w", err)
	}
	closed = true
	fmt.Println("disconnected: server stopped")
	return nil
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// Result varies by JSON-RPC method; each method below supplies a named struct.
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
}

type echoArgs struct {
	Message string `json:"message"`
}

type echoCall struct {
	Name      string   `json:"name"`
	Arguments echoArgs `json:"arguments"`
}

type serverCapabilities struct {
	Tools struct{} `json:"tools"`
}

type initializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    serverCapabilities `json:"capabilities"`
	ServerInfo      mcp.ServerInfo     `json:"serverInfo"`
}

type toolDefinition struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	InputSchema llm.Schema `json:"inputSchema"`
}

type listToolsResult struct {
	Tools []toolDefinition `json:"tools"`
}

type echoResult struct {
	Content []mcp.TextContent `json:"content"`
}

func serveStdio() error {
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	initialized := false
	for scanner.Scan() {
		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			return fmt.Errorf("decode request: %w", err)
		}
		if len(req.ID) == 0 { // notifications/initialized has no response
			continue
		}

		var result any
		switch req.Method {
		case "initialize":
			initialized = true
			result = initializeResult{
				ProtocolVersion: "2024-11-05",
				Capabilities:    serverCapabilities{},
				ServerInfo:      mcp.ServerInfo{Name: "local-echo", Version: "1.0.0"},
			}
		case "tools/list":
			if !initialized {
				return errors.New("tools/list received before initialize")
			}
			result = listToolsResult{Tools: []toolDefinition{{
				Name:        "echo",
				Description: "returns the supplied message",
				InputSchema: tools.SchemaFor[echoArgs](),
			}}}
		case "tools/call":
			if !initialized {
				return errors.New("tools/call received before initialize")
			}
			var params echoCall
			if err := json.Unmarshal(req.Params, &params); err != nil {
				return fmt.Errorf("decode tool call: %w", err)
			}
			result = echoResult{Content: []mcp.TextContent{{Text: "echo: " + params.Arguments.Message}}}
		default:
			return fmt.Errorf("unexpected method %q", req.Method)
		}
		if err := encoder.Encode(response{JSONRPC: "2.0", ID: req.ID, Result: result}); err != nil {
			return fmt.Errorf("encode response: %w", err)
		}
	}
	return scanner.Err()
}
