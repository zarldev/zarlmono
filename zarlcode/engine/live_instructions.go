package engine

import (
	"context"

	"github.com/zarldev/zarlmono/zarlcode/instructions"
)

func (l *LiveRunner) reloadInstructions(ctx context.Context) error {
	if l == nil {
		return nil
	}
	root := l.ws.Root()
	docs, errs := instructions.DiscoverRootContext(ctx, root, instructions.DefaultMaxBytes)
	nested, nestedErrs := instructions.ListNestedContext(ctx, root)
	errs = append(errs, nestedErrs...)
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := context.Cause(ctx); err != nil {
		return err
	}
	l.instructionDocs = append([]instructions.Document(nil), docs...)
	l.instructionErrs = append([]error(nil), errs...)
	l.nestedInstructionIndex = append([]instructions.NestedDoc(nil), nested...)
	return nil
}
