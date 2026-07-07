package computer

import (
	"context"
	"errors"

	model "github.com/zarldev/zarlmono/zkit/agent/computer"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// ObserveArgs controls which optional fields computer_observe returns.
type ObserveArgs struct {
	IncludeScreenshot bool `json:"include_screenshot,omitempty" doc:"Include a screenshot as an image data URI. Screenshots can be large; request them only when visual context is needed."`
	IncludeTargets    bool `json:"include_targets,omitempty" doc:"Include discovered interactive or semantic targets such as links, buttons, inputs, and role-labelled elements."`
	IncludeText       bool `json:"include_text,omitempty" doc:"Include visible surface text."`
	IncludeRaw        bool `json:"include_raw,omitempty" doc:"Include backend-specific raw metadata. This is an escape hatch, not the portable contract."`
}

// ObserveTool observes a computer surface through a model.Observer backend.
type ObserveTool struct {
	observer model.Observer
	tool     tools.Tool
}

// NewObserveTool returns the typed computer_observe tool backed by observer.
func NewObserveTool(observer model.Observer) *ObserveTool {
	t := &ObserveTool{observer: observer}
	t.tool = tools.NewTyped(observeSpec(), t.executeTyped)
	return t
}

// Definition advertises computer_observe with ObserveArgs parameters.
func (t *ObserveTool) Definition() tools.ToolSpec { return observeSpec() }

// Execute decodes ObserveArgs and returns a model.Observation from the backend.
func (t *ObserveTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	return t.tool.Execute(ctx, call)
}

func observeSpec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        ToolNameComputerObserve,
		Description: "Observe the current computer surface. Returns surface metadata and, when requested, visible text, semantic targets, screenshot, and backend-specific raw metadata.",
		Parameters:  tools.SchemaFor[ObserveArgs](),
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
	return obs, nil
}
