package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/dynamic"
)

const toolName tools.ToolName = "echo_upper"

func main() { os.Exit(runMain()) }

func runMain() int {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func run(ctx context.Context) error {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}
	workspace, err := os.MkdirTemp("", "dynamic-tools-example-")
	if err != nil {
		return fmt.Errorf("create temp workspace: %w", err)
	}
	defer os.RemoveAll(workspace)

	if err := scaffoldModule(workspace, repoRoot); err != nil {
		return err
	}

	catalogPath := filepath.Join(workspace, "catalog.json")
	registry := tools.NewRegistry()
	catalog := dynamic.NewCatalog(dynamic.NewFileStore(catalogPath))
	registrar := dynamic.NewRegistrar(catalog, registry, dynamic.WithBinaryRoot(workspace))
	builder := dynamic.NewBuildTool(registrar, workspace)
	author := dynamic.NewNewToolTool(builder, workspace)

	result, err := author.Execute(ctx, tools.ToolCall{
		ID:       "author",
		ToolName: dynamic.ToolNameNewTool,
		Arguments: tools.ToolParameters{
			"name":        string(toolName),
			"description": "Return input text in uppercase.",
			"args_fields": "Text string `json:\"text\" doc:\"text to uppercase\"`",
			"body":        "return strings.ToUpper(args.Text), nil",
			"imports":     `"strings"`,
		},
	})
	if err != nil {
		return fmt.Errorf("author tool: %w", err)
	}
	if !result.Success {
		return fmt.Errorf("author tool: %s", result.Error)
	}
	fmt.Printf("authored_and_registered=%s\n", toolName)

	value, err := invoke(ctx, registry, "first call")
	if err != nil {
		return err
	}
	fmt.Printf("invoked=%s\n", value)

	// A fresh catalog and registry model a process restart. LoadContext reads the
	// persisted catalog and Sync recreates each runtime BinaryTool registration.
	reloadedRegistry := tools.NewRegistry()
	reloadedCatalog := dynamic.NewCatalog(dynamic.NewFileStore(catalogPath))
	if err := reloadedCatalog.LoadContext(ctx); err != nil {
		return err
	}
	reloadedRegistrar := dynamic.NewRegistrar(reloadedCatalog, reloadedRegistry, dynamic.WithBinaryRoot(workspace))
	shadowed, err := reloadedRegistrar.Sync()
	if err != nil {
		return fmt.Errorf("sync reloaded catalog: %w", err)
	}
	if len(shadowed) != 0 {
		return fmt.Errorf("sync unexpectedly shadowed: %v", shadowed)
	}
	value, err = invoke(ctx, reloadedRegistry, "after reload")
	if err != nil {
		return err
	}
	fmt.Printf("reloaded_and_invoked=%s\n", value)

	// Dynamic registration must not shadow a tool owned by another provider.
	const reserved tools.ToolName = "reserved_name"
	if err := reloadedRegistry.Register(staticTool{name: reserved}); err != nil {
		return fmt.Errorf("register fixture built-in: %w", err)
	}
	entry, ok := reloadedCatalog.Get(toolName)
	if !ok {
		return errors.New("reloaded catalog lost authored tool")
	}
	collisionSpec := entry.Spec
	collisionSpec.Name = reserved
	if err := reloadedRegistrar.Register(collisionSpec, entry.BinaryPath); !errors.Is(err, dynamic.ErrNameExists) {
		return fmt.Errorf("collision error: %w; want ErrNameExists", err)
	}
	fmt.Println("collision=rejected")

	unregister := dynamic.NewUnregisterTool(reloadedRegistrar)
	result, err = unregister.Execute(ctx, tools.ToolCall{
		ID:        "unregister",
		ToolName:  dynamic.ToolNameUnregisterTool,
		Arguments: tools.ToolParameters{"name": string(toolName)},
	})
	if err != nil {
		return fmt.Errorf("unregister tool: %w", err)
	}
	if !result.Success {
		return fmt.Errorf("unregister tool: %s", result.Error)
	}
	if _, ok := reloadedRegistry.Tool(toolName); ok {
		return errors.New("tool remains registered")
	}
	fmt.Printf("unregistered=%s\n", toolName)
	fmt.Printf("catalog_entries=%d\n", len(reloadedCatalog.Entries()))
	return nil
}

func invoke(ctx context.Context, registry *tools.Registry, text string) (string, error) {
	tool, ok := registry.Tool(toolName)
	if !ok {
		return "", fmt.Errorf("tool %q is not registered", toolName)
	}
	result, err := tool.Execute(ctx, tools.ToolCall{
		ID:        "invoke",
		ToolName:  toolName,
		Arguments: tools.ToolParameters{"text": text},
	})
	if err != nil {
		return "", fmt.Errorf("invoke %s: %w", toolName, err)
	}
	if !result.Success {
		return "", fmt.Errorf("invoke %s: %s", toolName, result.Error)
	}
	value, ok := result.Data.(string)
	if !ok {
		return "", fmt.Errorf("invoke %s returned %T", toolName, result.Data)
	}
	return value, nil
}

type staticTool struct {
	name tools.ToolName
}

func (t staticTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        t.name,
		Description: "A built-in fixture used to demonstrate collision rejection.",
		Parameters:  tools.SchemaFor[struct{}](),
	}
}

func (staticTool) Execute(context.Context, tools.ToolCall) (*tools.ToolResult, error) {
	return nil, errors.New("fixture tool is not invoked")
}

func scaffoldModule(workspace, repoRoot string) error {
	goMod, err := os.ReadFile(filepath.Join(repoRoot, "examples", "go.mod"))
	if err != nil {
		return fmt.Errorf("read examples go.mod: %w", err)
	}
	module := strings.Replace(string(goMod), "module github.com/zarldev/zarlmono/examples", "module example.local/dynamictools", 1)
	module = strings.Replace(module, "replace github.com/zarldev/zarlmono/zarlcode => ../zarlcode", fmt.Sprintf("replace github.com/zarldev/zarlmono/zarlcode => %s", filepath.ToSlash(filepath.Join(repoRoot, "zarlcode"))), 1)
	module += fmt.Sprintf("\nreplace github.com/zarldev/zarlmono/zkit => %s\n", filepath.ToSlash(filepath.Join(repoRoot, "zkit")))
	if err := os.WriteFile(filepath.Join(workspace, "go.mod"), []byte(module), 0o644); err != nil { //nolint:gosec // workspace is process-owned.
		return fmt.Errorf("write temp go.mod: %w", err)
	}
	goSum, err := os.ReadFile(filepath.Join(repoRoot, "examples", "go.sum"))
	if err != nil {
		return fmt.Errorf("read examples go.sum: %w", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "go.sum"), goSum, 0o644); err != nil { //nolint:gosec // workspace is process-owned.
		return fmt.Errorf("write temp go.sum: %w", err)
	}
	return nil
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("working directory: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "zkit", "go.mod")); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("repository root not found above %s", strings.TrimSpace(dir))
		}
		dir = parent
	}
}
