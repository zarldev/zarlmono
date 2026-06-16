package dynamic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/mcp"
)

// ToolNameMCPConnect is the agent-facing tool name for connecting to an MCP server.
const ToolNameMCPConnect tools.ToolName = "mcp_connect"

// MCPNotifier is called once per server-pushed notification across
// every registered MCP connection. The connection name is the one
// the agent passed to mcp_connect; method is the JSON-RPC method
// (e.g. "notifications/resources/updated", or any custom method
// the server publishes); params is the raw JSON payload.
//
// The handler runs on the MCP transport's reader goroutine — it must
// not block. The intended consumer is an agent's inject queue
// (queueState in zarlcode): the handler formats a one-line
// summary of the notification and appends it to the queue, where the
// runner drains it at the next iteration boundary so the agent sees
// the long-running-task completion (or resource update, or custom
// event) as a fresh user message in its context.
//
// Nil is fine — connections will still register tools, just won't
// route their notifications anywhere.
type MCPNotifier func(connection, method string, params json.RawMessage)

// MCPRegistry owns the zarlcode's MCP connections. Each connection
// is tagged with a unique name so it can be cleanly disconnected
// later. The exported type lets zarlcode construct the registry
// once at startup and pass it to the per-tool constructors below.
type MCPRegistry struct {
	mu       sync.Mutex
	clients  map[string]mcpConnection // name → connection
	reg      *tools.Registry          // registry to register/unregister tools against
	notifier MCPNotifier              // optional notification sink
	policy   MCPConnectPolicy         // validates process/network-capable connections
}

// MCPConnectPolicy is the policy seam for approving or denying MCP
// connection attempts before any subprocess is launched or HTTP request
// is made. UIs can replace the default with an allowlist or
// user-confirmation implementation.
type MCPConnectPolicy interface {
	ValidateMCPConnect(ctx context.Context, name string, conn MCPConnSpec) error
}

// MCPConnectPolicyFunc adapts a function into an MCPConnectPolicy.
type MCPConnectPolicyFunc func(ctx context.Context, name string, conn MCPConnSpec) error

// ValidateMCPConnect calls f, letting a plain function serve as the
// approval gate the registry consults before any MCP subprocess is
// launched or HTTP request is made.
func (f MCPConnectPolicyFunc) ValidateMCPConnect(ctx context.Context, name string, conn MCPConnSpec) error {
	return f(ctx, name, conn)
}

// DefaultMCPConnectPolicy enforces the built-in transport validations.
// It is intentionally non-interactive; callers that need user approval
// can install their own policy with SetConnectPolicy.
var DefaultMCPConnectPolicy MCPConnectPolicy = MCPConnectPolicyFunc(
	func(ctx context.Context, _ string, conn MCPConnSpec) error {
		if conn.Type == Transports.TRANSPORTHTTP {
			if err := validateMCPHTTPBaseURL(ctx, conn.BaseURL); err != nil {
				return err
			}
			// Refuse to send a bearer token over cleartext http. The
			// transport in pkg/mcp/http.go attaches the token as an
			// Authorization: Bearer header on every request — over
			// http:// that's a credential exposed to the wire, every
			// downstream proxy, and any on-path observer. Forcing
			// https here removes the foot-gun before the request
			// leaves the binary. Token-less http:// stays allowed for
			// public / unauthenticated MCP servers.
			if conn.AuthToken != "" {
				if u, err := url.Parse(conn.BaseURL); err == nil && strings.EqualFold(u.Scheme, "http") {
					return errors.New(
						"mcp_connect: refusing to send auth_token over cleartext http — use an https:// base_url, or drop auth_token for unauthenticated servers",
					)
				}
			}
			return nil
		}
		if conn.Type == Transports.TRANSPORTSTDIO {
			return validateMCPStdioCommand(conn.Command)
		}
		return nil
	},
)

const (
	shellBash = "bash"

	maxMCPToolsPerConnection = 64
	maxMCPDescriptionBytes   = 2048
	maxMCPSchemaBytes        = 32 * 1024
)

type mcpConnection struct {
	client    *mcp.Client
	tools     []tools.Tool // the RemoteTool instances for cleanup
	toolNames []string     // for reporting
	cancelSub func()       // unregister the SubscribeAny on disconnect
}

// NewMCPRegistry constructs an empty registry. Pass notifier=nil for
// agents that don't want notifications routed anywhere.
func NewMCPRegistry(reg *tools.Registry, notifier MCPNotifier) *MCPRegistry {
	return &MCPRegistry{
		clients:  make(map[string]mcpConnection),
		reg:      reg,
		notifier: notifier,
		policy:   DefaultMCPConnectPolicy,
	}
}

