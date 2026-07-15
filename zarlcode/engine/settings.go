package engine

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zarldev/zarlmono/zarlcode/home"
	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	backends "github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/modelsdev"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
	"github.com/zarldev/zarlmono/zkit/cache"
	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/oauth/codex"
	"github.com/zarldev/zarlmono/zkit/prefs"
	"github.com/zarldev/zarlmono/zkit/vault"
)

// DefaultSearxngURL is where a self-hosted SearXNG conventionally listens
// (docker/searxng/). 8080 is the SearXNG port — llama-server sits on 8081.
const DefaultSearxngURL = "http://127.0.0.1:8080"

// Settings bundles the persistence layer the TUI reads its configuration
// from: the sqlite store, the prefs funnel (plaintext settings + the
// vault-encrypted api_keys), and the provider registry. It's the SAME
// ~/.zarlcode/state.db + master.key the v1 shell uses, so preferences and
// stored credentials carry across both front-ends.
//
// Construct once at startup with OpenSettings; the settings overlay (later
// phase) reads and writes through the same handle.
type Settings struct {
	Store    *db.Store
	Svc      *prefs.Service
	Registry *backends.ProviderRegistry
	wsRoot   string

	// modelsDev is the live model-info source wired into Registry. The
	// warm goroutine (started by OpenSettings, cancelled in Close)
	// populates its cache off the hot path so the first cost lookup is
	// a cache hit rather than a blocking HTTP fetch.
	modelsDev  *modelsdev.Source
	warmCancel context.CancelFunc
	warmDone   chan struct{}
}

// providerKeyResolver adapts prefs.Service to the registry's tiny
// key-resolution interface, binding reads to effective scope (workspace
// then global) — the same precedence the runtime uses everywhere else.
type providerKeyResolver struct{ svc *prefs.Service }

func (r providerKeyResolver) GetKey(ctx context.Context, provider string) (string, bool, error) {
	if r.svc == nil {
		return "", false, nil
	}
	return r.svc.GetKey(ctx, prefs.ScopeEffective, provider)
}

// OpenSettings opens the shared state.db (applying migrations), loads the
// vault, and builds the prefs service + provider registry (seeded with the
// built-in providers + any persisted custom rows).
//
// A failed vault is non-fatal: plaintext settings still work and the
// service reports HasVault()==false, so key/OAuth-dependent rows degrade to
// "unavailable" rather than blocking startup. A failed store IS fatal —
// without it there's nowhere to read configuration from.
//
// passphrase is the interactive passphrase prompt; it may be nil for callers
// that rely on $ZARLCODE_KEY / $ZARLCODE_PASSPHRASE (headless / eval), or
// when no vault exists yet (a fresh install isn't prompted). When the vault
// opens with a legacy master.key still present, its credentials are migrated to
// the passphrase-derived key here, once, transparently.
func OpenSettings(ctx context.Context, wsRoot string, passphrase vault.PassphraseFunc) (*Settings, error) {
	store, err := db.Open(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("open state.db: %w", err)
	}
	probe := prefs.NewService(store, nil, wsRoot)
	legacyOff := false
	if sv, ok, err := probe.GetSetting(ctx, prefs.ScopeEffective, prefs.KeyVaultPrompt); err == nil && ok && sv.Value == "off" {
		legacyOff = true
	}
	hasVaultRows, err := probe.HasVaultBackedKeys(ctx)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	mode, err := probe.CredentialProtection(ctx)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	shouldOpenVault := os.Getenv("ZARLCODE_KEY") != "" || os.Getenv("ZARLCODE_PASSPHRASE") != "" || (hasVaultRows && !legacyOff) || mode == prefs.CredentialProtectionPassphrase
	var v *vault.Vault
	if shouldOpenVault {
		var verr error
		v, verr = vault.Open(passphrase)
		if verr != nil {
			// ErrUninitialised / ErrLocked are the expected "no usable vault"
			// signals; plaintext credentials still work. Encrypted rows surface as
			// ErrCredentialsLocked from the credential service.
			slog.WarnContext(ctx, "vault unavailable; encrypted credentials locked", "err", verr)
			v = nil
		}
	}
	s := NewSettings(ctx, store, v, wsRoot)
	s.startModelsDevWarm()
	if legacyOff {
		if n, derr := s.Svc.DisableCredentialProtection(ctx, passphrase); derr != nil {
			_ = store.Close()
			return nil, fmt.Errorf("disable credential protection: %w", derr)
		} else if n > 0 {
			slog.InfoContext(ctx, "disabled credential protection; decrypted stored credentials", "count", n)
		}
	}
	if v != nil {
		if n, merr := s.Svc.MigrateVaultKeys(ctx); merr != nil {
			slog.WarnContext(ctx, "vault key migration incomplete", "migrated", n, "err", merr)
		} else if n > 0 {
			slog.InfoContext(ctx, "migrated credentials to passphrase-derived key", "count", n)
		}
	}
	return s, nil
}

