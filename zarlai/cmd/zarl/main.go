package main

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/profile"
	ztools "github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/zhttp"

	agentrunner "github.com/zarldev/zarlmono/zkit/agent/runner"
	zsensor "github.com/zarldev/zarlmono/zkit/agent/sensor"

	znotify "github.com/zarldev/zarlmono/zkit/znotify"

	"connectrpc.com/connect"

	zarl "github.com/zarldev/zarlmono/zarlai"
	"github.com/zarldev/zarlmono/zarlai/events"
	"github.com/zarldev/zarlmono/zarlai/repository"
	"github.com/zarldev/zarlmono/zarlai/service"
	"github.com/zarldev/zarlmono/zarlai/subscribers"
	"github.com/zarldev/zarlmono/zarlai/taskrunner"
	"github.com/zarldev/zarlmono/zarlai/tools"
	"github.com/zarldev/zarlmono/zarlai/tools/homeassistant"
	transportgrpc "github.com/zarldev/zarlmono/zarlai/transport/grpc"
	"github.com/zarldev/zarlmono/zarlai/transport/grpc/gen/zarl/v1/zarlv1connect"
	"github.com/zarldev/zarlmono/zkit/mcp"
)

func registerTools(registry *ztools.Registry, toolList ...ztools.Tool) error {
	for _, tool := range toolList {
		if err := registry.Register(tool); err != nil {
			return fmt.Errorf("register %s: %w", tool.Definition().Name, err)
		}
	}
	return nil
}

