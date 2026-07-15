package shellpolicy_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/shellpolicy"
)

func TestPolicy_VersionMismatchBlocks(t *testing.T) {
	t.Parallel()
	ir := shellpolicy.ParsedIR{Version: "bogus", Platform: shellpolicy.PlatformUnix}
	d := shellpolicy.NewPolicyEngine().Decide(ir)
	if !d.IsBlocked {
		t.Fatal("version mismatch: want IsBlocked")
	}
	if !strings.Contains(d.BlockReason, "version") {
		t.Errorf("BlockReason = %q, want it to mention version", d.BlockReason)
	}
}

func TestPolicy_SyntaxErrorBlocksWithParserMessage(t *testing.T) {
	t.Parallel()
	ir, _ := shellpolicy.NewUnixParser().Parse("echo 'unterminated")
	d := shellpolicy.NewPolicyEngine().Decide(ir)
	if !d.IsBlocked {
		t.Fatal("syntax error: want IsBlocked")
	}
	if !strings.Contains(d.BlockReason, "parse") {
		t.Errorf("BlockReason = %q, want it to mention parse", d.BlockReason)
	}
	if !strings.Contains(d.BlockReason, "parser said") {
		t.Errorf("BlockReason = %q, want it to surface the parser's message", d.BlockReason)
	}
}

func TestPolicy_CdBlocks(t *testing.T) {
	t.Parallel()
	ir, _ := shellpolicy.NewUnixParser().Parse("cd /tmp")
	d := shellpolicy.NewPolicyEngine().Decide(ir)
	if !d.IsBlocked {
		t.Fatal("cd: want IsBlocked")
	}
	for _, want := range []string{"cd", "workspace", "read", "write"} {
		if !strings.Contains(d.BlockReason, want) {
			t.Errorf("BlockReason missing %q: %q", want, d.BlockReason)
		}
	}
}

func TestPolicy_UnsafeRedirectBlocks(t *testing.T) {
	t.Parallel()
	ir, _ := shellpolicy.NewUnixParser().Parse("echo hi > /tmp/x")
	d := shellpolicy.NewPolicyEngine().Decide(ir)
	if !d.IsBlocked {
		t.Fatal("file redirect: want IsBlocked")
	}
	for _, want := range []string{"redirection", "`write`", "edit"} {
		if !strings.Contains(d.BlockReason, want) {
			t.Errorf("BlockReason missing %q: %q", want, d.BlockReason)
		}
	}
}

func TestPolicy_SafeCommandPasses(t *testing.T) {
	t.Parallel()
	cases := []string{
		"git log --oneline",
		"go test ./pkg/foo",
		"echo hi > /dev/null",
		"echo $HOME",
		"echo $(date)",
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			ir, _ := shellpolicy.NewUnixParser().Parse(cmd)
			d := shellpolicy.NewPolicyEngine().Decide(ir)
			if d.IsBlocked {
				t.Errorf("Decide(%q): want pass, got Block(%q); flags=%v", cmd, d.BlockReason, ir.RiskFlags)
			}
		})
	}
}

func TestPolicy_DecisionEchoesReasonCodes(t *testing.T) {
	t.Parallel()
	ir, _ := shellpolicy.NewUnixParser().Parse("ls | grep foo")
	d := shellpolicy.NewPolicyEngine().Decide(ir)
	if !d.IsBlocked {
		t.Fatalf("Decide(shell read pipeline): want block")
	}
	if !strings.Contains(d.BlockReason, "workspace tools") {
		t.Errorf("BlockReason = %q, want workspace-tool guidance", d.BlockReason)
	}
	if len(d.ReasonCodes) == 0 {
		t.Error("ReasonCodes = empty, want the operator flag echoed")
	}
}

func TestPolicy_ShellReadToolsBlockWithGuidance(t *testing.T) {
	t.Parallel()
	for _, cmd := range []string{"grep -r foo .", "sed -n '1,20p' main.go", "find . -name '*.go'", "head -20 README.md"} {
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			ir, _ := shellpolicy.NewUnixParser().Parse(cmd)
			d := shellpolicy.NewPolicyEngine().Decide(ir)
			if !d.IsBlocked {
				t.Fatalf("Decide(%q): want shell read-tool block", cmd)
			}
			if !strings.Contains(d.BlockReason, "registered workspace tools") {
				t.Errorf("BlockReason = %q, want registered-tool guidance", d.BlockReason)
			}
		})
	}
}

func TestPolicy_OpaqueInterpreterBlocks(t *testing.T) {
	t.Parallel()
	ir, _ := shellpolicy.NewUnixParser().Parse("echo 'print(1)' | python3")
	d := shellpolicy.NewPolicyEngine().Decide(ir)
	if !d.IsBlocked {
		t.Fatal("opaque interpreter: want block")
	}
	if !strings.Contains(d.BlockReason, "static analysis cannot inspect") {
		t.Errorf("BlockReason = %q, want opaque-payload guidance", d.BlockReason)
	}
}

func TestPolicy_CdBlockTakesPriorityOverRedirect(t *testing.T) {
	t.Parallel()
	// Both signals present — cd is checked first so its message wins.
	ir, _ := shellpolicy.NewUnixParser().Parse("cd /tmp && echo hi > /tmp/x")
	d := shellpolicy.NewPolicyEngine().Decide(ir)
	if !d.IsBlocked {
		t.Fatal("cd+redirect: want IsBlocked")
	}
	if !strings.Contains(d.BlockReason, "cd") {
		t.Errorf("BlockReason = %q, want it to lead with cd (higher priority than redirect)", d.BlockReason)
	}
}