// NewSettings assembles a Settings from an already-open store, optional vault,
// and workspace root — the same prefs + provider-registry wiring OpenSettings
// performs after opening the db. Exposed so callers and tests can inject their
// own store/vault without going through the db.Open path.
func NewSettings(ctx context.Context, store *db.Store, v *vault.Vault, wsRoot string) *Settings {
	svc := prefs.NewService(store, v, wsRoot)
	src := newModelsDevSource()
	reg := backends.NewRegistry(
		backends.WithStore(store),
		backends.WithSettingsService(providerKeyResolver{svc: svc}),
		backends.WithModelsDevSource(src),
	)
	if err := reg.Reload(ctx); err != nil {
		slog.WarnContext(ctx, "provider registry reload (custom providers)", "err", err)
	}
	return &Settings{Store: store, Svc: svc, Registry: reg, wsRoot: wsRoot, modelsDev: src}
}

// newModelsDevSource builds a file-cached models.dev source. The cache
// lives under ~/.zarlcode/cache/modelsdev for cross-restart persistence;
// if that directory can't be resolved it downgrades to a per-process
// temp cache (NewFileCache never fails).
func newModelsDevSource() *modelsdev.Source {
	var store cache.Cache[string, modelsdev.Snapshot]
	if dir, err := home.CacheDir(); err == nil {
		store = cache.NewFileCache[string, modelsdev.Snapshot](
			cache.WithOSFileSystem[string, modelsdev.Snapshot](filepath.Join(dir, "modelsdev")),
		)
	} else {
		store = cache.NewFileCache[string, modelsdev.Snapshot]()
	}
	return modelsdev.New(store)
}

// startModelsDevWarm primes the models.dev snapshot cache off the hot
// path so the first ResolveCost / ResolveCapabilities lookup is a cache
// hit instead of a blocking HTTP fetch. The goroutine is owned by the
// Settings handle and cancelled in Close — not fire-and-forget. Called
// only from OpenSettings (real startup), never from the NewSettings
// injection seam, so tests don't reach for the network.
func (s *Settings) startModelsDevWarm() {
	if s == nil || s.modelsDev == nil {
		return
	}
	warmCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := s.modelsDev.Warm(warmCtx); err != nil {
			slog.WarnContext(warmCtx, "models.dev warm", "err", err)
		}
	}()
	s.warmCancel = cancel
	s.warmDone = done
}

// Close cancels the models.dev warm goroutine (if running) and releases
// the underlying store.
func (s *Settings) Close() error {
	if s == nil {
		return nil
	}
	if s.warmCancel != nil {
		s.warmCancel()
		<-s.warmDone
	}
	if s.Store == nil {
		return nil
	}
	return s.Store.Close()
}

// ConfirmQuit resolves the confirm_quit setting (effective scope). When unset or
// "on", the TUI shows a confirmation prompt before quitting. "off" disables it.
func (s *Settings) ConfirmQuit(ctx context.Context) bool {
	return s.setting(ctx, prefs.KeyConfirmQuit, "on") == "on"
}

