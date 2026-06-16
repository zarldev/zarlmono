package dynamic

import (
	"strings"
	"testing"
)

func TestRenderToolMain_Canonical(t *testing.T) {
	t.Parallel()
	src, err := renderToolMain(toolMainData{
		Name:         "shout",
		Description:  "Uppercase the input.",
		ArgsFields:   "Text string `json:\"text\" doc:\"the input string\"`",
		OutType:      "string",
		Body:         "return strings.ToUpper(args.Text), nil",
		ExtraImports: []string{`"strings"`},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	mustContain := []string{
		"package main",
		`"strings"`,
		`"github.com/zarldev/zarlmono/zkit/ai/tools/toolkit"`,
		"type Args struct {",
		"Text string `json:\"text\" doc:\"the input string\"`",
		"toolkit.Run(toolkit.Tool[Args, string]{",
		`Name:        "shout"`,
		"Description: `Uppercase the input.`",
		"return strings.ToUpper(args.Text), nil",
	}
	for _, want := range mustContain {
		if !strings.Contains(src, want) {
			t.Errorf("rendered source missing %q\n--- got ---\n%s", want, src)
		}
	}
	if strings.Contains(src, "go.mod") {
		t.Error("render must not mention go.mod")
	}
}

func TestRenderToolMain_NoArgs(t *testing.T) {
	t.Parallel()
	src, err := renderToolMain(toolMainData{
		Name:        "ping",
		Description: "Returns pong.",
		OutType:     "string",
		Body:        `return "pong", nil`,
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(src, "type Args struct {") {
		t.Error("missing Args struct shell")
	}
	if !strings.Contains(src, `return "pong", nil`) {
		t.Error("missing body")
	}
}

func TestParseImports(t *testing.T) {
	t.Parallel()
	got := parseImports("\"strings\"\n\n\"time\"\n")
	if len(got) != 2 || got[0] != `"strings"` || got[1] != `"time"` {
		t.Errorf("parseImports = %v", got)
	}
}
