package releasecheck_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/tools/releasecheck"
)

func TestBuild(t *testing.T) {
	t.Parallel()
	root := fixture(t, `## [zarlcode/v1.2.3] — 2026-08-29
## [examples/v1.2.3] — 2026-08-29
`)

	plan, err := releasecheck.Build(root, "v1.2.3", "custom", " zarlcode,examples,zarlcode ")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if want := []string{"zarlcode", "examples"}; !reflect.DeepEqual(plan.Modules, want) {
		t.Errorf("modules = %v, want %v", plan.Modules, want)
	}
	if want := []string{"zarlcode/v1.2.3", "examples/v1.2.3"}; !reflect.DeepEqual(plan.Tags, want) {
		t.Errorf("tags = %v, want %v", plan.Tags, want)
	}
	if len(plan.Pins) != 2 || plan.Pins[0].Consumer != "examples" || plan.Pins[1].Consumer != "zarlcode" {
		t.Errorf("pins = %#v", plan.Pins)
	}
}

func TestBuildAcceptsCanonicalPrerelease(t *testing.T) {
	t.Parallel()
	root := fixture(t, "## [zkit/v1.2.3-rc.1] — 2026-08-29\n")
	if _, err := releasecheck.Build(root, "v1.2.3-rc.1", "zkit", ""); err != nil {
		t.Fatalf("Build: %v", err)
	}
}

func TestBuildRejectsInvalidPlans(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		version   string
		scope     string
		custom    string
		changelog string
		want      string
	}{
		{name: "bad version", version: "v1.02.3", scope: "zkit", want: "canonical semantic version"},
		{name: "build metadata", version: "v1.2.3+meta", scope: "zkit", want: "canonical semantic version"},
		{name: "unknown scope", version: "v1.2.3", scope: "all", want: "unknown release scope"},
		{name: "empty custom", version: "v1.2.3", scope: "custom", want: "at least one"},
		{name: "unknown module", version: "v1.2.3", scope: "custom", custom: "zarlcode,nope", want: "unsupported module"},
		{name: "zkit with consumer", version: "v1.2.3", scope: "custom", custom: "zkit,zarlcode", want: "release zkit separately"},
		{name: "missing heading", version: "v1.2.3", scope: "zkit", want: "found 0"},
		{name: "undated heading", version: "v1.2.3", scope: "zkit", changelog: "## [zkit/v1.2.3]\n", want: "found 0"},
		{name: "bad date", version: "v1.2.3", scope: "zkit", changelog: "## [zkit/v1.2.3] — 2026-02-30\n", want: "invalid date"},
		{name: "duplicate heading", version: "v1.2.3", scope: "zkit", changelog: "## [zkit/v1.2.3] — 2026-08-29\n## [zkit/v1.2.3] — 2026-08-29\n", want: "found 2"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := fixture(t, test.changelog)
			_, err := releasecheck.Build(root, test.version, test.scope, test.custom)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func fixture(t *testing.T, changelog string) string {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "CHANGELOG.md"), changelog)
	write(t, filepath.Join(root, "zarlcode", "go.mod"), "module github.com/zarldev/zarlmono/zarlcode\n\ngo 1.26.0\n\nrequire github.com/zarldev/zarlmono/zkit v1.2.2\n")
	write(t, filepath.Join(root, "examples", "go.mod"), "module github.com/zarldev/zarlmono/examples\n\ngo 1.26.0\n\nrequire github.com/zarldev/zarlmono/zkit v1.2.1\n")
	write(t, filepath.Join(root, "swebench-eval", "go.mod"), "module github.com/zarldev/zarlmono/swebench-eval\n\ngo 1.26.0\n\nrequire (\n github.com/zarldev/zarlmono/zarlcode v1.2.0\n github.com/zarldev/zarlmono/zkit v1.2.1\n)\n")
	return root
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
