package llm

import (
	"context"
	"errors"
	"iter"
)

// ModelOptions holds semantic model configuration options as a
// map[string]any.
type ModelOptions map[string]any

// PersonalityModifiers holds trait-driven adjustments to LLM behavior.
type PersonalityModifiers map[string]any

// Message roles, as carried on [Message].Role across every provider. The
// strings match the OpenAI-compatible wire values so histories serialize
// without translation.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// ModelConfig specifies a model with its provider.
type ModelConfig struct {
	Provider string `json:"provider"` // "openai", "anthropic", "google", etc.
	Model    string `json:"model"`    // "gpt-4", "claude-3-5-sonnet", "gemini-pro", etc.
}

// ModelPreferences represents model preferences for user configuration.
type ModelPreferences struct {
	Primary   ModelConfig   `json:"primary"`
	Fallbacks []ModelConfig `json:"fallbacks,omitempty"`
}

// Common errors.
var (
	ErrProviderUnavailable   = errors.New("llm provider unavailable")
	ErrInvalidAPIKey         = errors.New("invalid api key")
	ErrModelNotSupported     = errors.New("model not supported")
	ErrRateLimitExceeded     = errors.New("rate limit exceeded")
	ErrContextLengthExceeded = errors.New("context length exceeded")
)

// Provider is the minimum contract a backend has to satisfy. It is
// deliberately narrow: a single streaming completion entry-point plus
// a name for identification. Anything richer (model discovery, image
// generation, MCP, thinking-mode toggles) belongs on a separate
// opt-in interface that consumers type-assert for when they need it.
type Provider interface {
	// Complete streams a completion as an iter.Seq2: each chunk is yielded
	// with a nil error, and a mid-stream failure is the yield's second value
	// (not a field on the chunk). The returned error is a pre-stream setup
	// failure only. Consumers range it: `for chunk, err := range seq`.
	Complete(ctx context.Context, req CompletionRequest) (iter.Seq2[CompletionChunk, error], error)
	Name() string
}

// CompletionRequest represents a request to generate text.
type CompletionRequest struct {
	Messages    []Message
	Temperature float32
	MaxTokens   int
	Stream      bool
	Tools       []Tool // Function calling tools available to the LLM

	// ChatTemplateKwargs is the llama.cpp / vLLM extension field that
	// gets serialised as `chat_template_kwargs` on the wire. Providers
	// that don't recognise it ignore it. Typical use: pass
	// `{"enable_thinking": true, "preserve_thinking": true}` for
	// Qwen3 agentic loops — the runner builds this from the active
	// ChatTemplate's ThinkingKwargs.AsMap() each request.
	ChatTemplateKwargs map[string]any

	// ResponseFormat constrains the model's output shape. When set to
	// a JSONSchema variant, llama.cpp converts the schema to a GBNF
	// grammar and constrains sampling so the model literally cannot
	// emit a token that violates the schema — including invented enum
	// values. The OpenAI hosted API enforces structured output the
	// same way when strict=true; Anthropic maps it to its native
	// structured-outputs config and Gemini to responseJsonSchema. The
	// claude-code CLI can't grammar-constrain, so it falls back to a
	// prompt directive.
	//
	// Leave zero-valued for free-form text output.
	ResponseFormat ResponseFormat

	// Thinking requests the model's extended reasoning for this request.
	// Each provider maps it to its native mechanism — Anthropic's thinking
	// budget, Gemini's thinking config, OpenAI/codex reasoning effort,
	// llama.cpp's chat_template_kwargs. Providers that surface reasoning
	// unconditionally (Gemini, the OpenAI-compatible reasoning_content
	// path) ignore the toggle. Zero value leaves the provider default.
	Thinking ThinkingConfig

	// Provider-specific options
	Options ModelOptions
}

// ThinkingConfig is the provider-neutral request for extended reasoning.
type ThinkingConfig struct {
	// Enabled turns extended thinking on. Off (the default) leaves the
	// provider/model default in place.
	Enabled bool

	// BudgetTokens optionally caps the thinking-token budget for providers
	// that accept one (Anthropic requires >= 1024 and clamps). Zero lets
	// the provider choose a sane default.
	BudgetTokens int
}

// ResponseFormatType discriminates how a provider should constrain
// the model's output. Zero value (ResponseFormatText) means no
// constraint — the historical default.
type ResponseFormatType string

