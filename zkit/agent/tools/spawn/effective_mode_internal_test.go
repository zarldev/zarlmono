package spawn

import "testing"

func TestEffectiveModePrecedence(t *testing.T) {
	t.Parallel()
	tool := &Tool{}
	cases := []struct {
		name     string
		args     Args
		profile  SpawnMode
		explicit bool
		want     SpawnMode
	}{
		{
			name:     "explicit overrides profile upward",
			args:     Args{Mode: string(SpawnModeImplement)},
			profile:  SpawnModeVerify,
			explicit: true,
			want:     SpawnModeImplement,
		},
		{
			name:     "explicit overrides profile downward",
			args:     Args{Mode: string(SpawnModeExplore)},
			profile:  SpawnModeImplement,
			explicit: true,
			want:     SpawnModeExplore,
		},
		{
			name:    "automatic router cannot escalate profile",
			args:    Args{Mode: string(SpawnModeImplement)},
			profile: SpawnModeVerify,
			want:    SpawnModeVerify,
		},
		{
			name:    "automatic router may tighten profile",
			args:    Args{Mode: string(SpawnModeExplore)},
			profile: SpawnModeImplement,
			want:    SpawnModeExplore,
		},
		{
			name:    "profile wins without router mode",
			args:    Args{},
			profile: SpawnModeVerify,
			want:    SpawnModeVerify,
		},
		{
			name: "router mode applies without profile",
			args: Args{Mode: string(SpawnModeExplore)},
			want: SpawnModeExplore,
		},
		{
			name:     "invalid explicit falls back to implement",
			args:     Args{Mode: "wat"},
			explicit: true,
			want:     SpawnModeImplement,
		},
		{
			name: "default implement fallback",
			args: Args{},
			want: SpawnModeImplement,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tool.effectiveMode(tc.args, tc.profile, tc.explicit); got != tc.want {
				t.Fatalf("effectiveMode() = %q, want %q", got, tc.want)
			}
		})
	}
}
