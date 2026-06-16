package tui

import (
	"strings"
	"testing"
)

func TestRenderMarkdown_RendersContent(t *testing.T) {
	out := renderMarkdown("# Hello\n\nsome **bold** words", 60)
	if !strings.Contains(out, "Hello") || !strings.Contains(out, "bold") {
		t.Fatalf("markdown not rendered: %q", out)
	}
}

func TestRenderMarkdown_CacheHitIdentical(t *testing.T) {
	const md = "## Section\n\na paragraph"
	first := renderMarkdown(md, 50)
	second := renderMarkdown(md, 50)
	if first != second {
		t.Fatal("same (content,width) must render identically")
	}
}

func TestAssistantItem_RendersMarkdown(t *testing.T) {
	a := &assistantItem{content: "## Section\n\nplain paragraph text"}
	joined := strings.Join(a.render(70), "\n")
	if !strings.Contains(joined, "Section") || !strings.Contains(joined, "paragraph") {
		t.Fatalf("assistant markdown render missing content: %q", joined)
	}
}
