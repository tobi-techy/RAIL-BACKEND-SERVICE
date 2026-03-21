package didit

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
)

const (
	baseURL        = "https://verification.didit.me"
	defaultTimeout = 30 * time.Second
)

type Config struct {
	APIKey        string
	WebhookSecret string
	WorkflowID    string
}

type Client struct {
	httpClient *http.Client
	config     Config
	logger     *zap.Logger
}

func NewClient(cfg Config, logger *zap.Logger) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: defaultTimeout},
		config:     cfg,
		logger:     logger,
	}
}

// --- Session types ---

type CreateSessionRequest struct {
	WorkflowID  string `json:"workflow_id"`
	VendorData  string `json:"vendor_data,omitempty"`
	Callback    string `json:"callback,omitempty"`
}

type SessionResponse struct {
	SessionID    string `json:"session_id"`
	SessionToken string `json:"session_token"`
	Status       string `json:"status"`
	WorkflowID   string `json:"workflow_id"`
	URL          string `json:"url"`
}

type SessionDecision struct {
	SessionID  string `json:"session_id"`
	Status     string `json:"status"`
	WorkflowID string `json:"workflow_id"`
	VendorData string `json:"vendor_data"`
	Features   []string `json:"features"`

	IDVerifications []IDVerification `json:"id_verifications"`
}

type IDVerification struct {
	Status         string `json:"status"`
	DocumentType   string `json:"document_type"`
	DocumentNumber string `json:"document_number"`
	PersonalNumber string `json:"personal_number"` // national ID / tax number on document
	FirstName      string `json:"first_name"`
	LastName       string `json:"last_name"`
	DateOfBirth    string `json:"date_of_birth"`
	ExpirationDate string `json:"expiration_date"`
	DateOfIssue    string `json:"date_of_issue"`
	IssuingState   string `json:"issuing_state"`
	Nationality    string `json:"nationality"`
	Gender         string `json:"gender"`
	Address        string `json:"address"`
	ParsedAddress  *struct {
		Street1    string `json:"street_1"`
		Street2    string `json:"street_2"`
		City       string `json:"city"`
		Region     string `json:"region"`
		Country    string `json:"country"`
		PostalCode string `json:"postal_code"`
	} `json:"parsed_address"`
	FrontImage    string `json:"front_image"`
	BackImage     string `json:"back_image"`
	FullFrontImage string `json:"full_front_image"`
	FullBackImage  string `json:"full_back_image"`
	PortraitImage string `json:"portrait_image"`
}

// --- Webhook types ---

type WebhookPayload struct {
	SessionID    string           `json:"session_id"`
	Status       string           `json:"status"`
	WebhookType  string           `json:"webhook_type"`
	CreatedAt    json.Number      `json:"created_at"`
	Timestamp    json.Number      `json:"timestamp"`
	WorkflowID   string           `json:"workflow_id"`
	VendorData   string           `json:"vendor_data"`
	Decision     *WebhookDecision `json:"decision,omitempty"`
}

type WebhookDecision struct {
	SessionID       string           `json:"session_id"`
	Status          string           `json:"status"`
	Features        []string         `json:"features"`
	VendorData      string           `json:"vendor_data"`
	IDVerifications []IDVerification `json:"id_verifications"`
}

// --- API methods ---

