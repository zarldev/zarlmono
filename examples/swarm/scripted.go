package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/zarldev/zarlmono/zkit/agent/board"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	boardtools "github.com/zarldev/zarlmono/zkit/agent/tools/board"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// Each activation owns one client, used synchronously by its runner. Decisions
// use only model-visible tool results, never direct access to the shared board.
type scriptedClient struct {
	prerequisite contribution
	step         contribution
	runID        string
	calls        int
	lastID       string
	lastTool     tools.ToolName
	readID       board.ID
	posted       bool
}

var _ runner.Client = (*scriptedClient)(nil)

func (c *scriptedClient) Complete(ctx context.Context, request llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		if err := context.Cause(ctx); err != nil {
			yield(llm.CompletionChunk{}, err)
			return
		}
		chunk, err := c.next(request.Messages)
		yield(chunk, err)
	}
}

func (c *scriptedClient) next(history []llm.Message) (llm.CompletionChunk, error) {
	if c.posted {
		return runnertest.ChunkText("Contribution already published; no duplicate work."), nil
	}
	if c.lastID == "" {
		return c.call(boardtools.ListName, struct {
			Limit int `json:"limit"`
		}{Limit: board.MaxPage})
	}
	for _, message := range history {
		if message.Role == llm.RoleTool && message.ToolCallID == c.lastID {
			return c.receive(message.Content)
		}
	}
	return llm.CompletionChunk{}, fmt.Errorf("script missing result for %s", c.lastID)
}

func (c *scriptedClient) receive(content string) (llm.CompletionChunk, error) {
	switch c.lastTool {
	case boardtools.ListName:
		return c.readPrerequisite(content)
	case boardtools.ReadName:
		var posts []board.Post
		if err := json.Unmarshal([]byte(content), &posts); err != nil {
			return llm.CompletionChunk{}, fmt.Errorf("decode board read: %w", err)
		}
		if len(posts) != 1 || posts[0].ID != c.readID || posts[0].Author != author(c.prerequisite.role) ||
			posts[0].Topic != c.prerequisite.topic || posts[0].Body != c.prerequisite.body {
			return llm.CompletionChunk{}, fmt.Errorf("%w: %s prerequisite evidence mismatch", ErrIncomplete, c.step.role)
		}
		return c.call(boardtools.PostName, struct {
			Summary string   `json:"summary"`
			Body    string   `json:"body"`
			Topic   string   `json:"topic"`
			ReplyTo board.ID `json:"reply_to"`
		}{Summary: string(c.step.role) + " contribution", Body: c.step.body, Topic: c.step.topic, ReplyTo: c.readID})
	case boardtools.PostName:
		var receipt board.Summary
		if err := json.Unmarshal([]byte(content), &receipt); err != nil {
			return llm.CompletionChunk{}, fmt.Errorf("decode board receipt: %w", err)
		}
		if receipt.ID == 0 || receipt.Author != author(c.step.role) || receipt.ReplyTo != c.readID {
			return llm.CompletionChunk{}, fmt.Errorf("%w: %s publication receipt mismatch", ErrIncomplete, c.step.role)
		}
		c.posted = true
		return runnertest.ChunkText("Published evidence; waiting for peers."), nil
	default:
		return llm.CompletionChunk{}, fmt.Errorf("script received unexpected tool %s", c.lastTool)
	}
}

func (c *scriptedClient) readPrerequisite(content string) (llm.CompletionChunk, error) {
	var page board.Page
	if err := json.Unmarshal([]byte(content), &page); err != nil {
		return llm.CompletionChunk{}, fmt.Errorf("decode board list: %w", err)
	}
	for _, post := range page.Posts {
		if post.Author == author(c.step.role) && post.Topic == c.step.topic {
			c.posted = true
			return runnertest.ChunkText("My contribution is already on the board."), nil
		}
		if post.Author == author(c.prerequisite.role) && post.Topic == c.prerequisite.topic {
			c.readID = post.ID
		}
	}
	if c.readID != 0 {
		return c.call(boardtools.ReadName, struct {
			IDs []board.ID `json:"ids"`
		}{IDs: []board.ID{c.readID}})
	}
	// A new observation can arrive before this execution seals. If the runner
	// continues this same turn, refresh the board rather than reusing this page.
	c.lastID = ""
	return runnertest.ChunkText("Prerequisite not yet available; idle without publishing an acknowledgment."), nil
}

func (c *scriptedClient) call(name tools.ToolName, args any) (llm.CompletionChunk, error) {
	encoded, err := json.Marshal(args)
	if err != nil {
		return llm.CompletionChunk{}, fmt.Errorf("encode %s arguments: %w", name, err)
	}
	c.calls++
	c.lastID = fmt.Sprintf("%s-%d", c.runID, c.calls)
	c.lastTool = name
	return runnertest.ChunkToolCall(c.lastID, name.String(), string(encoded)), nil
}
