package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ModelOptions holds provider-neutral, JSON-like model configuration options.
// Supported values are nil, bool, string, numeric scalar types, [json.Number],
// ModelOptions, map[string]any, and recursively nested []any values. Callers
// crossing an ownership boundary use [ModelOptions.Clone].
type ModelOptions map[string]any

// Clone returns a recursively owned copy of o. It panics when a value is not
// part of the supported JSON-like ModelOptions contract.
func (o ModelOptions) Clone() ModelOptions {
	if o == nil {
		return nil
	}
	clone := make(ModelOptions, len(o))
	for key, value := range o {
		clone[key] = cloneModelOptionValue(value)
	}
	return clone
}

func cloneModelOptionValue(value any) any {
	switch value := value.(type) {
	case nil, bool, string,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64, json.Number:
		return value
	case ModelOptions:
		return value.Clone()
	case map[string]any:
		clone := make(map[string]any, len(value))
		for key, nested := range value {
			clone[key] = cloneModelOptionValue(nested)
		}
		return clone
	case []any:
		clone := make([]any, len(value))
		for i, nested := range value {
			clone[i] = cloneModelOptionValue(nested)
		}
		return clone
	default:
		panic(fmt.Sprintf("llm: unsupported ModelOptions value type %T", value))
	}
}

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
	// Complete constructs a fully lazy, one-shot completion stream. Calling
	// Complete performs no I/O, token refresh, validation, transport or process
	// setup, goroutine start, or other external side effect; that work begins
	// only when the returned stream is invoked or ranged.
	//
	// req, including all of its reference-backed fields, is borrowed until the
	// stream invocation returns. Callers must not mutate it during that lifetime,
	// and providers must not mutate it. Operational failures are yielded by the
	// stream; Complete has no eager error result.
	// The stream must stop promptly when ctx is canceled, close every owned
	// transport/process/decoder resource, and wait for owned goroutines before
	// returning. Consumers invoke streams synchronously and cannot safely repair
	// a provider that ignores cancellation.
	Complete(ctx context.Context, req CompletionRequest) CompletionStream
	Name() string
}

// ChatTemplateKwargs is the typed payload serialized into the non-standard
// chat_template_kwargs request extension used by llama.cpp/vLLM chat templates.
// Providers render it to raw JSON only at the transport edge.
type ChatTemplateKwargs struct {
	EnableThinking   bool `json:"enable_thinking"`
	PreserveThinking bool `json:"preserve_thinking,omitempty"`
}

// IsZero reports whether k carries no template overrides.
func (k ChatTemplateKwargs) IsZero() bool {
	return !k.EnableThinking && !k.PreserveThinking
}

// AsMap renders k as a generic map for transports that pass arbitrary JSON
// extension fields. It returns nil when no fields are set.
func (k ChatTemplateKwargs) AsMap() map[string]any {
	if k.IsZero() {
		return nil
	}
	out := map[string]any{"enable_thinking": k.EnableThinking}
	if k.PreserveThinking {
		out["preserve_thinking"] = true
	}
	return out
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
	// that don't recognise it ignore it. The runner builds this from the
	// active ChatTemplate's ThinkingKwargs each request.
	ChatTemplateKwargs ChatTemplateKwargs

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

// TextResponseFormat returns an unconstrained text response format.
func TextResponseFormat() ResponseFormat { return ResponseFormat{Type: ResponseFormatText} }

// JSONObjectResponseFormat returns provider JSON-object mode without a pinned schema.
func JSONObjectResponseFormat() ResponseFormat { return ResponseFormat{Type: ResponseFormatJSONObject} }

// JSONSchemaResponseFormat returns a schema-constrained response format.
func JSONSchemaResponseFormat(name string, schema Schema, strict bool) ResponseFormat {
	return ResponseFormat{Type: ResponseFormatJSONSchema, Name: name, Schema: schema, Strict: strict}
}

// Validate reports whether f has the fields required by its Type.
func (f ResponseFormat) Validate() error {
	switch f.Type {
	case ResponseFormatText:
		if f.Name != "" || !f.Schema.IsZero() || f.Strict {
			return fmt.Errorf("response_format %q must not set name, schema, or strict", f.Type)
		}
	case ResponseFormatJSONObject:
		if f.Name != "" || !f.Schema.IsZero() || f.Strict {
			return fmt.Errorf("response_format %q must not set name, schema, or strict", f.Type)
		}
	case ResponseFormatJSONSchema:
		if f.Name == "" {
			return fmt.Errorf("response_format %q requires name", f.Type)
		}
		if f.Schema.IsZero() {
			return fmt.Errorf("response_format %q requires schema", f.Type)
		}
	default:
		return fmt.Errorf("response_format type %q is invalid", f.Type)
	}
	return nil
}

// Message represents a single message in a conversation.
// Includes vision/audio/video-capable parts for user input.
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

	// ContentOutputIndex is the optional native position of the projected visible
	// assistant content. Providers interpret the position in their own native
	// output sequence; nil selects their legacy deterministic fallback.
	ContentOutputIndex *int `json:"content_output_index,omitempty"`

	// Parts carries multimodal content (text + image + audio + video) for
	// vision/audio/video-capable models. When non-nil, providers SHOULD send
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

	ToolCallID string `json:"tool_call_id,omitempty"`

	// ContinuationItems carries completed provider-native output items needed
	// to continue the conversation losslessly. Items remain in provider output
	// order and coexist with the neutral Content, ReasoningContent, and
	// ToolCalls projections used for display and execution. Providers that do
	// not recognize an item's Provider and Format ignore it.
	ContinuationItems []ContinuationItem `json:"continuation_items,omitempty"`
}

