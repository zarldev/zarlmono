package shellpolicy_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/shellpolicy"
)

func TestWriteTargets(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    []string
	}{
		{"rm file", "rm pkg/foo/foo_test.go", []string{"pkg/foo/foo_test.go"}},
		{"rm recursive dir", "rm -rf testdata", []string{"testdata"}},
		{"mv both operands", "mv a_test.go b_test.go", []string{"a_test.go", "b_test.go"}},
		{"cp both operands", "cp src.go dst_test.go", []string{"src.go", "dst_test.go"}},
		{"tee target", "tee foo_test.go", []string{"foo_test.go"}},
		{"truncate skips flag, keeps value+file", "truncate -s 0 foo_test.go", []string{"0", "foo_test.go"}},
		{"unlink", "unlink foo_test.go", []string{"foo_test.go"}},

		{"sed in-place skips script", "sed -i 's/want 5/want 6/' pkg/foo/foo_test.go", []string{"pkg/foo/foo_test.go"}},
		{"sed in-place with backup ext", "sed -i.bak -e 's/a/b/' bar_test.go", []string{"bar_test.go"}},
		{"sed read-only collects nothing", "sed 's/x/y/' foo_test.go", nil},
		{"sed in-place on source, script mentions test", "sed -i 's/foo_test/bar/' pkg/foo/foo.go", []string{"pkg/foo/foo.go"}},
		{"perl in-place", "perl -i -pe 's/a/b/' foo_test.go", []string{"foo_test.go"}},

		{"redirect overwrite", "echo broken > foo_test.go", []string{"foo_test.go"}},
		{"redirect append", "cat x >> bar_test.go", []string{"bar_test.go"}},
		{"redirect to /dev/null ignored", "ls -la > /dev/null", nil},
		{"fd merge ignored", "go test ./... 2>&1", nil},

		{"dd of only", "dd if=foo_test.go of=out.txt", []string{"out.txt"}},
		{"dd of test", "dd of=foo_test.go", []string{"foo_test.go"}},

		{"git checkout dashdash", "git checkout -- pkg/foo/foo_test.go", []string{"pkg/foo/foo_test.go"}},
		{"git rm", "git rm foo_test.go", []string{"foo_test.go"}},
		{"git restore", "git restore pkg/foo_test.go", []string{"pkg/foo_test.go"}},
		{"git status not mutating", "git status", nil},

		{"absolute path basename", "/bin/rm foo_test.go", []string{"foo_test.go"}},
		{"subshell chain", "(cd pkg/foo && rm bar_test.go)", []string{"bar_test.go"}},
		{"command substitution", "echo $(rm foo_test.go)", []string{"foo_test.go"}},
		{"chained commands", "go build ./... && rm foo_test.go", []string{"foo_test.go"}},

		{"read-only cat", "cat pkg/foo/foo_test.go", nil},
		{"read-only grep", "grep -r foo_test .", nil},
		{"non-mutating ls", "ls -la testdata", nil},
		{"test runner", "go test ./...", nil},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := shellpolicy.WriteTargets(tt.command)
			if err != nil {
				t.Fatalf("WriteTargets(%q) error: %v", tt.command, err)
			}
			gotSorted := slices.Clone(got)
			slices.Sort(gotSorted)
			wantSorted := slices.Clone(tt.want)
			slices.Sort(wantSorted)
			if !slices.Equal(gotSorted, wantSorted) {
				t.Errorf("WriteTargets(%q) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}

func TestWriteTargetsParseError(t *testing.T) {
	// An unterminated quote is a syntax error; WriteTargets surfaces ErrUnparseable
	// so the caller can tell it apart from a clean "nothing to write" (the strict
	// guardrail treats it as a pass and leaves the syntax rejection to the
	// ShellGuardrail).
	got, err := shellpolicy.WriteTargets("rm 'unterminated")
	if !errors.Is(err, shellpolicy.ErrUnparseable) {
		t.Fatalf("WriteTargets(malformed) err = %v, want ErrUnparseable", err)
	}
	if got != nil {
		t.Errorf("WriteTargets(malformed) targets = %v, want nil", got)
	}
}

func TestWriteTargetsDynamicWordSkipped(t *testing.T) {
	// A target built from a parameter expansion can't be resolved statically;
	// it is skipped here (a full Parse raises an Expansion risk flag the caller
	// can fail closed on separately). The command parses fine, so no error.
	got, err := shellpolicy.WriteTargets("rm $TARGET")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("WriteTargets(rm $TARGET) = %v, want none (dynamic word)", got)
	}
}
