package tui_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestSubmittedAttachmentsPersistHumanMetadataWithoutPayloadBytes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "notes.txt")
	const body = "private attachment body"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	workspace, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	ui := tui.New()
	ui.SetWorkspace(root, "test-model")
	ui.SetLiveRunner(engine.NewLiveRunner(nil, workspace, "test-model"))
	ui.SetStartupReady(true)
	if err := ui.AttachFile(path); err != nil {
		t.Fatal(err)
	}
	ui.Submit("review this")
	ui.StartSubmittedTurn("turn", "review this")

	entries := ui.CanonicalThread().Entries()
	if len(entries) != 1 || entries[0].Kind != transcript.EntryKinds.ENTRYUSERMESSAGE {
		t.Fatalf("entries = %#v", entries)
	}
	got := entries[0].Payload
	if got.Text != "review this" || len(got.Attachments) != 1 {
		t.Fatalf("user payload = %#v", got)
	}
	attachment := got.Attachments[0]
	if attachment.Name != "notes.txt" || attachment.Size != int64(len(body)) {
		t.Fatalf("attachment = %#v", attachment)
	}
	if strings.Contains(attachment.Name, body) || strings.Contains(attachment.MIMEType, body) {
		t.Fatalf("attachment metadata leaked payload: %#v", attachment)
	}
	exported := transcript.Markdown(transcript.MarkdownMetadata{Title: "attachments"}, ui.CanonicalThread())
	if !strings.Contains(exported, "Attachment: \"notes.txt\"") || strings.Contains(exported, body) {
		t.Fatalf("markdown export = %q", exported)
	}
}

func TestAttachmentNamesCannotInjectMarkdown(t *testing.T) {
	t.Parallel()
	builder := transcript.NewBuilder()
	builder.AddUserWithAttachments("inspect", []transcript.Attachment{{
		Name: "safe`\n### injected",
	}})
	exported := transcript.Markdown(transcript.MarkdownMetadata{Title: "attachments"}, builder.Thread())
	if strings.Contains(exported, "\n### injected") || strings.Count(exported, "- Attachment:") != 1 {
		t.Fatalf("markdown export = %q", exported)
	}
}

func TestExternalTextAttachmentDoesNotPersistAbsolutePath(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	externalRoot := t.TempDir()
	path := filepath.Join(externalRoot, "outside.txt")
	if err := os.WriteFile(path, []byte("external"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	ui := tui.New()
	ui.SetWorkspace(root, "test-model")
	ui.SetLiveRunner(engine.NewLiveRunner(nil, workspace, "test-model"))
	ui.SetStartupReady(true)
	if err := ui.AttachFile(path); err != nil {
		t.Fatal(err)
	}
	ui.Submit("inspect")
	ui.StartSubmittedTurn("turn", "inspect")

	attachment := ui.CanonicalThread().Entries()[0].Payload.Attachments[0]
	if attachment.Name != "outside.txt" || strings.Contains(attachment.Name, externalRoot) {
		t.Fatalf("external attachment metadata = %#v", attachment)
	}
}

func TestSubmittedImagePersistsMIMEType(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "pixel.png")
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	if err := os.WriteFile(path, png, 0o600); err != nil {
		t.Fatal(err)
	}
	workspace, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	ui := tui.New()
	ui.SetWorkspace(root, "test-model")
	ui.SetLiveRunner(engine.NewLiveRunner(nil, workspace, "test-model"))
	ui.SetStartupReady(true)
	if err := ui.AttachImage(path); err != nil {
		t.Fatal(err)
	}
	ui.Submit("inspect")
	ui.StartSubmittedTurn("turn", "inspect")

	attachment := ui.CanonicalThread().Entries()[0].Payload.Attachments[0]
	if attachment.MIMEType != "image/png" {
		t.Fatalf("attachment MIME type = %q", attachment.MIMEType)
	}
}

func TestAttachmentOnlySubmissionCreatesCanonicalUserEntry(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "context.txt")
	if err := os.WriteFile(path, []byte("context"), 0o600); err != nil {
		t.Fatal(err)
	}

	workspace, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	ui := tui.New()
	ui.SetWorkspace(root, "test-model")
	ui.SetLiveRunner(engine.NewLiveRunner(nil, workspace, "test-model"))
	ui.SetStartupReady(true)
	if err := ui.AttachFile(path); err != nil {
		t.Fatal(err)
	}
	ui.Submit("")
	ui.StartSubmittedTurn("turn", "")

	entries := ui.CanonicalThread().Entries()
	if len(entries) != 1 || len(entries[0].Payload.Attachments) != 1 {
		t.Fatalf("attachment-only entries = %#v", entries)
	}
}

func TestCanonicalAttachmentSnapshotDoesNotAlias(t *testing.T) {
	t.Parallel()
	builder := transcript.NewBuilder()
	attachments := []transcript.Attachment{{Name: "original", MIMEType: "image/png", Size: 42}}
	builder.AddUserWithAttachments("look", attachments)
	attachments[0].Name = "mutated-input"

	entries := builder.Thread().Entries()
	entries[0].Payload.Attachments[0].Name = "mutated-snapshot"
	got := builder.Thread().Entries()[0].Payload.Attachments[0]
	if got.Name != "original" {
		t.Fatalf("canonical attachment = %#v", got)
	}
}
