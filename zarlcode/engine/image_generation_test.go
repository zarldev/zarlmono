package engine_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

type imageProvider struct {
	data       []byte
	gotPrompt  string
	references int
}

func (p *imageProvider) Name() string { return "image-fixture" }

func (*imageProvider) Complete(context.Context, llm.CompletionRequest) llm.CompletionStream {
	return func(func(llm.CompletionChunk, error) bool) {}
}

func (p *imageProvider) GenerateImage(_ context.Context, request llm.ImageGenerationRequest) (llm.GeneratedImage, error) {
	p.gotPrompt = request.Prompt
	p.references = len(request.ReferenceImages)
	return llm.GeneratedImage{Data: p.data, MIMEType: "image/png", RevisedPrompt: "fixture revised prompt"}, nil
}

func TestImageGenerationToolSavesAndReturnsImage(t *testing.T) {
	root := t.TempDir()
	workspace, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	provider := &imageProvider{data: testEnginePNG(t)}
	tool := engine.NewImageGenerationTool(workspace, provider)
	t.Cleanup(func() { _ = workspace.Close() })

	definition := tool.Definition()
	if !definition.Mutates || definition.WorkspaceAccess != tools.WorkspaceAccesses.WRITE {
		t.Fatalf("image tool lacks write metadata: %+v", definition)
	}
	result, err := tool.Execute(t.Context(), tools.ToolCall{ID: "image", Arguments: tools.ToolParameters{"prompt": "a tiny red square"}})
	if err != nil || !result.Success {
		t.Fatalf("Execute: result=%+v err=%v", result, err)
	}
	if provider.gotPrompt != "a tiny red square" || provider.references != 0 {
		t.Fatalf("provider request = prompt %q, references %d", provider.gotPrompt, provider.references)
	}
	data, ok := tools.DataAs[engine.ImageGenerationResult](result)
	if !ok || data.Path == "" || filepath.IsAbs(data.Path) || data.MIMEType != "image/png" || data.Bytes != len(provider.data) || data.RevisedPrompt != "fixture revised prompt" {
		t.Fatalf("image result = %#v", result.Data)
	}
	saved, err := workspace.ReadFileInRoot(filepath.Join(root, data.Path))
	if err != nil || !bytes.Equal(saved, provider.data) {
		t.Fatalf("saved image: bytes match=%v err=%v", bytes.Equal(saved, provider.data), err)
	}
	if len(result.Parts) != 1 || result.Parts[0].Type != llm.ContentTypeImage || result.Parts[0].Image == nil {
		t.Fatalf("image preview attachment = %#v", result.Parts)
	}
	effects := result.FileEffects()
	if len(effects) != 1 || effects[0].Path != data.Path || effects[0].Op != tools.FileCreate {
		t.Fatalf("file effects = %#v", effects)
	}
}

func TestImageGenerationToolReadsOnlyWorkspaceReference(t *testing.T) {
	root := t.TempDir()
	workspace, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	defer workspace.Close()
	provider := &imageProvider{data: testEnginePNG(t)}
	tool := engine.NewImageGenerationTool(workspace, provider)
	refPath := filepath.Join(root, "reference.png")
	if err := os.WriteFile(refPath, testEnginePNG(t), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := tool.Execute(t.Context(), tools.ToolCall{ID: "image", Arguments: tools.ToolParameters{
		"prompt": "edit this reference", "reference_image": "reference.png",
	}})
	if err != nil || !result.Success || provider.references != 1 {
		t.Fatalf("reference generation: result=%+v references=%d err=%v", result, provider.references, err)
	}

	outside := filepath.Join(t.TempDir(), "outside.png")
	if err := os.WriteFile(outside, testEnginePNG(t), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape.png")); err != nil {
		t.Fatal(err)
	}
	for _, reference := range []string{"../outside.png", "escape.png"} {
		result, err := tool.Execute(t.Context(), tools.ToolCall{ID: "blocked", Arguments: tools.ToolParameters{
			"prompt": "edit", "reference_image": reference,
		}})
		if err != nil || result.Success || result.Err == nil || result.Err.Kind != tools.Kinds.PERMISSION {
			t.Fatalf("reference %q: result=%+v err=%v", reference, result, err)
		}
	}
}

func TestImageGenerationToolIsRegisteredOnlyForCapableProvider(t *testing.T) {
	for _, tc := range []struct {
		name      string
		provider  llm.Provider
		available bool
	}{
		{name: "capable provider", provider: &imageProvider{data: testEnginePNG(t)}, available: true},
		{name: "ordinary provider", provider: ordinaryImageTestProvider{}, available: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace, err := code.NewWorkspace(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			live := engine.NewLiveRunner(tc.provider, workspace, "gpt-6-astra")
			t.Cleanup(func() {
				cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), time.Second)
				defer cancel()
				if err := live.Close(cleanupCtx); err != nil {
					t.Error(err)
				}
				if err := workspace.Close(); err != nil {
					t.Error(err)
				}
			})
			inspection := live.Inspect(t.Context())
			found := slices.ContainsFunc(inspection.Tools, func(spec tools.ToolSpec) bool {
				return spec.Name == engine.ToolNameImageGenerate
			})
			if found != tc.available {
				t.Fatalf("image_generate available = %v, want %v; inspect errors: %v", found, tc.available, inspection.Errors)
			}
		})
	}
}

type ordinaryImageTestProvider struct{}

func (ordinaryImageTestProvider) Name() string { return "text-only" }
func (ordinaryImageTestProvider) Complete(context.Context, llm.CompletionRequest) llm.CompletionStream {
	return func(func(llm.CompletionChunk, error) bool) {}
}

func testEnginePNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{G: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatal("PNG encoder returned invalid signature")
	}
	return buf.Bytes()
}
