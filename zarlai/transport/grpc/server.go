package grpc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	znotify "github.com/zarldev/zarlmono/zkit/znotify"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/zarldev/zarlmono/zarlai/events"
	"github.com/zarldev/zarlmono/zarlai/repository"
	"github.com/zarldev/zarlmono/zarlai/service"
	"github.com/zarldev/zarlmono/zarlai/taskrunner"
	"github.com/zarldev/zarlmono/zarlai/tools/memory"
	zarlv1 "github.com/zarldev/zarlmono/zarlai/transport/grpc/gen/zarl/v1"
	"github.com/zarldev/zarlmono/zarlai/transport/grpc/gen/zarl/v1/zarlv1connect"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/vectorstore/qdrant"
)

var _ zarlv1connect.ZarlServiceHandler = (*ZarlServer)(nil)

type ZarlServer struct {
	llm               service.LLM
	transcriber       service.Transcriber
	synthesizer       service.Synthesizer
	face              *service.FaceService
	registry          *tools.Registry
	systemPrompt      string
	agentName         string
	sessions          sync.Map // map[string]*service.Session
	notifications     *znotify.NotificationStore
	qdrant            *qdrant.Client
	toolCallRepo      *repository.ToolCallRepo
	convLock          *runner.ConversationLock
	summaries         *repository.ConversationSummaryRepo
	bus               *events.Bus
	sessionTimers     sync.Map // map[string]*time.Timer
	settings          *repository.SettingsRepo
	convContextBudget int
	toolSelector      *service.ToolSelector
	skillSelector     *service.SkillSelector
}

// ZarlOption configures a ZarlServer.
type ZarlOption func(*ZarlServer)

// WithLLM sets the conversation LLM client.
func WithLLM(llm service.LLM) ZarlOption {
	return func(s *ZarlServer) { s.llm = llm }
}

// WithSystemPrompt sets the system prompt. The prompt is a template —
// {{agent_name}} placeholders are resolved against the configured agent
// name at session creation time.
func WithSystemPrompt(prompt string) ZarlOption {
	return func(s *ZarlServer) { s.systemPrompt = prompt }
}

// WithAgentName sets the spoken name the agent refers to itself by.
// The name is substituted into the system prompt on session creation so
// new sessions pick it up without restart.
func WithAgentName(name string) ZarlOption {
	return func(s *ZarlServer) { s.agentName = name }
}

// WithSettings provides the settings repository so the TTS pipeline can
// read the agent_spoken_name substitution at run time.
func WithSettings(sr *repository.SettingsRepo) ZarlOption {
	return func(s *ZarlServer) { s.settings = sr }
}

// WithConvContextBudget sets the token budget for the conversation session's
// TrimWithSummary. Exceeding the budget triggers a summary compaction of older
// messages. Default: 28000 (leaves ~4k headroom within a 32k per-slot context).
func WithConvContextBudget(n int) ZarlOption {
	return func(s *ZarlServer) { s.convContextBudget = n }
}

// WithToolSelector installs a ToolSelector for dynamic per-turn tool
// retrieval. Without it, every Chat call ships the full registry —
// which on a rich MCP setup is 6–10k tokens of prompt per turn that
// the model mostly ignores. With it, the conversation path ships the
// top-N relevant tools plus an always-on core.
func WithToolSelector(sel *service.ToolSelector) ZarlOption {
	return func(s *ZarlServer) { s.toolSelector = sel }
}

// WithSkillSelector installs a SkillSelector for per-turn skill
// injection. Each Chat call embeds the latest user message, picks the
// top-K matching enabled skills whose profile binding is "default" or
// global, and prepends their markdown to the system prompt.
func WithSkillSelector(sel *service.SkillSelector) ZarlOption {
	return func(s *ZarlServer) { s.skillSelector = sel }
}

// convResponseHeadroom reserves space for the model's reply within the
// ceiling so we don't trim history to exactly the budget only to have
// the completion tokens push us over on the way back.
const convResponseHeadroom = 2000

// effectiveHistoryBudget derives the token budget available to history
// (what TrimWithSummary keeps) from the overall ceiling minus tool-spec
// cost minus response headroom. Floor at 4000 so tiny models or huge
// toolsets can't drive the budget negative and trigger pathological
// summarisation on every turn.
func (s *ZarlServer) effectiveHistoryBudget(toolSpecsTokens int) int {
	ceiling := s.convContextBudget
	if ceiling <= 0 {
		ceiling = 28000
	}
	b := max(ceiling-toolSpecsTokens-convResponseHeadroom, 4000)
	return b
}

// latestUserText walks the session history backward to find the most
// recent user message and returns its content. Used as the retrieval
// query for dynamic tool selection. Returns "" when no user message
// exists (first turn, system-only history) — Select handles that.
func latestUserText(history []service.Message) string {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "user" {
			return history[i].Content
		}
	}
	return ""
}

// resolveTools returns the tool specs to ship for this Chat call. When
// a ToolSelector is configured, only the relevant subset plus the
// always-on core is returned; otherwise the full registry.
func (s *ZarlServer) resolveTools(ctx context.Context, session *service.Session) []llm.Tool {
	if s.toolSelector == nil {
		return service.LLMToolSpecs(s.registry, nil)
	}
	specs, err := s.toolSelector.Select(ctx, latestUserText(session.History()))
	if err != nil {
		slog.Warn("tool selector fell back to full registry", "err", err)
		return service.LLMToolSpecs(s.registry, nil)
	}
	names := make([]string, len(specs))
	for i, sp := range specs {
		names[i] = sp.Function.Name
	}
	slog.Info("tool selection", "count", len(specs), "registry_total", s.registry.Len(), "tools", names)
	return specs
}

