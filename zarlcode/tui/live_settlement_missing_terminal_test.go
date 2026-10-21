package tui_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
)

func TestLiveTurnMissingTerminalReleasesCompletedOperation(t *testing.T) {
	for _, tc := range []struct {
		name           string
		missingStart   bool
		missingSegment bool
	}{
		{name: "missing terminal"},
		{name: "missing terminal and segment", missingSegment: true},
		{name: "missing bookends", missingStart: true, missingSegment: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newBeforeFixture(t)
			calls := 0
			f.provider.check = func(context.Context) { calls++ }
			f.ui.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
			driveBeforeCommand(f, f.ui.Submit("first prompt"))
			f.sink.Drain()
			f.mu.Lock()
			events := append([]tea.Msg(nil), f.events...)
			f.events = nil
			f.mu.Unlock()
			if len(events) < 2 || calls != 1 {
				t.Fatal("turn did not execute")
			}
			before, err := f.store.GetSessionResumeState(t.Context(), f.ui.SessionIdentity())
			if err != nil {
				t.Fatal(err)
			}
			var terminal teasink.ConversationEndedMsg
			for _, event := range events[:len(events)-1] {
				switch event := event.(type) {
				case teasink.ConversationStartedMsg:
					if tc.missingStart {
						continue
					}
				case teasink.ConversationEndedMsg:
					terminal = event
					continue
				case teasink.IterationCompletedMsg:
					// Exercise both a delivered segment boundary and a missing
					// one; neither establishes the turn's terminal outcome.
					if tc.missingSegment {
						continue
					}
				}
				f.ui.Update(event)
			}
			if terminal.TaskID == "" {
				t.Fatal("fixture did not capture a terminal to omit")
			}
			if tc.missingStart {
				f.live.QueueAppend("queued second")
			} else {
				f.ui.Submit("queued second")
			}
			marker := events[len(events)-1]
			_, next := f.ui.Update(marker)
			if !strings.Contains(f.ui.ToastText(), "turn outcome unknown") {
				t.Fatalf("missing explicit incomplete-turn warning: %s", f.ui.ToastText())
			}
			if row := transcriptScreenRow(surfaceCanvas(f.ui, 100, 30), 27); strings.Contains(row, "working") || strings.Contains(row, "responding") {
				t.Fatalf("completed operation still has active input activity: %s", row)
			}
			entries := f.ui.CanonicalThread().Entries()
			found := false
			for _, entry := range entries {
				if entry.Kind == transcript.EntryKinds.ENTRYASSISTANTMESSAGE && entry.Payload.Text == "answer" {
					found = true
					if !entry.Payload.Interrupted || entry.Payload.Complete {
						t.Fatal("missing terminal was represented as successful completion")
					}
				}
			}
			if !found || len(f.live.ContextSnapshot()) == 0 {
				t.Fatal("recovery discarded the answer or runtime history")
			}
			driveBeforeCommand(f, next)
			f.ui.Update(marker)   // duplicate completion must not promote queued input
			f.ui.Update(terminal) // a late terminal must not overwrite recovery
			if f.live.QueueLen() != 1 || calls != 1 || !strings.Contains(f.ui.ToastText(), "turn outcome unknown") {
				t.Fatal("recovery consumed queued input or lost its warning")
			}
			after, err := f.store.GetSessionResumeState(t.Context(), f.ui.SessionIdentity())
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("missing terminal fabricated a durable completed head", err)
			}
			// Explicit submission may continue from the retained in-memory
			// context; the already accepted queued input goes first.
			f.ui.Update(tea.PasteMsg{Content: "third draft"})
			driveBeforeCommand(f, f.ui.Submit("third draft"))
			f.sink.Drain()
			f.mu.Lock()
			following := append([]tea.Msg(nil), f.events...)
			f.events = nil
			f.mu.Unlock()
			if len(following) < 2 {
				t.Fatal("explicit continuation produced no events")
			}
			for i, event := range following {
				_, cmd := f.ui.Update(event)
				if _, started := event.(teasink.ConversationStartedMsg); started {
					f.ui.Update(marker) // a stale marker cannot end the new turn
					f.ui.Update(terminal)
					if row := transcriptScreenRow(surfaceCanvas(f.ui, 100, 30), 27); !strings.Contains(row, "working") {
						t.Fatalf("stale completion cleared the following turn: %s", row)
					}
				}
				if i == len(following)-1 {
					driveBeforeCommand(f, cmd)
				}
			}
			if calls != 2 || f.live.QueueLen() != 0 || f.ui.ComposerText() != "third draft" {
				t.Fatalf("explicit continuation: calls=%d queue=%d draft=%q toast=%q", calls, f.live.QueueLen(), f.ui.ComposerText(), f.ui.ToastText())
			}
		})
	}
}
