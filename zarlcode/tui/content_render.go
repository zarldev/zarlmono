package tui

import (
	"bytes"
	"container/list"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"time"

	"github.com/charmbracelet/x/ansi"

	programtools "github.com/zarldev/zarlmono/zkit/agent/tools/program"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// contentKind identifies the semantic content being rendered. Timeline items
// still own interaction state (collapse, status, selection), but prose-like and
// structured bodies flow through this shared renderer so wrapping, truncation,
// rails, nesting, and theme-coloured output stay consistent.
type contentKind int

const (
	contentPlain contentKind = iota
	contentMarkdown
	contentStatus
	contentToolResult
	contentThinking
	contentCode
	contentDiff
	contentPlan
)

// contentTone applies a uniform colour mood to rendered content. It replaces
// ad-hoc style func+stripANSI combinations so the render cache can key on an
// enum value rather than an opaque function pointer.
type contentTone int

const (
	toneNormal contentTone = iota
	toneMuted
)

type contentBlock struct {
	kind contentKind

	// text is the raw body for plain/markdown/tool/thinking/diff blocks.
	text string
	// toolName and hint let contentToolResult choose a safe semantic renderer.
	// hint is usually the compact argument shown in the tool row, e.g. a path.
	toolName string
	hint     string
	// data is the tool's typed structured result (code.GrepResult, LsResult,
	// GlobResult). When set, contentToolResult renders from these fields instead
	// of re-parsing text by tool name. Nil for legacy/string results and restored
	// sessions, which fall back to the text path.
	data any
	// syntax is the optional code-fence language for contentCode blocks.
	syntax string

	// plan is the structured body for contentPlan blocks.
	plan code.Plan

	// rail is an outer per-line prefix such as the user/assistant gutter.
	rail string

	// bodyPrefix is part of the content body, for example tool-result or
	// thinking indentation. It participates in wrap-width calculation.
	bodyPrefix string
	// firstPrefix and continuationPrefix are semantic body prefixes for wrapped
	// rows such as numbered plan steps. They are applied before style, so status
	// colours cover both the prefix and wrapped content.
	firstPrefix        string
	continuationPrefix string

	// depth and nested mirror timeline item layout. They are applied after the
	// semantic body is rendered.
	depth  int
	nested bool

	// maxLines truncates expanded bodies. Zero means no truncation.
	maxLines int

	// markdown points at a streaming renderer when the block is live assistant
	// content. Nil falls back to a normal full markdown render.
	markdown *streamingMarkdown

	// style is applied after bodyPrefix and before rail/nesting/depth.
	style func(string) string

	// stripANSI removes any ANSI emitted by the semantic renderer before style is
	// applied. This is useful for monochrome markdown blocks such as thinking:
	// parse markdown structure, then render all resulting text in one muted colour.
	stripANSI bool

	// cacheKey opts a static preview into the shared render cache. Leave empty for
	// live/streaming content and interactive timeline rows already cached by item
	// version. The rendered text hash is still part of the key, so stale file
	// contents don't collide when a preview reloads.
	cacheKey string

	// lineNumbers adds a source-aligned line-number gutter to contentCode blocks.
	// Each source line maps to one rendered line so counts stay accurate.
	lineNumbers bool

	// tone applies a uniform colour mood (e.g. muted) to the final rendered
	// lines. Prefer this over style+stripANSI for cache eligibility.
	tone contentTone
}

func renderContentBlock(width int, b contentBlock) []string {
	if key, ok := contentCacheKeyFor(width, b); ok {
		if lines, hit := getContentRenderCache(key); hit {
			return cloneLines(lines)
		}
		lines := renderContentBlockUncached(width, b)
		putContentRenderCache(key, lines)
		return cloneLines(lines)
	}
	return renderContentBlockUncached(width, b)
}

func renderContentBlockUncached(width int, b contentBlock) []string {
	var lines []string
	switch b.kind {
	case contentMarkdown:
		lines = renderMarkdownContent(width, b)
	case contentCode:
		lines = renderCodeContent(width, b)
	case contentToolResult:
		lines = renderToolResultContent(width, b)
	case contentStatus:
		lines = []string{b.text}
	case contentDiff:
		lines = renderDiffContent(b.text, renderAvailableContentWidth(width, b), b.maxLines)
	case contentPlan:
		lines = renderPlanContent(width, b)
	default:
		lines = wrapText(b.text, renderContentWidth(width, b))
	}

	if b.kind != contentDiff && b.kind != contentPlan {
		lines = truncateContentLines(lines, b.maxLines, "")
	}
	if b.bodyPrefix != "" {
		lines = prefixLines(lines, b.bodyPrefix)
	}
	if b.firstPrefix != "" || b.continuationPrefix != "" {
		cont := b.continuationPrefix
		if cont == "" {
			cont = b.firstPrefix
		}
		for i, line := range lines {
			prefix := cont
			if i == 0 {
				prefix = b.firstPrefix
			}
			lines[i] = prefix + line
		}
	}
	if b.stripANSI {
		for i, line := range lines {
			lines[i] = ansi.Strip(line)
		}
	}
	// Apply content tone before the style func so tone+style compose cleanly.
	if b.tone == toneMuted {
		for i, line := range lines {
			lines[i] = palette.Muted.On(line)
		}
	}
	if b.style != nil {
		for i, line := range lines {
			lines[i] = b.style(line)
		}
	}
	if b.rail != "" {
		lines = prefixLines(lines, b.rail)
	}
	if b.nested {
		lines = prefixLines(lines, nestPad)
	}
	return indentLines(lines, b.depth)
}

type contentRenderCacheKey struct {
	cacheKey        string
	kind            contentKind
	width           int
	textHash        uint64
	textLen         int
	toolName        string
	hint            string
	syntax          string
	rail            string
	bodyPrefix      string
	firstPrefix     string
	continuation    string
	depth           int
	nested          bool
	maxLines        int
	stripANSI       bool
	tone            contentTone
	themeGeneration uint64
	lineNumbers     bool
}

// contentRenderCacheMaxEntries bounds the shared content-render cache.
// The dashboard, file viewer, and timeline together routinely hold more
// than the old 64-entry ceiling; with LRU eviction an oversized working
// set costs one eviction per insert instead of wiping every entry and
// re-rendering the lot.
const contentRenderCacheMaxEntries = 256

var contentRenderCache = newContentLRU(contentRenderCacheMaxEntries)

// contentLRU is a mutex-guarded LRU over rendered content blocks, keyed
// by (content, width, style…). A theme switch bumps themeGen, which
// invalidates every baked-in colour, so the whole cache resets on
// generation mismatch.
type contentLRU struct {
	mu      sync.Mutex
	max     int
	gen     uint64
	ll      *list.List // front = most recently used
	entries map[contentRenderCacheKey]*list.Element
}

type contentLRUNode struct {
	key   contentRenderCacheKey
	lines []string
}

func newContentLRU(capacity int) *contentLRU {
	return &contentLRU{
		max:     capacity,
		ll:      list.New(),
		entries: make(map[contentRenderCacheKey]*list.Element),
	}
}

// reset clears the cache to a known-empty state at the current theme
// generation. Used by tests that need a clean cache.
func (c *contentLRU) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen = themeGen
	c.ll.Init()
	c.entries = make(map[contentRenderCacheKey]*list.Element)
}