func NewZarlServer(
	transcriber service.Transcriber,
	synthesizer service.Synthesizer,
	face *service.FaceService,
	registry *tools.Registry,
	notifications *znotify.NotificationStore,
	qdrantClient *qdrant.Client,
	toolCallRepo *repository.ToolCallRepo,
	convLock *runner.ConversationLock,
	summaries *repository.ConversationSummaryRepo,
	bus *events.Bus,
	opts ...ZarlOption,
) *ZarlServer {
	s := &ZarlServer{
		transcriber:   transcriber,
		synthesizer:   synthesizer,
		face:          face,
		registry:      registry,
		notifications: notifications,
		qdrant:        qdrantClient,
		toolCallRepo:  toolCallRepo,
		convLock:      convLock,
		summaries:     summaries,
		bus:           bus,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Reconfigure applies options to a running server.
func (s *ZarlServer) Reconfigure(opts ...ZarlOption) {
	for _, o := range opts {
		o(s)
	}
}

func (s *ZarlServer) ConversationLock() *runner.ConversationLock { return s.convLock }

// Face exposes the FaceService for the admin EmbedFace RPC. Other callers
// should not rely on this — face flows in the conversation path go through
// Identify directly.
func (s *ZarlServer) Face() *service.FaceService { return s.face }

func (s *ZarlServer) Converse(
	ctx context.Context,
	req *connect.Request[zarlv1.ConverseRequest],
	stream *connect.ServerStream[zarlv1.ConverseResponse],
) error {
	session, sessionID := s.getOrCreateSession(req.Msg.SessionId)

	// Reset the session idle timer — when no Converse calls arrive for
	// sessionIdleTimeout, we consider the session ended and emit the event.
	s.resetSessionTimer(sessionID, session)

	// Send session ID so client can use it for subsequent turns
	if err := stream.Send(&zarlv1.ConverseResponse{
		Payload: &zarlv1.ConverseResponse_SessionCreated{
			SessionCreated: &zarlv1.SessionCreated{SessionId: sessionID},
		},
	}); err != nil {
		return fmt.Errorf("send session: %w", err)
	}

	slog.Info("converse", "session_id", sessionID, "input_type", fmt.Sprintf("%T", req.Msg.Input))

	// Store location if provided
	if req.Msg.Latitude != 0 || req.Msg.Longitude != 0 {
		session.LocateAt(req.Msg.Latitude, req.Msg.Longitude)
	}

	// Drain pending notifications (from async tools like timers)
	if s.notifications != nil {
		for _, n := range s.notifications.Drain(sessionID) {
			session.AddUser(fmt.Sprintf("[Notification from %s]: %s", n.ToolName, n.Content), nil)
		}
	}

	switch input := req.Msg.Input.(type) {
	case *zarlv1.ConverseRequest_TextInput:
		return s.handleTextInput(ctx, stream, session, sessionID, input.TextInput)
	case *zarlv1.ConverseRequest_AudioInput:
		slog.Info("audio received", "wav_bytes", len(input.AudioInput.Wav), "image_bytes", len(input.AudioInput.ImageJpeg))
		return s.handleAudioInput(ctx, stream, session, sessionID, input.AudioInput)
	default:
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("no input provided"))
	}
}

func (s *ZarlServer) getOrCreateSession(id string) (*service.Session, string) {
	if id != "" {
		if val, ok := s.sessions.Load(id); ok {
			return val.(*service.Session), id
		}
	}
	sessionID := uuid.NewString()
	session := service.NewSession(service.RenderSystemPrompt(s.systemPrompt, s.agentName, "", ""))
	s.sessions.Store(sessionID, session)
	return session, sessionID
}

const sessionIdleTimeout = 60 * time.Second

// resetSessionTimer resets (or creates) an idle timer for the session.
// When the timer fires without being reset by another Converse call,
// we consider the session ended and emit a SessionEnded event.
func (s *ZarlServer) resetSessionTimer(sessionID string, session *service.Session) {
	if s.bus == nil {
		return
	}
	// Stop existing timer if any.
	if val, ok := s.sessionTimers.Load(sessionID); ok {
		val.(*time.Timer).Stop()
	}
	t := time.AfterFunc(sessionIdleTimeout, func() {
		s.sessionTimers.Delete(sessionID)
		if session.Identity() == "" {
			return
		}
		var msgs []events.Message
		for _, m := range session.History() {
			msgs = append(msgs, events.Message{Role: m.Role, Content: m.Content})
		}
		s.bus.Emit(events.Event{
			Type: events.SessionEnded,
			Payload: events.SessionEndedPayload{
				SessionID:  sessionID,
				PersonName: session.Identity(),
				Messages:   msgs,
			},
		})
	})
	s.sessionTimers.Store(sessionID, t)
}

const maxToolIterations = 6

// chatResult bundles what chatWithTools returns — the final content,
// accumulated thinking from every iteration (already split from content by
// the LLM client), and wall-clock time.
type chatResult struct {
	content  string
	thinking string
	duration float64
}

// chatWithTools runs the LLM with tool support, looping until a text response or max iterations.
// chatTurn carries every piece of state a single Converse RPC needs
// across its setup → tool-loop → final-pass phases. Bundled so phase
// methods stop dragging a 12-arg parameter wall.
type chatTurn struct {
	stream    *connect.ServerStream[zarlv1.ConverseResponse]
	sink      *ttsSink
	session   *service.Session
	sessionID string
	tools     []llm.Tool
	t0        time.Time

	// historyBudget is the per-call TrimWithSummary ceiling. Recomputed
	// for the final pass when no tools ride.
	historyBudget int

	// chartRendered flips when render_chart fires successfully so the
	// no-content fallback ("Here's the chart.") can be emitted to TTS.
	chartRendered bool

	// allThinking accumulates the model's <think> blocks across every
	// chat call this turn (loop + final pass).
	allThinking []string

	// Per-turn dedup state. oncePerTurn tools (e.g. gesture) get a single
	// invocation regardless of iteration count — without it, reasoning
	// models stack 8+ identical calls and burn thousands of thinking
	// tokens. calledSig short-circuits identical-argument re-runs (the
	// "queue the same track twice" cascade) without firing the side
	// effect again.
	oncePerTurn map[string]bool
	called      map[string]bool
	calledSig   map[string]bool
}

// toolOutput is the per-call result the concurrent dispatcher fills in.
// Promoted to a package-level type so the loop and the dispatcher share
// the shape.
type toolOutput struct {
	name    string
	content string
	err     error
}

func (s *ZarlServer) chatWithTools(
	ctx context.Context,
	stream *connect.ServerStream[zarlv1.ConverseResponse],
	sink *ttsSink,
	session *service.Session,
	sessionID string,
) (chatResult, error) {
	if s.convLock != nil {
		s.convLock.Acquire()
		defer s.convLock.Release()
	}
	turn := s.prepareTurn(ctx, stream, sink, session, sessionID)
	if done, res, err := s.runToolLoop(ctx, &turn); err != nil {
		return chatResult{}, err
	} else if done {
		return res, nil
	}
	return s.runFinalPass(ctx, &turn)
}

// prepareTurn renders the per-turn system prompt (live agent name +
// identified speaker + skills), resolves the active toolset, and seeds
// the dedup maps + budget for the loop.
func (s *ZarlServer) prepareTurn(ctx context.Context, stream *connect.ServerStream[zarlv1.ConverseResponse], sink *ttsSink, session *service.Session, sessionID string) chatTurn {
	// Refresh the system message every turn (not at session creation) so
	// prompt edits, identity changes, and movement take effect on the
	// very next response.
	sysPrompt := service.RenderSystemPrompt(
		s.systemPrompt,
		s.agentName,
		session.Identity(),
		service.FormatLocation(session.Location()),
	)
	// Skills compound with identity context — appended after the
	// rendered base prompt rather than replacing it.
	if s.skillSelector != nil {
		if block, err := s.selectSkillsForTurn(ctx, session.History()); err != nil {
			slog.Warn("skill selector for live chat", "err", err)
		} else if block != "" {
			sysPrompt += "\n\n" + block
		}
	}
	session.SetSystemPrompt(sysPrompt)

	tools := s.resolveTools(ctx, session)
	// Tool specs ride on every Chat call and aren't in session.History(),
	// so their token cost must be subtracted from the trim budget — a
	// full 50-tool spec sheet (~6–10k tokens) otherwise silently
	// overflows the 32k per-slot context.
	toolSpecsTokens := service.EstimateToolSpecsTokens(tools)

	return chatTurn{
		stream:        stream,
		sink:          sink,
		session:       session,
		sessionID:     sessionID,
		tools:         tools,
		t0:            time.Now(),
		historyBudget: s.effectiveHistoryBudget(toolSpecsTokens),
		oncePerTurn:   map[string]bool{"gesture": true},
		called:        map[string]bool{},
		calledSig:     map[string]bool{},
	}
}

// runToolLoop runs up to maxToolIterations of chat → tool dispatch →
// append results. Returns (done=true, result) when the model emits a
// pure-text response — that's the natural-language reply the user
// hears. Returns (done=false, _) when the loop exhausts iterations
// without a text turn; chatWithTools then falls through to runFinalPass
// for a no-tools final pass.
func (s *ZarlServer) runToolLoop(ctx context.Context, turn *chatTurn) (done bool, res chatResult, err error) {
	for range maxToolIterations {
		if err := turn.session.TrimWithSummary(ctx, s.llm, turn.historyBudget); err != nil {
			return false, chatResult{}, fmt.Errorf("trim conversation history: %w", err)
		}

		result, err := s.chatOrStream(ctx, turn.stream, turn.sink, turn.session.HistoryForChat(), turn.tools)
		if err != nil {
			return false, chatResult{}, fmt.Errorf("llm chat: %w", err)
		}
		if result.Thinking != "" {
			turn.allThinking = append(turn.allThinking, result.Thinking)
		}

		if len(result.ToolCalls) == 0 {
			res, err := s.finishTextTurn(turn, result.Content)
			return true, res, err
		}

		outputs := s.dispatchToolCalls(ctx, turn, result.ToolCalls)
		for _, out := range outputs {
			if out.name == "render_chart" && out.err == nil {
				turn.chartRendered = true
			}
		}
		turn.session.AddAssistantWithToolCalls(result.Content, result.ToolCalls)
		s.appendToolResults(turn, outputs)
	}
	return false, chatResult{}, nil
}

// dispatchToolCalls fans the model's tool calls out concurrently after
// applying both dedup gates (oncePerTurn + identical-args). Results are
// returned in caller-call order so the post-loop result-append step
// matches the model's ToolCall list.
func (s *ZarlServer) dispatchToolCalls(ctx context.Context, turn *chatTurn, calls []service.ToolCall) []toolOutput {
	outputs := make([]toolOutput, len(calls))
	var wg sync.WaitGroup

	for i, tc := range calls {
		// Stream "executing" status to the UI before any gating so the
		// frontend always reflects what the model just attempted.
		_ = turn.stream.Send(&zarlv1.ConverseResponse{
			Payload: &zarlv1.ConverseResponse_ToolStatus{
				ToolStatus: &zarlv1.ToolStatus{
					ToolName: tc.Function.Name,
					Status:   "executing",
					Summary:  fmt.Sprintf("Calling %s...", tc.Function.Name),
				},
			},
		})

		// Dedup gates run on the controlling goroutine, single-threaded —
		// no mutex needed. Concurrent execution starts after both checks.
		if turn.oncePerTurn[tc.Function.Name] && turn.called[tc.Function.Name] {
			outputs[i] = toolOutput{
				name:    tc.Function.Name,
				content: fmt.Sprintf("%s already used this turn — continue your reply or call a different tool.", tc.Function.Name),
			}
			s.emitToolCall(turn.sessionID, toolCallEvent{
				Name:     tc.Function.Name,
				Provider: s.registry.ProviderFor(tools.ToolName(tc.Function.Name)),
				Error:    "deduped: already called this turn",
				At:       time.Now().UnixMilli(),
			})
			continue
		}
		turn.called[tc.Function.Name] = true

		// json.Marshal sorts map keys deterministically (Go 1.12+) so
		// equivalent arg maps hash identically. Hoist outside the
		// goroutine so the dedup signature and the tool_calls log row
		// share one allocation.
		argsJSON, _ := json.Marshal(tc.Function.Arguments)
		sig := tc.Function.Name + "|" + string(argsJSON)
		if turn.calledSig[sig] {
			outputs[i] = toolOutput{
				name:    tc.Function.Name,
				content: fmt.Sprintf("%s was already called with identical arguments this turn — finalize your reply now or call a different tool.", tc.Function.Name),
			}
			s.emitToolCall(turn.sessionID, toolCallEvent{
				Name:     tc.Function.Name,
				Provider: s.registry.ProviderFor(tools.ToolName(tc.Function.Name)),
				Args:     string(argsJSON),
				Error:    "deduped: identical args this turn",
				At:       time.Now().UnixMilli(),
			})
			continue
		}
		turn.calledSig[sig] = true

		wg.Add(1)
		go func(idx int, call service.ToolCall, argsJSON []byte) {
			defer wg.Done()
			outputs[idx] = s.executeOneToolCall(ctx, turn, call, argsJSON)
		}(i, tc, argsJSON)
	}
	wg.Wait()
	return outputs
}

// executeOneToolCall runs a single tool with a 30s timeout, logs the
// invocation, emits the bus event, and returns the result. Always
// returns a populated toolOutput — an unknown tool produces an err
// field, which the caller renders to the model as "error: …".
func (s *ZarlServer) executeOneToolCall(ctx context.Context, turn *chatTurn, call service.ToolCall, argsJSON []byte) toolOutput {
	tool, ok := s.registry.Tool(tools.ToolName(call.Function.Name))
	if !ok {
		return toolOutput{name: call.Function.Name, err: fmt.Errorf("unknown tool: %s", call.Function.Name)}
	}

	toolCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	toolCtx = context.WithValue(toolCtx, service.CtxPersonName, turn.session.Identity())
	toolCtx = context.WithValue(toolCtx, service.CtxSessionID, turn.sessionID)

	execStart := time.Now()
	tcall := tools.ToolCall{
		ToolName:  tools.ToolName(call.Function.Name),
		Arguments: tools.ToolParameters(call.Function.Arguments),
	}
	tr, execErr := tool.Execute(toolCtx, tcall)
	elapsed := time.Since(execStart).Milliseconds()

	// Derive the strings the surrounding code (DB log, bus event,
	// returned output) consumed. A tool-reported failure (tr.Success
	// false) is conveyed via content text, not a Go error — only a
	// genuine execErr is propagated as toolOutput.err.
	errStr := ""
	content := ""
	switch {
	case execErr != nil:
		errStr = execErr.Error()
		content = fmt.Sprintf("tool %s error: %v", call.Function.Name, execErr)
	case tr != nil && !tr.Success:
		errStr = tr.Error
		content = fmt.Sprintf("tool %s error: %s", call.Function.Name, tr.Error)
	default:
		content = service.ToolResultText(tr)
	}

	if err := s.toolCallRepo.Log(ctx, repository.ToolCall{
		SessionID:  turn.sessionID,
		ToolName:   call.Function.Name,
		Provider:   s.registry.ProviderFor(tools.ToolName(call.Function.Name)),
		Args:       string(argsJSON),
		Result:     content,
		Error:      errStr,
		DurationMs: int(elapsed),
	}); err != nil {
		slog.WarnContext(ctx, "log tool call", "err", err)
	}
	s.emitToolCall(turn.sessionID, toolCallEvent{
		Name:       call.Function.Name,
		Provider:   s.registry.ProviderFor(tools.ToolName(call.Function.Name)),
		Args:       string(argsJSON),
		Result:     content,
		Error:      errStr,
		DurationMs: int(elapsed),
		At:         time.Now().UnixMilli(),
	})
	return toolOutput{name: call.Function.Name, content: content, err: execErr}
}

// appendToolResults streams completion-status frames to the client and
// feeds each tool result back into the session so the next iteration's
// chat call sees them. Errors get rendered as "error: …" so the model
// stays in the conversation rather than silently looping.
func (s *ZarlServer) appendToolResults(turn *chatTurn, outputs []toolOutput) {
	for _, out := range outputs {
		status := "completed"
		content := out.content
		if out.err != nil {
			status = "failed"
			content = fmt.Sprintf("error: %v", out.err)
		}
		_ = turn.stream.Send(&zarlv1.ConverseResponse{
			Payload: &zarlv1.ConverseResponse_ToolStatus{
				ToolStatus: &zarlv1.ToolStatus{
					ToolName: out.name,
					Status:   status,
					Summary:  content,
				},
			},
		})
		turn.session.AddToolResult(content)
	}
}

// finishTextTurn closes the loop on a pure-text response from the
// model. Substitutes a "Here's the chart." fallback when the model
// emits no content but render_chart fired this turn — the streaming
// path pushed nothing to TTS, so the fallback has to land via sink.push
// or it'll never be spoken.
func (s *ZarlServer) finishTextTurn(turn *chatTurn, content string) (chatResult, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" && turn.chartRendered {
		trimmed = "Here's the chart."
		if err := turn.sink.push(trimmed); err != nil {
			return chatResult{}, fmt.Errorf("push tts fallback: %w", err)
		}
	}
	return chatResult{
		content:  trimmed,
		thinking: strings.Join(turn.allThinking, "\n\n"),
		duration: time.Since(turn.t0).Seconds(),
	}, nil
}

