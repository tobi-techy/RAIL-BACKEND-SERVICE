package supermemory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
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
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// Message is a single conversation turn.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// IngestConversation sends a conversation to Supermemory for fact extraction.
// containerTag scopes memory to a specific user. Message content is PII-redacted
// before it leaves the process.
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
		msgs[i] = reqMsg{Role: m.Role, Content: RedactPII(m.Content)}
	}
	return c.postWithRetry(ctx, "/v4/conversations", payload{
		ConversationID: fmt.Sprintf("miriam-%s-%d", userID, time.Now().UnixMilli()),
		ContainerTag:   userID,
		Messages:       msgs,
	}, nil)
}

// Memory is a single memory entry to create directly.
type Memory struct {
	Content         string            `json:"content"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	TemporalContext *TemporalContext  `json:"temporalContext,omitempty"`
}

// TemporalContext provides date context for a memory.
type TemporalContext struct {
	DocumentDate string   `json:"documentDate,omitempty"`
	EventDate    []string `json:"eventDate,omitempty"`
}

// CreateMemories writes memories directly (up to 100 per call) without going through
// the conversation ingestion workflow. Memories are immediately searchable.
//
// This is the single write chokepoint for direct memories: content is PII-redacted
// and a numeric event_ts (unix seconds) is injected into metadata from the memory's
// temporal context so that recency-aware retrieval and date filtering work uniformly
// across every ingestion path (statements, receipts, budgets, trends).
func (c *Client) CreateMemories(ctx context.Context, containerTag string, memories []Memory) error {
	type payload struct {
		Memories     []Memory `json:"memories"`
		ContainerTag string   `json:"containerTag"`
	}

	prepared := make([]Memory, len(memories))
	for i, m := range memories {
		m.Content = RedactPII(m.Content)
		if ts, ok := eventUnixFromTemporal(m.TemporalContext); ok {
			if m.Metadata == nil {
				m.Metadata = map[string]string{}
			} else {
				cloned := make(map[string]string, len(m.Metadata)+1)
				for k, v := range m.Metadata {
					cloned[k] = v
				}
				m.Metadata = cloned
			}
			if _, exists := m.Metadata["event_ts"]; !exists {
				m.Metadata["event_ts"] = strconv.FormatInt(ts, 10)
			}
		}
		prepared[i] = m
	}

	// API limit is 100 per call — batch if needed
	for i := 0; i < len(prepared); i += 100 {
		end := i + 100
		if end > len(prepared) {
			end = len(prepared)
		}
		if err := c.postWithRetry(ctx, "/v4/memories", payload{
			Memories:     prepared[i:end],
			ContainerTag: containerTag,
		}, nil); err != nil {
			return fmt.Errorf("batch %d-%d: %w", i, end, err)
		}
		// Brief pause between batches to avoid rate limits
		if end < len(prepared) {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(200 * time.Millisecond):
			}
		}
	}
	return nil
}

// eventUnixFromTemporal derives a unix-seconds timestamp from a memory's temporal
// context, preferring the first event date, then the document date.
func eventUnixFromTemporal(tc *TemporalContext) (int64, bool) {
	if tc == nil {
		return 0, false
	}
	candidates := make([]string, 0, len(tc.EventDate)+1)
	candidates = append(candidates, tc.EventDate...)
	if tc.DocumentDate != "" {
		candidates = append(candidates, tc.DocumentDate)
	}
	for _, s := range candidates {
		if s == "" {
			continue
		}
		for _, layout := range []string{"2006-01-02", time.RFC3339, "2006-01-02T15:04:05Z"} {
			if t, err := time.Parse(layout, s); err == nil {
				return t.UTC().Unix(), true
			}
		}
	}
	return 0, false
}

// SearchResult is a single memory result.
type SearchResult struct {
	Memory     string
	Similarity float64
	Metadata   map[string]string
	UpdatedAt  time.Time
}

// FilterCondition is a single metadata/numeric/string filter for search.
type FilterCondition struct {
	FilterType      string // metadata|numeric|array_contains|string_contains ("" => metadata)
	Key             string
	Value           string
	NumericOperator string // >,<,>=,<=,= (numeric only)
	Negate          bool
	IgnoreCase      bool
}

// SearchOptions controls a memory search.
type SearchOptions struct {
	Limit      int
	Threshold  float64           // 0..1; <=0 => server default
	Rerank     bool              // rerank results by query relevance
	FiltersAND []FilterCondition // combined with AND
}

// SearchMemory searches a user's memory for relevant facts (simple form).
func (c *Client) SearchMemory(ctx context.Context, userID, query string, limit int) ([]SearchResult, error) {
	return c.Search(ctx, userID, query, SearchOptions{Limit: limit})
}

// Search runs a memory search with optional metadata filters and reranking.
func (c *Client) Search(ctx context.Context, userID, query string, opts SearchOptions) ([]SearchResult, error) {
	body := map[string]interface{}{
		"q":            query,
		"containerTag": userID,
	}
	if opts.Limit > 0 {
		body["limit"] = opts.Limit
	}
	if opts.Threshold > 0 {
		body["threshold"] = opts.Threshold
	}
	if opts.Rerank {
		body["rerank"] = true
	}
	if len(opts.FiltersAND) > 0 {
		conds := make([]map[string]interface{}, 0, len(opts.FiltersAND))
		for _, f := range opts.FiltersAND {
			cond := map[string]interface{}{"key": f.Key, "value": f.Value}
			if f.FilterType != "" {
				cond["filterType"] = f.FilterType
			}
			if f.NumericOperator != "" {
				cond["numericOperator"] = f.NumericOperator
			}
			if f.Negate {
				cond["negate"] = true
			}
			if f.IgnoreCase {
				cond["ignoreCase"] = true
			}
			conds = append(conds, cond)
		}
		body["filters"] = map[string]interface{}{"AND": conds}
	}

	type rawResult struct {
		Memory     string                 `json:"memory"`
		Similarity float64                `json:"similarity"`
		Metadata   map[string]interface{} `json:"metadata"`
		UpdatedAt  string                 `json:"updatedAt"`
	}
	type response struct {
		Results []rawResult `json:"results"`
	}
	var resp response
	if err := c.postWithRetry(ctx, "/v4/search", body, &resp); err != nil {
		return nil, err
	}

	out := make([]SearchResult, 0, len(resp.Results))
	for _, r := range resp.Results {
		res := SearchResult{Memory: r.Memory, Similarity: r.Similarity}
		if len(r.Metadata) > 0 {
			res.Metadata = make(map[string]string, len(r.Metadata))
			for k, v := range r.Metadata {
				res.Metadata[k] = stringifyMeta(v)
			}
		}
		if r.UpdatedAt != "" {
			if t, err := time.Parse(time.RFC3339, r.UpdatedAt); err == nil {
				res.UpdatedAt = t
			}
		}
		out = append(out, res)
	}
	return out, nil
}

func stringifyMeta(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return fmt.Sprintf("%v", t)
	}
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
