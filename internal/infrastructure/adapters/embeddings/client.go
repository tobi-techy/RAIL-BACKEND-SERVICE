package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	cencori "github.com/cencori/cencori-go"
	"go.uber.org/zap"
)

const (
	openAIEmbeddingsURL = "https://api.openai.com/v1/embeddings"
	geminiEmbeddingsURL = "https://generativelanguage.googleapis.com/v1beta/models/gemini-embedding-001:embedContent"
)

// Client calls an embeddings API (OpenAI or Gemini).
type Client struct {
	apiKey   string
	model    string
	provider string // "openai" or "gemini"
	client   *http.Client
	logger   *zap.Logger
}

// NewClient creates a new embeddings client using OpenAI.
func NewClient(apiKey string, logger *zap.Logger) *Client {
	return &Client{
		apiKey:   apiKey,
		model:    "text-embedding-3-small",
		provider: "openai",
		client:   &http.Client{Timeout: 30 * time.Second},
		logger:   logger,
	}
}

// NewGeminiClient creates a new embeddings client using Google Gemini (free).
func NewGeminiClient(apiKey string, logger *zap.Logger) *Client {
	return &Client{
		apiKey:   apiKey,
		model:    "gemini-embedding-001",
		provider: "gemini",
		client:   &http.Client{Timeout: 30 * time.Second},
		logger:   logger,
	}
}

type embeddingRequest struct {
	Input string `json:"input"`
	Model string `json:"model"`
}

type embeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Usage struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
}

// Embed generates an embedding vector for the given text.
func (c *Client) Embed(ctx context.Context, text string) ([]float32, error) {
	if c.provider == "gemini" {
		return c.embedGemini(ctx, text)
	}
	return c.embedOpenAI(ctx, text)
}

func (c *Client) embedOpenAI(ctx context.Context, text string) ([]float32, error) {
	body, err := json.Marshal(embeddingRequest{Input: text, Model: c.model})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIEmbeddingsURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embeddings request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("embeddings API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result embeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(result.Data) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}

	c.logger.Debug("embedding generated", zap.Int("tokens", result.Usage.TotalTokens))
	return result.Data[0].Embedding, nil
}

type geminiEmbedResponse struct {
	Embedding struct {
		Values []float32 `json:"values"`
	} `json:"embedding"`
}

func (c *Client) embedGemini(ctx context.Context, text string) ([]float32, error) {
	url := geminiEmbeddingsURL + "?key=" + c.apiKey
	body, err := json.Marshal(map[string]interface{}{
		"model": "models/gemini-embedding-001",
		"content": map[string]interface{}{
			"parts": []map[string]string{{"text": text}},
		},
		"outputDimensionality": 768,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embeddings request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("gemini embeddings error %d: %s", resp.StatusCode, string(respBody))
	}

	var result geminiEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(result.Embedding.Values) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}

	c.logger.Debug("gemini embedding generated", zap.Int("dimensions", len(result.Embedding.Values)))
	return result.Embedding.Values, nil
}

// EmbedBatch generates embeddings for multiple texts. Calls the API
// sequentially to stay within rate limits.
func (c *Client) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, 0, len(texts))
	for _, text := range texts {
		emb, err := c.Embed(ctx, text)
		if err != nil {
			return nil, fmt.Errorf("embed chunk: %w", err)
		}
		results = append(results, emb)
	}
	return results, nil
}

// CencoriEmbeddingsClient uses the official Cencori Go SDK for embeddings.
// It routes through the Cencori gateway, which handles provider selection,
// failover, and billing automatically.
type CencoriEmbeddingsClient struct {
	client *cencori.Client
	model  string
	logger *zap.Logger
}

// NewCencoriEmbeddingsClient creates a new embeddings client using the Cencori SDK.
// baseURL may be empty (default Cencori endpoint) or a Cloudflare AI Gateway URL
// to route embedding traffic through the gateway for caching/analytics.
func NewCencoriEmbeddingsClient(apiKey string, model string, baseURL string, logger *zap.Logger) *CencoriEmbeddingsClient {
	if model == "" {
		model = "text-embedding-3-small"
	}

	opts := []cencori.Option{
		cencori.WithAPIKey(apiKey),
		cencori.WithTimeout(30 * time.Second),
	}
	if baseURL != "" {
		opts = append(opts, cencori.WithBaseURL(baseURL))
	}

	client, err := cencori.NewClient(opts...)
	if err != nil {
		logger.Error("Failed to create Cencori embeddings client", zap.Error(err))
		return &CencoriEmbeddingsClient{
			model:  model,
			logger: logger,
		}
	}

	return &CencoriEmbeddingsClient{
		client: client,
		model:  model,
		logger: logger,
	}
}

// Embed generates an embedding vector for the given text via Cencori.
func (c *CencoriEmbeddingsClient) Embed(ctx context.Context, text string) ([]float32, error) {
	if c.client == nil {
		return nil, fmt.Errorf("Cencori client not initialized")
	}

	resp, err := c.client.Chat.Embeddings(ctx, cencori.EmbeddingParams{
		Input: text,
		Model: c.model,
	})
	if err != nil {
		return nil, fmt.Errorf("cencori embeddings: %w", err)
	}

	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("no embedding returned from Cencori")
	}

	// Convert float64 to float32
	emb := make([]float32, len(resp.Data[0].Embedding))
	for i, v := range resp.Data[0].Embedding {
		emb[i] = float32(v)
	}

	c.logger.Debug("cencori embedding generated",
		zap.Int("dimensions", len(emb)),
		zap.Int("tokens", resp.Usage.TotalTokens),
	)

	return emb, nil
}

// EmbedBatch generates embeddings for multiple texts via Cencori.
func (c *CencoriEmbeddingsClient) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, 0, len(texts))
	for _, text := range texts {
		emb, err := c.Embed(ctx, text)
		if err != nil {
			return nil, fmt.Errorf("embed chunk: %w", err)
		}
		results = append(results, emb)
	}
	return results, nil
}
