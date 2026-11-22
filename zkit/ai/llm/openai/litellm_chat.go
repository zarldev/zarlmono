package openai

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/openai/openai-go/v3/option"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/options"
)

const liteLLMChatFormat = "translated_chat.v1"

// Limits bound retained translated state and decoding work, not the SDK's
// preceding HTTP/SSE allocation. Aggregate counts wire bytes (including mirrors
// and usage), so equivalent fragmented responses can reach it sooner.
const (
	liteLLMMaxBytes      = 8 << 20
	liteLLMMaxBlockBytes = 1 << 20
	liteLLMMaxBlocks     = 128
	liteLLMMaxCalls      = 256
	liteLLMMaxFragments  = 65536
	liteLLMMaxMessages   = 1024
)

// ErrLiteLLMChat identifies unsupported or inconsistent translated Chat state.
// Its diagnostics never include response payloads, signatures or history.
var ErrLiteLLMChat = errors.New("litellm translated chat state unsupported")

// LiteLLMPromptCachePolicy explicitly requests Anthropic-compatible caching on
// a configured LiteLLM Chat route. No model-alias inference or server honoring
// is implied. The zero policy sends no markers. Automatic is an alternative to
// explicit System/Tools breakpoints; Tools marks only the last client tool.
// All requested breakpoints use TTL (default, 5m or 1h), with at most four.
// AllowReplayMarkers separately authorizes assistant response/history markers;
// ReplayMaxTTL is their maximum permitted lifetime (default means 5m),
// independent of TTL for requested System/Tools/Automatic markers. Without that
// authorization, any response or stored marker is rejected, never stripped.
type LiteLLMPromptCachePolicy struct {
	System, Tools, Automatic bool
	AllowReplayMarkers       bool
	TTL                      LiteLLMCacheTTL
	ReplayMaxTTL             LiteLLMCacheTTL
}

// WithLiteLLMChat enables only the v1.109 translated Chat dialect. It does not
// enable Responses, native Messages, or deployment-affinity guarantees.
// Limits are 8 MiB of aggregate response wire bytes or replay input (including
// projections), 1 MiB per encoded/decoded signed block or function call,
// 128 blocks, 256 calls, 65,536 events and 1,024 history messages (with at
// most 256 continuation items and 256 content parts each). Limits are
// checked before extension decoding/retention, but cannot bound preceding SDK
// HTTP/SSE allocations. This is bounded semantic preservation, not raw fidelity.
// Envelopes are published only after successful EOF; displayed prefixes on
// errors are not replayable. Replay identity is constructor-local, not durable.
func WithLiteLLMChat() options.Option[Provider] {
	return func(p *Provider) { p.liteLLMChat = true }
}

// WithLiteLLMPromptCache assigns explicit Anthropic-compatible route policy.
// Only a provider composed with WithLiteLLMChat consumes this policy.
func WithLiteLLMPromptCache(policy LiteLLMPromptCachePolicy) options.Option[Provider] {
	return func(p *Provider) { p.liteLLMCache = policy }
}

type liteLLMCacheControl struct {
	Type LiteLLMCacheKind `json:"type"`
	TTL  *LiteLLMCacheTTL `json:"ttl,omitempty"`
}

type liteLLMBlock struct {
	Type         LiteLLMThinkingKind  `json:"type"`
	Thinking     string               `json:"thinking,omitempty"`
	Signature    string               `json:"signature,omitempty"`
	Data         string               `json:"data,omitempty"`
	Index        *int                 `json:"index,omitempty"`
	CacheControl *liteLLMCacheControl `json:"cache_control,omitempty"`
}

type liteLLMChatEnvelope struct {
	Version   int            `json:"version"`
	Scope     string         `json:"scope"`
	Content   string         `json:"content"`
	Thinking  string         `json:"thinking"`
	ToolCalls []llm.ToolCall `json:"tool_calls,omitempty"`
	Blocks    []liteLLMBlock `json:"thinking_blocks"`
}

// LiteLLMReplayScope owns a random constructor-local replay identity. Its zero
// value is usable; do not copy it after use. Randomness is acquired lazily during
// iteration, never by construction or Complete. The persisted identity is never
// credential-derived. The owner privately retains the first immutable credential
// solely to reject cross-credential reuse, without hashing or publishing it.
// It permits continuity only within one concrete gateway provider, not durable
// resume. Unresolved scopes from another construction fail closed.
type LiteLLMReplayScope struct {
	once       sync.Once
	nonce      [32]byte
	err        error
	credential string
}

// WithLiteLLMReplayScope shares an identity owner with per-call Chat codecs of
// one concrete gateway provider. This is controlled composition, not a config or
// persistence identity override. Distinct providers must have distinct owners.
func WithLiteLLMReplayScope(scope *LiteLLMReplayScope) options.Option[Provider] {
	return func(p *Provider) { p.liteLLMReplay = scope }
}

func (s *LiteLLMReplayScope) initialize(credential string) error {
	s.once.Do(func() {
		s.credential = credential
		if _, err := rand.Read(s.nonce[:]); err != nil {
			s.err = ErrLiteLLMChat
		}
	})
	if s.credential != credential {
		return ErrLiteLLMChat
	}
	return s.err
}

// Only nonsecret routing values and independent random bytes enter the digest.
func (p *Provider) liteLLMScope() string {
	data, _ := json.Marshal([]string{p.baseURL, p.model, EndpointKinds.ENDPOINTCHATCOMPLETIONS.String()})
	hash := sha256.New()
	_, _ = hash.Write(p.liteLLMReplay.nonce[:])
	_, _ = hash.Write(data)
	return hex.EncodeToString(hash.Sum(nil))
}

