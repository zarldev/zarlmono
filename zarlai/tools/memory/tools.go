package memory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zarldev/zarlmono/zarlai/service"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/vectorstore/qdrant"
)

// Collection is the Qdrant collection name for person memories.
const Collection = "person_memories"

// Embedder converts text to a vector embedding.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// RememberTool stores a fact about the current person.
type RememberTool struct {
	qdrant   *qdrant.Client
	embedder Embedder
}

// NewRememberTool creates a RememberTool.
func NewRememberTool(q *qdrant.Client, e Embedder) *RememberTool {
	return &RememberTool{qdrant: q, embedder: e}
}

func (t *RememberTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "remember",
		Description: "Persist a DURABLE fact about the person you're currently talking to — something that will still be true and useful a month from now. Good: preferences, allergies, relationships, biographical facts, possessions, recurring routines, nicknames, scheduled commitments. Examples: \"prefers dark roast\", \"is allergic to peanuts\", \"has a border collie called Pip\", \"drives an EV\". Call only when the user explicitly says \"remember that…\", \"don't forget…\", \"for future reference…\", OR volunteers a stable personal detail. NEVER store: in-session actions (\"asked to play X\", \"requested a search\", \"queued a track\", \"paused playback\"), in-the-moment observations (\"is drinking X\", \"made noises\", \"considers Y classic\"), vague capabilities already obvious from your tools (\"uses Home Assistant\", \"has Wi-Fi\"), or facts about other people. Run recall first if unsure whether it's already known. The fact is embedded and indexed against the current speaker; no acknowledgement is shown, so give one in your reply.",
		Parameters: service.Parameters{
			{Name: "fact", Type: service.ParamString, Description: "A single self-contained fact in natural prose (e.g. \"prefers dark roast coffee\", \"works remotely on Tuesdays\"). One fact per call — call multiple times for multiple facts.", Required: true},
		}.ToJSONSchema(),
	}
}

func (t *RememberTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	fact := call.Arguments.String("fact", "")
	if fact == "" {
		return tools.Failure(call.ID, tools.Validation("remember", "fact is required")), nil
	}

	name := service.PersonNameFromCtx(ctx)
	if name == "" {
		return tools.Success(call.ID, "No person identified — cannot store memory without knowing who it's about."), nil
	}

	vec, err := t.embedder.Embed(ctx, fact)
	if err != nil {
		return tools.Failure(call.ID, tools.Transient("remember", fmt.Errorf("embed fact: %w", err))), nil
	}

	point := qdrant.Point{
		ID:     uuid.New().String(),
		Vector: vec,
		Payload: map[string]any{
			"person_name": name,
			"fact":        fact,
			"created_at":  time.Now().UTC().Format(time.RFC3339),
		},
	}

	if err := t.qdrant.Upsert(ctx, Collection, []qdrant.Point{point}); err != nil {
		return tools.Failure(call.ID, tools.Transient("remember", fmt.Errorf("upsert memory: %w", err))), nil
	}

	return tools.Success(call.ID, fmt.Sprintf("Remembered about %s: %s", name, fact)), nil
}

// RecallTool searches memories for the current or named person.
type RecallTool struct {
	qdrant   *qdrant.Client
	embedder Embedder
}

// NewRecallTool creates a RecallTool.
func NewRecallTool(q *qdrant.Client, e Embedder) *RecallTool {
	return &RecallTool{qdrant: q, embedder: e}
}

func (t *RecallTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "recall",
		Description: "Look up what you already know about the current person (or another named person) before answering. Call this PROACTIVELY when the user asks \"do you remember…\", \"what do you know about me\", \"what's my…\", or any personal context question, AND whenever the topic touches a preference/relationship/routine you may have stored (e.g. coffee order, kids' names, work schedule). Also call it at the start of a substantive reply if remembering context would change the answer. Semantic search — phrase the query like a topic (\"coffee preferences\", \"allergies\", \"family\") not a full sentence. Returns up to 10 matching facts or 'No memories found.'",
		Parameters: service.Parameters{
			{Name: "query", Type: service.ParamString, Description: "Topic to search memories for (e.g. \"allergies\", \"birthday\", \"work schedule\", \"favourite music\").", Required: true},
			{Name: "person_name", Type: service.ParamString, Description: "Optional — name of a different person. Defaults to the person currently in front of the camera.", Required: false},
		}.ToJSONSchema(),
	}
}

