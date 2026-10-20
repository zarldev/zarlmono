package engine_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/catalog"
	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/instructions"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestDiscoveryCancellationPreservesCatalog(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	dir := filepath.Join(root, ".zarlcode", "agents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "agent.md")
	if err := os.WriteFile(path, []byte("---\nname: original\ndescription: original agent\n---\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := engine.NewRuntimeCatalog(root)
	before, beforeErrs := c.Agents(), c.Errors()
	if len(before) != 1 {
		t.Fatalf("agents = %v; errors = %v", before, beforeErrs)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancelCause(t.Context())
	cause := errors.New("cancel discovery")
	cancel(cause)
	if errs := c.ReloadContext(ctx, t.TempDir()); !errors.Is(errors.Join(errs...), cause) {
		t.Fatalf("reload = %v", errs)
	}
	if !reflect.DeepEqual(c.Agents(), before) || !reflect.DeepEqual(c.Errors(), beforeErrs) {
		t.Fatal("cancelled reload replaced snapshot")
	}
	for _, tool := range []tools.Tool{engine.NewListAgentsTool(c), engine.NewListSkillsTool(c), engine.NewLoadSkillTool(c), engine.NewLoadInstructionTool(root, func() []instructions.NestedDoc { return nil })} {
		_, err := tool.Execute(ctx, tools.ToolCall{ID: "cancelled", Arguments: tools.ToolParameters{"name": "missing", "path": "AGENTS.md"}})
		if !errors.Is(err, cause) {
			t.Errorf("%s cancellation = %v", tool.Definition().Name, err)
		}
	}
	// The cancelled root replacement must not change ReloadCurrent's target.
	if err := os.WriteFile(path, []byte("---\nname: replacement\ndescription: replacement agent\n---\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.ReloadCurrent()
	if _, ok := c.Agent("replacement"); !ok {
		t.Fatal("cancelled refresh changed current workspace")
	}
}

func TestDiscoveryContextAPIsPreserveCause(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	cause := errors.New("stop discovery")
	cancel(cause)
	root := t.TempDir()
	checks := []struct {
		name string
		run  func() []error
	}{
		{"agents", func() []error { _, errs := catalog.LoadAgentsContext(ctx, root); return errs }},
		{"skills", func() []error { _, errs := catalog.LoadSkillsContext(ctx, root); return errs }},
		{"hooks", func() []error { _, errs := catalog.LoadHooksContext(ctx, root); return errs }},
		{"discover", func() []error { _, errs := instructions.DiscoverContext(ctx, root, 0); return errs }},
		{"root", func() []error { _, errs := instructions.DiscoverRootContext(ctx, root, 0); return errs }},
		{"nested", func() []error { _, errs := instructions.ListNestedContext(ctx, root); return errs }},
		{"load", func() []error { _, err := instructions.LoadOneContext(ctx, root, "AGENTS.md"); return []error{err} }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if errs := check.run(); !errors.Is(errors.Join(errs...), cause) {
				t.Fatalf("errors = %v, want cancellation cause", errs)
			}
		})
	}
}
