package grpc

import (
	"context"
	"fmt"

	znotify "github.com/zarldev/zarlmono/zkit/znotify"

	"github.com/zarldev/zarlmono/zarlai/repository"
	"github.com/zarldev/zarlmono/zarlai/service"
	"github.com/zarldev/zarlmono/zarlai/taskrunner"
	"github.com/zarldev/zarlmono/zarlai/transport/grpc/gen/zarl/v1/zarlv1connect"
	"github.com/zarldev/zarlmono/zkit/agent/profile"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/skills"
	"github.com/zarldev/zarlmono/zkit/vectorstore/qdrant"
)

var _ zarlv1connect.AdminServiceHandler = (*AdminServer)(nil)

type AdminServer struct {
	prompts          *repository.PromptRepo
	persons          *repository.PersonRepo
	providers        *repository.ToolProviderRepo
	toolCalls        *repository.ToolCallRepo
	tasks            *repository.TaskRepo
	settings         *repository.SettingsRepo
	proposals        *repository.ToolProposalRepo
	promptProposals  *repository.PromptProposalRepo
	registry         *tools.Registry
	zarlServer       *ZarlServer
	toolManager      *ToolManager
	synthesizer      voiceController
	runner           *taskrunner.Runner
	dolt             *repository.DoltRepo
	embedder         service.Embedder
	notifications    *znotify.NotificationStore
	profileRegistry  profileLister
	profileOverrides profileOverrideStore
	summaries        *repository.ConversationSummaryRepo
	sensorProposals  *repository.SensorProposalRepo
	sensorActivator  SensorActivator
	qdrantClient     *qdrant.Client
	// Tool description overrides — persisted via toolDescRepo, cached
	// in-memory via toolDescStore. Writes go to both (DB for durability,
	// store for the hot lookup path). actionTools is a separate slice
	// because those tools live outside the Registry.
	toolDescRepo    *repository.ToolDescriptionOverrideRepo
	toolDescStore   *tools.MemoryDescriptionStore
	actionTools     []tools.Tool
	skills          *repository.SkillRepo
	skillProposals  *repository.SkillProposalRepo
	skillStore      *skills.MemorySkillStore
	promptTemplates *repository.PromptTemplateRepo
	templateStore   *service.MemoryPromptTemplateStore
	workspaces      *repository.WorkspaceRepo
}

// profileLister is the subset of taskrunner.ProfileRegistry that AdminServer
// actually consumes. Defining it here (consumer-side) lets tests supply tiny
// fakes and signals that Resolve() belongs to the runtime execution path,
// not the admin surface.
type profileLister interface {
	List(ctx context.Context) ([]profile.Profile, error)
	GateFor(ctx context.Context, name profile.Name) (taskrunner.GateSpec, error)
}

// voiceController is the consumer-side narrow view of the live TTS
// controller — only what the admin voice / preview RPCs actually call.
// service.VoiceController satisfies it; tests can fake it.
type voiceController interface {
	Synthesize(ctx context.Context, text string) ([]int16, error)
	SampleRate() int
	Speaker() int
	Speed() float32
	NumSpeakers() int
	Tune(speaker int, speed float32)
	Engine() service.EngineName
	Engines() []service.EngineName
	SwitchEngine(name service.EngineName) error
}

// AdminConfig bundles every dependency the AdminServer needs. Using a config
// struct over a long positional constructor — adding/moving fields doesn't
// break call sites, and tests can populate only the fields the RPC under test
// uses (nil fields fail loudly when reached, which is the desired signal).
type AdminConfig struct {
	// Repositories.
	Prompts         *repository.PromptRepo
	Persons         *repository.PersonRepo
	Providers       *repository.ToolProviderRepo
	ToolCalls       *repository.ToolCallRepo
	Tasks           *repository.TaskRepo
	Settings        *repository.SettingsRepo
	Proposals       *repository.ToolProposalRepo
	PromptProposals *repository.PromptProposalRepo
	Summaries       *repository.ConversationSummaryRepo
	SensorProposals *repository.SensorProposalRepo
	Dolt            *repository.DoltRepo
	Workspaces      *repository.WorkspaceRepo

	// Services.
	Registry      *tools.Registry
	ZarlServer    *ZarlServer
	ToolManager   *ToolManager
	Synthesizer   voiceController
	Runner        *taskrunner.Runner
	Embedder      service.Embedder
	Notifications *znotify.NotificationStore

	// Profiles.
	ProfileRegistry  profileLister
	ProfileOverrides profileOverrideStore

	// SensorActivator hot-activates approved sensor proposals on the live
	// runner. Defined as an interface so the transport package doesn't have
	// to depend on cmd/zarl's concrete controller.
	SensorActivator SensorActivator

	// Qdrant is used to list and delete person memories.
	Qdrant *qdrant.Client

	// Tool description overrides. ToolDescriptionOverrides persists
	// human-authored descriptions; ToolDescriptionStore is the
	// in-memory cache served to the LLM. ActionTools is the unwrapped
	// bundle passed to the taskrunner — unwrapped so the admin can
	// surface their code-default descriptions.
	ToolDescriptionOverrides *repository.ToolDescriptionOverrideRepo
	ToolDescriptionStore     *tools.MemoryDescriptionStore
	ActionTools              []tools.Tool

	// Skills wiring. Skills is the CRUD repo; SkillProposals is the
	// LLM-author review queue; SkillStore is the selector-facing cache
	// the admin server re-loads after any write so the next turn picks
	// up the change without a rebuild.
	Skills         *repository.SkillRepo
	SkillProposals *repository.SkillProposalRepo
	SkillStore     *skills.MemorySkillStore

	// Operator-editable templates. Code owners register defaults;
	// admin writes land in the repo and the in-memory store.
	PromptTemplates     *repository.PromptTemplateRepo
	PromptTemplateStore *service.MemoryPromptTemplateStore
}

