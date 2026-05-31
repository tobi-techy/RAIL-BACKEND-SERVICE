package supermemory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	baseURL         = "https://api.supermemory.ai"
	maxResponseBody = 1 << 20 // 1 MB
)

// Client is a minimal Supermemory API client.
type Client struct {
	apiKey     string
	httpClient *http.Client
}

func New(apiKey string) *Client {
	return &Client{
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// Message is a single conversation turn.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// IngestConversation sends a conversation to Supermemory for fact extraction.
// containerTag scopes memory to a specific user.
func (c *Client) IngestConversation(ctx context.Context, userID string, messages []Message) error {
	type reqMsg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type payload struct {
		ConversationID string   `json:"conversationId"`
		ContainerTag   string   `json:"containerTag"`
		Messages       []reqMsg `json:"messages"`
	}
	msgs := make([]reqMsg, len(messages))
	for i, m := range messages {
		msgs[i] = reqMsg{Role: m.Role, Content: m.Content}
	}
	return c.postWithRetry(ctx, "/v4/conversations", payload{
		ConversationID: fmt.Sprintf("miriam-%s-%d", userID, time.Now().UnixMilli()),
		ContainerTag:   userID,
		Messages:       msgs,
	}, nil)
}

// SearchResult is a single memory result.
type SearchResult struct {
	Memory     string  `json:"memory"`
	Similarity float64 `json:"similarity"`
}

// SearchMemory searches a user's memory for relevant facts.
func (c *Client) SearchMemory(ctx context.Context, userID, query string, limit int) ([]SearchResult, error) {
	type payload struct {
		Q            string `json:"q"`
		ContainerTag string `json:"containerTag"`
		Limit        int    `json:"limit,omitempty"`
	}
	type response struct {
		Results []SearchResult `json:"results"`
	}
	var resp response
	if err := c.postWithRetry(ctx, "/v4/search", payload{Q: query, ContainerTag: userID, Limit: limit}, &resp); err != nil {
		return nil, err
	}
	return resp.Results, nil
}

// postWithRetry executes a POST with one retry on 429/5xx.
func (c *Client) postWithRetry(ctx context.Context, path string, reqBody, out interface{}) error {
	err := c.post(ctx, path, reqBody, out)
	if err == nil {
		return nil
	}
	// Single retry after brief backoff for transient errors
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(500 * time.Millisecond):
	}
	return c.post(ctx, path, reqBody, out)
}

func (c *Client) post(ctx context.Context, path string, reqBody, out interface{}) error {
	b, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		// Drain body to allow connection reuse
		io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBody)) //nolint:errcheck
		resp.Body.Close()
	}()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("supermemory %s: status %d", path, resp.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(out)
	}
	return nil
}
