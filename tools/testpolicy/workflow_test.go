package testpolicy_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/zarldev/zarlmono/tools/testpolicy"
)

type workflowStep struct {
	ID   string            `yaml:"id"`
	Name string            `yaml:"name"`
	Env  map[string]string `yaml:"env"`
	Run  string            `yaml:"run"`
}

func TestWorkflowPolicyBase(t *testing.T) {
	selector := policyWorkflowStep(t, "select test-source policy base")
	enforcer := policyWorkflowStep(t, "enforce test-source policy")
	if selector.ID != "test-policy-base" || selector.Env["TEST_POLICY_BASE"] != "${{ github.event.pull_request.base.sha || github.event.before }}" {
		t.Fatal("policy base must select the PR base before the push base")
	}
	if enforcer.Env["TEST_POLICY_BASE"] != "${{ steps.test-policy-base.outputs.base }}" {
		t.Fatal("policy enforcement does not consume the selected base")
	}

	for _, name := range []string{"commit", "initial push", "missing base"} {
		t.Run(name, func(t *testing.T) {
			root := gitFixture(t)
			writeFile(t, root, "zkit/widget/widget_test.go", "package widget_test\n")
			commitAll(t, root)
			base := policyGitOutput(t, root, "rev-parse", "HEAD")
			input := base
			switch name {
			case "initial push":
				input = strings.Repeat("0", 40)
			case "missing base":
				input = ""
			}
			outputPath := filepath.Join(t.TempDir(), "output")
			cmd := exec.CommandContext(t.Context(), "bash", "-e", "-o", "pipefail", "-c", selector.Run)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "TEST_POLICY_BASE="+input, "GITHUB_OUTPUT="+outputPath)
			cmd.WaitDelay = 2 * time.Second
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("select workflow base: %v: %s", err, output)
			}
			output, err := os.ReadFile(outputPath)
			if err != nil {
				t.Fatal(err)
			}
			selected, ok := strings.CutPrefix(strings.TrimSpace(string(output)), "base=")
			if !ok || selected == "" {
				t.Fatalf("workflow output = %q", output)
			}
			if name == "commit" {
				if selected != base {
					t.Fatalf("workflow changed existing base: %q, want %q", selected, base)
				}
			} else if tree := policyGitOutput(t, root, "ls-tree", selected); tree != "" {
				t.Fatalf("initial-push base is not empty: %q", tree)
			}
			var diagnostics bytes.Buffer
			if err := testpolicy.Run(t.Context(), root, selected, &diagnostics); err != nil {
				t.Fatalf("selected base rejected: %v: %s", err, &diagnostics)
			}
			// Existing test-context violations must be seen on an initial push,
			// rather than hidden by comparing the new branch to its own HEAD.
			writeFile(t, root, "zkit/widget/widget_test.go", "package widget_test\n\nimport (\"context\"; \"testing\")\n\nfunc TestContext(t *testing.T) { _ = context."+"Background() }\n")
			commitAll(t, root)
			if err := testpolicy.Run(t.Context(), root, selected, &diagnostics); !errors.Is(err, testpolicy.ErrViolations) {
				t.Fatalf("policy violation = %v, want ErrViolations; %s", err, &diagnostics)
			}
		})
	}
}

func TestPolicyRejectsUnknownAndBlobBases(t *testing.T) {
	root := gitFixture(t)
	writeFile(t, root, "README.md", "fixture\n")
	commitAll(t, root)
	blob := policyGitOutput(t, root, "rev-parse", "HEAD:README.md")
	for _, base := range []string{"unknown-ref", strings.Repeat("0", 40), blob} {
		t.Run(base, func(t *testing.T) {
			var diagnostics bytes.Buffer
			if err := testpolicy.Run(t.Context(), root, base, &diagnostics); !errors.Is(err, testpolicy.ErrUnknownBase) {
				t.Fatalf("Run(%q) = %v, want ErrUnknownBase", base, err)
			}
		})
	}
}

func policyWorkflowStep(t *testing.T, name string) workflowStep {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []workflowStep `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(body, &workflow); err != nil {
		t.Fatal(err)
	}
	for _, step := range workflow.Jobs["repository-tools"].Steps {
		if step.Name == name {
			return step
		}
	}
	t.Fatalf("workflow step %q not found", name)
	return workflowStep{}
}

func policyGitOutput(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = root
	cmd.WaitDelay = 2 * time.Second
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}