func strictChatJSON(raw []byte, value any, fields ...string) error {
	return strictChatJSONContext(context.Background(), raw, value, fields...)
}

func strictChatJSONContext(ctx context.Context, raw []byte, value any, fields ...string) error {
	if len(raw) > liteLLMMaxBytes {
		return ErrLiteLLMChat
	}

	if err := exactChatUnicode(ctx, raw); err != nil {
		return err
	}
	unique := json.NewDecoder(bytes.NewReader(raw))
	unique.UseNumber()
	if err := uniqueChatJSONValue(ctx, unique, 0); err != nil {
		return err
	}
	if len(fields) != 0 && exactChatFields(raw, fields...) != nil {
		return ErrLiteLLMChat
	}
	if slices.Contains(fields, "tool_calls") && exactChatCalls(raw, "tool_calls", !slices.Contains(fields, "version")) != nil {
		return ErrLiteLLMChat
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil {
		return ErrLiteLLMChat
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return ErrLiteLLMChat
	}
	return context.Cause(ctx)
}

// Signed representations cannot use duplicate keys to select conflicting state.
func uniqueChatJSONValue(ctx context.Context, decoder *json.Decoder, depth int) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	if depth > 64 {
		return ErrLiteLLMChat
	}
	token, err := decoder.Token()
	if err != nil {
		return ErrLiteLLMChat
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	keys := map[string]bool{}
	count := 0
	for decoder.More() {
		count++
		if count > liteLLMMaxCalls {
			return ErrLiteLLMChat
		}
		if delimiter == '{' {
			key, err := decoder.Token()
			if err != nil {
				return ErrLiteLLMChat
			}
			name, ok := key.(string)
			if !ok || keys[name] {
				return ErrLiteLLMChat
			}
			keys[name] = true
		}
		if err := uniqueChatJSONValue(ctx, decoder, depth+1); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return ErrLiteLLMChat
	}
	return nil
}

func (b *liteLLMBlock) UnmarshalJSON(raw []byte) error {
	if len(raw) > liteLLMMaxBlockBytes {
		return ErrLiteLLMChat
	}
	type wire liteLLMBlock
	var block wire
	if strictChatJSON(raw, &block, "type", "thinking", "signature", "data", "index", "cache_control") != nil {
		return ErrLiteLLMChat
	}
	// Presence is independent of a generated enum's valid zero value.
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields[jsonKeyType]) == 0 {
		return ErrLiteLLMChat
	}
	*b = liteLLMBlock(block)
	return nil
}

func (b liteLLMBlock) MarshalJSON() ([]byte, error) {
	type wire liteLLMBlock
	if b.Type == LiteLLMThinkingKinds.LITELLMTHINKING {
		// Thinking is required even when empty (signature-only thinking).
		return json.Marshal(struct {
			wire
			Thinking string `json:"thinking"`
		}{wire: wire(b), Thinking: b.Thinking})
	}
	return json.Marshal(wire(b))
}

func (c *liteLLMCacheControl) UnmarshalJSON(raw []byte) error {
	type wire liteLLMCacheControl
	var control wire
	if strictChatJSON(raw, &control, "type", "ttl") != nil {
		return ErrLiteLLMChat
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields[jsonKeyType]) == 0 {
		return ErrLiteLLMChat
	}
	*c = liteLLMCacheControl(control)
	return nil
}

func liteLLMSignedFinish(reason string) bool {
	finish := semanticFinishReason(reason)
	return finish == llm.FinishReasons.STOP || finish == llm.FinishReasons.TOOLCALLS
}

// Cache breakpoints follow request order: tools, system, then conversation.
// Anthropic requires longer-lived markers before shorter-lived ones.
type liteLLMCacheBudget struct {
	count int
	short bool
}

func (b *liteLLMCacheBudget) add(ttl LiteLLMCacheTTL) error {
	if b.short && ttl == LiteLLMCacheTTLs.LITELLMCACHE1H {
		return ErrLiteLLMChat
	}
	b.short = ttl != LiteLLMCacheTTLs.LITELLMCACHE1H
	b.count++
	if b.count > 4 {
		return ErrLiteLLMChat
	}
	return nil
}

func validLiteLLMBlock(block liteLLMBlock, complete bool) bool {
	if block.CacheControl != nil {
		if block.CacheControl.Type != LiteLLMCacheKinds.LITELLMCACHEEPHEMERAL {
			return false
		}
		if ttl := block.CacheControl.TTL; ttl != nil && *ttl != LiteLLMCacheTTLs.LITELLMCACHE5M && *ttl != LiteLLMCacheTTLs.LITELLMCACHE1H {
			return false
		}
	}
	switch block.Type {
	case LiteLLMThinkingKinds.LITELLMTHINKING:
		return block.Data == "" && (!complete || block.Signature != "")
	case LiteLLMThinkingKinds.LITELLMREDACTEDTHINKING:
		return block.Thinking == "" && block.Signature == "" && block.Data != ""
	default:
		return false
	}
}

