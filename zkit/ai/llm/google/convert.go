package google

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"google.golang.org/genai"
)

func decodeBase64(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}

// convertMessages splits llm.Message slice into Gemini's expected
// shape: a single optional system instruction plus a list of
// per-turn Contents (alternating user / model roles, with tool
// calls and tool results expressed as function_call / function_response
// parts on the surrounding turn).
//
// All system messages collapse into one SystemInstruction (joined
// with blank lines) — Gemini only honours one system block.
func convertMessages(messages []llm.Message) (*genai.Content, []*genai.Content) {
	var sys *genai.Content
	var contents []*genai.Content
	var sysParts []string
	for _, m := range messages {
		switch m.Role {
		case llm.RoleSystem:
			if m.Content != "" {
				sysParts = append(sysParts, m.Content)
			}
		case llm.RoleUser:
			parts := []*genai.Part{}
			if m.Content != "" {
				parts = append(parts, &genai.Part{Text: m.Content})
			}
			for _, p := range m.Parts {
				if gp := convertPart(p); gp != nil {
					parts = append(parts, gp)
				}
			}
			if len(parts) == 0 {
				continue
			}
			contents = append(contents, &genai.Content{
				Role:  llm.RoleUser,
				Parts: parts,
			})
		case llm.RoleAssistant:
			parts := []*genai.Part{}
			if m.Content != "" {
				parts = append(parts, &genai.Part{Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				args := map[string]any{}
				if tc.Function.Arguments != "" {
					_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
				}
				parts = append(parts, &genai.Part{
					FunctionCall: &genai.FunctionCall{
						ID:   tc.ID,
						Name: tc.Function.Name,
						Args: args,
					},
				})
			}
			if len(parts) == 0 {
				continue
			}
			contents = append(contents, &genai.Content{
				Role:  "model",
				Parts: parts,
			})
		case llm.RoleTool:
			// Tool-result messages live on the user side in Gemini's
			// turn alternation. The response goes inside FunctionResponse.
			//
			// MCP/zarlcode tool results are plain text; wrap as
			// {"result": <text>} so the schema matches what models expect.
			resp := map[string]any{"result": m.Content}
			contents = append(contents, &genai.Content{
				Role: llm.RoleUser,
				Parts: []*genai.Part{{
					FunctionResponse: &genai.FunctionResponse{
						ID:       m.ToolCallID,
						Name:     toolNameFromID(messages, m.ToolCallID),
						Response: resp,
					},
				}},
			})
		}
	}

	if len(sysParts) > 0 {
		sys = &genai.Content{
			Role:  llm.RoleUser,
			Parts: []*genai.Part{{Text: strings.Join(sysParts, "\n\n")}},
		}
	}
	return sys, contents
}

// toolNameFromID walks back through messages to find the assistant
// tool-call whose ID matches — Gemini's FunctionResponse requires the
// Name (not just ID) to bind the response to the call.
func toolNameFromID(messages []llm.Message, id string) string {
	for _, m := range messages {
		if m.Role != llm.RoleAssistant {
			continue
		}
		for _, tc := range m.ToolCalls {
			if tc.ID == id {
				return tc.Function.Name
			}
		}
	}
	return ""
}

// convertPart maps an llm.ContentPart to a genai.Part. Unknown /
// empty parts return nil so the caller can skip them.
func convertPart(p llm.ContentPart) *genai.Part {
	switch p.Type {
	case llm.ContentTypeText:
		if p.Text == "" {
			return nil
		}
		return &genai.Part{Text: p.Text}
	case llm.ContentTypeImage:
		if p.Image == nil {
			return nil
		}
		// Inline base64 image. Gemini wants InlineData, not a URL.
		mime := p.Image.MIMEType
		data := dataURIBytes(p.Image.DataURI)
		if data == nil {
			return nil
		}
		return &genai.Part{
			InlineData: &genai.Blob{Data: data, MIMEType: mime},
		}
	}
	return nil
}

// dataURIBytes decodes the base64 payload of a data: URI. Returns nil
// when the URI doesn't match the expected shape.
func dataURIBytes(uri string) []byte {
	const sep = ";base64,"
	_, after, ok := strings.Cut(uri, sep)
	if !ok {
		return nil
	}
	encoded := after
	out, err := decodeBase64(encoded)
	if err != nil {
		return nil
	}
	return out
}

// convertTools wraps every llm.Tool as a single genai.Tool with one
// FunctionDeclaration each. Gemini accepts multiple FunctionDeclarations
// inside one Tool — flattening them keeps the wire format minimal.
func convertTools(tools []llm.Tool) []*genai.Tool {
	if len(tools) == 0 {
		return nil
	}
	decls := make([]*genai.FunctionDeclaration, 0, len(tools))
	for _, t := range tools {
		schema := jsonSchemaToGenAI(t.Function.Parameters)
		decls = append(decls, &genai.FunctionDeclaration{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  schema,
		})
	}
	return []*genai.Tool{{FunctionDeclarations: decls}}
}

// jsonSchemaToGenAI converts a JSON-Schema (the OpenAI-style
// map[string]any tools speak) into a genai.Schema. Walks
// {type, properties, items, required, description, enum} recursively.
// Open-world keys (Schema.Extra) are dropped — Gemini's schema is a subset of
// JSON Schema and rejects fields it doesn't recognise.
func jsonSchemaToGenAI(schema llm.Schema) *genai.Schema {
	if schema.IsZero() {
		return nil
	}
	out := &genai.Schema{}

	if schema.Type != "" {
		out.Type = jsonTypeToGenAI(schema.Type)
	}
	if schema.Description != "" {
		out.Description = schema.Description
	}
	for _, v := range schema.Enum {
		out.Enum = append(out.Enum, fmt.Sprint(v))
	}
	out.Required = append(out.Required, schema.Required...)
	if schema.Items != nil {
		out.Items = jsonSchemaToGenAI(*schema.Items)
	}
	if len(schema.Properties) > 0 {
		out.Properties = make(map[string]*genai.Schema, len(schema.Properties))
		for k, v := range schema.Properties {
			out.Properties[k] = jsonSchemaToGenAI(v)
		}
	}
	return out
}

// jsonTypeToGenAI maps a JSON-Schema "type" string to genai.Type. Falls
// back to TypeObject for unknown values so the call doesn't crash.
func jsonTypeToGenAI(t string) genai.Type {
	switch strings.ToLower(t) {
	case "string":
		return genai.TypeString
	case "number":
		return genai.TypeNumber
	case "integer":
		return genai.TypeInteger
	case "boolean":
		return genai.TypeBoolean
	case "array":
		return genai.TypeArray
	case "object":
		return genai.TypeObject
	}
	return genai.TypeObject
}