// runFinalPass forces a no-tools chat after the tool loop exhausts
// maxToolIterations. The model gets the full budget for history (no
// specs cost) and is expected to produce a natural-language wrap-up.
func (s *ZarlServer) runFinalPass(ctx context.Context, turn *chatTurn) (chatResult, error) {
	if err := turn.session.TrimWithSummary(ctx, s.llm, s.effectiveHistoryBudget(0)); err != nil {
		return chatResult{}, fmt.Errorf("trim conversation history: %w", err)
	}
	result, err := s.chatOrStream(ctx, turn.stream, turn.sink, turn.session.HistoryForChat(), nil)
	if err != nil {
		return chatResult{}, fmt.Errorf("llm chat (final): %w", err)
	}
	if result.Thinking != "" {
		turn.allThinking = append(turn.allThinking, result.Thinking)
	}
	return s.finishTextTurn(turn, result.Content)
}

func (s *ZarlServer) handleTextInput(
	ctx context.Context,
	stream *connect.ServerStream[zarlv1.ConverseResponse],
	session *service.Session,
	sessionID string,
	input *zarlv1.TextInput,
) error {
	// Voice commands (also work via text)
	s.checkVoiceCommands(ctx, session, input.Text, input.ImageJpeg)

	// Face identification
	faceContext := s.identifyFace(session, input.ImageJpeg)

	// Inject relevant task findings based on what the user said
	s.injectRelevantFindings(ctx, session, input.Text)

	content := service.BuildUserContent("", len(input.ImageJpeg) > 0, input.Text)
	if faceContext != "" {
		content = faceContext + " " + content
	}

	var images []string
	if len(input.ImageJpeg) > 0 {
		images = []string{base64.StdEncoding.EncodeToString(input.ImageJpeg)}
	}
	session.AddUser(content, images)

	sink := newTTSSink(ctx, stream, s.synthesizer, s.settings)
	cr, err := s.chatWithTools(ctx, stream, sink, session, sessionID)
	if err != nil {
		return fmt.Errorf("chat: %w", err)
	}
	if err := sink.close(); err != nil {
		return fmt.Errorf("close tts: %w", err)
	}

	session.AddAssistant(cr.content)
	slog.Info("llm response", "duration_sec", fmt.Sprintf("%.2f", cr.duration), "text", cr.content)
	s.emitThinking(sessionID, cr.thinking)

	if err := stream.Send(&zarlv1.ConverseResponse{
		Payload: &zarlv1.ConverseResponse_Text{
			Text: &zarlv1.TextResponse{
				Text:        cr.content,
				DurationSec: float32(cr.duration),
			},
		},
	}); err != nil {
		return fmt.Errorf("send text: %w", err)
	}
	return nil
}

