package shellpolicy_test

import (
	"slices"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/shellpolicy"
)

func TestUnixParser_SimpleCommand(t *testing.T) {
	t.Parallel()
	p := shellpolicy.NewUnixParser()
	ir, err := p.Parse("ls -la")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ir.Version != shellpolicy.IRVersion {
		t.Errorf("Version = %q, want %q", ir.Version, shellpolicy.IRVersion)
	}
	if !slices.Equal(ir.Commands, []string{"ls"}) {
		t.Errorf("Commands = %v, want [ls]", ir.Commands)
	}
	if !slices.Equal(ir.CommandFlags["ls"], []string{"-la"}) {
		t.Errorf("CommandFlags[ls] = %v, want [-la]", ir.CommandFlags["ls"])
	}
}

func TestUnixParser_Tier2CompoundKey(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		cmd      string
		wantCmds []string
	}{
		{"git log gets compound key", "git log --oneline -5", []string{"git log"}},
		{"git push gets compound key", "git push origin main", []string{"git push"}},
		{"go test gets compound key", "go test ./...", []string{"go test"}},
		{"go alone falls back to plain key", "go", []string{"go"}},
		{"go with flag only stays plain", "go -h", []string{"go"}},
		{"non-tier2 stays plain", "ls subcommand", []string{"ls"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ir, err := shellpolicy.NewUnixParser().Parse(tc.cmd)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.cmd, err)
			}
			if !slices.Equal(ir.Commands, tc.wantCmds) {
				t.Errorf("Commands = %v, want %v", ir.Commands, tc.wantCmds)
			}
		})
	}
}

func TestUnixParser_DistinctTier2KeysAccumulate(t *testing.T) {
	t.Parallel()
	ir, err := shellpolicy.NewUnixParser().Parse("git log && git push")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []string{"git log", "git push"}
	if !slices.Equal(ir.Commands, want) {
		t.Errorf("Commands = %v, want %v", ir.Commands, want)
	}
}

func TestUnixParser_FlagNormalisation(t *testing.T) {
	t.Parallel()
	ir, err := shellpolicy.NewUnixParser().Parse("head -20 -n=5 --color=always foo.txt")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	flags := ir.CommandFlags["head"]
	slices.Sort(flags)
	want := []string{"-*", "--color", "-n"} // ASCII sort: '*' (0x2A) < '-' (0x2D) < 'n' (0x6E)
	if !slices.Equal(flags, want) {
		t.Errorf("flags = %v, want %v (numeric → -*, --flag=value → --flag)", flags, want)
	}
}

func TestUnixParser_RisksTriggeredCorrectly(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cmd  string
		want shellpolicy.ReasonCode
	}{
		{"cd raises ReasonCd", "cd /tmp", shellpolicy.ReasonCd},
		{"file redirect raises ReasonRedirect", "echo hi > /tmp/x", shellpolicy.ReasonRedirect},
		{"append redirect raises ReasonRedirect", "echo hi >> /tmp/x", shellpolicy.ReasonRedirect},
		{"&> raises ReasonRedirect", "echo hi &> /tmp/x", shellpolicy.ReasonRedirect},
		{"variable expansion raises ReasonExpansion", "echo $HOME", shellpolicy.ReasonExpansion},
		{"arithmetic expansion raises ReasonExpansion", "echo $((1+1))", shellpolicy.ReasonExpansion},
		{"command substitution raises ReasonSubshell", "echo $(date)", shellpolicy.ReasonSubshell},
		{"backtick substitution raises ReasonSubshell", "echo `date`", shellpolicy.ReasonSubshell},
		{"pipe raises ReasonOperator", "ls | wc -l", shellpolicy.ReasonOperator},
		{"&& raises ReasonOperator", "ls && pwd", shellpolicy.ReasonOperator},
		{"semicolon between stmts raises ReasonOperator", "ls; pwd", shellpolicy.ReasonOperator},
		{"grep raises ReasonShellReadTool", "grep -r foo .", shellpolicy.ReasonShellReadTool},
		{"sed raises ReasonShellReadTool", "sed -n '1,20p' main.go", shellpolicy.ReasonShellReadTool},
		{"read-helper pipe raises ReasonShellReadTool", "ls | grep foo", shellpolicy.ReasonShellReadTool},
		{"opaque interpreter pipe raises ReasonOpaqueInterpreter", "echo 'print(1)' | python3", shellpolicy.ReasonOpaqueInterpreter},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ir, err := shellpolicy.NewUnixParser().Parse(tc.cmd)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.cmd, err)
			}
			if !slices.Contains(ir.RiskFlags, tc.want) {
				t.Errorf("RiskFlags = %v, want to contain %q", ir.RiskFlags, tc.want)
			}
		})
	}
}

func TestUnixParser_SafeRedirectTargetsDoNotFlag(t *testing.T) {
	t.Parallel()
	cases := []string{
		"echo hi > /dev/null",
		"echo hi 2> /dev/null",
		"grep foo bar.txt 2>&1",
		"cat foo.txt < bar.txt",
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			ir, err := shellpolicy.NewUnixParser().Parse(cmd)
			if err != nil {
				t.Fatalf("Parse(%q): %v", cmd, err)
			}
			if slices.Contains(ir.RiskFlags, shellpolicy.ReasonRedirect) {
				t.Errorf("ReasonRedirect set for %q; flags=%v", cmd, ir.RiskFlags)
			}
		})
	}
}

func TestUnixParser_SyntaxErrorFlagsAndReturnsError(t *testing.T) {
	t.Parallel()
	ir, err := shellpolicy.NewUnixParser().Parse("echo 'unterminated")
	if err == nil {
		t.Fatal("Parse: want error for unterminated quote")
	}
	if !slices.Contains(ir.RiskFlags, shellpolicy.ReasonSyntaxError) {
		t.Errorf("RiskFlags = %v, want ReasonSyntaxError", ir.RiskFlags)
	}
	if len(ir.ParseErrors) == 0 {
		t.Error("ParseErrors = empty, want at least one entry")
	}
}

func TestUnixParser_DynamicWordSkippedNotPanicked(t *testing.T) {
	t.Parallel()
	// The first word is a variable expansion; we shouldn't record
	// the command (the name is unresolvable) but ReasonExpansion
	// should still fire from the walker.
	ir, err := shellpolicy.NewUnixParser().Parse(`$CMD foo bar`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(ir.Commands) != 0 {
		t.Errorf("Commands = %v, want empty (dynamic name)", ir.Commands)
	}
	if !slices.Contains(ir.RiskFlags, shellpolicy.ReasonExpansion) {
		t.Errorf("RiskFlags = %v, want ReasonExpansion", ir.RiskFlags)
	}
}
