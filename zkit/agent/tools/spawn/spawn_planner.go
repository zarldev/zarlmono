package spawn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/templates"
)

const (
	schemaType   = "type"
	schemaString = "string"
	schemaAgent  = "agent"
)

// LLMSpawnPlanner is the production implementation of SpawnPlanner.
// It asks an llm.Provider for an agent + mode pick with the response
// pinned by llm.ResponseFormatJSONSchema — on llama.cpp this becomes
// GBNF-constrained sampling that physically cannot emit an agent
// name outside the supplied closed set. The model is free to
// fabricate a rationale (open-ended prose) but cannot mis-pick a
// name, which is exactly the confabulation surface this work
// targets — see feedback_enum_schemas_beat_instructions.
//
// Construct with NewLLMSpawnPlanner. Wire onto the spawn tool with
// the SAME agent list the AgentResolver knows about:
//
//	planner := spawn.NewLLMSpawnPlanner(provider)
//	tool := spawn.New(parent,
//	    spawn.WithAgentResolver(resolver),
//	    spawn.WithSpawnPlanner(planner, []string{"researcher", "coder", "reviewer"}),
//	)
//
// The planner reads in.AvailableAgents on each call, so a consumer
// that adds an agent at runtime can simply re-wire WithSpawnPlanner
// with the updated slice — no per-planner cache to invalidate.
type LLMSpawnPlanner struct {
	provider  llm.Provider
	maxTokens int
}

// NewLLMSpawnPlanner builds a planner backed by the supplied
// provider. As with LLMVerdictJudge, the provider's configured model
// is used as-is — for routing decisions you typically want a small
// fast model, so construct a dedicated provider with the small
// model rather than sharing the driving agent's provider.
func NewLLMSpawnPlanner(provider llm.Provider) *LLMSpawnPlanner {
	return &LLMSpawnPlanner{
		provider:  provider,
		maxTokens: defaultPlannerMaxTokens,
	}
}

// WithMaxTokens overrides the per-plan token cap. The default
// (defaultPlannerMaxTokens) is comfortable for one sentence of
// rationale plus the JSON envelope.
func (p *LLMSpawnPlanner) WithMaxTokens(n int) *LLMSpawnPlanner {
	if n > 0 {
		p.maxTokens = n
	}
	return p
}

// defaultPlannerMaxTokens caps the JSON response. Same shape as the
// verdict judge — rationale (≈1 short sentence) + agent (one enum
// value) + mode (one enum value) + JSON envelope. 250 gives headroom
// for slightly longer agent names than the verdict's four-value enum.
const defaultPlannerMaxTokens = 250

// Plan runs one constrained completion and returns the parsed plan.
// Any transport / parse / validation failure surfaces as an error;
// spawn.Tool's contract is to silently fall back to today's
// soft-fallback path rather than fail the original spawn call.
func (p *LLMSpawnPlanner) Plan(ctx context.Context, in SpawnPlanInput) (SpawnPlan, error) {
	if len(in.AvailableAgents) == 0 {
		return SpawnPlan{}, errors.New("planner: no available agents to choose from")
	}

	agentNames := agentCandidateNames(in.AvailableAgents)
	req := llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: "system", Content: plannerSystemPrompt},
			{Role: "user", Content: renderPlannerUserMessage(in)},
		},
		Stream:         true,
		MaxTokens:      p.maxTokens,
		Temperature:    0,
		ResponseFormat: plannerResponseFormat(agentNames),
		// Thinking off, same reasoning as LLMVerdictJudge: the schema's
		// rationale field is the reasoning slot, and a thinking-default
		// model otherwise burns MaxTokens inside <think> and returns an
		// empty plan. Providers without the kwarg ignore it.
		ChatTemplateKwargs: llm.ChatTemplateKwargs{EnableThinking: false},
	}

	chunks, err := p.provider.Complete(ctx, req)
	if err != nil {
		return SpawnPlan{}, fmt.Errorf("planner provider: %w", err)
	}

	var body strings.Builder
	for chunk, cerr := range chunks {
		if cerr != nil {
			return SpawnPlan{}, fmt.Errorf("planner stream: %w", cerr)
		}
		body.WriteString(chunk.Content)
	}

	// Third-party backends that inline `<think>…</think>` in content
	// (rather than routing reasoning through reasoning_content) can
	// prefix the constrained JSON with a reasoning block — strip it
	// so json.Unmarshal sees just the schema-matched payload.
	visible, _ := templates.SplitThinking(body.String())
	visible = strings.TrimSpace(visible)
	if visible == "" {
		return SpawnPlan{}, errors.New("planner empty response")
	}

	var payload struct {
		Rationale string `json:"rationale"`
		Agent     string `json:"agent"`
		Mode      string `json:"mode"`
	}
	if err := json.Unmarshal([]byte(visible), &payload); err != nil {
		return SpawnPlan{}, fmt.Errorf("planner json: %w (body: %q)", err, visible)
	}

	mode := SpawnMode(payload.Mode)
	if !mode.Valid() {
		return SpawnPlan{}, fmt.Errorf("planner invalid mode: %q", payload.Mode)
	}
	// Empty string is a valid agent value (means "use parent"); any
	// non-empty value must be in the supplied set. The grammar
	// constrains this end-to-end, but defensive-validate for
	// providers without grammar support.
	if payload.Agent != "" && !slices.Contains(agentNames, payload.Agent) {
		return SpawnPlan{}, fmt.Errorf("planner invalid agent: %q", payload.Agent)
	}

	return SpawnPlan{
		Rationale: payload.Rationale,
		Agent:     payload.Agent,
		Mode:      mode,
	}, nil
}

