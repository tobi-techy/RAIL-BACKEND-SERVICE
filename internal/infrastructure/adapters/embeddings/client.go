package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
)

const (
	openAIEmbeddingsURL = "https://api.openai.com/v1/embeddings"
	geminiEmbeddingsURL = "https://generativelanguage.googleapis.com/v1beta/models/text-embedding-004:embedContent"
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
		model:    "text-embedding-004",
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

type geminiEmbedRequest struct {
	Model   string              `json:"model"`
	Content geminiEmbedContent  `json:"content"`
}

type geminiEmbedContent struct {
	Parts []geminiEmbedPart `json:"parts"`
}

type geminiEmbedPart struct {
	Text string `json:"text"`
}

type geminiEmbedResponse struct {
	Embedding struct {
		Values []float32 `json:"values"`
	} `json:"embedding"`
}

func (c *Client) embedGemini(ctx context.Context, text string) ([]float32, error) {
	url := geminiEmbeddingsURL + "?key=" + c.apiKey
	body, err := json.Marshal(geminiEmbedRequest{
		Model:   "models/text-embedding-004",
		Content: geminiEmbedContent{Parts: []geminiEmbedPart{{Text: text}}},
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
