package engine

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/media"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

const (
	ToolNameImageGenerate  tools.ToolName = "image_generate"
	imageReferenceMaxBytes                = 12 * 1024 * 1024
	imagePreviewMaxBytes                  = 3 * 1024 * 1024
)

// ImageGenerationTool creates an image with the active provider's optional image
// generation capability and saves it as a new workspace file.
type ImageGenerationTool struct {
	workspace code.Workspace
	provider  llm.ImageGenerator
}

// NewImageGenerationTool binds native image generation to the current workspace
// and provider. The generated file is always new and stays within the workspace.
func NewImageGenerationTool(workspace code.Workspace, provider llm.ImageGenerator) *ImageGenerationTool {
	return &ImageGenerationTool{workspace: workspace, provider: provider}
}

type imageGenerationArgs struct {
	Prompt         string `json:"prompt" doc:"Describe the image to generate."`
	ReferenceImage string `json:"reference_image,omitempty" doc:"Optional workspace-relative image path to edit or use as reference."`
}

// ImageGenerationResult describes the generated image saved in the workspace.
type ImageGenerationResult struct {
	Path          string `json:"path"`
	MIMEType      string `json:"mime_type"`
	Bytes         int    `json:"bytes"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

// Definition describes the image generation operation to the model.
func (t *ImageGenerationTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:            ToolNameImageGenerate,
		Description:     "Generate an image using the active OpenAI Codex account. Saves a new PNG under the workspace as generated-image-<id>.png. Optionally provide a workspace-relative reference image to edit. Image generation uses Codex usage limits.",
		Parameters:      tools.SchemaFor[imageGenerationArgs](),
		Mutates:         true,
		WorkspaceAccess: tools.WorkspaceAccesses.WRITE,
	}
}

// Execute generates and saves a new image. Optional references are read only
// from the workspace; the generated output path is host-chosen and never
// overwrites an existing file.
func (t *ImageGenerationTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	args, err := tools.DecodeArgs[imageGenerationArgs](call.Arguments)
	if err != nil {
		return tools.Failure(call.ID, tools.Validation("image_generate", err.Error())), nil
	}
	if strings.TrimSpace(args.Prompt) == "" {
		return tools.Failure(call.ID, tools.Validation("image_generate", "prompt required")), nil
	}

	request := llm.ImageGenerationRequest{Prompt: args.Prompt}
	if args.ReferenceImage != "" {
		part, err := t.referenceImage(args.ReferenceImage)
		if err != nil {
			return tools.Failure(call.ID, err), nil
		}
		request.ReferenceImages = []llm.ContentPart{part}
	}
	image, err := t.provider.GenerateImage(ctx, request)
	if err != nil {
		return tools.Failure(call.ID, tools.Fatal("image_generate", err)), nil
	}
	if err := ctx.Err(); err != nil {
		return tools.Failure(call.ID, tools.Fatal("image_generate", err)), nil
	}
	if image.MIMEType != "image/png" || len(image.Data) == 0 {
		return tools.Failure(call.ID, tools.Fatal("image_generate", errors.New("provider returned an unsupported or empty image"))), nil
	}

	path, err := writeGeneratedImage(t.workspace, image.Data)
	if err != nil {
		return tools.Failure(call.ID, tools.Fatal("image_generate", err)), nil
	}
	result := tools.Success(call.ID, ImageGenerationResult{
		Path: path, MIMEType: image.MIMEType, Bytes: len(image.Data), RevisedPrompt: image.RevisedPrompt,
	})
	result.AddEffect(tools.NewFileEffect(tools.FileCreate, path))
	if len(image.Data) <= imagePreviewMaxBytes {
		result.Parts = []llm.ContentPart{media.ImagePartFromBytes(image.Data, image.MIMEType)}
	}
	return result, nil
}

func (t *ImageGenerationTool) referenceImage(path string) (llm.ContentPart, error) {
	abs, err := t.workspace.Resolve(path)
	if err != nil {
		return llm.ContentPart{}, tools.Permission("image_generate", err.Error())
	}
	info, err := t.workspace.StatPath(abs)
	if err != nil {
		return llm.ContentPart{}, tools.Validation("image_generate", fmt.Sprintf("reference image: %v", err))
	}
	if !info.Mode().IsRegular() {
		return llm.ContentPart{}, tools.Validation("image_generate", "reference image must be a regular file")
	}
	if info.Size() > imageReferenceMaxBytes {
		return llm.ContentPart{}, tools.Budget("image_generate", "reference image exceeds 12 MiB")
	}
	data, err := t.workspace.ReadFilePath(abs)
	if err != nil {
		return llm.ContentPart{}, tools.Fatal("image_generate", fmt.Errorf("read reference image: %w", err))
	}
	mimeType := media.ImageMIME(path, data)
	switch mimeType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return media.ImagePartFromBytes(data, mimeType), nil
	default:
		return llm.ContentPart{}, tools.Validation("image_generate", "reference file is not a supported PNG, JPEG, GIF, or WebP image")
	}
}

func writeGeneratedImage(workspace code.Workspace, data []byte) (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate image filename: %w", err)
	}
	path := "generated-image-" + hex.EncodeToString(random[:]) + ".png"
	abs, err := workspace.Resolve(path)
	if err != nil {
		return "", fmt.Errorf("resolve generated image path: %w", err)
	}
	file, err := workspace.OpenFileInRoot(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("create generated image: %w", err)
	}
	if _, err := io.Copy(file, bytes.NewReader(data)); err != nil {
		_ = file.Close()
		_ = workspace.RemoveInRoot(abs)
		return "", fmt.Errorf("write generated image: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = workspace.RemoveInRoot(abs)
		return "", fmt.Errorf("close generated image: %w", err)
	}
	return path, nil
}

var _ tools.Tool = (*ImageGenerationTool)(nil)
