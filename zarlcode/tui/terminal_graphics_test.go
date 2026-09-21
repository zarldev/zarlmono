package tui_test

import (
	"fmt"
	"image"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// Capture the actual output adapter, not View.Content: Bubble Tea parses the
// latter as styled cells and is allowed to discard Kitty APC sequences.
func nativeOutput(t *testing.T, m *tui.UI) func(string) string {
	t.Helper()
	file, err := os.Create(filepath.Join(t.TempDir(), "terminal"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	writer := m.CaptureTerminalGraphicsOutput(file)
	var offset int64
	return func(text string) string {
		t.Helper()
		if _, err := io.WriteString(writer, text); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(file.Name())
		if err != nil {
			t.Fatal(err)
		}
		out := string(data[offset:])
		offset = int64(len(data))
		return out
	}
}

func expandedNativeScreenshot(t *testing.T, count int) (*tui.UI, func(string) string) {
	t.Helper()
	t.Setenv("TERM_PROGRAM", "ghostty")
	t.Setenv("TERM", "xterm-ghostty")
	m := tui.New()
	output := nativeOutput(t, m)
	output("\x1b[?1049h")
	step(t, m, window(120, 50))
	part := screenshotPart(t, image.NewGray(image.Rect(0, 0, 1280, 880)))
	parts := make([]llm.ContentPart, count)
	for i := range parts {
		parts[i] = part
	}
	step(t, m, teasink.ToolStartedMsg{TaskID: "t", ToolID: "observe", ToolName: "computer_observe"})
	step(t, m, teasink.ToolCompletedMsg{TaskID: "t", ToolID: "observe", ToolName: "computer_observe", FormattedResult: "readable observation metadata", Parts: parts})
	step(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	step(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	return m, output
}

func assertNativeImages(t *testing.T, out string, count int) {
	t.Helper()
	if got := strings.Count(out, "\x1b_Ga=T,"); got != count {
		t.Fatalf("native transmissions = %d, want %d (output %d bytes)", got, count, len(out))
	}
	if strings.Contains(out, "▀") || strings.Contains(out, "data:image") {
		t.Fatal("native output contains cell fallback or data URI")
	}
}

func TestNativeToolScreenshotsOutputAndCollapse(t *testing.T) {
	m, output := expandedNativeScreenshot(t, 2)
	view := m.View().Content
	if strings.Contains(view, "\x1b_G") || strings.Contains(view, "▀") || !strings.Contains(ansi.Strip(view), "readable observation metadata") {
		t.Fatal("native view must reserve text cells and preserve metadata without APC")
	}
	out := output(view)
	assertNativeImages(t, out, 2)
	if strings.Count(out, "a=d,d=A") != 1 || strings.Index(out, "a=d,d=A") < len(view) {
		t.Fatal("graphics must clear once, after the text frame, before transmitting all images")
	}
	step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	out = output(m.View().Content)
	assertNativeImages(t, out, 0)
	if !strings.Contains(out, "a=d,d=A") {
		t.Fatal("collapse did not delete existing placements")
	}
}

func TestNativeToolScreenshotStableAcrossUnrelatedWrites(t *testing.T) {
	m, output := expandedNativeScreenshot(t, 1)
	out := output(m.View().Content)
	assertNativeImages(t, out, 1)
	placement := regexp.MustCompile(`\x1b\[\d+;\d+H\x1b_Ga=T,f=100,q=2,C=1,c=(\d+),r=(\d+)`).FindStringSubmatch(out)
	if placement == nil {
		t.Fatal("missing native screenshot placement")
	}
	cols, _ := strconv.Atoi(placement[1])
	rows, _ := strconv.Atoi(placement[2])
	if cols < 58 || rows < 20 {
		t.Fatalf("native screenshot is only %d × %d cells", cols, rows)
	}
	for _, text := range []string{
		"\x1b[1;1Hstatus update",
		"\x1b[?25l",  // cursor visibility
		"\x1b7\x1b8", // an older queued image-only wake
		ansi.SetModeSynchronizedOutput + "\x1b[50;1Hcomposer\x1b[K" + ansi.ResetModeSynchronizedOutput,
		"\x1b]2;title JLMST\a",
	} {
		m.View() // republishing the same desired frame must not invalidate it
		if got := output(text); got != text {
			t.Fatalf("unrelated terminal write %q repainted graphics (%d bytes)", text, len(got))
		}
	}
	step(t, m, teasink.ToolCompletedMsg{
		TaskID: "t", ToolID: "observe", ToolName: "computer_observe",
		FormattedResult: "updated observation metadata",
		Parts:           []llm.ContentPart{screenshotPart(t, image.NewGray(image.Rect(0, 0, 1280, 880)))},
	})
	if !strings.Contains(m.View().Content, "updated observation metadata") {
		t.Fatal("text-only completion did not update the view")
	}
	const metadataDiff = "\x1b[45;1Hupdated observation metadata"
	if got := output(metadataDiff); got != metadataDiff {
		t.Fatal("text-only completion repainted unchanged image pixels and placement")
	}
	step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	out = output(m.View().Content)
	if !strings.Contains(out, "a=d,d=A") {
		t.Fatal("stable image was not cleared on collapse")
	}
	if got := output("later text"); got != "later text" {
		t.Fatal("collapsed image was repeatedly cleared")
	}
}

func TestNativeToolScreenshotReplayAfterTerminalDamage(t *testing.T) {
	m, output := expandedNativeScreenshot(t, 1)
	assertNativeImages(t, output(m.View().Content), 1)
	for _, damage := range []string{"\x1b[2J", "\x1b[J", "\x1b[1J", "\x1b[2S", "\x1b[T", "\x1b[3L", "\x1b[M", "\x1bM", "\n"} {
		out := output(damage)
		assertNativeImages(t, out, 1)
		if !strings.HasPrefix(out, ansi.SetModeSynchronizedOutput+damage) || !strings.HasSuffix(out, ansi.ResetModeSynchronizedOutput) {
			t.Fatalf("terminal damage %q and image replacement are not synchronized", damage)
		}
		if got := output("\x1b7\x1b8"); got != "\x1b7\x1b8" {
			t.Fatal("recovered image was repainted again")
		}
	}
}

func TestNativeToolScreenshotExtendsRendererSynchronizedUpdate(t *testing.T) {
	m, output := expandedNativeScreenshot(t, 1)
	text := m.View().Content
	out := output(ansi.SetModeSynchronizedOutput + text + ansi.ResetModeSynchronizedOutput)
	assertNativeImages(t, out, 1)
	if !strings.HasPrefix(out, ansi.SetModeSynchronizedOutput+text) ||
		!strings.HasSuffix(out, ansi.ResetModeSynchronizedOutput) ||
		strings.Count(out, ansi.SetModeSynchronizedOutput) != 1 ||
		strings.Count(out, ansi.ResetModeSynchronizedOutput) != 1 {
		t.Fatal("text and graphics must share one synchronized update")
	}
}

func TestNativeToolScreenshotsResizeClipScrollAndOverlay(t *testing.T) {
	m, output := expandedNativeScreenshot(t, 1)
	assertNativeImages(t, output(m.View().Content), 1)
	step(t, m, window(70, 16))
	// A renderer resize erases the terminal. Native bytes must follow the erase.
	frame := "\x1b[2J" + m.View().Content
	out := output(frame)
	assertNativeImages(t, out, 1)
	if strings.Index(out, "\x1b_Ga=T,") < len(frame) {
		t.Fatal("resize image precedes the renderer's erase")
	}
	placement := regexp.MustCompile(`\x1b\[(\d+);(\d+)H\x1b_Ga=T,f=100,q=2,C=1,c=(\d+),r=(\d+),x=(\d+),y=(\d+),w=(\d+),h=(\d+)`).FindStringSubmatch(out)
	if placement == nil {
		t.Fatal("missing explicit viewport crop")
	}
	values := make([]int, 8)
	for i := range values {
		values[i], _ = strconv.Atoi(placement[i+1])
	}
	y, x, cols, rows := values[0], values[1], values[2], values[3]
	if y < 3 || x < 1 || x+cols > 71 || y+rows > 16 || values[7] >= 880 {
		t.Fatalf("image not cropped to short timeline: %v", values)
	}
	// Following new output must remove the now-offscreen image.
	step(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	m.AddTranscriptMessages([]llm.Message{{Role: llm.RoleUser, Content: strings.Repeat("later output\n", 60)}})
	assertNativeImages(t, output(m.View().Content), 0)
	for range 100 {
		step(t, m, tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelUp}))
	}
	assertNativeImages(t, output(m.View().Content), 1)
	step(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})         // return to composer
	step(t, m, tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl}) // dashboard covers timeline
	out = output(m.View().Content)
	assertNativeImages(t, out, 0)
	if !strings.Contains(out, "a=d,d=A") {
		t.Fatal("covering surface left image painted")
	}
}

func TestNativeToolScreenshotShutdownAndWake(t *testing.T) {
	m, output := expandedNativeScreenshot(t, 1)
	assertNativeImages(t, output(m.View().Content), 1)
	// Replacing pixels with identical labels/layout needs an image-only wake.
	step(t, m, window(90, 50)) // no live-usage sidebar in this image-only fixture
	before := m.View().Content
	img := image.NewGray(image.Rect(0, 0, 1280, 880))
	img.SetGray(0, 0, color.Gray{Y: 255})
	_, cmd := m.Update(teasink.ToolCompletedMsg{TaskID: "t", ToolID: "observe", ToolName: "computer_observe", FormattedResult: "readable observation metadata", Parts: []llm.ContentPart{screenshotPart(t, img)}})
	if m.View().Content != before {
		t.Fatal("image-only fixture changed the text grid")
	}
	if cmd == nil {
		t.Fatal("missing image-only output wake")
	}
	var hasRaw func(tea.Cmd) bool
	hasRaw = func(cmd tea.Cmd) bool {
		if cmd == nil {
			return false
		}
		switch msg := cmd().(type) {
		case tea.RawMsg:
			return fmt.Sprint(msg.Msg) == "\x1b7\x1b8"
		case tea.BatchMsg:
			for _, child := range msg {
				if hasRaw(child) {
					return true
				}
			}
		}
		return false
	}
	if !hasRaw(cmd) {
		t.Fatal("changed image frame did not wake raw output")
	}
	_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}) // collapse changes text too
	if hasRaw(cmd) {
		t.Fatal("text-changing collapse must use the renderer write, not an early raw wake")
	}
	// An older queued wake replays only the newest (collapsed) state.
	assertNativeImages(t, output("\x1b7\x1b8"), 0)
	step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	assertNativeImages(t, output(m.View().Content), 1)
	out := output("\x1b[?1049l")
	assertNativeImages(t, out, 0)
	if !strings.HasPrefix(out, "\x1b_Ga=d,d=A") {
		t.Fatal("shutdown did not clear before leaving the alternate screen")
	}
	assertNativeImages(t, output("shell prompt"), 0)
}