func liteLLMEnvelope(ctx context.Context, message llm.Message, scope string) (liteLLMChatEnvelope, bool, error) {
	var envelope liteLLMChatEnvelope
	found := false
	for _, item := range message.ContinuationItems {
		if item.Provider != "litellm" {
			continue
		}
		if found || item.Format != liteLLMChatFormat || item.Kind != "assistant" || item.ID != "" || item.OutputIndex != nil || strictChatJSONContext(ctx, item.Data, &envelope, "version", "scope", "content", "thinking", "tool_calls", "thinking_blocks") != nil {
			if cause := context.Cause(ctx); cause != nil {
				return envelope, false, cause
			}
			return envelope, false, ErrLiteLLMChat
		}
		found = true
	}
	if !found {
		return envelope, false, nil
	}
	if message.Role != roleAssistant || envelope.Version != 1 || envelope.Scope != scope || envelope.Content != message.Content || envelope.Thinking != message.ReasoningContent || len(message.Parts) != 0 || message.ContentOutputIndex != nil || len(envelope.Blocks) == 0 || !slices.EqualFunc(envelope.ToolCalls, message.ToolCalls, equalLiteLLMCall) {
		return envelope, false, ErrLiteLLMChat
	}
	if len(envelope.Blocks) > liteLLMMaxBlocks || len(envelope.ToolCalls) > liteLLMMaxCalls {
		return envelope, false, ErrLiteLLMChat
	}
	var thinking strings.Builder
	for _, block := range envelope.Blocks {
		if cause := context.Cause(ctx); cause != nil {
			return envelope, false, cause
		}
		if liteLLMBlockBytes(block) > liteLLMMaxBlockBytes {
			return envelope, false, ErrLiteLLMChat
		}
		if block.Index != nil || !validLiteLLMBlock(block, true) {
			return envelope, false, ErrLiteLLMChat
		}
		thinking.WriteString(block.Thinking)
	}
	if thinking.String() != envelope.Thinking {
		return envelope, false, ErrLiteLLMChat
	}
	return envelope, true, nil
}

func equalLiteLLMCall(a, b llm.ToolCall) bool {
	return a.ID == b.ID && a.Type == b.Type && a.Function == b.Function && a.OutputIndex == nil && b.OutputIndex == nil
}

func validLiteLLMCall(call llm.ToolCall) bool {
	return call.ID != "" && !strings.HasPrefix(call.ID, "srvtoolu_") && call.Type == typeFunction && call.Function.Name != "" && call.OutputIndex == nil && json.Valid([]byte(call.Function.Arguments)) && strings.HasPrefix(strings.TrimSpace(call.Function.Arguments), "{")
}

// History is borrowed external state: validate bindings and tool associations
// before request serialization or I/O. Exact argument bytes avoid float coercion.
func (p *Provider) validateLiteLLMChat(ctx context.Context, req llm.CompletionRequest) error {
	if err := p.liteLLMReplay.initialize(p.apiKey); err != nil {
		return err
	}
	if len(req.Messages) > liteLLMMaxMessages {
		return ErrLiteLLMChat
	}
	total, calls, blocks := 0, 0, 0
	policy := p.liteLLMCache
	if policy.Automatic && (policy.System || policy.Tools) {
		return ErrLiteLLMChat
	}
	if !validLiteLLMCacheTTL(policy.TTL) || !validLiteLLMCacheTTL(policy.ReplayMaxTTL) {
		return ErrLiteLLMChat
	}
	var budget liteLLMCacheBudget
	if policy.Automatic || (policy.Tools && len(req.Tools) != 0) {
		if err := budget.add(policy.TTL); err != nil {
			return err
		}
	}
	// LiteLLM extracts ALL system messages before any conversation content,
	// regardless of their positions in the Chat request. Association validation
	// below intentionally stays in original message order.
	for _, message := range req.Messages {
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		if policy.System && message.Role == llm.RoleSystem {
			if err := budget.add(policy.TTL); err != nil {
				return err
			}
		}
	}
	pending := map[string]bool{}
	seen := map[string]bool{}
	for _, message := range req.Messages {
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		calls += len(message.ToolCalls)
		if calls > liteLLMMaxCalls || len(message.ContinuationItems) > liteLLMMaxCalls {
			return ErrLiteLLMChat
		}
		total += len(message.Content) + len(message.ReasoningContent) + len(message.ToolCallID)
		if len(message.Parts) > liteLLMMaxCalls {
			return ErrLiteLLMChat
		}
		for _, part := range message.Parts {
			if cause := context.Cause(ctx); cause != nil {
				return cause
			}
			total += part.ByteLen()
			if total > liteLLMMaxBytes {
				return ErrLiteLLMChat
			}
		}
		for _, item := range message.ContinuationItems {
			total += item.ByteLen()
		}
		for _, call := range message.ToolCalls {
			if len(call.ID)+len(call.Function.Name)+len(call.Function.Arguments) > liteLLMMaxBlockBytes {
				return ErrLiteLLMChat
			}
			encoded, _ := json.Marshal(call)
			if len(encoded) > liteLLMMaxBlockBytes {
				return ErrLiteLLMChat
			}
			total += len(call.ID) + len(call.Function.Name) + len(call.Function.Arguments)
		}
		if total > liteLLMMaxBytes {
			return ErrLiteLLMChat
		}
		envelope, _, err := liteLLMEnvelope(ctx, message, p.liteLLMScope())
		if err != nil {
			return err
		}
		blocks += len(envelope.Blocks)
		if blocks > liteLLMMaxBlocks {
			return ErrLiteLLMChat
		}
		for _, block := range envelope.Blocks {
			if !p.liteLLMCache.allows(block) {
				return ErrLiteLLMChat
			}
			if block.CacheControl == nil {
				continue
			}
			if policy.Automatic {
				return ErrLiteLLMChat
			}
			ttl := LiteLLMCacheTTLs.LITELLMCACHEDEFAULT
			if block.CacheControl.TTL != nil {
				ttl = *block.CacheControl.TTL
			}
			if err := budget.add(ttl); err != nil {
				return err
			}
		}
		if message.Role != roleTool && message.ToolCallID != "" {
			return ErrLiteLLMChat
		}
		if message.Role != roleAssistant && message.ReasoningContent != "" {
			return ErrLiteLLMChat
		}
		switch message.Role {
		case llm.RoleSystem, roleUser, roleAssistant:
		case roleTool:
			if !pending[message.ToolCallID] {
				return ErrLiteLLMChat
			}
			delete(pending, message.ToolCallID)
		default:
			return ErrLiteLLMChat
		}
		if message.Role != roleTool && len(pending) != 0 {
			return ErrLiteLLMChat
		}
		if len(message.ToolCalls) != 0 && message.Role != roleAssistant {
			return ErrLiteLLMChat
		}
		for _, call := range message.ToolCalls {
			if !validLiteLLMCall(call) || seen[call.ID] {
				return ErrLiteLLMChat
			}
			seen[call.ID], pending[call.ID] = true, true
		}
	}
	if len(pending) != 0 {
		return ErrLiteLLMChat
	}
	return context.Cause(ctx)
}

