package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlai/service"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

type fakeChatClient struct {
	summary string
	calls   int
}

func (f *fakeChatClient) Chat(_ context.Context, _ []service.Message, _ []llm.Tool) (service.ChatResult, error) {
	f.calls++
	return service.ChatResult{Content: f.summary}, nil
}

func TestSessionAddMessage(t *testing.T) {
	s := service.NewSession("test-prompt")
	history := s.History()

	if len(history) != 1 {
		t.Fatalf("expected 1 message (system), got %d", len(history))
	}
	if history[0].Role != "system" {
		t.Errorf("expected system role, got %q", history[0].Role)
	}
	if history[0].Content != "test-prompt" {
		t.Errorf("expected system content %q, got %q", "test-prompt", history[0].Content)
	}

	s.AddUser("hello", nil)
	s.AddAssistant("hi there")

	history = s.History()
	if len(history) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(history))
	}
	if history[1].Role != "user" || history[1].Content != "hello" {
		t.Errorf("unexpected user message: %+v", history[1])
	}
	if history[2].Role != "assistant" || history[2].Content != "hi there" {
		t.Errorf("unexpected assistant message: %+v", history[2])
	}
}

func TestSessionUserMessageWithImage(t *testing.T) {
	s := service.NewSession("prompt")
	s.AddUser("what do you see?", []string{"base64img"})

	history := s.History()
	last := history[len(history)-1]
	if len(last.Images) != 1 {
		t.Errorf("expected 1 image, got %d", len(last.Images))
	}
}

func TestSession_HistoryForChat_StripsStaleImages(t *testing.T) {
	s := service.NewSession("prompt")
	s.AddUser("turn 1, looking at me", []string{"img1"})
	s.AddAssistant("you look fine")
	s.AddUser("turn 2, still here", []string{"img2"})
	s.AddAssistant("yep")
	s.AddUser("turn 3 with fresh image", []string{"img3"})

	chatHistory := s.HistoryForChat()
	// Authoritative history must still carry every image — only the
	// prompt-facing view strips them.
	rawImgs := 0
	for _, m := range s.History() {
		rawImgs += len(m.Images)
	}
	if rawImgs != 3 {
		t.Errorf("authoritative history image count = %d, want 3", rawImgs)
	}

	// Only the last user message keeps its image.
	chatImgs := 0
	lastImg := ""
	for _, m := range chatHistory {
		if len(m.Images) > 0 {
			chatImgs += len(m.Images)
			lastImg = m.Images[0]
		}
	}
	if chatImgs != 1 {
		t.Errorf("chat history image count = %d, want 1", chatImgs)
	}
	if lastImg != "img3" {
		t.Errorf("surviving image = %q, want img3", lastImg)
	}
}

// When the most recent user turn has no image (frontend deduped the
// frame), NO image should survive — the prior-turn image shouldn't
// linger and get reinterpreted as current visual evidence.
func TestSession_HistoryForChat_NoImageOnLatestTurn(t *testing.T) {
	s := service.NewSession("prompt")
	s.AddUser("first look", []string{"imgA"})
	s.AddAssistant("hi Bruno")
	s.AddUser("quick follow-up (deduped)", nil)

	chat := s.HistoryForChat()
	for _, m := range chat {
		if len(m.Images) > 0 {
			t.Errorf("image leaked into prompt on deduped turn: role=%s images=%v", m.Role, m.Images)
		}
	}
	// Authoritative history still has imgA on turn 1.
	if rawImgs := len(s.History()[1].Images); rawImgs != 1 {
		t.Errorf("authoritative history lost imgA: count=%d want 1", rawImgs)
	}
}

func TestSessionEnrollmentState(t *testing.T) {
	s := service.NewSession("system prompt")

	if s.HasPendingEnrollment() {
		t.Error("new session should not have pending enrollment")
	}
	if s.HasAskedForName() {
		t.Error("new session should not have asked for name")
	}

	emb := make([]float32, 128)
	s.PendEnrollment(emb)
	s.AskForName()

	if !s.HasPendingEnrollment() {
		t.Error("should have pending enrollment")
	}
	if !s.HasAskedForName() {
		t.Error("should have asked for name")
	}

	got := s.PendingEnrollment()
	if len(got) != 128 {
		t.Errorf("embedding length = %d, want 128", len(got))
	}

	s.ClearEnrollment()
	if s.HasPendingEnrollment() {
		t.Error("should not have pending enrollment after clear")
	}
	if s.HasAskedForName() {
		t.Error("should not have asked for name after clear")
	}
}

