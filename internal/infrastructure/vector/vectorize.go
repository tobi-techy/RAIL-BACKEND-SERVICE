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
	"sync"
	"time"

	"github.com/google/uuid"
	memory "github.com/rail-service/rail_service/internal/domain/services/ai/memory"
	"go.uber.org/zap"
)

const vectorizeAPIBase = "https://api.cloudflare.com/client/v4/accounts/%s/vectorize/v2/indexes"

// VectorizeConfig configures the Cloudflare Vectorize vector store client.
type VectorizeConfig struct {
	AccountID        string
	APIToken         string
	DefaultDim       int    // embedding dimension (768 for @cf/baai/bge-base-en-v1.5)
	CollectionPrefix string // optional prefix for index names
	// BaseURL overrides the API base (default
	// https://api.cloudflare.com/client/v4/accounts/{id}/vectorize/v2/indexes).
	// Used by tests; leave empty in production.
	BaseURL string
}

// VectorizeStore implements memory.VectorStore via Cloudflare Vectorize's REST API.
// It is a drop-in alternative to QdrantStore and works best paired with the
// Workers AI embedder (same Cloudflare account).
type VectorizeStore struct {
	config   *VectorizeConfig
	embedder memory.Embedder
	baseURL  string
	http     *http.Client
	logger   *zap.Logger

	mu            sync.Mutex
	metadataReady map[string]bool // indexes where the user_id metadata index is known ready
}

// NewVectorizeStore creates a Vectorize vector store client. It returns an
// error when the required credentials are missing so callers can decide how to
// handle the misconfiguration instead of crashing the process.
func NewVectorizeStore(config *VectorizeConfig, embedder memory.Embedder, logger *zap.Logger) (*VectorizeStore, error) {
	if config == nil {
		return nil, fmt.Errorf("VectorizeConfig is required")
	}
	if config.AccountID == "" {
		return nil, fmt.Errorf("VectorizeConfig.AccountID is required")
	}
	if config.APIToken == "" {
		return nil, fmt.Errorf("VectorizeConfig.APIToken is required")
	}
	if config.DefaultDim == 0 {
		config.DefaultDim = 768
	}
	baseURL := strings.TrimSuffix(config.BaseURL, "/")
	if baseURL == "" {
		baseURL = fmt.Sprintf(vectorizeAPIBase, config.AccountID)
	}
	return &VectorizeStore{
		config:        config,
		embedder:      embedder,
		baseURL:       baseURL,
		http:          &http.Client{Timeout: 10 * time.Second},
		logger:        logger,
		metadataReady: make(map[string]bool),
	}, nil
}

// indexName returns the fully-qualified Vectorize index name. Vectorize V2
// requires lowercase kebab-case names, so the optional prefix is joined with a
// hyphen and the result is validated before any API call.
func (v *VectorizeStore) indexName(base string) (string, error) {
	name := base
	if v.config.CollectionPrefix != "" {
		name = v.config.CollectionPrefix + "-" + base
	}
	if !validIndexName(name) {
		return "", fmt.Errorf("vectorize invalid index name %q: must be lowercase kebab-case, start with a letter, and be shorter than 32 characters", name)
	}
	return name, nil
}

// validIndexName reports whether name satisfies Vectorize V2 index naming rules.
func validIndexName(name string) bool {
	if name == "" || len(name) > 31 {
		return false
	}
	if name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			continue
		}
		return false
	}
	return true
}

// Store stores a text entry as a vector in the given Vectorize index.
func (v *VectorizeStore) Store(ctx context.Context, collection string, userID uuid.UUID, content string, metadata map[string]string) error {
	name, err := v.indexName(collection)
	if err != nil {
		return err
	}

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

	// V2 upserts are NDJSON: one JSON object per line.
	line, err := json.Marshal(map[string]interface{}{
		"id":       uuid.New().String(),
		"values":   embedding,
		"metadata": payload,
	})
	if err != nil {
		return fmt.Errorf("vectorize marshal upsert: %w", err)
	}

	var resp struct {
		Result struct {
			MutationID string `json:"mutationId"`
		} `json:"result"`
	}
	if err := v.do(ctx, http.MethodPost, "/"+name+"/upsert?unparsable-behavior=error", line, "application/x-ndjson", &resp); err != nil {
		return fmt.Errorf("vectorize upsert: %w", err)
	}
	return nil
}

// Search finds the most similar entries to a query text in the given index.
func (v *VectorizeStore) Search(ctx context.Context, collection string, userID uuid.UUID, query string, limit int) ([]memory.Result, error) {
	name, err := v.indexName(collection)
	if err != nil {
		return nil, err
	}

	embedding, err := v.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("vectorize embed query: %w", err)
	}

	body, err := json.Marshal(map[string]interface{}{
		"vector":         embedding,
		"topK":           limit,
		"returnMetadata": "indexed",
		"filter":         map[string]string{"user_id": userID.String()},
	})
	if err != nil {
		return nil, fmt.Errorf("vectorize marshal query: %w", err)
	}

	var resp struct {
		Result struct {
			Matches []struct {
				ID       string                 `json:"id"`
				Score    float64                `json:"score"`
				Metadata map[string]interface{} `json:"metadata"`
			} `json:"matches"`
		} `json:"result"`
	}
	if err := v.do(ctx, http.MethodPost, "/"+name+"/query", body, "application/json", &resp); err != nil {
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
	name, err := v.indexName(collection)
	if err != nil {
		return err
	}
	// 404 is treated as success (index already gone).
	var resp struct{}
	err = v.do(ctx, http.MethodDelete, "/"+name, nil, "application/json", &resp)
	if err == nil || isNotFound(err) {
		return nil
	}
	return fmt.Errorf("vectorize delete: %w", err)
}