func (p *Provider) liteLLMOptions(ctx context.Context, req llm.CompletionRequest) []option.RequestOption {
	// Reuse the native serializer, then add only proven extension placements.
	encoded, _ := json.Marshal(p.convertMessagesToOpenAI(req.Messages))
	var messages []map[string]any
	_ = json.Unmarshal(encoded, &messages)
	for i, message := range llm.ExpandToolResultParts(req.Messages) {
		if envelope, found, _ := liteLLMEnvelope(ctx, message, p.liteLLMScope()); found {
			messages[i]["thinking_blocks"] = envelope.Blocks
		}
		if message.Role == llm.RoleSystem && p.liteLLMCache.System {
			messages[i]["content"] = []any{map[string]any{jsonKeyType: "text", "text": message.Content, "cache_control": p.liteLLMCacheControl()}}
		}
	}
	opts := []option.RequestOption{option.WithJSONSet("messages", messages)}
	if p.liteLLMCache.Tools && len(req.Tools) != 0 {
		opts = append(opts, option.WithJSONSet(fmt.Sprintf("tools.%d.cache_control", len(req.Tools)-1), p.liteLLMCacheControl()))
	}
	if p.liteLLMCache.Automatic {
		opts = append(opts, option.WithJSONSet("cache_control", p.liteLLMCacheControl()))
	}
	return opts
}

func (p *Provider) liteLLMCacheControl() liteLLMCacheControl {
	control := liteLLMCacheControl{Type: LiteLLMCacheKinds.LITELLMCACHEEPHEMERAL}
	if p.liteLLMCache.TTL != LiteLLMCacheTTLs.LITELLMCACHEDEFAULT {
		ttl := p.liteLLMCache.TTL
		control.TTL = &ttl
	}
	return control
}

// Invocation-owned accumulator; indices here are thinking ordinals, never
// native output positions. No item is published until the assistant is complete.
type liteLLMChatState struct {
	blocks                   []liteLLMBlock
	calls                    []llm.ToolCall
	content, thinking        []byte
	blockThinking            [][]byte
	callArguments            [][]byte
	bytes, fragments         int
	policy                   LiteLLMPromptCachePolicy
	visible, tools, finished bool
	usage                    llm.CompletionChunk
	reportedCost             llm.ReportedCost
}

type liteLLMWireCall struct {
	Index    *int                 `json:"index,omitempty"`
	ID       string               `json:"id"`
	Type     string               `json:"type"`
	Function llm.ToolCallFunction `json:"function"`
}

type liteLLMChatDelta struct {
	FunctionCall           json.RawMessage            `json:"function_call"`
	Audio                  json.RawMessage            `json:"audio"`
	Refusal                json.RawMessage            `json:"refusal"`
	Images                 json.RawMessage            `json:"images"`
	Annotations            json.RawMessage            `json:"annotations"`
	ReasoningItems         json.RawMessage            `json:"reasoning_items"`
	Role                   string                     `json:"role"`
	Content                string                     `json:"content"`
	ReasoningContent       *string                    `json:"reasoning_content"`
	ThinkingBlocks         json.RawMessage            `json:"thinking_blocks"`
	ProviderSpecificFields map[string]json.RawMessage `json:"provider_specific_fields"`
	ToolCalls              []liteLLMWireCall          `json:"tool_calls"`
}