// plannerResponseFormat builds the JSON Schema the planner constrains
// the model to. Property order matters: rationale appears BEFORE the
// agent + mode enums so the model gets a chain-of-thought slot
// before committing to the constrained values. llama.cpp's grammar
// emits properties in serialized document order, which only honours
// this intent because PropertyOrder pins it — map-marshalled schemas
// serialize alphabetically (see zkit/agent/guardrails/decompose_judge.go
// for the same pattern).
//
// The agent enum is built from the supplied agent names PLUS the empty string
// planner can decide that none of the registered agents fit and
// fall back cleanly.
func plannerResponseFormat(agents []string) llm.ResponseFormat {
	agentEnum := make([]string, 0, len(agents)+1)
	agentEnum = append(agentEnum, "")
	agentEnum = append(agentEnum, agents...)

	schema := llm.SchemaFromMap(map[string]any{
		schemaType: "object",
		"properties": map[string]any{
			"rationale": map[string]any{
				schemaType:    schemaString,
				"description": "One short sentence stating why the chosen agent and mode fit this task.",
			},
			schemaAgent: map[string]any{
				schemaType:    schemaString,
				"enum":        agentEnum,
				"description": "The agent to delegate to. Empty string means the parent runner.",
			},
			"mode": map[string]any{
				schemaType: schemaString,
				"enum": []string{
					string(SpawnModeExplore),
					string(SpawnModeImplement),
					string(SpawnModeVerify),
				},
			},
		},
		"required":             []string{"rationale", schemaAgent, "mode"},
		"additionalProperties": false,
	})
	schema.PropertyOrder = []string{"rationale", schemaAgent, "mode"}
	return llm.ResponseFormat{
		Type:   llm.ResponseFormatJSONSchema,
		Name:   "spawn_plan",
		Strict: true,
		Schema: schema,
	}
}

const plannerSystemPrompt = `You are the routing planner for a coding agent's sub-agent delegation. The agent wants to delegate a task and either omitted the target agent or named one that doesn't exist. Your job: pick the best target from the available agents (or empty for the parent runner) and choose a work mode.

Agents are listed in the user message. The three work modes:
  - explore: read-only investigation (file reads, greps, build queries). No mutations.
  - implement: make code changes (writes, edits, code-mutating tools).
  - verify: review or sanity-check (run tests, lint, re-read changes). Output is a verdict, not a change set.

Respond with one short sentence of rationale, then the agent name and mode. Pick exactly one of each.`

// renderPlannerUserMessage formats the task + the closed set of
// agents for the model. Plain labeled lines; the model only reads
// this, doesn't have to parse it.
func renderPlannerUserMessage(in SpawnPlanInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Task: %s\n", in.Prompt)
	b.WriteString("Available agents:\n")
	for _, agent := range in.AvailableAgents {
		parts := []string{agent.Name}
		if agent.Mode.Valid() {
			parts = append(parts, "mode="+string(agent.Mode))
		}
		if agent.Description != "" {
			parts = append(parts, "description="+agent.Description)
		}
		fmt.Fprintf(&b, "  - %s\n", strings.Join(parts, " — "))
	}
	return b.String()
}
