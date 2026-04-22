package umbra

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client communicates with the Umbra sidecar service.
type Client struct {
	baseURL    string
	authToken  string
	httpClient *http.Client
}

// NewClient creates a new Umbra sidecar client.
func NewClient(baseURL string, authToken string) *Client {
	return &Client{
		baseURL:   baseURL,
		authToken: authToken,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// --- Request / Response types ---

type InitRequest struct {
	PrivateKey string `json:"privateKey"`
}

type InitResponse struct {
	Status  string `json:"status"`
	Address string `json:"address"`
}

type RegisterResponse struct {
	Signatures []string `json:"signatures"`
	Count      int      `json:"count"`
}

type ShieldRequest struct {
	Mint        string `json:"mint"`
	Amount      string `json:"amount"`
	Destination string `json:"destination,omitempty"`
}

type ShieldResponse struct {
	QueueSignature    string `json:"queueSignature"`
	CallbackSignature string `json:"callbackSignature"`
}

type UnshieldRequest struct {
	Mint        string `json:"mint"`
	Amount      string `json:"amount"`
	Destination string `json:"destination,omitempty"`
}

type UnshieldResponse struct {
	QueueSignature    string `json:"queueSignature"`
	CallbackSignature string `json:"callbackSignature"`
}

type BalanceRequest struct {
	Mint string `json:"mint"`
}

type BalanceResponse struct {
	Mint      string `json:"mint"`
	Available string `json:"available"`
	Pending   string `json:"pending"`
}

type PrivateTransferRequest struct {
	Mint      string `json:"mint"`
	Amount    string `json:"amount"`
	Recipient string `json:"recipient"`
}

type PrivateTransferResponse struct {
	Signatures []string `json:"signatures,omitempty"`
	Error      string   `json:"error,omitempty"`
}

type ClaimUtxosRequest struct {
	TreeIndex  int `json:"treeIndex"`
	StartIndex int `json:"startIndex"`
}

type ClaimUtxosResponse struct {
	Received int    `json:"received"`
	Claimed  int    `json:"claimed"`
	Error    string `json:"error,omitempty"`
}

type ViewingKeyRequest struct {
	Scope string `json:"scope"`
	Year  int    `json:"year,omitempty"`
	Month int    `json:"month,omitempty"`
	Day   int    `json:"day,omitempty"`
}

type ViewingKeyResponse struct {
	Scope  string `json:"scope"`
	Year   int    `json:"year,omitempty"`
	Month  int    `json:"month,omitempty"`
	Day    int    `json:"day,omitempty"`
	Status string `json:"status"`
}

type HealthResponse struct {
	Status string `json:"status"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

// --- Client methods ---

func (c *Client) Health(ctx context.Context) (*HealthResponse, error) {
	var resp HealthResponse
	if err := c.get(ctx, "/health", &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) Init(ctx context.Context, privateKey string) (*InitResponse, error) {
	var resp InitResponse
	if err := c.post(ctx, "/init", &InitRequest{PrivateKey: privateKey}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) Register(ctx context.Context) (*RegisterResponse, error) {
	var resp RegisterResponse
	if err := c.post(ctx, "/register", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) Shield(ctx context.Context, req *ShieldRequest) (*ShieldResponse, error) {
	var resp ShieldResponse
	if err := c.post(ctx, "/shield", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) Unshield(ctx context.Context, req *UnshieldRequest) (*UnshieldResponse, error) {
	var resp UnshieldResponse
	if err := c.post(ctx, "/unshield", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) GetBalance(ctx context.Context, mint string) (*BalanceResponse, error) {
	var resp BalanceResponse
	if err := c.post(ctx, "/balance", &BalanceRequest{Mint: mint}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) PrivateTransfer(ctx context.Context, req *PrivateTransferRequest) (*PrivateTransferResponse, error) {
	var resp PrivateTransferResponse
	if err := c.post(ctx, "/private-transfer", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) ClaimUtxos(ctx context.Context, req *ClaimUtxosRequest) (*ClaimUtxosResponse, error) {
	var resp ClaimUtxosResponse
	if err := c.post(ctx, "/claim-utxos", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) DeriveViewingKey(ctx context.Context, req *ViewingKeyRequest) (*ViewingKeyResponse, error) {
	var resp ViewingKeyResponse
	if err := c.post(ctx, "/viewing-key", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// --- HTTP helpers ---

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("umbra: create request: %w", err)
	}
	return c.do(req, out)
}

func (c *Client) post(ctx context.Context, path string, body any, out any) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("umbra: marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("umbra: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.authToken != "" {
		req.Header.Set("X-Sidecar-Token", c.authToken)
	}
	return c.do(req, out)
}

func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("umbra: request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("umbra: read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var errResp ErrorResponse
		_ = json.Unmarshal(data, &errResp)
		return fmt.Errorf("umbra: %s %s returned %d: %s", req.Method, req.URL.Path, resp.StatusCode, errResp.Error)
	}

	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("umbra: decode response: %w", err)
		}
	}
	return nil
}
