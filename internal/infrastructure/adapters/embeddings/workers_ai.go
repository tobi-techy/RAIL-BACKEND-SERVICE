package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

// WorkersAIEmbedder generates embeddings via Cloudflare Workers AI using the
// BGE small model (@cf/baai/bge-base-en-v1.5, 768 dims). It satisfies the
// memory.Embedder / di.Embedder interfaces so it can replace the paid Cencori
// embeddings path for knowledge and episodic/fact memory.
type WorkersAIEmbedder struct {
	apiBase  string
	apiToken string
	model    string
	http     *http.Client
	logger   *zap.Logger
}

// NewWorkersAIEmbedder creates a Workers AI embedder. model defaults to
// @cf/baai/bge-base-en-v1.5 (768 dimensions). baseURL overrides the API base
// (used by tests); leave empty in production.
func NewWorkersAIEmbedder(accountID, apiToken, model, baseURL string, logger *zap.Logger) *WorkersAIEmbedder {
	if model == "" {
		model = "@cf/baai/bge-base-en-v1.5"
	}
	if baseURL == "" {
		baseURL = fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s", accountID)
	}
	return &WorkersAIEmbedder{
		apiBase:  strings.TrimSuffix(baseURL, "/"),
		apiToken: apiToken,
		model:    model,
		http:     &http.Client{Timeout: 30 * time.Second},
		logger:   logger,
	}
}

// Embed generates an embedding vector for the given text.
func (w *WorkersAIEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	batch, err := w.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(batch) == 0 || len(batch[0]) == 0 {
		return nil, fmt.Errorf("workers ai embeddings: empty result")
	}
	return batch[0], nil
}

// EmbedBatch generates embeddings for multiple texts in a single request.
func (w *WorkersAIEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(map[string]interface{}{"text": texts})
	if err != nil {
		return nil, fmt.Errorf("workers ai embeddings marshal: %w", err)
	}

	url := w.apiBase + "/ai/run/" + w.model
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("workers ai embeddings request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+w.apiToken)

	resp, err := w.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("workers ai embeddings request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("workers ai embeddings read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("workers ai embeddings status %d: %s", resp.StatusCode, truncateBytes(respBody, 512))
	}

	var parsed struct {
		Success bool            `json:"success"`
		Result  json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("workers ai embeddings decode: %w", err)
	}

	vectors, err := parseEmbeddingResult(parsed.Result)
	if err != nil {
		return nil, fmt.Errorf("workers ai embeddings parse: %w", err)
	}
	if len(vectors) == 0 {
		return nil, fmt.Errorf("workers ai embeddings: empty result")
	}

	w.logger.Debug("workers ai embeddings generated",
		zap.Int("count", len(vectors)),
		zap.Int("dimensions", len(vectors[0])),
	)
	return vectors, nil
}

// parseEmbeddingResult handles both Workers AI's 2D array output
// ({"data": [[...]]}) and the OpenAI-style output ({"data": [{"embedding": [...]}]}).
func parseEmbeddingResult(raw json.RawMessage) ([][]float32, error) {
	// Workers AI native format first: {"data": [[...], ...]}
	var native struct {
		Data [][]float32 `json:"data"`
	}
	if err := json.Unmarshal(raw, &native); err == nil && len(native.Data) > 0 && len(native.Data[0]) > 0 {
		return native.Data, nil
	}

	// Fall back to OpenAI-style: {"data": [{"embedding": [...]}]}
	var openAI struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &openAI); err == nil && len(openAI.Data) > 0 && len(openAI.Data[0].Embedding) > 0 {
		out := make([][]float32, len(openAI.Data))
		for i, d := range openAI.Data {
			out[i] = d.Embedding
		}
		return out, nil
	}
	return nil, fmt.Errorf("unrecognized embedding result shape")
}

// truncateBytes returns s cut to n bytes for logging. It never fails.
func truncateBytes(s []byte, n int) string {
	if len(s) <= n {
		return string(s)
	}
	return string(s[:n]) + "..."
}
