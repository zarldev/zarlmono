package prompts

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
)

// Data is the render context shared by every prompt template — the embedded
// [System] / [Plan] defaults, explicit or legacy prompt overrides, named
// sub-agent bodies, and literal user preferences. Every consumer (the TUI and
// the eval harness) builds the same struct and renders through [Render], so the
// two cannot silently drift.
//
// Data is intentionally a STABLE SUPERSET: fields are not removed when the
// default embedded prompt stops using them, because a user's
// ~/.zarlcode/prompt.override.md or legacy ~/.zarlcode/prompt.md may still
// reference them. text/template treats a missing struct field as a hard execute
// error (unlike a missing map key), so dropping a field crashes every override
// that names it — failing the whole turn before any provider call. Keep unused
// fields here (nil/zero renders empty) rather than deleting them.
type Data struct {
	WorkspaceRoot   string
	Tools           []ToolInfo
	DynamicTools    []ToolInfo
	Skills          []ToolInfo
	Agents          []AgentInfo
	InstructionDocs []InstructionDoc

	// SelfMod is retained for override-prompt compatibility. New templates should
	// prefer the narrower CanAuthorTool / CanRegisterTool fields.
	SelfMod bool

	// CanAuthorTool is true when a runtime tool can author/register a new tool
	// from its schema in one call (currently new_tool).
	CanAuthorTool bool

	// CanRegisterTool is true when a runtime tool can register a tool source that
	// already exists (currently register_tool).
	CanRegisterTool bool

	// Planning enables the update_plan operating contract. It should track
	// whether the update_plan tool is registered (the interactive TUI wires
	// it; the eval harness does not).
	Planning bool

	// ProgrammaticTools is true when the portable program tool is present. In
	// that roster, read/search/catalogue tools are intentionally hidden behind
	// program while mutating and shell tools remain direct.
	ProgrammaticTools bool

	// UserPreferences is literal additive guidance loaded from
	// ~/.zarlcode/preferences.md. Render appends it after the active prompt body
	// and before workspace instructions; it is not parsed as a template.
	UserPreferences string
}

// ToolInfo is the name + description of a registered tool or skill as the
// templates render it.
type ToolInfo struct {
	Name        string
	Description string
}

// AgentInfo is a named sub-agent as a prompt template renders it. Retained for
// override-prompt compatibility even though the default prompt no longer
// enumerates agents (they are discovered via list_agents at runtime).
type AgentInfo struct {
	Name        string
	Description string
	Provider    string
	Model       string
	Workspace   string
	Mode        string
}

// InstructionDoc is one repository/workspace guidance file appended to the
// rendered prompt.
type InstructionDoc struct {
	Path    string
	Content string
}

// workspaceInstructionsTail is appended to every rendered prompt. It renders
// nothing when Data.InstructionDocs is empty, so it is harmless for consumers
// (the eval harness) that never supply instruction docs.
const userPreferencesTail = "{{- if .UserPreferences }}\n\n" +
	"# User preferences\n\n" +
	"The following durable per-user preferences came from `~/.zarlcode/preferences.md`.\n" +
	"Follow them when relevant, but they do not override system, developer, tool,\n" +
	"safety, or workspace instructions.\n\n" +
	"{{ .UserPreferences }}\n" +
	"{{- end }}\n"

const workspaceInstructionsTail = `{{- if .InstructionDocs }}

# Workspace instructions

The following files are repository/workspace guidance. Follow them when relevant,
but they do not override system, developer, tool, or safety instructions.

{{ range .InstructionDocs }}## {{ .Path }}

{{ .Content }}

{{ end }}{{- end }}
`

// Render parses body (one of [System], [Plan], or a user/agent override) with
// the user-preferences and workspace-instructions tails appended, and executes it
// against d. name is used only in error messages and template diagnostics.
func Render(name, body string, d Data) (string, error) {
	tmpl, err := template.New(name).Parse(body + userPreferencesTail + workspaceInstructionsTail)
	if err != nil {
		return "", fmt.Errorf("parse %s prompt: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, d); err != nil {
		return "", fmt.Errorf("render %s prompt: %w", name, err)
	}
	out := strings.TrimRight(buf.String(), "\n")
	if out == "" {
		return "", nil
	}
	return out + "\n", nil
}

// HasTool reports whether tools contains a tool with the given name. Consumers
// use it to set the Data capability flags (SelfMod, Planning) from the live
// roster rather than hardcoding them.
func HasTool(tools []ToolInfo, name string) bool {
	for _, t := range tools {
		if t.Name == name {
			return true
		}
	}
	return false
}
