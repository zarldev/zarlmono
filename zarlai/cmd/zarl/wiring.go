package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/zarldev/zarlmono/zarlai/repository"
	repoGen "github.com/zarldev/zarlmono/zarlai/repository/gen"
	"github.com/zarldev/zarlmono/zarlai/service"
	"github.com/zarldev/zarlmono/zarlai/subscribers"
	"github.com/zarldev/zarlmono/zarlai/taskrunner"
	transportgrpc "github.com/zarldev/zarlmono/zarlai/transport/grpc"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/skills"
)

// Repos bundles the database-backed repositories created during startup.
// Grouping keeps function signatures short — main() and the wiring
// helpers pass a single Repos value instead of a parameter wall.
type Repos struct {
	Prompt          *repository.PromptRepo
	Person          *repository.PersonRepo
	ToolCall        *repository.ToolCallRepo
	Settings        *repository.SettingsRepo
	Summary         *repository.ConversationSummaryRepo
	ToolProposal    *repository.ToolProposalRepo
	PromptProposal  *repository.PromptProposalRepo
	SensorProposal  *repository.SensorProposalRepo
	ToolProvider    *repository.ToolProviderRepo
	Task            *repository.TaskRepo
	Workspaces      *repository.WorkspaceRepo
	ToolDescription *repository.ToolDescriptionOverrideRepo
	Skill           *repository.SkillRepo
	SkillProposal   *repository.SkillProposalRepo
	PromptTemplate  *repository.PromptTemplateRepo
	ProfileOverride *repository.TaskProfileOverrideRepo
	Dolt            *repository.DoltRepo
}

// LLMs bundles the two chat clients main() builds — one for the live
// conversation path, one for the autonomous taskrunner. Separate HTTP
// clients give them independent connection pools so the two request
// streams never serialise at the Go layer.
type LLMs struct {
	Conversation *service.LlamaCppClient
	Task         *service.LlamaCppClient
	Embedder     service.Embedder
}

