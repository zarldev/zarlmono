package tui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/catalog"
)

func TestFileViewer_RenderedContentUsesCodeBlockRenderer(t *testing.T) {
	root := t.TempDir()
	v := &fileViewer{
		workspaceDir: root,
		viewingFile:  filepath.Join(root, "main.go"),
		fileContent:  "package main\n\nfunc main() {}",
	}

	out := ansi.Strip(strings.Join(v.renderedContentLines(80), "\n"))
	if strings.Contains(out, "```") {
		t.Fatalf("file viewer should render code blocks, not raw fences:\n%s", out)
	}
	if !strings.Contains(out, "package main") || !strings.Contains(out, "func main") {
		t.Fatalf("file viewer rendered output lost source content:\n%s", out)
	}
	// With lineNumbers enabled, each source line gets a gutter.
	if !strings.Contains(out, "1 │") || !strings.Contains(out, "2 │") {
		t.Fatalf("file viewer should show line-number gutter:\n%s", out)
	}
}

func TestInferSyntaxFromHint(t *testing.T) {
	cases := []struct {
		hint string
		want string
	}{
		{"main.go", "go"},
		{"src/app.tsx", "tsx"},
		{"config.yaml", "yaml"},
		{"README.md", "markdown"},
		{"$ go test ./...", ""},
	}
	for _, c := range cases {
		t.Run(c.hint, func(t *testing.T) {
			if got := inferSyntaxFromHint(c.hint); got != c.want {
				t.Fatalf("inferSyntaxFromHint(%q) = %q, want %q", c.hint, got, c.want)
			}
		})
	}
}

func TestFileViewerPreviewCapsLargeFiles(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "large.txt")
	data := bytes.Repeat([]byte("a"), fileViewerMaxPreviewBytes+17)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write large file: %v", err)
	}

	got, truncated, size, err := readFileViewerPreview(path)
	if err != nil {
		t.Fatalf("read preview: %v", err)
	}
	if !truncated {
		t.Fatal("large file preview should report truncation")
	}
	if size != int64(len(data)) {
		t.Fatalf("size = %d, want %d", size, len(data))
	}
	if len(got) != fileViewerMaxPreviewBytes {
		t.Fatalf("preview length = %d, want %d", len(got), fileViewerMaxPreviewBytes)
	}
}

func TestFileViewerTruncatesLongLines(t *testing.T) {
	content := strings.Repeat("x", fileViewerMaxLineRunes+10) + "\nshort"

	got, truncated := truncateFileViewerLongLines(content)
	if !truncated {
		t.Fatal("long line should report truncation")
	}
	if !strings.Contains(got, "[line truncated]") {
		t.Fatalf("truncated content = %q, want line truncated marker", got)
	}
	if strings.Contains(got, strings.Repeat("x", fileViewerMaxLineRunes+1)) {
		t.Fatalf("truncated content still contains an over-limit line")
	}
	if !strings.Contains(got, "\nshort") {
		t.Fatalf("truncated content lost later lines: %q", got)
	}
}

func TestFileViewerPreviewRejectsNonRegularFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "dir")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	_, _, _, err := readFileViewerPreview(dir)
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("read preview error = %v, want not regular file", err)
	}
}

func TestFileViewerSkipsBinaryPreview(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "blob.bin")
	if err := os.WriteFile(path, []byte{'a', 0, 'b'}, 0o644); err != nil {
		t.Fatalf("write binary file: %v", err)
	}

	v := newFileViewer(root)
	if !strings.Contains(v.fileContent, "binary file preview skipped") {
		t.Fatalf("file content = %q, want binary preview skip notice", v.fileContent)
	}
}

func TestFileViewerPreviewsImages(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "preview.png")
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	img.Set(1, 0, color.RGBA{G: 255, A: 255})
	img.Set(0, 1, color.RGBA{B: 255, A: 255})
	img.Set(1, 1, color.RGBA{R: 255, G: 255, A: 255})
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create image: %v", err)
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		t.Fatalf("encode image: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close image: %v", err)
	}

	v := newFileViewer(root)
	if v.imagePreview == nil {
		t.Fatalf("image preview was not loaded; fileContent=%q", v.fileContent)
	}
	if v.imagePreview.format != "png" || v.imagePreview.width != 2 || v.imagePreview.height != 2 {
		t.Fatalf("image metadata = format %q %dx%d, want png 2x2", v.imagePreview.format, v.imagePreview.width, v.imagePreview.height)
	}
	if v.fileContent != "" {
		t.Fatalf("image preview should not use text content, got %q", v.fileContent)
	}
	lines := renderFileViewerImage(v.imagePreview.image, 8, 4)
	if len(lines) == 0 {
		t.Fatal("rendered image preview is empty")
	}
	if !strings.Contains(lines[0], "\x1b[38;2;") || !strings.Contains(lines[0], "▀") {
		t.Fatalf("rendered image line missing colour half-block escapes: %q", lines[0])
	}
}

