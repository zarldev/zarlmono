// Package retrieval defines dependency-light RAG primitives: documents,
// chunking, embedding, vector storage, retrieval, and reranking. Concrete
// stores and model backends live outside this package so agents can depend on
// the small interfaces without inheriting provider dependencies.
package retrieval
