package hitl

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// FormatReviewMessage renders req/review as a deterministic, explicitly framed
// steering message. The frame is intentionally plain text so every runner
// transport can inject it as a user message without needing a richer protocol.
func FormatReviewMessage(req Request, review Review) string {
	var b strings.Builder
	b.WriteString("[human review]\n")
	writeLine(&b, "request_id", string(review.RequestID))
	if review.RequestID == "" {
		writeLine(&b, "request_id", string(req.ID))
	}
	writeLine(&b, "decision", string(review.Decision))
	writeLine(&b, "reviewer", review.Reviewer)
	writeLine(&b, "comment", review.Comment)
	if req.ID != "" && review.RequestID != req.ID {
		writeLine(&b, "original_request_id", string(req.ID))
	}
	writeLine(&b, "run_id", req.RunID)
	writeLine(&b, "checkpoint_id", req.CheckpointID)
	writeLine(&b, "action", req.Action)
	writeLine(&b, "summary", req.Summary)
	writeLine(&b, "risk", string(req.Risk))
	writeMap(&b, "patch", review.Patch)
	writeMap(&b, "payload", req.Payload)
	return strings.TrimRight(b.String(), "\n")
}

func writeLine(b *strings.Builder, key, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(b, "%s=%s\n", key, value)
}

func writeMap(b *strings.Builder, key string, value map[string]any) {
	if len(value) == 0 {
		return
	}
	keys := make([]string, 0, len(value))
	for k := range value {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ordered := make(map[string]any, len(value))
	for _, k := range keys {
		ordered[k] = value[k]
	}
	raw, err := json.Marshal(ordered)
	if err != nil {
		fmt.Fprintf(b, "%s=%q\n", key, fmt.Sprint(value))
		return
	}
	fmt.Fprintf(b, "%s=%s\n", key, string(raw))
}