// SensorActivator turns an approved proposal into a running sensor (and
// tears one down when the proposal is re-rejected). Implemented by
// cmd/zarl's SensorController; exposed as an interface here so this
// package doesn't reach into the binary's wiring.
type SensorActivator interface {
	Activate(p repository.SensorProposal) (key string, err error)
	Deactivate(p repository.SensorProposal) bool
}

// NewAdminServer builds an AdminServer from a populated AdminConfig.
func NewAdminServer(cfg AdminConfig) *AdminServer {
	return &AdminServer{
		prompts:          cfg.Prompts,
		persons:          cfg.Persons,
		providers:        cfg.Providers,
		toolCalls:        cfg.ToolCalls,
		tasks:            cfg.Tasks,
		settings:         cfg.Settings,
		proposals:        cfg.Proposals,
		promptProposals:  cfg.PromptProposals,
		registry:         cfg.Registry,
		zarlServer:       cfg.ZarlServer,
		toolManager:      cfg.ToolManager,
		synthesizer:      cfg.Synthesizer,
		runner:           cfg.Runner,
		dolt:             cfg.Dolt,
		embedder:         cfg.Embedder,
		notifications:    cfg.Notifications,
		profileRegistry:  cfg.ProfileRegistry,
		profileOverrides: cfg.ProfileOverrides,
		summaries:        cfg.Summaries,
		sensorProposals:  cfg.SensorProposals,
		sensorActivator:  cfg.SensorActivator,
		qdrantClient:     cfg.Qdrant,
		toolDescRepo:     cfg.ToolDescriptionOverrides,
		toolDescStore:    cfg.ToolDescriptionStore,
		actionTools:      cfg.ActionTools,
		skills:           cfg.Skills,
		skillProposals:   cfg.SkillProposals,
		skillStore:       cfg.SkillStore,
		promptTemplates:  cfg.PromptTemplates,
		templateStore:    cfg.PromptTemplateStore,
		workspaces:       cfg.Workspaces,
	}
}

// profileOverrideStore is the interface for the per-profile override repository.
// It exists so tests can supply an in-memory implementation without a real DB.
type profileOverrideStore interface {
	Get(ctx context.Context, profile string) (repository.TaskProfileOverride, error)
	Upsert(ctx context.Context, profile string, o repository.TaskProfileOverride) error
	Delete(ctx context.Context, profile string) error
	List(ctx context.Context) (map[string]repository.TaskProfileOverride, error)
}

// noopEmbedder satisfies service.Embedder for chat clients that only
// need the Chat path. Using it keeps LlamaCppClient's embedder field
// non-nil without dragging a real embedder into the runner's
// reconfigure path — the runner never embeds; that lives in
// subscribers/summarizer and stays bound to the startup-time taskLlm.
type noopEmbedder struct{}

func (noopEmbedder) Embed(context.Context, string) ([]float32, error) {
	return nil, fmt.Errorf("embedder not configured for this chat client")
}

func maskKey(key string) string {
	if len(key) <= 8 {
		return ""
	}
	return key[:8] + "****"
}

func buildChatClient(provider, baseURL, apiKey, model string) (service.ChatClient, error) {
	switch provider {
	case "llamacpp":
		// Task-runner route to the local llama-server. Qwen3 thinking
		// mode + the deepseek reasoning format match the live chat
		// client, so reasoning_content comes back structured and the
		// runner's tool-loop logs don't get polluted by inline <think>.
		if baseURL == "" {
			return nil, fmt.Errorf("llamacpp provider requires base URL (e.g. http://localhost:8081/v1)")
		}
		return service.NewLlamaCppClient(baseURL, model, noopEmbedder{},
			service.WithLlamaCppTemplate(service.Qwen3Template{}),
			service.WithLlamaCppReasoning(true),
		), nil
	case "ollama":
		return service.NewOllamaClient(baseURL, model, noopEmbedder{}), nil
	case "openai":
		if apiKey == "" {
			return nil, fmt.Errorf("openai provider requires API key")
		}
		return service.NewOpenAIClient(baseURL, apiKey, model), nil
	case "anthropic":
		if apiKey == "" {
			return nil, fmt.Errorf("anthropic provider requires API key")
		}
		return service.NewAnthropicClient(apiKey, model), nil
	default:
		return nil, fmt.Errorf("unknown provider %q", provider)
	}
}
