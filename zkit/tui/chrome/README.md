# Shared terminal chrome

`chrome.Style` provides the family's themeable, cell-fitted presentation contracts. Applications keep their original Slant Relief artwork, content models, resource pipelines and rendering backends; the package supplies common framing, spacing and state treatments rather than replacement art.

- `Rule(title, width, focused)` draws a thin, cell-fitted heading; a focused title is bracketed for no-color visibility.
- `Strip(left, right, width)` aligns plain semantic status text; the right cluster appears only when it fits.
- `Marker(selected, active)` distinguishes selection from activity with thin two-cell cues without relying on color.
- `MarkerFor(selected, activity)` combines that pointer with a meaningful one-cell domain glyph, such as playback's `▶`, with the same ASCII fallback.
- `Connection(connected, demo)` gives stateful apps the same theme-colored connection/demo glyphs; it does not invent a connected state for stateless tools.

- `Logo(art, width, maxRows)` retains and centers the complete original artwork, with one blank row above/below and one thin bottom divider. It returns no band if the whole composition cannot fit.
- `PaneTitle(title, width, focused)` gives plain pane labels a common inset and focus color, without bracketed decoration.
- `PaneLabel(title, focused)` supplies the same heading role without padding when inline details follow it.
- `Selection(text, width, selected)` uses the theme's selection surface and readable foreground across a whole row. Domain activity glyphs remain visible even when the row is selected; pointers remain a secondary, non-color cue.
- `SelectionColors()` exposes that exact foreground/background contract to cell renderers; text and cell backends do not maintain separate selection palettes.
- `Status(message, indicators, width)` reserves small persistent indicators at the bottom-right, ellipsizing the message lane rather than moving those indicators.

Focus identifies the input target, selection identifies the chosen item, and activity identifies what is actually running/playing. Selection must not disappear when an item is active. Live working/playback metrics belong beside their control, not in the compact status indicator cluster. Authored labels are lowercase; received/user content retains its casing and media colors.

Shapes and spacing are independent of the supplied `theme.Theme`; semantic palette roles color the primitives without embedding fixed colors. A zero theme emits no added color. `ASCII` replaces decorative glyphs (not caller text), and `Plain` removes ANSI styling, including styling already present in caller text. The default glyphs are UTF-8; consumers can select ASCII and Plain fallbacks. The package does not detect terminal capabilities, read environment variables, or alter the existing renderer or media output.
