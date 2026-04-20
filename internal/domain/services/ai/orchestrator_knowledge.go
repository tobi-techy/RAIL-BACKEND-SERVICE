package ai

import (
	"context"
	"fmt"
	"strings"

	"github.com/rail-service/rail_service/internal/domain/entities"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
)

// ToolSearchKnowledge is the tool name for RAG knowledge base search.
const ToolSearchKnowledge = "search_knowledge_base"

// KnowledgeSearcher is the subset of knowledge.Service the orchestrator needs.
type KnowledgeSearcher interface {
	Search(ctx context.Context, query string, limit int) ([]entities.KnowledgeSearchResult, error)
}

// SetKnowledge sets the knowledge search provider (optional).
// Deprecated: Use NewOrchestratorWithDeps instead.
func (o *Orchestrator) SetKnowledge(k KnowledgeSearcher) {
	o.knowledge = k
}

// KnowledgeTool returns the tool definition for the knowledge base search.
func KnowledgeTool() infraai.Tool {
	return infraai.Tool{
		Name:        ToolSearchKnowledge,
		Description: "Search the financial knowledge base for information about investing, saving, budgeting, financial concepts, and money management. Use this when the user asks about financial topics, concepts, or strategies.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "The search query for the knowledge base",
				},
			},
			"required": []string{"query"},
		},
	}
}

// MinKnowledgeSimilarity is the cosine similarity threshold below which
// knowledge results are discarded as irrelevant.
const MinKnowledgeSimilarity = 0.70

// executeKnowledgeSearch handles the search_knowledge_base tool call.
func (o *Orchestrator) executeKnowledgeSearch(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
	if o.knowledge == nil {
		return map[string]interface{}{"error": "knowledge base not configured"}, nil
	}

	query, _ := args["query"].(string)
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}

	results, err := o.knowledge.Search(ctx, query, 3)
	if err != nil {
		return nil, fmt.Errorf("knowledge search: %w", err)
	}

	// Filter out low-relevance results
	filtered := results[:0]
	for _, r := range results {
		if r.Similarity >= MinKnowledgeSimilarity {
			filtered = append(filtered, r)
		}
	}

	if len(filtered) == 0 {
		return map[string]interface{}{"found": false, "message": "No relevant information found"}, nil
	}

	// Build context from top results
	var sb strings.Builder
	for i, r := range filtered {
		fmt.Fprintf(&sb, "[Source: %s] %s", r.SourceDoc, r.ChunkText)
		if i < len(filtered)-1 {
			sb.WriteString("\n\n")
		}
	}

	return map[string]interface{}{
		"found":   true,
		"context": sb.String(),
		"sources": len(filtered),
	}, nil
}