// resetForGenLocked clears the cache when the theme generation changed.
// Caller holds c.mu.
func (c *contentLRU) resetForGenLocked() {
	if c.gen == themeGen {
		return
	}
	c.gen = themeGen
	c.ll.Init()
	c.entries = make(map[contentRenderCacheKey]*list.Element)
}

func (c *contentLRU) get(key contentRenderCacheKey) ([]string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resetForGenLocked()
	el, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	c.ll.MoveToFront(el)
	node, _ := el.Value.(*contentLRUNode)
	return node.lines, true
}

func (c *contentLRU) put(key contentRenderCacheKey, lines []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resetForGenLocked()
	if el, ok := c.entries[key]; ok {
		node, _ := el.Value.(*contentLRUNode)
		node.lines = cloneLines(lines)
		c.ll.MoveToFront(el)
		return
	}
	c.entries[key] = c.ll.PushFront(&contentLRUNode{key: key, lines: cloneLines(lines)})
	if c.ll.Len() > c.max {
		if oldest := c.ll.Back(); oldest != nil {
			c.ll.Remove(oldest)
			node, _ := oldest.Value.(*contentLRUNode)
			delete(c.entries, node.key)
		}
	}
}

func contentCacheKeyFor(width int, b contentBlock) (contentRenderCacheKey, bool) {
	if b.cacheKey == "" || b.markdown != nil || b.style != nil || b.kind == contentPlan {
		return contentRenderCacheKey{}, false
	}
	return contentRenderCacheKey{
		cacheKey:        b.cacheKey,
		kind:            b.kind,
		width:           width,
		textHash:        hashContentText(b.text),
		textLen:         len(b.text),
		toolName:        b.toolName,
		hint:            b.hint,
		syntax:          b.syntax,
		rail:            b.rail,
		bodyPrefix:      b.bodyPrefix,
		firstPrefix:     b.firstPrefix,
		continuation:    b.continuationPrefix,
		depth:           b.depth,
		nested:          b.nested,
		maxLines:        b.maxLines,
		stripANSI:       b.stripANSI,
		tone:            b.tone,
		themeGeneration: themeGen,
		lineNumbers:     b.lineNumbers,
	}, true
}

