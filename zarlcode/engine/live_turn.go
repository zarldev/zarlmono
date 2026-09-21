package engine

import (
	"context"
	"errors"
	"maps"

	agentcompact "github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/options"
)

// ManualCompactionResult reports the effect of a user-triggered conversation
// compaction.
type ManualCompactionResult struct {
	MessagesBefore int
	MessagesAfter  int
	BytesTrimmed   int
	Engine         string
}

// CompactNow immediately applies the configured compaction engine to the live
func (l *LiveRunner) CompactNow(ctx context.Context) (ManualCompactionResult, error) {
	if l == nil {
		return ManualCompactionResult{}, errors.New("compact now: live runner is nil")
	}
	if !l.admission.enter() {
		return ManualCompactionResult{}, l.admission.rejection()
	}
	defer l.admission.leave()
	l.mu.Lock()
	tgt := l.target
	settings := l.settings
	l.mu.Unlock()

	prov, model, window := tgt.Provider, tgt.Model, tgt.Window
	if window <= 0 {
		window = LiveContextWindow
	}
	engineName, compactProv, compactModel := agentcompact.EngineTiered, prov, model
	if settings != nil {
		engineName = settings.CompactEngine(ctx)
		compactProv, compactModel = settings.CompactorProvider(ctx, prov, model)
	}
	return l.context.compactNow(ctx, buildLiveCompactor(engineName, window, compactProv, compactModel, l, l.ws.Root()), l.sink)
}

func (l *LiveRunner) RunTurn(ctx context.Context, prompt string) error {
	return l.RunTurnWithAttachments(ctx, prompt, nil)
}

func (l *LiveRunner) RunTurnWithAttachments(ctx context.Context, prompt string, attachments []llm.ContentPart) error {
	if !l.admission.enter() {
		return l.admission.rejection()
	}
	defer l.admission.leave()
	return l.runTurnAdmitted(ctx, runner.TaskSpec{Prompt: prompt, Attachments: attachments}, func() {})
}

// onPrepared converts an exclusive reservation only after the turn's target and
// dependencies have been built. finish covers context commit as well as child
// drain; ConversationEnded alone is not this boundary.
func (l *LiveRunner) runTurnAdmitted(ctx context.Context, spec runner.TaskSpec, onPrepared func(), historyOptions ...options.Option[runner.Runner]) error {
	var finish func()
	return l.context.transition(ctx, spec, func() (func(runner.TaskSpec) runner.TaskResult, error) {
		runCtx, end, err := l.beginTurn(ctx)
		if err != nil {
			return nil, err
		}
		finish = end
		turn, err := l.buildTurnWithSource(runCtx, l.source, append(historyOptions, runner.WithContextBreakdown())...)
		if err != nil {
			return nil, err
		}
		onPrepared()
		return func(spec runner.TaskSpec) runner.TaskResult {
			defer turn.close(runCtx)
			spec.Thinking = turn.thinking
			scope, err := turn.group.Bind(runCtx, spec.ID)
			if err != nil {
				return runner.TaskResult{ID: spec.ID, Reason: runner.TerminalError, Err: err}
			}
			defer func() { _ = scope.Close(context.WithoutCancel(runCtx)) }()
			return turn.runner.Run(runCtx, spec)
		}, nil
	}, func() {
		if finish != nil {
			finish()
		}
	})
}

func (l *LiveRunner) thinkingEnabledForLocked(tgt RunTarget) bool {
	if l.settings == nil || l.settings.Registry == nil {
		return false
	}
	return l.settings.Registry.Capabilities(tgt.Spec.Name, tgt.Model).SupportsThinking
}
func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	return out
}
