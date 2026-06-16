package taskrunner

import (
	"context"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/profile"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

func TestZkitPromptSourceSystem(t *testing.T) {
	mem := func(_ context.Context, person string) string {
		if person == "" {
			return ""
		}
		return "memory:" + person
	}
	skills := func(_ context.Context, profile, query string) string {
		if profile == "" {
			return ""
		}
		return "skills:" + profile + ":" + query
	}

	tests := []struct {
		name string
		src  zkitPromptSource
		vars runner.PromptVars
		want string
	}{
		{
			name: "all blocks present, joined by blank lines",
			src:  zkitPromptSource{base: func() string { return "BASE" }, memory: mem, skills: skills},
			vars: runner.PromptVars{
				promptVarPrefix:  "PREFIX",
				promptVarPerson:  "Alice",
				promptVarProfile: "researcher",
				promptVarQuery:   "find prices",
			},
			want: "BASE\n\nPREFIX\n\nmemory:Alice\n\nskills:researcher:find prices",
		},
		{
			name: "empty prefix/person/profile skipped",
			src:  zkitPromptSource{base: func() string { return "BASE" }, memory: mem, skills: skills},
			vars: runner.PromptVars{},
			want: "BASE",
		},
		{
			name: "nil loaders skipped, prefix only",
			src:  zkitPromptSource{base: func() string { return "" }},
			vars: runner.PromptVars{promptVarPrefix: "PREFIX"},
			want: "PREFIX",
		},
		{
			name: "nil base func tolerated",
			src:  zkitPromptSource{memory: mem},
			vars: runner.PromptVars{promptVarPerson: "Bob"},
			want: "memory:Bob",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.src.System(t.Context(), tt.vars)
			if err != nil {
				t.Fatalf("System: unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("System mismatch:\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}

func TestPromptVarsFor(t *testing.T) {
	resolved := ResolvedProfile{Resolved: profile.Resolved{Name: profile.NameResearcher, PromptPrefix: "be terse"}}
	vars := promptVarsFor(taskPromptInput{PersonName: "Alice", Prompt: "research GPUs"}, resolved)

	if got := vars.String(promptVarPrefix); got != "be terse" {
		t.Errorf("prefix = %q, want %q", got, "be terse")
	}
	if got := vars.String(promptVarPerson); got != "Alice" {
		t.Errorf("person = %q, want %q", got, "Alice")
	}
	if got := vars.String(promptVarProfile); got != "researcher" {
		t.Errorf("profile = %q, want %q", got, "researcher")
	}
	if got := vars.String(promptVarQuery); got != "research GPUs" {
		t.Errorf("query = %q, want %q", got, "research GPUs")
	}
}

// newPromptSource on a minimal runner must read the live system prompt
// through the closure (hot-reload), and degrade memory/skills to "" when
// qdrant/embedder/selector are unconfigured — matching buildPrompts.
func TestNewPromptSourceLiveBase(t *testing.T) {
	r := NewRunner(Config{}, WithSystemPrompt("v1"))
	src := r.newPromptSource()

	got, err := src.System(t.Context(), runner.PromptVars{})
	if err != nil {
		t.Fatalf("System: %v", err)
	}
	if got != "v1" {
		t.Fatalf("System = %q, want %q", got, "v1")
	}

	r.Reconfigure(WithSystemPrompt("v2"))
	got, err = src.System(t.Context(), runner.PromptVars{})
	if err != nil {
		t.Fatalf("System after reconfigure: %v", err)
	}
	if got != "v2" {
		t.Fatalf("System after reconfigure = %q, want live %q", got, "v2")
	}
}
