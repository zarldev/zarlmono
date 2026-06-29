package dynamic_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/dynamic"
)

// buildEcho compiles a tiny CLI that satisfies the dynamic-tool
// contract: --describe prints a ToolSpec, --call reads JSON args and
// echoes back {"data": args} or {"error": "..."} when args["fail"]
// is set. Returns the binary path; cleaned up by the t.TempDir
// containing it.
func buildEcho(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	src := `package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

type spec struct {
	Name        string                 ` + "`json:\"name\"`" + `
	Description string                 ` + "`json:\"description\"`" + `
	Parameters  map[string]interface{} ` + "`json:\"parameters\"`" + `
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "missing action")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "--describe":
		s := spec{
			Name:        "echo_back",
			Description: "Echoes args.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"msg": map[string]interface{}{"type": "string"},
				},
				"required": []string{"msg"},
			},
		}
		_ = json.NewEncoder(os.Stdout).Encode(s)
	case "--call":
		body, _ := io.ReadAll(os.Stdin)
		var args map[string]interface{}
		_ = json.Unmarshal(body, &args)
		if _, fail := args["fail"]; fail {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]interface{}{"error": "asked to fail"})
			return
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]interface{}{"data": args})
	default:
		fmt.Fprintln(os.Stderr, "unknown action: ", os.Args[1])
		os.Exit(2)
	}
}
`
	srcPath := filepath.Join(dir, "main.go")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(dir, "echo_back")
	cmd := exec.Command("go", "build", "-o", binPath, srcPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build echo binary: %v\n%s", err, out)
	}
	return binPath
}

