package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/zarldev/zarlmono/zkit/agent/mcp"
)

func TestNotifierForBoundsWholeExpandedFrame(t *testing.T) {
	q := &fakeQueue{}
	notify := mcp.NotifierFor(q)

	quotedPayload := json.RawMessage(`"` + strings.Repeat(`\\`, mcp.MaxParamsLen) + `"`)
	if !json.Valid(quotedPayload) {
		t.Fatal("test payload is not valid JSON")
	}
	notify(
		strings.Repeat("connection\n\"\\🙂", 1024),
		strings.Repeat("method\n\"\\🙂", 1024),
		quotedPayload,
	)

	messages := q.snapshot()
	if len(messages) != 1 {
		t.Fatalf("notification count = %d, want 1", len(messages))
	}
	frame := messages[0]
	if len(frame) != mcp.MaxNotificationLen {
		t.Fatalf("notification length = %d, want %d", len(frame), mcp.MaxNotificationLen)
	}
	if !utf8.ValidString(frame) {
		t.Fatal("bounded notification is not valid UTF-8")
	}
	if !strings.HasSuffix(frame, "…[truncated]") {
		t.Fatalf("bounded notification suffix = %q, want truncation marker", frame[len(frame)-32:])
	}
	if strings.ContainsRune(frame, '\n') {
		t.Fatal("quoted notification contains a literal newline")
	}
	if !strings.Contains(frame, `connection="connection\n\"\\`) || !strings.Contains(frame, `method="method\n\"\\`) {
		t.Fatal("connection or method was not quoted before whole-frame truncation")
	}
}