func getContentRenderCache(key contentRenderCacheKey) ([]string, bool) {
	return contentRenderCache.get(key)
}

func putContentRenderCache(key contentRenderCacheKey, lines []string) {
	contentRenderCache.put(key, lines)
}

func cloneLines(lines []string) []string {
	return append([]string(nil), lines...)
}

func hashContentText(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

func renderMarkdownContent(width int, b contentBlock) []string {
	w := renderContentWidth(width, b)
	var rendered string
	if b.markdown != nil {
		rendered = b.markdown.render(b.text, w)
	} else {
		rendered = renderMarkdown(b.text, w)
	}
	return strings.Split(rendered, "\n")
}

func renderCodeContent(width int, b contentBlock) []string {
	md := fencedMarkdownCode(b.text, b.syntax)
	raw := renderMarkdown(md, renderAvailableContentWidth(width, b))
	lines := strings.Split(raw, "\n")
	// Strip outer blank margin lines the glamour chroma renderer adds around the
	// code block. We need the exact rendered code lines — no block padding.
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[0])) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return []string{""}
	}
	if b.lineNumbers {
		return addCodeLineNumbers(lines)
	}
	return lines
}

func addCodeLineNumbers(lines []string) []string {
	numW := max(len(strconv.Itoa(len(lines))), 3)
	out := make([]string, len(lines))
	for i, line := range lines {
		line = stripCodeBlockChrome(line)
		out[i] = fmt.Sprintf("%s%*d │ %s", palette.Subtle.FG(), numW, i+1, line)
	}
	return out
}

// stripCodeBlockChrome removes glamour's per-line block-level ANSI: the leading
// background-colour SGR and the trailing reset, leaving only the highlighted
// code text.
func stripCodeBlockChrome(line string) string {
	line = stripLeadingANSISGR(line, 48) // background: CSI 48;2;R;G;Bm
	line = strings.TrimSuffix(line, "\x1b[0m")
	return line
}

func stripLeadingANSISGR(line string, param int) string {
	prefix := fmt.Sprintf("\x1b[%d;2;", param)
	if strings.HasPrefix(line, prefix) {
		if idx := strings.IndexByte(line[len(prefix):], 'm'); idx >= 0 {
			return line[len(prefix)+idx+1:]
		}
	}
	return line
}

