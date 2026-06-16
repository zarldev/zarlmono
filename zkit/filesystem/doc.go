// Package filesystem defines file-system abstractions with in-memory, OS, and
// SeaweedFS-backed implementations.
//
// Use the small interfaces for dependency injection in code that only needs a
// subset of filesystem behavior. Concrete adapters currently live in this
// package; the SeaweedFS implementation is a future split candidate if adapter
// dependencies become too heavy for core consumers.
package filesystem
