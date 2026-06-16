package grpc

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	znotify "github.com/zarldev/zarlmono/zkit/znotify"

	"github.com/zarldev/zarlmono/zarlai/repository"
	"github.com/zarldev/zarlmono/zarlai/service"
	"github.com/zarldev/zarlmono/zarlai/tools/homeassistant"
	"github.com/zarldev/zarlmono/zarlai/tools/memory"
	"github.com/zarldev/zarlmono/zarlai/tools/searxng"
	"github.com/zarldev/zarlmono/zarlai/tools/spotify"
	"github.com/zarldev/zarlmono/zarlai/tools/timer"
	"github.com/zarldev/zarlmono/zarlai/tools/wiki"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/mcp"
	"github.com/zarldev/zarlmono/zkit/vectorstore/qdrant"
)

// ToolManager handles provider lifecycle: loading tools from DB config,
// registering them in the Registry, and hot-reloading on config changes.
type ToolManager struct {
	registry      *tools.Registry
	providers     *repository.ToolProviderRepo
	settings      *repository.SettingsRepo
	embedder      service.Embedder
	notifications *znotify.NotificationStore
	qdrantClient  *qdrant.Client
	spotifyClient *spotify.Client

	// mcpClients tracks active MCP clients keyed by provider name so we can
	// terminate stdio subprocesses on unload/reload.
	mcpClients map[string]*mcp.Client
}

// NewToolManager creates a ToolManager.
func NewToolManager(
	registry *tools.Registry,
	providers *repository.ToolProviderRepo,
	settings *repository.SettingsRepo,
	embedder service.Embedder,
	notifications *znotify.NotificationStore,
) *ToolManager {
	return &ToolManager{
		registry:      registry,
		providers:     providers,
		settings:      settings,
		embedder:      embedder,
		notifications: notifications,
		mcpClients:    make(map[string]*mcp.Client),
	}
}

// QdrantClient returns the qdrant client initialized by the memory provider.
func (tm *ToolManager) QdrantClient() *qdrant.Client {
	return tm.qdrantClient
}

// SpotifyClient returns the spotify client initialized by the spotify
// provider, or nil when the provider isn't configured. Consumed by the
// sensor subsystem so the now-playing sensor can share the same OAuth
// token cache as the tools.
func (tm *ToolManager) SpotifyClient() *spotify.Client {
	return tm.spotifyClient
}

// McpClient looks up an active MCP client by provider name. Returns
// (nil, false) when no provider of that name is loaded, or the loaded
// provider isn't MCP. Used by the reactive sensor controller to bind
// mcp_notification sensors to their subscription source.
func (tm *ToolManager) McpClient(name string) (*mcp.Client, bool) {
	c, ok := tm.mcpClients[name]
	return c, ok
}

// InitAll loads all enabled providers from the database and initializes their tools.
func (tm *ToolManager) InitAll(ctx context.Context) error {
	providers, err := tm.providers.List(ctx)
	if err != nil {
		return fmt.Errorf("list providers: %w", err)
	}
	for _, p := range providers {
		if !p.Enabled {
			slog.Info("tool provider disabled, skipping", "provider", p.Name)
			continue
		}
		if err := tm.initProvider(ctx, p); err != nil {
			slog.Error("init tool provider", "provider", p.Name, "error", err)
			continue
		}
		slog.Info("tool provider initialized",
			"provider", p.Name,
			"type", p.Type,
			"tools", tm.registry.ToolCountForProvider(p.Name),
		)
	}
	return nil
}