func renderToolResultContent(width int, b contentBlock) []string {
	// Prefer the tool's typed result: render grep/ls/glob from their structured
	// fields instead of re-parsing the formatted string by tool name. Falls
	// through to the text path for legacy/string results and restored sessions.
	if lines := renderTypedToolResult(width, b); lines != nil {
		return lines
	}
	switch toolResultRenderKind(b.toolName) {
	case contentMarkdown:
		return renderMarkdownContent(width, b)
	case contentCode:
		if b.syntax == "" && !isShellTool(b.toolName) {
			b.syntax = inferSyntaxFromHint(b.hint)
		}
		return renderCodeContent(width, b)
	default:
		if pretty, ok := prettyJSON(b.text); ok {
			b.text = pretty
			b.syntax = "json"
			return renderCodeContent(width, b)
		}
		if isSearchTool(b.toolName) {
			return renderSearchResults(width, b)
		}
		if isListTool(b.toolName) {
			return renderCompactList(width, b)
		}
		return wrapText(b.text, renderContentWidth(width, b))
	}
}

func isShellTool(name string) bool {
	switch strings.ToLower(name) {
	case "bash", "shell", "sh":
		return true
	}
	return false
}

func isSearchTool(name string) bool {
	switch strings.ToLower(name) {
	case "grep", "search", "ripgrep":
		return true
	}
	return false
}

func isListTool(name string) bool {
	switch strings.ToLower(name) {
	case "glob", "ls", "list", "list_dir":
		return true
	}
	return false
}

// renderSearchResults colorizes grep-style output: the path portion before the
// first colon gets the Secondary colour, the rest stays muted. Blank lines and
// non-matching lines pass through uncoloured.
func renderSearchResults(width int, b contentBlock) []string {
	rawLines := strings.Split(b.text, "\n")
	out := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		out = append(out, colorizeSearchLine(line))
	}
	return wrapText(strings.Join(out, "\n"), renderContentWidth(width, b))
}

func colorizeSearchLine(line string) string {
	line = strings.TrimRight(line, "\r")
	if line == "" {
		return ""
	}
	// Find the first colon that looks like a path separator (has no space before it).
	idx := strings.IndexByte(line, ':')
	if idx < 0 || idx == 0 || strings.Contains(line[:idx], " ") {
		return palette.Muted.On(line)
	}
	return palette.Secondary.On(line[:idx]) + ":" + palette.Muted.On(line[idx+1:])
}

// renderCompactList renders one-path-per-line output (glob, ls) with muted
// styling and a compact bullet prefix.
func renderCompactList(_ int, b contentBlock) []string {
	lines := strings.Split(b.text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, palette.Muted.On("  "+line))
	}
	if len(out) == 0 {
		return []string{palette.Muted.On("  (empty)")}
	}
	return out
}

// renderTypedToolResult renders a structured tool result from its typed fields
// instead of re-parsing the formatted string by tool name. Returns nil when the
// data isn't a recognised structured result, so the caller falls back to the
// text path (legacy/string results, restored sessions whose typed data wasn't
// persisted). width is the available content width after body-prefix reserve.
func renderTypedToolResult(width int, b contentBlock) []string {
	cw := renderAvailableContentWidth(width, b)
	switch r := b.data.(type) {
	case code.GrepResult:
		return renderGrepResultLines(cw, r)
	case code.LsResult:
		return renderLsResultLines(cw, r)
	case code.GlobResult:
		return renderGlobResultLines(cw, r)
	case programtools.Result:
		return renderProgramResultLines(cw, r)
	default:
		return nil
	}
}

// renderGrepResultLines renders grep hits grouped by file: a Secondary file
// header, then each hit as a Muted line number + the matched text, with long
// text hard-wrapped under the gutter. Structured — no colon heuristic.
func renderGrepResultLines(width int, r code.GrepResult) []string {
	if len(r.Hits) == 0 {
		return []string{palette.Muted.On("(no matches)")}
	}
	out := make([]string, 0, len(r.Hits)+1)
	current := ""
	for _, h := range r.Hits {
		if h.File != current {
			out = append(out, palette.Secondary.On(h.File))
			current = h.File
		}
		gutter := strconv.Itoa(h.Line) + ": "
		gw := ansi.StringWidth(gutter)
		for i, seg := range strings.Split(ansi.Hardwrap(h.Text, max(1, width-2-gw), true), "\n") {
			lead := gutter
			if i > 0 {
				lead = strings.Repeat(" ", gw)
			}
			out = append(out, "  "+palette.Muted.On(lead)+palette.Fg.On(seg))
		}
	}
	if r.Truncated {
		out = append(out, palette.Muted.On("  … truncated"))
	}
	return out
}