const (
	// ResponseFormatText is unconstrained free-form output. The zero
	// value, so a CompletionRequest with no ResponseFormat set picks
	// this implicitly.
	ResponseFormatText ResponseFormatType = ""

	// ResponseFormatJSONObject asks the model for valid JSON without
	// pinning the shape — OpenAI's classic "JSON mode". Useful when
	// the prompt already describes the keys but the schema would be
	// noisy to author. llama.cpp accepts the same flag.
	ResponseFormatJSONObject ResponseFormatType = "json_object"

	// ResponseFormatJSONSchema constrains output to exactly the
	// supplied JSON Schema. This is the form to use for enum-driven
	// classifications, verdicts, and routing decisions — the model
	// cannot mis-spell or invent an enum value because the sampler
	// rejects any token sequence that would.
	ResponseFormatJSONSchema ResponseFormatType = "json_schema"
)

// ResponseFormat carries the per-request structured-output directive
// in a provider-neutral shape that mirrors OpenAI's response_format
// payload (which llama.cpp and vLLM also accept). Type discriminates;
// the other fields are only consulted when Type == ResponseFormatJSONSchema.
type ResponseFormat struct {
	// Type selects the output mode. Zero value is unconstrained text.
	Type ResponseFormatType

	// Name labels the schema for OpenAI's structured-output API
	// (required there) and shows up in llama.cpp's logs. Free-form;
	// stick to identifier-ish strings ("verdict", "skill_pick").
	Name string

	// Schema is the typed JSON Schema document the model's output must
	// satisfy (the same Schema type used for tool parameters, so there is
	// one schema representation across the package). The zero value means
	// no schema. Hand-author inline as a Schema literal, or build from a
	// map with SchemaFromMap; the provider serialises it into the request.
	//
	// For enum-constrained decisions, the canonical shape is:
	//
	//	{
	//	    "type": "object",
	//	    "properties": {
	//	        "verdict": {"type": "string", "enum": ["a", "b", "c"]},
	//	    },
	//	    "required": ["verdict"],
	//	    "additionalProperties": false,
	//	}
	Schema Schema

	// Strict asks OpenAI's API to refuse responses that don't satisfy
	// the schema rather than best-effort matching. llama.cpp ignores
	// it (grammar-constrained sampling is always strict by
	// construction). Default false to match OpenAI's default.
	Strict bool
}

// Message represents a single message in a conversation.
type Message struct {
	Role    string `json:"role"` // Use messages.RoleSystem, messages.RoleUser, etc.
	Content string `json:"content"`

	// ReasoningContent stores an assistant turn's reasoning out-of-band
	// from Content. The runner populates it from CompletionChunk.Thinking
	// at end-of-turn; per-provider history serializers reshape it back
	// onto the wire (openai's ReasoningInline re-wraps it as
	// `<think>…</think>`, ReasoningField forwards it via the
	// reasoning_content extra field, ReasoningStrip drops it). Disjoint
	// from Content — see CompletionChunk's docstring for the channel
	// contract.
	ReasoningContent string `json:"reasoning_content,omitempty"`

	// Parts carries multimodal content (text + image + audio) for
	// vision/audio-capable models. When non-nil, providers SHOULD send
	// Parts in preference to Content; if a provider doesn't support
	// multimodal, it falls back to flattening the Text parts to
	// Content. Parts is typically only set on role="user" messages —
	// assistant turns return text via Content.
	Parts []ContentPart `json:"parts,omitempty"`

	// ToolCalls is set on assistant messages that requested tool
	// invocations. Carrying these in the conversation history is what
	// lets the model see "I already called this tool" rather than
	// re-emitting the same call on every iteration.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`

	// ToolCallID is set on role="tool" messages and matches the ID of
	// the assistant's tool call this message is responding to. OpenAI
	// (and llama.cpp's OAI shim) reject tool messages without it.
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// ContentPartType discriminates which kind of payload a ContentPart
// carries. Adding a new modality is a matter of adding a constant and
// the matching field.
type ContentPartType string

// The supported content-part modalities; each selects the matching typed
// field on [ContentPart].
const (
	ContentTypeText  ContentPartType = "text"
	ContentTypeImage ContentPartType = "image"
	ContentTypeAudio ContentPartType = "audio"
)

// ContentPart is one element of a multimodal Message.Parts slice.
// Exactly one of the typed fields (Text/Image/Audio) is meaningful per
// instance; Type tells you which.
type ContentPart struct {
	Type ContentPartType `json:"type"`

	// Text is set when Type == ContentTypeText.
	Text string `json:"text,omitempty"`

	// Image is set when Type == ContentTypeImage.
	Image *ImageData `json:"image,omitempty"`

	// Audio is set when Type == ContentTypeAudio.
	Audio *AudioData `json:"audio,omitempty"`
}

// ImageData is the image payload for an image content part. Either URL
// (remote http(s) URL) or DataURI ("data:image/png;base64,...") must
// be set; providers prefer DataURI when both are present.
type ImageData struct {
	URL      string `json:"url,omitempty"`
	DataURI  string `json:"data_uri,omitempty"`
	MIMEType string `json:"mime_type,omitempty"`

	// Detail is OpenAI-specific: "low" | "high" | "auto" (default).
	// Other providers ignore it.
	Detail string `json:"detail,omitempty"`
}

// AudioData is the audio payload for an audio content part. Format is
// the codec hint, e.g. "wav", "mp3". DataURI must be a base64 data URI.
type AudioData struct {
	DataURI string `json:"data_uri,omitempty"`
	Format  string `json:"format,omitempty"`
}

// TextPart is a convenience constructor for a text ContentPart.
func TextPart(text string) ContentPart {
	return ContentPart{Type: ContentTypeText, Text: text}
}

// ImagePartFromDataURI is a convenience constructor for an image
// ContentPart backed by a base64 data URI.
func ImagePartFromDataURI(dataURI, mime string) ContentPart {
	return ContentPart{
		Type:  ContentTypeImage,
		Image: &ImageData{DataURI: dataURI, MIMEType: mime},
	}
}

// ImagePartFromURL is a convenience constructor for an image
// ContentPart referenced by remote URL.
func ImagePartFromURL(url string) ContentPart {
	return ContentPart{Type: ContentTypeImage, Image: &ImageData{URL: url}}
}

// Tool represents a function that the LLM can call.
type Tool struct {
	Type     string       `json:"type"` // "function"
	Function ToolFunction `json:"function"`
}

// ToolFunction defines a callable function for the LLM.
type ToolFunction struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  Schema `json:"parameters"`
}