func TestFileViewerAsciiFallbackDoesNotPrepareGraphicsPayload(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("TERM", "xterm-256color")
	root := t.TempDir()
	path := filepath.Join(root, "preview.png")
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create image: %v", err)
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		t.Fatalf("encode image: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close image: %v", err)
	}

	v := newFileViewer(root)
	if v.imagePreview == nil || v.imagePreview.image == nil {
		t.Fatalf("ascii fallback should still decode a preview image")
	}
	if len(v.imagePreview.data) != 0 {
		t.Fatalf("unsupported terminals should not prepare graphics payload, got %d bytes", len(v.imagePreview.data))
	}
	if got := v.kittyGraphicsOverlay(); got != "" {
		t.Fatalf("unsupported terminal overlay = %q, want empty", got)
	}
}

func TestKittyGraphicsOverlay(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "ghostty")
	v := &fileViewer{
		imagePreview: &fileViewerImagePreview{
			width:  20,
			height: 10,
			data:   []byte("png-data"),
		},
		imagePlacement: fileViewerImagePlacement{x: 2, y: 3, w: 40, h: 20},
	}

	out := v.kittyGraphicsOverlay()
	if !strings.Contains(out, "\x1b_Ga=T") {
		t.Fatalf("overlay missing kitty transmit escape: %q", out)
	}
	if !strings.Contains(out, "f=100") || !strings.Contains(out, "c=40") || !strings.Contains(out, "r=10") {
		t.Fatalf("overlay missing sizing metadata: %q", out)
	}
	if !strings.Contains(out, "cG5nLWRhdGE=") {
		t.Fatalf("overlay missing base64 image payload: %q", out)
	}
}

func TestKittyGraphicsOverlayDisabledOutsideSupportedTerminals(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("TERM", "xterm-256color")
	v := &fileViewer{
		imagePreview:   &fileViewerImagePreview{width: 1, height: 1, data: []byte("x")},
		imagePlacement: fileViewerImagePlacement{x: 1, y: 1, w: 1, h: 1},
	}
	if got := v.kittyGraphicsOverlay(); got != "" {
		t.Fatalf("overlay = %q, want disabled outside kitty/ghostty", got)
	}
}

func TestFileViewerDirectoryPreviewIsBounded(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sub")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for i := range fileViewerDirPreviewLimit + 5 {
		path := filepath.Join(dir, fmt.Sprintf("file-%02d.txt", i))
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("write preview entry: %v", err)
		}
	}

	v := newFileViewer(root)
	if v.dirPreview.path != dir {
		t.Fatalf("dir preview path = %q, want %q", v.dirPreview.path, dir)
	}
	if len(v.dirPreview.entries) != fileViewerDirPreviewLimit {
		t.Fatalf("dir preview entries = %d, want %d", len(v.dirPreview.entries), fileViewerDirPreviewLimit)
	}
	if !v.dirPreview.truncated {
		t.Fatal("directory preview should report truncation")
	}
}

func TestFileViewerEditKeyReturnsSelectedFileAction(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "edit-me.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	v := newFileViewer(root)
	a := v.handleKey(tea.KeyPressMsg{Code: 'e', Text: "e"})
	edit, ok := a.(actionEditFile)
	if !ok {
		t.Fatalf("action = %T, want actionEditFile", a)
	}
	if edit.path != path {
		t.Fatalf("edit path = %q, want %q", edit.path, path)
	}
}

func TestFileViewerEditKeyIgnoresDirectories(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	v := newFileViewer(root)
	if a := v.handleKey(tea.KeyPressMsg{Code: 'e', Text: "e"}); a != (actionNone{}) {
		t.Fatalf("action = %T, want actionNone", a)
	}
}

func TestFileViewerCatalogPreviewMetadata(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "skill.md")
	v := &fileViewer{
		workspaceDir: root,
		mode:         fileViewerSkills,
		skills: []catalog.Skill{{
			Name:        "repo-search",
			Description: "search the repo",
			Body:        "use rg first",
			Source:      source,
		}},
	}
	v.tryPreview()
	v.width, v.height = 100, 30

	buf := uv.NewScreenBuffer(100, 30)
	v.drawCatalogPreview(buf, 0, 0, 80, 12)
	out := ansi.Strip(buf.String())
	for _, want := range []string{"repo-search", "search the repo", "skill.md", "use rg first"} {
		if !strings.Contains(out, want) {
			t.Fatalf("catalog preview missing %q:\n%s", want, out)
		}
	}
}

func TestFileViewerRefreshEditedPathReloadsPreview(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "edit-me.txt")
	if err := os.WriteFile(path, []byte("before"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	v := newFileViewer(root)
	if !strings.Contains(v.fileContent, "before") {
		t.Fatalf("initial preview = %q, want before", v.fileContent)
	}
	if err := os.WriteFile(path, []byte("after"), 0o644); err != nil {
		t.Fatalf("rewrite file: %v", err)
	}
	v.refreshEditedPath(path)
	if !strings.Contains(v.fileContent, "after") {
		t.Fatalf("refreshed preview = %q, want after", v.fileContent)
	}
}
