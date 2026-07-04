package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/catalog"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

const (
	schemaAdditional = "additionalProperties"
	schemaType       = "type"
	schemaTypeObject = "object"
	schemaProperties = "properties"
	schemaPropName   = "name"
)

const (
	ToolNameLoadSkill  tools.ToolName = "load_skill"
	ToolNameListSkills tools.ToolName = "list_skills"
	ToolNameListAgents tools.ToolName = "list_agents"
)

type loadSkillTool struct{ catalog *RuntimeCatalog }

type loadSkillArgs struct {
	Name string `json:"name"`
}

func NewLoadSkillTool(c *RuntimeCatalog) *loadSkillTool { return &loadSkillTool{catalog: c} }

func (t *loadSkillTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name: ToolNameLoadSkill,
		Description: "Load a skill's markdown body into context by exact name. Use this only " +
			"when the user asks for a skill or after listing skills to choose one; do not guess " +
			"skill names and do not use read(<path>) for skill bodies.",
		Parameters: llm.SchemaFromMap(map[string]any{
			schemaType: schemaTypeObject,
			schemaProperties: map[string]any{
				schemaPropName: map[string]any{
					schemaType:    "string",
					"description": "Exact skill name from list_skills or the user's request.",
				},
			},
			"required":       []string{schemaPropName},
			schemaAdditional: false,
		})}
}

func (t *loadSkillTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	args, derr := tools.DecodeArgs[loadSkillArgs](call.Arguments)
	if derr != nil {
		return tools.Failure(call.ID, derr), nil
	}
	name := strings.TrimSpace(args.Name)
	if name == "" {
		return tools.Failure(call.ID, tools.Validation("load_skill", "name is required")), nil
	}
	skill, ok := t.catalog.Skill(name)
	if !ok {
		return tools.Failure(call.ID, tools.NotFound("load_skill", fmt.Sprintf(
			"no skill named %q. Available: %s", name, strings.Join(t.catalog.SkillNames(), ", ")))), nil
	}
	return &tools.ToolResult{ToolCallID: call.ID, Success: true, Data: skill.Body, ExecutedAt: time.Now()}, nil
}

type listSkillsTool struct{ catalog *RuntimeCatalog }

func NewListSkillsTool(c *RuntimeCatalog) *listSkillsTool { return &listSkillsTool{catalog: c} }

func (t *listSkillsTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name: ToolNameListSkills,
		Description: "Return the workspace skill catalogue as labelled plaintext — one entry per skill " +
			"with name, description, and path. Call only when the user asks about skills or " +
			"when a task clearly needs a skill lookup.",
		Parameters: llm.SchemaFromMap(map[string]any{schemaType: schemaTypeObject, schemaProperties: map[string]any{}, schemaAdditional: false})}
}

func (t *listSkillsTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	return &tools.ToolResult{ToolCallID: call.ID, Success: true, Data: renderSkillsLabeled(t.catalog.Skills()), ExecutedAt: time.Now()}, nil
}

func renderSkillsLabeled(skills []catalog.Skill) string {
	var b strings.Builder
	fmt.Fprintf(&b, "skills: %d\n", len(skills))
	if len(skills) == 0 {
		b.WriteString("(none authored)")
		return b.String()
	}
	nameWidth := 0
	for _, s := range skills {
		if n := ansi.StringWidth(s.Name); n > nameWidth {
			nameWidth = n
		}
	}
	for _, s := range skills {
		pad := strings.Repeat(" ", nameWidth-ansi.StringWidth(s.Name))
		fmt.Fprintf(&b, "  %s%s  — %s\n", s.Name, pad, s.Description)
		fmt.Fprintf(&b, "    path: %s\n", s.Source)
	}
	return strings.TrimRight(b.String(), "\n")
}

type listAgentsTool struct{ catalog *RuntimeCatalog }

func NewListAgentsTool(c *RuntimeCatalog) *listAgentsTool { return &listAgentsTool{catalog: c} }

func (t *listAgentsTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name: ToolNameListAgents,
		Description: "Return the workspace named sub-agent catalogue as labelled plaintext — one entry " +
			"per agent with name, description, provider/model/workspace when set, and path. Call " +
			"only when the user asks about sub-agents or delegation is clearly needed.",
		Parameters: llm.SchemaFromMap(map[string]any{schemaType: schemaTypeObject, schemaProperties: map[string]any{}, schemaAdditional: false})}
}

func (t *listAgentsTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	return &tools.ToolResult{ToolCallID: call.ID, Success: true, Data: renderAgentsLabeled(t.catalog.Agents()), ExecutedAt: time.Now()}, nil
}

func renderAgentsLabeled(agents []catalog.Agent) string {
	var b strings.Builder
	fmt.Fprintf(&b, "agents: %d\n", len(agents))
	if len(agents) == 0 {
		b.WriteString("(none authored)")
		return b.String()
	}
	nameWidth := 0
	for _, a := range agents {
		if n := ansi.StringWidth(a.Name); n > nameWidth {
			nameWidth = n
		}
	}
	for _, a := range agents {
		pad := strings.Repeat(" ", nameWidth-ansi.StringWidth(a.Name))
		runs := ""
		switch {
		case a.Provider != "" && a.Model != "":
			runs = fmt.Sprintf("  (%s · %s)", a.Provider, a.Model)
		case a.Provider != "":
			runs = fmt.Sprintf("  (%s)", a.Provider)
		case a.Model != "":
			runs = fmt.Sprintf("  (%s)", a.Model)
		}
		fmt.Fprintf(&b, "  %s%s  — %s%s\n", a.Name, pad, a.Description, runs)
		fmt.Fprintf(&b, "    path: %s\n", a.Source)
	}
	return strings.TrimRight(b.String(), "\n")
}
