package vector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	memory "github.com/rail-service/rail_service/internal/domain/services/ai/memory"
	"go.uber.org/zap"
)

const vectorizeAPIBase = "https://api.cloudflare.com/client/v4/accounts/%s/vectorize/v1/indexes"

// VectorizeConfig configures the Cloudflare Vectorize vector store client.
type VectorizeConfig struct {
	AccountID        string
	APIToken         string
	DefaultDim       int    // embedding dimension (768 for @cf/baai/bge-base-en-v1.5)
	CollectionPrefix string // optional prefix for index names
	// BaseURL overrides the API base (default
	// https://api.cloudflare.com/client/v4/accounts/{id}/vectorize/v1/indexes).
	// Used by tests; leave empty in production.
	BaseURL string
}

// VectorizeStore implements memory.VectorStore via Cloudflare Vectorize's REST API.
// It is a drop-in alternative to QdrantStore and works best paired with the
// Workers AI embedder (free, same Cloudflare account).
type VectorizeStore struct {
	config   *VectorizeConfig
	embedder memory.Embedder
	baseURL  string
	http     *http.Client
	logger   *zap.Logger
}

// NewVectorizeStore creates a Vectorize vector store client.
func NewVectorizeStore(config *VectorizeConfig, embedder memory.Embedder, logger *zap.Logger) *VectorizeStore {
	if config.DefaultDim == 0 {
		config.DefaultDim = 768
	}
	baseURL := strings.TrimSuffix(config.BaseURL, "/")
	if baseURL == "" {
		baseURL = fmt.Sprintf(vectorizeAPIBase, config.AccountID)
	}
	return &VectorizeStore{
		config:   config,
		embedder: embedder,
		baseURL:  baseURL,
		http:     &http.Client{Timeout: 10 * time.Second},
		logger:   logger,
	}
}

// indexName returns the fully-qualified Vectorize index name.
func (v *VectorizeStore) indexName(base string) string {
	if v.config.CollectionPrefix != "" {
		return v.config.CollectionPrefix + "_" + base
	}
	return base
}

// Store stores a text entry as a vector in the given Vectorize index.
func (v *VectorizeStore) Store(ctx context.Context, collection string, userID uuid.UUID, content string, metadata map[string]string) error {
	name := v.indexName(collection)

	if err := v.ensureIndex(ctx, name); err != nil {
		return fmt.Errorf("vectorize ensure index: %w", err)
	}

	embedding, err := v.embedder.Embed(ctx, content)
	if err != nil {
		return fmt.Errorf("vectorize embed: %w", err)
	}

	payload := map[string]string{"user_id": userID.String(), "content": content}
	for k, val := range metadata {
		payload[k] = val
	}

	body, _ := json.Marshal(map[string]interface{}{
		"ids":      []string{uuid.New().String()},
		"vectors":  [][]float32{embedding},
		"metadata": []map[string]string{payload},
	})

	var resp struct {
		Result struct {
			IDs []string `json:"ids"`
		} `json:"result"`
	}
	if err := v.do(ctx, http.MethodPost, "/"+name+"/upsert", body, &resp); err != nil {
		return fmt.Errorf("vectorize upsert: %w", err)
	}
	return nil
}

// Search finds the most similar entries to a query text in the given index.
func (v *VectorizeStore) Search(ctx context.Context, collection string, userID uuid.UUID, query string, limit int) ([]memory.Result, error) {
	name := v.indexName(collection)

	embedding, err := v.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("vectorize embed query: %w", err)
	}

	body, _ := json.Marshal(map[string]interface{}{
		"vector": embedding,
		"top_k":  limit,
		"filter": map[string]string{"user_id": userID.String()},
	})

	var resp struct {
		Result struct {
			Matches []struct {
				ID       string                 `json:"id"`
				Score    float64                `json:"score"`
				Metadata map[string]interface{} `json:"metadata"`
			} `json:"matches"`
		} `json:"result"`
	}
	if err := v.do(ctx, http.MethodPost, "/"+name+"/query", body, &resp); err != nil {
		return nil, fmt.Errorf("vectorize query: %w", err)
	}

	results := make([]memory.Result, 0, len(resp.Result.Matches))
	for _, m := range resp.Result.Matches {
		content, _ := m.Metadata["content"].(string)
		results = append(results, memory.Result{
			Content:    content,
			Similarity: m.Score,
			Source:     "vectorize",
		})
	}
	return results, nil
}

// DeleteCollection removes an entire Vectorize index.
func (v *VectorizeStore) DeleteCollection(ctx context.Context, collection string) error {
	name := v.indexName(collection)
	// 404 is treated as success (index already gone).
	var resp struct{}
	err := v.do(ctx, http.MethodDelete, "/"+name, nil, &resp)
	if err == nil || isNotFound(err) {
		return nil
	}
	return fmt.Errorf("vectorize delete: %w", err)
}

// ensureIndex creates the index if it does not exist.
func (v *VectorizeStore) ensureIndex(ctx context.Context, name string) error {
	var resp struct{}
	if err := v.do(ctx, http.MethodGet, "/"+name, nil, &resp); err == nil {
		return nil // exists
	} else if !isNotFound(err) {
		return err
	}

	createBody, _ := json.Marshal(map[string]interface{}{
		"name":      name,
		"dimension": v.config.DefaultDim,
		"metric":    "cosine",
	})
	var created struct{}
	if err := v.do(ctx, http.MethodPost, "", createBody, &created); err != nil {
		return fmt.Errorf("vectorize create index: %w", err)
	}
	return nil
}

// do performs an authenticated Vectorize API request and decodes the response.
// A non-2xx status is returned as an error carrying the API error message.
func (v *VectorizeStore) do(ctx context.Context, method, path string, body []byte, out interface{}) error {
	url := v.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("vectorize request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+v.config.APIToken)

	resp, err := v.http.Do(req)
	if err != nil {
		return fmt.Errorf("vectorize request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("vectorize read: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &httpStatusError{status: resp.StatusCode, body: string(respBody)}
	}

	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("vectorize decode: %w", err)
		}
	}
	return nil
}

// httpStatusError is a minimal error carrying the HTTP status.
type httpStatusError struct {
	status int
	body   string
}

func (e *httpStatusError) Error() string {
	msg := strings.TrimSpace(e.body)
	if len(msg) > 512 {
		msg = msg[:512] + "..."
	}
	return fmt.Sprintf("vectorize status %d: %s", e.status, msg)
}

func isNotFound(err error) bool {
	var statusErr *httpStatusError
	return errors.As(err, &statusErr) && statusErr.status == http.StatusNotFound
}
