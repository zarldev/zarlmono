// Package prefs owns zarlcode's persisted preference catalogue and exposes the
// generic scoped preference service used by application composition roots.
//
// Key strings are persisted in state.db and are therefore compatibility values:
// rename Go identifiers freely, but never change an existing string in place.
package prefs

import (
	"context"
	"errors"

	"github.com/zarldev/zarlmono/zkit/db"
	shared "github.com/zarldev/zarlmono/zkit/prefs"
	"github.com/zarldev/zarlmono/zkit/vault"
)

// Service adds zarlcode-owned preference transitions to the generic service.
type Service struct {
	*shared.Service
}

// Scope selects global, workspace, or effective preference resolution.
type Scope = shared.Scope

// SettingValue includes a resolved value and its source scope.
type SettingValue = shared.SettingValue

// ModelSelection is the application provider/model pair persisted together.
type ModelSelection struct {
	Provider string
	Model    string
}

var (
	// ScopeGlobal addresses preferences shared by every workspace.
	ScopeGlobal = shared.ScopeGlobal
	// ScopeWorkspace addresses preferences for the active workspace.
	ScopeWorkspace = shared.ScopeWorkspace
	// ScopeEffective resolves workspace preferences before global preferences.
	ScopeEffective = shared.ScopeEffective
)

const (

	// CredentialProtectionOff stores credentials as plaintext in state.db.
	CredentialProtectionOff = shared.CredentialProtectionOff
	// CredentialProtectionPassphrase encrypts credentials with the unlocked vault.
	CredentialProtectionPassphrase = shared.CredentialProtectionPassphrase
)

var (
	// ErrNotFound means no value exists at the requested scope.
	ErrNotFound = shared.ErrNotFound
	// ErrNoWorkspace means a workspace operation has no active workspace.
	ErrNoWorkspace = shared.ErrNoWorkspace
	// ErrInvalidScope means a write used the effective read-only scope.
	ErrInvalidScope = shared.ErrInvalidScope
	// ErrNoVault means passphrase protection was requested without an unlocked vault.
	ErrNoVault = shared.ErrNoVault
	// ErrCredentialsLocked means encrypted credentials require an unlocked vault.
	ErrCredentialsLocked = shared.ErrCredentialsLocked
)

// NewService creates zarlcode's scoped preference service.
func NewService(store *db.Store, v *vault.Vault, workspace string) *Service {
	return &Service{Service: shared.NewService(store, v, workspace)}
}

// SetModelSelection atomically persists a provider and optional model. An empty
// model deletes the scoped override so the provider default applies.
func (s *Service) SetModelSelection(ctx context.Context, scope Scope, selection ModelSelection) error {
	if selection.Provider == "" {
		return errors.New("settings: model selection requires provider")
	}
	changes := []shared.SettingChange{{Key: KeyProvider, Value: selection.Provider}}
	if selection.Model == "" {
		changes = append(changes, shared.SettingChange{Key: KeyModel, Delete: true})
	} else {
		changes = append(changes, shared.SettingChange{Key: KeyModel, Value: selection.Model})
	}
	return s.ApplySettings(ctx, scope, changes...)
}

const (
	// Persisted preference keys. Values are stable database compatibility strings.
	KeyProvider                      = "provider"
	KeyModel                         = "model"
	KeyAgent                         = "agent"
	KeyTemperature                   = "temperature"
	KeyTextVerbosity                 = "text_verbosity"
	KeyMaxIterations                 = "max_iterations"
	KeyResponseTimeout               = "response_timeout"
	KeyReserveTokens                 = "reserve_tokens"
	KeyToolResultMaxKB               = "tool_result_max_kb"
	KeyToolResultMaxLines            = "tool_result_max_lines"
	KeyCompactionMode                = "compaction_mode"
	KeyCompactEngine                 = "compact_engine"
	KeyCompactProvider               = "compact_provider"
	KeyCompactModel                  = "compact_model"
	KeySpawnEnabled                  = "spawn_enabled"
	KeySpawnMaxIterations            = "spawn_max_iterations"
	KeySpawnAwaitTimeout             = "spawn_await_timeout"
	KeySpawnAwaitMaxTimeout          = "spawn_await_max_timeout"
	KeySpawnMaxDepth                 = "spawn_max_depth"
	KeyFanoutCap                     = "fanout_cap"
	KeySpawnFanoutCap                = "spawn_fanout_cap"
	KeySpawnDefaultExploreAgent      = "spawn_default_explore_agent"
	KeySpawnDefaultVerifyAgent       = "spawn_default_verify_agent"
	KeySpawnDefaultImplementAgent    = "spawn_default_implement_agent"
	KeySpawnDefaultExploreProvider   = "spawn_default_explore_provider"
	KeySpawnDefaultExploreModel      = "spawn_default_explore_model"
	KeySpawnDefaultVerifyProvider    = "spawn_default_verify_provider"
	KeySpawnDefaultVerifyModel       = "spawn_default_verify_model"
	KeySpawnDefaultImplementProvider = "spawn_default_implement_provider"
	KeySpawnDefaultImplementModel    = "spawn_default_implement_model"
	KeySpawnExploreMaxIterations     = "spawn_explore_max_iterations"
	KeySpawnVerifyMaxIterations      = "spawn_verify_max_iterations"
	KeySpawnImplementMaxIterations   = "spawn_implement_max_iterations"
	KeySpawnMaxConcurrent            = "spawn_max_concurrent"
	KeySpawnMaxRuntime               = "spawn_max_runtime"
	KeySpawnFallback                 = "spawn_fallback"
	KeyProgramParallel               = "program_parallel_calls"
	KeyPlanFirst                     = "plan_first"
	KeyReadBeforeWrite               = "read_before_write"
	KeyTestEditGuard                 = "test_edit_guard"
	KeyImprovementGuard              = "improvement_guard"
	KeySkillHints                    = "skill_hints"
	KeyDecomposeJudge                = "decompose_judge"
	KeyJudgeProvider                 = "judge_provider"
	KeyJudgeModel                    = "judge_model"
	KeyShellGuard                    = "shell_guard"
	KeySandbox                       = "sandbox"
	KeyVerifyTests                   = "verify_tests"
	KeyVerifyAttempts                = "verify_attempts"
	KeyCredentialProtection          = "credential_protection" //nolint:gosec // Database setting name, not a credential.
	KeySudoAskpass                   = "sudo_askpass"
	KeyEnableWeb                     = "enable_web"
	KeyProgrammaticTools             = "programmatic_tools"
	KeyEnableMCP                     = "enable_mcp"
	KeyEnableBackground              = "enable_background"
	KeySearchProvider                = "search_provider"
	KeySearxngURL                    = "search_searxng_url"
	KeyChromeBinPath                 = "chrome_bin_path"
	KeyComputerBrowserVisible        = "computer_browser_visible"
	KeyEditor                        = "editor"
	KeyMaxAliveProcesses             = "max_alive_processes"
	KeyProcessOutputBuffer           = "process_output_buffer"
	KeyTheme                         = "theme"
	KeyConfirmQuit                   = "confirm_quit"
	KeyNotificationSounds            = "notification_sounds"
	KeyPprofAddr                     = "pprof_addr"
	KeyTraceFile                     = "trace_file"
	KeyCodexEffort                   = "codex_reasoning_effort"
	KeyUpgradeSource                 = "upgrade_source"
	KeyUpgradeRestart                = "upgrade_restart"
	KeyUpgradeDryRun                 = "upgrade_dry_run"
	KeyUpgradeBinPath                = "upgrade_bin_path"
)
