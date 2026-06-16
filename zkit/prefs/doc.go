// Package prefs is the single funnel for persisted preferences: plaintext
// settings and encrypted secrets behind one [Service], with global vs
// per-workspace scoping resolved in one place. The vault stays a pure
// crypto primitive and the store a pure key/value sink; the precedence and
// scope rules live here. See [Service] for the composition details.
package prefs
