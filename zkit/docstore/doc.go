// Package docstore provides a typed document-store abstraction with metadata and
// fluent querying.
//
// Stores work with application document types that expose stable IDs. The common
// query interface supports equality, range, membership, existence, and regex
// predicates while concrete implementations map those operators to their backing
// store, such as memory or MongoDB.
package docstore
