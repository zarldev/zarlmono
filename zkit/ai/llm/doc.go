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
//
// Portable behavior belongs above adapters: callers and the runner supply the
// same inspectable prompt and neutral content/tool contract to every provider.
// [Message.ReasoningContent] is the displayable reasoning projection; it may be
// rendered to users and reshaped by adapters. [Message.ContinuationItems] is a
// separate opaque replay channel for complete provider-native output items.
// Adapters replay only items whose Provider and Format they own, preserving the
// exact payload and native output order, and ignore foreign items so switching
// providers cannot leak backend state.
//
// The current contract is deliberately stateless and request-scoped. Stateful
// response chaining and provider beta controls that cannot be represented by
// the stable neutral request are deferred to future typed, opt-in capabilities;
// they must not be smuggled through continuation items or widen Provider.
package llm