// ReloadByID unregisters old tools for a provider and re-initializes if enabled.
func (tm *ToolManager) ReloadByID(ctx context.Context, id repository.ToolProviderID) error {
	providers, err := tm.providers.List(ctx)
	if err != nil {
		return fmt.Errorf("list providers: %w", err)
	}
	var target *repository.ToolProvider
	for i := range providers {
		if providers[i].ID == id {
			target = &providers[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("provider not found: %s", id)
	}

	tm.registry.UnregisterProvider(target.Name)
	slog.Info("tool provider unloaded", "provider", target.Name)

	if !target.Enabled {
		return nil
	}

	if err := tm.initProvider(ctx, *target); err != nil {
		return fmt.Errorf("init provider %s: %w", target.Name, err)
	}
	slog.Info("tool provider reloaded",
		"provider", target.Name,
		"tools", tm.registry.ToolCountForProvider(target.Name),
	)
	return nil
}

// UnloadByID unregisters all tools for a provider without re-initializing.
func (tm *ToolManager) UnloadByID(ctx context.Context, id repository.ToolProviderID) error {
	providers, err := tm.providers.List(ctx)
	if err != nil {
		return fmt.Errorf("list providers: %w", err)
	}
	for _, p := range providers {
		if p.ID == id {
			tm.registry.UnregisterProvider(p.Name)
			if client, ok := tm.mcpClients[p.Name]; ok {
				_ = client.Close()
				delete(tm.mcpClients, p.Name)
			}
			slog.Info("tool provider unloaded", "provider", p.Name)
			return nil
		}
	}
	return fmt.Errorf("provider not found: %s", id)
}

// initProvider switches on the provider name and registers the appropriate tools.
func (tm *ToolManager) initProvider(ctx context.Context, p repository.ToolProvider) error {
	switch p.Name {
	case "home_assistant":
		return tm.initHomeAssistant(p)
	case "memory":
		return tm.initMemory(ctx, p)
	case "searxng":
		return tm.initSearxng(p)
	case "spotify":
		return tm.initSpotify(p)
	case "timer":
		return tm.initTimer(p)
	case "wiki":
		return tm.initWiki(ctx, p)
	default:
		if p.Type == "mcp" {
			return tm.initMCP(ctx, p)
		}
		return fmt.Errorf("unknown provider: %s (type %s)", p.Name, p.Type)
	}
}

func (tm *ToolManager) initHomeAssistant(p repository.ToolProvider) error {
	url := p.Config["url"]
	token := p.Config["token"]
	if url == "" || token == "" {
		return fmt.Errorf("home_assistant requires url and token config")
	}
	ha := homeassistant.NewClient(url, token)
	tm.registry.RegisterWithProvider(homeassistant.NewGetStateTool(ha), p.Name)
	tm.registry.RegisterWithProvider(homeassistant.NewCallServiceTool(ha), p.Name)
	tm.registry.RegisterWithProvider(homeassistant.NewListEntitiesTool(ha), p.Name)
	return nil
}

func (tm *ToolManager) initMemory(ctx context.Context, p repository.ToolProvider) error {
	qdrantURL := p.Config["qdrant_url"]
	if qdrantURL == "" {
		return fmt.Errorf("memory requires qdrant_url config")
	}
	qc := qdrant.NewClient(qdrantURL)
	if err := qc.EnsureCollection(ctx, memory.Collection, 768); err != nil {
		return fmt.Errorf("ensure memory collection: %w", err)
	}
	tm.qdrantClient = qc
	tm.registry.RegisterWithProvider(memory.NewRememberTool(qc, tm.embedder), p.Name)
	tm.registry.RegisterWithProvider(memory.NewRecallTool(qc, tm.embedder), p.Name)
	tm.registry.RegisterWithProvider(memory.NewForgetTool(qc, tm.embedder), p.Name)
	return nil
}

func (tm *ToolManager) initSearxng(p repository.ToolProvider) error {
	url := p.Config["url"]
	if url == "" {
		return fmt.Errorf("searxng requires url config")
	}
	sc := searxng.NewClient(url)
	tm.registry.RegisterWithProvider(searxng.NewSearchTool(sc), p.Name)
	tm.registry.RegisterWithProvider(searxng.NewYouTubeSearchTool(sc, tm.notifications), p.Name)
	return nil
}

func (tm *ToolManager) initSpotify(p repository.ToolProvider) error {
	cfg := spotify.Config{
		ClientID:     p.Config["client_id"],
		ClientSecret: p.Config["client_secret"],
		CachePath:    p.Config["cache_path"],
	}
	if cfg.ClientID == "" || cfg.ClientSecret == "" || cfg.CachePath == "" {
		return fmt.Errorf("spotify requires client_id, client_secret, cache_path config")
	}
	if tm.settings != nil {
		cfg.Preference = &spotifyDevicePreference{settings: tm.settings}
	}
	client, err := spotify.NewClient(cfg)
	if err != nil {
		return err
	}
	tm.spotifyClient = client
	tm.registry.RegisterWithProvider(spotify.NewSearchTool(client), p.Name)
	tm.registry.RegisterWithProvider(spotify.NewPlayTool(client), p.Name)
	tm.registry.RegisterWithProvider(spotify.NewPauseTool(client), p.Name)
	tm.registry.RegisterWithProvider(spotify.NewSkipTool(client), p.Name)
	tm.registry.RegisterWithProvider(spotify.NewQueueAddTool(client), p.Name)
	tm.registry.RegisterWithProvider(spotify.NewNowPlayingTool(client), p.Name)
	tm.registry.RegisterWithProvider(spotify.NewListDevicesTool(client), p.Name)
	tm.registry.RegisterWithProvider(spotify.NewSetPreferredDeviceTool(client), p.Name)
	return nil
}

// spotifyPreferredDeviceKey is the settings table key for the pinned
// Spotify Connect device name.
const spotifyPreferredDeviceKey = "spotify_preferred_device"

// spotifyDevicePreference adapts SettingsRepo to spotify.DevicePreference
// so the spotify package stays free of repository imports.
type spotifyDevicePreference struct {
	settings *repository.SettingsRepo
}

func (p *spotifyDevicePreference) Preferred(ctx context.Context) (string, error) {
	return p.settings.Get(ctx, spotifyPreferredDeviceKey)
}

func (p *spotifyDevicePreference) SetPreferred(ctx context.Context, name string) error {
	return p.settings.Set(ctx, spotifyPreferredDeviceKey, name)
}

func (tm *ToolManager) initTimer(p repository.ToolProvider) error {
	if tm.notifications == nil {
		return fmt.Errorf("timer requires notification store")
	}
	tt := timer.NewTimerTool(tm.notifications)
	tm.registry.RegisterWithProvider(tt, p.Name)
	tm.registry.RegisterWithProvider(timer.NewStatusTool(tt), p.Name)
	return nil
}

func (tm *ToolManager) initWiki(ctx context.Context, p repository.ToolProvider) error {
	qdrantURL := p.Config["qdrant_url"]
	if qdrantURL == "" && tm.qdrantClient != nil {
		// Fallback to memory provider's qdrant client
		if err := tm.qdrantClient.EnsureCollection(ctx, wiki.Collection, 768); err != nil {
			return fmt.Errorf("ensure wiki collection: %w", err)
		}
		tm.registry.RegisterWithProvider(wiki.NewSearchTool(tm.qdrantClient, tm.embedder), p.Name)
		return nil
	}
	if qdrantURL == "" {
		return fmt.Errorf("wiki requires qdrant_url config or an initialized memory provider")
	}
	qc := qdrant.NewClient(qdrantURL)
	if err := qc.EnsureCollection(ctx, wiki.Collection, 768); err != nil {
		return fmt.Errorf("ensure wiki collection: %w", err)
	}
	tm.registry.RegisterWithProvider(wiki.NewSearchTool(qc, tm.embedder), p.Name)
	return nil
}

func (tm *ToolManager) initMCP(ctx context.Context, p repository.ToolProvider) error {
	// Two transports: HTTP (set `url`) or stdio (set `command`, optional
	// `args` as a whitespace-separated string and `env` as "K=V\n..."). If
	// both are set, url wins.
	url := p.Config["url"]
	command := p.Config["command"]
	if url == "" && command == "" {
		return fmt.Errorf("mcp provider requires url or command config")
	}

	var (
		mc  *mcp.Client
		err error
	)
	if url != "" {
		authToken := p.Config["auth_token"]
		mc = mcp.NewClient(url, authToken)
	} else {
		args := parseStdioArgs(p.Config["args"])
		env := parseStdioEnv(p.Config["env"])
		mc, err = mcp.NewStdioClient(command, args, env)
		if err != nil {
			return fmt.Errorf("mcp stdio start: %w", err)
		}
	}

	defs, err := mc.Discover(ctx)
	if err != nil {
		_ = mc.Close()
		return fmt.Errorf("mcp discover: %w", err)
	}
	tm.mcpClients[p.Name] = mc
	hints := extractToolHints(p.Config)
	for _, def := range defs {
		if hint, ok := hints[def.Name]; ok {
			def.Description = mergeToolDescription(def.Description, hint)
		}
		tm.registry.RegisterWithProvider(tools.NewRemoteTool(mc, def), p.Name)
	}
	return nil
}

// extractToolHints pulls per-tool description hints from a provider config.
// Keys of the form `hint:<tool_name>` carry extra guidance that is appended
// to the tool's description. This is data, not code — operators can tune
// descriptions per-provider via the DB without changes here.
func extractToolHints(cfg map[string]string) map[string]string {
	out := make(map[string]string)
	for k, v := range cfg {
		name, ok := strings.CutPrefix(k, "hint:")
		if !ok || v == "" {
			continue
		}
		out[name] = v
	}
	return out
}

// mergeToolDescription appends the hint to the existing description, trimming
// trailing whitespace on the original and separating with a blank line so the
// addition reads as its own paragraph to the LLM.
func mergeToolDescription(existing, hint string) string {
	trimmed := strings.TrimRight(existing, " \t\n")
	if trimmed == "" {
		return hint
	}
	return trimmed + "\n\n" + hint
}

// parseStdioArgs splits a whitespace-separated argv string. Quoted substrings
// ("a b c") survive as a single argument. Empty input yields nil.
func parseStdioArgs(s string) []string {
	if s == "" {
		return nil
	}
	var args []string
	var current []rune
	inQuote := false
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
		case !inQuote && (r == ' ' || r == '\t' || r == '\n'):
			if len(current) > 0 {
				args = append(args, string(current))
				current = current[:0]
			}
		default:
			current = append(current, r)
		}
	}
	if len(current) > 0 {
		args = append(args, string(current))
	}
	return args
}

// parseStdioEnv parses a newline-separated list of KEY=VALUE pairs. Blank
// lines and lines without `=` are ignored.
func parseStdioEnv(s string) map[string]string {
	if s == "" {
		return nil
	}
	out := map[string]string{}
	for _, line := range splitLines(s) {
		for i := 0; i < len(line); i++ {
			if line[i] == '=' {
				out[line[:i]] = line[i+1:]
				break
			}
		}
	}
	return out
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			if i > start {
				lines = append(lines, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
