package tui

import "strings"

// streamingMarkdown caches a "stable prefix" glamour render so each
// streaming flush only re-renders the trailing portion of the document
// instead of the whole thing.
//
// The boundary between "stable" and "trailing" is found by
// findSafeMarkdownBoundary: a position immediately after a blank line at
// which we can prove no markdown construct is open (fenced code block,
// list, table, block quote, setext header, HTML block, link ref def).
//
// Two renders concatenated are NOT generally equal to a single render of
// the whole document — glamour resets wrap state between calls — so the
// boundary check is deliberately conservative: on the slightest doubt it
// returns -1 and the caller falls back to a full render, leaving the
// cache untouched.
//
// Invariants:
//   - stablePrefix is always a literal byte prefix of the most recently
//     rendered content; if new content doesn't have it as a prefix the
//     cache is dropped.
//   - stableRender is the glamour render of stablePrefix alone, margins
//     trimmed for clean concatenation.
//   - width is the wrap width that produced stableRender; a width change
//     drops the cache.
//   - gen is the themeGen at render time; a theme switch drops the cache
//     so the stable render recolours with the new palette.
type streamingMarkdown struct {
	width        int
	gen          uint64 // themeGen at last render; mismatch drops the cache
	stablePrefix string
	stableRender string
}

func (s *streamingMarkdown) reset() {
	s.width = 0
	s.gen = 0
	s.stablePrefix = ""
	s.stableRender = ""
}

// render returns the markdown render of content at width, reusing the
// cached stable-prefix render when safe. On any uncertainty it falls
// back to a full render and leaves the cache untouched (or drops it).
func (s *streamingMarkdown) render(content string, width int) string {
	full := func() string { return renderMarkdown(content, width) }

	// Width change OR theme change OR content not a prefix-extension: drop
	// the cache, full render, and try to seed a fresh boundary on this call.
	if width != s.width || s.gen != themeGen || !strings.HasPrefix(content, s.stablePrefix) {
		s.reset()
		s.width = width
		s.gen = themeGen
		out := full()
		s.tryAdvanceFromEmpty(content, width)
		return out
	}

	boundary := findSafeMarkdownBoundary(content)
	if boundary < 0 {
		// No safe boundary yet. Full render; leave the cache alone.
		return full()
	}

	if boundary <= len(s.stablePrefix) {
		// Cached prefix already covers an at-least-as-late boundary.
		trail := content[len(s.stablePrefix):]
		return glueRenders(s.stableRender, s.renderTrailing(trail))
	}

	// A new chunk of safe content: render it, append to the stable
	// render, promote the boundary, then render the remaining trail.
	newChunk := content[len(s.stablePrefix):boundary]
	s.stableRender = glueRenders(s.stableRender, s.renderTrailing(newChunk))
	s.stablePrefix = content[:boundary]

	trail := content[boundary:]
	if trail == "" {
		return s.stableRender
	}
	return glueRenders(s.stableRender, s.renderTrailing(trail))
}

// tryAdvanceFromEmpty seeds the cache from a fresh state after a full
// render. If a safe boundary exists inside content, render that prefix
// once and cache it so the next flush can skip the full work.
func (s *streamingMarkdown) tryAdvanceFromEmpty(content string, width int) {
	boundary := findSafeMarkdownBoundary(content)
	if boundary <= 0 {
		return
	}
	prefix := content[:boundary]
	s.stablePrefix = prefix
	s.stableRender = trimGlamourMargins(renderMarkdown(prefix, width))
	s.width = width
	s.gen = themeGen
}

// renderTrailing renders a trailing partial as a fresh document and
// trims surrounding whitespace so it concatenates cleanly.
func (s *streamingMarkdown) renderTrailing(text string) string {
	if text == "" {
		return ""
	}
	return trimGlamourMargins(renderMarkdown(text, s.width))
}

// glueRenders concatenates two glamour fragments with a single blank
// line, trimming both sides so the seam has no doubled margin.
func glueRenders(prefix, trail string) string {
	prefix = trimGlamourMargins(prefix)
	trail = trimGlamourMargins(trail)
	switch {
	case prefix == "" && trail == "":
		return ""
	case prefix == "":
		return trail
	case trail == "":
		return prefix
	default:
		return prefix + "\n\n" + trail
	}
}

func trimGlamourMargins(s string) string { return strings.Trim(s, " \t\n") }

