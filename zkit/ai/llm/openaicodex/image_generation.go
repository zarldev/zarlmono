package openaicodex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

const (
	imageGenerationModel             = "gpt-image-2"
	maxImageGenerationPromptBytes    = 64 * 1024
	maxImageGenerationReferenceBytes = 12 * 1024 * 1024
	maxImageGenerationResponseBytes  = 32 * 1024 * 1024
)

type imageGenerationTool struct {
	Type   string `json:"type"`
	Model  string `json:"model"`
	Action string `json:"action"`
}

type imageGenerationRequest struct {
	Model      string                `json:"model"`
	Input      []inputItem           `json:"input"`
	Tools      []imageGenerationTool `json:"tools"`
	ToolChoice string                `json:"tool_choice"`
	Stream     bool                  `json:"stream"`
	Store      bool                  `json:"store"`
}

type imageGenerationEvent struct {
	Type  string `json:"type"`
	Error struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error"`
	Response struct {
		Error struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
		Output []imageGenerationOutput `json:"output"`
	} `json:"response"`
	Item imageGenerationOutput `json:"item"`
}

type imageGenerationOutput struct {
	Type          string `json:"type"`
	Result        string `json:"result"`
	RevisedPrompt string `json:"revised_prompt"`
}

// GenerateImage uses the Codex Responses API's built-in image_generation tool.
// It uses the configured ChatGPT/Codex OAuth account and consumes Codex usage;
// it does not require an OpenAI API key.
func (p *Provider) GenerateImage(ctx context.Context, req llm.ImageGenerationRequest) (llm.GeneratedImage, error) {
	if cause := context.Cause(ctx); cause != nil {
		return llm.GeneratedImage{}, cause
	}
	if req.Prompt == "" || len(req.Prompt) > maxImageGenerationPromptBytes {
		return llm.GeneratedImage{}, errors.New("openaicodex: image prompt must be non-empty and at most 64 KiB")
	}
	if len(req.ReferenceImages) > 4 {
		return llm.GeneratedImage{}, errors.New("openaicodex: image generation accepts at most 4 reference images")
	}
	totalReferenceBytes := 0
	for i, part := range req.ReferenceImages {
		if part.Type != llm.ContentTypeImage || part.Image == nil || part.Image.DataURI == "" || part.Image.URL != "" {
			return llm.GeneratedImage{}, fmt.Errorf("openaicodex: reference image %d must be an embedded image", i+1)
		}
		totalReferenceBytes += len(part.Image.DataURI)
		if totalReferenceBytes > maxImageGenerationReferenceBytes {
			return llm.GeneratedImage{}, errors.New("openaicodex: reference images exceed 12 MiB")
		}
	}

	tok, err := p.tokens.Token(ctx)
	if err != nil {
		return llm.GeneratedImage{}, fmt.Errorf("openaicodex: fetch image-generation token: %w", err)
	}
	if tok.AccountID == "" {
		return llm.GeneratedImage{}, ErrNoAccountID
	}

	inputMessage := llm.Message{Role: llm.RoleUser, Content: req.Prompt}
	if len(req.ReferenceImages) > 0 {
		inputMessage.Parts = make([]llm.ContentPart, 0, 1+len(req.ReferenceImages))
		inputMessage.Parts = append(inputMessage.Parts, llm.TextPart(req.Prompt))
		inputMessage.Parts = append(inputMessage.Parts, llm.CloneContentParts(req.ReferenceImages)...)
	}
	baseModel, _ := resolveModel(p.model)
	if baseModel == "" {
		baseModel = p.model
	}
	payload, err := json.Marshal(imageGenerationRequest{
		Model:      baseModel,
		Input:      messagesToInput([]llm.Message{inputMessage}),
		Tools:      []imageGenerationTool{{Type: "image_generation", Model: imageGenerationModel, Action: "generate"}},
		ToolChoice: "required",
		Stream:     true,
		Store:      false,
	})
	if err != nil {
		return llm.GeneratedImage{}, fmt.Errorf("openaicodex: marshal image-generation request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+responsesPath, bytes.NewReader(payload))
	if err != nil {
		return llm.GeneratedImage{}, fmt.Errorf("openaicodex: build image-generation request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+tok.Access)
	httpReq.Header.Set("Chatgpt-Account-Id", tok.AccountID)
	httpReq.Header.Set("Openai-Beta", "responses=experimental")
	httpReq.Header.Set("Originator", originatorCodex)
	resp, err := p.client.Do(ctx, httpReq)
	if err != nil {
		if ctxErr := context.Cause(ctx); ctxErr != nil {
			return llm.GeneratedImage{}, ctxErr
		}
		return llm.GeneratedImage{}, fmt.Errorf("openaicodex: image-generation request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
		message := strings.TrimSpace(string(body))
		if readErr != nil {
			return llm.GeneratedImage{}, fmt.Errorf("openaicodex: image-generation status %d (error body truncated: %w): %s", resp.StatusCode, readErr, message)
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			return llm.GeneratedImage{}, codexRateLimitError(message, resp.Header)
		}
		return llm.GeneratedImage{}, fmt.Errorf("openaicodex: image-generation status %d: %s", resp.StatusCode, message)
	}

	return parseImageGenerationSSE(ctx, resp.Body)
}

func parseImageGenerationSSE(ctx context.Context, body io.Reader) (llm.GeneratedImage, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), maxImageGenerationResponseBytes+len("data: ")+1)
	var event strings.Builder
	var result imageGenerationOutput
	terminal := false
	consume := func(payload string) error {
		if payload == "" {
			return nil
		}
		if payload == "[DONE]" {
			terminal = true
			return nil
		}
		var item imageGenerationEvent
		if err := json.Unmarshal([]byte(payload), &item); err != nil {
			return fmt.Errorf("openaicodex: decode image-generation event: %w", err)
		}
		switch item.Type {
		case "response.output_item.done":
			if item.Item.Type == "image_generation_call" && item.Item.Result != "" {
				result = item.Item
			}
		case "response.completed":
			terminal = true
			for _, output := range item.Response.Output {
				if output.Type == "image_generation_call" && output.Result != "" {
					result = output
				}
			}
		case "response.failed":
			message := item.Response.Error.Message
			if message == "" {
				message = item.Error.Message
			}
			if message == "" {
				message = "image generation request failed"
			}
			return fmt.Errorf("openaicodex: %s", message)
		}
		return nil
	}
	for !terminal && scanner.Scan() {
		if err := context.Cause(ctx); err != nil {
			return llm.GeneratedImage{}, err
		}
		line := scanner.Text()
		if line == "" {
			if err := consume(event.String()); err != nil {
				return llm.GeneratedImage{}, err
			}
			event.Reset()
			continue
		}
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		data = strings.TrimPrefix(data, " ")
		if event.Len()+len(data)+1 > maxImageGenerationResponseBytes {
			return llm.GeneratedImage{}, errors.New("openaicodex: image-generation response exceeds 32 MiB")
		}
		if event.Len() > 0 {
			event.WriteByte('\n')
		}
		event.WriteString(data)
	}
	if err := scanner.Err(); err != nil && !terminal {
		if contextErr := context.Cause(ctx); contextErr != nil {
			return llm.GeneratedImage{}, contextErr
		}
		return llm.GeneratedImage{}, fmt.Errorf("openaicodex: read image-generation stream: %w", err)
	}
	if event.Len() > 0 {
		if err := consume(event.String()); err != nil {
			return llm.GeneratedImage{}, err
		}
	}
	if err := context.Cause(ctx); err != nil {
		return llm.GeneratedImage{}, err
	}
	if result.Result == "" {
		return llm.GeneratedImage{}, errors.New("openaicodex: image-generation response contained no image")
	}
	data, err := base64.StdEncoding.DecodeString(result.Result)
	if err != nil {
		return llm.GeneratedImage{}, fmt.Errorf("openaicodex: decode generated image: %w", err)
	}
	if len(data) == 0 || len(data) > maxImageGenerationResponseBytes {
		return llm.GeneratedImage{}, errors.New("openaicodex: generated image is empty or exceeds 32 MiB")
	}
	if _, err := png.DecodeConfig(bytes.NewReader(data)); err != nil {
		return llm.GeneratedImage{}, errors.New("openaicodex: generated image is not a valid PNG")
	}
	return llm.GeneratedImage{Data: data, MIMEType: "image/png", RevisedPrompt: result.RevisedPrompt}, nil
}

var _ llm.ImageGenerator = (*Provider)(nil)