// ContinuationItem is an opaque, SDK-independent provider-native output item.
// Data contains semantically complete opaque JSON: all native fields and values
// survive replay, though surrounding request marshaling may canonicalize JSON
// whitespace. Provider and Format route it back to the adapter that understands
// that encoding. Kind and ID are optional routing hints and must not be
// interpreted as the opaque payload. Slice order is the provider's original
// output-item order.
type ContinuationItem struct {
	// OutputIndex is the item's optional zero-based position in the provider's
	// native output sequence. Nil preserves compatibility with history written
	// before native ordering metadata existed; non-nil zero is position zero.
	OutputIndex *int   `json:"output_index,omitempty"`
	Provider    string `json:"provider"`
	Format      string `json:"format"`
	Kind        string `json:"kind,omitempty"`
	ID          string `json:"id,omitempty"`
	Data        []byte `json:"data"`
}

// ByteLen returns the number of bytes retained by the continuation item,
// including its opaque payload and routing metadata.
func (i ContinuationItem) ByteLen() int {
	n := len(i.Provider) + len(i.Format) + len(i.Kind) + len(i.ID) + len(i.Data)
	if i.OutputIndex != nil {
		n += 8
	}
	return n
}

// Clone returns an owned deep copy of the continuation item.
func (i ContinuationItem) Clone() ContinuationItem {
	clone := i
	clone.OutputIndex = cloneInt(i.OutputIndex)
	clone.Provider = strings.Clone(i.Provider)
	clone.Format = strings.Clone(i.Format)
	clone.Kind = strings.Clone(i.Kind)
	clone.ID = strings.Clone(i.ID)
	clone.Data = append([]byte(nil), i.Data...)
	return clone
}

// Clone returns an owned deep copy of the message and all reference-backed
// nested state.
func (m Message) Clone() Message {
	clone := m
	clone.Role = strings.Clone(m.Role)
	clone.Content = strings.Clone(m.Content)
	clone.ReasoningContent = strings.Clone(m.ReasoningContent)
	clone.ToolCallID = strings.Clone(m.ToolCallID)
	clone.Parts = cloneContentParts(m.Parts)
	if m.ToolCalls != nil {
		clone.ToolCalls = make([]ToolCall, len(m.ToolCalls))
		for i, call := range m.ToolCalls {
			clone.ToolCalls[i] = call.Clone()
		}
	}
	clone.ContentOutputIndex = cloneInt(m.ContentOutputIndex)
	if m.ContinuationItems != nil {
		clone.ContinuationItems = make([]ContinuationItem, len(m.ContinuationItems))
		for idx, item := range m.ContinuationItems {
			clone.ContinuationItems[idx] = item.Clone()
		}
	}
	return clone
}

// CloneMessages returns an owned deep copy of messages.
func CloneMessages(messages []Message) []Message {
	if messages == nil {
		return nil
	}
	clone := make([]Message, len(messages))
	for i, message := range messages {
		clone[i] = message.Clone()
	}
	return clone
}

