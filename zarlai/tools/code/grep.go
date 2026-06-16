package code

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/zarldev/zarlmono/zarlai/service"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

const grepDefaultMaxResults = 100

// GrepTool wraps ripgrep and returns structured matches.
type GrepTool struct{ ws Workspace }

func NewGrepTool(ws Workspace) *GrepTool { return &GrepTool{ws: ws} }

func (t *GrepTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "grep",
		Description: "Search workspace contents with ripgrep. Returns JSON [{file, line, text}]. Honors .gitignore by default.",
		Parameters: service.Parameters{
			{Name: "pattern", Type: service.ParamString, Description: "Regular expression.", Required: true},
			{Name: "path", Type: service.ParamString, Description: "Subpath inside workspace (default = workspace root).", Required: false},
			{Name: "glob", Type: service.ParamString, Description: "Glob filter (e.g. *.go).", Required: false},
			{Name: "case_insensitive", Type: service.ParamBool, Description: "Case-insensitive match.", Required: false},
			{Name: "max_results", Type: service.ParamInteger, Description: "Cap on returned matches (default 100).", Required: false},
		}.ToJSONSchema(),
	}
}

type grepHit struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

func (t *GrepTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	if _, err := exec.LookPath("rg"); err != nil {
		return tools.Failure(call.ID, tools.Transient("grep", fmt.Errorf("grep: ripgrep (rg) not installed: %w", err))), nil
	}
	pattern := call.Arguments.String("pattern", "")
	if pattern == "" {
		return tools.Failure(call.ID, tools.Validation("grep", "grep: pattern required")), nil
	}
	target := t.ws.Root()
	if sub := call.Arguments.String("path", ""); sub != "" {
		abs, err := t.ws.Resolve(sub)
		if err != nil {
			return tools.Failure(call.ID, tools.Validation("grep", err.Error())), nil
		}
		target = abs
	}
	max := call.Arguments.Int("max_results", grepDefaultMaxResults)
	if max <= 0 {
		max = grepDefaultMaxResults
	}

	rgArgs := []string{"--json", "--no-messages"}
	if call.Arguments.Bool("case_insensitive", false) {
		rgArgs = append(rgArgs, "-i")
	}
	if g := call.Arguments.String("glob", ""); g != "" {
		rgArgs = append(rgArgs, "--glob", g)
	}
	rgArgs = append(rgArgs, "--", pattern, target)

	cmd := exec.CommandContext(ctx, "rg", rgArgs...)
	out, _ := cmd.Output() // rg exits 1 when no matches; ignore the error

	var hits []grepHit
	for line := range bytes.SplitSeq(out, []byte("\n")) {
		if len(line) == 0 || len(hits) >= max {
			continue
		}
		var ev struct {
			Type string `json:"type"`
			Data struct {
				Path       struct{ Text string } `json:"path"`
				LineNumber int                   `json:"line_number"`
				Lines      struct{ Text string } `json:"lines"`
			} `json:"data"`
		}
		if err := json.Unmarshal(line, &ev); err != nil || ev.Type != "match" {
			continue
		}
		hits = append(hits, grepHit{
			File: strings.TrimPrefix(ev.Data.Path.Text, t.ws.Root()+"/"),
			Line: ev.Data.LineNumber,
			Text: strings.TrimRight(ev.Data.Lines.Text, "\n"),
		})
	}

	body, err := json.Marshal(hits)
	if err != nil {
		return tools.Failure(call.ID, tools.Transient("grep", fmt.Errorf("grep marshal: %w", err))), nil
	}
	return tools.Success(call.ID, string(body)), nil
}
