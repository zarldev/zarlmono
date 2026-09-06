package computer

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"

	model "github.com/zarldev/zarlmono/zkit/agent/computer"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// ObserveArgs controls which optional fields computer_observe returns.
type ObserveArgs struct {
	IncludeScreenshot bool `json:"include_screenshot,omitempty" doc:"Include a screenshot as image content (up to 4 MiB encoded). Request only when visual context is needed."`
	IncludeTargets    bool `json:"include_targets,omitempty" doc:"Include discovered interactive or semantic targets such as links, buttons, inputs, and role-labelled elements."`
	IncludeText       bool `json:"include_text,omitempty" doc:"Include visible surface text."`
	IncludeRaw        bool `json:"include_raw,omitempty" doc:"Include backend-specific raw metadata. This is an escape hatch, not the portable contract."`
}

// ObserveTool observes a computer surface through a model.Observer backend.
type ObserveTool struct {
	observer model.Observer
}

// NewObserveTool returns the computer_observe tool backed by observer.
func NewObserveTool(observer model.Observer) *ObserveTool {
	return &ObserveTool{observer: observer}
}

// Definition advertises computer_observe with ObserveArgs parameters.
func (t *ObserveTool) Definition() tools.ToolSpec { return observeSpec() }

// Execute returns observation metadata in Data and the screenshot, if present,
// as an image in Parts rather than base64 embedded in the metadata text.
func (t *ObserveTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	args, err := tools.DecodeArgs[ObserveArgs](call.Arguments)
	if err != nil {
		return tools.Failure(call.ID, err), nil
	}
	obs, err := t.executeTyped(ctx, args)
	if err != nil {
		return tools.Failure(call.ID, err), nil
	}
	var parts []llm.ContentPart
	if obs.Screenshot != nil {
		parts = []llm.ContentPart{llm.ImagePartFromDataURI(obs.Screenshot.DataURI, obs.Screenshot.MIMEType)}
		// Keep metadata without mutating the backend's image or duplicating bytes.
		obs.Screenshot = &model.ObservationImage{MIMEType: obs.Screenshot.MIMEType}
	}
	result := tools.Success(call.ID, obs)
	result.Parts = parts
	return result, nil
}

func observeSpec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:            ToolNameComputerObserve,
		WorkspaceAccess: tools.WorkspaceAccesses.NONE,
		Description:     "Observe the current computer surface. Returns surface metadata and, when requested, visible text, semantic targets, screenshot, and backend-specific raw metadata.",
		Parameters:      tools.SchemaFor[ObserveArgs](),
	}
}

func (t *ObserveTool) executeTyped(ctx context.Context, args ObserveArgs) (model.Observation, error) {
	if t.observer == nil {
		return model.Observation{}, tools.Fatal("computer_observe", errors.New("observer backend is nil"))
	}
	obs, err := t.observer.Observe(ctx, model.ObserveRequest(args))
	if err != nil {
		return model.Observation{}, tools.Fatal("computer_observe", err)
	}
	// Images bypass the runner's text truncator; reject oversized screenshots
	// intact instead of sending truncated, undecodable image data.
	const maxScreenshotDataURIBytes = 4 * 1024 * 1024
	if obs.Screenshot != nil && len(obs.Screenshot.DataURI) > maxScreenshotDataURIBytes {
		return model.Observation{}, tools.Budget("computer_observe", "screenshot exceeds 4 MiB encoded; use a smaller surface or observe without a screenshot")
	}
	if obs.Screenshot != nil {
		if err := validateScreenshot(*obs.Screenshot); err != nil {
			return model.Observation{}, tools.Fatal("computer_observe", err)
		}
	}
	return obs, nil
}

func validateScreenshot(image model.ObservationImage) error {
	switch image.MIMEType {
	case "image/png", "image/jpeg", "image/gif":
	default:
		return errors.New("screenshot MIME type must be image/png, image/jpeg, or image/gif")
	}
	prefix := "data:" + image.MIMEType + ";base64,"
	if !strings.HasPrefix(image.DataURI, prefix) {
		return errors.New("screenshot must be a base64 data URI matching its MIME type")
	}
	payload := strings.TrimPrefix(image.DataURI, prefix)
	if payload == "" {
		return errors.New("screenshot data URI is empty")
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return errors.New("screenshot data URI contains invalid base64")
	}
	reader := bytes.NewReader(data)
	// DecodeConfig verifies the declared format header without allocating a
	// potentially amplified pixel buffer from untrusted compressed tool output.
	switch image.MIMEType {
	case "image/png":
		_, err = png.DecodeConfig(reader)
	case "image/jpeg":
		_, err = jpeg.DecodeConfig(reader)
	case "image/gif":
		_, err = gif.DecodeConfig(reader)
	}
	if err != nil {
		return errors.New("screenshot bytes do not match the declared image MIME type")
	}
	return nil
}
