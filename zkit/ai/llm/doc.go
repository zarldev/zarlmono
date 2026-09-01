// Package llm defines zkit's provider-neutral language-model contract.
//
// [Provider] is deliberately narrow: it constructs a fully lazy
// [CompletionStream] and reports its name. Calling Provider.Complete performs no
// I/O or other external work; all request preparation, resource acquisition,
// and operational failures begin only when the returned stream is invoked.
// Streams are synchronous, directly rangeable, one-shot, and non-concurrent by
// contract.
//
// Requests and their reference-backed fields are borrowed until stream
// invocation returns. Yielded reference-backed chunk fields are borrowed only
// until the yield callback returns; retaining consumers use
// [CompletionChunk.Clone]. Providers and middleware forward synchronously and
// stop immediately when downstream returns false.
//
// Normal stream return is successful completion. A terminal failure is one
// yield of a zero [CompletionChunk] and a non-nil error, followed immediately by
// return. No completion chunk contains lifecycle Done or Error fields. Finish
// reason and usage are optional ordinary metadata, including meaningful
// metadata-only chunks, and never indicate end-of-stream. Usage presence is
// represented by CompletionChunk.UsageReported so a reported all-zero value is
// distinguishable from absence.
//
// Richer capabilities, such as model discovery or OAuth-backed construction,
// remain separate opt-in interfaces or backend helpers rather than widening
// Provider.
package llm