// SetConnectPolicy replaces the registry's MCP connection policy. Passing
// nil restores the default validation policy.
func (r *MCPRegistry) SetConnectPolicy(policy MCPConnectPolicy) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if policy == nil {
		policy = DefaultMCPConnectPolicy
	}
	r.policy = policy
}

// NewMCPTools constructs the connect / disconnect / list trio bound
// to the same registry. Convenience wrapper for callers that just
// want to register all three at once — each concrete tool satisfies
// tools.Tool directly via its own Execute method.
func NewMCPTools(reg *tools.Registry, notifier MCPNotifier) []tools.Tool {
	r := NewMCPRegistry(reg, notifier)
	return []tools.Tool{
		NewMCPConnect(r),
		NewMCPDisconnect(r),
		NewMCPList(r),
	}
}

// connect creates a new MCP client, discovers tools, registers them,
// and (when the registry was constructed with a notifier) subscribes
// to the client's server-pushed notifications so they flow into the
// agent's inject queue.
func (r *MCPRegistry) connect(ctx context.Context, name string, conn MCPConnSpec) ([]string, error) {
	r.mu.Lock()
	if _, exists := r.clients[name]; exists {
		r.mu.Unlock()
		return nil, fmt.Errorf("mcp connection %q already exists", name)
	}
	policy := r.policy
	if policy == nil {
		policy = DefaultMCPConnectPolicy
	}
	notifier := r.notifier
	r.mu.Unlock()

	if err := policy.ValidateMCPConnect(ctx, name, conn); err != nil {
		return nil, err
	}

	var c *mcp.Client
	var err error

	switch conn.Type {
	case Transports.TRANSPORTSTDIO:
		c, err = mcp.NewStdioClient(conn.Command, conn.Args, conn.Env)
	case Transports.TRANSPORTHTTP:
		// Pass the same allowlist validateMCPHTTPBaseURL uses at
		// config time, but enforced per-dial. The URL validator
		// only checks the boot-time DNS reply; this policy
		// re-checks every resolved IP at TCP connect time, so a
		// rebinding (or initially-allowed host whose A-record
		// flips to RFC1918 between calls) can't tunnel to local
		// services through the MCP transport.
		c = mcp.NewClientWithDialPolicy(conn.BaseURL, conn.AuthToken,
			func(ip netip.Addr) bool { return !isDisallowedMCPAddr(ip) })
	default:
		return nil, fmt.Errorf("unknown transport type %s", conn.Type)
	}

	if err != nil {
		return nil, fmt.Errorf("mcp_connect: %w", err)
	}
	defs, err := c.Discover(ctx)
	if err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("discover tools: %w", err)
	}
	if err := validateMCPToolDefs(defs); err != nil {
		_ = c.Close()
		return nil, err
	}

	mcpTools := tools.WrapMCPTools(c, defs)
	provider := "mcp:" + name

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.clients[name]; exists {
		_ = c.Close()
		return nil, fmt.Errorf("mcp connection %q already exists", name)
	}
	toolNames, err := r.validateRemoteToolNames(provider, mcpTools)
	if err != nil {
		_ = c.Close()
		return nil, err
	}

	mc := mcpConnection{
		client:    c,
		tools:     mcpTools,
		toolNames: toolNames,
	}

	// Wire the connection's server-pushed notifications to the
	// agent's notifier (if one was provided). Bind the connection
	// name into the closure so a single notifier can demultiplex
	// across multiple connections by the connection-name argument.
	if notifier != nil {
		connName := name // capture for the closure
		mc.cancelSub = c.SubscribeAny(func(method string, params json.RawMessage) {
			notifier(connName, method, params)
		})
	}

	r.clients[name] = mc

	for _, t := range mcpTools {
		r.reg.RegisterWithProvider(t, provider)
	}

	return toolNames, nil
}

