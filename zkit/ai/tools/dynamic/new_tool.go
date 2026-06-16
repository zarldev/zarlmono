package dynamic

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/filesystem"
)

// ToolNameNewTool is the agent-facing tool that scaffolds a fresh
// dynamic tool from a template, then hands off to build_tool.
const ToolNameNewTool tools.ToolName = "new_tool"

// NewToolTool scaffolds a tools/<name>/main.go from the documented
// toolkit shape, then runs the build+register cycle through the
// existing build_tool. The agent provides the Args struct fields,
// the handler body, and any extra imports — everything else
// (package main, the toolkit.Run call, the Func signature) is fixed
// boilerplate this tool emits verbatim.
//
// Crucial: never emits a go.mod and never gives the agent a chance
// to. Pair with build_tool's "delete stray go.mod/go.sum" scrub for
// belt-and-braces protection.
type NewToolTool struct {
	builder *BuildTool
	wsRoot  string
}

// NewToolArgs is the typed argument struct NewToolTool.Execute
// decodes into via tools.DecodeArgs. Field tags match the JSON
// Schema in Definition; the optional fields are omitempty-compatible
// (zero string = unset).
type NewToolArgs struct {
	Name        string `json:"name" doc:"snake_case tool name; matches ^[a-z][a-z0-9_]{1,63}$."`
	Description string `json:"description" doc:"One-line summary the LLM will see in the tool registry."`
	ArgsFields  string `json:"args_fields,omitempty" doc:"The fields of the Args struct, each on its own line with json + doc tags. Empty when the tool takes no arguments."`
	OutType     string `json:"out_type,omitempty" doc:"Return type of the Func. Defaults to \"string\". Common values: \"string\", \"map[string]any\", \"[]string\"."`
	Body        string `json:"body" doc:"The handler body. ctx (context.Context) and args (Args) are in scope. Must return (out, error)."`
	Imports     string `json:"imports,omitempty" doc:"Optional extra imports, one quoted path per line. context and the toolkit are pre-imported."`
	// Replace must be true to overwrite an existing tools/<name>/main.go.
	// Default false makes a silent destructive overwrite impossible:
	// asking new_tool for a name already on disk returns the existing
	// path in the error so the model can read/edit/build manually.
	Replace bool `json:"replace,omitempty" doc:"Set true to overwrite an existing tools/<name>/main.go. Defaults false: asking for a name already on disk refuses with the existing path so local edits cannot be destroyed silently."`
}

// NewNewToolTool wires the scaffolder to an existing BuildTool and
// the workspace root. Both are needed: builder for the
// compile+register cycle, wsRoot to resolve tools/<name>/.
func NewNewToolTool(builder *BuildTool, wsRoot string) *NewToolTool {
	return &NewToolTool{builder: builder, wsRoot: wsRoot}
}

// Definition advertises new_tool: name, description, and body are
// required; args_fields, out_type, imports, and replace are optional.
// Declares Mutates:true — a successful call writes tools/<name>/main.go
// and registers a binary, so it's gated out of read-only spawn modes.
func (t *NewToolTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name: ToolNameNewTool,
		Description: "Author + build + register a dynamic Go tool from typed pieces in ONE call. " +
			"Pass name, description, args_fields (the body of the Args struct), body (the handler body), " +
			"and optional out_type / imports. The tool writes tools/<name>/main.go using the canonical " +
			"toolkit pattern, runs go build, registers the binary. NEVER emits a go.mod — tools reuse the " +
			"monorepo's root go.mod. Use this instead of write+build_tool when the handler fits the typed " +
			"Args/Out shape (most cases). For schemas needing oneOf/format/$ref, write the file yourself " +
			"and call build_tool.",
		Parameters: tools.SchemaFor[NewToolArgs](),
		// Writes a source file and mutates the tool registry — durable
		// state change, so it's gated out of read-only spawn modes.
		Mutates: true,
	}
}

