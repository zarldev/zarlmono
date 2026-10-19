package dynamic

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"iter"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

const (
	// ToolNameMCPSearch searches metadata already discovered from connected MCP servers.
	ToolNameMCPSearch tools.ToolName = "tool_search"
	// ToolNameMCPActivate adds one exact discovered MCP definition to the session's desired set.
	ToolNameMCPActivate tools.ToolName = "tool_activate"
	// ToolNameMCPDeactivate removes one exact MCP reference from the session's desired set.
	ToolNameMCPDeactivate tools.ToolName = "tool_deactivate"

	maxMCPDesiredTools       = 16
	defaultMCPSearchResults  = 10
	maxMCPSearchResults      = 20
	maxMCPSearchDescription  = 240
	mcpDefinitionHashVersion = "v1:"
)

// MCPToolRef is a durable selection hint for one definition discovered from a
// logical MCP connection. It carries no endpoint, credentials, approval, or
// executable authority. A reference that is not currently connected with the
// same complete definition remains dormant.
type MCPToolRef struct {
	Connection     string         `json:"connection"`
	Name           tools.ToolName `json:"name"`
	DefinitionHash string         `json:"definition_hash"`
}

// MCPExposure owns the desired MCP references for one session. It reads the
// registry's live connection catalog without changing direct registration. Core
// and synthetic names supplied in reserved can never be activated.
type MCPExposure struct {
	registry *MCPRegistry

	mu       sync.Mutex
	desired  map[MCPToolRef]struct{}
	reserved map[tools.ToolName]struct{}
}

// NewMCPExposure constructs an empty per-session exposure owner over registry.
// reserved must contain every direct/core name in the final composed source;
// the three control-tool names are always reserved automatically. Construction
// does not alter the registry's direct exposure behavior.
func NewMCPExposure(registry *MCPRegistry, reserved ...tools.ToolName) *MCPExposure {
	e := &MCPExposure{
		registry: registry,
		desired:  make(map[MCPToolRef]struct{}),
		reserved: make(map[tools.ToolName]struct{}, len(reserved)+3),
	}
	for _, name := range reserved {
		e.reserved[name] = struct{}{}
	}
	e.reserved[ToolNameMCPSearch] = struct{}{}
	e.reserved[ToolNameMCPActivate] = struct{}{}
	e.reserved[ToolNameMCPDeactivate] = struct{}{}
	return e
}

// Snapshot returns a sorted, independently owned copy of the complete desired
// set, including references that are currently dormant.
func (e *MCPExposure) Snapshot() []MCPToolRef {
	e.mu.Lock()
	refs := make([]MCPToolRef, 0, len(e.desired))
	for ref := range e.desired {
		refs = append(refs, ref)
	}
	e.mu.Unlock()
	sortMCPToolRefs(refs)
	return refs
}

// Reserve adds direct/core names that this exposure must never advertise or
// dispatch. Existing desired references remain as dormant selection hints so a
// later snapshot preserves them; already-enumerated tools are denied on execute.
func (e *MCPExposure) Reserve(names ...tools.ToolName) {
	e.mu.Lock()
	for _, name := range names {
		e.reserved[name] = struct{}{}
	}
	e.mu.Unlock()
}

// Replace validates and atomically replaces the complete desired set. It does
// not connect, approve, activate, or discard unresolved references.
func (e *MCPExposure) Replace(refs []MCPToolRef) error {
	if err := ValidateMCPToolRefs(refs); err != nil {
		return err
	}
	next := make(map[MCPToolRef]struct{}, len(refs))
	for _, ref := range refs {
		next[ref] = struct{}{}
	}
	e.mu.Lock()
	e.desired = next
	e.mu.Unlock()
	return nil
}

// Source returns the activated-remote source. Its Tools method snapshots state
// before iteration and its Execute method independently revalidates the exact
// definition and connection generation before acquiring an execution lease.
func (e *MCPExposure) Source() tools.Source { return mcpExposureSource{exposure: e} }

