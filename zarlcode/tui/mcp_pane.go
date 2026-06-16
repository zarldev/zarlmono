package tui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/prefs"
)

// mcpAuthKeyProvider namespaces an MCP server's bearer token as a provider
// name in the encrypted api_keys store, so MCP secrets share the vault path
// used for provider keys instead of sitting plaintext in the mcp_servers
// table. The "mcp:" prefix keeps them from colliding with real provider keys.
func mcpAuthKeyProvider(name string) string { return "mcp:" + name }

// mcpPane lists the persisted MCP servers and manages them: add a new server
// (stdio or http), delete one, or toggle whether it auto-connects at startup.
// Edits persist to state.db; they take effect on the next launch (the live
// connections are established in Launch), so the status line says "next
// launch" rather than implying a live reconnect.
type mcpPane struct {
	ctx     context.Context
	s       *engine.Settings
	servers []db.MCPServerRow
	cursor  int

	adding bool        // inline add-server form is open
	addEds [6]composer // name, transport, command, args, base url, auth token
	addIdx int

	status   string
	statusAt time.Time
}

var mcpAddLabels = [6]string{"name", "transport", "command", "args", "base url", "auth token"}

func newMCPPaneWithContext(ctx context.Context, s *engine.Settings) *mcpPane {
	if ctx == nil {
		ctx = context.Background()
	}
	d := &mcpPane{ctx: ctx, s: s}
	d.refresh()
	return d
}

// inSubMode reports whether the add form is open, so the host knows esc/left
// should cancel the form rather than return focus to the category nav.
func (d *mcpPane) inSubMode() bool { return d.adding }

// handlePaste inserts clipboard content into the focused add-form field.
// Content is already single-line (the settings surface strips newlines).
// No-op when the add form is closed.
func (d *mcpPane) handlePaste(content string) {
	if d.adding && d.addIdx >= 0 && d.addIdx < len(d.addEds) {
		d.addEds[d.addIdx].insert(content)
	}
}

func (d *mcpPane) refresh() {
	if d.s == nil || d.s.Store == nil {
		return
	}
	servers, err := d.s.Store.ListMCPServers(d.ctx)
	if err != nil {
		d.status, d.statusAt = "list: "+err.Error(), time.Now()
		return
	}
	d.servers = servers
	if d.cursor >= len(d.servers) {
		d.cursor = len(d.servers) - 1
	}
	if d.cursor < 0 {
		d.cursor = 0
	}
}

func (d *mcpPane) cur() (db.MCPServerRow, bool) {
	if d.cursor < 0 || d.cursor >= len(d.servers) {
		return db.MCPServerRow{}, false
	}
	return d.servers[d.cursor], true
}

func (d *mcpPane) footerHint() string {
	if d.adding {
		return keyLegend(keyHint{"tab", "field"}, keyHint{"enter", "next/save"}, keyHint{"esc", "cancel"})
	}
	return keyLegend(keyHint{"n", "new"}, keyHint{"x", "delete"}, keyHint{"t", "toggle"},
		keyHint{"↑↓", "move"}, keyHint{"esc", "back"})
}

// handleKey runs the inner logic and timestamps any status change so the
// host's footer toast can age it out.
func (d *mcpPane) handleKey(msg tea.KeyPressMsg) action {
	before := d.status
	a := d.handleKeyInner(msg)
	if d.status != before {
		d.statusAt = time.Now()
	}
	return a
}

func (d *mcpPane) handleKeyInner(msg tea.KeyPressMsg) action {
	if d.adding {
		return d.handleAddKey(msg)
	}
	switch msg.String() {
	case "esc", "q":
		return actionClose{}
	case "up", "k":
		if d.cursor > 0 {
			d.cursor--
		}
	case "down", "j":
		if d.cursor < len(d.servers)-1 {
			d.cursor++
		}
	case "n":
		d.adding = true
		d.addEds = [6]composer{}
		d.addIdx = 0
		d.status = ""
	case "x", "delete":
		d.deleteCur()
	case "t":
		d.toggleCur()
	}
	return actionNone{}
}

func (d *mcpPane) handleAddKey(msg tea.KeyPressMsg) action {
	return handleAddFormKey(msg, d.addEds[:], &d.addIdx, func() { d.adding = false }, d.submitAdd)
}

