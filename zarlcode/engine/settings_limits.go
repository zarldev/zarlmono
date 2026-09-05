package engine

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/prefs"
)

// Limits is the resolved run-budget configuration. Zero-valued budget fields
// resolve to runner defaults. SpawnMaxDepth defaults to one; the separate
// SpawnEnabled capability gate controls whether spawning is available at all.
type Limits struct {
	ReserveTokens        int // compactor headroom held back from the window
	MaxIterations        int // cap on the agent loop per turn
	SpawnMaxIterations   int // cap on sub-agent loop per agent_spawn call; 0 = inherit parent
	SpawnMaxDepth        int // sub-agent recursion ceiling; defaults to 1 when enabled
	SpawnAwaitTimeout    int // seconds one agent_await blocks; non-positive uses the tool default
	SpawnAwaitMaxTimeout int // maximum model-requested wait seconds; non-positive disables the ceiling
}

// Limits resolves the run-budget settings (effective scope).
func (s *Settings) Limits(ctx context.Context) Limits {
	return Limits{
		ReserveTokens:        s.intSetting(ctx, prefs.KeyReserveTokens, 0),
		MaxIterations:        s.intSetting(ctx, prefs.KeyMaxIterations, 0),
		SpawnMaxIterations:   s.intSetting(ctx, prefs.KeySpawnMaxIterations, 0),
		SpawnMaxDepth:        s.intSetting(ctx, prefs.KeySpawnMaxDepth, 1),
		SpawnAwaitTimeout:    s.intSetting(ctx, prefs.KeySpawnAwaitTimeout, 30),
		SpawnAwaitMaxTimeout: s.intSetting(ctx, prefs.KeySpawnAwaitMaxTimeout, 300),
	}
}

// SpawnModeConfig is the resolved policy for one sub-agent work mode.
type SpawnModeConfig struct {
	DefaultAgent  string
	DefaultTarget ProviderSpec
	MaxIterations int
}

// SpawnModesConfig is the complete typed per-mode sub-agent policy. Explicit
// fields make mode additions compile-visible instead of silently missing from
// string-keyed maps.
type SpawnModesConfig struct {
	Explore   SpawnModeConfig
	Verify    SpawnModeConfig
	Implement SpawnModeConfig
}

// SpawnModes resolves named-agent defaults, direct provider/model defaults, and
// per-mode child budgets while preserving the stable persisted preference keys.
func (s *Settings) SpawnModes(ctx context.Context) SpawnModesConfig {
	return SpawnModesConfig{
		Explore: SpawnModeConfig{
			DefaultAgent: strings.TrimSpace(s.setting(ctx, prefs.KeySpawnDefaultExploreAgent, "")),
			DefaultTarget: ProviderSpec{
				Name:  strings.TrimSpace(s.setting(ctx, prefs.KeySpawnDefaultExploreProvider, "")),
				Model: strings.TrimSpace(s.setting(ctx, prefs.KeySpawnDefaultExploreModel, "")),
			},
			MaxIterations: s.intSetting(ctx, prefs.KeySpawnExploreMaxIterations, 0),
		},
		Verify: SpawnModeConfig{
			DefaultAgent: strings.TrimSpace(s.setting(ctx, prefs.KeySpawnDefaultVerifyAgent, "")),
			DefaultTarget: ProviderSpec{
				Name:  strings.TrimSpace(s.setting(ctx, prefs.KeySpawnDefaultVerifyProvider, "")),
				Model: strings.TrimSpace(s.setting(ctx, prefs.KeySpawnDefaultVerifyModel, "")),
			},
			MaxIterations: s.intSetting(ctx, prefs.KeySpawnVerifyMaxIterations, 0),
		},
		Implement: SpawnModeConfig{
			DefaultAgent: strings.TrimSpace(s.setting(ctx, prefs.KeySpawnDefaultImplementAgent, "")),
			DefaultTarget: ProviderSpec{
				Name:  strings.TrimSpace(s.setting(ctx, prefs.KeySpawnDefaultImplementProvider, "")),
				Model: strings.TrimSpace(s.setting(ctx, prefs.KeySpawnDefaultImplementModel, "")),
			},
			MaxIterations: s.intSetting(ctx, prefs.KeySpawnImplementMaxIterations, 0),
		},
	}
}

// SpawnMaxConcurrent resolves the simultaneous child cap. Zero is unbounded.
func (s *Settings) SpawnMaxConcurrent(ctx context.Context) int {
	return s.intSetting(ctx, prefs.KeySpawnMaxConcurrent, 0)
}

// SpawnFallback resolves unresolved agent routing: planner, parent, or error.
func (s *Settings) SpawnFallback(ctx context.Context) string {
	switch value := strings.ToLower(strings.TrimSpace(s.setting(ctx, prefs.KeySpawnFallback, "planner"))); value {
	case "parent", "error":
		return value
	default:
		return "planner"
	}
}

// SpawnMaxRuntime resolves the maximum lifetime of each child task. Zero leaves
// child runtime unbounded; Group.Close remains the owner shutdown path.
func (s *Settings) SpawnMaxRuntime(ctx context.Context) time.Duration {
	seconds := s.intSetting(ctx, prefs.KeySpawnMaxRuntime, 0)
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

// ProcessLimits resolves the background-process manager knobs (effective
// scope), falling back to the built-in defaults when unset. Read once at
// startup when the ProcessManager is constructed.
func (s *Settings) ProcessLimits(ctx context.Context) (int, int) {
	maxAlive := s.intSetting(ctx, prefs.KeyMaxAliveProcesses, defaultMaxAliveProcesses)
	if maxAlive <= 0 {
		maxAlive = defaultMaxAliveProcesses
	}
	bufferLines := s.intSetting(ctx, prefs.KeyProcessOutputBuffer, defaultProcessOutputBuffer)
	if bufferLines <= 0 {
		bufferLines = defaultProcessOutputBuffer
	}
	return maxAlive, bufferLines
}

const (
	defaultMaxAliveProcesses   = 16
	defaultProcessOutputBuffer = 10000
)

// intSetting reads a setting as a non-negative int, returning def when unset
// or unparseable (the settings pane validates on entry, but be defensive).
func (s *Settings) intSetting(ctx context.Context, key string, def int) int {
	v := s.setting(ctx, key, "")
	if v == "" {
		return def
	}
	if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
		return n
	}
	return def
}

// DefaultModelSelection returns the provider paired with its configured
// default model. An empty model means the provider supplies its own runtime
// model and the workspace model override must be absent.
func (s *Settings) DefaultModelSelection(provider string) prefs.ModelSelection {
	model := ""
	if s != nil && s.Registry != nil {
		if def, err := s.Registry.Parse(provider); err == nil {
			model = def.DefaultModel
			if model == "" && len(def.SeedModels) > 0 {
				model = def.SeedModels[0]
			}
		}
	}
	return prefs.ModelSelection{Provider: provider, Model: model}
}
