package tui

import (
	"strings"
	"sync"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
)

// Glamour renderers are cached per (wrap-width, themeGen): building a
// TermRenderer (parsing the chroma style, compiling the ANSI templates) is
// the expensive part, and width + theme are the only inputs that vary
// between messages.
var (
	mdMu       sync.Mutex
	mdCache    = map[int]*glamour.TermRenderer{}
	mdCacheGen uint64 // themeGen at last cache fill; on mismatch, clear and rebuild
)

// renderMarkdown renders md to ANSI, word-wrapped to width, via a
// per-width cached glamour renderer. The whole render is serialized
// under mdMu — glamour's Render is stateful (goldmark's block stack)
// and unsafe for concurrent use on a shared renderer; the streaming
// path issues several Render calls per flush. On any error (or
// non-positive width) it returns the raw text so content is never lost.
func renderMarkdown(md string, width int) string {
	if width < 1 {
		return md
	}
	mdMu.Lock()
	defer mdMu.Unlock()

	if mdCacheGen != themeGen {
		mdCache = map[int]*glamour.TermRenderer{}
		mdCacheGen = themeGen
	}

	r, ok := mdCache[width]
	if !ok {
		var err error
		r, err = glamour.NewTermRenderer(
			glamour.WithStyles(themeStyleConfig()),
			glamour.WithWordWrap(width),
		)
		if err != nil {
			return md
		}
		mdCache[width] = r
	}
	out, err := r.Render(md)
	if err != nil {
		return md
	}
	return strings.Trim(out, "\n")
}

// themeStyleConfig builds a glamour StyleConfig from the active palette so
// markdown body text, headings, links, and code blocks all pick up the
// current TUI theme colours.
func themeStyleConfig() ansi.StyleConfig {
	p := palette
	s := func(v string) *string { return &v }
	b := func(v bool) *bool { return &v }
	u := func(v uint) *uint { return &v }

	return ansi.StyleConfig{
		Document: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				BlockPrefix: "\n",
				BlockSuffix: "\n",
				Color:       s(string(p.Fg)),
			},
			Margin: u(2),
		},
		Paragraph: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Color: s(string(p.Fg)),
			},
		},
		Heading: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				BlockSuffix: "\n",
				Color:       s(string(p.Primary)),
				Bold:        b(true),
			},
		},
		H1: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix:          " ",
				Suffix:          " ",
				Color:           s(string(p.Bg)),
				BackgroundColor: s(string(p.Primary)),
				Bold:            b(true),
			},
		},
		H2: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{Prefix: "## "},
		},
		H3: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{Prefix: "### "},
		},
		H4: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{Prefix: "#### "},
		},
		H5: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{Prefix: "##### "},
		},
		H6: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix: "###### ",
				Color:  s(string(p.Secondary)),
			},
		},
		BlockQuote: ansi.StyleBlock{
			Indent:      u(1),
			IndentToken: s("│ "),
		},
		List: ansi.StyleList{LevelIndent: 2},
		Item: ansi.StylePrimitive{
			BlockPrefix: "• ",
			Color:       s(string(p.Primary)),
		},
		Enumeration: ansi.StylePrimitive{
			BlockPrefix: ". ",
		},
		Task: ansi.StyleTask{
			Ticked:   "[✓] ",
			Unticked: "[ ] ",
		},
		Text: ansi.StylePrimitive{
			Color: s(string(p.Fg)),
		},
		Strikethrough: ansi.StylePrimitive{
			CrossedOut: b(true),
		},
		Emph: ansi.StylePrimitive{
			Italic: b(true),
			Color:  s(string(p.Secondary)),
		},
		Strong: ansi.StylePrimitive{
			Bold:  b(true),
			Color: s(string(p.Primary)),
		},
		HorizontalRule: ansi.StylePrimitive{
			Color:  s(string(p.Subtle)),
			Format: "\n────────\n",
		},
		Link: ansi.StylePrimitive{
			Color:     s(string(p.Info)),
			Underline: b(true),
		},
		LinkText: ansi.StylePrimitive{
			Color: s(string(p.Primary)),
			Bold:  b(true),
		},
		Image: ansi.StylePrimitive{
			Color:     s(string(p.Info)),
			Underline: b(true),
		},
		ImageText: ansi.StylePrimitive{
			Color:  s(string(p.Muted)),
			Format: "Image: {{.text}} →",
		},
		Code: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix:          "\u00a0",
				Suffix:          "\u00a0",
				Color:           s(string(p.Warning)),
				BackgroundColor: s(string(p.Highlight)),
			},
		},
		CodeBlock: ansi.StyleCodeBlock{
			StyleBlock: ansi.StyleBlock{
				StylePrimitive: ansi.StylePrimitive{
					Color: s(string(p.Subtle)),
				},
				Margin: u(2),
			},
			Chroma: &ansi.Chroma{
				Text:                ansi.StylePrimitive{Color: s(string(p.Fg))},
				Error:               ansi.StylePrimitive{Color: s(string(p.Error))},
				Comment:             ansi.StylePrimitive{Color: s(string(p.Subtle))},
				CommentPreproc:      ansi.StylePrimitive{Color: s(string(p.Warning))},
				Keyword:             ansi.StylePrimitive{Color: s(string(p.Info))},
				KeywordReserved:     ansi.StylePrimitive{Color: s(string(p.System))},
				KeywordNamespace:    ansi.StylePrimitive{Color: s(string(p.Tool))},
				KeywordType:         ansi.StylePrimitive{Color: s(string(p.Primary))},
				Operator:            ansi.StylePrimitive{Color: s(string(p.BorderFocus))},
				Punctuation:         ansi.StylePrimitive{Color: s(string(p.Muted))},
				Name:                ansi.StylePrimitive{Color: s(string(p.Fg))},
				NameBuiltin:         ansi.StylePrimitive{Color: s(string(p.Info))},
				NameTag:             ansi.StylePrimitive{Color: s(string(p.User))},
				NameAttribute:       ansi.StylePrimitive{Color: s(string(p.BorderFocus))},
				NameClass:           ansi.StylePrimitive{Color: s(string(p.Assistant)), Bold: b(true)},
				NameConstant:        ansi.StylePrimitive{Color: s(string(p.System))},
				NameDecorator:       ansi.StylePrimitive{Color: s(string(p.Warning))},
				NameFunction:        ansi.StylePrimitive{Color: s(string(p.Success))},
				NameOther:           ansi.StylePrimitive{Color: s(string(p.Fg))},
				LiteralNumber:       ansi.StylePrimitive{Color: s(string(p.Tool))},
				LiteralString:       ansi.StylePrimitive{Color: s(string(p.System))},
				LiteralStringEscape: ansi.StylePrimitive{Color: s(string(p.Success))},
				GenericDeleted:      ansi.StylePrimitive{Color: s(string(p.Error))},
				GenericEmph:         ansi.StylePrimitive{Italic: b(true)},
				GenericInserted:     ansi.StylePrimitive{Color: s(string(p.Success))},
				GenericStrong:       ansi.StylePrimitive{Bold: b(true)},
				GenericSubheading:   ansi.StylePrimitive{Color: s(string(p.Muted))},
				Background:          ansi.StylePrimitive{BackgroundColor: s(string(p.Highlight))},
			},
		},
		Table: ansi.StyleTable{
			CenterSeparator: s("┼"),
			ColumnSeparator: s("│"),
			RowSeparator:    s("─"),
		},
		DefinitionDescription: ansi.StylePrimitive{
			BlockPrefix: "\n🠶 ",
		},
	}
}