// SudoAskpass resolves whether sudo -A integration should be exposed to bash
// subprocesses. Off by default: enabling it lets shell commands trigger a TUI
// password prompt via a private Unix socket.
func (s *Settings) SudoAskpass(ctx context.Context) bool {
	return s.setting(ctx, prefs.KeySudoAskpass, "off") == "on"
}

// ShellSandbox resolves whether bash subprocesses should run under the
// kernel-enforced workspace sandbox. On by default; "off" disables
// confinement entirely (subject to any explicit env override at launch).
func (s *Settings) ShellSandbox(ctx context.Context) bool {
	return s.setting(ctx, prefs.KeySandbox, "on") == "on"
}

// PlanFirst resolves whether the plan-first guardrail is armed — the first
// workspace-changing call in a task is then refused until update_plan has run.
// Off by default; recommended for weak / local models that skip planning.
func (s *Settings) PlanFirst(ctx context.Context) bool {
	return s.setting(ctx, prefs.KeyPlanFirst, "off") == "on"
}

// ReadBeforeWriteMode resolves the read-before-write guardrail mode. "off"
// disables it; "advisory" and "strict" both refuse blind edit/write calls
// until the task has first established local context.
func (s *Settings) ReadBeforeWriteMode(ctx context.Context) guardrails.ReadBeforeWriteMode {
	switch strings.ToLower(strings.TrimSpace(s.setting(ctx, prefs.KeyReadBeforeWrite, "off"))) {
	case "advisory":
		return guardrails.ReadBeforeWriteAdvisory
	case "strict":
		return guardrails.ReadBeforeWriteStrict
	default:
		return guardrails.ReadBeforeWriteOff
	}
}

// Temperature resolves the sampling temperature for completion requests. A
// zero return means "unset" — the runner leaves it off the request so the
// server's own default applies. A low value (e.g. 0.2) improves determinism
// for local models.
func (s *Settings) Temperature(ctx context.Context) float32 {
	v := strings.TrimSpace(s.setting(ctx, prefs.KeyTemperature, ""))
	if v == "" || v == "(default)" {
		return 0
	}
	f, err := strconv.ParseFloat(v, 32)
	if err != nil || f < 0 {
		return 0
	}
	return float32(f)
}

// ToolResultMaxBytes resolves the per-tool-result byte cap before tail
// truncation + spill, in bytes. Default 50 KB, matching the runner default.
func (s *Settings) ToolResultMaxBytes(ctx context.Context) int {
	return s.intSetting(ctx, prefs.KeyToolResultMaxKB, 50) * 1024
}

// ToolResultMaxLines resolves the per-tool-result line cap. Default 2000.
func (s *Settings) ToolResultMaxLines(ctx context.Context) int {
	return s.intSetting(ctx, prefs.KeyToolResultMaxLines, 2000)
}

// FanoutCap resolves the per-tool exploration fan-out cap. 0 keeps the built-in
// per-tool defaults; a positive value caps every exploration tool at that count.
func (s *Settings) FanoutCap(ctx context.Context) int {
	return s.intSetting(ctx, prefs.KeyFanoutCap, 0)
}

// EnableMCP / EnableWeb / EnableBackground gate optional tool clusters. All on
// by default; turn off to shrink the tool surface for a lean local-model setup.
func (s *Settings) EnableMCP(ctx context.Context) bool {
	return s.setting(ctx, prefs.KeyEnableMCP, "on") == "on"
}

func (s *Settings) EnableWeb(ctx context.Context) bool {
	return s.setting(ctx, prefs.KeyEnableWeb, "on") == "on"
}

func (s *Settings) EnableBackground(ctx context.Context) bool {
	return s.setting(ctx, prefs.KeyEnableBackground, "on") == "on"
}

func (s *Settings) ProgrammaticTools(ctx context.Context) bool {
	return s.setting(ctx, prefs.KeyProgrammaticTools, "off") == "on"
}

