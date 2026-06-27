package retrieval

import (
	"context"
	"fmt"
	"time"

	airetrieval "github.com/zarldev/zarlmono/zkit/ai/retrieval"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// ToolNameRetrieve is the default retriever tool name.
const ToolNameRetrieve tools.ToolName = "retrieve_context"

// Tool exposes a Retriever as an LLM-callable tool.
type Tool struct {
	Name        tools.ToolName
	Description string
	Retriever   airetrieval.Retriever
	Format      FormatOptions
}

type retrieveArgs struct {
	Query string `json:"query" doc:"Natural-language query to retrieve relevant context for."`
	Limit int    `json:"limit,omitempty" doc:"Maximum number of documents to return."`
}

// Definition describes the retrieval tool.
func (t Tool) Definition() tools.ToolSpec {
	name := t.Name
	if name == "" {
		name = ToolNameRetrieve
	}
	desc := t.Description
	if desc == "" {
		desc = "Retrieve relevant context documents for a query."
	}
	return tools.ToolSpec{Name: name, Description: desc, Parameters: tools.SchemaFor[retrieveArgs]()}
}

// Execute runs retrieval and returns both formatted text and raw documents.
func (t Tool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	var args retrieveArgs
	if err := tools.DecodeArgs(call.Arguments, &args); err != nil {
		return nil, err
	}
	opts := []airetrieval.RetrieveOption(nil)
	if args.Limit > 0 {
		opts = append(opts, airetrieval.WithLimit(args.Limit))
	}
	docs, err := t.Retriever.Retrieve(ctx, args.Query, opts...)
	if err != nil {
		return nil, fmt.Errorf("retrieve context: %w", err)
	}
	return &tools.ToolResult{ToolCallID: call.ID, Success: true, Data: map[string]any{"text": FormatDocuments(docs, t.Format), "documents": docs}, Metadata: tools.NewToolMetadata(t.Definition().Name), ExecutedAt: time.Now()}, nil
}
