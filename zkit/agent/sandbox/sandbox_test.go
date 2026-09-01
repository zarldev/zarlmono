package sandbox_test

import (
	"os"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/sandbox"
)

const envSwitch = "ZARLCODE_SANDBOX"

func TestEnvOverride(t *testing.T) {
	t.Run("unset", func(t *testing.T) {
		old, had := os.LookupEnv(envSwitch)
		if had {
			t.Cleanup(func() { _ = os.Setenv(envSwitch, old) }) //nolint:usetesting // restores prior value; t.Setenv cannot unset.
		} else {
			t.Cleanup(func() { _ = os.Unsetenv(envSwitch) })
		}
		if err := os.Unsetenv(envSwitch); err != nil {
			t.Fatalf("unset env: %v", err)
		}
		enabled, ok := sandbox.EnvOverride()
		if ok || enabled {
			t.Fatalf("unset env: enabled=%v ok=%v, want false/false", enabled, ok)
		}
	})

	t.Run("off disables", func(t *testing.T) {
		t.Setenv(envSwitch, "off")
		enabled, ok := sandbox.EnvOverride()
		if !ok || enabled {
			t.Fatalf("off env: enabled=%v ok=%v, want false/true", enabled, ok)
		}
	})

	t.Run("on enables", func(t *testing.T) {
		t.Setenv(envSwitch, "on")
		enabled, ok := sandbox.EnvOverride()
		if !ok || !enabled {
			t.Fatalf("on env: enabled=%v ok=%v, want true/true", enabled, ok)
		}
	})
}

func TestEnabledFromEnvDefault(t *testing.T) {
	old, had := os.LookupEnv(envSwitch)
	if had {
		t.Cleanup(func() { _ = os.Setenv(envSwitch, old) }) //nolint:usetesting // restores prior value; t.Setenv cannot unset.
	} else {
		t.Cleanup(func() { _ = os.Unsetenv(envSwitch) })
	}
	if err := os.Unsetenv(envSwitch); err != nil {
		t.Fatalf("unset env: %v", err)
	}
	if got := sandbox.EnabledFromEnvDefault(false); got {
		t.Fatal("unset env should use provided default false")
	}
	if got := sandbox.EnabledFromEnvDefault(true); !got {
		t.Fatal("unset env should use provided default true")
	}
}