func TestBinaryTool_RoundTripSuccess(t *testing.T) {
	t.Parallel()
	binPath := buildEcho(t)

	spec, err := dynamic.DescribeBinary(t.Context(), binPath, 0)
	if err != nil {
		t.Fatalf("DescribeBinary: %v", err)
	}
	if spec.Name != "echo_back" {
		t.Fatalf("spec.Name = %q, want echo_back", spec.Name)
	}

	tool := dynamic.NewBinaryTool(spec, binPath)
	res, err := tool.Execute(t.Context(), tools.ToolCall{
		ID:        "x",
		ToolName:  spec.Name,
		Arguments: tools.ToolParameters{"msg": "hi"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, got error %q", res.Error)
	}
	data, _ := res.Data.(map[string]any)
	if data["msg"] != "hi" {
		t.Fatalf("data = %v, want msg=hi", data)
	}
}

func TestBinaryTool_BinaryReportedError(t *testing.T) {
	t.Parallel()
	binPath := buildEcho(t)

	tool := dynamic.NewBinaryTool(tools.ToolSpec{Name: "echo_back"}, binPath)
	res, _ := tool.Execute(t.Context(), tools.ToolCall{
		ID:        "x",
		ToolName:  "echo_back",
		Arguments: tools.ToolParameters{"fail": true},
	})
	if res.Success {
		t.Fatalf("expected failure, got success with data %v", res.Data)
	}
	if res.Error != "asked to fail" {
		t.Errorf("error = %q, want %q", res.Error, "asked to fail")
	}
}

func TestBinaryTool_MissingBinary(t *testing.T) {
	t.Parallel()
	tool := dynamic.NewBinaryTool(tools.ToolSpec{Name: "missing"}, "/does/not/exist")
	res, _ := tool.Execute(t.Context(), tools.ToolCall{
		ID:       "x",
		ToolName: "missing",
	})
	if res.Success {
		t.Fatal("expected failure for missing binary")
	}
}

func TestCatalog_AddRemovePersist(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "catalog.json")
	m := dynamic.NewCatalog(dynamic.NewFileStore(path))
	if err := m.Load(); err != nil {
		t.Fatalf("Load on missing: %v", err)
	}
	if len(m.Entries()) != 0 {
		t.Fatal("missing catalog should load as empty")
	}

	entry := dynamic.Entry{
		Spec:       tools.ToolSpec{Name: "foo", Description: "demo", Parameters: llm.Schema{Type: "object"}},
		BinaryPath: "/tmp/foo",
	}
	if err := m.Add(entry); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Re-read from disk.
	m2 := dynamic.NewCatalog(dynamic.NewFileStore(path))
	if err := m2.Load(); err != nil {
		t.Fatalf("Load after Add: %v", err)
	}
	got, ok := m2.Get("foo")
	if !ok {
		t.Fatal("entry not persisted")
	}
	if got.BinaryPath != "/tmp/foo" {
		t.Errorf("BinaryPath = %q, want /tmp/foo", got.BinaryPath)
	}

	removed, err := m2.Remove("foo")
	if err != nil || !removed {
		t.Fatalf("Remove = %v, %v", removed, err)
	}

	// Removing again is a no-op.
	removed, err = m2.Remove("foo")
	if err != nil {
		t.Fatalf("Remove idempotent: %v", err)
	}
	if removed {
		t.Error("second Remove should report false")
	}
}

func TestRegistrar_RegisterUnregisterFlow(t *testing.T) {
	t.Parallel()
	binPath := buildEcho(t)
	catalogPath := filepath.Join(t.TempDir(), "catalog.json")

	catalog := dynamic.NewCatalog(dynamic.NewFileStore(catalogPath))
	if err := catalog.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	registry := tools.NewRegistry()
	reg := dynamic.NewRegistrar(catalog, registry)

	spec, err := dynamic.DescribeBinary(t.Context(), binPath, 0)
	if err != nil {
		t.Fatalf("DescribeBinary: %v", err)
	}
	if err := reg.Register(spec, binPath); err != nil {
		t.Fatalf("Register: %v", err)
	}

	tool, ok := registry.Tool(spec.Name)
	if !ok {
		t.Fatal("registry did not know about registered tool")
	}
	res, _ := tool.Execute(t.Context(), tools.ToolCall{
		ID:        "x",
		ToolName:  spec.Name,
		Arguments: tools.ToolParameters{"msg": "round-trip"},
	})
	if !res.Success {
		t.Fatalf("registered tool failed: %s", res.Error)
	}

	if got := registry.ProviderFor(spec.Name); got != dynamic.ProviderName {
		t.Errorf("ProviderFor = %q, want %q", got, dynamic.ProviderName)
	}

	if err := reg.Unregister(spec.Name); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if _, ok := registry.Tool(spec.Name); ok {
		t.Error("tool still in registry after Unregister")
	}
	if _, ok := catalog.Get(spec.Name); ok {
		t.Error("entry still in catalog after Unregister")
	}
}

func TestRegistrar_RefusesShadowingBuiltin(t *testing.T) {
	t.Parallel()
	binPath := buildEcho(t)
	registry := tools.NewRegistry()
	registry.Register(stubTool{name: "builtin_thing"})

	catalogPath := filepath.Join(t.TempDir(), "catalog.json")
	catalog := dynamic.NewCatalog(dynamic.NewFileStore(catalogPath))
	reg := dynamic.NewRegistrar(catalog, registry)

	err := reg.Register(tools.ToolSpec{
		Name:        "builtin_thing",
		Description: "shadow attempt",
		Parameters:  llm.Schema{Type: "object"},
	}, binPath)
	if err == nil {
		t.Fatal("expected refusal to shadow built-in registration")
	}
}

// Regression: a stale catalog entry from an earlier shell version
// (before a same-named built-in shipped) used to silently overwrite
// the new built-in via Sync, leaving the user with whatever binary
// they'd authored years ago. Sync must skip shadowing entries and
// report them.
func TestRegistrar_SyncSkipsShadowingEntries(t *testing.T) {
	t.Parallel()
	binPath := buildEcho(t)

	// First shell: dynamic tool registered.
	catalogPath := filepath.Join(t.TempDir(), "catalog.json")
	{
		m := dynamic.NewCatalog(dynamic.NewFileStore(catalogPath))
		if err := m.Load(); err != nil {
			t.Fatalf("load: %v", err)
		}
		r := dynamic.NewRegistrar(m, tools.NewRegistry())
		if err := r.Register(tools.ToolSpec{
			Name:        "web_search",
			Description: "user's old dynamic tool",
			Parameters:  llm.Schema{Type: "object"},
		}, binPath); err != nil {
			t.Fatalf("register: %v", err)
		}
	}

	// Second shell: a new version of the binary ships a built-in
	// web_search BEFORE Sync runs. Sync must not clobber it.
	registry := tools.NewRegistry()
	builtin := stubTool{name: "web_search"}
	registry.Register(builtin)

	catalog := dynamic.NewCatalog(dynamic.NewFileStore(catalogPath))
	if err := catalog.Load(); err != nil {
		t.Fatalf("reload catalog: %v", err)
	}
	reg := dynamic.NewRegistrar(catalog, registry)
	shadowed, err := reg.Sync()
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(shadowed) != 1 || shadowed[0] != "web_search" {
		t.Fatalf("shadowed = %v; want [web_search]", shadowed)
	}
	// Built-in must still be the registered tool — catalog binary
	// must NOT have replaced it.
	got, ok := registry.Tool("web_search")
	if !ok {
		t.Fatal("built-in disappeared after Sync")
	}
	if got.Definition().Name != builtin.Definition().Name {
		t.Fatalf("registry tool kind = %T; built-in was clobbered", got)
	}
}

func TestRegistrar_SyncFromManifest(t *testing.T) {
	t.Parallel()
	binPath := buildEcho(t)
	catalogPath := filepath.Join(t.TempDir(), "catalog.json")
	catalog := dynamic.NewCatalog(dynamic.NewFileStore(catalogPath))
	if err := catalog.Add(dynamic.Entry{
		Spec:       tools.ToolSpec{Name: "echo_back", Description: "x", Parameters: llm.Schema{Type: "object"}},
		BinaryPath: binPath,
	}); err != nil {
		t.Fatal(err)
	}

	// Fresh registry, restore from catalog.
	registry := tools.NewRegistry()
	reg := dynamic.NewRegistrar(catalog, registry)
	if _, err := reg.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if got := registry.ToolCountForProvider(dynamic.ProviderName); got != 1 {
		t.Fatalf("dynamic provider count = %d, want 1", got)
	}
	if _, ok := registry.Tool("echo_back"); !ok {
		t.Fatal("echo_back not registered after Sync")
	}
}

// RegisterTool was dropped — new_tool is the canonical (and only)
// authoring path now, and Registrar.Register is exercised by the
// new_tool / build_tool integration tests below. The historical
// TestRegisterTool_Validates / TestRegisterTool_BinaryRootEnforced
// covered an entry point that no longer exists.

func TestUnregisterTool(t *testing.T) {
	t.Parallel()
	binPath := buildEcho(t)
	registry := tools.NewRegistry()
	catalog := dynamic.NewCatalog(dynamic.NewFileStore(filepath.Join(t.TempDir(), "catalog.json")))
	reg := dynamic.NewRegistrar(catalog, registry)
	if err := reg.Register(tools.ToolSpec{
		Name:        "echo_back",
		Description: "x",
		Parameters:  llm.Schema{Type: "object"},
	}, binPath); err != nil {
		t.Fatal(err)
	}

	un := dynamic.NewUnregisterTool(reg)
	res, _ := un.Execute(t.Context(), tools.ToolCall{
		ID:        "x",
		ToolName:  dynamic.ToolNameUnregisterTool,
		Arguments: tools.ToolParameters{"name": "echo_back"},
	})
	if !res.Success {
		t.Fatalf("unregister failed: %s", res.Error)
	}
	if _, ok := registry.Tool("echo_back"); ok {
		t.Error("tool still registered after unregister_tool")
	}
}

func TestRegistrar_BinaryRootEnforced(t *testing.T) {
	t.Parallel()
	binPath := buildEcho(t)
	binDir := filepath.Dir(binPath)

	catalog := dynamic.NewCatalog(dynamic.NewFileStore(filepath.Join(t.TempDir(), "catalog.json")))
	registry := tools.NewRegistry()
	reg := dynamic.NewRegistrar(catalog, registry,
		dynamic.WithBinaryRoot(binDir))

	// Inside the root: allowed.
	if err := reg.Register(tools.ToolSpec{
		Name:        "echo_back",
		Description: "x",
		Parameters:  llm.Schema{Type: "object"},
	}, binPath); err != nil {
		t.Fatalf("Register inside root: %v", err)
	}

	// Outside the root: refused.
	err := reg.Register(tools.ToolSpec{
		Name:        "shellshock",
		Description: "no",
		Parameters:  llm.Schema{Type: "object"},
	}, "/bin/sh")
	if err == nil {
		t.Fatal("expected ErrOutsideRoot for /bin/sh")
	}

	// Relative paths refused even when inside the root by string match.
	err = reg.Register(tools.ToolSpec{
		Name:        "rel",
		Description: "no",
		Parameters:  llm.Schema{Type: "object"},
	}, "echo_back")
	if err == nil {
		t.Fatal("expected ErrOutsideRoot for relative path")
	}
}

func TestRegistrar_RestartDurability(t *testing.T) {
	t.Parallel()
	binPath := buildEcho(t)
	catalogPath := filepath.Join(t.TempDir(), "catalog.json")

	// "Run 1": wire registry+catalog+registrar, register the tool, throw
	// everything away.
	{
		catalog := dynamic.NewCatalog(dynamic.NewFileStore(catalogPath))
		if err := catalog.Load(); err != nil {
			t.Fatalf("Load run1: %v", err)
		}
		registry := tools.NewRegistry()
		reg := dynamic.NewRegistrar(catalog, registry,
			dynamic.WithBinaryRoot(filepath.Dir(binPath)))
		if err := reg.Register(tools.ToolSpec{
			Name:        "echo_back",
			Description: "demo",
			Parameters:  llm.Schema{Type: "object"},
		}, binPath); err != nil {
			t.Fatalf("Register run1: %v", err)
		}
	}

	// "Run 2": fresh registry + fresh registrar pointing at the same
	// catalog path. Sync should restore the registration and the tool
	// should be callable.
	catalog2 := dynamic.NewCatalog(dynamic.NewFileStore(catalogPath))
	if err := catalog2.Load(); err != nil {
		t.Fatalf("Load run2: %v", err)
	}
	registry2 := tools.NewRegistry()
	reg2 := dynamic.NewRegistrar(catalog2, registry2)
	if _, err := reg2.Sync(); err != nil {
		t.Fatalf("Sync run2: %v", err)
	}

	tool, ok := registry2.Tool("echo_back")
	if !ok {
		t.Fatal("echo_back not in registry after restart")
	}
	res, err := tool.Execute(t.Context(), tools.ToolCall{
		ID:        "x",
		ToolName:  "echo_back",
		Arguments: tools.ToolParameters{"msg": "still here"},
	})
	if err != nil {
		t.Fatalf("Execute after restart: %v", err)
	}
	if !res.Success {
		t.Fatalf("registered-after-restart tool failed: %s", res.Error)
	}
	data, _ := res.Data.(map[string]any)
	if data["msg"] != "still here" {
		t.Fatalf("data = %v, want msg=still here", data)
	}
}

// stubTool is a minimal Tool used to seed a registry slot we want the
// registrar to refuse to overwrite.
type stubTool struct{ name tools.ToolName }

func (s stubTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{Name: s.name, Description: "stub", Parameters: llm.Schema{Type: "object"}}
}
func (s stubTool) Execute(_ context.Context, c tools.ToolCall) (*tools.ToolResult, error) {
	return nil, fmt.Errorf("stub %s: not callable", s.name)
}

// Regression: build_tool MUST refuse a `directory` arg that
// resolves outside the workspace. Earlier the resolver accepted any
// absolute path or `../` traversal — once `go build` ran there, the
// capability escaped the workspace boundary to wherever the OS
// user could reach.
func TestBuildTool_RejectsAbsolutePathOutsideWorkspace(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, "tools", "in"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// go.mod required so the boundary check fires before the
	// "missing go.mod" check.
	if err := os.WriteFile(filepath.Join(ws, "go.mod"), []byte("module ws\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	outsideDir := t.TempDir() // a directory outside ws
	if err := os.WriteFile(
		filepath.Join(outsideDir, "main.go"),
		[]byte("package main\nfunc main() {}\n"),
		0o644,
	); err != nil {
		t.Fatalf("write outside main.go: %v", err)
	}
	registrar := dynamic.NewRegistrar(
		dynamic.NewCatalog(dynamic.NewFileStore(filepath.Join(t.TempDir(), "catalog.json"))),
		tools.NewRegistry(),
	)
	bt := dynamic.NewBuildTool(registrar, ws)
	res, err := bt.Execute(t.Context(), tools.ToolCall{
		ID:        "c",
		ToolName:  dynamic.ToolNameBuildTool,
		Arguments: tools.ToolParameters{"directory": outsideDir},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Success {
		t.Fatalf("build_tool accepted directory outside workspace: %+v", res)
	}
	if !strings.Contains(res.Error, "outside the workspace") {
		t.Errorf("expected 'outside the workspace' in error, got %q", res.Error)
	}
}

func TestBuildTool_RejectsParentTraversal(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, "tools", "in"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, "go.mod"), []byte("module ws\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	registrar := dynamic.NewRegistrar(
		dynamic.NewCatalog(dynamic.NewFileStore(filepath.Join(t.TempDir(), "catalog.json"))),
		tools.NewRegistry(),
	)
	bt := dynamic.NewBuildTool(registrar, ws)
	// "tools/../../" walks out of ws.
	res, err := bt.Execute(t.Context(), tools.ToolCall{
		ID:        "c",
		ToolName:  dynamic.ToolNameBuildTool,
		Arguments: tools.ToolParameters{"directory": "tools/../../"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Success {
		t.Fatalf("build_tool accepted ../../ traversal: %+v", res)
	}
}

// Regression: new_tool MUST refuse to overwrite an existing
// tools/<name>/main.go without explicit replace=true. Earlier this
// path would clobber local edits silently — asking for a tool name
// that already had hand-written code destroyed the work.
func TestNewTool_RefusesOverwriteWithoutReplace(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	existingDir := filepath.Join(ws, "tools", "mytool")
	if err := os.MkdirAll(existingDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	existingMain := filepath.Join(existingDir, "main.go")
	const sentinel = "// IMPORTANT LOCAL EDIT — do not lose\npackage main\n"
	if err := os.WriteFile(existingMain, []byte(sentinel), 0o644); err != nil {
		t.Fatalf("write existing: %v", err)
	}
	registrar := dynamic.NewRegistrar(
		dynamic.NewCatalog(dynamic.NewFileStore(filepath.Join(t.TempDir(), "catalog.json"))),
		tools.NewRegistry(),
	)
	bt := dynamic.NewBuildTool(registrar, ws)
	nt := dynamic.NewNewToolTool(bt, ws)
	res, err := nt.Execute(t.Context(), tools.ToolCall{
		ID:       "c",
		ToolName: dynamic.ToolNameNewTool,
		Arguments: tools.ToolParameters{
			"name":        "mytool",
			"description": "test",
			"body":        "return \"\", nil",
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Success {
		t.Errorf("new_tool overwrote without replace=true: %+v", res)
	}
	// Sentinel content must survive.
	got, err := os.ReadFile(existingMain)
	if err != nil {
		t.Fatalf("read existing after refused write: %v", err)
	}
	if string(got) != sentinel {
		t.Errorf("existing file was clobbered: %q", string(got))
	}
}

// TestDescribeBinary_BoundsStdout guards the cappedWriter wrapping
// of `--describe`. Earlier shape used `cmd.Output()` which buffered
// the entire stdout regardless of size — a malicious / buggy
// binary emitting gigabytes during registration would have grown
// the parent process to match. The capped version keeps at most
// dynamicStdoutCapBytes (1 MB) before discarding the rest, so the
// parser fails on truncated JSON instead of OOM'ing.
func TestDescribeBinary_BoundsStdout(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := `package main

import (
	"os"
	"strings"
)

// Emit ~3 MB of garbage to stdout — well above the 1 MB cap. The
// cap should kick in; DescribeBinary should fail to parse the
// truncated payload rather than allocate 3 MB on the parent.
func main() {
	if len(os.Args) > 1 && os.Args[1] == "--describe" {
		junk := strings.Repeat("A", 3*1024*1024)
		_, _ = os.Stdout.WriteString(junk)
	}
}
`
	srcPath := filepath.Join(dir, "main.go")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(dir, "noisy_describe")
	if out, err := exec.Command("go", "build", "-o", binPath, srcPath).CombinedOutput(); err != nil {
		t.Fatalf("build noisy binary: %v\n%s", err, out)
	}

	_, err := dynamic.DescribeBinary(t.Context(), binPath, 0)
	if err == nil {
		t.Fatal("expected DescribeBinary to fail parsing the truncated noisy output")
	}
	// We don't assert a specific error message — the cap is enforced
	// at the writer layer, and the resulting failure surfaces as
	// either "parse" (json unmarshal of truncated payload) or
	// "spec missing name" (if the truncated bytes happened to start
	// with a JSON-looking prefix). Both are acceptable signals that
	// the cap kicked in. The crucial assertion is "didn't OOM" —
	// which we get for free by the test completing.
	if !strings.Contains(err.Error(), "describe") {
		t.Errorf("err = %v, want describe-related error", err)
	}
}

func buildEnvProbe(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := `package main

import (
	"encoding/json"
	"fmt"
	"os"
)

const key = "ZARL_DYNAMIC_TOOL_SECRET_SHOULD_NOT_LEAK"

type spec struct {
	Name        string                 ` + "`json:\"name\"`" + `
	Description string                 ` + "`json:\"description\"`" + `
	Parameters  map[string]interface{} ` + "`json:\"parameters\"`" + `
}

func main() {
	if len(os.Args) < 2 {
		os.Exit(2)
	}
	switch os.Args[1] {
	case "--describe":
		if v := os.Getenv(key); v != "" {
			fmt.Fprintf(os.Stderr, "secret env leaked during describe: %s", v)
			os.Exit(3)
		}
		_ = json.NewEncoder(os.Stdout).Encode(spec{Name: "env_probe", Description: "env probe", Parameters: map[string]interface{}{"type": "object"}})
	case "--call":
		_ = json.NewEncoder(os.Stdout).Encode(map[string]interface{}{"data": map[string]interface{}{"secret": os.Getenv(key)}})
	}
}
`
	srcPath := filepath.Join(dir, "main.go")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(dir, "env_probe")
	cmd := exec.Command("go", "build", "-o", binPath, srcPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build env probe binary: %v\n%s", err, out)
	}
	return binPath
}

func TestBinaryToolDoesNotInheritParentEnvironment(t *testing.T) {
	const key = "ZARL_DYNAMIC_TOOL_SECRET_SHOULD_NOT_LEAK"
	t.Setenv(key, "super-secret")
	binPath := buildEnvProbe(t)

	spec, err := dynamic.DescribeBinary(t.Context(), binPath, 0)
	if err != nil {
		t.Fatalf("DescribeBinary leaked env or failed: %v", err)
	}
	tool := dynamic.NewBinaryTool(spec, binPath)
	res, err := tool.Execute(
		t.Context(),
		tools.ToolCall{ID: "x", ToolName: spec.Name, Arguments: tools.ToolParameters{}},
	)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.Success {
		t.Fatalf("Execute failed: %s", res.Error)
	}
	data, _ := res.Data.(map[string]any)
	if data["secret"] != "" {
		t.Fatalf("dynamic tool inherited secret env: %v", data["secret"])
	}
}