func main() {
	ctx := context.Background()
	cfg := LoadConfig()
	if cfg.ChatURL == "" {
		slog.Error("CHAT_URL is required (any OpenAI-compatible /v1 endpoint)")
		os.Exit(1)
	}

	// --- Storage ---
	db, queries := mustOpenDB(cfg.DoltDSN)
	defer db.Close()
	repos := buildRepos(queries, db)

	// --- Inference: chat + embed + STT + TTS + face ---
	embedder := service.NewOpenAIEmbedder(cfg.EmbedURL, cfg.EmbedModel)
	llms := buildLLMs(cfg, embedder)
	transcriber := mustOpenSTT(cfg)
	defer transcriber.Close()
	synthesizer := mustOpenTTS(cfg)
	defer synthesizer.Close()
	faceService := openFaceService(cfg, repos.Person)
	if faceService != nil {
		defer faceService.Close()
	}

	// --- Restore persisted runtime settings ---
	restoreVoice(ctx, repos.Settings, synthesizer)
	systemPrompt := loadActivePrompt(ctx, repos.Prompt)
	agentName := resolveAgentName(ctx, repos.Settings)
	slog.Info("agent name", "name", agentName)

	// --- Tool registry + admin-editable description overrides ---
	notifications := znotify.NewNotificationStore()
	registry := ztools.NewRegistry()
	descStore := buildToolDescStore(ctx, repos.ToolDescription, registry)
	registry.SetDescriptionStore(descStore)

	// --- Skills + selector ---
	skillStore, _ := buildSkillStore(ctx, repos.Skill)
	skillSelector := service.NewSkillSelector(skillStore, embedder,
		service.WithSkillSelectorTopK(3),
	)

	// --- Operator-editable templates ---
	templateStore := buildTemplateStore(ctx, repos.PromptTemplate)

	// --- DB-configured tool providers (HA, MCP, memory, searxng, …) ---
	toolMgr := transportgrpc.NewToolManager(registry, repos.ToolProvider, repos.Settings, embedder, notifications)
	if err := toolMgr.InitAll(ctx); err != nil {
		slog.WarnContext(ctx, "init tool providers", "err", err)
	}

	if err := registerTools(registry,
		tools.NewTimeTool(),
		tools.NewRenderChartTool(notifications),
		tools.NewGestureTool(notifications),
		taskrunner.NewPresentFindingsTool(notifications),
	); err != nil {
		slog.Error("register always-on tools", "err", err)
		os.Exit(1)
	}

	// --- Event bus + session-end subscribers ---
	bus := events.New(64)
	summaryStore := subscribers.NewRepoSummaryStore(repos.Summary)
	memoryStore := subscribers.NewQdrantMemoryStore(toolMgr.QdrantClient(), embedder)
	bus.Register(events.SessionEnded, subscribers.NewSummarizer(llms.Task, summaryStore, templateStore))
	bus.Register(events.SessionEnded, subscribers.NewExtractor(llms.Task, memoryStore, templateStore))
	bus.Register(events.SessionEnded, subscribers.NewPersonPageKeeper(registry))
	bus.Start(ctx)
	defer bus.Stop()

	// --- Action tools (taskrunner-only verbs the live conversation also exposes) ---
	convLock := agentrunner.NewConversationLock()
	storeMemTool := taskrunner.NewStoreMemoryTool(memoryStore, taskrunner.WithStoreMemoryBus(bus))
	spawnTool := taskrunner.NewSpawnTaskTool(repos.Task, nil)
	adjustTool := taskrunner.NewAdjustScheduleTool(repos.Task)
	notifyTool := taskrunner.NewNotifyUserTool(notifications)
	proposeTool := taskrunner.NewProposeToolTool(repos.ToolProposal,
		taskrunner.WithMCPEndpointValidator(func(ctx context.Context, url string) error {
			probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			_, err := mcp.NewClient(url, "").Discover(probeCtx)
			return err
		}),
		taskrunner.WithProposeToolBus(bus),
	)
	requestPromptTool := taskrunner.NewRequestPromptUpdateTool(
		repos.Prompt, repos.PromptProposal,
		taskrunner.WithRequestPromptUpdateBus(bus),
	)
	readPromptTool := taskrunner.NewReadSystemPromptTool(repos.Prompt)
	findingsTool := taskrunner.NewPresentFindingsTool(notifications)
	proposeSensorTool := taskrunner.NewProposeSensorTool(sensorProposalStore{repos.SensorProposal}, registry)
	proposeSkillTool := taskrunner.NewProposeSkillTool(skillProposalAdapter{repo: repos.SkillProposal})
	// rawActionTools — what AdminServer's description editor renders.
	// actionTools — the wrapped set the runner sees, where description
	// reads pass through descStore so admin edits land on the next turn.
	rawActionTools := []ztools.Tool{storeMemTool, spawnTool, adjustTool, notifyTool, proposeTool, requestPromptTool, readPromptTool, findingsTool, proposeSensorTool, proposeSkillTool}
	actionTools := ztools.WrapDescriptionOverrides(rawActionTools, descStore)

	// --- Profile registry + chat-client factory for per-profile models ---
	// Persona/execution resolution is the shared zkit profile registry;
	// tool gating happens at the taskrunner's registry edge.
	envTaskModel := cfg.TaskChatModel
	profileRegistry := taskrunner.NewProfileRegistry(
		profile.NewRegistry(profile.Builtin(), profileOverrideAdapter{repo: repos.ProfileOverride}, envTaskModel),
		taskrunner.BuiltinToolGates(),
		toolNamesOverrideAdapter{repo: repos.ProfileOverride},
		registry,
		actionTools,
	)
	chatClientFactory := func(model string) service.ChatClient {
		if model == "" || model == envTaskModel {
			return llms.Task
		}
		return service.NewLlamaCppClient(cfg.TaskChatURL, model, embedder,
			service.WithLlamaCppTemplate(service.Qwen3Template{}),
			service.WithLlamaCppReasoning(true),
			service.WithLlamaCppHTTPClient(&http.Client{Timeout: 0}),
		)
	}

	// --- Taskrunner ---
	taskRunner := taskrunner.NewRunner(
		taskrunner.Config{
			Tasks:         repos.Task,
			ToolCallRepo:  repos.ToolCall,
			Notifications: notifications,
			Qdrant:        toolMgr.QdrantClient(),
			Embedder:      embedder,
			ConvLock:      convLock,
		},
		taskrunner.WithChatClient(llms.Task),
		taskrunner.WithChatFactory(chatClientFactory),
		taskrunner.WithProfiles(profileRegistry),
		taskrunner.WithRegistry(registry),
		taskrunner.WithActionTools(actionTools),
		taskrunner.WithSystemPrompt(systemPrompt),
		taskrunner.WithAgentName(agentName),
		taskrunner.WithContextBudget(40000),
		taskrunner.WithBus(bus),
		taskrunner.WithSkillSelector(skillSelector),
		taskrunner.WithPromptTemplates(templateStore),
		taskrunner.WithCoderToolFactory(taskrunner.NewCoderToolFactory()),
		taskrunner.WithWorkspaces(repos.Workspaces),
	)
	spawnTool.SetEnqueuer(taskRunner) // close the construction cycle
	defer taskRunner.Stop()

	// Apply persisted overrides BEFORE Start — otherwise a recovered
	// pending task would dispatch via the default factory before the
	// operator's configured provider lands.
	bootstrapTaskProviderFromEnv(ctx, repos.Settings)
	restoreTaskProvider(ctx, repos.Settings, taskRunner, embedder)
	restoreContextBudget(ctx, repos.Settings, taskRunner)
	taskRunner.Start(ctx)

	// --- Scheduler (cron-driven recurring tasks) ---
	scheduler := taskrunner.NewScheduler(repos.Task, taskRunner)
	if err := scheduler.Start(ctx); err != nil {
		slog.WarnContext(ctx, "scheduler start", "err", err)
	}
	defer scheduler.Stop()

	// --- Home Assistant event stream + sensor runner ---
	var haEventStream *homeassistant.EventStream
	if haURL, haToken, ok := lookupHAProvider(ctx, repos.ToolProvider); ok {
		stream, err := homeassistant.NewEventStream(haURL, haToken)
		if err != nil {
			slog.Warn("ha event stream disabled", "error", err)
		}
		if err == nil {
			haEventStream = stream
			haEventStream.Start(ctx)
			defer haEventStream.Stop()
		}
	}
	sensorRunner := zsensor.New()
	sensorController := NewSensorController(sensorRunner, registry, haEventStream, toolMgr)
	sensors := buildSensors(ctx, sensorController, notifications, repos.SensorProposal, toolMgr)
	sensors.Start(ctx)
	defer sensors.Stop()

	if err := registerTools(registry,
		tools.NewStartTaskTool(repos.Task, taskRunner, repos.Workspaces),
		tools.NewTaskStatusTool(repos.Task),
		tools.NewScheduleTaskTool(repos.Task, scheduler),
		proposeTool,
		requestPromptTool,
		readPromptTool,
		proposeSensorTool,
		proposeSkillTool,
	); err != nil {
		slog.Error("register runner tools", "err", err)
		os.Exit(1)
	}

	// --- Tool selector (registry now fully populated) ---
	// topN deliberately high — full catalog ships every turn. The
	// ranked-selection variant missed tools whose descriptions didn't
	// embed close to the user's phrasing, which made the model
	// hallucinate "I don't have that capability" while the tool was
	// right there. Worth the extra tokens.
	toolSelector := service.NewToolSelector(registry, embedder,
		service.WithToolSelectorTopN(1000),
	)

	// --- ZarlServer (the live conversation handler) ---
	const convBudget = 28000 // ~4k headroom within 32k per-slot llama.cpp context (q8_0 KV, -c 65536, -np 2)
	zarlServer := transportgrpc.NewZarlServer(transcriber, synthesizer, faceService, registry, notifications, toolMgr.QdrantClient(), repos.ToolCall, convLock, repos.Summary, bus,
		transportgrpc.WithLLM(llms.Conversation),
		transportgrpc.WithSystemPrompt(systemPrompt),
		transportgrpc.WithAgentName(agentName),
		transportgrpc.WithSettings(repos.Settings),
		transportgrpc.WithConvContextBudget(convBudget),
		transportgrpc.WithToolSelector(toolSelector),
		transportgrpc.WithSkillSelector(skillSelector),
	)
	restoreConversationLLM(ctx, repos.Settings, zarlServer, embedder)

	// --- HTTP mux ---
	mux := http.NewServeMux()
	path, handler := zarlv1connect.NewZarlServiceHandler(
		zarlServer,
		connect.WithInterceptors(loggingInterceptor()),
	)
	mux.Handle(path, handler)

	bus.Register(events.ToolProposed, subscribers.NewNotifier(notifications))

	adminServer := transportgrpc.NewAdminServer(transportgrpc.AdminConfig{
		Prompts:                  repos.Prompt,
		Persons:                  repos.Person,
		Providers:                repos.ToolProvider,
		ToolCalls:                repos.ToolCall,
		Tasks:                    repos.Task,
		Settings:                 repos.Settings,
		Proposals:                repos.ToolProposal,
		PromptProposals:          repos.PromptProposal,
		Summaries:                repos.Summary,
		SensorProposals:          repos.SensorProposal,
		Dolt:                     repos.Dolt,
		Workspaces:               repos.Workspaces,
		Registry:                 registry,
		ZarlServer:               zarlServer,
		ToolManager:              toolMgr,
		Synthesizer:              synthesizer,
		Runner:                   taskRunner,
		Embedder:                 embedder,
		Notifications:            notifications,
		ProfileRegistry:          profileRegistry,
		ProfileOverrides:         repos.ProfileOverride,
		SensorActivator:          sensorController,
		Qdrant:                   toolMgr.QdrantClient(),
		ToolDescriptionOverrides: repos.ToolDescription,
		ToolDescriptionStore:     descStore,
		ActionTools:              rawActionTools,
		Skills:                   repos.Skill,
		SkillProposals:           repos.SkillProposal,
		SkillStore:               skillStore,
		PromptTemplates:          repos.PromptTemplate,
		PromptTemplateStore:      templateStore,
	})
	adminPath, adminHandler := zarlv1connect.NewAdminServiceHandler(adminServer)
	mux.Handle(adminPath, adminHandler)

	// --- Embedded frontend with SPA fallback ---
	distFS, err := fs.Sub(zarl.FrontendFS, "frontend/dist")
	if err != nil {
		slog.Error("embed frontend", "error", err)
		os.Exit(1)
	}
	mux.Handle("/", spaHandler(http.FileServer(http.FS(distFS))))

	// --- HTTP serve ---
	// zhttp.NewServer applies safe timeout defaults (incl. ReadHeaderTimeout,
	// which fends off slowloris). Read/Write timeouts are zeroed because
	// ZarlService.Converse is server-streaming — it holds the request body
	// open for audio uploads and streams TTS/LLM chunks back for as long as a
	// turn runs; a finite read/write deadline would sever live conversations.
	srv := zhttp.NewServer(":"+cfg.Port, mux,
		zhttp.WithServerReadTimeout(0),
		zhttp.WithServerWriteTimeout(0),
	)
	// Serve HTTP/1.1 (SPA) and cleartext HTTP/2 (h2c, for the ConnectRPC gRPC
	// clients) on the same listener via the stdlib http.Protocols mechanism
	// (the deprecated h2c.NewHandler wrapper's modern replacement).
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	srv.Protocols = protocols
	installSignalShutdown(srv)
	slog.Info("listening", "port", cfg.Port)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}