// findSafeMarkdownBoundary returns the byte offset of the end of the
// latest safe boundary in content — content[:boundary] is a valid
// stable-prefix candidate, and the offset points immediately after a
// blank-line separator. Returns -1 when no safe boundary exists.
// SAFETY FIRST: any doubt returns -1.
//
// A single forward pass tracks the prefix state incrementally — fence
// parity, the open-construct flags below, and the last non-blank line —
// so the whole scan is O(n) with no per-candidate prefix re-walk. The
// last offset whose prefix and trailing line both pass the checks wins.
//
// Hazards are scoped to the construct's actual lifetime rather than
// poisoning everything after them:
//
//   - openList: set by any list-item marker, cleared when a flush-left
//     non-marker line follows a blank separator (CommonMark: a
//     paragraph at the original indentation interrupts the list). A
//     blank between same-list items makes the whole list loose —
//     re-rendering a closed-early prefix would flip tight↔loose — so
//     while a list is open no boundary is safe, but once it provably
//     closes, caching resumes. Without scoping, one bullet list near
//     the top of a long answer would force a full re-render on every
//     flush for the rest of the stream.
//   - openHTML: generic HTML blocks (CommonMark types 6/7) end at the
//     next blank line, so the flag clears there.
//   - sticky: constructs with document or until-close-marker scope —
//     link reference definitions (a def anywhere can resolve links
//     anywhere) and raw-HTML blocks (script/pre/style/textarea,
//     comments, CDATA, processing instructions, declarations) that
//     survive blank lines. These stay set for the rest of the scan.
func findSafeMarkdownBoundary(content string) int {
	if content == "" {
		return -1
	}

	// The chosen boundary is the start of the latest line that follows
	// a blank-line separator (or the very end, when content closes on a
	// blank line). consider() evaluates each line's start boundary using
	// the prefix state accumulated from earlier lines, then folds the
	// line into that state — no per-candidate prefix re-walk, no
	// allocation. A boundary whose own line is blank is deferred to a
	// later non-blank line's boundary: the prefix state across a blank
	// run is identical and the later offset is preferred, so deferring
	// loses no safe candidate. result keeps the latest passing offset.
	result := -1
	inFence := false
	openList := false
	openHTML := false
	sticky := false
	lastNonBlank := ""
	prevSepBlank := false
	lineIndex := 0

	consider := func(line string, start int) {
		blank := isBlankOrSpaces(line)
		flushLeft := line != "" && line[0] != ' ' && line[0] != '\t'

		// A flush-left non-marker line after a blank separator closes
		// any open list. The line must not be able to grow into a
		// marker on a later flush ("-", "12", "12.") — content only
		// ever extends at the end, so this can only concern the line
		// currently being streamed.
		listJustClosed := false
		if openList && prevSepBlank && !inFence && flushLeft && !blank &&
			!isListItemMarker(line) && !couldGrowIntoListMarker(line) {
			openList = false
			listJustClosed = true
		}

		// A boundary sits at start when the preceding line is a blank
		// separator and is not itself the first line (lineIndex >= 2).
		if lineIndex >= 2 && prevSepBlank && !inFence && !openList && !openHTML && !sticky && !blank {
			opensConstruct := lastNonBlank != "" && lineOpensConstruct(lastNonBlank)
			if listJustClosed {
				// lastNonBlank is the final item of the list this line
				// just proved closed; only its non-list aspects can
				// still hold a construct open.
				opensConstruct = lastNonBlank != "" && lineOpensConstructBesidesList(lastNonBlank)
			}
			if !opensConstruct && !isSetextUnderlineCandidate(line) {
				result = start
			}
		}

		switch {
		case isFenceLine(line):
			inFence = !inFence
		case !inFence:
			if blank {
				openHTML = false // generic HTML blocks end at a blank line
			} else if trimmed := strings.TrimLeft(line, " \t"); trimmed != "" {
				if isListItemMarker(trimmed) {
					openList = true
				}
				switch {
				case isLinkRefDefinition(line) || isStickyHTMLOpener(line):
					sticky = true
				case isHTMLBlockOpener(line):
					openHTML = true
				}
			}
		}
		if !blank {
			lastNonBlank = line
		}
		prevSepBlank = blank
		lineIndex++
	}

	lineStart := 0
	for i := range len(content) {
		if content[i] == '\n' {
			consider(content[lineStart:i], lineStart)
			lineStart = i + 1
		}
	}
	if lineStart <= len(content)-1 {
		consider(content[lineStart:], lineStart)
	}

	// Virtual end-of-content boundary: when content closes on a blank
	// line, content[:len] is a candidate with an empty rest (no setext
	// underline can follow), preferred over any earlier boundary.
	if lineIndex >= 2 && prevSepBlank && !inFence && !openList && !openHTML && !sticky {
		if lastNonBlank == "" || !lineOpensConstruct(lastNonBlank) {
			result = len(content)
		}
	}
	return result
}

// couldGrowIntoListMarker reports whether a flush-left line, as
// currently streamed, could still become a list-item marker when more
// bytes arrive: a bare bullet ("-", "*", "+") or an ordinal with or
// without its delimiter ("12", "12.", "12)"). Such a line must not be
// treated as a list terminator yet.
func couldGrowIntoListMarker(line string) bool {
	if line == "-" || line == "*" || line == "+" {
		return true
	}
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i == 0 || i > 9 {
		return false
	}
	if i == len(line) {
		return true
	}
	return (line[i] == '.' || line[i] == ')') && i+1 == len(line)
}

