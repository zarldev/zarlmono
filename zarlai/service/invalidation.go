package service

// InvalidationBumper is implemented by caches whose derived state goes stale
// when an upstream store changes (e.g. the tool-selector embedding index, or
// prompt/skill caches). Stores that drive such caches take bumpers as
// dependencies and call BumpVersion on every mutation.
type InvalidationBumper interface {
	BumpVersion()
}