// ProgrammaticParallelCalls resolves the maximum number of nested program tool
// calls that call_many may run concurrently. 0 keeps the program package default.
func (s *Settings) ProgrammaticParallelCalls(ctx context.Context) int {
	return s.intSetting(ctx, prefs.KeyProgramParallel, 0)
}

// setting reads an effective-scope setting, returning def when unset or on
// error (config reads must never block startup).
func (s *Settings) setting(ctx context.Context, key, def string) string {
	if s == nil || s.Svc == nil {
		return def
	}
	if v, ok, err := s.Svc.GetSetting(ctx, prefs.ScopeEffective, key); err == nil && ok && v.Value != "" {
		return v.Value
	}
	return def
}

// Setting reads the effective value for key, falling back to def. It is the
// exported read accessor over the internal resolver, for callers and tests
// outside the engine package.
func (s *Settings) Setting(ctx context.Context, key, def string) string {
	return s.setting(ctx, key, def)
}

// SearxngURL resolves the web_search tool's SearXNG endpoint, mirroring the
// v1 precedence: the search_searxng_url setting (effective scope) → the
// SEARXNG_URL env var → the conventional local default. Never empty, so
// web_search is always wired; an unreachable endpoint fails the call with a
// friendly error rather than hiding the tool.
func (s *Settings) SearxngURL(ctx context.Context) string {
	if v := s.setting(ctx, prefs.KeySearxngURL, ""); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("SEARXNG_URL")); v != "" {
		return v
	}
	return DefaultSearxngURL
}

// ChromeBinPath returns the configured Chrome/Chromium binary path for the
// web_fetch tool's chromedp browser fallback (effective scope). Empty means
// chromedp auto-detects via the standard platform search paths.
func (s *Settings) ChromeBinPath(ctx context.Context) string {
	return strings.TrimSpace(s.setting(ctx, prefs.KeyChromeBinPath, ""))
}

// Editor returns the configured external editor command (effective scope), or
// "" when unset — the caller then falls back to $ZARLCODE_EDITOR / $VISUAL /
// $EDITOR, then vi. The value may carry flags, e.g. "code -w".
func (s *Settings) Editor(ctx context.Context) string {
	return strings.TrimSpace(s.setting(ctx, prefs.KeyEditor, ""))
}

// PprofAddr returns the optional pprof/runtime-metrics listen address. Empty
// disables the profiling HTTP server.
func (s *Settings) PprofAddr(ctx context.Context) string {
	return strings.TrimSpace(s.setting(ctx, prefs.KeyPprofAddr, ""))
}

// TraceFile returns the optional runtime trace output path. Empty disables
// full-run trace capture.
func (s *Settings) TraceFile(ctx context.Context) string {
	return strings.TrimSpace(s.setting(ctx, prefs.KeyTraceFile, ""))
}

// ActiveProvider resolves the active ProviderSpec from settings, falling
// back to fb (caller's env-derived defaults) for any unset field. The env
// BaseURL/APIKey overrides only apply to the fallback provider itself; for
// any other backend they'd be wrong (e.g. a local llama.cpp URL leaking onto
// openai), so other providers resolve those through the registry chain.
func (s *Settings) ActiveProvider(ctx context.Context, fb ProviderSpec) ProviderSpec {
	spec := ProviderSpec{
		Name:        s.resolveProvider(ctx, fb.Name),
		Model:       s.setting(ctx, prefs.KeyModel, fb.Model),
		CodexEffort: s.setting(ctx, prefs.KeyCodexEffort, fb.CodexEffort),
	}
	if spec.Name == fb.Name {
		spec.BaseURL = fb.BaseURL
		spec.APIKey = fb.APIKey
	}
	return spec
}

