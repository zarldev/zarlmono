package prefs

// Setting keys are the stable string identifiers for rows in the settings
// table. They live here — charm-free, beside the Service that reads and
// writes them — so every front-end (the v1 shell, the v2 TUI, the CLI)
// shares one source of truth and can't drift on the same state.db.
//
// The values are the on-disk key strings; never change one without a
// migration, since existing databases key their rows by these exact
// strings.
const (
	KeyTheme           = "theme"
	KeyProvider        = "provider"
	KeyModel           = "model"
	KeyAgent           = "agent"
	KeyCompactEngine   = "compact_engine"
	KeyCompactProvider = "compact_provider"
	KeyCompactModel    = "compact_model"
	// KeyCompactionMode controls whether the runner compacts automatically.
	// "auto" (default) trims history under context pressure without asking;
	// "manual" leaves the history intact, warns in the cockpit when it crosses
	// the trigger, and waits for the user to compact on demand.
	KeyCompactionMode = "compaction_mode"
	// Decompose-judge rows: the constrained-verdict LLM judge the decompose
	// guardrail consults on repeated tool failures. Off by default — the
	// guardrail then keeps its deterministic advisory path. Provider/model
	// mirror the compact_* pair: unset reuses the active backend.
	KeyDecomposeJudge = "decompose_judge"
	KeyJudgeProvider  = "judge_provider"
	KeyJudgeModel     = "judge_model"
	// KeyPlanFirst arms the plan-first guardrail: the first workspace-changing
	// call in a task is refused until update_plan has run. Off by default;
	// turn it on for weak / local models that dive into edits before planning.
	KeyPlanFirst = "plan_first"
	// KeyReadBeforeWrite controls the read-before-write guardrail. "off" disables
	// it; "advisory" and "strict" both refuse blind edit/write calls until the
	// task reads the target or enough nearby context first.
	KeyReadBeforeWrite = "read_before_write"
	// KeyTestEditGuard controls the test-edit guardrail for interactive runs.
	// "off" (default) leaves test edits unchecked; "advisory" warns when the
	// model edits a test rather than the code under test; "strict" refuses.
	// Headless runs stay strict for eval determinism regardless of this value.
	KeyTestEditGuard = "test_edit_guard"
	// KeyImprovementGuard toggles the improvement-loop guardrail, which keeps
	// the agent working while its verifiers still report failure. "on" default.
	KeyImprovementGuard = "improvement_guard"
	// KeySkillHints toggles the skill-hint guardrail, which suggests a recovery
	// skill after a tool call keeps failing. "on" default.
	KeySkillHints = "skill_hints"
	// KeyShellGuard controls the static shell policy's leniency. "auto" (default)
	// follows the sandbox setting — strict when the sandbox is on, lenient when
	// off; "strict" and "lenient" pin the choice regardless of the sandbox.
	KeyShellGuard = "shell_guard"
	// KeyTemperature sets the sampling temperature on completion requests.
	// Empty/"(default)" leaves it unset (server default). A low value (e.g. 0.2)
	// improves determinism and tool-call reliability for local models.
	KeyTemperature = "temperature"
	// KeyToolResultMaxKB / KeyToolResultMaxLines cap how much of a tool result
	// joins the conversation before tail-truncation + spill. Defaults match the
	// runner (50 KB / 2000 lines); lower them for small-context local models.
	KeyToolResultMaxKB    = "tool_result_max_kb"
	KeyToolResultMaxLines = "tool_result_max_lines"
	// KeyFanoutCap overrides the per-tool exploration fan-out cap (ls/grep/glob).
	// 0 keeps the built-in per-tool defaults; a positive value caps every capped
	// exploration tool at that count to bound context growth.
	KeyFanoutCap = "fanout_cap"
	// KeyResponseTimeout overrides the stream-idle stall watchdog in whole
	// seconds: how long the runner waits with no chunk from the model before
	// cancelling the iteration. Default 90. Raise it for a slow local model or
	// connection that legitimately pauses longer than 90s between chunks.
	KeyResponseTimeout = "response_timeout"
	// KeyEnableMCP / KeyEnableWeb / KeyEnableBackground gate optional tool
	// clusters. On by default; turn off to shrink the tool surface for a lean
	// local-model setup (MCP tools, web_search/web_fetch, background-process
	// tools + bash background mode respectively).
	KeyEnableMCP           = "enable_mcp"
	KeyEnableWeb           = "enable_web"
	KeyEnableBackground    = "enable_background"
	KeyProgrammaticTools   = "programmatic_tools"
	KeyProgramParallel     = "program_parallel_calls"
	KeyPprofAddr           = "pprof_addr"
	KeyTraceFile           = "trace_file"
	KeySearxngURL          = "search_searxng_url"
	KeyEditor              = "editor"
	KeyReserveTokens       = "reserve_tokens"
	KeyMaxIterations       = "max_iterations"
	KeySpawnMaxIterations  = "spawn_max_iterations"
	KeySpawnMaxDepth       = "spawn_max_depth"
	KeyCodexEffort         = "codex_reasoning_effort" // Background-process limits for the bash process manager.
	KeyMaxAliveProcesses   = "max_alive_processes"
	KeyProcessOutputBuffer = "process_output_buffer"
	KeyVerifyTests         = "verify_tests"
	// KeyVerifyAttempts caps the headless verified re-drive loop: how many
	// agent attempts the verify_tests oracle may bounce back. 1 (default)
	// keeps single-shot; the loop arms at 2+ with a command set.
	KeyVerifyAttempts = "verify_attempts"
	KeyUpgradeSource  = "upgrade_source"
	KeyUpgradeRestart = "upgrade_restart"
	KeyUpgradeDryRun  = "upgrade_dry_run"
	KeyUpgradeBinPath = "upgrade_bin_path"
	// KeyKeybindings stores the user's TUI keybinding overrides as a
	// JSON-encoded map[string][]string under the global scope.
	KeyKeybindings = "keybindings"

	// KeyChromeBinPath is the absolute path to a Chrome or Chromium
	// binary used by the web_fetch tool's chromedp browser fallback.
	// When unset, chromedp searches the standard platform paths.
	KeyChromeBinPath = "chrome_bin_path"

	// KeyConfirmQuit toggles the quit-confirmation dialog. When "on" (the
	// default), ctrl+c shows a confirmation prompt before quitting.
	KeyConfirmQuit = "confirm_quit"

	// KeyCredentialProtection controls how provider credentials are stored.
	// "off" stores plaintext in state.db and never prompts. "passphrase"
	// encrypts rows with the vault and prompts when encrypted rows need unlock.
	KeyCredentialProtection = "credential_protection"

	// KeyVaultPrompt is a legacy key from a short-lived broken implementation.
	// Treat vault_prompt=off as a pending request to disable credential
	// protection, then delete it after migrating encrypted rows to plaintext.
	KeyVaultPrompt = "vault_prompt"

	// KeySudoAskpass toggles sudo -A integration for bash subprocesses. When
	// "on" the TUI exposes a private askpass socket and password popup.
	KeySudoAskpass = "sudo_askpass"

	// KeySandbox toggles kernel-enforced shell confinement for bash
	// subprocesses. When "on" (the default), bash runs under the workspace
	// sandbox when the host kernel supports it.
	KeySandbox = "sandbox"
)