// renderLsResultLines renders ls entries one per line: dirs in Secondary with a
// trailing slash, files/symlinks in Fg, size/kind in Muted.
func renderLsResultLines(width int, r code.LsResult) []string {
	if len(r.Entries) == 0 {
		return []string{palette.Muted.On("(empty)")}
	}
	out := make([]string, 0, len(r.Entries))
	for _, e := range r.Entries {
		name, style, suffix := e.Name, palette.Fg.On, "  "+palette.Muted.On(humanBytes(e.Size))
		switch e.Type {
		case "dir":
			name, style, suffix = e.Name+"/", palette.Secondary.On, "  "+palette.Muted.On("(dir)")
		case "symlink":
			suffix = "  " + palette.Muted.On(humanBytes(e.Size)+" (symlink)")
		}
		out = append(out, ansi.Truncate(style(name)+suffix, width, "…"))
	}
	return out
}

// renderGlobResultLines renders glob matches one path per line: dirs Secondary,
// files Fg, size Muted.
func renderGlobResultLines(width int, r code.GlobResult) []string {
	if len(r.Entries) == 0 {
		return []string{palette.Muted.On("(no matches)")}
	}
	out := make([]string, 0, len(r.Entries))
	for _, e := range r.Entries {
		style, suffix := palette.Fg.On, "  "+palette.Muted.On(humanBytes(e.Size))
		if e.Dir {
			style, suffix = palette.Secondary.On, "  "+palette.Muted.On("(dir)")
		}
		out = append(out, ansi.Truncate(style(e.Path)+suffix, width, "…"))
	}
	return out
}

func renderProgramResultLines(width int, r programtools.Result) []string {
	body, ok := programOutputText(r.Output)
	if !ok {
		return []string{palette.Muted.On("(empty program output)")}
	}
	if lines, ok := renderProgramCallResults(r.Output); ok {
		return appendProgramStats(lines, r.Stats)
	}
	lines := renderProgramOutputLines(width, body)
	return appendProgramStats(lines, r.Stats)
}
func appendProgramStats(lines []string, stats programtools.Stats) []string {
	summary := fmt.Sprintf("%d calls", stats.ToolCalls)
	if stats.ParallelBatches > 0 {
		summary += fmt.Sprintf(", %d parallel", stats.ParallelBatches)
	}
	if stats.Duration > 0 {
		summary += ", " + stats.Duration.Round(time.Millisecond).String()
	}
	return append(lines, palette.Muted.On("program: "+summary))
}

func renderProgramCallResults(output any) ([]string, bool) {
	items, ok := output.([]any)
	if !ok || len(items) == 0 {
		return nil, false
	}
	out := make([]string, 0, len(items)*2)
	for i, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, false
		}
		okValue, hasOK := m["ok"].(bool)
		if !hasOK {
			return nil, false
		}
		prefix := palette.Success.On("✓")
		if !okValue {
			prefix = palette.Error.On("✗")
		}
		label := programCallLabel(i, stringValue(m["name"]), mapValue(m["args"]))
		if !okValue {
			if msg, _ := m["error"].(string); strings.TrimSpace(msg) != "" {
				out = append(out, prefix+" "+palette.Warning.On(label)+": "+palette.Muted.On(firstLine(msg)))
				continue
			}
		}
		out = append(out, prefix+" "+palette.Secondary.On(label))
		if line := renderProgramDataSummary(m["data"]); line != "" {
			out = append(out, "  "+palette.Muted.On(line))
		}
	}
	return out, true
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