func isBlankOrSpaces(s string) bool {
	for i := range len(s) {
		if s[i] != ' ' && s[i] != '\t' {
			return false
		}
	}
	return true
}

func isFenceLine(line string) bool {
	i := 0
	for i < len(line) && i < 3 && line[i] == ' ' {
		i++
	}
	if i >= len(line) {
		return false
	}
	c := line[i]
	if c != '`' && c != '~' {
		return false
	}
	run := 0
	for i < len(line) && line[i] == c {
		i++
		run++
	}
	return run >= 3
}

// lineOpensConstruct reports whether line keeps a markdown construct
// open across the boundary (indented code, block quote, list, table,
// setext underline). Conservative.
func lineOpensConstruct(line string) bool {
	if lineOpensConstructBesidesList(line) {
		return true
	}
	trimmed := strings.TrimLeft(line, " \t")
	return trimmed != "" && isListItemMarker(trimmed)
}

// lineOpensConstructBesidesList is lineOpensConstruct minus the
// list-marker clause — used when the line is the final item of a list
// the scanner has just proved closed, where only its non-list aspects
// can still hold a construct open.
func lineOpensConstructBesidesList(line string) bool {
	if len(line) > 0 && line[0] == '\t' {
		return true
	}
	if strings.HasPrefix(line, "    ") {
		return true
	}
	trimmed := strings.TrimLeft(line, " \t")
	if trimmed == "" {
		return false
	}
	if trimmed[0] == '>' {
		return true
	}
	if strings.ContainsRune(line, '|') {
		return true
	}
	return isSetextUnderlineCandidate(trimmed)
}

func isListItemMarker(line string) bool {
	if line == "" {
		return false
	}
	c := line[0]
	if c == '-' || c == '*' || c == '+' {
		return len(line) >= 2 && (line[1] == ' ' || line[1] == '\t')
	}
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i == 0 || i > 9 || i >= len(line) {
		return false
	}
	if line[i] != '.' && line[i] != ')' {
		return false
	}
	return i+1 < len(line) && (line[i+1] == ' ' || line[i+1] == '\t')
}

func isSetextUnderlineCandidate(line string) bool {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if i == len(line) {
		return false
	}
	c := line[i]
	if c != '=' && c != '-' {
		return false
	}
	j := i
	for j < len(line) && line[j] == c {
		j++
	}
	for j < len(line) {
		if line[j] != ' ' && line[j] != '\t' {
			return false
		}
		j++
	}
	return j-i >= 1
}

// isStickyHTMLOpener matches HTML block openers whose blocks survive
// blank lines (CommonMark types 1–5): script/pre/style/textarea,
// comments, processing instructions, declarations, CDATA. These end at
// a close marker the scanner doesn't track, so they poison the rest of
// the scan.
func isStickyHTMLOpener(line string) bool {
	rest := afterLeadingIndent(line)
	if len(rest) < 2 || rest[0] != '<' {
		return false
	}
	if strings.HasPrefix(rest, "<!--") || strings.HasPrefix(rest, "<?") || strings.HasPrefix(rest, "<![CDATA[") {
		return true
	}
	if len(rest) >= 3 && rest[1] == '!' && isASCIILetter(rest[2]) {
		return true
	}
	low := strings.ToLower(rest)
	for _, t := range []string{"<script", "<pre", "<style", "<textarea"} {
		if strings.HasPrefix(low, t) {
			next := byte(0)
			if len(low) > len(t) {
				next = low[len(t)]
			}
			if next == 0 || next == ' ' || next == '\t' || next == '>' {
				return true
			}
		}
	}
	return false
}

// isHTMLBlockOpener matches generic, tag-shaped HTML block openers
// (CommonMark types 6/7), whose blocks end at the next blank line.
func isHTMLBlockOpener(line string) bool {
	rest := afterLeadingIndent(line)
	if len(rest) < 2 || rest[0] != '<' {
		return false
	}
	j := 1
	if rest[j] == '/' {
		j++
	}
	return j < len(rest) && isASCIILetter(rest[j])
}

// afterLeadingIndent strips up to three leading spaces — the indent a
// block-level construct may carry without becoming indented code.
func afterLeadingIndent(line string) string {
	i := 0
	for i < len(line) && i < 3 && line[i] == ' ' {
		i++
	}
	return line[i:]
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// isLinkRefDefinition matches a CommonMark link reference definition
// opener: ^[ ]{0,3}\[label\]:\s*\S+
func isLinkRefDefinition(line string) bool {
	line = afterLeadingIndent(line)
	if len(line) == 0 || line[0] != '[' {
		return false
	}
	i := 1
	labelStart := i
	for i < len(line) && line[i] != ']' {
		i++
	}
	if i >= len(line) || i == labelStart {
		return false
	}
	i++
	if i >= len(line) || line[i] != ':' {
		return false
	}
	i++
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	return i < len(line)
}
