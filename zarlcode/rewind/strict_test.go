package rewind_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestCheckpointRejectsInvalidUTF8(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(*rewind.CaptureInput)
	}{
		{"prompt", func(i *rewind.CaptureInput) { i.Boundary.PromptText = "private-canary\xff" }},
		{"context", func(i *rewind.CaptureInput) { i.Context[0].Content = "private-canary\xff" }},
		{"target", func(i *rewind.CaptureInput) { i.Target.Model = "private-canary\xff" }},
		{"plan", func(i *rewind.CaptureInput) { i.Plan.Steps[0].Text = "private-canary\xff" }},
		{"tool output identity", func(i *rewind.CaptureInput) { i.ToolCallIDs[0] = "private-canary\xff" }},
		{"native data", func(i *rewind.CaptureInput) {
			i.Context[1].ContinuationItems[0].Data = []byte("{\"type\":\"reasoning\",\"encrypted_content\":\"private-canary\xff\"}")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			input := captureInput(t)
			tc.mutate(&input)
			_, err := rewind.Capture(input)
			if !errors.Is(err, rewind.ErrInvalid) {
				t.Fatalf("Capture: %v", err)
			}
			if strings.Contains(err.Error(), "private-canary") {
				t.Fatal("private error")
			}
		})
	}
	for _, replacement := range [][]byte{[]byte("edit\xffme"), []byte(`edit\ud800me`), []byte(`edit\udc00me`)} {
		record := captureRecord(t, captureInput(t))
		record.Payload = bytes.Replace(record.Payload, []byte("edit me"), replacement, 1)
		if _, err := rewind.Decode(record); !errors.Is(err, rewind.ErrInvalid) {
			t.Fatalf("Decode invalid UTF: %v", err)
		}
	}
}

func TestCheckpointRequiresExplicitVersionedFields(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ object, field string }{
		{"", "plan"}, {"", "tool_call_ids"}, {"", "revision"},
		{"boundary", "prompt_text"}, {"boundary", "event_watermark"}, {"boundary", "settled_turn_id"}, {"boundary", "has_attachments"},
		{"target", "reserve"}, {"target", "window"}, {"target", "plan_mode"}, {"target", "codex_effort"},
		{"plan", "steps"},
	} {
		t.Run(tc.object+"/"+tc.field, func(t *testing.T) {
			t.Parallel()
			record := captureRecord(t, captureInput(t))
			mutatePayload(t, &record, func(p map[string]any) {
				if tc.object != "" {
					p = p[tc.object].(map[string]any)
				}
				delete(p, tc.field)
			})
			if _, err := rewind.Decode(record); !errors.Is(err, rewind.ErrInvalid) {
				t.Fatalf("missing field: %v", err)
			}
		})
	}
	record := captureRecord(t, captureInput(t))
	record.Payload = append([]byte(`{"version":1,`), record.Payload[1:]...)
	if _, err := rewind.Decode(record); !errors.Is(err, rewind.ErrInvalid) {
		t.Fatalf("duplicate field: %v", err)
	}
	record = captureRecord(t, captureInput(t))
	mutatePayload(t, &record, func(p map[string]any) { p["target"].(map[string]any)["plan_mode"] = nil })
	if _, err := rewind.Decode(record); !errors.Is(err, rewind.ErrInvalid) {
		t.Fatalf("null scalar: %v", err)
	}
}

func TestCheckpointRejectsUnorderedRecords(t *testing.T) {
	t.Parallel()
	record := captureRecord(t, captureInput(t))
	mutatePayload(t, &record, func(p map[string]any) {
		records := p["records"].([]any)
		records[0], records[1] = records[1], records[0]
	})
	if _, err := rewind.Decode(record); !errors.Is(err, rewind.ErrInvalid) {
		t.Fatalf("unordered records: %v", err)
	}
}

func TestCheckpointRejectsUnsupportedMediaReplay(t *testing.T) {
	t.Parallel()
	for _, role := range []string{llm.RoleSystem, llm.RoleAssistant} {
		if err := rewind.ValidateContext([]llm.Message{{Role: role, Parts: []llm.ContentPart{{Type: llm.ContentTypeText, Text: "would be omitted"}}}}, "openai"); !errors.Is(err, rewind.ErrInvalid) {
			t.Fatalf("role %s: %v", role, err)
		}
	}
	for _, provider := range []string{"openai", "anthropic", "openai-codex", "custom"} {
		for _, part := range []llm.ContentPart{
			{Type: llm.ContentTypeAudio, Audio: &llm.AudioData{DataURI: "data:audio/wav;base64,YQ=="}},
			{Type: llm.ContentTypeVideo, Video: &llm.VideoData{URL: "https://example.test/video"}},
			{Type: llm.ContentTypeImage, Image: &llm.ImageData{DataURI: "not-a-data-uri"}},
		} {
			if err := rewind.ValidateContext([]llm.Message{{Role: llm.RoleUser, Parts: []llm.ContentPart{part}}}, provider); !errors.Is(err, rewind.ErrInvalid) {
				t.Fatalf("%s/%s: %v", provider, part.Type, err)
			}
		}
	}
}