func cloneContentParts(parts []ContentPart) []ContentPart {
	if parts == nil {
		return nil
	}
	clone := make([]ContentPart, len(parts))
	for i, part := range parts {
		clone[i] = part
		clone[i].Text = strings.Clone(part.Text)
		if part.Image != nil {
			image := *part.Image
			image.URL = strings.Clone(image.URL)
			image.DataURI = strings.Clone(image.DataURI)
			image.MIMEType = strings.Clone(image.MIMEType)
			image.Detail = strings.Clone(image.Detail)
			clone[i].Image = &image
		}
		if part.Audio != nil {
			audio := *part.Audio
			audio.DataURI = strings.Clone(audio.DataURI)
			audio.Format = strings.Clone(audio.Format)
			clone[i].Audio = &audio
		}
		if part.Video != nil {
			video := *part.Video
			video.URL = strings.Clone(video.URL)
			video.DataURI = strings.Clone(video.DataURI)
			video.MIMEType = strings.Clone(video.MIMEType)
			clone[i].Video = &video
		}
	}
	return clone
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
	ContentTypeVideo ContentPartType = "video"
)

// ContentPart is one element of a multimodal Message.Parts slice.
// Exactly one of the typed fields (Text/Image/Audio/Video) is meaningful per
// instance; Type tells you which.
type ContentPart struct {
	Type ContentPartType `json:"type"`

	// Text is set when Type == ContentTypeText.
	Text string `json:"text,omitempty"`

	// Image is set when Type == ContentTypeImage.
	Image *ImageData `json:"image,omitempty"`

	// Audio is set when Type == ContentTypeAudio.
	Audio *AudioData `json:"audio,omitempty"`

	// Video is set when Type == ContentTypeVideo.
	Video *VideoData `json:"video,omitempty"`
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

// VideoData is the video payload for a video content part. Either URL
// (remote http(s) URL) or DataURI ("data:video/...;base64,...") must be set;
// providers prefer DataURI when both are present.
type VideoData struct {
	URL      string `json:"url,omitempty"`
	DataURI  string `json:"data_uri,omitempty"`
	MIMEType string `json:"mime_type,omitempty"`
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

// VideoPartFromDataURI is a convenience constructor for a video
// ContentPart backed by a base64 data URI.
func VideoPartFromDataURI(dataURI, mime string) ContentPart {
	return ContentPart{
		Type:  ContentTypeVideo,
		Video: &VideoData{DataURI: dataURI, MIMEType: mime},
	}
}

// VideoPartFromURL is a convenience constructor for a video
// ContentPart referenced by remote URL.
func VideoPartFromURL(url string) ContentPart {
	return ContentPart{Type: ContentTypeVideo, Video: &VideoData{URL: url}}
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

	// OutputIndex is the optional zero-based position of this call in the
	// provider's native assistant output sequence.
	OutputIndex *int `json:"output_index,omitempty"`
}

// Clone returns an owned deep copy of the tool call.
func (c ToolCall) Clone() ToolCall {
	clone := c
	clone.ID = strings.Clone(c.ID)
	clone.Type = strings.Clone(c.Type)
	clone.Function.Name = strings.Clone(c.Function.Name)
	clone.Function.Arguments = strings.Clone(c.Function.Arguments)
	clone.OutputIndex = cloneInt(c.OutputIndex)
	return clone
}

// OutputPosition returns an owned optional native output position, including
// for position zero.
func OutputPosition(index int) *int { return &index }

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

// ToolCallFunction represents the function details in a tool call.
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string of arguments
}

// CompletionChunk is one synchronous completion observation.
//
// Content and Thinking are disjoint output channels: for any given byte of
// model output, exactly one carries it. Content is visible output; Thinking is
// reasoning surfaced out of band. ToolCalls, FinishReason, and UsageReported
// may accompany output or form a meaningful metadata-only chunk. Metadata does
// not signal stream completion, and an all-zero chunk remains an ordinary legal
// observation.
//
// Reference-backed state is borrowed only until the downstream yield callback
// returns. Consumers that retain a chunk must call [CompletionChunk.Clone]
// before returning from yield.
type CompletionChunk struct {
	Content            string
	ContentOutputIndex *int
	Thinking           string
	ToolCalls          []ToolCall
	CompletedItems     []ContinuationItem
	FinishReason       FinishReason
	Usage              Usage
	UsageReported      bool
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
	SupportsVideo     bool `json:"supports_video"`    // Video input processing
	SupportsTools     bool `json:"supports_tools"`    // Function/tool calling
	SupportsSystem    bool `json:"supports_system"`   // System messages
	SupportsThinking  bool `json:"supports_thinking"` // DeepSeek R1 style reasoning
}
