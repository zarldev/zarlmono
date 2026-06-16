package shellpolicy_test

import (
	"slices"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/shellpolicy"
)

// These lock the integrity-guard bypasses found in the audit:
// each command deletes/overwrites a grader's test file but previously
// returned zero write targets, sailing past TestEditStrict in eval mode.

func TestWriteTargets_ClosedBypasses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		command string
		want    string // a target that must appear
	}{
		{"git -C dir subcommand", "git -C . rm foo_test.go", "foo_test.go"},
		{"git -C subdir checkout", "git -C sub checkout -- foo_test.go", "foo_test.go"},
		{"git --git-dir space", "git --git-dir .git rm foo_test.go", "foo_test.go"},
		{"git --git-dir= equals", "git --git-dir=.git rm foo_test.go", "foo_test.go"},
		{"command wrapper", "command rm foo_test.go", "foo_test.go"},
		{"env wrapper", "env rm foo_test.go", "foo_test.go"},
		{"env with assignment", "env FOO=bar rm foo_test.go", "foo_test.go"},
		{"busybox wrapper", "busybox rm foo_test.go", "foo_test.go"},
		{"sudo wrapper", "sudo rm foo_test.go", "foo_test.go"},
		{"timeout duration wrapper", "timeout 5 rm foo_test.go", "foo_test.go"},
		{"nested wrappers", "env command rm foo_test.go", "foo_test.go"},
		{"find -delete pattern", "find . -name '*_test.go' -delete", "*_test.go"},
		{"find -exec rm", "find . -name 'x_test.go' -exec rm {} ;", "x_test.go"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := shellpolicy.WriteTargets(tt.command)
			if err != nil {
				t.Fatalf("WriteTargets(%q) err = %v", tt.command, err)
			}
			if !slices.Contains(got, tt.want) {
				t.Fatalf("WriteTargets(%q) = %v, want it to contain %q", tt.command, got, tt.want)
			}
		})
	}
}

func TestWriteTargets_StillReadsAreNotTargets(t *testing.T) {
	t.Parallel()
	// Reads / non-mutating forms must still yield nothing (no over-blocking).
	for _, cmd := range []string{
		"git -C . status",
		"git log --oneline",
		"cat foo_test.go",
		"find . -name '*_test.go'", // search only, no -delete
		"env FOO=bar go test ./...",
		"timeout 5 go build ./...",
	} {
		got, err := shellpolicy.WriteTargets(cmd)
		if err != nil {
			t.Fatalf("WriteTargets(%q) err = %v", cmd, err)
		}
		if len(got) != 0 {
			t.Fatalf("WriteTargets(%q) = %v, want no targets", cmd, got)
		}
	}
}

func TestInterpreterInlineCode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		command string
		want    string // a substring that must appear in some payload
	}{
		{`python -c "import os; os.remove('foo_test.go')"`, "foo_test.go"},
		{`python3 -c 'open("x_test.go","w")'`, "x_test.go"},
		{`node -e "require('fs').unlinkSync('a_test.go')"`, "a_test.go"},
		{`perl -e 'unlink "b_test.go"'`, "b_test.go"},
		{`env node -e "fs.writeFileSync('c_test.go','')"`, "c_test.go"},
		{`sh -c "rm d_test.go"`, "d_test.go"},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			t.Parallel()
			codes, err := shellpolicy.InterpreterInlineCode(tt.command)
			if err != nil {
				t.Fatalf("InterpreterInlineCode(%q) err = %v", tt.command, err)
			}
			found := false
			for _, c := range codes {
				if len(c) > 0 && contains(c, tt.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("InterpreterInlineCode(%q) = %v, want a payload containing %q", tt.command, codes, tt.want)
			}
		})
	}
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
