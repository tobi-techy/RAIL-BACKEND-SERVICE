package enrichment

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// EnrichRequest is the payload sent to the sidecar.
type EnrichRequest struct {
	RawDescription     string    `json:"raw_description"`
	Currency           string    `json:"currency,omitempty"`           // ISO 4217 code (USD, EUR, NGN)
	MCCCode            *int      `json:"mcc_code,omitempty"`
	Amount             *float64  `json:"amount,omitempty"`
	HistoricalAmounts  []float64 `json:"historical_amounts,omitempty"`
	HistoricalDates    []string  `json:"historical_dates,omitempty"`
}

// BehaviorTag represents a detected behavioral pattern.
type BehaviorTag struct {
	Tag        string             `json:"tag"`
	Confidence float64            `json:"confidence"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

// TransactionFact represents a durable financial fact extracted from a transaction.
type TransactionFact struct {
	Type       string  `json:"type"`
	Value      string  `json:"value"`
	Confidence float64 `json:"confidence"`
	Category   string  `json:"category"`
}

// EnrichResponse is what the sidecar returns.
type EnrichResponse struct {
	Counterparty        string           `json:"counterparty"`
	CategoryL1          *string          `json:"category_l1"`
	CategoryL2          *string          `json:"category_l2"`
	IsEssential         bool             `json:"is_essential"`
	Confidence          float64          `json:"confidence"`
	ClassificationLayer string           `json:"classification_layer"`
	PlainDescription    string           `json:"plain_description"`
	MerchantContext     string           `json:"merchant_context"`
	BehaviorTags        []BehaviorTag    `json:"behavior_tags"`
	Facts               []TransactionFact `json:"facts"`
	Embedding           []float64        `json:"embedding"`
	Bank                *string          `json:"bank"`
	TxType              *string          `json:"tx_type"`
}

// BatchRequest wraps multiple transactions.
type BatchRequest struct {
	Transactions []EnrichRequest `json:"transactions"`
}

// BatchResponse wraps multiple results.
type BatchResponse struct {
	Results []EnrichResponse `json:"results"`
}

// Client calls the Python enrichment sidecar over HTTP.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a client pointing at the enrichment sidecar.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Enrich classifies a single transaction.
func (c *Client) Enrich(ctx context.Context, raw string, currency string) (*EnrichResponse, error) {
	enrichReq := EnrichRequest{RawDescription: raw}
	if currency != "" {
		enrichReq.Currency = currency
	}
	body, _ := json.Marshal(enrichReq)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/enrich", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("enrichment sidecar: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("enrichment sidecar returned %d", resp.StatusCode)
	}

	var result EnrichResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

// EnrichBatchWithCurrency classifies multiple transactions with currency context in one call.
func (c *Client) EnrichBatchWithCurrency(ctx context.Context, descriptions []string, currencies []string) ([]EnrichResponse, error) {
	txns := make([]EnrichRequest, len(descriptions))
	for i, d := range descriptions {
		txns[i] = EnrichRequest{RawDescription: d}
		if i < len(currencies) && currencies[i] != "" {
			txns[i].Currency = currencies[i]
		}
	}

	body, _ := json.Marshal(BatchRequest{Transactions: txns})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/enrich/batch", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("enrichment sidecar batch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("enrichment sidecar batch returned %d", resp.StatusCode)
	}

	var result BatchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Results, nil
}

// EnrichBatch classifies multiple transactions without currency context (backward-compatible).
func (c *Client) EnrichBatch(ctx context.Context, descriptions []string) ([]EnrichResponse, error) {
	return c.EnrichBatchWithCurrency(ctx, descriptions, nil)
}