func mapValue(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func programCallLabel(i int, name string, args map[string]any) string {
	if name == "" {
		return fmt.Sprintf("result %d", i+1)
	}
	if hint := toolArgHint(name, args); hint != "" {
		return name + "  " + hint
	}
	return name
}

func renderProgramDataSummary(data any) string {
	if data == nil {
		return ""
	}
	switch v := data.(type) {
	case string:
		return ansi.Truncate(firstLine(v), 96, "…")
	case map[string]any:
		return summarizeProgramMap(v)
	case []any:
		return fmt.Sprintf("%d items", len(v))
	default:
		text, ok := programOutputText(v)
		if !ok {
			return ""
		}
		return compactJSONPreview(text, 96)
	}
}

func summarizeProgramMap(m map[string]any) string {
	if payload, ok := m["Payload"].(map[string]any); ok {
		if files, ok := payload["files"].([]any); ok {
			return fmt.Sprintf("file_map: %d files", len(files))
		}
	}
	if hits, ok := m["Hits"].([]any); ok {
		return fmt.Sprintf("grep: %d hits", len(hits))
	}
	if entries, ok := m["Entries"].([]any); ok {
		return fmt.Sprintf("%d entries", len(entries))
	}
	if matches, ok := m["Matches"].([]any); ok {
		return fmt.Sprintf("%d matches", len(matches))
	}
	if files, ok := m["files"].([]any); ok {
		return fmt.Sprintf("%d files", len(files))
	}
	return compactJSONPreview(mustJSON(m), 96)
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func compactJSONPreview(text string, width int) string {
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		return ansi.Truncate(text, width, "…")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ansi.Truncate(text, width, "…")
	}
	return ansi.Truncate(string(b), width, "…")
}

func programOutputText(output any) (string, bool) {
	switch v := output.(type) {
	case nil:
		return "", false
	case string:
		return v, strings.TrimSpace(v) != ""
	default:
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return fmt.Sprint(v), true
		}
		return string(b), true
	}
}

func renderProgramOutputLines(width int, text string) []string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return []string{palette.Muted.On("(empty program output)")}
	}
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return renderCodeContent(width, contentBlock{kind: contentCode, text: trimmed, syntax: "json"})
	}
	return wrapText(trimmed, width)
}

