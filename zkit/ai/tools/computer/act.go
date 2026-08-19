package computer

import (
	"context"
	"errors"

	model "github.com/zarldev/zarlmono/zkit/agent/computer"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// Point describes a two-dimensional point or delta in surface coordinates.
type Point struct {
	X int `json:"x,omitempty" doc:"Horizontal coordinate/delta."`
	Y int `json:"y,omitempty" doc:"Vertical coordinate/delta."`
}

// TargetRef identifies an action or trigger target. Resolution is backend
// specific, but browser backends prefer id, then locator, semantic fields, then
// position.
type TargetRef struct {
	ID       string `json:"id,omitempty" doc:"Target id from observe; preferred."`
	Role     string `json:"role,omitempty" doc:"Semantic role."`
	Name     string `json:"name,omitempty" doc:"Accessible name."`
	Text     string `json:"text,omitempty" doc:"Visible text."`
	Locator  string `json:"locator,omitempty" doc:"Backend locator (for example CSS)."`
	Position *Point `json:"position,omitempty" doc:"Fallback coordinates."`
}

// Action describes an operation to perform on the current computer surface.
type Action struct {
	Kind   model.ActionKind `json:"kind" enum:"navigate,click,fill,press,scroll" doc:"Action kind."`
	Target *TargetRef       `json:"target,omitempty" doc:"Target for click/fill/press."`
	Value  string           `json:"value,omitempty" doc:"Fill value."`
	Key    string           `json:"key,omitempty" doc:"Key name or text to press."`
	URL    string           `json:"url,omitempty" doc:"Navigation URL."`
	Delta  *Point           `json:"delta,omitempty" doc:"Movement/scroll delta."`
}

// Trigger describes a condition used by When and Until. When is an action
// precondition; Until is a completion/settlement condition after the action.
type Trigger struct {
	Kind   model.TriggerKind `json:"kind" enum:"visible,hidden,focused,text_present,value_equals,url_matches,navigation_complete,surface_stable" doc:"Condition kind."`
	Target *TargetRef        `json:"target,omitempty" doc:"Condition target."`
	Text   string            `json:"text,omitempty" doc:"Expected text."`
	Value  string            `json:"value,omitempty" doc:"Expected value."`
	URL    string            `json:"url,omitempty" doc:"Expected URL substring."`
}

// ActArgs describes a computer_act request. When is checked before the action;
// Until is checked after the action before the resulting observation is returned.
type ActArgs struct {
	Action Action   `json:"action" doc:"Action to perform."`
	When   *Trigger `json:"when,omitempty" doc:"Precondition before the action."`
	Until  *Trigger `json:"until,omitempty" doc:"Completion condition after it."`
}

// ActTool applies computer actions through a model.Actor backend.
type ActTool struct {
	actor model.Actor
	tool  tools.Tool
}

// NewActTool returns the typed computer_act tool backed by actor.
func NewActTool(actor model.Actor) *ActTool {
	t := &ActTool{actor: actor}
	t.tool = tools.NewTyped(actSpec(), t.executeTyped)
	return t
}

// Definition advertises computer_act with ActArgs parameters.
func (t *ActTool) Definition() tools.ToolSpec { return actSpec() }

// Execute decodes ActArgs and returns the resulting model.Observation from the
// backend.
func (t *ActTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	return t.tool.Execute(ctx, call)
}

func actSpec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:             ToolNameComputerAct,
		Description:      "Act on the current surface; optionally wait for a precondition and post-action completion condition.",
		Parameters:       tools.SchemaFor[ActArgs](),
		AffectsWorkspace: true,
	}
}

func (t *ActTool) executeTyped(ctx context.Context, args ActArgs) (model.Observation, error) {
	if t.actor == nil {
		return model.Observation{}, tools.Fatal("computer_act", errors.New("actor backend is nil"))
	}
	req, err := args.toModel()
	if err != nil {
		return model.Observation{}, err
	}
	obs, err := t.actor.Act(ctx, req)
	if err != nil {
		return model.Observation{}, tools.Fatal("computer_act", err)
	}
	return obs, nil
}

func (a ActArgs) toModel() (model.ActionRequest, error) {
	if !a.Action.Kind.IsValid() {
		return model.ActionRequest{}, tools.Validation("computer_act", "action.kind must be one of navigate, click, fill, press, scroll")
	}
	req := model.ActionRequest{
		Action: model.Action{
			Kind:   a.Action.Kind,
			Target: a.Action.Target.toModel(),
			Value:  a.Action.Value,
			Key:    a.Action.Key,
			URL:    a.Action.URL,
			Delta:  a.Action.Delta.toModel(),
		},
	}
	if a.When != nil {
		when, err := a.When.toModel()
		if err != nil {
			return model.ActionRequest{}, err
		}
		req.When = when
	}
	if a.Until != nil {
		until, err := a.Until.toModel()
		if err != nil {
			return model.ActionRequest{}, err
		}
		req.Until = until
	}
	return req, nil
}

func (t *Trigger) toModel() (*model.Trigger, error) {
	if t == nil {
		return nil, tools.Validation("computer_act", "trigger is nil")
	}
	if !t.Kind.IsValid() {
		return nil, tools.Validation("computer_act", "trigger.kind must be one of visible, hidden, focused, text_present, value_equals, navigation_complete, surface_stable")
	}
	return &model.Trigger{
		Kind:   t.Kind,
		Target: t.Target.toModel(),
		Text:   t.Text,
		Value:  t.Value,
		URL:    t.URL,
	}, nil
}

func (t *TargetRef) toModel() *model.TargetRef {
	if t == nil {
		return nil
	}
	return &model.TargetRef{
		ID:       t.ID,
		Role:     t.Role,
		Name:     t.Name,
		Text:     t.Text,
		Locator:  t.Locator,
		Position: t.Position.toModel(),
	}
}

func (p *Point) toModel() *model.Point {
	if p == nil {
		return nil
	}
	return &model.Point{X: p.X, Y: p.Y}
}
