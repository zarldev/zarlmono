package harness_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/swebench-eval/harness"
)

func TestAblationArms(t *testing.T) {
	t.Run("empty spec is no arms", func(t *testing.T) {
		arms, err := harness.AblationArms("")
		if err != nil || arms != nil {
			t.Fatalf("AblationArms(\"\") = %v, %v; want nil, nil", arms, err)
		}
	})

	t.Run("all yields every canonical arm including baseline", func(t *testing.T) {
		arms, err := harness.AblationArms("all")
		if err != nil {
			t.Fatalf("AblationArms(all): %v", err)
		}
		names := map[string]bool{}
		for _, a := range arms {
			names[a.Name] = true
		}
		for _, want := range []string{"baseline", "no-decompose", "no-fanout", "judge"} {
			if !names[want] {
				t.Errorf("all: missing arm %q (got %v)", want, names)
			}
		}
	})

	t.Run("named arms resolve in order", func(t *testing.T) {
		arms, err := harness.AblationArms("baseline, no-decompose ,judge")
		if err != nil {
			t.Fatalf("AblationArms: %v", err)
		}
		if len(arms) != 3 || arms[0].Name != "baseline" || arms[1].Name != "no-decompose" || arms[2].Name != "judge" {
			t.Fatalf("arms = %+v, want baseline/no-decompose/judge", arms)
		}
		if len(arms[1].Disabled) != 1 || arms[1].Disabled[0] != "decompose" {
			t.Errorf("no-decompose arm disables %v, want [decompose]", arms[1].Disabled)
		}
		if !arms[2].Judge {
			t.Error("judge arm has Judge=false")
		}
	})

	t.Run("unknown arm errors rather than silently running baseline", func(t *testing.T) {
		_, err := harness.AblationArms("no-decmpose")
		if err == nil || !strings.Contains(err.Error(), "unknown ablation arm") {
			t.Fatalf("AblationArms(typo) = %v, want unknown-arm error", err)
		}
	})
}

func TestZarlcodeDriverNameCarriesArm(t *testing.T) {
	base := &harness.ZarlcodeDriver{}
	if got := base.Name(); got != "zarlcode" {
		t.Errorf("baseline Name() = %q, want zarlcode", got)
	}
	named := &harness.ZarlcodeDriver{Ablation: harness.Ablation{Name: "no-decompose"}}
	// Hyphen, not colon — the name reaches Docker container names via the
	// evaluator, and colons are rejected there.
	if got := named.Name(); got != "zarlcode-no-decompose" {
		t.Errorf("arm Name() = %q, want zarlcode-no-decompose", got)
	}
	explicit := &harness.ZarlcodeDriver{Ablation: harness.Ablation{Name: "baseline"}}
	if got := explicit.Name(); got != "zarlcode" {
		t.Errorf("explicit baseline Name() = %q, want zarlcode", got)
	}
}