func (s *liteLLMChatState) observe(ctx context.Context, raw, projected string, stream bool) (string, error) {
	var delta liteLLMChatDelta
	if cause := context.Cause(ctx); cause != nil {
		return "", cause
	}
	if strictChatJSONContext(ctx, []byte(raw), &delta, "function_call", "audio", "refusal", "images", "annotations", "reasoning_items", "role", "content", "reasoning_content", "thinking_blocks", "provider_specific_fields", "tool_calls") != nil || (delta.Role != "" && delta.Role != roleAssistant) || (!stream && delta.Role != roleAssistant) || s.finished {
		if cause := context.Cause(ctx); cause != nil {
			return "", cause
		}
		return "", ErrLiteLLMChat
	}
	// Tagged Message/Delta include nullable legacy and media fields. Empty
	// fields carry no replay state; populated unsupported forms must not vanish.
	for _, field := range []json.RawMessage{delta.FunctionCall, delta.Audio, delta.Refusal} {
		if !emptyLiteLLMField(field, false) {
			return "", ErrLiteLLMChat
		}
	}
	for _, field := range []json.RawMessage{delta.Images, delta.Annotations, delta.ReasoningItems} {
		if !emptyLiteLLMField(field, true) {
			return "", ErrLiteLLMChat
		}
	}
	for field, value := range delta.ProviderSpecificFields {
		if field == "citations" && emptyLiteLLMField(value, true) {
			continue
		}
		if field != "thinking_blocks" {
			return "", ErrLiteLLMChat
		}
	}
	blocks, err := mirroredLiteLLMBlocks(delta.ThinkingBlocks, delta.ProviderSpecificFields["thinking_blocks"])
	if err != nil {
		return "", err
	}
	var text strings.Builder
	for _, block := range blocks {
		if cause := context.Cause(ctx); cause != nil {
			return "", cause
		}
		text.WriteString(block.Thinking)
		if err := s.appendBlock(block, stream); err != nil {
			return "", err
		}
	}
	if text.Len() != 0 {
		if projected != "" && projected != text.String() {
			return "", ErrLiteLLMChat
		}
		projected = text.String()
	}
	if len(s.thinking)+len(projected) > liteLLMMaxBytes {
		return "", ErrLiteLLMChat
	}
	s.thinking = append(s.thinking, projected...)
	if delta.Content != "" {
		if s.tools {
			return "", ErrLiteLLMChat
		}
		s.visible = true
		if len(s.content)+len(delta.Content) > liteLLMMaxBytes {
			return "", ErrLiteLLMChat
		}
		s.content = append(s.content, delta.Content...)
	}
	for i, wire := range delta.ToolCalls {
		if cause := context.Cause(ctx); cause != nil {
			return "", cause
		}
		if err := s.appendCall(wire, i, stream); err != nil {
			return "", err
		}
	}
	return projected, nil
}

func (s *liteLLMChatState) appendBlock(block liteLLMBlock, stream bool) error {
	if liteLLMBlockBytes(block) > liteLLMMaxBlockBytes || !s.policy.allows(block) {
		return ErrLiteLLMChat
	}
	if s.visible || s.tools || !validLiteLLMBlock(block, !stream) {
		return ErrLiteLLMChat
	}
	if !stream {
		if block.Index != nil {
			return ErrLiteLLMChat
		}
		if len(s.blocks) >= liteLLMMaxBlocks {
			return ErrLiteLLMChat
		}
		s.blockThinking = append(s.blockThinking, []byte(block.Thinking))
		block.Thinking = ""
		s.blocks = append(s.blocks, block)
		return nil
	}
	if block.Index == nil || *block.Index < 0 || *block.Index > len(s.blocks) {
		return ErrLiteLLMChat
	}
	index := *block.Index
	if index == len(s.blocks) {
		if index > 0 && !validLiteLLMBlock(s.blocks[index-1], true) {
			return ErrLiteLLMChat
		}
		block.Index = nil
		if len(s.blocks) >= liteLLMMaxBlocks {
			return ErrLiteLLMChat
		}
		s.blockThinking = append(s.blockThinking, []byte(block.Thinking))
		block.Thinking = ""
		s.blocks = append(s.blocks, block)
		return nil
	}
	current := s.blocks[index]
	// A signature completes a block. Subsequent fragments, repeated
	// signatures and revisiting earlier ordinals are ambiguous and rejected.
	if index != len(s.blocks)-1 || current.Signature != "" || current.Type != block.Type || current.Type == LiteLLMThinkingKinds.LITELLMREDACTEDTHINKING || block.CacheControl != nil {
		return ErrLiteLLMChat
	}
	if len(s.blockThinking[index])+len(block.Thinking)+len(block.Signature) > liteLLMMaxBlockBytes {
		return ErrLiteLLMChat
	}
	s.blockThinking[index] = append(s.blockThinking[index], block.Thinking...)
	current.Signature = block.Signature
	s.blocks[index] = current
	return nil
}

func (s *liteLLMChatState) appendCall(wire liteLLMWireCall, ordinal int, stream bool) error {
	if strings.HasPrefix(wire.ID, "srvtoolu_") || len(wire.ID)+len(wire.Function.Name)+len(wire.Function.Arguments) > liteLLMMaxBlockBytes {
		return ErrLiteLLMChat
	}
	s.tools = true
	index := ordinal
	if stream {
		if wire.Index == nil {
			return ErrLiteLLMChat
		}
		index = *wire.Index
	}
	if index < 0 || index > len(s.calls) || (wire.Type != "" && wire.Type != typeFunction) {
		return ErrLiteLLMChat
	}
	if index == len(s.calls) {
		if wire.ID == "" || wire.Function.Name == "" || wire.Type != typeFunction {
			return ErrLiteLLMChat
		}
		for _, call := range s.calls {
			if call.ID == wire.ID {
				return ErrLiteLLMChat
			}
		}
		if len(s.calls) >= liteLLMMaxCalls {
			return ErrLiteLLMChat
		}
		s.callArguments = append(s.callArguments, []byte(wire.Function.Arguments))
		wire.Function.Arguments = ""
		s.calls = append(s.calls, llm.ToolCall{ID: wire.ID, Type: typeFunction, Function: wire.Function})
	} else {
		call := s.calls[index]
		if (wire.ID != "" && wire.ID != call.ID) || (wire.Function.Name != "" && wire.Function.Name != call.Function.Name) {
			return ErrLiteLLMChat
		}
		if len(call.ID)+len(call.Function.Name)+len(s.callArguments[index])+len(wire.Function.Arguments) > liteLLMMaxBlockBytes {
			return ErrLiteLLMChat
		}
		s.callArguments[index] = append(s.callArguments[index], wire.Function.Arguments...)
		s.calls[index] = call
	}
	return nil
}

