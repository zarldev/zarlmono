package engine

import "github.com/zarldev/zarlmono/zarlcode/instructions"

func (l *LiveRunner) reloadInstructions() []error {
	if l == nil {
		return nil
	}
	root := l.ws.Root()
	docs, errs := instructions.DiscoverRoot(root, instructions.DefaultMaxBytes)
	nested, nestedErrs := instructions.ListNested(root)
	errs = append(errs, nestedErrs...)
	l.mu.Lock()
	l.instructionDocs = append([]instructions.Document(nil), docs...)
	l.instructionErrs = append([]error(nil), errs...)
	l.nestedInstructionIndex = append([]instructions.NestedDoc(nil), nested...)
	l.mu.Unlock()
	return errs
}
