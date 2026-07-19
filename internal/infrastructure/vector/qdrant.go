package vector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	memory "github.com/rail-service/rail_service/internal/domain/services/ai/memory"
	"go.uber.org/zap"
)

// QdrantConfig configures the Qdrant vector store client.
type QdrantConfig struct {
	BaseURL    string // e.g. "http://localhost:6333"
	APIKey     string
	DefaultDim int    // embedding dimension (e.g. 768 for ada-002)
	CollectionPrefix string // optional prefix for collection names
}

// QdrantStore implements VectorStore via Qdrant's REST API.
type QdrantStore struct {
	config  *QdrantConfig
	embedder memory.Embedder
	httpClient *http.Client
	logger     *zap.Logger
}

// NewQdrantStore creates a new Qdrant vector store client.
func NewQdrantStore(config *QdrantConfig, embedder memory.Embedder, logger *zap.Logger) *QdrantStore {
	return &QdrantStore{
		config:  config,
		embedder: embedder,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		logger:     logger,
	}
}

// collectionName returns the fully-qualified collection name.
func (q *QdrantStore) collectionName(base string) string {
	if q.config.CollectionPrefix != "" {
		return q.config.CollectionPrefix + "_" + base
	}
	return base
}

// Store stores a text entry as a vector point in Qdrant.
func (q *QdrantStore) Store(ctx context.Context, collection string, userID uuid.UUID, content string, metadata map[string]string) error {
	col := q.collectionName(collection)

	// Ensure collection exists
	if err := q.ensureCollection(ctx, col); err != nil {
		return fmt.Errorf("qdrant ensure collection: %w", err)
	}

	// Generate embedding
	embedding, err := q.embedder.Embed(ctx, content)
	if err != nil {
		return fmt.Errorf("qdrant embed: %w", err)
	}

	// Build point with payload
	payload := map[string]interface{}{
		"user_id": userID.String(),
		"content": content,
	}
	for k, v := range metadata {
		payload[k] = v
	}

	point := map[string]interface{}{
		"id":      uuid.New().String(), // unique ID per point
		"vector":  embedding,
		"payload": payload,
	}

	body, _ := json.Marshal(map[string]interface{}{
		"points": []interface{}{point},
	})

	req, err := http.NewRequestWithContext(ctx, "PUT", fmt.Sprintf("%s/collections/%s/points", q.config.BaseURL, col), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("qdrant request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if q.config.APIKey != "" {
		req.Header.Set("api-key", q.config.APIKey)
	}

	resp, err := q.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("qdrant upsert: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("qdrant upsert status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// Search finds the most similar entries to a query text.
func (q *QdrantStore) Search(ctx context.Context, collection string, userID uuid.UUID, query string, limit int) ([]memory.Result, error) {
	col := q.collectionName(collection)

	// Generate query embedding
	embedding, err := q.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("qdrant embed query: %w", err)
	}

	// Build search request
	searchBody := map[string]interface{}{
		"vector": embedding,
		"limit":  limit,
		"with_payload": true,
		"filter": map[string]interface{}{
			"must": []map[string]interface{}{
				{
					"key": "user_id",
					"match": map[string]interface{}{
						"value": userID.String(),
					},
				},
			},
		},
	}

	body, _ := json.Marshal(searchBody)
	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/collections/%s/points/search", q.config.BaseURL, col), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("qdrant search request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if q.config.APIKey != "" {
		req.Header.Set("api-key", q.config.APIKey)
	}

	resp, err := q.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("qdrant search: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("qdrant search status %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse response
	var searchResp struct {
		Result []struct {
			ID      string                 `json:"id"`
			Score   float64                `json:"score"`
			Payload map[string]interface{} `json:"payload"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		return nil, fmt.Errorf("qdrant decode: %w", err)
	}

	results := make([]memory.Result, 0, len(searchResp.Result))
	for _, r := range searchResp.Result {
		content, _ := r.Payload["content"].(string)
		results = append(results, memory.Result{
			Content:    content,
			Similarity: r.Score,
			Source:     "qdrant",
		})
	}

	return results, nil
}

// DeleteCollection removes an entire Qdrant collection.
func (q *QdrantStore) DeleteCollection(ctx context.Context, collection string) error {
	col := q.collectionName(collection)
	req, err := http.NewRequestWithContext(ctx, "DELETE", fmt.Sprintf("%s/collections/%s", q.config.BaseURL, col), nil)
	if err != nil {
		return fmt.Errorf("qdrant delete request: %w", err)
	}
	if q.config.APIKey != "" {
		req.Header.Set("api-key", q.config.APIKey)
	}

	resp, err := q.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("qdrant delete: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 && resp.StatusCode != 404 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("qdrant delete status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// ensureCollection creates the collection if it doesn't exist.
func (q *QdrantStore) ensureCollection(ctx context.Context, collection string) error {
	// Check if collection exists
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/collections/%s", q.config.BaseURL, collection), nil)
	if err != nil {
		return fmt.Errorf("qdrant check request: %w", err)
	}
	if q.config.APIKey != "" {
		req.Header.Set("api-key", q.config.APIKey)
	}

	resp, err := q.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("qdrant check: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode == 200 {
		return nil // exists
	}

	// Create collection
	dim := q.config.DefaultDim
	if dim == 0 {
		dim = 768 // default for ada-002
	}

	createBody, _ := json.Marshal(map[string]interface{}{
		"name": collection,
		"vectors": map[string]interface{}{
			"size":   dim,
			"distance": "Cosine",
		},
	})

	req, err = http.NewRequestWithContext(ctx, "PUT", fmt.Sprintf("%s/collections/%s", q.config.BaseURL, collection), bytes.NewReader(createBody))
	if err != nil {
		return fmt.Errorf("qdrant create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if q.config.APIKey != "" {
		req.Header.Set("api-key", q.config.APIKey)
	}

	resp, err = q.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("qdrant create: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("qdrant create status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}


