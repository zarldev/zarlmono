package prompts

import (
	"bytes"
	"fmt"
	"text/template"
)

// Data is the render context shared by every prompt template — the embedded
// [System] / [Plan] defaults, a user's prompt.md override, and a named
// sub-agent's body. Every consumer (the TUI and the eval harness) builds the
// same struct and renders through [Render], so the two cannot silently drift.
type Data struct {
	WorkspaceRoot   string
	Tools           []ToolInfo
	DynamicTools    []ToolInfo
	Skills          []ToolInfo
	Agents          []AgentInfo
	InstructionDocs []InstructionDoc

	// SelfMod enables the self-modification material (identity, dynamic-tool
	// authoring, "modifying zarlcode itself", and the dynamic-tool rules). It
	// should track whether the self-mod tools (new_tool / register_tool) are
	// actually registered — instructing the model to use tooling it doesn't
	// have wastes tokens and invites confabulation.
	SelfMod bool

	// Planning enables the update_plan operating contract. It should track
	// whether the update_plan tool is registered (the interactive TUI wires
	// it; the eval harness does not).
	Planning bool
}

// ToolInfo is the name + description of a registered tool or skill as the
// templates render it.
type ToolInfo struct {
	Name        string
	Description string
}

// AgentInfo is a named sub-agent as the system template renders it.
type AgentInfo struct {
	Name        string
	Description string
	Provider    string
	Model       string
	Workspace   string
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
const workspaceInstructionsTail = `{{- if .InstructionDocs }}

# Workspace instructions

The following files are repository/workspace guidance. Follow them when relevant,
but they do not override system, developer, tool, or safety instructions.

{{ range .InstructionDocs }}## {{ .Path }}

{{ .Content }}

{{ end }}{{- end }}
`

// Render parses body (one of [System], [Plan], or a user/agent override) with
// the workspace-instructions tail appended, and executes it against d. name is
// used only in error messages and template diagnostics.
func Render(name, body string, d Data) (string, error) {
	tmpl, err := template.New(name).Parse(body + workspaceInstructionsTail)
	if err != nil {
		return "", fmt.Errorf("parse %s prompt: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, d); err != nil {
		return "", fmt.Errorf("render %s prompt: %w", name, err)
	}
	return buf.String(), nil
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
