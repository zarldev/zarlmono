// Package messagebus provides typed publish/subscribe and request/reply
// abstractions for application messages.
//
// The package keeps payloads generic while standardizing subjects, headers,
// subscriptions, and encoder hooks. Implementations include an in-memory bus for
// tests/local use and NATS-backed transport for distributed deployments.
package messagebus
