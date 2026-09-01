package engine_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
)

func TestResolveSpawnMaxIterations(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                    string
		host, profile, fallback int
		want                    int
	}{
		{name: "host setting is authoritative", host: 80, profile: 12, fallback: 20, want: 80},
		{name: "profile without host setting", profile: 12, fallback: 20, want: 12},
		{name: "fallback without overrides", fallback: 30, want: 30},
		{name: "compiled default", want: 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := engine.ResolveSpawnMaxIterations(tc.host, tc.profile, tc.fallback); got != tc.want {
				t.Fatalf("ResolveSpawnMaxIterations(%d, %d, %d) = %d, want %d", tc.host, tc.profile, tc.fallback, got, tc.want)
			}
		})
	}
}