// submitAdd validates the form and upserts the server config. Transport must
// be stdio (command required) or http (base url required); args are split on
// whitespace.
func (d *mcpPane) submitAdd() {
	if d.s == nil || d.s.Store == nil {
		d.adding = false
		return
	}
	name := strings.TrimSpace(d.addEds[0].text())
	transport := strings.ToLower(strings.TrimSpace(d.addEds[1].text()))
	command := strings.TrimSpace(d.addEds[2].text())
	args := strings.Fields(d.addEds[3].text())
	baseURL := strings.TrimSpace(d.addEds[4].text())
	authToken := strings.TrimSpace(d.addEds[5].text())

	if name == "" {
		d.status = "name required"
		return
	}
	switch transport {
	case "stdio":
		if command == "" {
			d.status = "stdio: command required"
			return
		}
	case "http":
		if baseURL == "" {
			d.status = "http: base url required"
			return
		}
	default:
		d.status = "transport must be 'stdio' or 'http'"
		return
	}

	if d.s == nil || d.s.Svc == nil {
		d.status = "credential service unavailable"
		return
	}
	if authToken != "" {
		if err := d.s.Svc.SetKey(d.ctx, prefs.ScopeGlobal, mcpAuthKeyProvider(name), authToken); err != nil {
			d.status = "store auth token: " + err.Error()
			return
		}
	}
	row := db.MCPServerRow{
		Name:      name,
		Transport: transport,
		Command:   command,
		Args:      args,
		BaseURL:   baseURL,
		AuthToken: "", // secret lives in the vault under mcpAuthKeyProvider(name)
		Enabled:   true,
	}
	if err := d.s.Store.UpsertMCPServer(d.ctx, row); err != nil {
		d.status = "add: " + err.Error()
		return
	}
	d.adding = false
	d.status = name + " added (connects next launch)"
	d.refresh()
}

func (d *mcpPane) deleteCur() {
	srv, ok := d.cur()
	if !ok {
		return
	}
	if err := d.s.Store.DeleteMCPServer(d.ctx, srv.Name); err != nil {
		d.status = "delete: " + err.Error()
		return
	}
	// Drop the vaulted bearer token too (idempotent: no-op when there was
	// none). DeleteKey doesn't require a vault — it only removes the row.
	if d.s.Svc != nil {
		_ = d.s.Svc.DeleteKey(d.ctx, prefs.ScopeGlobal, mcpAuthKeyProvider(srv.Name))
	}
	d.status = srv.Name + " deleted"
	d.refresh()
}

func (d *mcpPane) toggleCur() {
	srv, ok := d.cur()
	if !ok {
		return
	}
	srv.Enabled = !srv.Enabled
	if err := d.s.Store.UpsertMCPServer(d.ctx, srv); err != nil {
		d.status = "toggle: " + err.Error()
		return
	}
	state := "enabled"
	if !srv.Enabled {
		state = "disabled"
	}
	d.status = srv.Name + " " + state
	d.refresh()
}

// detailLines renders the server list (or the add form) for the settings
// detail region.
func (d *mcpPane) detailLines(width int) []string {
	if d.adding {
		return d.addFormLines()
	}
	if len(d.servers) == 0 {
		return []string{palette.Muted.On("no mcp servers configured — press n to add one.")}
	}
	lines := make([]string, 0, len(d.servers))
	for i, srv := range d.servers {
		marker := "  "
		namecell := palette.Subtle.On(pad(srv.Name, 16))
		if i == d.cursor {
			marker = palette.Primary.On("▸ ")
			namecell = palette.Primary.On(pad(srv.Name, 16))
		}
		lines = append(lines, rowLayout(marker+namecell, d.tags(srv), width))
	}
	return lines
}

func (d *mcpPane) addFormLines() []string {
	lines := make([]string, 0, len(d.addEds)+3)
	lines = append(lines, palette.Assistant.On("add mcp server"),
		palette.Muted.On("transport: stdio (command+args) or http (base url[+auth token])"), "")
	for i := range d.addEds {
		label := pad(mcpAddLabels[i], 12)
		val := d.addEds[i].text()
		if i == d.addIdx {
			val = string(d.addEds[i].value[:d.addEds[i].cursor]) +
				palette.Primary.On("▏") + string(d.addEds[i].value[d.addEds[i].cursor:])
			label = palette.Primary.On(label)
		} else {
			label = palette.Subtle.On(label)
		}
		lines = append(lines, label+val)
	}
	return lines
}

// tags renders the per-server badges: transport, endpoint, and whether it
// auto-connects at startup.
func (d *mcpPane) tags(srv db.MCPServerRow) string {
	endpoint := srv.BaseURL
	if srv.Transport == "stdio" {
		endpoint = srv.Command
	}
	state := palette.Success.On("on")
	if !srv.Enabled {
		state = palette.Muted.On("off")
	}
	return palette.Muted.On(srv.Transport) + "  " + palette.Subtle.On(endpoint) + "  " + state
}

// draw renders the pane as a standalone centered dialog (used in tests).
func (d *mcpPane) draw(scr uv.Screen, area uv.Rectangle) {
	lines := append(d.detailLines(area.Dx()), "", d.footerHint())
	drawDialogBox(scr, area, "mcp servers", lines)
}