// validateRemoteToolNames preflights MCP-discovered tool names before
// registration. Registry registration intentionally replaces same-name
// tools, but a remote MCP server is untrusted input: it must not be able
// to shadow built-ins, dynamic tools, or tools from another MCP
// connection by advertising a colliding name. Duplicate names within the
// same discovery response are rejected too, because otherwise the later
// registration would silently win.
func (r *MCPRegistry) validateRemoteToolNames(provider string, mcpTools []tools.Tool) ([]string, error) {
	seen := make(map[tools.ToolName]struct{}, len(mcpTools))
	toolNames := make([]string, len(mcpTools))
	for i, t := range mcpTools {
		name := t.Definition().Name
		toolNames[i] = string(name)
		if !validToolName.MatchString(string(name)) {
			return nil, fmt.Errorf("mcp tool %q has invalid name (must match ^[a-z][a-z0-9_]{1,63}$)", name)
		}
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("mcp server advertised duplicate tool name %q", name)
		}
		seen[name] = struct{}{}

		if _, exists := r.reg.Tool(name); exists {
			currentProvider := r.reg.ProviderFor(name)
			if currentProvider == "" {
				currentProvider = "runtime"
			}
			return nil, fmt.Errorf(
				"mcp tool %q from %s would shadow existing tool from %s",
				name,
				provider,
				currentProvider,
			)
		}
	}
	return toolNames, nil
}

// disconnect tears down an MCP client and unregisters its tools.
func (r *MCPRegistry) disconnect(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	conn, exists := r.clients[name]
	if !exists {
		return fmt.Errorf("mcp connection %q not found", name)
	}

	if conn.cancelSub != nil {
		conn.cancelSub()
	}
	r.reg.UnregisterProvider("mcp:" + name)
	delete(r.clients, name)

	return conn.client.Close()
}

// CloseAll tears down every live MCP connection — cancelling each
// subscription, unregistering its tools, and closing the client (which, for a
// stdio transport, gracefully stops then kills the child process). Used by the
// shell's shutdown path so a stdio MCP server subprocess isn't orphaned on
// exit. Idempotent: the client map is cleared, so a second call is a no-op.
// Returns the first close error encountered, after attempting all of them.
func (r *MCPRegistry) CloseAll() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var firstErr error
	for name, conn := range r.clients {
		if conn.cancelSub != nil {
			conn.cancelSub()
		}
		r.reg.UnregisterProvider("mcp:" + name)
		if err := conn.client.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close mcp %q: %w", name, err)
		}
		delete(r.clients, name)
	}
	return firstErr
}

// list returns the names of all connected MCP servers.
func (r *MCPRegistry) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	names := make([]string, 0, len(r.clients))
	for n := range r.clients {
		names = append(names, n)
	}
	return names
}

// MCPConnSpec is the connection configuration for an MCP server.
type MCPConnSpec struct {
	Type      Transport         `json:"type"`       // stdio or http
	Command   string            `json:"command"`    // stdio: binary to run
	Args      []string          `json:"args"`       // stdio: command-line arguments
	Env       map[string]string `json:"env"`        // stdio: environment variables (optional)
	BaseURL   string            `json:"base_url"`   // http: server URL
	AuthToken string            `json:"auth_token"` // http: Bearer token (optional)
}

// MCPConnect is the agent-facing tool that connects to an MCP server and
// registers all its tools on the registry.
type MCPConnect struct {
	reg *MCPRegistry
}

// MCPConnectArgs is the typed argument struct MCPConnect.Execute
// decodes into via tools.DecodeArgs. The schema has transport-
// specific fields (stdio uses command / args / env; http uses
// base_url / auth_token) — the typed struct carries them all and
// Execute picks the right subset per transport.
type MCPConnectArgs struct {
	Name      string            `json:"name" doc:"A unique name for this connection. Used to identify it later (e.g. in mcp_disconnect). Must match ^[a-z][a-z0-9_]{1,63}$."`
	Transport Transport         `json:"transport" doc:"Connection transport: 'stdio' for local subprocess servers or 'http' for remote servers."`
	Command   string            `json:"command,omitempty" doc:"(stdio) Absolute path to the MCP server binary to run."`
	Args      []string          `json:"args,omitempty" doc:"(stdio) Command-line arguments to pass to the binary."`
	Env       map[string]string `json:"env,omitempty" doc:"(stdio) Environment variables to set on the subprocess. Optional."`
	BaseURL   string            `json:"base_url,omitempty" doc:"(http) The MCP server's base URL (e.g. 'https://mcp.example.com')."`
	AuthToken string            `json:"auth_token,omitempty" doc:"(http) Bearer token for authentication. Optional."`
}

// NewMCPConnect returns the tool that connects to an MCP server and
// registers its discovered tools through r.
func NewMCPConnect(r *MCPRegistry) *MCPConnect { return &MCPConnect{reg: r} }

