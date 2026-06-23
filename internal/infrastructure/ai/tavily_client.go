package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// TavilyClient is a minimal client for Tavily's search API.
type TavilyClient struct {
	apiKey     string
	httpClient *http.Client
}

// NewTavilyClient creates a Tavily search client.
func NewTavilyClient(apiKey string) *TavilyClient {
	return &TavilyClient{
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// TavilySearchRequest is the request body for Tavily search.
type TavilySearchRequest struct {
	Query         string `json:"query"`
	MaxResults    int    `json:"max_results,omitempty"`
	IncludeImages bool   `json:"include_images,omitempty"`
	Topic         string `json:"topic,omitempty"` // "general" or "news"
}

// TavilySearchResult is a single search result.
type TavilySearchResult struct {
	Title   string  `json:"title"`
	URL     string  `json:"url"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
}

// TavilySearchResponse is the response from Tavily search.
type TavilySearchResponse struct {
	Results []TavilySearchResult `json:"results"`
	Images  []TavilyImage        `json:"images"`
}

// TavilyImage is an image result.
type TavilyImage struct {
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
}

// Search performs a web search via Tavily.
func (c *TavilyClient) Search(ctx context.Context, req TavilySearchRequest) (*TavilySearchResponse, error) {
	body := map[string]interface{}{
		"api_key":        c.apiKey,
		"query":          req.Query,
		"max_results":    req.MaxResults,
		"include_images": req.IncludeImages,
	}
	if req.MaxResults == 0 {
		body["max_results"] = 5
	}
	if req.Topic != "" {
		body["topic"] = req.Topic
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", "https://api.tavily.com/search", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("tavily: %d %s", resp.StatusCode, string(b))
	}

	var result TavilySearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}
