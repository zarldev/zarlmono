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
	schemaAdditional       = "additionalProperties"
	schemaDescription      = "description"
	schemaProperties       = "properties"
	schemaPropName         = "name"
	schemaRequired         = "required"
	schemaType             = "type"
	schemaTypeObject       = "object"
	schemaPropInstructions = "instructions"
	schemaTypeString       = "string"
)

const (
	ToolNameCreateSkill tools.ToolName = "skill_create"
	ToolNameLoadSkill   tools.ToolName = "skill_load"
	ToolNameListSkills  tools.ToolName = "list_skills"
	ToolNameListAgents  tools.ToolName = "list_agents"
)

type loadSkillTool struct{ catalog *RuntimeCatalog }

type loadSkillArgs struct {
	Name string `json:"name"`
}

type createSkillTool struct{ catalog *RuntimeCatalog }

type createSkillArgs struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	Instructions string `json:"instructions"`
}

// NewCreateSkillTool creates the BUILD-mode self-extension tool.
func NewCreateSkillTool(c *RuntimeCatalog) *createSkillTool { return &createSkillTool{catalog: c} }

func (t *createSkillTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:            ToolNameCreateSkill,
		WorkspaceAccess: tools.WorkspaceAccesses.WRITE,
		Mutates:         true,
		Description: "Create a new reusable Agent Skill in the canonical user skill directory. " +
			"Use when the user asks to add, author, or save a skill. The tool chooses the path, " +
			"writes standard <name>/SKILL.md frontmatter, and never overwrites an existing skill.",
		Parameters: llm.SchemaFromMap(map[string]any{
			schemaType: schemaTypeObject,
			schemaProperties: map[string]any{
				schemaPropName:         map[string]any{schemaType: schemaTypeString, schemaDescription: "Portable skill name: 1-64 lowercase letters, numbers, and hyphens."},
				schemaDescription:      map[string]any{schemaType: schemaTypeString, schemaDescription: "What the skill does and when the agent should load it."},
				schemaPropInstructions: map[string]any{schemaType: schemaTypeString, schemaDescription: "Complete markdown instructions for performing the skill."},
			},
			schemaRequired:   []string{schemaPropName, schemaDescription, schemaPropInstructions},
			schemaAdditional: false,
		})}
}

func (t *createSkillTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	args, err := tools.DecodeArgs[createSkillArgs](call.Arguments)
	if err != nil {
		return tools.Failure(call.ID, err), nil
	}
	path, err := catalog.CreateSkill(strings.TrimSpace(args.Name), args.Description, args.Instructions)
	if err != nil {
		return tools.Failure(call.ID, tools.Validation("skill_create", err.Error())), nil
	}
	if t.catalog != nil {
		t.catalog.ReloadCurrent()
	}
	return &tools.ToolResult{ToolCallID: call.ID, Success: true, Data: "created skill: " + path, ExecutedAt: time.Now()}, nil
}

func NewLoadSkillTool(c *RuntimeCatalog) *loadSkillTool { return &loadSkillTool{catalog: c} }

func (t *loadSkillTool) refresh() {
	if t != nil && t.catalog != nil {
		t.catalog.ReloadCurrent()
	}
}

func (t *loadSkillTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:            ToolNameLoadSkill,
		WorkspaceAccess: tools.WorkspaceAccesses.READ,
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
		return tools.Failure(call.ID, tools.Validation("skill_load", "name is required")), nil
	}
	skill, ok := t.catalog.Skill(name)
	if !ok {
		t.refresh()
		skill, ok = t.catalog.Skill(name)
	}
	if !ok {
		return tools.Failure(call.ID, tools.NotFound("skill_load", fmt.Sprintf(
			"no skill named %q. Available: %s", name, strings.Join(t.catalog.SkillNames(), ", ")))), nil
	}
	return &tools.ToolResult{ToolCallID: call.ID, Success: true, Data: skill.Body, ExecutedAt: time.Now()}, nil
}

type listSkillsTool struct{ catalog *RuntimeCatalog }

func NewListSkillsTool(c *RuntimeCatalog) *listSkillsTool { return &listSkillsTool{catalog: c} }

func (t *listSkillsTool) refresh() {
	if t != nil && t.catalog != nil {
		t.catalog.ReloadCurrent()
	}
}

func (t *listSkillsTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:            ToolNameListSkills,
		WorkspaceAccess: tools.WorkspaceAccesses.READ,
		Description: "Return the workspace skill catalogue as labelled plaintext — one entry per skill " +
			"with name, description, and path. Call only when the user asks about skills or " +
			"when a task clearly needs a skill lookup.",
		Parameters: llm.SchemaFromMap(map[string]any{schemaType: schemaTypeObject, schemaProperties: map[string]any{}, schemaAdditional: false})}
}

func (t *listSkillsTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	t.refresh()
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

func (t *listAgentsTool) refresh() {
	if t != nil && t.catalog != nil {
		t.catalog.ReloadCurrent()
	}
}

func (t *listAgentsTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:            ToolNameListAgents,
		WorkspaceAccess: tools.WorkspaceAccesses.READ,
		Description: "Return the workspace named sub-agent catalogue as labelled plaintext — one entry " +
			"per agent with name, description, provider/model/workspace when set, and path. Call " +
			"only when the user asks about sub-agents or delegation is clearly needed.",
		Parameters: llm.SchemaFromMap(map[string]any{schemaType: schemaTypeObject, schemaProperties: map[string]any{}, schemaAdditional: false})}
}

func (t *listAgentsTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	t.refresh()
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
		attrs := agentRunAttrs(a)
		if attrs != "" {
			attrs = "  (" + attrs + ")"
		}
		fmt.Fprintf(&b, "  %s%s  — %s%s\n", a.Name, pad, a.Description, attrs)
		fmt.Fprintf(&b, "    path: %s\n", a.Source)
	}
	return strings.TrimRight(b.String(), "\n")
}

func agentRunAttrs(a catalog.Agent) string {
	var attrs []string
	if a.Provider != "" {
		attrs = append(attrs, a.Provider)
	}
	if a.Model != "" {
		attrs = append(attrs, a.Model)
	}
	if a.Mode != "" {
		attrs = append(attrs, "mode="+a.Mode)
	}
	return strings.Join(attrs, " · ")
}