func TestSessionIdentity(t *testing.T) {
	s := service.NewSession("system prompt")

	if s.Identity() != "" {
		t.Error("new session should have empty identity")
	}

	s.Identify("Bruno")
	if s.Identity() != "Bruno" {
		t.Errorf("identity = %q, want Bruno", s.Identity())
	}

	s.Identify("")
	if s.Identity() != "" {
		t.Error("should be empty after clear")
	}
}

func TestSession_EstimateTokens(t *testing.T) {
	s := service.NewSession("You are a helpful assistant.")
	s.AddUser("Tell me about Go.", nil)
	s.AddAssistant("Go is a statically typed, compiled language.")

	tokens := s.EstimateTokens()
	if tokens <= 0 {
		t.Errorf("EstimateTokens() = %d, want > 0", tokens)
	}
}

func TestSession_TrimWithSummary_UnderBudget(t *testing.T) {
	s := service.NewSession("system prompt")
	s.AddUser("hello", nil)
	s.AddAssistant("hi")

	fake := &fakeChatClient{summary: "a summary"}
	if err := s.TrimWithSummary(t.Context(), fake, 100_000); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.calls != 0 {
		t.Errorf("Chat called %d times, want 0", fake.calls)
	}
}

func TestSession_TrimWithSummary_OverBudget(t *testing.T) {
	s := service.NewSession("system prompt")
	s.AddUser("initial task", nil)

	// Add 30 filler exchanges so there are plenty of middle messages.
	for i := range 30 {
		s.AddUser(strings.Repeat("x", 50), nil)
		s.AddAssistant(strings.Repeat(strings.ToUpper(string(rune('a'+i%26))), 50))
	}

	before := len(s.History())
	fake := &fakeChatClient{summary: "condensed findings"}

	if err := s.TrimWithSummary(t.Context(), fake, 500); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	history := s.History()

	if fake.calls != 1 {
		t.Errorf("Chat called %d times, want 1", fake.calls)
	}
	if len(history) >= before {
		t.Errorf("history len %d not reduced from %d", len(history), before)
	}
	if history[0].Content != "system prompt" {
		t.Errorf("system prompt not preserved: %q", history[0].Content)
	}
	if history[1].Content != "initial task" {
		t.Errorf("initial task not preserved: %q", history[1].Content)
	}

	found := false
	for _, m := range history {
		if strings.HasPrefix(m.Content, "[") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected summary message starting with '[' not found")
	}
}

func TestSession_TrimWithSummary_PreservesRecentMessages(t *testing.T) {
	s := service.NewSession("system prompt")
	s.AddUser("initial task", nil)

	// Add old filler content.
	for range 20 {
		s.AddUser(strings.Repeat("old", 20), nil)
		s.AddAssistant(strings.Repeat("old", 20))
	}

	// Add recognisable recent markers (10 messages = recentWindowSize).
	for i := range 10 {
		s.AddAssistant("RECENT_MARKER_" + strings.ToUpper(string(rune('A'+i))))
	}

	fake := &fakeChatClient{summary: "summary of old stuff"}
	if err := s.TrimWithSummary(t.Context(), fake, 500); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	history := s.History()
	for i := range 10 {
		marker := "RECENT_MARKER_" + strings.ToUpper(string(rune('A'+i)))
		found := false
		for _, m := range history {
			if m.Content == marker {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("recent marker %q not found after trim", marker)
		}
	}
}

func TestBuildUserContent(t *testing.T) {
	tests := []struct {
		name          string
		transcription string
		hasImage      bool
		text          string
		wantContains  []string
	}{
		{
			name:          "speech only",
			transcription: "hello there",
			wantContains:  []string{`The user said: "hello there"`},
		},
		{
			// With the "describe what you see" narration removed, the
			// transcription alone is carried as content and the image
			// rides on its own field on the message.
			name:          "speech with image",
			transcription: "what is this",
			hasImage:      true,
			wantContains:  []string{`The user said: "what is this"`},
		},
		{
			// Image only → no transcription, no text, no narration. The
			// helper falls through to the "Hello!" default so the message
			// isn't empty; the image is attached separately upstream.
			name:         "image only",
			hasImage:     true,
			wantContains: []string{"Hello!"},
		},
		{
			name:         "text input",
			text:         "typed message",
			wantContains: []string{`The user said: "typed message"`},
		},
		{
			name:         "empty input",
			wantContains: []string{"Hello!"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := service.BuildUserContent(tt.transcription, tt.hasImage, tt.text)
			for _, want := range tt.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("content %q missing %q", got, want)
				}
			}
		})
	}
}