// Execute validates name (^[a-z][a-z0-9_]{1,63}$), description, and body,
// renders the fixed toolkit main.go template, and writes it to
// tools/<name>/main.go — refusing to overwrite an existing file unless
// replace=true — then hands off to BuildTool.Execute under the same call ID.
func (t *NewToolTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	var args NewToolArgs
	if derr := tools.DecodeArgs(call.Arguments, &args); derr != nil {
		return failureResult(call.ID, derr.Error()), nil
	}
	name := strings.TrimSpace(args.Name)
	description := strings.TrimSpace(args.Description)
	outType := strings.TrimSpace(args.OutType)

	if name == "" {
		return failureResult(call.ID, "new_tool: name required"), nil
	}
	if !validToolName.MatchString(name) {
		return failureResult(
			call.ID,
			fmt.Sprintf("new_tool: invalid name %q (must match ^[a-z][a-z0-9_]{1,63}$)", name),
		), nil
	}
	if description == "" {
		return failureResult(call.ID, "new_tool: description required"), nil
	}
	if strings.TrimSpace(args.Body) == "" {
		return failureResult(call.ID, "new_tool: body required (handler body returning (out, error))"), nil
	}
	if outType == "" {
		const goTypeString = "string"

		// ... (in Execute) ...
		outType = goTypeString
	}

	src, err := renderToolMain(toolMainData{
		Name:         name,
		Description:  description,
		ArgsFields:   strings.TrimSpace(args.ArgsFields),
		OutType:      outType,
		Body:         strings.TrimSpace(args.Body),
		ExtraImports: parseImports(args.Imports),
	})
	if err != nil {
		return failureResult(call.ID, fmt.Sprintf("new_tool: render: %v", err)), nil
	}

	dir := filepath.Join(t.wsRoot, "tools", name)
	if err := os.MkdirAll(dir, filesystem.ModePublicDir); err != nil {
		return failureResult(call.ID, fmt.Sprintf("new_tool: mkdir %s: %v", dir, err)), nil
	}
	mainPath := filepath.Join(dir, "main.go")
	// Refuse silent overwrite. The agent (or a user with the same
	// tool name) may have hand-edited the file; clobbering it loses
	// work. Caller must pass replace=true to confirm intent.
	if !args.Replace {
		if _, err := os.Stat(mainPath); err == nil {
			return failureResult(call.ID, fmt.Sprintf(
				"new_tool: %s already exists; pass replace=true to overwrite "+
					"(or read+edit+build_tool to keep manual changes)", mainPath)), nil
		}
	}
	if err := os.WriteFile(mainPath, []byte(src), filesystem.ModePublicFile); err != nil {
		return failureResult(call.ID, fmt.Sprintf("new_tool: write %s: %v", mainPath, err)), nil
	}

	// Hand off to build_tool. Same scrub-go.mod / build / register
	// path; we reuse the same call ID so the response threads
	// correctly.
	return t.builder.Execute(ctx, tools.ToolCall{
		ID: call.ID,
		Arguments: tools.ToolParameters{
			"directory": filepath.Join("tools", name),
		},
	})
}

// parseImports normalises whitespace and split-by-line. Drops blank
// lines so the LLM can use them for readability.
func parseImports(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		s := strings.TrimSpace(line)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}

// toolMainData is the template payload.
type toolMainData struct {
	Name         string
	Description  string
	ArgsFields   string
	OutType      string
	Body         string
	ExtraImports []string
}

// indent prefixes every line of s with n tabs. Used by the template
// so the agent's body lands at the correct nesting level.
func indent(n int, s string) string {
	if s == "" {
		return ""
	}
	prefix := strings.Repeat("\t", n)
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

// renderToolMain renders main.go. The template fixes the package
// declaration, the canonical imports, the toolkit.Run call shape,
// and the Func signature — leaving only the agent-supplied pieces
// to vary.
const toolMainTemplate = `package main

import (
	"context"
{{- range .ExtraImports }}
	{{ . }}
{{- end }}

	"github.com/zarldev/zarlmono/zkit/ai/tools/toolkit"
)

type Args struct {
{{ indent 1 .ArgsFields }}
}

func main() {
	toolkit.Run(toolkit.Tool[Args, {{ .OutType }}]{
		Name:        "{{ .Name }}",
		Description: ` + "`" + `{{ .Description }}` + "`" + `,
		Func: func(ctx context.Context, args Args) ({{ .OutType }}, error) {
{{ indent 3 .Body }}
		},
	})
}
`

func renderToolMain(d toolMainData) (string, error) {
	tmpl := template.Must(template.New("toolmain").Funcs(template.FuncMap{
		"indent": indent,
	}).Parse(toolMainTemplate))
	var buf strings.Builder
	if err := tmpl.Execute(&buf, d); err != nil {
		return "", err
	}
	return buf.String(), nil
}
