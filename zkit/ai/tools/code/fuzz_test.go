package code_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func FuzzApplyPatchSafety(f *testing.F) {
	for _, seed := range []string{
		"",
		"*** Begin Patch\n*** End Patch\n",
		"*** Begin Patch\n*** Add File: added.txt\n+hello\n*** End Patch\n",
		"*** Begin Patch\n*** Update File: seed.txt\n@@\n-seed\n+changed\n*** End Patch\n",
		"*** Begin Patch\n*** Add File: ../outside.txt\n+escaped\n*** End Patch\n",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, patch string) {
		if len(patch) > 32<<10 {
			t.Skip()
		}

		if got, again := code.PatchPaths(patch), code.PatchPaths(patch); !reflect.DeepEqual(got, again) {
			t.Fatalf("PatchPaths is not deterministic: first=%q second=%q", got, again)
		}
		if got, again := code.PatchExistingPaths(patch), code.PatchExistingPaths(patch); !reflect.DeepEqual(got, again) {
			t.Fatalf("PatchExistingPaths is not deterministic: first=%q second=%q", got, again)
		}

		base := t.TempDir()
		root := filepath.Join(base, "workspace")
		if err := os.Mkdir(root, 0o755); err != nil {
			t.Fatalf("create workspace: %v", err)
		}
		seedPath := filepath.Join(root, "seed.txt")
		if err := os.WriteFile(seedPath, []byte("seed\n"), 0o644); err != nil {
			t.Fatalf("seed workspace: %v", err)
		}
		outsidePath := filepath.Join(base, "outside.txt")
		if err := os.WriteFile(outsidePath, []byte("outside\n"), 0o644); err != nil {
			t.Fatalf("seed outside sentinel: %v", err)
		}
		workspace, err := code.NewWorkspace(root)
		if err != nil {
			t.Fatalf("NewWorkspace: %v", err)
		}
		result, err := code.NewApplyPatchTool(workspace).Execute(t.Context(), tools.ToolCall{
			ID:        "fuzz",
			Arguments: tools.ToolParameters{"patch": patch},
		})
		if err != nil {
			t.Fatalf("Execute returned transport error: %v", err)
		}

		outside, err := os.ReadFile(outsidePath)
		if err != nil {
			t.Fatalf("read outside sentinel: %v", err)
		}
		if string(outside) != "outside\n" {
			t.Fatalf("patch mutated outside workspace: %q", outside)
		}
		if result.Success {
			return
		}
		seed, err := os.ReadFile(seedPath)
		if err != nil {
			t.Fatalf("failed patch removed seed file: %v", err)
		}
		if string(seed) != "seed\n" {
			t.Fatalf("failed patch partially mutated seed file: %q", seed)
		}
	})
}

func FuzzHashlineEditSafety(f *testing.F) {
	f.Add("seed.txt", 1, "bad", "replace", "replacement")
	f.Add("../outside.txt", 1, "bad", "replace", "escaped")
	f.Add("seed.txt", -1, "", "delete", "")
	f.Add("seed.txt", 1, "bad", "unknown", "x")

	f.Fuzz(func(t *testing.T, path string, startLine int, startHash, mode, replacement string) {
		if len(path)+len(startHash)+len(mode)+len(replacement) > 32<<10 {
			t.Skip()
		}

		base := t.TempDir()
		root := filepath.Join(base, "workspace")
		if err := os.Mkdir(root, 0o755); err != nil {
			t.Fatalf("create workspace: %v", err)
		}
		seedPath := filepath.Join(root, "seed.txt")
		if err := os.WriteFile(seedPath, []byte("seed\nsecond\n"), 0o644); err != nil {
			t.Fatalf("seed workspace: %v", err)
		}
		outsidePath := filepath.Join(base, "outside.txt")
		if err := os.WriteFile(outsidePath, []byte("outside\n"), 0o644); err != nil {
			t.Fatalf("seed outside sentinel: %v", err)
		}
		workspace, err := code.NewWorkspace(root)
		if err != nil {
			t.Fatalf("NewWorkspace: %v", err)
		}
		result, err := code.NewEditFileHLTool(workspace).Execute(t.Context(), tools.ToolCall{
			ID: "fuzz",
			Arguments: tools.ToolParameters{
				"path": path,
				"edits": []any{map[string]any{
					"start_line": startLine,
					"start_hash": startHash,
					"mode":       mode,
					"new_string": replacement,
				}},
			},
		})
		if err != nil {
			t.Fatalf("Execute returned transport error: %v", err)
		}

		outside, err := os.ReadFile(outsidePath)
		if err != nil {
			t.Fatalf("read outside sentinel: %v", err)
		}
		if string(outside) != "outside\n" {
			t.Fatalf("edit mutated outside workspace: %q", outside)
		}
		if result.Success {
			return
		}
		seed, err := os.ReadFile(seedPath)
		if err != nil {
			t.Fatalf("failed edit removed seed file: %v", err)
		}
		if string(seed) != "seed\nsecond\n" {
			t.Fatalf("failed edit partially mutated seed file: %q", seed)
		}
	})
}