// The real Bubble Tea renderer must reach the adapter; pure View assertions
// would have missed the original APC-stripping problem.
func TestNativeScreenshotThroughBubbleTea(t *testing.T) {
	m, _ := expandedNativeScreenshot(t, 1)
	file, err := os.Create(filepath.Join(t.TempDir(), "bubbletea"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	program := tea.NewProgram(nativeExitModel{m}, tea.WithContext(t.Context()),
		tea.WithInput(strings.NewReader("")), tea.WithOutput(m.CaptureTerminalGraphicsOutput(file)),
		tea.WithWindowSize(120, 50), tea.WithoutSignalHandler())
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	transmit, exit := strings.Index(out, "\x1b_Ga=T,"), strings.LastIndex(out, "\x1b[?1049l")
	if transmit < 0 || exit < transmit || !strings.Contains(out[transmit:exit], "a=d,d=A") {
		t.Fatalf("Bubble Tea did not deliver and clean up native graphics (%d bytes)", len(out))
	}
}

type nativeExitModel struct{ *tui.UI }

func (nativeExitModel) Init() tea.Cmd { return tea.Quit }

func TestNativeScreenshotInsideProgram(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "ghostty")
	m := tui.New()
	output := nativeOutput(t, m)
	output("\x1b[?1049h")
	step(t, m, window(120, 50))
	step(t, m, teasink.ToolStartedMsg{TaskID: "t", ToolID: "program", ToolName: "program"})
	step(t, m, teasink.ToolStartedMsg{TaskID: "t", ToolID: "image", ParentToolID: "program", ToolName: "computer_observe"})
	step(t, m, teasink.ToolCompletedMsg{TaskID: "t", ToolID: "image", ParentToolID: "program", ToolName: "computer_observe", Parts: []llm.ContentPart{screenshotPart(t, image.NewGray(image.Rect(0, 0, 100, 60)))}})
	step(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}) // group
	step(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}) // program
	step(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}) // nested observation
	assertNativeImages(t, output(m.View().Content), 1)
	step(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}) // collapse parent
	out := output(m.View().Content)
	assertNativeImages(t, out, 0)
	if !strings.Contains(out, "a=d,d=A") {
		t.Fatal("collapsed program left descendant image painted")
	}
}

func TestGhosttyRedirectedOutputUsesCells(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "ghostty")
	m := tui.New()
	file, err := os.Create(filepath.Join(t.TempDir(), "redirect"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	if m.TerminalGraphicsOutput(file) != file {
		t.Fatal("redirected output installed a native transport")
	}
	step(t, m, window(120, 50))
	step(t, m, teasink.ToolStartedMsg{TaskID: "t", ToolID: "observe", ToolName: "computer_observe"})
	step(t, m, teasink.ToolCompletedMsg{TaskID: "t", ToolID: "observe", ToolName: "computer_observe", Parts: []llm.ContentPart{screenshotPart(t, image.NewGray(image.Rect(0, 0, 20, 10)))}})
	step(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	step(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	out := m.View().Content
	if !strings.Contains(out, "▀") || strings.Contains(out, "\x1b_G") {
		t.Fatal("redirected Ghostty output did not retain the cell fallback")
	}
}