// ParametersMap renders the function's parameter schema as a generic JSON
// Schema map for SDKs whose parameter field is map[string]any. Returns nil
// when the function takes no arguments.
func (f ToolFunction) ParametersMap() map[string]any {
	if f.Parameters.IsZero() {
		return nil
	}
	return f.Parameters.Map()
}

// ToolCall represents a function call made by the LLM (raw transport format).
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"` // "function"
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction represents the function details in a tool call.
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string of arguments
}

// CompletionChunk represents a piece of streaming response.
//
// Content and Thinking are the two output channels, and they are
// disjoint by contract: for any given byte of model output, exactly
// one of them carries it. Content holds the visible answer; Thinking
// holds reasoning / extended-thinking / chain-of-thought, routed
// out-of-band so consumers can render it in a dedicated surface.
// Every provider (Anthropic extended thinking, DeepSeek/OpenAI
// reasoning_content, Gemini thought parts, openaicodex reasoning
// events) lands on this contract — there is no inline-tag escape
// hatch at the provider boundary.
type CompletionChunk struct {
	Content      string
	Thinking     string
	FinishReason string // "stop", "length", "error"
	Done         bool
	// Error is NOT the streaming error channel. Provider errors ride the
	// iter.Seq2's second return value, which the consumer (runner drain)
	// reads; this field is populated only by the conformance test harness
	// (folding the yield error in for legacy assertions). A provider that
	// sets it directly will have it silently ignored — don't.
	Error     error
	ToolCalls []ToolCall // Function calls made by the LLM (transport format)

	// Usage information (only in final chunk)
	Usage *Usage
}

// Usage tracks token usage.
//
// CachedTokens is the subset of PromptTokens served from the
// provider's prompt cache — Anthropic reports this as
// `cache_read_input_tokens`, OpenAI as
// `prompt_tokens_details.cached_tokens`, and llama.cpp's
// openai-compat endpoint approximates it from the KV-cache reuse.
// Adapters that can't distinguish cached vs uncached leave this
// at 0.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	CachedTokens     int
}

// Model represents an available LLM model.
type Model struct {
	ID          string
	Name        string
	Description string
	MaxTokens   int
	InputCost   float64 // per 1k tokens
	OutputCost  float64 // per 1k tokens

	// Model capabilities metadata
	Capabilities ModelCapabilities `json:"capabilities"`
}

// ModelCapabilities describes what features a specific model supports.
type ModelCapabilities struct {
	SupportsStreaming bool `json:"supports_streaming"`
	SupportsVision    bool `json:"supports_vision"`   // Image input processing
	SupportsTools     bool `json:"supports_tools"`    // Function/tool calling
	SupportsSystem    bool `json:"supports_system"`   // System messages
	SupportsThinking  bool `json:"supports_thinking"` // DeepSeek R1 style reasoning
}
