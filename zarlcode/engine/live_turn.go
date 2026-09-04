package engine

import (
	"context"
	"errors"
	"maps"
	"sync"

	agentcompact "github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
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

func (l *LiveRunner) beginTurn(ctx context.Context) (context.Context, func(), error) {
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	l.mu.Lock()
	if l.closing {
		l.mu.Unlock()
		cancel()
		return nil, nil, errors.New("live runner is closing")
	}
	l.turnCancel = cancel
	l.turnDone = done
	l.mu.Unlock()

	var once sync.Once
	finish := func() {
		once.Do(func() {
			cancel()
			close(done)
			l.mu.Lock()
			if l.turnDone == done {
				l.turnCancel = nil
				l.turnDone = nil
			}
			l.mu.Unlock()
		})
	}
	return runCtx, finish, nil
}

func (l *LiveRunner) RunTurnWithAttachments(ctx context.Context, prompt string, attachments []llm.ContentPart) error {
	return l.context.transition(runner.TaskSpec{Prompt: prompt, Attachments: attachments}, func() (func(runner.TaskSpec) runner.TaskResult, error) {
		runCtx, finish, err := l.beginTurn(ctx)
		if err != nil {
			return nil, err
		}
		r, thinking, resources, err := l.buildTurnWithSource(runCtx, l.source, runner.WithContextBreakdown())
		if err != nil {
			finish()
			return nil, err
		}
		return func(spec runner.TaskSpec) runner.TaskResult {
			defer finish()
			spec.Thinking = thinking
			result := r.Run(runCtx, spec)
			if closeErr := resources.Close(runCtx); closeErr != nil && result.Err == nil {
				result.Reason = runner.TerminalError
				result.Err = closeErr
			}
			return result
		}, nil
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
