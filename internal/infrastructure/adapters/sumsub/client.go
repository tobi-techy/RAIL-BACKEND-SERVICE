package sumsub

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
)

const (
	defaultTimeout = 30 * time.Second
)

type Config struct {
	BaseURL       string
	AppToken      string
	SecretKey     string
	WebhookSecret string
	LevelName     string
	UserAgent     string
	Timeout       time.Duration
}

type Client struct {
	httpClient *http.Client
	config     Config
	logger     *zap.Logger
}

type CreateApplicantRequest struct {
	ExternalUserID string              `json:"externalUserId"`
	Email          string              `json:"email,omitempty"`
	Phone          string              `json:"phone,omitempty"`
	FixedInfo      *ApplicantFixedInfo `json:"fixedInfo,omitempty"`
}

type ApplicantFixedInfo struct {
	FirstName string `json:"firstName,omitempty"`
	LastName  string `json:"lastName,omitempty"`
	DOB       string `json:"dob,omitempty"`
	Country   string `json:"country,omitempty"`
}

type ApplicantResponse struct {
	ID             string `json:"id"`
	InspectionID   string `json:"inspectionId"`
	ExternalUserID string `json:"externalUserId"`
	LevelName      string `json:"levelName"`
}

type AccessTokenResponse struct {
	Token string `json:"token"`
}

type ApplicantDataResponse struct {
	ID             string              `json:"id"`
	InspectionID   string              `json:"inspectionId"`
	ExternalUserID string              `json:"externalUserId"`
	LevelName      string              `json:"levelName"`
	ReviewStatus   string              `json:"reviewStatus"`
	FixedInfo      *ApplicantFixedInfo `json:"fixedInfo,omitempty"`
	Info           *ApplicantInfo      `json:"info,omitempty"`
}

type ApplicantInfo struct {
	FirstName string           `json:"firstName,omitempty"`
	LastName  string           `json:"lastName,omitempty"`
	DOB       string           `json:"dob,omitempty"`
	Country   string           `json:"country,omitempty"`
	TaxID     string           `json:"taxId,omitempty"`
	IDDocs    []ApplicantIDDoc `json:"idDocs,omitempty"`
}

type ApplicantIDDoc struct {
	IDDocType string `json:"idDocType,omitempty"`
	Country   string `json:"country,omitempty"`
	Number    string `json:"number,omitempty"`
}

func NewClient(cfg Config, logger *zap.Logger) *Client {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = "https://api.sumsub.com"
	}

	return &Client{
		httpClient: &http.Client{Timeout: timeout},
		config: Config{
			BaseURL:       strings.TrimRight(baseURL, "/"),
			AppToken:      strings.TrimSpace(cfg.AppToken),
			SecretKey:     strings.TrimSpace(cfg.SecretKey),
			WebhookSecret: strings.TrimSpace(cfg.WebhookSecret),
			LevelName:     strings.TrimSpace(cfg.LevelName),
			UserAgent:     strings.TrimSpace(cfg.UserAgent),
			Timeout:       timeout,
		},
		logger: logger,
	}
}