// toolCallEvent is the JSON shape of a `tool_call` notification. Mirrors
// the tool_calls table row so the frontend panel can render the same
// columns it would see in the admin history, but live.
type toolCallEvent struct {
	Name       string `json:"name"`
	Provider   string `json:"provider,omitempty"`
	Args       string `json:"args,omitempty"`
	Result     string `json:"result,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMs int    `json:"duration_ms"`
	At         int64  `json:"at"`
}

// emitToolCall broadcasts a live tool-call event so the frontend can show
// each invocation with its args/result/timing. Separate from the repo Log
// above — that's the durable audit trail; this is the ambient stream.
func (s *ZarlServer) emitToolCall(sessionID string, ev toolCallEvent) {
	if s.notifications == nil {
		return
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		slog.Warn("marshal tool_call event", "err", err)
		return
	}
	slog.Info("emit tool_call", "session_id", sessionID, "tool", ev.Name, "duration_ms", ev.DurationMs, "ok", ev.Error == "")
	s.notifications.Push(znotify.Notification{
		SessionID: sessionID,
		ToolName:  "tool_call",
		Content:   string(payload),
		Broadcast: true,
	})
}

// selectSkillsForTurn picks and renders the skills most relevant to
// the user's latest message, scoped to the "default" profile (live
// chat always runs under that binding) plus any globally-bound
// skills. Returns the rendered markdown block or empty if no skills
// matched / no selector configured.
func (s *ZarlServer) selectSkillsForTurn(ctx context.Context, history []service.Message) (string, error) {
	if s.skillSelector == nil {
		return "", nil
	}
	query := latestUserText(history)
	skills, err := s.skillSelector.Select(ctx, "default", query)
	if err != nil {
		return "", err
	}
	return taskrunner.RenderSkills(skills), nil
}

// chatOrStream runs a single LLM call and, when the LLM supports
// streaming, forwards ReasoningChunk / TextChunk payloads to the client
// and pushes content fragments into the TTS sink as they arrive. Returns
// an aggregate ChatResult so the surrounding tool loop and session
// history can operate as if the call were batched.
// Providers that don't implement StreamingChatClient fall through to the
// non-streaming Chat() path — the resulting content is pushed to the
// sink in one shot so the TTS path is identical to the old behaviour.
func (s *ZarlServer) chatOrStream(
	ctx context.Context,
	stream *connect.ServerStream[zarlv1.ConverseResponse],
	sink *ttsSink,
	history []service.Message,
	tools []llm.Tool,
) (service.ChatResult, error) {
	sc, ok := s.llm.(service.StreamingChatClient)
	if !ok {
		r, err := s.llm.Chat(ctx, history, tools)
		if err != nil {
			return service.ChatResult{}, err
		}
		if err := sink.push(r.Content); err != nil {
			return service.ChatResult{}, fmt.Errorf("push tts: %w", err)
		}
		return r, nil
	}

	ch := sc.ChatStream(ctx, history, tools)
	var content, reasoning strings.Builder
	var toolCalls []service.ToolCall
	for d := range ch {
		if d.Err != nil {
			return service.ChatResult{}, d.Err
		}
		if d.Done {
			toolCalls = d.ToolCalls
			continue
		}
		if d.Content != "" {
			content.WriteString(d.Content)
			// TTS is deferred until we know whether this iteration ends
			// with tool calls. If it does, the streamed content is
			// pre-tool narration that the next iteration re-synthesizes
			// from the tool result — speaking it now produces the
			// duplicate read-back users see on multi-iteration turns
			// (e.g. a playlist build that runs N spotify_search calls
			// would otherwise have its track list spoken N+1 times).
			// Visual streaming to the frontend is unaffected.
			if err := stream.Send(&zarlv1.ConverseResponse{
				Payload: &zarlv1.ConverseResponse_TextChunk{
					TextChunk: &zarlv1.TextChunk{Text: d.Content},
				},
			}); err != nil {
				return service.ChatResult{}, fmt.Errorf("send text_chunk: %w", err)
			}
		}
		if d.Reasoning != "" {
			reasoning.WriteString(d.Reasoning)
			if err := stream.Send(&zarlv1.ConverseResponse{
				Payload: &zarlv1.ConverseResponse_ReasoningChunk{
					ReasoningChunk: &zarlv1.ReasoningChunk{Text: d.Reasoning},
				},
			}); err != nil {
				return service.ChatResult{}, fmt.Errorf("send reasoning_chunk: %w", err)
			}
		}
	}

	if len(toolCalls) == 0 {
		if err := sink.push(content.String()); err != nil {
			return service.ChatResult{}, fmt.Errorf("push tts: %w", err)
		}
	}

	return service.ChatResult{
		Content:   content.String(),
		Thinking:  reasoning.String(),
		ToolCalls: toolCalls,
	}, nil
}

// emitThinking broadcasts a model's internal reasoning as a separate
// notification so the frontend can opt-in show it. Silently skips when the
// LLM produced no thinking.
func (s *ZarlServer) emitThinking(sessionID, thinking string) {
	if thinking == "" || s.notifications == nil {
		slog.Debug("no thinking to emit", "session_id", sessionID)
		return
	}
	slog.Info("emit thinking", "session_id", sessionID, "chars", len(thinking))
	s.notifications.Push(znotify.Notification{
		SessionID: sessionID,
		ToolName:  "thinking",
		Content:   thinking,
		Broadcast: true,
	})
}

// identifyFace runs face recognition and returns identity context for the LLM prompt.
func (s *ZarlServer) identifyFace(session *service.Session, imageJpeg []byte) string {
	if s.face == nil || len(imageJpeg) == 0 {
		// No image this turn — keep whatever identity we have
		if session.Identity() != "" {
			return s.buildIdentityContext(session.Identity())
		}
		return ""
	}

	name, embedding, photo, notes, err := s.face.Identify(imageJpeg)
	if err != nil {
		// No face detected in frame — clear identity, someone may have left
		session.Identify("")
		return ""
	}

	if name != "" {
		// Recognized — update identity (could be a different person than before)
		if name != session.Identity() {
			slog.Info("face changed", "from", session.Identity(), "to", name)
			session.ClearEnrollment()
		}
		session.Identify(name)
		ctx := s.buildIdentityContext(name)
		if notes != "" {
			ctx += fmt.Sprintf(" Notes about %s: %s", name, notes)
		}
		return ctx
	}

	// Unknown face — different person sat down
	if session.Identity() != "" {
		slog.Info("unknown face, clearing previous identity", "was", session.Identity())
		session.Identify("")
	}
	// Ask their name (reset askedForName since this is a new person)
	session.PendEnrollment(embedding)
	session.PendPhoto(photo)
	session.ResetAskedForName()
	session.AskForName()
	return "You see someone you don't recognize. The previous person has left. Ask the new person their name naturally."
}

// buildIdentityContext assembles the full context for a recognized person:
// greeting, memories from Qdrant, and recent conversation summaries.
func (s *ZarlServer) buildIdentityContext(name string) string {
	var ctx strings.Builder
	fmt.Fprintf(&ctx, "You are speaking with %s.", name)

	// Auto-load memories from Qdrant
	if s.qdrant != nil && s.llm != nil {
		memories, err := memory.LoadRecentMemories(context.Background(), s.qdrant, s.llm, name, 5)
		if err != nil {
			slog.Warn("load memories", "error", err)
		} else if len(memories) > 0 {
			fmt.Fprintf(&ctx, "\nRecent memories about %s:\n", name)
			for _, m := range memories {
				fmt.Fprintf(&ctx, "- %s\n", m)
			}
		}
	}

	// Load recent conversation summaries
	if s.summaries != nil {
		summaries, err := s.summaries.ListRecent(context.Background(), name, 5)
		if err != nil {
			slog.Warn("load conversation summaries", "error", err)
		} else if len(summaries) > 0 {
			slog.Info("injecting conversation summaries", "person", name, "count", len(summaries))
			ctx.WriteString("\n\nPrevious conversations with " + name + ":\n")
			for _, sum := range summaries {
				ctx.WriteString("- " + sum.CreatedAt + ": " + sum.Summary + "\n")
			}
		}
	}

	return ctx.String()
}

// checkVoiceCommands detects face-related voice commands in transcription.
func (s *ZarlServer) checkVoiceCommands(ctx context.Context, session *service.Session, text string, imageJpeg []byte) {
	lower := strings.ToLower(text)

	if strings.Contains(lower, "forget my face") || strings.Contains(lower, "forget me") {
		if name := session.Identity(); name != "" && s.face != nil {
			if err := s.face.Forget(name); err != nil {
				slog.WarnContext(ctx, "face forget", "name", name, "err", err)
			}
			session.Identify("")
			slog.InfoContext(ctx, "face forgotten", "name", name)
		}
		return
	}

	if strings.Contains(lower, "remember me as ") {
		parts := strings.SplitN(lower, "remember me as ", 2)
		if len(parts) == 2 && s.face != nil {
			newName := strings.TrimSpace(parts[1])
			newName = strings.TrimRight(newName, ".!? ")
			if newName != "" {
				// Capitalize
				newName = strings.ToUpper(newName[:1]) + newName[1:]
				// Try pending enrollment first, otherwise re-identify from image
				emb := session.PendingEnrollment()
				photo := session.PendingPhoto()
				if emb == nil && len(imageJpeg) > 0 {
					_, freshEmb, freshPhoto, _, err := s.face.Identify(imageJpeg)
					if err == nil {
						emb = freshEmb
						photo = freshPhoto
					}
				}
				if emb != nil {
					// Remove old name if exists
					if old := session.Identity(); old != "" {
						if err := s.face.Forget(old); err != nil {
							slog.WarnContext(ctx, "face forget", "name", old, "err", err)
						}
					}
					if err := s.face.Enroll(newName, [][]float32{emb}, photo); err != nil {
						slog.WarnContext(ctx, "face enroll", "name", newName, "err", err)
					}
					session.Identify(newName)
					session.ClearEnrollment()
					slog.InfoContext(ctx, "face enrolled via command", "name", newName)
				}
			}
		}
	}
}

// tryEnrollFromResponse checks if we're in enrollment mode and extracts name from user speech.
func (s *ZarlServer) tryEnrollFromResponse(ctx context.Context, session *service.Session, transcription string) {
	if !session.HasPendingEnrollment() || s.face == nil {
		return
	}

	text := strings.TrimSpace(transcription)
	name := ""

	patterns := []string{
		"i'm ", "im ", "i am ", "my name is ", "call me ", "it's ", "its ",
		"this is ", "hey i'm ", "hey im ", "hey, i'm ", "hey, im ",
	}

	lower := strings.ToLower(text)
	for _, p := range patterns {
		if idx := strings.Index(lower, p); idx != -1 {
			after := strings.TrimSpace(text[idx+len(p):])
			parts := strings.Fields(after)
			if len(parts) > 0 {
				name = strings.TrimRight(parts[0], ".,!?")
				break
			}
		}
	}

	// Fallback: if the entire response is 1-2 words, treat it as the name
	if name == "" {
		words := strings.Fields(text)
		if len(words) >= 1 && len(words) <= 2 {
			name = strings.TrimRight(words[len(words)-1], ".,!?")
		}
	}

	if name != "" {
		if len(name) > 0 {
			name = strings.ToUpper(name[:1]) + name[1:]
		}
		if err := s.face.Enroll(name, [][]float32{session.PendingEnrollment()}, session.PendingPhoto()); err != nil {
			slog.WarnContext(ctx, "face enroll", "name", name, "err", err)
		}
		session.Identify(name)
		session.ClearEnrollment()
		slog.InfoContext(ctx, "face enrolled", "name", name)
	}
}

func (s *ZarlServer) handleAudioInput(
	ctx context.Context,
	stream *connect.ServerStream[zarlv1.ConverseResponse],
	session *service.Session,
	sessionID string,
	input *zarlv1.AudioInput,
) error {
	if s.transcriber == nil {
		return connect.NewError(connect.CodeUnimplemented, fmt.Errorf("STT not configured"))
	}
	if len(input.Wav) <= 44 {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("empty audio"))
	}

	text, ok, err := s.transcribeAndStream(ctx, stream, input.Wav)
	if err != nil || !ok {
		return err
	}

	// Enrichments — voice commands, face id, pending enrolment, finding
	// injection. Each is best-effort; nothing here can fail the turn.
	s.checkVoiceCommands(ctx, session, text, input.ImageJpeg)
	s.tryEnrollFromResponse(ctx, session, text)
	faceContext := s.identifyFace(session, input.ImageJpeg)
	s.injectRelevantFindings(ctx, session, text)

	s.appendUserTurn(session, text, input.ImageJpeg, faceContext)

	sink := newTTSSink(ctx, stream, s.synthesizer, s.settings)
	cr, err := s.chatWithTools(ctx, stream, sink, session, sessionID)
	if err != nil {
		return fmt.Errorf("chat: %w", err)
	}
	if err := sink.close(); err != nil {
		return fmt.Errorf("close tts: %w", err)
	}
	return s.streamTextResponse(stream, session, sessionID, cr)
}

// transcribeAndStream runs STT, logs timing, suppresses noise/non-speech,
// and streams the transcription frame to the client. Returns (text,
// ok=true) on a real utterance the caller should act on; (_, ok=false,
// nil) when STT picked up silence or noise that the loop should skip;
// and (_, _, err) on a hard failure (transport send / underlying STT).
func (s *ZarlServer) transcribeAndStream(ctx context.Context, stream *connect.ServerStream[zarlv1.ConverseResponse], wav []byte) (string, bool, error) {
	t0 := time.Now()
	text, err := s.transcriber.Transcribe(ctx, wav)
	if err != nil {
		return "", false, fmt.Errorf("transcribe: %w", err)
	}
	sttTime := time.Since(t0).Seconds()
	slog.Info("stt", "duration_sec", fmt.Sprintf("%.2f", sttTime), "text", text)

	if service.IsNonSpeech(text) {
		if text != "" {
			slog.Info("stt suppressed non-speech", "text", text)
		}
		return "", false, nil
	}
	if err := stream.Send(&zarlv1.ConverseResponse{
		Payload: &zarlv1.ConverseResponse_Transcription{
			Transcription: &zarlv1.Transcription{
				Text:        text,
				DurationSec: float32(sttTime),
			},
		},
	}); err != nil {
		return "", false, fmt.Errorf("send transcription: %w", err)
	}
	return text, true, nil
}

// appendUserTurn builds the multimodal user message (text + optional
// face-context prefix + optional base64 image) and pushes it onto the
// session. Face context lives at the front of the content so the
// model's first token of context is "who is speaking".
func (s *ZarlServer) appendUserTurn(session *service.Session, text string, imageJpeg []byte, faceContext string) {
	content := service.BuildUserContent(text, len(imageJpeg) > 0, "")
	if faceContext != "" {
		content = faceContext + " " + content
	}
	var images []string
	if len(imageJpeg) > 0 {
		images = []string{base64.StdEncoding.EncodeToString(imageJpeg)}
	}
	session.AddUser(content, images)
}

// streamTextResponse closes out a turn: append the assistant's reply
// to session history, log timing, emit the thinking event for admin
// observability, and send the final text frame to the client.
func (s *ZarlServer) streamTextResponse(stream *connect.ServerStream[zarlv1.ConverseResponse], session *service.Session, sessionID string, cr chatResult) error {
	session.AddAssistant(cr.content)
	slog.Info("llm response", "duration_sec", fmt.Sprintf("%.2f", cr.duration), "text", cr.content)
	s.emitThinking(sessionID, cr.thinking)

	if err := stream.Send(&zarlv1.ConverseResponse{
		Payload: &zarlv1.ConverseResponse_Text{
			Text: &zarlv1.TextResponse{
				Text:        cr.content,
				DurationSec: float32(cr.duration),
			},
		},
	}); err != nil {
		return fmt.Errorf("send text: %w", err)
	}
	return nil
}

func (s *ZarlServer) SubscribeNotifications(
	ctx context.Context,
	req *connect.Request[zarlv1.SubscribeNotificationsRequest],
	stream *connect.ServerStream[zarlv1.SubscribeNotificationsResponse],
) error {
	sessionID := req.Msg.SessionId
	if sessionID == "" {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("session_id required"))
	}

	ch := s.notifications.Subscribe(ctx, sessionID)
	defer s.notifications.Unsubscribe(sessionID, ch)

	slog.Info("notification subscription started", "session_id", sessionID)

	for {
		select {
		case <-ctx.Done():
			return nil
		case n, ok := <-ch:
			if !ok {
				return nil
			}
			if err := stream.Send(&zarlv1.SubscribeNotificationsResponse{
				ToolName: n.ToolName,
				Content:  n.Content,
			}); err != nil {
				return fmt.Errorf("send notification: %w", err)
			}
		}
	}
}

// injectRelevantFindings searches task findings by semantic similarity to the
// user's message and adds any relevant results to the session as context.
func (s *ZarlServer) injectRelevantFindings(ctx context.Context, session *service.Session, userText string) {
	if s.qdrant == nil || s.llm == nil || userText == "" {
		return
	}
	if findings := s.loadRelevantFindings(ctx, userText); findings != "" {
		slog.Info("injecting task findings", "query", truncateStr(userText, 50))
		session.AddUser(findings, nil)
	}
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func (s *ZarlServer) loadRelevantFindings(ctx context.Context, query string) string {
	vec, err := s.llm.Embed(ctx, query)
	if err != nil {
		slog.Warn("embed query for findings", "error", err)
		return ""
	}
	results, err := s.qdrant.Search(ctx, qdrant.SearchRequest{
		Collection: "task_findings",
		Vector:     vec,
		Limit:      3,
	})
	if err != nil {
		slog.Warn("search task findings", "error", err)
		return ""
	}
	var relevant []string
	for _, r := range results {
		if r.Score < 0.7 {
			continue
		}
		content, _ := r.Payload["content"].(string)
		taskPrompt, _ := r.Payload["task_prompt"].(string)
		if content != "" {
			relevant = append(relevant, fmt.Sprintf("[Task: %q] %s", taskPrompt, content))
		}
	}
	if len(relevant) == 0 {
		return ""
	}
	return "[Relevant research findings:\n" + strings.Join(relevant, "\n") + "]"
}

// stripMarkdown removes common markdown formatting that causes TTS artifacts.
func stripMarkdown(text string) string {
	// Remove bold/italic markers
	text = strings.ReplaceAll(text, "***", "")
	text = strings.ReplaceAll(text, "**", "")
	text = strings.ReplaceAll(text, "*", "")
	// Remove headers
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " ")
		if strings.HasPrefix(trimmed, "# ") || strings.HasPrefix(trimmed, "## ") || strings.HasPrefix(trimmed, "### ") {
			lines[i] = strings.TrimLeft(trimmed, "# ")
		}
		// Remove horizontal rules
		if trimmed == "---" || trimmed == "***" || trimmed == "___" {
			lines[i] = ""
		}
	}
	text = strings.Join(lines, "\n")
	// Remove inline code backticks
	text = strings.ReplaceAll(text, "`", "")
	// Remove link syntax [text](url) -> text
	for {
		start := strings.Index(text, "[")
		if start == -1 {
			break
		}
		mid := strings.Index(text[start:], "](")
		if mid == -1 {
			break
		}
		end := strings.Index(text[start+mid:], ")")
		if end == -1 {
			break
		}
		linkText := text[start+1 : start+mid]
		text = text[:start] + linkText + text[start+mid+end+1:]
	}
	return text
}