// ControlTools returns the separate search, activate, and deactivate tools for
// normal engine registration and guardrail wrapping.
func (e *MCPExposure) ControlTools() []tools.Tool {
	return []tools.Tool{
		tools.New(ToolSpecMCPSearch(), e.search),
		tools.New(ToolSpecMCPActivate(), e.activate),
		tools.New(ToolSpecMCPDeactivate(), e.deactivate),
	}
}

// ToolSpecMCPSearch returns the stable agent-facing search definition.
func ToolSpecMCPSearch() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        ToolNameMCPSearch,
		Description: "Search metadata for tools already discovered from connected MCP servers. This performs no connection or network I/O.",
		Parameters:  tools.SchemaFor[MCPSearchArgs](),
	}
}

// ToolSpecMCPActivate returns the stable agent-facing activation definition.
func ToolSpecMCPActivate() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        ToolNameMCPActivate,
		Description: "Activate one exact MCP tool reference returned by tool_search for this session.",
		Parameters:  tools.SchemaFor[MCPActivateArgs](),
	}
}

// ToolSpecMCPDeactivate returns the stable agent-facing deactivation definition.
func ToolSpecMCPDeactivate() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        ToolNameMCPDeactivate,
		Description: "Deactivate one exact desired MCP tool reference for this session; dormant references can also be removed.",
		Parameters:  tools.SchemaFor[MCPDeactivateArgs](),
	}
}

// MCPSearchArgs are the bounded search arguments.
type MCPSearchArgs struct {
	Query string `json:"query,omitempty" doc:"Case-insensitive text matched against connection, tool name, and description. Empty lists all connected tools."`
	Limit int    `json:"limit,omitempty" doc:"Maximum results. Zero uses 10; valid range is 1 through 20."`
}

// MCPSearchResult describes one connected definition. Description is untrusted
// remote metadata clipped to 240 runes; Ref is the exact activation token.
type MCPSearchResult struct {
	Ref         MCPToolRef `json:"ref"`
	Description string     `json:"description"`
}

// MCPActivateArgs identifies the exact searched definition to activate.
type MCPActivateArgs struct {
	Connection     string         `json:"connection"`
	Name           tools.ToolName `json:"name"`
	DefinitionHash string         `json:"definition_hash"`
}

// MCPDeactivateArgs identifies the exact desired reference to remove.
type MCPDeactivateArgs = MCPActivateArgs

type mcpExposureSource struct{ exposure *MCPExposure }

type exposedMCPTool struct {
	exposure   *MCPExposure
	ref        MCPToolRef
	generation uint64
	spec       tools.ToolSpec
}

func (s mcpExposureSource) Tools(context.Context) iter.Seq[tools.Tool] {
	visible := s.exposure.visibleTools()
	return func(yield func(tools.Tool) bool) {
		for _, tool := range visible {
			if !yield(tool) {
				return
			}
		}
	}
}

func (s mcpExposureSource) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	for _, tool := range s.exposure.visibleTools() {
		if tool.Definition().Name == call.ToolName {
			return tool.Execute(ctx, call)
		}
	}
	return nil, tools.NotFound("mcp exposure execute", fmt.Sprintf("tool %q is not activated with its current definition", call.ToolName))
}

func (t exposedMCPTool) Definition() tools.ToolSpec { return t.spec }

func (t exposedMCPTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	return t.exposure.registry.executeRemoteWhen(
		ctx, t.ref.Connection, t.ref, t.generation, call,
		func() bool { return t.exposure.isActive(t.ref) },
	)
}

func (e *MCPExposure) visibleTools() []tools.Tool {
	desired, reserved := e.stateSnapshot()
	catalog := e.registry.catalogSnapshot()
	resolved := make(map[tools.ToolName][]mcpCatalogEntry)
	for _, entry := range catalog {
		if _, ok := desired[entry.ref]; !ok {
			continue
		}
		if _, blocked := reserved[entry.ref.Name]; blocked {
			continue
		}
		resolved[entry.ref.Name] = append(resolved[entry.ref.Name], entry)
	}
	visible := make([]tools.Tool, 0, len(resolved))
	for _, entries := range resolved {
		if len(entries) != 1 {
			continue
		}
		entry := entries[0]
		visible = append(visible, exposedMCPTool{exposure: e, ref: entry.ref, generation: entry.generation, spec: entry.spec})
	}
	slices.SortFunc(visible, func(a, b tools.Tool) int {
		return cmp.Compare(a.Definition().Name, b.Definition().Name)
	})
	return visible
}

