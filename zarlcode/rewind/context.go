package rewind

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

const (
	continuationReasoning = "reasoning"
	continuationText      = "text"
	continuationThinking  = "thinking"
	providerAnthropic     = "anthropic"
	providerOpenAI        = "openai"
	providerCodex         = "openai-codex"
)

// ValidateContext rejects malformed exact-context representations without repair
// or truncation. Empty context is valid. Tool results must close each assistant
// call group in order before the next message; call IDs may recur in later groups.
// Native continuation items must match a currently supported saved provider route.
// This structural check does not replace successfully building the saved target.
// Text bytes are opaque; persistence must use a byte-preserving context codec.
func ValidateContext(messages []llm.Message, provider string) error {
	if provider != providerOpenAI && provider != providerAnthropic && provider != providerCodex {
		return ErrInvalid
	}
	return ValidateLegacyContext(messages, provider)
}

// ValidateLegacyContext checks saved message arrays without requiring exact-replay
// provider qualification. Historical text/tool contexts and empty drafts may lack
// provider metadata. Message shape, tool ordering, provenance and provider-bound
// multipart/native continuation checks still apply, without repair or truncation.
// This does not qualify a provider for an exact checkpoint or versioned resume.
func ValidateLegacyContext(messages []llm.Message, provider string) error {
	var pending []string
	for i, message := range messages {
		if !messageShapeValid(message) || !providerMessageValid(message, provider) {
			return fmt.Errorf("%w: message %d has unsupported replay fields or output order", ErrInvalid, i+1)
		}
		if message.Role == llm.RoleTool {
			if len(pending) == 0 || pending[0] != message.ToolCallID {
				return ErrInvalid
			}
			pending = pending[1:]
		} else if len(pending) != 0 {
			return ErrInvalid
		}
		seen := make(map[string]bool, len(message.ToolCalls))
		for _, call := range message.ToolCalls {
			if call.ID == "" || seen[call.ID] || call.Type != "function" || call.Function.Name == "" ||
				!validJSONText([]byte(call.Function.Arguments)) || !indexValid(call.OutputIndex) {
				return ErrInvalid
			}
			seen[call.ID] = true
			pending = append(pending, call.ID)
		}
		for _, part := range message.Parts {
			if !partValid(part, provider) {
				return ErrInvalid
			}
		}
		for _, item := range message.ContinuationItems {
			if !continuationValid(item, provider) {
				return fmt.Errorf("%w: message %d has unsupported native continuation", ErrInvalid, i+1)
			}
		}
	}
	if len(pending) != 0 {
		return ErrInvalid
	}
	return nil
}

func messageShapeValid(message llm.Message) bool {
	origin := message.Observation
	if origin != (llm.ObservationProvenance{}) && (origin.Version != 1 || origin.ID == "" || message.Role != llm.RoleUser) {
		return false
	}
	if message.Role == llm.RoleSystem || !indexValid(message.ContentOutputIndex) || (message.Role == llm.RoleAssistant && len(message.Parts) != 0) {
		return false
	}
	switch message.Role {
	case llm.RoleAssistant:
		return message.ToolCallID == "" && (message.Content != "" || len(message.ToolCalls) != 0 || len(message.ContinuationItems) != 0)
	case llm.RoleTool:
		return message.ToolCallID != "" && len(message.ToolCalls) == 0 && len(message.ContinuationItems) == 0 &&
			message.ReasoningContent == "" && message.ContentOutputIndex == nil
	case llm.RoleSystem, llm.RoleUser:
		return message.ToolCallID == "" && len(message.ToolCalls) == 0 && len(message.ContinuationItems) == 0 &&
			message.ReasoningContent == "" && message.ContentOutputIndex == nil
	default:
		return false
	}
}

func indexValid(index *int) bool { return index == nil || *index >= 0 }

// M1 admits only the shared text/image surface of the built-in replay routes.
// Audio/video and custom routes require a separately verified adapter contract.
func partValid(part llm.ContentPart, provider string) bool {
	if provider != providerOpenAI && provider != providerAnthropic && provider != providerCodex {
		return false
	}
	switch part.Type {
	case llm.ContentTypeText:
		return part.Text != "" && part.Image == nil && part.Audio == nil && part.Video == nil
	case llm.ContentTypeImage:
		if part.Text != "" || part.Audio != nil || part.Video != nil || part.Image == nil {
			return false
		}
		image := part.Image
		if (image.URL == "") == (image.DataURI == "") || (provider == providerAnthropic && image.Detail != "") {
			return false
		}
		if part.Image.DataURI == "" {
			return part.Image.URL != "" && image.MIMEType == ""
		}
		prefix, encoded, ok := strings.Cut(part.Image.DataURI, ";base64,")
		if !ok || !strings.HasPrefix(prefix, "data:image/") || encoded == "" {
			return false
		}
		// Screenshot constructors retain the MIME type as well as the data URI.
		// Admit the redundant metadata only when both representations agree.
		if image.MIMEType != "" && image.MIMEType != strings.TrimPrefix(prefix, "data:") {
			return false
		}
		_, err := base64.StdEncoding.DecodeString(encoded)
		return err == nil
	default:
		return false
	}
}

func continuationValid(item llm.ContinuationItem, provider string) bool {
	if item.Provider != provider || !indexValid(item.OutputIndex) {
		return false
	}
	var native struct {
		Type             string `json:"type"`
		ID               string `json:"id"`
		Role             string `json:"role"`
		EncryptedContent string `json:"encrypted_content"`
		Signature        string `json:"signature"`
		Data             string `json:"data"`
		Content          []struct {
			Type string `json:"type"`
		} `json:"content"`
	}
	// Preserve admitted bytes, but bind their structure to the selected
	// adapter. Anthropic reconstructs a closed block; Codex replays raw items.
	if !uniqueJSONText(item.Data) || json.Unmarshal(item.Data, &native) != nil {
		return false
	}
	switch provider {
	case providerAnthropic:
		if item.Format != "content_block.v1" || item.Kind != native.Type || !anthropicNativeShape(item.Data, native.Type) {
			return false
		}
		switch native.Type {
		case continuationThinking:
			return native.Signature != ""
		case "redacted_thinking":
			return native.Data != ""
		case continuationText:
			return item.OutputIndex != nil
		default:
			return false
		}
	case providerOpenAI, providerCodex:
		if provider == providerOpenAI {
			if item.Format != "responses.output_item" || item.Kind != native.Type || item.ID != native.ID {
				return false
			}
		} else if item.Format != "responses.reasoning.v1" || (item.Kind != native.Type && item.Kind != "") {
			return false
		}
		switch native.Type {
		case continuationReasoning:
			if provider == providerCodex && native.ID == "" {
				return false
			}
			return native.EncryptedContent != ""
		case "message":
			if native.Role != llm.RoleAssistant || item.OutputIndex == nil || item.Kind != native.Type || item.ID != native.ID {
				return false
			}
			for _, part := range native.Content {
				if part.Type == "output_text" {
					return true
				}
			}
		}
		return false
	default:
		return false
	}
}
