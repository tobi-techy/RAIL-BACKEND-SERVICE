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
	RawDescription string `json:"raw_description"`
	MCCCode        *int   `json:"mcc_code,omitempty"`
}

// EnrichResponse is what the sidecar returns.
type EnrichResponse struct {
	Counterparty        string  `json:"counterparty"`
	CategoryL1          *string `json:"category_l1"`
	CategoryL2          *string `json:"category_l2"`
	IsEssential         bool    `json:"is_essential"`
	Confidence          float64 `json:"confidence"`
	ClassificationLayer string  `json:"classification_layer"`
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
func (c *Client) Enrich(ctx context.Context, raw string) (*EnrichResponse, error) {
	body, _ := json.Marshal(EnrichRequest{RawDescription: raw})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/enrich", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
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

// EnrichBatch classifies multiple transactions in one call.
func (c *Client) EnrichBatch(ctx context.Context, descriptions []string) ([]EnrichResponse, error) {
	txns := make([]EnrichRequest, len(descriptions))
	for i, d := range descriptions {
		txns[i] = EnrichRequest{RawDescription: d}
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
