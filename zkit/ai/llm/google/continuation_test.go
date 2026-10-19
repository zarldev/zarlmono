package google_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/google"
)

func TestProviderSignedFunctionCallsCaptureAndReplayInOrder(t *testing.T) {
	var requests [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests = append(requests, append([]byte(nil), body...))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"functionCall\":{\"name\":\"read\",\"args\":{\"path\":\"foo.go\"}},\"thoughtSignature\":\"c2lnbmVk\"},{\"functionCall\":{\"name\":\"write\",\"args\":{\"path\":\"bar.go\"}}}]},\"finishReason\":\"STOP\"}]}\n\n"))
	}))
	defer server.Close()

	p, err := google.NewProvider("test-key", google.WithBaseURL(server.URL), google.WithModel("gemini-3.8-flash"))
	if err != nil {
		t.Fatal(err)
	}
	var calls []llm.ToolCall
	var items []llm.ContinuationItem
	for chunk, err := range p.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "read and write"}}}) {
		if err != nil {
			t.Fatal(err)
		}
		calls = append(calls, chunk.ToolCalls...)
		items = append(items, chunk.Clone().CompletedItems...)
	}
	if len(calls) != 2 || calls[0].Function.Name != "read" || calls[1].Function.Name != "write" {
		t.Fatalf("calls=%#v, want read then write", calls)
	}
	if calls[0].ID == calls[1].ID || calls[0].OutputIndex == nil || *calls[0].OutputIndex != 0 ||
		calls[1].OutputIndex == nil || *calls[1].OutputIndex != 1 {
		t.Fatalf("call pairing metadata=%#v", calls)
	}
	if len(items) != 1 || items[0].ID != calls[0].ID || items[0].OutputIndex == nil || *items[0].OutputIndex != 0 {
		t.Fatalf("items=%#v, want signed first call only", items)
	}

	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "read and write"},
		{Role: llm.RoleAssistant, ToolCalls: calls, ContinuationItems: items},
		{Role: llm.RoleTool, ToolCallID: calls[0].ID, Content: "read ok"},
		{Role: llm.RoleTool, ToolCallID: calls[1].ID, Content: "write ok"},
	}
	for _, err := range p.Complete(t.Context(), llm.CompletionRequest{Messages: messages}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(requests) != 2 {
		t.Fatalf("requests=%d", len(requests))
	}

	var request struct {
		Contents []struct {
			Role  string           `json:"role"`
			Parts []map[string]any `json:"parts"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(requests[1], &request); err != nil {
		t.Fatal(err)
	}
	if len(request.Contents) != 3 || len(request.Contents[1].Parts) != 2 {
		t.Fatalf("replayed contents=%s", requests[1])
	}
	if request.Contents[2].Role != llm.RoleUser || len(request.Contents[2].Parts) != 2 {
		t.Fatalf("parallel function responses were not grouped in call order: %s", requests[1])
	}
	for index, name := range []string{"read", "write"} {
		response, ok := request.Contents[2].Parts[index]["functionResponse"].(map[string]any)
		if !ok || response["name"] != name {
			t.Fatalf("function response %d = %#v, want %s", index, request.Contents[2].Parts[index], name)
		}
	}
	parts := request.Contents[1].Parts
	first, ok := parts[0]["functionCall"].(map[string]any)
	if !ok || first["name"] != "read" || parts[0]["thoughtSignature"] != "c2lnbmVk" {
		t.Fatalf("first replayed part=%#v", parts[0])
	}
	second, ok := parts[1]["functionCall"].(map[string]any)
	if !ok || second["name"] != "write" {
		t.Fatalf("second replayed part=%#v", parts[1])
	}
	if _, duplicated := parts[1]["thoughtSignature"]; duplicated {
		t.Fatalf("signature duplicated onto unsigned call: %#v", parts[1])
	}
}
