package main

// guardrails for healthcheck are configured inline in pursue.go — the
// SchemaGuardrail (built-in) and FanoutGuardrail (built-in) both come
// from pkg/agent/guardrails. No custom guardrails are needed for this
// example, which is intentional: it shows how to compose the built-in
// rails without writing your own.
//
// SchemaGuardrail validates that check_endpoint args have a valid "name"
// field — so the model can't call check_endpoint with a typo'd or missing
// endpoint name and get a confusing "down" result.
//
// FanoutGuardrail caps check_endpoint at 5 calls per task. Once the model
// has checked every endpoint individually, the fanout guardrail nudges
// toward delegation (spawn_agent). Below 5, calls pass through normally.
