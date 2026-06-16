// Package options provides the canonical functional-options type used across
// zkit.
//
// The package intentionally exposes only a small generic shape:
// Option[T] func(*T). Packages define their own option constructors and apply
// them to local config structs rather than inventing package-specific option
// aliases.
package options
