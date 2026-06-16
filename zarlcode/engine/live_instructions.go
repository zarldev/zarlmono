package engine

import "github.com/zarldev/zarlmono/zarlcode/instructions"

func (l *LiveRunner) reloadInstructions() []error {
	if l == nil {
		return nil
	}
	docs, errs := instructions.Discover(l.ws.Root(), instructions.DefaultMaxBytes)
	l.mu.Lock()
	l.instructionDocs = append([]instructions.Document(nil), docs...)
	l.instructionErrs = append([]error(nil), errs...)
	l.mu.Unlock()
	return errs
}