// Definition advertises mcp_connect: required name (must match
// ^[a-z][a-z0-9_]{1,63}$) and transport ('stdio' or 'http'), plus the
// transport-specific command/args/env and base_url/auth_token fields.
func (t *MCPConnect) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name: ToolNameMCPConnect,
		Description: "Connect to an MCP server and register its tools. " +
			"Supports two transport types: 'stdio' (runs a local command as a subprocess) " +
			"and 'http' (connects to a remote MCP server). After connecting, all discovered " +
			"tools become immediately available for use. Tool names must be valid and must not " +
			"collide with existing tools; colliding MCP servers are rejected instead of shadowing " +
			"runtime tools. Use mcp_list to see active connections, " +
			"and mcp_disconnect to tear one down.",
		Parameters: tools.SchemaFor[MCPConnectArgs](),
	}
}

// Execute validates the connection name and the per-transport required
// args (command for stdio, base_url for http), then connects through the
// registry: the MCPConnectPolicy gate runs first, the discovered tools are
// validated (max 64, capped description/schema sizes, no shadowing of
// existing tools) and registered under provider "mcp:<name>".
func (t *MCPConnect) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	var args MCPConnectArgs
	if derr := tools.DecodeArgs(call.Arguments, &args); derr != nil {
		return failureResult(call.ID, derr.Error()), nil
	}
	if args.Name == "" {
		return failureResult(call.ID, "mcp_connect: name required"), nil
	}
	if !validToolName.MatchString(args.Name) {
		return failureResult(call.ID, fmt.Sprintf("name %q must match ^[a-z][a-z0-9_]{1,63}$", args.Name)), nil
	}
	if !args.Transport.IsValid() {
		return failureResult(call.ID, "mcp_connect: transport required (use 'stdio' or 'http')"), nil
	}

	var spec MCPConnSpec
	switch args.Transport {
	case Transports.TRANSPORTSTDIO:
		if args.Command == "" {
			return failureResult(call.ID, "mcp_connect: command required for stdio transport"), nil
		}
		spec = MCPConnSpec{
			Type:    Transports.TRANSPORTSTDIO,
			Command: args.Command,
			Args:    args.Args,
			Env:     args.Env,
		}
	case Transports.TRANSPORTHTTP:
		if args.BaseURL == "" {
			return failureResult(call.ID, "mcp_connect: base_url required for http transport"), nil
		}
		spec = MCPConnSpec{
			Type:      Transports.TRANSPORTHTTP,
			BaseURL:   args.BaseURL,
			AuthToken: args.AuthToken,
		}
	default:
		return failureResult(call.ID, fmt.Sprintf("unknown transport %s", args.Transport)), nil
	}
	toolNames, err := t.reg.connect(ctx, args.Name, spec)
	if err != nil {
		return failureResult(call.ID, fmt.Sprintf("mcp_connect: %v", err)), nil
	}

	return tools.Success(call.ID, fmt.Sprintf("connected %s (%d tools): %s", args.Name, len(toolNames), joinStrings(", ", toolNames))), nil
}

