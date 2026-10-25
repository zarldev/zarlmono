package engine

import (
	programtools "github.com/zarldev/zarlmono/zkit/agent/tools/program"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// rotateProgramOutputsLocked requires mu and the conversation publication gate.
// Closing before replacement revokes both reads and late writes from every old
// source, including children. Mode changes and ordinary turns never call this.
func (l *LiveRunner) rotateProgramOutputsLocked() {
	l.programOutputs.Close()
	if !l.closing {
		l.programOutputs = programtools.NewOutputStore()
	}
}

func (l *LiveRunner) restoreLegacyContext(messages []llm.Message, clearMCP bool) {
	l.context.mu.Lock()
	defer l.context.mu.Unlock()
	l.publication.Lock()
	defer l.publication.Unlock()
	l.context.restoreLocked(messages)
	if clearMCP {
		_ = l.restoreMCPActivations(MCPActivations{})
	}
	l.mu.Lock()
	l.rotateProgramOutputsLocked()
	l.mu.Unlock()
}
