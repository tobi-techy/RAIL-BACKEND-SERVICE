package ai

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
)

const ToolWebSearch = "web_search"

// searchResultCache is a TTL cache for search results (1hr, global across users).
type searchResultCache struct {
	mu      sync.RWMutex
	entries map[string]searchCacheEntry
}

type searchCacheEntry struct {
	result    map[string]interface{}
	expiresAt time.Time
}

var searchCache = &searchResultCache{entries: make(map[string]searchCacheEntry)}

func (c *searchResultCache) Get(key string) (map[string]interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[key]
	if !ok || time.Now().After(entry.expiresAt) {
		return nil, false
	}
	return entry.result, true
}

func (c *searchResultCache) Set(key string, result map[string]interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = searchCacheEntry{result: result, expiresAt: time.Now().Add(1 * time.Hour)}
	// Lazy eviction: purge expired entries if cache grows large
	if len(c.entries) > 200 {
		now := time.Now()
		for k, v := range c.entries {
			if now.After(v.expiresAt) {
				delete(c.entries, k)
			}
		}
	}
}

// normalizeSearchQuery lowercases and trims the query for cache key dedup.
func normalizeSearchQuery(q string) string {
	return strings.ToLower(strings.TrimSpace(q))
}

// WebSearcher is the interface for web search (satisfied by TavilyClient).
type WebSearcher interface {
	Search(ctx context.Context, req infraai.TavilySearchRequest) (*infraai.TavilySearchResponse, error)
}

// SetWebSearcher wires the web search provider.
func (o *Orchestrator) SetWebSearcher(s WebSearcher) {
	o.webSearcher = s
}

// WebSearchTool returns the tool definition.
func WebSearchTool() infraai.Tool {
	return infraai.Tool{
		Name:        ToolWebSearch,
		Description: "Search the internet for anything the user asks about: restaurants, products, flights, events, services, experiences, news. Use when the user asks for recommendations, comparisons, or information you don't have. Returns results with titles, links, descriptions, and images.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "The search query. Be specific: include location, budget, or preferences the user mentioned.",
				},
			},
			"required":             []string{"query"},
			"additionalProperties": false,
		},
	}
}

// executeWebSearch runs a Tavily search and returns results with budget context.
func (o *Orchestrator) executeWebSearch(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	query, _ := args["query"].(string)
	if query == "" {
		return map[string]interface{}{"error": "query is required"}, nil
	}

	// Check cache first
	cacheKey := normalizeSearchQuery(query)
	if cached, ok := searchCache.Get(cacheKey); ok {
		return cached, nil
	}

	resp, err := o.webSearcher.Search(ctx, infraai.TavilySearchRequest{
		Query:         query,
		MaxResults:    5,
		IncludeImages: true,
	})
	if err != nil {
		return map[string]interface{}{"error": "search failed, try again"}, nil
	}

	// Build results for the LLM to reference
	results := make([]map[string]interface{}, 0, len(resp.Results))
	for _, r := range resp.Results {
		results = append(results, map[string]interface{}{
			"title":   r.Title,
			"url":     r.URL,
			"content": r.Content,
		})
	}

	// Attach images if available
	images := make([]map[string]interface{}, 0, len(resp.Images))
	for _, img := range resp.Images {
		images = append(images, map[string]interface{}{
			"url":         img.URL,
			"description": img.Description,
		})
	}

	// Get user's spend balance for budget context
	var budget string
	if o.aggregateStats != nil {
		spend, err := o.aggregateStats.GetAccountBalance(ctx, userID, "spending_balance")
		if err == nil {
			budget = "$" + spend.StringFixed(2)
		}
	}

	result := map[string]interface{}{
		"results": results,
		"images":  images,
		"count":   len(results),
	}
	if budget != "" {
		result["user_spend_balance"] = budget
		result["budget_note"] = "Tell the user which options fit their budget. Flag anything that might be expensive relative to their balance."
	}

	// Cache the result (without budget since that's per-user)
	cacheResult := map[string]interface{}{
		"results": results,
		"images":  images,
		"count":   len(results),
	}
	searchCache.Set(cacheKey, cacheResult)

	return result, nil
}