func emptyLiteLLMField(raw json.RawMessage, list bool) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) == 0 || bytes.Equal(raw, []byte("null")) || (list && bytes.Equal(raw, []byte("[]")))
}

func mirroredLiteLLMBlocks(top, mirror json.RawMessage) ([]liteLLMBlock, error) {
	var a, b []liteLLMBlock
	if len(top) > liteLLMMaxBytes || len(mirror) > liteLLMMaxBytes {
		return nil, ErrLiteLLMChat
	}
	if len(top) != 0 && strictChatJSON(top, &a) != nil {
		return nil, ErrLiteLLMChat
	}
	if len(mirror) != 0 && strictChatJSON(mirror, &b) != nil {
		return nil, ErrLiteLLMChat
	}
	if len(a) > liteLLMMaxBlocks || len(b) > liteLLMMaxBlocks {
		return nil, ErrLiteLLMChat
	}
	if len(top) != 0 && len(mirror) != 0 {
		x, _ := json.Marshal(a)
		y, _ := json.Marshal(b)
		if !bytes.Equal(x, y) {
			return nil, ErrLiteLLMChat
		}
	}
	if len(top) != 0 {
		return a, nil
	}
	return b, nil
}

func (s *liteLLMChatState) complete(ctx context.Context, scope string) ([]llm.ContinuationItem, error) {
	if cause := context.Cause(ctx); cause != nil {
		return nil, cause
	}
	if s.finished {
		return nil, ErrLiteLLMChat
	}
	s.finished = true
	for i, call := range s.calls {
		if cause := context.Cause(ctx); cause != nil {
			return nil, cause
		}
		call.Function.Arguments = string(s.callArguments[i])
		s.calls[i] = call
		encoded, err := json.Marshal(call)
		if !validLiteLLMCall(call) || err != nil || len(encoded) > liteLLMMaxBlockBytes {
			return nil, ErrLiteLLMChat
		}
	}
	if len(s.blocks) == 0 {
		return nil, nil
	}
	var thinking strings.Builder
	var budget liteLLMCacheBudget
	for i, block := range s.blocks {
		if cause := context.Cause(ctx); cause != nil {
			return nil, cause
		}
		if block.CacheControl != nil {
			ttl := LiteLLMCacheTTLs.LITELLMCACHEDEFAULT
			if block.CacheControl.TTL != nil {
				ttl = *block.CacheControl.TTL
			}
			if err := budget.add(ttl); err != nil {
				return nil, err
			}
		}
		block.Thinking = string(s.blockThinking[i])
		s.blocks[i] = block
		encoded, err := json.Marshal(block)
		if !validLiteLLMBlock(block, true) || err != nil || len(encoded) > liteLLMMaxBlockBytes {
			return nil, ErrLiteLLMChat
		}
		thinking.WriteString(block.Thinking)
	}
	if thinking.String() != string(s.thinking) {
		return nil, ErrLiteLLMChat
	}
	data, err := json.Marshal(liteLLMChatEnvelope{Version: 1, Scope: scope, Content: string(s.content), Thinking: string(s.thinking), ToolCalls: s.calls, Blocks: s.blocks})
	replayBytes := len(data) + len(s.content) + len(s.thinking) + len("litellm") + len(liteLLMChatFormat) + len("assistant")
	for _, call := range s.calls {
		replayBytes += len(call.ID) + len(call.Function.Name) + len(call.Function.Arguments)
	}
	if err != nil || replayBytes > liteLLMMaxBytes {
		return nil, ErrLiteLLMChat
	}
	if cause := context.Cause(ctx); cause != nil {
		return nil, cause
	}
	return []llm.ContinuationItem{{Provider: "litellm", Format: liteLLMChatFormat, Kind: "assistant", Data: data}}, nil
}

// Usage-bearing chunks are snapshots, not increments. Normalize aliases once
// per snapshot, retaining an independently present body cost across later
// cost-less usage and selecting the last reported value on explicit conflict.
func (s *liteLLMChatState) observeUsage(raw string) {
	var snapshot llm.CompletionChunk
	liteLLMUsage(raw, &snapshot)
	if snapshot.Execution != nil && snapshot.Execution.ReportedCost.Reported {
		cost := snapshot.Execution.ReportedCost
		if s.reportedCost.Reported && (s.reportedCost.Amount != cost.Amount || s.reportedCost.Source == "litellm-usage-cost-conflicting-snapshots") {
			cost.Source = "litellm-usage-cost-conflicting-snapshots"
		}
		s.reportedCost = cost
	}
	s.usage = snapshot
}

func (s *liteLLMChatState) applyUsage(metadata *llm.CompletionChunk) {
	metadata.PricingUsage, metadata.Execution = s.usage.PricingUsage, s.usage.Execution
	metadata.Usage.CachedTokens = s.usage.Usage.CachedTokens
	metadata.Usage.CacheCreationTokens = s.usage.Usage.CacheCreationTokens
	metadata.Usage.CacheCreation5mTokens = s.usage.Usage.CacheCreation5mTokens
	metadata.Usage.CacheCreation1hTokens = s.usage.Usage.CacheCreation1hTokens
	if s.reportedCost.Reported {
		if metadata.Execution == nil {
			metadata.Execution = &llm.ExecutionObservation{}
		}
		metadata.Execution.ReportedCost = s.reportedCost
	}
}

