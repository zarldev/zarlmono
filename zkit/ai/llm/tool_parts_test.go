package llm_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestExpandToolResultParts(t *testing.T) {
	t.Parallel()
	image := llm.ImagePartFromDataURI("data:image/png;base64,cG5n", "image/png")
	for _, tc := range []struct {
		name     string
		messages []llm.Message
		roles    []string
	}{
		{name: "nil"},
		{name: "text only", messages: []llm.Message{{Role: llm.RoleTool, Content: "ok", ToolCallID: "a"}}, roles: []string{llm.RoleTool}},
		{name: "batch at end", messages: []llm.Message{
			{Role: llm.RoleTool, Content: "one", ToolCallID: "a", Parts: []llm.ContentPart{image}},
			{Role: llm.RoleTool, Content: "two", ToolCallID: "b", Parts: []llm.ContentPart{image}},
			{Role: llm.RoleTool, Content: "three", ToolCallID: "c"},
		}, roles: []string{llm.RoleTool, llm.RoleTool, llm.RoleTool, llm.RoleUser}},
		{name: "separate batches", messages: []llm.Message{
			{Role: llm.RoleTool, Content: "one", ToolCallID: "a", Parts: []llm.ContentPart{image}},
			{Role: llm.RoleAssistant, Content: "next"},
			{Role: llm.RoleTool, Content: "two", ToolCallID: "b", Parts: []llm.ContentPart{image}},
		}, roles: []string{llm.RoleTool, llm.RoleUser, llm.RoleAssistant, llm.RoleTool, llm.RoleUser}},
		{name: "user parts unchanged", messages: []llm.Message{{Role: llm.RoleUser, Parts: []llm.ContentPart{image}}}, roles: []string{llm.RoleUser}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			before := llm.CloneMessages(tc.messages)
			got := llm.ExpandToolResultParts(tc.messages)
			if !reflect.DeepEqual(tc.messages, before) {
				t.Fatal("canonical history mutated")
			}
			if len(got) != len(tc.roles) {
				t.Fatalf("messages = %d, want %d", len(got), len(tc.roles))
			}
			var pending []string
			original := 0
			for i, message := range got {
				if message.Role != tc.roles[i] {
					t.Fatalf("role[%d] = %s, want %s", i, message.Role, tc.roles[i])
				}
				if message.Role == llm.RoleUser && len(pending) > 0 {
					if len(message.Parts) != 2*len(pending) {
						t.Fatal("attachment count changed")
					}
					for j, id := range pending {
						label := message.Parts[2*j].Text
						if !strings.Contains(label, `"`+id+`"`) || !strings.Contains(label, "untrusted tool output") {
							t.Fatalf("attachment attribution = %q", label)
						}
						if !reflect.DeepEqual(message.Parts[2*j+1], image) {
							t.Fatal("image changed")
						}
					}
					pending = nil
					continue
				}
				want := tc.messages[original]
				original++
				if want.Role == llm.RoleTool && len(want.Parts) > 0 {
					pending = append(pending, want.ToolCallID)
					want.Parts = nil
				}
				if !reflect.DeepEqual(message, want) {
					t.Fatal("non-attachment message changed")
				}
			}
		})
	}
}

func TestContentPartsCloneAndByteLen(t *testing.T) {
	t.Parallel()
	parts := []llm.ContentPart{
		llm.TextPart("text"),
		{Type: llm.ContentTypeImage, Image: &llm.ImageData{URL: "image-url", DataURI: "image-data", MIMEType: "image/png", Detail: "high"}},
		{Type: llm.ContentTypeAudio, Audio: &llm.AudioData{DataURI: "audio-data", Format: "wav"}},
		{Type: llm.ContentTypeVideo, Video: &llm.VideoData{URL: "video-url", DataURI: "video-data", MIMEType: "video/mp4"}},
	}
	wantBytes := len(llm.ContentTypeText) + len("text") +
		len(llm.ContentTypeImage) + len("image-url") + len("image-data") + len("image/png") + len("high") +
		len(llm.ContentTypeAudio) + len("audio-data") + len("wav") +
		len(llm.ContentTypeVideo) + len("video-url") + len("video-data") + len("video/mp4")
	if got := llm.ContentPartsByteLen(parts); got != wantBytes {
		t.Fatalf("ContentPartsByteLen = %d, want %d", got, wantBytes)
	}

	clone := llm.CloneContentParts(parts)
	if !reflect.DeepEqual(clone, parts) {
		t.Fatal("clone content changed")
	}
	if clone[1].Image == parts[1].Image || clone[2].Audio == parts[2].Audio || clone[3].Video == parts[3].Video {
		t.Fatal("clone retained media pointers")
	}
	clone[1].Image.DataURI = "changed-image"
	clone[2].Audio.DataURI = "changed-audio"
	clone[3].Video.DataURI = "changed-video"
	if parts[1].Image.DataURI != "image-data" || parts[2].Audio.DataURI != "audio-data" || parts[3].Video.DataURI != "video-data" {
		t.Fatal("mutating clone changed original media")
	}
}
