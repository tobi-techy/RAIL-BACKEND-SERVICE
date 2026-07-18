package document

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

// LLMEnricher categorizes/normalizes extracted fields via an OpenAI-compatible
// chat API. It sends only the small structured payload — never the image — so
// token cost stays low.
type LLMEnricher struct {
	apiKey     string
	baseURL    string
	model      string
	httpClient *http.Client
	logger     *zap.Logger
}

// NewLLMEnricher builds an enricher. Returns nil when no API key is configured,
// which the pipeline treats as "skip enrichment".
func NewLLMEnricher(apiKey, baseURL, model string, logger *zap.Logger) *LLMEnricher {
	if apiKey == "" {
		return nil
	}
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	if model == "" {
		model = "gpt-4o-mini"
	}
	return &LLMEnricher{
		apiKey:     apiKey,
		baseURL:    strings.TrimRight(baseURL, "/"),
		model:      model,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		logger:     logger,
	}
}

const enrichSystemPrompt = `You normalize and categorize financial documents. Given structured fields, respond with ONLY compact JSON: {"category":"...","description":"...","merchant":"..."}.
category: one of Food & Dining, Groceries, Transport, Shopping, Entertainment, Health, Utilities, Logistics, Education, Services, Subscriptions, Income, Transfer, Other.
description: a short human label. merchant: cleaned merchant name. Do not invent amounts.`

// Categorize implements Enricher.
func (e *LLMEnricher) Categorize(ctx context.Context, f *Fields) (*Enrichment, error) {
	// Send a compact projection of fields, never the raw text/image.
	projection := map[string]interface{}{
		"type":     string(f.Type),
		"merchant": f.Merchant,
		"currency": f.Currency,
	}
	if f.Total != nil {
		projection["total"] = f.Total.String()
	}
	if f.Date != nil {
		projection["date"] = f.Date.Format("2006-01-02")
	}
	userJSON, _ := json.Marshal(projection)

	body := map[string]interface{}{
		"model":       e.model,
		"temperature": 0.1,
		"max_tokens":  200,
		"messages": []map[string]interface{}{
			{"role": "system", "content": enrichSystemPrompt},
			{"role": "user", "content": string(userJSON)},
		},
	}
	payload, _ := json.Marshal(body)

	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, e.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("enrich call: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1*1024*1024))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("enrich api error %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, err
	}
	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("empty enrich response")
	}

	content := strings.TrimSpace(result.Choices[0].Message.Content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var enr Enrichment
	if err := json.Unmarshal([]byte(content), &enr); err != nil {
		return nil, fmt.Errorf("parse enrich json: %w", err)
	}
	return &enr, nil
}