// ensureIndex creates the index if it does not exist and makes sure the
// user_id metadata index is ready before any write.
func (v *VectorizeStore) ensureIndex(ctx context.Context, name string) error {
	var resp struct{}
	if err := v.do(ctx, http.MethodGet, "/"+name, nil, "application/json", &resp); err == nil {
		return v.ensureMetadataIndex(ctx, name)
	} else if !isNotFound(err) {
		return err
	}

	createBody, err := json.Marshal(map[string]interface{}{
		"name": name,
		"config": map[string]interface{}{
			"dimensions": v.config.DefaultDim,
			"metric":     "cosine",
		},
	})
	if err != nil {
		return fmt.Errorf("vectorize marshal create index: %w", err)
	}
	var created struct{}
	if err := v.do(ctx, http.MethodPost, "", createBody, "application/json", &created); err != nil {
		// Idempotency: a concurrent writer may have created the index between our
		// GET 404 and this POST. If it now exists, ensure its metadata index.
		if isConflict(err) {
			if checkErr := v.do(ctx, http.MethodGet, "/"+name, nil, "application/json", &resp); checkErr == nil {
				return v.ensureMetadataIndex(ctx, name)
			}
		}
		return fmt.Errorf("vectorize create index: %w", err)
	}

	return v.ensureMetadataIndex(ctx, name)
}

// ensureMetadataIndex makes sure the user_id metadata index exists and is
// processed before a write. Vectorize metadata index creation is asynchronous,
// so we poll the list endpoint until the index is reported ready.
func (v *VectorizeStore) ensureMetadataIndex(ctx context.Context, name string) error {
	if v.isMetadataReady(name) {
		return nil
	}

	ready, err := v.metadataIndexExists(ctx, name)
	if err != nil {
		return fmt.Errorf("vectorize list metadata index: %w", err)
	}
	if !ready {
		if err := v.createMetadataIndex(ctx, name); err != nil && !isConflict(err) {
			return fmt.Errorf("vectorize create metadata index: %w", err)
		}
	}
	if err := v.waitMetadataIndexReady(ctx, name); err != nil {
		return fmt.Errorf("vectorize wait for metadata index: %w", err)
	}
	v.markMetadataReady(name)
	return nil
}

func (v *VectorizeStore) isMetadataReady(name string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.metadataReady[name]
}

func (v *VectorizeStore) markMetadataReady(name string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.metadataReady[name] = true
}

func (v *VectorizeStore) metadataIndexExists(ctx context.Context, name string) (bool, error) {
	var resp struct {
		Result struct {
			MetadataIndexes []struct {
				IndexType    string `json:"indexType"`
				PropertyName string `json:"propertyName"`
			} `json:"metadataIndexes"`
		} `json:"result"`
	}
	if err := v.do(ctx, http.MethodGet, "/"+name+"/metadata_index/list", nil, "application/json", &resp); err != nil {
		return false, err
	}
	for _, idx := range resp.Result.MetadataIndexes {
		if idx.PropertyName == "user_id" && idx.IndexType == "string" {
			return true, nil
		}
	}
	return false, nil
}

func (v *VectorizeStore) createMetadataIndex(ctx context.Context, name string) error {
	body, err := json.Marshal(map[string]interface{}{
		"indexType":    "string",
		"propertyName": "user_id",
	})
	if err != nil {
		return fmt.Errorf("vectorize marshal metadata index: %w", err)
	}
	var resp struct{}
	return v.do(ctx, http.MethodPost, "/"+name+"/metadata_index/create", body, "application/json", &resp)
}

func (v *VectorizeStore) waitMetadataIndexReady(ctx context.Context, name string) error {
	const maxAttempts = 20
	delay := 25 * time.Millisecond
	for i := 0; i < maxAttempts; i++ {
		ready, err := v.metadataIndexExists(ctx, name)
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
		if i == maxAttempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		if delay < 500*time.Millisecond {
			delay *= 2
			if delay > 500*time.Millisecond {
				delay = 500 * time.Millisecond
			}
		}
	}
	return fmt.Errorf("user_id metadata index did not become ready after %d attempts", maxAttempts)
}

// isConflict reports whether err is an HTTP 409 conflict response.
func isConflict(err error) bool {
	var statusErr *httpStatusError
	return errors.As(err, &statusErr) && statusErr.status == http.StatusConflict
}

// do performs an authenticated Vectorize API request and decodes the response.
// A non-2xx status is returned as an error carrying the API error message.
func (v *VectorizeStore) do(ctx context.Context, method, path string, body []byte, contentType string, out interface{}) error {
	url := v.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("vectorize request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
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
