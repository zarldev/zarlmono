package shellpolicy_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/shellpolicy"
)

// decideVerify runs a command through the same pipeline the shell
// guardrail uses in verify mode: parse → IR, WriteTargets, DecideVerify.
func decideVerify(t *testing.T, cmd string) shellpolicy.Decision {
	t.Helper()
	ir, _ := shellpolicy.NewUnixParser().Parse(cmd)
	targets, _ := shellpolicy.WriteTargets(cmd)
	return shellpolicy.NewPolicyEngine().DecideVerify(ir, targets)
}

func TestDecideVerify_AllowsReadOnlyAndTestCommands(t *testing.T) {
	t.Parallel()
	for _, cmd := range []string{
		"go test ./...",
		"go test -race -count=1 ./agent/...",
		"go vet ./...",
		"go build ./...",
		"npm test",
		"ls -la",
		"grep -rn TODO .",
		"cat main.go",
		"git status",
		"git log --oneline -5",
		"git diff HEAD",
		"git grep -n pattern",
		"sed -n '1,40p' main.go",   // stream filter — no -i, pure read
		"find . -name '*_test.go'", // find without -delete / -exec
		"echo hello | wc -l",
		"head -20 README.md && tail -5 README.md",
	} {
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			if d := decideVerify(t, cmd); d.IsBlocked {
				t.Errorf("verify profile blocked %q: %s", cmd, d.BlockReason)
			}
		})
	}
}

func TestDecideVerify_BlocksWorkspaceMutation(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		cmd  string
		want string // substring expected in BlockReason
	}{
		// write-target detection (statically-resolvable operands)
		{"sed -i 's/a/b/' main.go", "main.go"},
		{"tee out.log", "out.log"},
		{"find . -name '*.tmp' -delete", "verify"},
		{"echo x | xargs rm", `"xargs"`},
		// deny-list heads: content mutators with dynamic operands that
		// WriteTargets can't resolve still block on the command key
		{"rm -rf $DIR", `"rm"`},
		{"mv \"$A\" \"$B\"", `"mv"`},
		{"cp -r src dst", "verify"},
		{"touch stamp", `"touch"`},
		{"mkdir -p build", `"mkdir"`},
		{"chmod +x run.sh", `"chmod"`},
		{"patch -p1 < fix.diff", `"patch"`},
		// repo-state git
		{"git commit -m wip", `"git commit"`},
		{"git checkout -- .", "verify"},
		{"git reset --hard HEAD", `"git reset"`},
		{"git stash", `"git stash"`},
		{"git push origin main", `"git push"`},
		// module-mutating go
		{"go mod tidy", `"go mod"`},
		{"go get example.com/pkg@latest", `"go get"`},
		{"go generate ./...", `"go generate"`},
	} {
		t.Run(tt.cmd, func(t *testing.T) {
			t.Parallel()
			d := decideVerify(t, tt.cmd)
			if !d.IsBlocked {
				t.Fatalf("verify profile passed %q; want block", tt.cmd)
			}
			if !strings.Contains(d.BlockReason, tt.want) {
				t.Errorf("BlockReason = %q, want it to mention %q", d.BlockReason, tt.want)
			}
		})
	}
}

// The standard rules still apply under the verify profile — and take
// priority, so their messages (which point at better tools) win.
func TestDecideVerify_StandardRulesStillApply(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		cmd  string
		want string
	}{
		{"cd /tmp && ls", "`cd` is blocked"},
		{"echo hi > out.txt", "output redirection"},
		{"if then fi", "did not parse"},
	} {
		t.Run(tt.cmd, func(t *testing.T) {
			t.Parallel()
			d := decideVerify(t, tt.cmd)
			if !d.IsBlocked {
				t.Fatalf("verify profile passed %q; want block", tt.cmd)
			}
			if !strings.Contains(d.BlockReason, tt.want) {
				t.Errorf("BlockReason = %q, want standard-rule message containing %q", d.BlockReason, tt.want)
			}
		})
	}
}
