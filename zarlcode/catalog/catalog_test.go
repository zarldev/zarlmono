package catalog_test

import (
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/zarldev/zarlmono/zarlcode/catalog"
)

func TestMain(m *testing.M) {
	usr, err := user.Current()
	if err != nil || usr.HomeDir == "" {
		os.Exit(m.Run())
	}
	oldHome, hadHome := os.LookupEnv("HOME")
	tmp, err := os.MkdirTemp("", "zarlcode-catalog-home-")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("HOME", tmp)
	code := m.Run()
	if hadHome {
		_ = os.Setenv("HOME", oldHome)
	} else {
		_ = os.Unsetenv("HOME")
	}
	_ = os.RemoveAll(tmp)
	os.Exit(code)
}

func TestLoadAgentsParsesMode(t *testing.T) {
	root := t.TempDir()
	writeAgent(t, filepath.Join(root, ".zarlcode", "agents", "explorer.md"), `---
name: explorer
description: maps code
mode: explore
---

Explore only.
`)
	writeAgent(t, filepath.Join(root, ".zarlcode", "agents", "plain.md"), `---
name: plain
description: no mode
---

Plain.
`)

	agents, errs := LoadAgents(root)
	if len(errs) != 0 {
		t.Fatalf("LoadAgents errors: %v", errs)
	}
	byName := map[string]Agent{}
	for _, agent := range agents {
		byName[agent.Name] = agent
	}
	if got := byName["explorer"].Mode; got != "explore" {
		t.Errorf("explorer mode = %q, want explore", got)
	}
	if got := byName["plain"].Mode; got != "" {
		t.Errorf("plain mode = %q, want empty", got)
	}
}

func TestLoadAgentsRejectsInvalidMode(t *testing.T) {
	root := t.TempDir()
	writeAgent(t, filepath.Join(root, ".zarlcode", "agents", "bad.md"), `---
name: bad
description: invalid mode
mode: mutate
---

Bad.
`)

	agents, errs := LoadAgents(root)
	if len(agents) != 0 {
		t.Fatalf("LoadAgents returned %d agents, want none", len(agents))
	}
	if len(errs) != 1 {
		t.Fatalf("LoadAgents returned %d errors, want 1: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Error(), "mode") || !strings.Contains(errs[0].Error(), "explore") {
		t.Fatalf("error = %v, want mode validation detail", errs[0])
	}
}

func TestLoadAgentsWorkspaceOverridePreservesPositionAndMode(t *testing.T) {
	root := t.TempDir()
	writeAgent(t, filepath.Join(root, "zarlcode", "agents", "shared.md"), `---
name: shared
description: source tree version
mode: verify
---

Source.
`)
	writeAgent(t, filepath.Join(root, ".zarlcode", "agents", "shared.md"), `---
name: shared
description: workspace version
mode: implement
---

Workspace.
`)

	agents, errs := LoadAgents(root)
	if len(errs) != 0 {
		t.Fatalf("LoadAgents errors: %v", errs)
	}
	if len(agents) != 1 {
		t.Fatalf("LoadAgents returned %d agents, want 1", len(agents))
	}
	if agents[0].Description != "workspace version" {
		t.Errorf("description = %q, want workspace version", agents[0].Description)
	}
	if agents[0].Mode != "implement" {
		t.Errorf("mode = %q, want implement", agents[0].Mode)
	}
}

func writeAgent(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}
