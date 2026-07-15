package shellpolicy_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/shellpolicy"
)

// The relaxed profile (kernel sandbox off) drops the ergonomic cd and
// redirect blocks: with no kernel boundary they only provoke evasion.
func TestPolicy_RelaxedAllowsCdAndRedirect(t *testing.T) {
	t.Parallel()
	cases := []string{
		"cd /tmp",
		"echo hi > /tmp/x",
		"cd src && echo hi > x.go",
	}
	engine := shellpolicy.NewPolicyEngine(shellpolicy.WithRelaxed(true))
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			ir, _ := shellpolicy.NewUnixParser().Parse(cmd)
			d := engine.Decide(ir)
			if d.IsBlocked {
				t.Errorf("relaxed Decide(%q): want pass, got Block(%q)", cmd, d.BlockReason)
			}
		})
	}
}

// Correctness blocks (version, syntax) are not nannying and hold in both
// profiles.
func TestPolicy_RelaxedStillBlocksSyntaxAndVersion(t *testing.T) {
	t.Parallel()
	engine := shellpolicy.NewPolicyEngine(shellpolicy.WithRelaxed(true))

	bad, _ := shellpolicy.NewUnixParser().Parse("echo 'unterminated")
	if d := engine.Decide(bad); !d.IsBlocked {
		t.Error("relaxed: syntax error must still block")
	}

	ver := shellpolicy.ParsedIR{Version: "bogus", Platform: shellpolicy.PlatformUnix}
	if d := engine.Decide(ver); !d.IsBlocked {
		t.Error("relaxed: version mismatch must still block")
	}
}

// Verify mode is always strict: a verify sub-agent must not mutate the
// workspace even on a relaxed engine.
func TestPolicy_DecideVerifyStaysStrictWhenRelaxed(t *testing.T) {
	t.Parallel()
	engine := shellpolicy.NewPolicyEngine(shellpolicy.WithRelaxed(true))

	redir, _ := shellpolicy.NewUnixParser().Parse("echo hi > /tmp/x")
	targets, _ := shellpolicy.WriteTargets("echo hi > /tmp/x")
	if d := engine.DecideVerify(redir, targets); !d.IsBlocked {
		t.Error("relaxed verify: redirect must still block")
	}

	cd, _ := shellpolicy.NewUnixParser().Parse("cd /tmp")
	if d := engine.DecideVerify(cd, nil); !d.IsBlocked {
		t.Errorf("relaxed verify: cd must still block, got pass")
	}
}

// The default engine (sandbox on) keeps the strict profile.
func TestPolicy_DefaultStaysStrict(t *testing.T) {
	t.Parallel()
	ir, _ := shellpolicy.NewUnixParser().Parse("echo hi > /tmp/x")
	d := shellpolicy.NewPolicyEngine().Decide(ir)
	if !d.IsBlocked || !strings.Contains(d.BlockReason, "redirection") {
		t.Errorf("default engine: want redirect block, got %+v", d)
	}
}