// resolveProvider returns the configured provider, but refuses to let the
// subprocess-spawning claude-code backend be a *default*: it's honoured only
// when explicitly pinned to this workspace, never when merely inherited from
// the global scope (e.g. carried over from the v1 shell on the shared
// state.db). This stops zarlcode-v2 from auto-running the `claude` CLI on
// launch just because v1 had it set; pick it for the workspace to use it.
func (s *Settings) resolveProvider(ctx context.Context, def string) string {
	if s == nil || s.Svc == nil {
		return def
	}
	sv, ok, err := s.Svc.GetSetting(ctx, prefs.ScopeEffective, prefs.KeyProvider)
	if err != nil || !ok || sv.Value == "" {
		return def
	}
	if id, _ := llm.ParseLLMProvider(sv.Value); id == backends.NameClaudeCode && sv.Source != prefs.ScopeWorkspace {
		slog.WarnContext(ctx, "ignoring inherited claude-code default; using local default — pin claude-code to this workspace to use it",
			"default", def, "source", sv.Source.String())
		return def
	}
	return sv.Value
}

// BuildActive resolves the active provider (settings over fb) and builds
// it, covering every backend method (registry for API-key providers,
// vault-backed token source for OAuth). Returns the built provider plus the
// resolved spec so the caller can label the UI with the real model.
func (s *Settings) BuildActive(ctx context.Context, fb ProviderSpec) (llm.Provider, ProviderSpec, error) {
	spec := s.ActiveProvider(ctx, fb)
	prov, err := BuildProvider(ctx, s.Registry, s.Svc, spec)
	return prov, spec, err
}

// ContextWindow resolves the compaction budget for the active provider.
// The ChatGPT-account Codex backend advertises model caps from /codex/models,
// including auto_compact_token_limit. Prefer that live value when OAuth is
// available; fall back to the registry's static table when the probe fails so
// startup and provider switching never block on it.
func (s *Settings) ContextWindow(ctx context.Context, spec ProviderSpec) int {
	if id, _ := llm.ParseLLMProvider(spec.Name); s != nil && s.Svc != nil && id == backends.NameOpenAICodex {
		if cw, err := openaicodex.FetchContextWindow(ctx, codex.NewTokenSource(s.Svc), spec.BaseURL, spec.Model); err == nil && cw > 0 {
			return cw
		}
	}
	if s == nil || s.Registry == nil {
		return 0
	}
	return s.Registry.ResolveContextWindow(ctx, spec.Name, spec.BaseURL, spec.Model)
}

// Theme resolves the configured theme name, or def when unset.
func (s *Settings) Theme(ctx context.Context, def string) string {
	return s.setting(ctx, prefs.KeyTheme, def)
}

// CompactEngine resolves the chosen compaction engine, defaulting to tiered
// (the quiet, no-LLM progressive trimmer) when unset.
func (s *Settings) CompactEngine(ctx context.Context) string {
	return s.setting(ctx, prefs.KeyCompactEngine, "tiered")
}

// CompactorProvider resolves the LLM target for the summary/executive
// compaction engines. The compact_provider / compact_model settings win when
// set (so daily work can run on a cheap model while briefings use a bigger
// one); otherwise it reuses the active provider + model. A build failure
// falls back to the active provider so a misconfigured override never breaks
// compaction.
func (s *Settings) CompactorProvider(ctx context.Context, active llm.Provider, activeModel string) (llm.Provider, string) {
	cp := s.setting(ctx, prefs.KeyCompactProvider, "")
	cm := s.setting(ctx, prefs.KeyCompactModel, "")
	if cm == "" {
		cm = activeModel
	}
	if cp == "" {
		return active, cm // reuse the active backend, optional model override
	}
	prov, err := BuildProvider(ctx, s.Registry, s.Svc, ProviderSpec{Name: cp, Model: cm})
	if err != nil || prov == nil {
		return active, activeModel
	}
	return prov, cm
}