// mustOpenDB connects to Dolt or terminates. Returns the *sql.DB and
// the sqlc-generated query handle ready to feed Repos.
func mustOpenDB(dsn string) (*sql.DB, *repoGen.Queries) {
	db, err := repository.NewDB(dsn)
	if err != nil {
		slog.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	slog.Info("database connected", "dsn", dsn)
	return db, repoGen.New(db)
}

// buildRepos materialises every repo backed by a single sqlc handle.
// Cheap enough that we always build them all — the cost is one struct
// allocation per repo and the alternative (lazy construction at every
// call site) is messier.
func buildRepos(q *repoGen.Queries, db *sql.DB) Repos {
	return Repos{
		Prompt:          repository.NewPromptRepo(q),
		Person:          repository.NewPersonRepo(q),
		ToolCall:        repository.NewToolCallRepo(q),
		Settings:        repository.NewSettingsRepo(q),
		Summary:         repository.NewConversationSummaryRepo(q),
		ToolProposal:    repository.NewToolProposalRepo(q),
		PromptProposal:  repository.NewPromptProposalRepo(q),
		SensorProposal:  repository.NewSensorProposalRepo(q),
		ToolProvider:    repository.NewToolProviderRepo(q),
		Task:            repository.NewTaskRepo(q),
		Workspaces:      repository.NewWorkspaceRepo(q),
		ToolDescription: repository.NewToolDescriptionOverrideRepo(q),
		Skill:           repository.NewSkillRepo(q),
		SkillProposal:   repository.NewSkillProposalRepo(q),
		PromptTemplate:  repository.NewPromptTemplateRepo(q),
		ProfileOverride: repository.NewTaskProfileOverrideRepo(q),
		Dolt:            repository.NewDoltRepo(db),
	}
}

// buildLLMs constructs the conversation + taskrunner chat clients
// against the same llama-server (or whichever OpenAI-compatible
// endpoint cfg points at). Each gets its own *http.Client so request
// streams stay independent.
func buildLLMs(cfg Config, embedder service.Embedder) LLMs {
	convHTTP := &http.Client{Timeout: 0}
	taskHTTP := &http.Client{Timeout: 0}
	conv := service.NewLlamaCppClient(cfg.ChatURL, cfg.ChatModel, embedder,
		service.WithLlamaCppTemplate(service.Qwen3Template{}),
		service.WithLlamaCppReasoning(true),
		service.WithLlamaCppHTTPClient(convHTTP),
	)
	task := service.NewLlamaCppClient(cfg.TaskChatURL, cfg.TaskChatModel, embedder,
		service.WithLlamaCppTemplate(service.Qwen3Template{}),
		service.WithLlamaCppReasoning(true),
		service.WithLlamaCppHTTPClient(taskHTTP),
	)
	slog.Info("configured llm",
		"backend", "llama-server",
		"conversation_url", cfg.ChatURL,
		"conversation_model", cfg.ChatModel,
		"task_url", cfg.TaskChatURL,
		"task_model", cfg.TaskChatModel,
		"embed_base", cfg.EmbedURL,
		"embed_model", cfg.EmbedModel,
	)
	return LLMs{Conversation: conv, Task: task, Embedder: embedder}
}

// mustOpenSTT loads the sherpa-onnx Whisper bundle or terminates.
func mustOpenSTT(cfg Config) *service.WhisperTranscriber {
	t, err := service.NewWhisperTranscriber(service.WhisperConfig{
		Encoder: cfg.STTDir + "/small.en-encoder.int8.onnx",
		Decoder: cfg.STTDir + "/small.en-decoder.int8.onnx",
		Tokens:  cfg.STTDir + "/small.en-tokens.txt",
	})
	if err != nil {
		slog.Error("stt init", "error", err)
		os.Exit(1)
	}
	slog.Info("stt ready", "model", "whisper-small.en")
	return t
}

// mustOpenTTS loads every TTS engine that has a model bundle on disk and
// returns a controller pointing at the first one in priority order
// (Kokoro, then Supertonic). The active engine + per-engine voice are
// restored from settings later in startup. Kokoro is mandatory — boot
// fails if its bundle is missing. Supertonic is optional and degrades
// gracefully when the bundle isn't present.
func mustOpenTTS(cfg Config) *service.VoiceController {
	kokoro, err := service.NewKokoroSynthesizer(service.KokoroConfig{
		Model:   cfg.KokoroDir + "/model.onnx",
		Voices:  cfg.KokoroDir + "/voices.bin",
		Tokens:  cfg.KokoroDir + "/tokens.txt",
		DataDir: cfg.KokoroDir + "/espeak-ng-data",
		Speed:   1.1,
		Speaker: 0,
	})
	if err != nil {
		slog.Error("tts init kokoro", "error", err)
		os.Exit(1)
	}
	slog.Info("tts engine ready", "engine", "kokoro", "sample_rate", kokoro.SampleRate())

	engines := map[service.EngineName]service.VoiceEngine{
		service.EngineKokoro: kokoro,
	}
	if supertonic := tryOpenSupertonic(cfg); supertonic != nil {
		engines[service.EngineSupertonic] = supertonic
		slog.Info("tts engine ready", "engine", "supertonic", "sample_rate", supertonic.SampleRate())
	}

	ctrl, err := service.NewVoiceController(
		[]service.EngineName{service.EngineKokoro, service.EngineSupertonic},
		engines,
	)
	if err != nil {
		slog.Error("tts controller init", "error", err)
		os.Exit(1)
	}
	return ctrl
}

// tryOpenSupertonic returns nil when the Supertonic bundle isn't on
// disk — the engine is optional. Hard failures (corrupt model files)
// still log a warning so the operator notices.
func tryOpenSupertonic(cfg Config) *service.SupertonicSynthesizer {
	required := []string{
		cfg.SupertonicDir + "/duration_predictor.int8.onnx",
		cfg.SupertonicDir + "/text_encoder.int8.onnx",
		cfg.SupertonicDir + "/vector_estimator.int8.onnx",
		cfg.SupertonicDir + "/vocoder.int8.onnx",
		cfg.SupertonicDir + "/tts.json",
		cfg.SupertonicDir + "/unicode_indexer.bin",
		cfg.SupertonicDir + "/voice.bin",
	}
	for _, p := range required {
		if _, err := os.Stat(p); err != nil {
			return nil
		}
	}
	s, err := service.NewSupertonicSynthesizer(service.SupertonicConfig{
		DurationPredictor: required[0],
		TextEncoder:       required[1],
		VectorEstimator:   required[2],
		Vocoder:           required[3],
		TtsJson:           required[4],
		UnicodeIndexer:    required[5],
		VoiceStyle:        required[6],
		Speed:             1.0,
		Speaker:           0,
	})
	if err != nil {
		slog.Warn("supertonic init", "error", err)
		return nil
	}
	return s
}

// openFaceService loads dlib face recognition models. nil means
// recognition stays disabled — every consumer already handles that.
func openFaceService(cfg Config, persons *repository.PersonRepo) *service.FaceService {
	f, err := service.NewFaceService(cfg.FaceModelsDir, personsAdapter{repo: persons})
	if err != nil {
		slog.Warn("face recognition disabled", "error", err)
		return nil
	}
	slog.Info("face recognition ready", "models", cfg.FaceModelsDir)
	return f
}

// personsAdapter satisfies service.PersonStore against
// *repository.PersonRepo. The threshold + Person → service.FaceMatch
// translation lives here so the service layer doesn't have to import
// repository — face recognition's data needs are narrow (match,
// enroll, forget) and the consumer-side interface keeps it that way.
type personsAdapter struct{ repo *repository.PersonRepo }

func (a personsAdapter) Match(ctx context.Context, embedding []float32) (service.FaceMatch, error) {
	p, dist, err := a.repo.Match(ctx, embedding, repository.EuclideanMatchThreshold)
	if err != nil {
		return service.FaceMatch{}, err
	}
	return service.FaceMatch{Name: p.Name, Notes: p.Notes, Dist: dist}, nil
}

func (a personsAdapter) Enroll(ctx context.Context, name string, embeddings [][]float32, photo string) error {
	_, err := a.repo.Create(ctx, name, embeddings, photo)
	return err
}

func (a personsAdapter) Forget(ctx context.Context, name string) error {
	return a.repo.DeleteByName(ctx, name)
}

// restoreVoice replays persisted voice state across every loaded engine
// and selects the active one. Each engine has its own "voice.<engine>"
// key holding "speaker:speed", and "voice.engine" names which one is
// active. The legacy single "voice" key (Kokoro-only deploys) is
// migrated into "voice.kokoro" on first read.
func restoreVoice(ctx context.Context, settings *repository.SettingsRepo, ctrl *service.VoiceController) {
	migrateLegacyVoice(ctx, settings)

	for _, name := range ctrl.Engines() {
		v, err := settings.Get(ctx, "voice."+string(name))
		if err != nil || v == "" {
			continue
		}
		var speaker int
		var speed float32
		if _, err := fmt.Sscanf(v, "%d:%f", &speaker, &speed); err != nil {
			continue
		}
		if err := ctrl.TuneEngine(name, speaker, speed); err != nil {
			slog.Warn("restore engine voice", "engine", name, "error", err)
			continue
		}
		slog.Info("restored voice", "engine", name, "speaker", speaker, "speed", speed)
	}

	active, _ := settings.Get(ctx, "voice.engine")
	if active == "" {
		return
	}
	if err := ctrl.SwitchEngine(service.EngineName(active)); err != nil {
		slog.Warn("restore active engine", "engine", active, "error", err)
		return
	}
	slog.Info("restored active engine", "engine", active)
}

// migrateLegacyVoice copies the pre-multi-engine "voice" key (which
// only ever held Kokoro state) into "voice.kokoro" and clears the old
// key. No-op once migrated.
func migrateLegacyVoice(ctx context.Context, settings *repository.SettingsRepo) {
	legacy, err := settings.Get(ctx, "voice")
	if err != nil || legacy == "" {
		return
	}
	if existing, _ := settings.Get(ctx, "voice.kokoro"); existing != "" {
		_ = settings.Set(ctx, "voice", "")
		return
	}
	if err := settings.Set(ctx, "voice.kokoro", legacy); err != nil {
		slog.Warn("migrate legacy voice key", "error", err)
		return
	}
	_ = settings.Set(ctx, "voice", "")
	slog.Info("migrated legacy voice setting", "value", legacy)
}

// loadActivePrompt returns the persisted active prompt, falling back
// to the inline default when the prompts table is empty (fresh
// deployments before the onboarding wizard runs).
func loadActivePrompt(ctx context.Context, repo *repository.PromptRepo) string {
	const defaultPrompt = "You are zarl, a friendly conversational AI assistant. You can see the user through their camera and recognize faces. Keep your responses to 1-4 short sentences. Be natural and conversational."
	active, err := repo.GetActive(ctx)
	if err != nil {
		slog.Warn("no active prompt in database, using default", "error", err)
		return defaultPrompt
	}
	slog.Info("loaded prompt", "name", active.Name)
	return active.Content
}

// resolveAgentName picks the agent's spoken name. The settings row
// wins; AGENT_NAME in env bootstraps the row on a fresh deploy; the
// service default backs everything up.
func resolveAgentName(ctx context.Context, settings *repository.SettingsRepo) string {
	if stored, err := settings.Get(ctx, "agent_name"); err == nil && stored != "" {
		return stored
	}
	if env := strings.TrimSpace(os.Getenv("AGENT_NAME")); env != "" {
		_ = settings.Set(ctx, "agent_name", env)
		return env
	}
	return service.DefaultAgentName
}

// buildToolDescStore returns a memory-backed description store with
// the persisted operator overrides loaded. Wired into the registry's
// version bumper so downstream selector caches rebuild on writes.
func buildToolDescStore(ctx context.Context, repo *repository.ToolDescriptionOverrideRepo, registry *tools.Registry) *tools.MemoryDescriptionStore {
	store := tools.NewMemoryDescriptionStore()
	store.AddBumper(registry)
	overrides, err := repo.List(ctx)
	if err != nil {
		slog.Warn("load tool description overrides", "error", err)
		return store
	}
	seed := make(map[tools.ToolName]string, len(overrides))
	for _, o := range overrides {
		seed[tools.ToolName(o.Name)] = o.Description
	}
	store.Load(seed)
	slog.Info("tool description overrides loaded", "count", len(overrides))
	return store
}

// buildSkillStore returns the in-memory skill store plus a reload
// closure the admin handlers call after every skill write.
func buildSkillStore(ctx context.Context, repo *repository.SkillRepo) (*skills.MemorySkillStore, func(context.Context)) {
	store := skills.NewMemorySkillStore()
	reload := func(ctx context.Context) {
		rows, err := repo.ListEnabled(ctx)
		if err != nil {
			slog.Warn("load skills", "error", err)
			return
		}
		slim := make([]skills.Skill, len(rows))
		for i, r := range rows {
			binding := ""
			if r.ProfileBinding != nil {
				binding = *r.ProfileBinding
			}
			slim[i] = skills.Skill{
				ID:             r.ID,
				Name:           r.Name,
				Description:    r.Description,
				Markdown:       r.Markdown,
				ProfileBinding: binding,
			}
		}
		store.Load(slim)
		slog.Info("skills loaded", "count", len(slim))
	}
	reload(ctx)
	return store, reload
}

// buildTemplateStore registers code-default templates and layers DB
// overrides on top.
func buildTemplateStore(ctx context.Context, repo *repository.PromptTemplateRepo) *service.MemoryPromptTemplateStore {
	store := service.NewMemoryPromptTemplateStore()
	taskrunner.RegisterReportTemplates(store)
	subscribers.RegisterTemplates(store)
	rows, err := repo.List(ctx)
	if err != nil {
		slog.Warn("load prompt templates", "error", err)
		return store
	}
	seed := make(map[string]string, len(rows))
	for _, row := range rows {
		seed[row.Key] = row.Content
	}
	store.LoadOverrides(seed)
	slog.Info("prompt templates loaded", "count", len(rows))
	return store
}

// bootstrapTaskProviderFromEnv copies TASK_PROVIDER_* env vars into
// settings rows on first boot. Once an operator configures the runner
// from the admin UI (task_provider settings populated), this function
// is a no-op.
func bootstrapTaskProviderFromEnv(ctx context.Context, settings *repository.SettingsRepo) {
	if provider, _ := settings.Get(ctx, "task_provider"); provider != "" {
		return
	}
	for _, pair := range []struct{ env, key string }{
		{"TASK_PROVIDER", "task_provider"},
		{"TASK_PROVIDER_MODEL", "task_provider_model"},
		{"TASK_PROVIDER_BASE", "task_provider_base"},
		{"TASK_PROVIDER_KEY", "task_provider_key"},
	} {
		if v := os.Getenv(pair.env); v != "" {
			_ = settings.Set(ctx, pair.key, v)
		}
	}
}

// restoreTaskProvider applies the persisted task_provider settings to
// the live runner. Called once, after the runner is constructed but
// before Start, so the recovered task path always sees the operator's
// configured client.
func restoreTaskProvider(ctx context.Context, settings *repository.SettingsRepo, runner *taskrunner.Runner, embedder service.Embedder) {
	provider, err := settings.Get(ctx, "task_provider")
	if err != nil || provider == "" || provider == "ollama" {
		return
	}
	model, _ := settings.Get(ctx, "task_provider_model")
	baseURL, _ := settings.Get(ctx, "task_provider_base")
	apiKey, _ := settings.Get(ctx, "task_provider_key")
	if model == "" {
		return
	}
	chatClient := buildTaskChatClient(provider, baseURL, model, apiKey, embedder)
	if chatClient == nil {
		return
	}
	// Clear the factory along with the client — the default factory
	// only knows how to build llama-server clients, so per-profile
	// model overrides on a non-llama provider would otherwise dispatch
	// to a bogus server.
	runner.Reconfigure(
		taskrunner.WithChatClient(chatClient),
		taskrunner.WithChatFactory(nil),
	)
	slog.Info("restored task provider", "provider", provider, "model", model)
}

func buildTaskChatClient(provider, baseURL, model, apiKey string, embedder service.Embedder) service.ChatClient {
	switch provider {
	case "llamacpp":
		return service.NewLlamaCppClient(baseURL, model, embedder,
			service.WithLlamaCppTemplate(service.Qwen3Template{}),
			service.WithLlamaCppReasoning(true),
		)
	case "openai":
		return service.NewOpenAIClient(baseURL, apiKey, model)
	case "anthropic":
		return service.NewAnthropicClient(apiKey, model)
	}
	return nil
}

// restoreContextBudget applies the persisted task_context_budget on top
// of the runner's defaults. Quietly skipped when unset or unparseable.
func restoreContextBudget(ctx context.Context, settings *repository.SettingsRepo, runner *taskrunner.Runner) {
	budgetStr, err := settings.Get(ctx, "task_context_budget")
	if err != nil || budgetStr == "" {
		return
	}
	var b int
	if _, err := fmt.Sscanf(budgetStr, "%d", &b); err != nil || b <= 0 {
		return
	}
	runner.Reconfigure(taskrunner.WithContextBudget(b))
}

// restoreConversationLLM rebuilds the conversation LLM from persisted
// llm_provider/model/base_url/reasoning settings and hot-swaps it on
// the live ZarlServer. No-op on a fresh deploy.
func restoreConversationLLM(ctx context.Context, settings *repository.SettingsRepo, server *transportgrpc.ZarlServer, embedder service.Embedder) {
	provider, err := settings.Get(ctx, "llm_provider")
	if err != nil || provider == "" {
		return
	}
	model, _ := settings.Get(ctx, "llm_model")
	baseURL, _ := settings.Get(ctx, "llm_base_url")
	reasoning, _ := settings.Get(ctx, "llm_reasoning")
	reasoningEnabled := reasoning == "true"
	if model == "" {
		return
	}
	var restored service.LLM
	switch provider {
	case "ollama":
		restored = service.NewOllamaClient(baseURL, model, embedder,
			service.WithOllamaReasoning(reasoningEnabled))
	case "llamacpp":
		restored = service.NewLlamaCppClient(baseURL, model, embedder,
			service.WithLlamaCppTemplate(service.Qwen3Template{}),
			service.WithLlamaCppReasoning(reasoningEnabled),
			service.WithLlamaCppHTTPClient(&http.Client{Timeout: 0}),
		)
	}
	if restored == nil {
		return
	}
	server.Reconfigure(transportgrpc.WithLLM(restored))
	slog.Info("restored conversation llm", "provider", provider, "model", model, "reasoning", reasoningEnabled)
}

// lookupHAProvider returns the URL + token from the home_assistant
// tool_providers row, if one exists and is enabled. The same row feeds
// the REST tools (ha_get_state, ha_call_service, ha_list_entities) via
// the tool manager, so a single admin-side configuration covers both
// the tools and the WebSocket event stream.
func lookupHAProvider(ctx context.Context, repo *repository.ToolProviderRepo) (url, token string, ok bool) {
	providers, err := repo.List(ctx)
	if err != nil {
		slog.Warn("ha event stream disabled: list providers", "error", err)
		return "", "", false
	}
	for _, p := range providers {
		if p.Type != "home_assistant" || !p.Enabled {
			continue
		}
		u, t := p.Config["url"], p.Config["token"]
		if u == "" || t == "" {
			continue
		}
		return u, t, true
	}
	return "", "", false
}

// installSignalShutdown wires SIGINT/SIGTERM into a graceful HTTP
// shutdown. Returns immediately; the handler runs in its own
// goroutine and lives until a signal arrives or the process exits.
func installSignalShutdown(srv *http.Server) {
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		slog.Info("shutting down")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			slog.WarnContext(ctx, "server shutdown", "err", err)
		}
	}()
}