// profileOverrideAdapter bridges repository.TaskProfileOverrideRepo to
// profile.OverrideStore (persona/execution fields) at the wiring
// boundary. The same DB row's ToolNames feed the tool gate via
// toolNamesOverrideAdapter — one row, two consumers.
type profileOverrideAdapter struct {
	repo *repository.TaskProfileOverrideRepo
}

func (a profileOverrideAdapter) Get(ctx context.Context, name profile.Name) (profile.Override, error) {
	o, err := a.repo.Get(ctx, string(name))
	if err != nil {
		return profile.Override{}, err
	}
	var maxIter *int32
	if o.MaxIterations != nil {
		v := *o.MaxIterations
		maxIter = &v
	}
	return profile.Override{
		Model:         o.Model,
		PromptPrefix:  o.PromptPrefix,
		MaxIterations: maxIter,
	}, nil
}

// toolNamesOverrideAdapter exposes the same override row's ToolNames to
// the taskrunner's registry-level tool gate.
type toolNamesOverrideAdapter struct {
	repo *repository.TaskProfileOverrideRepo
}

func (a toolNamesOverrideAdapter) ToolNames(ctx context.Context, name profile.Name) ([]ztools.ToolName, error) {
	o, err := a.repo.Get(ctx, string(name))
	if err != nil {
		return nil, err
	}
	if len(o.ToolNames) == 0 {
		return nil, nil
	}
	names := make([]ztools.ToolName, len(o.ToolNames))
	for i, n := range o.ToolNames {
		names[i] = ztools.ToolName(n)
	}
	return names, nil
}

