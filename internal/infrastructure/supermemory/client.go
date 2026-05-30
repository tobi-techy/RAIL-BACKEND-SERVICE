package supermemory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const baseURL = "https://api.supermemory.ai"

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
	type body struct {
		ConversationID string   `json:"conversationId"`
		ContainerTag   string   `json:"containerTag"`
		Messages       []reqMsg `json:"messages"`
	}
	msgs := make([]reqMsg, len(messages))
	for i, m := range messages {
		msgs[i] = reqMsg{Role: m.Role, Content: m.Content}
	}
	return c.post(ctx, "/v4/conversations", body{
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
	type body struct {
		Q            string `json:"q"`
		ContainerTag string `json:"containerTag"`
		Limit        int    `json:"limit,omitempty"`
	}
	type response struct {
		Results []SearchResult `json:"results"`
	}
	var resp response
	if err := c.post(ctx, "/v4/search", body{Q: query, ContainerTag: userID, Limit: limit}, &resp); err != nil {
		return nil, err
	}
	return resp.Results, nil
}

func (c *Client) post(ctx context.Context, path string, body, out interface{}) error {
	b, err := json.Marshal(body)
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
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("supermemory: status %d", resp.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
