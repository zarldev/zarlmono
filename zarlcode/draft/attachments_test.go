package draft_test

import (
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/draft"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestAttachmentDraftRoundTripAndOwnership(t *testing.T) {
	parts := []llm.ContentPart{llm.TextPart("  attached text\n"), llm.ImagePartFromDataURI("data:image/png;base64,Ynl0ZXM=", "image/png")}
	want := llm.CloneContentParts(parts)
	encoded, err := draft.EncodeWithAttachments("", parts)
	if err != nil {
		t.Fatal(err)
	}
	parts[1].Image.DataURI = "mutated"
	text, err := draft.Decode(encoded)
	if err != nil || text != "" {
		t.Fatalf("attachment-only draft: %q, %v", text, err)
	}
	saved, err := draft.DecodeAttachments(encoded)
	if err != nil || !reflect.DeepEqual(saved, want) {
		t.Fatalf("attachment bytes changed: %v", err)
	}
	saved[1].Image.DataURI = "mutated again"
	again, err := draft.DecodeAttachments(encoded)
	if err != nil || !reflect.DeepEqual(again, want) {
		t.Fatal("decoded attachments alias previous result")
	}
}

func TestAttachmentDraftRejectsMissingOrInconsistentBytes(t *testing.T) {
	for _, input := range []string{
		`{"attachments":[{"type":"image"}]}`,
		`{"attachments":[{"type":"image","image":{"url":"https://example.com/image"}}]}`,
		`{"attachments":[{"type":"text","image":{"data_uri":"data:image/png;base64,eA=="}}]}`,
		`{"attachments":[{"type":"unknown"}]}`,
		`{"attachments":[{"type":"text","extra":true}]}`,
		`{"text":"draft"} {}`,
	} {
		if _, err := draft.Decode([]byte(input)); err == nil {
			t.Errorf("accepted malformed draft %s", input)
		}
		if _, err := draft.DecodeAttachments([]byte(input)); err == nil {
			t.Errorf("accepted malformed attachments %s", input)
		}
	}
}
