// Command recording-fixture serves deterministic OpenAI-compatible responses for
// zarlcode's documentation recordings. It is local-only and never reads secrets.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const fixtureModel = "zarlcode-recording-model"

type message struct {
	Role string `json:"role"`
	Text string `json:"content"`
}

type completionRequest struct {
	Messages []message `json:"messages"`
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8081", "local listen address")
	readyFile := flag.String("ready-file", "", "write the selected listen address after binding")
	ownerPID := flag.Int("owner-pid", 0, "exit when this supervising process exits")
	flag.Parse()

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()

	if *readyFile != "" {
		if err := os.WriteFile(*readyFile, []byte(listener.Addr().String()), 0o600); err != nil {
			_ = listener.Close()
			log.Fatal(err)
		}
	}

	server := &http.Server{
		Handler:      handler(),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		IdleTimeout:  30 * time.Second,
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()

	parentPID := os.Getppid()
	fmt.Printf("recording fixture listening on http://%s/v1\n", listener.Addr())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	parentCheck := time.NewTicker(250 * time.Millisecond)
	defer parentCheck.Stop()

	stopping := false
	for !stopping {
		select {
		case err := <-serveDone:
			if !errors.Is(err, http.ErrServerClosed) {
				log.Fatal(err)
			}
			return
		case <-ctx.Done():
			stopping = true
		case <-parentCheck.C:
			ownerGone := *ownerPID > 0 && errors.Is(syscall.Kill(*ownerPID, 0), syscall.ESRCH)
			if ownerGone {
				_ = syscall.Kill(parentPID, syscall.SIGTERM)
				stopping = true
			}
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatal(err)
	}
	if err := <-serveDone; !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"object": "list",
			"data":   []map[string]string{{"id": fixtureModel, "object": "model"}},
		})
	})
	mux.HandleFunc("POST /v1/chat/completions", complete)
	return mux
}

func complete(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var req completionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid completion request", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)

	if hasToolResult(req.Messages) {
		switch {
		case hasText(req.Messages, "Extract the greeting prefix"):
			result := latestToolResult(req.Messages)
			switch {
			case strings.Contains(result, "edited greet.go"):
				writeToolCompletion(w, flusher, "bash", `{"command":"gofmt -w greet.go && go test -v ./..."}`)
			case strings.Contains(result, "[exit 0]"):
				writeTextCompletion(w, flusher, "## Ready to review\n\nExtracted the greeting prefix into a named constant in **greet.go**.\n\n- Behavior is unchanged: `Message(\"zarlcode\")` returns `hello, zarlcode`.\n- Verification: `go test -v ./...` passed.\n- One file changed. Open the working set to inspect the diff.")
			default:
				writeTextCompletion(w, flusher, "The tool step did not complete successfully. Inspect its output before continuing.")
			}
		case hasText(req.Messages, "Delegate a review"):
			writeTextCompletion(w, flusher, "Review requested. Open the agents panel to inspect the delegated task and its status.")
		case hasText(req.Messages, "Plan a small refactor"):
			writeTextCompletion(w, flusher, "## Plan: make the greeting explicit\n\n1. Extract the `hello, ` prefix into a named constant in **greet.go**.\n2. Preserve the existing output for every name.\n3. Run `go test -v ./...` and inspect the one-file diff.\n\nNo files changed. Switch to Build when you are ready.")
		case hasText(req.Messages, "Review greet.go"):
			writeTextCompletion(w, flusher, "## Greeting review\n\nThe helper returns a prefix followed by the supplied name. The existing test checks the public output. Extracting the prefix into a local constant preserves that behavior; no dependency or API change is needed.")
		default:
			writeTextCompletion(w, flusher, "## A small Go package\n\n**greet.go** builds a greeting from a name. **greet_test.go** checks its output.\n\nThe next change is deliberately small: name the greeting prefix, keep the behavior, and verify it with the existing test.")
		}
		return
	}

	switch {
	case hasText(req.Messages, "Extract the greeting prefix"):
		writeToolCompletion(w, flusher, "edit", `{"path":"greet.go","edits":[{"start_line":4,"start_hash":"xWuV","end_line":4,"end_hash":"xWuV","mode":"replace","new_string":"\tconst greeting = \"hello, \"\n\treturn greeting + name\n"}]}`)
	case hasText(req.Messages, "Delegate a review"):
		writeToolCompletion(w, flusher, "agent_spawn", `{"prompt":"Review greet.go and greet_test.go. Explain whether extracting the greeting prefix changes behavior.","mode":"explore"}`)
	default:
		writeToolCompletion(w, flusher, "program", `{"script":"emit(call_many([{\"name\":\"read\",\"args\":{\"path\":\"greet.go\"}},{\"name\":\"read\",\"args\":{\"path\":\"greet_test.go\"}}]))"}`)
	}
}

func hasToolResult(messages []message) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		switch messages[i].Role {
		case "tool":
			return true
		case "user":
			return false
		}
	}
	return false
}

func latestToolResult(messages []message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "tool" {
			return messages[i].Text
		}
	}
	return ""
}

func hasText(messages []message, want string) bool {
	// A resumed conversation contains earlier prompts. Only the current user turn
	// selects the scripted scene; previous Build turns must not trigger another edit.
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return strings.Contains(messages[i].Text, want)
		}
	}
	return false
}

func writeToolCompletion(w http.ResponseWriter, flusher http.Flusher, name, arguments string) {
	writeEvent(w, flusher, map[string]any{
		"id":     "recording-fixture",
		"object": "chat.completion.chunk",
		"choices": []any{map[string]any{
			"index": 0,
			"delta": map[string]any{
				"tool_calls": []any{map[string]any{
					"index": 0,
					"id":    "call_recording_fixture",
					"type":  "function",
					"function": map[string]string{
						"name":      name,
						"arguments": arguments,
					},
				}},
			},
		}},
	})
	writeEvent(w, flusher, map[string]any{
		"id":      "recording-fixture",
		"object":  "chat.completion.chunk",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"}},
	})
	writeDone(w, flusher)
}

func writeTextCompletion(w http.ResponseWriter, flusher http.Flusher, text string) {
	writeEvent(w, flusher, map[string]any{
		"id":      "recording-fixture",
		"object":  "chat.completion.chunk",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]string{"content": text}}},
	})
	writeEvent(w, flusher, map[string]any{
		"id":      "recording-fixture",
		"object":  "chat.completion.chunk",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}},
	})
	writeEvent(w, flusher, map[string]any{
		"id":      "recording-fixture",
		"object":  "chat.completion.chunk",
		"choices": []any{},
		"usage":   map[string]int{"prompt_tokens": 144, "completion_tokens": 24, "total_tokens": 168},
	})
	writeDone(w, flusher)
}

func writeEvent(w http.ResponseWriter, flusher http.Flusher, value any) {
	data, _ := json.Marshal(value)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
	if flusher != nil {
		flusher.Flush()
	}
}

func writeDone(w http.ResponseWriter, flusher http.Flusher) {
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("encode response: %v", err)
	}
}
