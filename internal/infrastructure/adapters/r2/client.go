package r2

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

type Config struct {
	AccountID string
	AccessKey string
	SecretKey string
	Bucket    string
	PublicURL string
}

type Client struct {
	httpClient *http.Client
	config     Config
	publicURL  string
	logger     *zap.Logger
}

type UploadResult struct {
	URL        string
	ObjectKey  string
	ETag       string
	UploadedAt time.Time
}

func NewClient(cfg Config, logger *zap.Logger) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 60 * time.Second},
		config:     cfg,
		publicURL:  strings.TrimRight(cfg.PublicURL, "/"),
		logger:     logger,
	}
}

func (c *Client) UploadKYCDocument(ctx context.Context, userID, documentType string, data []byte, contentType string) (*UploadResult, error) {
	if c == nil {
		return nil, fmt.Errorf("R2 client is not configured")
	}

	objectKey := fmt.Sprintf("kyc/%s/%s/%d", userID, documentType, time.Now().Unix())

	url := fmt.Sprintf("https://%s.r2.cloudflarestorage.com/%s/%s", c.config.AccountID, c.config.Bucket, objectKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create R2 request: %w", err)
	}

	req.Header.Set("Content-Type", contentType)
	req.Header.Set("x-amz-acl", "private")
	c.signRequest(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("R2 upload failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("R2 API error (%d): %s", resp.StatusCode, string(body))
	}

	etag := resp.Header.Get("ETag")

	return &UploadResult{
		URL:        fmt.Sprintf("%s/%s", c.publicURL, objectKey),
		ObjectKey:  objectKey,
		ETag:       etag,
		UploadedAt: time.Now(),
	}, nil
}

func (c *Client) GetKYCDocument(ctx context.Context, objectKey string) ([]byte, error) {
	if c == nil {
		return nil, fmt.Errorf("R2 client is not configured")
	}

	url := fmt.Sprintf("https://%s.r2.cloudflarestorage.com/%s/%s", c.config.AccountID, c.config.Bucket, objectKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create R2 request: %w", err)
	}

	c.signRequest(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("R2 download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("document not found: %s", objectKey)
	}

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("R2 API error (%d): %s", resp.StatusCode, string(body))
	}

	return io.ReadAll(resp.Body)
}

func (c *Client) DeleteKYCDocument(ctx context.Context, objectKey string) error {
	if c == nil {
		return fmt.Errorf("R2 client is not configured")
	}

	url := fmt.Sprintf("https://%s.r2.cloudflarestorage.com/%s/%s", c.config.AccountID, c.config.Bucket, objectKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create R2 request: %w", err)
	}

	c.signRequest(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("R2 delete failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 && resp.StatusCode != 404 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("R2 API error (%d): %s", resp.StatusCode, string(body))
	}

	return nil
}

func (c *Client) GetSignedURL(ctx context.Context, objectKey string, expiryMinutes int) (string, error) {
	if c == nil {
		return "", fmt.Errorf("R2 client is not configured")
	}

	if expiryMinutes <= 0 {
		expiryMinutes = 60
	}

	url := fmt.Sprintf("%s/%s?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=%s%%2F%s%%2Fr2%%2Faws4_request&X-Amz-Date=%s&X-Amz-Expires=%d&X-Amz-SignedHeaders=host",
		c.publicURL,
		objectKey,
		c.config.AccessKey,
		time.Now().Format("20060102"),
		time.Now().Format("20060102T150405Z"),
		expiryMinutes*60)

	stringToSign := fmt.Sprintf("AWS4-HMAC-SHA256\n%s\n%s/r2/aws4_request\nUNSIGNED-PAYLOAD",
		time.Now().Format("20060102T150405Z"),
		time.Now().Format("20060102"))

	signature := c.signString(stringToSign)

	url += fmt.Sprintf("&X-Amz-Signature=%s", signature)

	return url, nil
}

func (c *Client) signRequest(req *http.Request) {
	timestamp := time.Now().UTC().Format("20060102T150405Z")

	req.Header.Set("Host", req.URL.Host)
	req.Header.Set("x-amz-date", timestamp)
	req.Header.Set("x-amz-content-sha256", "UNSIGNED-PAYLOAD")

	canonicalHeaders := fmt.Sprintf("host:%s\nx-amz-content-sha256:UNSIGNED-PAYLOAD\nx-amz-date:%s\n", req.URL.Host, timestamp)
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"

	canonicalRequest := fmt.Sprintf("%s\n%s\n\n%s\n%s\nUNSIGNED-PAYLOAD",
		req.Method,
		req.URL.Path,
		canonicalHeaders,
		signedHeaders)

	stringToSign := fmt.Sprintf("AWS4-HMAC-SHA256\n%s\n%s/r2/aws4_request\n%s",
		timestamp,
		time.Now().Format("20060102"),
		c.sha256Hash([]byte(canonicalRequest)))

	signature := c.signString(stringToSign)

	authHeader := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s/r2/aws4_request, SignedHeaders=%s, Signature=%s",
		c.config.AccessKey, time.Now().Format("20060102"), signedHeaders, signature)

	req.Header.Set("Authorization", authHeader)
}

func (c *Client) signString(stringToSign string) string {
	kDate := hmacSHA256([]byte("AWS4"+c.config.SecretKey), []byte(time.Now().UTC().Format("20060102")))
	kRegion := hmacSHA256(kDate, []byte("auto"))
	kService := hmacSHA256(kRegion, []byte("r2"))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))

	return hex.EncodeToString(hmacSHA256(kSigning, []byte(stringToSign)))
}

func (c *Client) sha256Hash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}
