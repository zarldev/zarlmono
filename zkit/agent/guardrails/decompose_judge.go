package guardrails

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/templates"
	"github.com/zarldev/zarlmono/zkit/options"
)

// LLMVerdictJudge is the production implementation of VerdictJudge.
// It asks an llm.Provider for a recovery action with the response
// shape pinned by llm.ResponseFormatJSONSchema — on llama.cpp this
// becomes GBNF-constrained sampling that physically cannot emit a
// token sequence outside the allowed schema. The model is free to
// fabricate a rationale (rationale is open-ended prose), but it can
// only mis-pick from the four allowed actions, never invent a new
// one. That's the load-bearing property — see
// feedback_enum_schemas_beat_instructions.
//
// Construct with NewLLMVerdictJudge. Then wire onto the guardrail:
//
//	judge := guardrails.NewLLMVerdictJudge(provider)
//	g := guardrails.NewDecomposeGuardrail(0, guardrails.WithDecomposeJudge(judge))
type LLMVerdictJudge struct {
	provider  llm.Provider
	maxTokens int
}

type LLMVerdictJudgeOption = options.Option[LLMVerdictJudge]

// WithVerdictMaxTokens overrides the per-verdict token cap. Non-positive
// values leave the default unchanged.
func WithVerdictMaxTokens(n int) LLMVerdictJudgeOption {
	return func(j *LLMVerdictJudge) {
		if n > 0 {
			j.maxTokens = n
		}
	}
}

// NewLLMVerdictJudge builds a judge backed by the supplied provider. The
// provider's configured model is used as-is; pass a dedicated provider when
// verdicts should use a smaller model.
func NewLLMVerdictJudge(provider llm.Provider, opts ...LLMVerdictJudgeOption) *LLMVerdictJudge {
	j := &LLMVerdictJudge{
		provider:  provider,
		maxTokens: defaultVerdictMaxTokens,
	}
	for _, opt := range opts {
		opt(j)
	}
	return j
}

var _ VerdictJudge = (*LLMVerdictJudge)(nil)

// defaultVerdictMaxTokens caps the JSON response. The schema is
// rationale (≈1 short sentence) + action (one enum value) + JSON
// envelope — 200 is comfortable headroom; the constraint sampler
// stops as soon as the schema completes anyway.
const defaultVerdictMaxTokens = 200

// Judge runs one constrained completion and returns the parsed
// verdict. Any transport / parse / validation failure surfaces as
// an error; DecomposeGuardrail's contract is to fall back to its
// deterministic advisory rather than fail the original tool call.
func (j *LLMVerdictJudge) Judge(ctx context.Context, in VerdictInput) (Verdict, error) {
	req := llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: "system", Content: verdictSystemPrompt},
			{Role: "user", Content: renderVerdictUserMessage(in)},
		},
		Stream:         true,
		MaxTokens:      j.maxTokens,
		Temperature:    0,
		ResponseFormat: verdictResponseFormat,
		// Thinking off: the schema's rationale field IS the reasoning slot.
		// On a thinking-default model (Qwen) the <think> block otherwise
		// burns the whole MaxTokens budget before the grammar-constrained
		// JSON starts — observed live as finish_reason=length with empty
		// content, which silently demotes every verdict to the
		// deterministic fallback. Providers without the kwarg ignore it.
		ChatTemplateKwargs: llm.ChatTemplateKwargs{EnableThinking: false},
	}

	chunks := j.provider.Complete(ctx, req)

	var body strings.Builder
	for chunk, cerr := range chunks {
		if cerr != nil {
			return Verdict{}, fmt.Errorf("verdict stream: %w", cerr)
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
		return Verdict{}, errors.New("verdict empty response")
	}

	var payload struct {
		Rationale string `json:"rationale"`
		Action    string `json:"action"`
	}
	if err := json.Unmarshal([]byte(visible), &payload); err != nil {
		return Verdict{}, fmt.Errorf("verdict json: %w (body: %q)", err, visible)
	}

	action := VerdictAction(payload.Action)
	if !action.Valid() {
		return Verdict{}, fmt.Errorf("verdict invalid action: %q", payload.Action)
	}
	return Verdict{Rationale: payload.Rationale, Action: action}, nil
}

// verdictResponseFormat is the JSON Schema the judge constrains the
// model to. Property order matters: rationale appears BEFORE action
// so the model gets a chain-of-thought slot (free-text) before it
// has to commit to the enum. llama.cpp's grammar emits properties in
// serialized document order, so the order only survives because
// PropertyOrder pins it — map-marshalled schemas serialize
// alphabetically, which would put action first.
var verdictResponseFormat = llm.ResponseFormat{
	Type:   llm.ResponseFormatJSONSchema,
	Name:   "decompose_verdict",
	Strict: true,
	Schema: verdictSchema(),
}

const (
	verdictFieldRationale = "rationale"
	verdictFieldAction    = "action"
)

func verdictSchema() llm.Schema {
	s := llm.SchemaFromMap(map[string]any{
		schemaKeyType: schemaObj,
		schemaProps: map[string]any{
			verdictFieldRationale: map[string]any{
				schemaKeyType: schemaTypeStr,
				"description": "One short sentence stating why the chosen action is the right next step.",
			},
			verdictFieldAction: map[string]any{
				schemaKeyType: schemaTypeStr,
				schemaKeyEnum: []string{
					string(ActionRetryUnchanged),
					string(ActionSmallerScope),
					string(ActionSwitchTool),
					string(ActionSpawnSubagent),
				},
			},
		},
		schemaKeyRequired:      []string{verdictFieldRationale, verdictFieldAction},
		"additionalProperties": false,
	})
	s.PropertyOrder = []string{verdictFieldRationale, verdictFieldAction}
	return s
}

// renderVerdictUserMessage formats the failure facts the model needs
// to choose an action. Plain labeled lines beat JSON here — labels
// are concise and parsing isn't required (the model only reads it).
func renderVerdictUserMessage(in VerdictInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Tool: %s\n", in.Tool)
	if len(in.Args) > 0 {
		args, err := json.Marshal(in.Args)
		if err == nil {
			fmt.Fprintf(&b, "Arguments: %s\n", args)
		}
	}
	fmt.Fprintf(&b, "Failed attempts: %d\n", in.Attempts)
	fmt.Fprintf(&b, "Error: %s\n", in.Error)
	return b.String()
}