func validateMCPHTTPBaseURL(ctx context.Context, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid base_url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("base_url scheme must be http or https, got %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("base_url host required")
	}
	lowerHost := strings.ToLower(strings.TrimSuffix(host, "."))
	if lowerHost == "localhost" || strings.HasSuffix(lowerHost, ".localhost") {
		return fmt.Errorf("base_url host %q is local; localhost MCP servers require an explicit allowlist", host)
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if isDisallowedMCPAddr(addr) {
			return fmt.Errorf("base_url host %q is not allowed for MCP HTTP connections", host)
		}
		return nil
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
	if err != nil {
		return fmt.Errorf("resolve base_url host %q: %w", host, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("resolve base_url host %q: no addresses", host)
	}
	for _, ip := range addrs {
		addr, ok := netip.AddrFromSlice(ip.IP)
		if !ok {
			return fmt.Errorf("resolve base_url host %q: invalid address %q", host, ip.IP.String())
		}
		if isDisallowedMCPAddr(addr) {
			return fmt.Errorf("base_url host %q resolves to disallowed address %q", host, addr)
		}
	}
	return nil
}

func validateMCPStdioCommand(command string) error {
	if !filepath.IsAbs(command) {
		return fmt.Errorf("stdio MCP command %q must be an absolute path", command)
	}
	switch filepath.Base(command) {
	case "sh", shellBash, "dash", "zsh", "fish", "ksh", "csh", "tcsh":
		return fmt.Errorf(
			"stdio MCP command %q is a shell; use a dedicated MCP server binary or an explicit allowlist policy",
			command,
		)
	}
	return nil
}

func validateMCPToolDefs(defs []mcp.ToolDef) error {
	if len(defs) > maxMCPToolsPerConnection {
		return fmt.Errorf("mcp server advertised %d tools, max %d", len(defs), maxMCPToolsPerConnection)
	}
	for _, def := range defs {
		if len(def.Description) > maxMCPDescriptionBytes {
			return fmt.Errorf(
				"mcp tool %q description too large (%d bytes, max %d)",
				def.Name,
				len(def.Description),
				maxMCPDescriptionBytes,
			)
		}
		raw, err := json.Marshal(def.InputSchema)
		if err != nil {
			return fmt.Errorf("mcp tool %q schema is not JSON-marshalable: %w", def.Name, err)
		}
		if len(raw) > maxMCPSchemaBytes {
			return fmt.Errorf("mcp tool %q schema too large (%d bytes, max %d)", def.Name, len(raw), maxMCPSchemaBytes)
		}
	}
	return nil
}

func isDisallowedMCPAddr(addr netip.Addr) bool {
	return addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
		addr.IsMulticast() ||
		addr.IsUnspecified()
}

// ToolNameMCPDisconnect is the agent-facing tool name for disconnecting an MCP server.
const ToolNameMCPDisconnect tools.ToolName = "mcp_disconnect"

// MCPDisconnect tears down an MCP connection and unregisters its tools.
type MCPDisconnect struct {
	reg *MCPRegistry
}

// MCPDisconnectArgs is the typed argument struct MCPDisconnect.Execute
// decodes into via tools.DecodeArgs.
type MCPDisconnectArgs struct {
	Name string `json:"name" doc:"The name of the MCP connection to disconnect."`
}

// NewMCPDisconnect returns the tool that tears down a named MCP
// connection in r and unregisters its tools.
func NewMCPDisconnect(r *MCPRegistry) *MCPDisconnect { return &MCPDisconnect{reg: r} }

// Definition advertises mcp_disconnect with a single required name
// parameter — the connection name given to mcp_connect.
func (t *MCPDisconnect) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        ToolNameMCPDisconnect,
		Description: "Disconnect an MCP server and unregister all its tools. The connection name is the one you gave when calling mcp_connect.",
		Parameters:  tools.SchemaFor[MCPDisconnectArgs](),
	}
}

// Execute tears down the named connection: cancels its notification
// subscription, unregisters every tool under provider "mcp:<name>", and
// closes the client (for stdio transports, stopping the subprocess).
func (t *MCPDisconnect) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	var args MCPDisconnectArgs
	if derr := tools.DecodeArgs(call.Arguments, &args); derr != nil {
		return failureResult(call.ID, derr.Error()), nil
	}
	if args.Name == "" {
		return failureResult(call.ID, "mcp_disconnect: name required"), nil
	}
	if err := t.reg.disconnect(args.Name); err != nil {
		return failureResult(call.ID, fmt.Sprintf("mcp_disconnect: %v", err)), nil
	}
	return tools.Success(call.ID, fmt.Sprintf("disconnected %s", args.Name)), nil
}

// ToolNameMCPList is the agent-facing tool name for listing active MCP connections.
const ToolNameMCPList tools.ToolName = "mcp_list"

// MCPList shows all currently connected MCP servers.
type MCPList struct {
	reg *MCPRegistry
}

// (MCPList takes no arguments — no args struct needed.)

// NewMCPList returns the tool that lists the active MCP connections in r.
func NewMCPList(r *MCPRegistry) *MCPList { return &MCPList{reg: r} }

// Definition advertises mcp_list with an empty parameter schema — the
// tool takes no arguments.
func (t *MCPList) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        ToolNameMCPList,
		Description: "List all currently active MCP server connections. Shows the connection names that can be used with mcp_disconnect.",
		Parameters:  tools.SchemaFor[struct{}](),
	}
}

// Execute returns the names of the currently connected MCP servers as a
// single "active connections: …" line ("(none)" when empty). Read-only —
// nothing is mutated.
func (t *MCPList) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	names := t.reg.list()
	if names == nil {
		names = []string{}
	}
	return tools.Success(call.ID, fmt.Sprintf("active connections: %s", joinStrings(", ", names))), nil
}

// joinStrings joins strings with a separator.
func joinStrings(sep string, ss []string) string {
	if len(ss) == 0 {
		return "(none)"
	}
	out := ss[0]
	var outSb602 strings.Builder
	for _, s := range ss[1:] {
		outSb602.WriteString(sep + s)
	}
	out += outSb602.String()
	return out
}