func (c *Client) CreateApplicant(ctx context.Context, req *CreateApplicantRequest, levelName string) (*ApplicantResponse, error) {
	if c == nil {
		return nil, fmt.Errorf("sumsub client is not configured")
	}

	query := url.Values{}
	if ln := strings.TrimSpace(levelName); ln != "" {
		query.Set("levelName", ln)
	}

	path := "/resources/applicants"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}

	var resp ApplicantResponse
	if err := c.doSignedJSONRequest(ctx, http.MethodPost, path, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) CreateAccessToken(ctx context.Context, applicantID, externalUserID, levelName string, ttlSeconds int) (*AccessTokenResponse, error) {
	if c == nil {
		return nil, fmt.Errorf("sumsub client is not configured")
	}

	if ttlSeconds <= 0 {
		ttlSeconds = 3600
	}

	payload := map[string]any{
		"userId":      externalUserID,
		"applicantId": applicantID,
		"ttlInSecs":   ttlSeconds,
	}
	if ln := strings.TrimSpace(levelName); ln != "" {
		payload["levelName"] = ln
	}

	var resp AccessTokenResponse
	if err := c.doSignedJSONRequest(ctx, http.MethodPost, "/resources/accessTokens/sdk", payload, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) GetApplicantData(ctx context.Context, applicantID string) (*ApplicantDataResponse, error) {
	if c == nil {
		return nil, fmt.Errorf("sumsub client is not configured")
	}

	applicantID = strings.TrimSpace(applicantID)
	if applicantID == "" {
		return nil, fmt.Errorf("sumsub applicant ID is required")
	}

	path := fmt.Sprintf("/resources/applicants/%s/one", url.PathEscape(applicantID))
	var resp ApplicantDataResponse
	if err := c.doSignedJSONRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) VerifyWebhookSignature(body []byte, digestHeader, digestAlgHeader string) error {
	if c == nil {
		return fmt.Errorf("sumsub client is not configured")
	}

	if c.config.WebhookSecret == "" {
		return fmt.Errorf("sumsub webhook secret is not configured")
	}

	got := normalizeDigest(digestHeader)
	if got == "" {
		return fmt.Errorf("missing sumsub payload digest")
	}

	hasherFactory, err := resolveHashAlgorithm(digestAlgHeader)
	if err != nil {
		return err
	}

	mac := hmac.New(hasherFactory, []byte(c.config.WebhookSecret))
	_, _ = mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))

	expectedBytes, errExpected := hex.DecodeString(expected)
	gotBytes, errGot := hex.DecodeString(got)
	if errExpected == nil && errGot == nil {
		if !hmac.Equal(expectedBytes, gotBytes) {
			return fmt.Errorf("sumsub webhook signature mismatch")
		}
		return nil
	}

	if !strings.EqualFold(expected, got) {
		return fmt.Errorf("sumsub webhook signature mismatch")
	}
	return nil
}

func (c *Client) doSignedJSONRequest(ctx context.Context, method, path string, body any, out any) error {
	if c == nil {
		return fmt.Errorf("sumsub client is not configured")
	}
	if c.httpClient == nil {
		return fmt.Errorf("sumsub HTTP client is not configured")
	}
	if c.config.AppToken == "" || c.config.SecretKey == "" {
		return fmt.Errorf("sumsub credentials are not configured")
	}

	var payload []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal sumsub request: %w", err)
		}
		payload = encoded
	}

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signaturePayload := timestamp + strings.ToUpper(method) + path + string(payload)
	mac := hmac.New(sha256.New, []byte(c.config.SecretKey))
	_, _ = mac.Write([]byte(signaturePayload))
	signature := hex.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequestWithContext(ctx, method, c.config.BaseURL+path, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("failed to create sumsub request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-App-Token", c.config.AppToken)
	req.Header.Set("X-App-Access-Ts", timestamp)
	req.Header.Set("X-App-Access-Sig", signature)
	if c.config.UserAgent != "" {
		req.Header.Set("User-Agent", c.config.UserAgent)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sumsub request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read sumsub response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if c.logger != nil {
			c.logger.Warn("Sumsub API returned non-success response",
				zap.Int("status_code", resp.StatusCode),
				zap.String("path", path),
				zap.String("response", string(respBody)))
		}
		return fmt.Errorf("sumsub API error (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	if out == nil || len(respBody) == 0 {
		return nil
	}

	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("failed to decode sumsub response: %w", err)
	}
	return nil
}

func resolveHashAlgorithm(raw string) (func() hash.Hash, error) {
	algorithm := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case algorithm == "", strings.Contains(algorithm, "sha256"):
		return sha256.New, nil
	case strings.Contains(algorithm, "sha512"):
		return sha512.New, nil
	default:
		return nil, fmt.Errorf("unsupported sumsub webhook digest algorithm: %s", raw)
	}
}

func normalizeDigest(digest string) string {
	d := strings.TrimSpace(digest)
	if d == "" {
		return ""
	}
	if parts := strings.SplitN(d, "=", 2); len(parts) == 2 {
		d = parts[1]
	}
	return strings.ToLower(strings.TrimSpace(d))
}
