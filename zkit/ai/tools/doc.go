// Package tools defines zkit's canonical tool interface, registry, and result
// types for AI agent tool execution.
//
// Tools expose typed names, JSON-schema-like parameters, effects metadata, and
// structured error kinds. Consumers compose Tool, Iterable, Executor, and Source
// rather than depending on a product-specific registry implementation.
//
// First-party tools should prefer typed argument/result structs via NewTyped,
// or SchemaFor[Args] plus DecodeArgs[Args] when they need custom validation.
// Direct ToolParameters access is the escape hatch for genuinely dynamic JSON
// shapes, not the default implementation style.
package tools