// Aliases are alternate observations of one quantity, never additions. Null is
// absent; conflicting or invalid aliases leave the quantity unknown.
func liteLLMQuantity(values ...json.RawMessage) llm.TokenQuantity {
	var result llm.TokenQuantity
	for _, raw := range values {
		if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			continue
		}
		var count int64
		if json.Unmarshal(raw, &count) != nil || count < 0 {
			return llm.TokenQuantity{}
		}
		if result.Known && result.Tokens != count {
			return llm.TokenQuantity{}
		}
		result = llm.TokenQuantity{Tokens: count, Known: true}
	}
	return result
}

func liteLLMUsage(raw string, chunk *llm.CompletionChunk) {
	var envelope struct {
		Usage map[string]json.RawMessage `json:"usage"`
	}
	if json.Unmarshal([]byte(raw), &envelope) != nil || envelope.Usage == nil {
		return
	}
	u := envelope.Usage
	var details map[string]json.RawMessage
	_ = json.Unmarshal(u["prompt_tokens_details"], &details)
	read := liteLLMQuantity(details["cached_tokens"], u["cache_read_input_tokens"])
	write := liteLLMQuantity(details["cache_write_tokens"], details["cache_creation_tokens"], details["cache_creation_input_tokens"], u["cache_creation_input_tokens"])
	var ttl map[string]json.RawMessage
	_ = json.Unmarshal(details["cache_creation_token_details"], &ttl)
	var topTTL map[string]json.RawMessage
	_ = json.Unmarshal(u["cache_creation"], &topTTL)
	five := liteLLMQuantity(ttl["ephemeral_5m_input_tokens"], topTTL["ephemeral_5m_input_tokens"])
	hour := liteLLMQuantity(ttl["ephemeral_1h_input_tokens"], topTTL["ephemeral_1h_input_tokens"])
	if write.Known && ((five.Known && five.Tokens > write.Tokens) || (hour.Known && hour.Tokens > write.Tokens) || (five.Known && hour.Known && five.Tokens > write.Tokens-hour.Tokens)) {
		five, hour = llm.TokenQuantity{}, llm.TokenQuantity{}
	}
	chunk.Usage.CachedTokens = int(read.Tokens)
	chunk.Usage.CacheCreationTokens = int(write.Tokens)
	chunk.Usage.CacheCreation5mTokens = int(five.Tokens)
	chunk.Usage.CacheCreation1hTokens = int(hour.Tokens)
	chunk.PricingUsage = &llm.PricingUsage{Input: llm.PricingInputUsage{Total: liteLLMQuantity(u["prompt_tokens"]), CacheRead: read, CacheWrite: write, WritesByLifetime: []llm.CacheWriteQuantity{{Lifetime: 5 * time.Minute, Quantity: five}, {Lifetime: time.Hour, Quantity: hour}}}, Output: llm.PricingOutputUsage{Total: liteLLMQuantity(u["completion_tokens"])}, AdditionalChargesPossible: true}
	// Other modality partitions, currency, and full charge coverage are unknown.
	if read.Known || write.Known {
		chunk.Execution = &llm.ExecutionObservation{PromptCacheReadTokens: read, PromptCacheWriteTokens: write}
	}
	costRaw := u["cost"]
	amount, err := strconv.ParseFloat(string(costRaw), 64)
	if err == nil && amount >= 0 && !math.IsInf(amount, 0) && !math.IsNaN(amount) && !(amount == 0 && liteLLMNonzeroMantissa(costRaw)) {
		if chunk.Execution == nil {
			chunk.Execution = &llm.ExecutionObservation{}
		}
		chunk.Execution.ReportedCost = llm.ReportedCost{Amount: amount, Reported: true, Source: "litellm-usage-cost"}
	}
}

func liteLLMNonzeroMantissa(raw []byte) bool {
	for _, c := range raw {
		if c == 'e' || c == 'E' {
			break
		}
		if c >= '1' && c <= '9' {
			return true
		}
	}
	return false
}

// exactChatFields closes encoding/json's case-insensitive struct-field matching.
// This applies only to owned signed shapes, never to function argument objects.
func exactChatFields(raw []byte, fields ...string) error {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return ErrLiteLLMChat
	}
	for key := range object {
		if !slices.Contains(fields, key) {
			return ErrLiteLLMChat
		}
	}
	if blocks := object["thinking_blocks"]; len(blocks) != 0 {
		var list []json.RawMessage
		if json.Unmarshal(blocks, &list) != nil || len(list) > liteLLMMaxBlocks {
			return ErrLiteLLMChat
		}
	}
	return nil
}

func exactChatCalls(raw []byte, field string, stream bool) error {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return ErrLiteLLMChat
	}
	calls := object[field]
	if len(calls) == 0 {
		return nil
	}
	var list []json.RawMessage
	if json.Unmarshal(calls, &list) != nil || len(list) > liteLLMMaxCalls {
		return ErrLiteLLMChat
	}
	fields := []string{"id", "type", "function", "output_index"}
	if stream {
		fields = []string{"id", "type", "function", "index"}
	}
	for _, call := range list {
		if len(call) > liteLLMMaxBlockBytes || exactChatFields(call, fields...) != nil {
			return ErrLiteLLMChat
		}
		var wire map[string]json.RawMessage
		if json.Unmarshal(call, &wire) != nil || exactChatFields(wire["function"], "name", "arguments") != nil {
			return ErrLiteLLMChat
		}
	}
	return nil
}

func liteLLMBlockBytes(block liteLLMBlock) int {
	return len(block.Thinking) + len(block.Signature) + len(block.Data)
}