func (e *MCPExposure) stateSnapshot() (map[MCPToolRef]struct{}, map[tools.ToolName]struct{}) {
	e.mu.Lock()
	desired := make(map[MCPToolRef]struct{}, len(e.desired))
	for ref := range e.desired {
		desired[ref] = struct{}{}
	}
	reserved := make(map[tools.ToolName]struct{}, len(e.reserved))
	for name := range e.reserved {
		reserved[name] = struct{}{}
	}
	e.mu.Unlock()
	return desired, reserved
}

func (e *MCPExposure) isActive(ref MCPToolRef) bool {
	e.mu.Lock()
	_, desired := e.desired[ref]
	_, reserved := e.reserved[ref.Name]
	e.mu.Unlock()
	return desired && !reserved
}

func (e *MCPExposure) search(_ context.Context, args MCPSearchArgs) ([]MCPSearchResult, error) {
	limit := args.Limit
	if limit == 0 {
		limit = defaultMCPSearchResults
	}
	if limit < 1 || limit > maxMCPSearchResults {
		return nil, tools.Validation(ToolNameMCPSearch.String(), fmt.Sprintf("limit must be between 1 and %d", maxMCPSearchResults))
	}
	query := strings.ToLower(strings.TrimSpace(args.Query))
	type ranked struct {
		entry mcpCatalogEntry
		score int
	}
	matches := make([]ranked, 0)
	for _, entry := range e.registry.catalogSnapshot() {
		score, ok := mcpSearchScore(query, entry)
		if ok {
			matches = append(matches, ranked{entry: entry, score: score})
		}
	}
	slices.SortFunc(matches, func(a, b ranked) int {
		if byScore := cmp.Compare(b.score, a.score); byScore != 0 {
			return byScore
		}
		if byName := cmp.Compare(a.entry.ref.Name, b.entry.ref.Name); byName != 0 {
			return byName
		}
		return cmp.Compare(a.entry.ref.Connection, b.entry.ref.Connection)
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	results := make([]MCPSearchResult, len(matches))
	for i, match := range matches {
		results[i] = MCPSearchResult{Ref: match.entry.ref, Description: clipRunes(match.entry.spec.Description, maxMCPSearchDescription)}
	}
	return results, nil
}

func (e *MCPExposure) activate(_ context.Context, args MCPActivateArgs) (MCPToolRef, error) {
	ref := MCPToolRef(args)
	if err := validateMCPToolRef(ref); err != nil {
		return MCPToolRef{}, err
	}
	catalog := e.registry.catalogSnapshot()
	found := false
	for _, entry := range catalog {
		if entry.ref == ref {
			found = true
			break
		}
	}
	if !found {
		return MCPToolRef{}, tools.NotFound(ToolNameMCPActivate.String(), "reference is not a current connected MCP definition; search again")
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if _, blocked := e.reserved[ref.Name]; blocked {
		return MCPToolRef{}, tools.Permission(ToolNameMCPActivate.String(), fmt.Sprintf("tool name %q is reserved by a direct or control tool", ref.Name))
	}
	if _, exists := e.desired[ref]; exists {
		return ref, nil
	}
	for desired := range e.desired {
		if desired.Name == ref.Name {
			return MCPToolRef{}, tools.Permission(ToolNameMCPActivate.String(), fmt.Sprintf("tool name %q is already desired from connection %q", ref.Name, desired.Connection))
		}
	}
	if len(e.desired) >= maxMCPDesiredTools {
		return MCPToolRef{}, tools.Budget(ToolNameMCPActivate.String(), fmt.Sprintf("desired MCP tool limit (%d) reached", maxMCPDesiredTools))
	}
	e.desired[ref] = struct{}{}
	return ref, nil
}

func (e *MCPExposure) deactivate(_ context.Context, args MCPDeactivateArgs) (MCPToolRef, error) {
	ref := MCPToolRef(args)
	if err := validateMCPToolRef(ref); err != nil {
		return MCPToolRef{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, exists := e.desired[ref]; !exists {
		return MCPToolRef{}, tools.NotFound(ToolNameMCPDeactivate.String(), "reference is not desired")
	}
	delete(e.desired, ref)
	return ref, nil
}

func validateMCPToolRef(ref MCPToolRef) error {
	if !validToolName.MatchString(ref.Connection) {
		return tools.Validation("mcp tool reference", "connection must match ^[a-z][a-z0-9_]{1,63}$")
	}
	if !validToolName.MatchString(ref.Name.String()) {
		return tools.Validation("mcp tool reference", "name must match ^[a-z][a-z0-9_]{1,63}$")
	}
	hash, ok := strings.CutPrefix(ref.DefinitionHash, mcpDefinitionHashVersion)
	if !ok || len(hash) != sha256.Size*2 {
		return tools.Validation("mcp tool reference", "definition_hash must be a v1 SHA-256 reference")
	}
	if _, err := hex.DecodeString(hash); err != nil {
		return tools.Validation("mcp tool reference", "definition_hash must be a v1 SHA-256 reference")
	}
	return nil
}

func hashMCPDefinition(connection string, spec tools.ToolSpec) (string, error) {
	canonical := struct {
		Version           int                   `json:"version"`
		Connection        string                `json:"connection"`
		Name              tools.ToolName        `json:"name"`
		Description       string                `json:"description"`
		Parameters        any                   `json:"parameters"`
		Mutates           bool                  `json:"mutates"`
		WorkspaceAccess   tools.WorkspaceAccess `json:"workspace_access"`
		AffectsWorkspace  bool                  `json:"affects_workspace"`
		DispatchBarrier   bool                  `json:"dispatch_barrier"`
		WorkspaceScopeAll bool                  `json:"workspace_scope_all"`
	}{
		Version:           1,
		Connection:        connection,
		Name:              spec.Name,
		Description:       spec.Description,
		Parameters:        spec.Parameters,
		Mutates:           spec.Mutates,
		WorkspaceAccess:   spec.Access(),
		AffectsWorkspace:  spec.AffectsWorkspace,
		DispatchBarrier:   spec.DispatchBarrier,
		WorkspaceScopeAll: true,
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("canonicalize MCP tool %q definition: %w", spec.Name, err)
	}
	digest := sha256.Sum256(encoded)
	return mcpDefinitionHashVersion + hex.EncodeToString(digest[:]), nil
}

func mcpSearchScore(query string, entry mcpCatalogEntry) (int, bool) {
	if query == "" {
		return 0, true
	}
	name := strings.ToLower(entry.ref.Name.String())
	connection := strings.ToLower(entry.ref.Connection)
	description := strings.ToLower(entry.spec.Description)
	score := 0
	for _, term := range strings.Fields(query) {
		switch {
		case name == term:
			score += 100
		case strings.HasPrefix(name, term):
			score += 50
		case strings.Contains(name, term):
			score += 25
		case strings.Contains(connection, term):
			score += 10
		case strings.Contains(description, term):
			score += 5
		default:
			return 0, false
		}
	}
	return score, true
}

func clipRunes(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit])
}

func sortMCPToolRefs(refs []MCPToolRef) {
	slices.SortFunc(refs, func(a, b MCPToolRef) int {
		if byConnection := cmp.Compare(a.Connection, b.Connection); byConnection != 0 {
			return byConnection
		}
		if byName := cmp.Compare(a.Name, b.Name); byName != 0 {
			return byName
		}
		return cmp.Compare(a.DefinitionHash, b.DefinitionHash)
	})
}

var _ tools.Source = mcpExposureSource{}
var _ tools.Tool = exposedMCPTool{}
