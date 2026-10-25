package prefs

// Application defaults are shared by runtime resolution and the settings UI.
// They apply only when a preference is absent; saved workspace/global values win.
const (
	// DefaultMCPToolExposure requires explicit activation of remote schemas.
	DefaultMCPToolExposure = "deferred"
	// DefaultReserveTokens is the context headroom retained for compaction.
	DefaultReserveTokens = 5120
	// DefaultMaxIterations bounds the main agent loop per turn.
	DefaultMaxIterations = 900
	// DefaultSpawnMaxIterations bounds a child without a per-mode override.
	DefaultSpawnMaxIterations = 300
	// DefaultSpawnExploreMaxIterations bounds an explore child.
	DefaultSpawnExploreMaxIterations = 80
	// DefaultSpawnVerifyMaxIterations bounds a verification child.
	DefaultSpawnVerifyMaxIterations = 80
	// DefaultSpawnImplementMaxIterations bounds an implementation child.
	DefaultSpawnImplementMaxIterations = 300
	// DefaultSpawnAwaitTimeout is the ordinary child wait in seconds.
	DefaultSpawnAwaitTimeout = 600
	// DefaultSpawnAwaitMaxTimeout caps a requested child wait in seconds.
	DefaultSpawnAwaitMaxTimeout = 600
	// DefaultSpawnMaxConcurrent caps simultaneous children per turn.
	DefaultSpawnMaxConcurrent = 20
	// DefaultSpawnMaxRuntime bounds each child's lifetime in seconds.
	DefaultSpawnMaxRuntime = 600
	// DefaultSpawnFanoutCap caps child launches per task.
	DefaultSpawnFanoutCap = 80
	// DefaultFanoutCap caps discovery calls per tool and task.
	DefaultFanoutCap = 80
	// DefaultProgramParallelCalls caps concurrent nested program calls.
	DefaultProgramParallelCalls = 16
	// DefaultResponseTimeout is the stream-idle watchdog in seconds.
	DefaultResponseTimeout = 600
	// DefaultToolResultMaxKB caps model-visible tool output in KiB before spilling.
	DefaultToolResultMaxKB = 512
	// DefaultToolResultMaxLines caps model-visible tool output lines before spilling.
	DefaultToolResultMaxLines = 3500

	// DefaultSpawnEnabled exposes delegation tools unless explicitly disabled.
	DefaultSpawnEnabled = "on"
	// DefaultPlanFirst requires a plan before the first workspace-changing call.
	DefaultPlanFirst = "on"
	// DefaultShellGuard leaves static shell policy disabled unless selected.
	DefaultShellGuard = "off"
	// DefaultSandbox leaves kernel shell confinement disabled unless selected.
	DefaultSandbox = "off"
	// DefaultSudoAskpass enables interactive sudo -A password prompts.
	DefaultSudoAskpass = "on"
	// DefaultNotificationSounds rings for run and plan-step completion.
	DefaultNotificationSounds = "all"
	// DefaultProvider selects the built-in Codex account backend at startup.
	DefaultProvider = "openai-codex"
	// DefaultModel selects the main coding model for the default provider.
	DefaultModel = "gpt-6-astra"
	// DefaultCodexEffort requests high reasoning when the model supports it.
	DefaultCodexEffort = "high"
	// DefaultDecomposeJudge enables LLM verdicts for repeatedly failing tools.
	DefaultDecomposeJudge = "on"
	// ActiveModelTarget explicitly inherits the active target instead of a built-in route.
	ActiveModelTarget = "(active)"
)

// ModelTargetDefault returns the model preference key and built-in route for a
// main-mode, compaction, judge, or per-mode spawn provider key. Other keys have no route.
func ModelTargetDefault(providerKey string) (string, ModelSelection) {
	switch providerKey {
	case KeyMainPlanProvider:
		return KeyMainPlanModel, ModelSelection{Provider: ActiveModelTarget, Model: ActiveModelTarget}
	case KeyMainBuildProvider:
		return KeyMainBuildModel, ModelSelection{Provider: ActiveModelTarget, Model: ActiveModelTarget}
	case KeyCompactProvider:
		return KeyCompactModel, ModelSelection{Provider: DefaultProvider, Model: "gpt-6-sol"}
	case KeyJudgeProvider:
		return KeyJudgeModel, ModelSelection{Provider: "deepseek", Model: "deepseek-v4-flash"}
	case KeySpawnDefaultExploreProvider:
		return KeySpawnDefaultExploreModel, ModelSelection{Provider: "deepseek", Model: "deepseek-v4-flash"}
	case KeySpawnDefaultVerifyProvider:
		return KeySpawnDefaultVerifyModel, ModelSelection{Provider: DefaultProvider, Model: "gpt-6-luna"}
	case KeySpawnDefaultImplementProvider:
		return KeySpawnDefaultImplementModel, ModelSelection{Provider: DefaultProvider, Model: "gpt-6-sol"}
	default:
		return "", ModelSelection{}
	}
}