func (p LiteLLMPromptCachePolicy) allows(block liteLLMBlock) bool {
	if block.CacheControl == nil {
		return true
	}
	if !p.AllowReplayMarkers || p.Automatic {
		return false
	}
	ttl := block.CacheControl.TTL
	return ttl == nil || *ttl != LiteLLMCacheTTLs.LITELLMCACHE1H || p.ReplayMaxTTL == LiteLLMCacheTTLs.LITELLMCACHE1H
}

func (s *liteLLMChatState) acceptRaw(ctx context.Context, raw string) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	if len(raw) > liteLLMMaxBytes-s.bytes || s.fragments >= liteLLMMaxFragments {
		return ErrLiteLLMChat
	}
	s.bytes += len(raw)
	s.fragments++
	if err := exactChatUnicode(ctx, []byte(raw)); err != nil {
		return err
	}
	return uniqueLiteLLMMetadata(ctx, []byte(raw), 0)
}

// The SDK's map decoding has already lost duplicate keys. Inspect the retained
// wire JSON before consuming *any* SDK usage/cost snapshot or publishing state.
// Duplicates/layout ambiguity are terminal, never an ordinary reported zero.
// Only owned root/usage objects are case-folded; arbitrary tool arguments are not.
func uniqueLiteLLMMetadata(ctx context.Context, raw []byte, depth int) error {
	if depth > 8 {
		return ErrLiteLLMChat
	}
	root := depth == 0
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return ErrLiteLLMChat
	}
	seen := map[string]bool{}
	for decoder.More() {
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		token, err := decoder.Token()
		if err != nil {
			return ErrLiteLLMChat
		}
		key, ok := token.(string)
		folded := foldLiteLLMMetadataKey(key)
		name := strings.ToLower(folded)
		if !ok || seen[folded] || len(seen) >= liteLLMMaxCalls {
			return ErrLiteLLMChat
		}
		seen[folded] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return ErrLiteLLMChat
		}
		child := name == "usage"
		if !root {
			child = name == "prompt_tokens_details" || name == "completion_tokens_details" || name == "cache_creation_token_details" || name == "cache_creation"
		}
		if (root && name == "choices") || child || (!root && slices.Contains([]string{"cost", "prompt_tokens", "completion_tokens", "total_tokens", "cached_tokens", "cache_write_tokens", "cache_creation_tokens", "cache_creation_input_tokens", "cache_read_input_tokens", "ephemeral_5m_input_tokens", "ephemeral_1h_input_tokens"}, name)) {
			if key != name {
				return ErrLiteLLMChat
			}
		}
		if root && name == "choices" {
			var choices []json.RawMessage
			if json.Unmarshal(value, &choices) != nil || len(choices) > 1 {
				return ErrLiteLLMChat
			}
			for _, choice := range choices {
				unique := json.NewDecoder(bytes.NewReader(choice))
				unique.UseNumber()
				if err := uniqueChatJSONValue(ctx, unique, 0); err != nil {
					return err
				}
				if exactChatFields(choice, "index", "delta", "message", "finish_reason", "logprobs", "content_filter_results") != nil {
					return ErrLiteLLMChat
				}
			}
		}
		if child && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			if err := uniqueLiteLLMMetadata(ctx, value, depth+1); err != nil {
				return err
			}
		}
	}
	if _, err := decoder.Token(); err != nil {
		return ErrLiteLLMChat
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return ErrLiteLLMChat
	}
	return context.Cause(ctx)
}

// Match encoding/json's Unicode simple-fold equivalence, including long s and
// Kelvin sign; strings.ToLower alone does not identify all semantic duplicates.
func foldLiteLLMMetadataKey(key string) string {
	return strings.Map(func(r rune) rune {
		for {
			next := unicode.SimpleFold(r)
			if next <= r {
				return next
			}
			r = next
		}
	}, key)
}

func validLiteLLMCacheTTL(ttl LiteLLMCacheTTL) bool {
	return ttl == LiteLLMCacheTTLs.LITELLMCACHEDEFAULT || ttl == LiteLLMCacheTTLs.LITELLMCACHE5M || ttl == LiteLLMCacheTTLs.LITELLMCACHE1H
}

// encoding/json accepts invalid UTF-8 and unpaired UTF-16 escapes by replacing
// them with U+FFFD. Signed opaque values must never be silently repaired. Inspect
// original bytes before any unmarshal; an explicitly encoded U+FFFD is valid.
func exactChatUnicode(ctx context.Context, raw []byte) error {
	if !utf8.Valid(raw) {
		return ErrLiteLLMChat
	}
	quoted := false
	for i := 0; i < len(raw); i++ {
		if i%4096 == 0 {
			if cause := context.Cause(ctx); cause != nil {
				return cause
			}
		}
		switch raw[i] {
		case '"':
			quoted = !quoted
		case '\\':
			if !quoted {
				continue
			}
			i++
			if i >= len(raw) {
				return ErrLiteLLMChat
			}
			if raw[i] != 'u' {
				continue
			}
			if i+4 >= len(raw) {
				return ErrLiteLLMChat
			}
			code, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
			if err != nil {
				return ErrLiteLLMChat
			}
			i += 4
			if code >= 0xDC00 && code <= 0xDFFF {
				return ErrLiteLLMChat
			}
			if code < 0xD800 || code > 0xDBFF {
				continue
			}
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return ErrLiteLLMChat
			}
			low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if err != nil || low < 0xDC00 || low > 0xDFFF {
				return ErrLiteLLMChat
			}
			i += 6
		}
	}
	return context.Cause(ctx)
}