func (c *Client) CreateSession(ctx context.Context, vendorData string) (*SessionResponse, error) {
	req := CreateSessionRequest{
		WorkflowID: c.config.WorkflowID,
		VendorData: vendorData,
	}

	var resp SessionResponse
	if err := c.doRequest(ctx, http.MethodPost, "/v3/session/", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) GetSessionDecision(ctx context.Context, sessionID string) (*SessionDecision, error) {
	path := fmt.Sprintf("/v3/session/%s/decision/", sessionID)
	var resp SessionDecision
	if err := c.doRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// VerifyWebhookSignature validates the X-Signature-V2 HMAC.
func (c *Client) VerifyWebhookSignature(body []byte, signatureHeader, timestampHeader string) error {
	if c.config.WebhookSecret == "" {
		return fmt.Errorf("didit webhook secret is not configured")
	}
	if signatureHeader == "" {
		return fmt.Errorf("missing didit webhook signature")
	}

	ts, err := strconv.ParseInt(strings.TrimSpace(timestampHeader), 10, 64)
	if err != nil {
		return fmt.Errorf("invalid didit webhook timestamp")
	}
	if abs(time.Now().Unix()-ts) > 300 {
		return fmt.Errorf("didit webhook timestamp too old")
	}

	// X-Signature-V2: HMAC of sorted, unescaped-unicode JSON
	var parsed interface{}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("failed to parse webhook body: %w", err)
	}
	processed := shortenFloats(parsed)
	canonical := sortedJSON(processed)

	mac := hmac.New(sha256.New, []byte(c.config.WebhookSecret))
	mac.Write([]byte(canonical))
	expected := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(expected), []byte(signatureHeader)) {
		return fmt.Errorf("didit webhook signature mismatch")
	}
	return nil
}

// --- internal helpers ---

func (c *Client) doRequest(ctx context.Context, method, path string, body any, out any) error {
	var payload io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal didit request: %w", err)
		}
		payload = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, payload)
	if err != nil {
		return fmt.Errorf("failed to create didit request: %w", err)
	}
	req.Header.Set("x-api-key", c.config.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("didit request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read didit response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("didit API error (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("failed to decode didit response: %w", err)
		}
	}
	return nil
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// shortenFloats converts whole-number floats to ints to match Didit's server-side behavior.
func shortenFloats(v interface{}) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(val))
		for k, v2 := range val {
			out[k] = shortenFloats(v2)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(val))
		for i, v2 := range val {
			out[i] = shortenFloats(v2)
		}
		return out
	case float64:
		if val == math.Trunc(val) {
			return int64(val)
		}
		return val
	default:
		return val
	}
}

// sortedJSON produces compact JSON with sorted keys (no unicode escaping).
// sortedJSON produces compact JSON with sorted keys and unescaped Unicode.
// Didit X-Signature-V2 signs JSON with ensure_ascii=False (unicode preserved).
func sortedJSON(v interface{}) string {
	switch val := v.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			// Keys are always ASCII in practice; use Marshal for safety.
			kb, _ := json.Marshal(k)
			b.Write(kb)
			b.WriteByte(':')
			b.WriteString(sortedJSON(val[k]))
		}
		b.WriteByte('}')
		return b.String()
	case []interface{}:
		var b strings.Builder
		b.WriteByte('[')
		for i, item := range val {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(sortedJSON(item))
		}
		b.WriteByte(']')
		return b.String()
	case string:
		// Use unescaped unicode — equivalent to Python's ensure_ascii=False.
		var b strings.Builder
		b.WriteByte('"')
		for _, r := range val {
			switch r {
			case '"':
				b.WriteString(`\"`)
			case '\\':
				b.WriteString(`\\`)
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			case '\t':
				b.WriteString(`\t`)
			default:
				if r < 0x20 {
					fmt.Fprintf(&b, `\u%04x`, r)
				} else {
					b.WriteRune(r) // preserve unicode as-is
				}
			}
		}
		b.WriteByte('"')
		return b.String()
	default:
		data, _ := json.Marshal(val)
		return string(data)
	}
}

// DeleteSession permanently deletes a verification session and all associated data (GDPR).
func (c *Client) DeleteSession(ctx context.Context, sessionID string) error {
	url := fmt.Sprintf("%s/v3/session/%s/delete/", baseURL, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("x-api-key", c.config.APIKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("delete session request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
		return nil
	}
	if resp.StatusCode == http.StatusNotFound {
		c.logger.Warn("Didit session not found for deletion", zap.String("session_id", sessionID))
		return nil // Already gone
	}
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("delete session failed (%d): %s", resp.StatusCode, string(body))
}