func prettyJSON(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || (!strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[")) {
		return "", false
	}
	if !json.Valid([]byte(trimmed)) {
		return "", false
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(trimmed), "", "  "); err != nil {
		return "", false
	}
	return buf.String(), true
}

func toolResultRenderKind(name string) contentKind {
	switch strings.ToLower(name) {
	case "load_skill":
		return contentMarkdown
	case "read", "read_file", "view", "cat":
		return contentCode
	case "bash", "shell", "sh":
		return contentCode // terminal-style block; no syntax highlighting
	case "grep", "search", "ripgrep":
		return contentPlain // rendered through renderSearchResults below
	case "glob", "ls", "list", "list_dir":
		return contentPlain // rendered through renderCompactList below
	default:
		return contentPlain
	}
}

func fencedMarkdownCode(text, syntax string) string {
	fence := "```"
	for strings.Contains(text, fence) {
		fence += "`"
	}
	if syntax != "" {
		return fence + syntax + "\n" + text + "\n" + fence
	}
	return fence + "\n" + text + "\n" + fence
}

func inferSyntaxFromHint(hint string) string {
	path := strings.TrimSpace(hint)
	if strings.HasPrefix(path, "$ ") {
		return ""
	}
	if i := strings.IndexByte(path, ':'); i >= 0 && !strings.Contains(path[:i], string(filepath.Separator)) {
		path = strings.TrimSpace(path[i+1:])
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".ts", ".mts", ".cts":
		return "typescript"
	case ".jsx":
		return "jsx"
	case ".tsx":
		return "tsx"
	case ".py":
		return "python"
	case ".sh", ".bash", ".zsh":
		return "bash"
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	case ".md", ".markdown":
		return "markdown"
	case ".sql":
		return "sql"
	case ".html", ".htm":
		return "html"
	case ".css":
		return "css"
	case ".diff", ".patch":
		return "diff"
	default:
		return ""
	}
}

func renderContentWidth(width int, b contentBlock) int {
	return contentWidth(renderAvailableContentWidth(width, b))
}

func renderAvailableContentWidth(width int, b contentBlock) int {
	rowPrefixWidth := max(ansi.StringWidth(b.firstPrefix), ansi.StringWidth(b.continuationPrefix))
	reserve := ansi.StringWidth(b.rail) + ansi.StringWidth(b.bodyPrefix) + rowPrefixWidth + b.depth*2
	if b.nested {
		reserve += ansi.StringWidth(nestPad)
	}
	if width-reserve < 1 {
		return 1
	}
	return width - reserve
}

func truncateContentLines(lines []string, maxLines int, morePrefix string) []string {
	if maxLines <= 0 || len(lines) <= maxLines {
		return lines
	}
	more := len(lines) - maxLines
	out := append([]string(nil), lines[:maxLines]...)
	out = append(out, morePrefix+palette.Muted.On("… "+strconv.Itoa(more)+" more lines"))
	return out
}

func renderDiffContent(diff string, width, maxLines int) []string {
	body := strings.Split(strings.TrimRight(diff, "\n"), "\n")
	// Drop the recorder's leading "@@ path @@" file header — the path is already
	// on the row above, so it's just noise in the body.
	if len(body) > 0 && strings.HasPrefix(body[0], "@@") {
		body = body[1:]
	}
	lines := make([]string, 0, len(body))
	for _, ln := range body {
		colorize := diffLineColorizer(ln)
		// Hard-wrap, not word-wrap: a diff is preformatted, so break at the column
		// limit and preserve every cell (including the leading marker) instead of
		// reflowing. Without this a long diff line was silently clipped at draw.
		for seg := range strings.SplitSeq(ansi.Hardwrap(ln, width, true), "\n") {
			lines = append(lines, colorize(seg))
		}
	}
	return truncateContentLines(lines, maxLines, "")
}

func renderPlanContent(width int, b contentBlock) []string {
	// Preserve the existing inline plan measure: planInlineLines owns its own
	// leading two-space body indentation, while nested/depth decoration is applied
	// below by renderContentBlock.
	return planInlineLines(b.plan, contentWidth(width-4-b.depth*2))
}

// --- Helper constructors -------------------------------------------------------

type contentOption func(*contentBlock)

func withRail(r string) contentOption               { return func(b *contentBlock) { b.rail = r } }
func withBodyPrefix(p string) contentOption         { return func(b *contentBlock) { b.bodyPrefix = p } }
func withDepth(d int) contentOption                 { return func(b *contentBlock) { b.depth = d } }
func withMaxLines(n int) contentOption              { return func(b *contentBlock) { b.maxLines = n } }
func withCacheKey(k string) contentOption           { return func(b *contentBlock) { b.cacheKey = k } }
func withLineNumbers() contentOption                { return func(b *contentBlock) { b.lineNumbers = true } }
func withData(d any) contentOption                  { return func(b *contentBlock) { b.data = d } }
func withStyle(s func(string) string) contentOption { return func(b *contentBlock) { b.style = s } }
func withFirstPrefix(first, cont string) contentOption {
	return func(b *contentBlock) { b.firstPrefix = first; b.continuationPrefix = cont }
}

func applyOpts(b *contentBlock, opts []contentOption) {
	for _, opt := range opts {
		opt(b)
	}
}

func renderPlain(width int, text string, opts ...contentOption) []string {
	b := contentBlock{kind: contentPlain, text: text}
	applyOpts(&b, opts)
	return renderContentBlock(width, b)
}

func renderMarkdownBlock(width int, text string, opts ...contentOption) []string {
	b := contentBlock{kind: contentMarkdown, text: text}
	applyOpts(&b, opts)
	return renderContentBlock(width, b)
}

func renderCodeBlock(width int, text, syntax string, opts ...contentOption) []string {
	b := contentBlock{kind: contentCode, text: text, syntax: syntax}
	applyOpts(&b, opts)
	return renderContentBlock(width, b)
}

func renderToolResult(width int, toolName, hint, text string, opts ...contentOption) []string {
	b := contentBlock{kind: contentToolResult, text: text, toolName: toolName, hint: hint}
	applyOpts(&b, opts)
	return renderContentBlock(width, b)
}