func (t *RecallTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	query := call.Arguments.String("query", "")
	if query == "" {
		return tools.Failure(call.ID, tools.Validation("recall", "query is required")), nil
	}

	personName := call.Arguments.String("person_name", "")
	if personName == "" {
		personName = service.PersonNameFromCtx(ctx)
	}

	vec, err := t.embedder.Embed(ctx, query)
	if err != nil {
		return tools.Failure(call.ID, tools.Transient("recall", fmt.Errorf("embed query: %w", err))), nil
	}

	var filter *qdrant.Filter
	if personName != "" {
		filter = &qdrant.Filter{
			Must: []qdrant.FieldCondition{
				{Key: "person_name", Match: qdrant.MatchValue{Value: personName}},
			},
		}
	}

	results, err := t.qdrant.Search(ctx, qdrant.SearchRequest{
		Collection: Collection,
		Vector:     vec,
		Filter:     filter,
		Limit:      10,
	})
	if err != nil {
		return tools.Failure(call.ID, tools.Transient("recall", fmt.Errorf("search memories: %w", err))), nil
	}

	if len(results) == 0 {
		return tools.Success(call.ID, "No memories found."), nil
	}

	var sb strings.Builder
	for i, r := range results {
		fact, _ := r.Payload["fact"].(string)
		fmt.Fprintf(&sb, "%d. %s\n", i+1, fact)
	}

	return tools.Success(call.ID, strings.TrimRight(sb.String(), "\n")), nil
}

// ForgetTool removes a memory matching a query.
type ForgetTool struct {
	qdrant   *qdrant.Client
	embedder Embedder
}

// NewForgetTool creates a ForgetTool.
func NewForgetTool(q *qdrant.Client, e Embedder) *ForgetTool {
	return &ForgetTool{qdrant: q, embedder: e}
}

func (t *ForgetTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "forget",
		Description: "Delete a single stored memory when the user explicitly asks you to forget it (phrases like \"forget that…\", \"I don't want you to remember…\", \"that's wrong, remove it\") or corrects a fact you previously stored. Matches by semantic similarity and removes only the closest hit — re-call if multiple memories need clearing. Never call preemptively or to \"clean up\"; only on an explicit user instruction.",
		Parameters: service.Parameters{
			{Name: "query", Type: service.ParamString, Description: "A phrase describing the memory to remove (e.g. \"my old phone number\", \"the meeting on Tuesday\").", Required: true},
			{Name: "person_name", Type: service.ParamString, Description: "Optional — name of a different person. Defaults to the current speaker.", Required: false},
		}.ToJSONSchema(),
	}
}

func (t *ForgetTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	query := call.Arguments.String("query", "")
	if query == "" {
		return tools.Failure(call.ID, tools.Validation("forget", "query is required")), nil
	}

	personName := call.Arguments.String("person_name", "")
	if personName == "" {
		personName = service.PersonNameFromCtx(ctx)
	}

	vec, err := t.embedder.Embed(ctx, query)
	if err != nil {
		return tools.Failure(call.ID, tools.Transient("forget", fmt.Errorf("embed query: %w", err))), nil
	}

	var filter *qdrant.Filter
	if personName != "" {
		filter = &qdrant.Filter{
			Must: []qdrant.FieldCondition{
				{Key: "person_name", Match: qdrant.MatchValue{Value: personName}},
			},
		}
	}

	results, err := t.qdrant.Search(ctx, qdrant.SearchRequest{
		Collection: Collection,
		Vector:     vec,
		Filter:     filter,
		Limit:      1,
	})
	if err != nil {
		return tools.Failure(call.ID, tools.Transient("forget", fmt.Errorf("search memories: %w", err))), nil
	}

	if len(results) == 0 || results[0].Score < 0.8 {
		return tools.Success(call.ID, "No matching memory found."), nil
	}

	best := results[0]
	fact, _ := best.Payload["fact"].(string)

	if err := t.qdrant.DeleteByID(ctx, Collection, best.ID); err != nil {
		return tools.Failure(call.ID, tools.Transient("forget", fmt.Errorf("delete memory: %w", err))), nil
	}

	return tools.Success(call.ID, fmt.Sprintf("Forgot: %s", fact)), nil
}

// LoadRecentMemories fetches recent memories for a person, for injecting into LLM context.
func LoadRecentMemories(ctx context.Context, q *qdrant.Client, e Embedder, personName string, limit int) ([]string, error) {
	vec, err := e.Embed(ctx, personName)
	if err != nil {
		return nil, fmt.Errorf("embed person name: %w", err)
	}

	filter := &qdrant.Filter{
		Must: []qdrant.FieldCondition{
			{Key: "person_name", Match: qdrant.MatchValue{Value: personName}},
		},
	}

	results, err := q.Search(ctx, qdrant.SearchRequest{
		Collection: Collection,
		Vector:     vec,
		Filter:     filter,
		Limit:      limit,
	})
	if err != nil {
		return nil, fmt.Errorf("search memories: %w", err)
	}

	facts := make([]string, 0, len(results))
	for _, r := range results {
		if fact, ok := r.Payload["fact"].(string); ok {
			facts = append(facts, fact)
		}
	}

	return facts, nil
}
