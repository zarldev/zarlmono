// Package docstore provides concrete in-memory and MongoDB document stores.
// Document identities are explicit Records and values must provide their own
// deep-copy operation, ensuring every store boundary is an independent snapshot.
package docstore
