// Package tools defines zkit's canonical tool interface, registry, and result
// types for AI agent tool execution.
//
// Tools expose typed names, JSON-schema-like parameters, effects metadata, and
// structured error kinds. Consumers compose Tool, Iterable, Executor, and Source
// rather than depending on a product-specific registry implementation.
package tools