// DecomposeJudgeProvider resolves the LLM target for the decompose
// guardrail's constrained-verdict judge. It returns nil while decompose_judge
// is off (the default) — the guardrail keeps its deterministic advisory path.
// When on, judge_provider / judge_model override the target the same way the
// compact_* pair does (verdicts want a small fast model); both unset reuses
// the active provider. A build failure falls back to the active provider so a
// misconfigured override never silently disables a judge the user enabled.
func (s *Settings) DecomposeJudgeProvider(ctx context.Context, active llm.Provider, activeSpec ProviderSpec) llm.Provider {
	if s.setting(ctx, prefs.KeyDecomposeJudge, "off") != "on" {
		return nil
	}
	jp := s.setting(ctx, prefs.KeyJudgeProvider, "")
	jm := s.setting(ctx, prefs.KeyJudgeModel, "")
	if jp == "" && jm == "" {
		return active
	}
	spec := activeSpec // model-only override keeps the active backend's URL/key
	if jp != "" {
		spec = ProviderSpec{Name: jp}
	}
	spec.Model = jm
	if jm == "" {
		spec.Model = activeSpec.Model
	}
	prov, err := BuildProvider(ctx, s.Registry, s.Svc, spec)
	if err != nil || prov == nil {
		slog.WarnContext(ctx, "decompose judge override unbuildable; reusing active provider",
			"judge_provider", jp, "judge_model", jm, "err", err)
		return active
	}
	return prov
}

// VerifyLoop resolves the headless verified re-drive configuration: the
// shell command that acts as the verification oracle (verify_tests) and the
// attempt cap (verify_attempts; default 1 = single-shot, loop off). The
// engine arms the loop only when the command is non-empty AND attempts > 1.
func (s *Settings) VerifyLoop(ctx context.Context) (string, int) {
	cmd := strings.TrimSpace(s.setting(ctx, prefs.KeyVerifyTests, ""))
	attempts := s.intSetting(ctx, prefs.KeyVerifyAttempts, 1)
	return cmd, attempts
}

// Limits is the resolved run-budget configuration. A zero field means
// "unset — use the runner's compiled-in default" (for SpawnMaxDepth, zero
// means spawning stays disabled — it's opt-in).
type Limits struct {
	ReserveTokens      int // compactor headroom held back from the window
	MaxIterations      int // cap on the agent loop per turn
	SpawnMaxIterations int // cap on sub-agent loop per spawn_agent call; 0 = inherit parent
	SpawnMaxDepth      int // sub-agent recursion ceiling; 0 = spawning disabled
}

// Limits resolves the run-budget settings (effective scope).
func (s *Settings) Limits(ctx context.Context) Limits {
	return Limits{
		ReserveTokens:      s.intSetting(ctx, prefs.KeyReserveTokens, 0),
		MaxIterations:      s.intSetting(ctx, prefs.KeyMaxIterations, 0),
		SpawnMaxIterations: s.intSetting(ctx, prefs.KeySpawnMaxIterations, 0),
		SpawnMaxDepth:      s.intSetting(ctx, prefs.KeySpawnMaxDepth, 0),
	}
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

// ResetModelToProviderDefault repoints the model setting at the given
// provider's default (its DefaultModel, else its first seed model), so
// switching provider can't strand a model from the previous backend (e.g.
// deepseek + opus). Clears the model when the provider has no default (local
// servers, where the model is whatever the server has loaded). Writes at
// workspace scope, matching where provider/model edits land.
func (s *Settings) ResetModelToProviderDefault(ctx context.Context, provider string) {
	if s == nil || s.Svc == nil {
		return
	}
	model := ""
	if s.Registry != nil {
		if def, err := s.Registry.Parse(provider); err == nil {
			model = def.DefaultModel
			if model == "" && len(def.SeedModels) > 0 {
				model = def.SeedModels[0]
			}
		}
	}
	if model == "" {
		if err := s.Svc.DeleteSetting(ctx, prefs.ScopeWorkspace, prefs.KeyModel); err != nil {
			slog.WarnContext(ctx, "reset model to provider default: clear workspace model", "err", err, "provider", provider)
		}
		return
	}
	if err := s.Svc.SetSetting(ctx, prefs.ScopeWorkspace, prefs.KeyModel, model); err != nil {
		slog.WarnContext(ctx, "reset model to provider default: write workspace model", "err", err, "provider", provider, "model", model)
	}
}
