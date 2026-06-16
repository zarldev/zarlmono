// Package taskscope stores lightweight task metadata on context.Context.
//
// Agent runners and tools use this package to pass the current task ID and
// spawn-recursion depth through call chains without widening every interface.
// The values are observational metadata for logging, cancellation routing,
// guardrails, and spawn-depth enforcement; they are not authentication or
// authorization data.
package taskscope