// spaHandler serves static files and falls back to index.html for SPA routes.
func spaHandler(fileServer http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, err := zarl.FrontendFS.Open("frontend/dist" + r.URL.Path)
		if err == nil {
			_ = f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}

// skillProposalAdapter bridges the taskrunner's SkillProposalStore
// interface to *repository.SkillProposalRepo. Same pattern as
// sensorProposalStore — taskrunner can't import repository (circular),
// so the concrete adapter lives in main.
type skillProposalAdapter struct{ repo *repository.SkillProposalRepo }

func (a skillProposalAdapter) CreateSkillProposal(ctx context.Context, in taskrunner.SkillProposalInput) error {
	p := repository.SkillProposal{
		ProposedName:        in.Name,
		ProposedDescription: in.Description,
		ProposedMarkdown:    in.Markdown,
		Rationale:           in.Rationale,
	}
	if in.TargetSkillID != "" {
		tid := in.TargetSkillID
		p.TargetSkillID = &tid
	}
	if in.Binding != "" {
		b := in.Binding
		p.ProposedBinding = &b
	}
	_, err := a.repo.Create(ctx, p)
	return err
}

// sensorProposalStore adapts *repository.SensorProposalRepo to the
// taskrunner.SensorProposalStore interface. The adapter exists only to
// translate between service.Arguments (agent-facing) and map[string]any
// (repository-facing) — the repo package can't import service because
// service already imports repository (face recognition path).
type sensorProposalStore struct {
	repo *repository.SensorProposalRepo
}

func (s sensorProposalStore) CreatePollProposal(ctx context.Context, toolName string, args service.Arguments, intervalSeconds int, rationale string) (string, error) {
	p, err := s.repo.CreatePoll(ctx, toolName, map[string]any(args), intervalSeconds, rationale)
	if err != nil {
		return "", err
	}
	return p.ID, nil
}

func (s sensorProposalStore) CreateHassStateProposal(ctx context.Context, entityID, rationale string) (string, error) {
	p, err := s.repo.CreateHassState(ctx, entityID, rationale)
	if err != nil {
		return "", err
	}
	return p.ID, nil
}

func (s sensorProposalStore) CreateMcpNotificationProposal(ctx context.Context, provider, method, rationale string) (string, error) {
	p, err := s.repo.CreateMcpNotification(ctx, provider, method, rationale)
	if err != nil {
		return "", err
	}
	return p.ID, nil
}

func loggingInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			slog.InfoContext(ctx, "rpc", "procedure", req.Spec().Procedure)
			resp, err := next(ctx, req)
			if err != nil {
				slog.ErrorContext(ctx, "rpc error", "procedure", req.Spec().Procedure, "error", err)
			}
			return resp, err
		}
	}
}

// 1776382887
// force restart 1776383574
// reload prompt 1776383660
// deploy trigger 1776386203
// chart deploy 1776386793
// chart y-axis fix 1776387542
// chart render 1776387747
// markdown deploy 1776388035
// strip pseudo tags 1776388241
// regex fix 1776388437
// kick 1776467578
