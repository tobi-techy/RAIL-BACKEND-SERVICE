package compute

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/stack-service/stack_service/internal/domain/entities"
	infconfig "github.com/stack-service/stack_service/internal/infrastructure/config"
	"github.com/stack-service/stack_service/pkg/retry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// Client handles communication with 0G compute/serving-broker network
type Client struct {
	config     *infconfig.ZeroGComputeConfig
	httpClient *http.Client
	logger     *zap.Logger
	tracer     trace.Tracer
	metrics    *ClientMetrics

	// Authentication and configuration
	privateKey  string
	brokerID    string
	providerURL string
}

// ClientMetrics contains observability metrics for the 0G compute client
type ClientMetrics struct {
	RequestsTotal      metric.Int64Counter
	RequestDuration    metric.Float64Histogram
	RequestErrors      metric.Int64Counter
	ActiveConnections  metric.Int64Gauge
	TokensUsed         metric.Int64Counter
	ServiceDiscoveries metric.Int64Counter
}

// InferenceRequest represents a request to 0G compute network
type InferenceRequest struct {
	Model       string                 `json:"model"`
	Messages    []ChatMessage          `json:"messages"`
	MaxTokens   int                    `json:"max_tokens,omitempty"`
	Temperature float32                `json:"temperature,omitempty"`
	Stream      bool                   `json:"stream,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// ChatMessage represents a message in the conversation
type ChatMessage struct {
	Role    string `json:"role"` // system, user, assistant
	Content string `json:"content"`
}

// InferenceResponse represents a response from 0G compute network
type InferenceResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

// Choice represents a completion choice
type Choice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

// Usage represents token usage information
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ServiceInfo represents information about available services
type ServiceInfo struct {
	ProviderID  string      `json:"provider_id"`
	ServiceName string      `json:"service_name"`
	Models      []ModelInfo `json:"models"`
	Status      string      `json:"status"`
	Endpoint    string      `json:"endpoint"`
}

// ModelInfo represents information about an AI model
type ModelInfo struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	MaxTokens   int     `json:"max_tokens"`
	InputPrice  float64 `json:"input_price"`
	OutputPrice float64 `json:"output_price"`
}

// NewClient creates a new 0G compute client
func NewClient(
	config *infconfig.ZeroGComputeConfig,
	privateKey string,
	logger *zap.Logger,
) (*Client, error) {
	if config == nil {
		return nil, fmt.Errorf("compute config is required")
	}

	if privateKey == "" {
		return nil, fmt.Errorf("private key is required for 0G authentication")
	}

	httpClient := &http.Client{
		Timeout: config.Timeout,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxConnsPerHost:     10,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}

	tracer := otel.Tracer("0g-compute-client")
	meter := otel.Meter("0g-compute-client")

	// Initialize metrics
	metrics, err := initClientMetrics(meter)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize metrics: %w", err)
	}

	client := &Client{
		config:      config,
		httpClient:  httpClient,
		logger:      logger,
		tracer:      tracer,
		metrics:     metrics,
		privateKey:  privateKey,
		providerURL: config.BrokerEndpoint,
	}

	logger.Info("0G compute client initialized",
		zap.String("endpoint", config.BrokerEndpoint),
		zap.Duration("timeout", config.Timeout),
		zap.Int("max_retries", config.MaxRetries),
	)

	return client, nil
}

// HealthCheck verifies connectivity to the 0G compute network
func (c *Client) HealthCheck(ctx context.Context) (*entities.HealthStatus, error) {
	ctx, span := c.tracer.Start(ctx, "compute.health_check")
	defer span.End()

	startTime := time.Now()

	// Try to discover services as a health check
	_, err := c.discoverServices(ctx)
	latency := time.Since(startTime)

	status := entities.HealthStatusHealthy
	var errors []string

	if err != nil {
		status = entities.HealthStatusUnhealthy
		errors = append(errors, err.Error())
		span.RecordError(err)
	}

	return &entities.HealthStatus{
		Status:  status,
		Latency: latency,
		Version: "1.0.0",
		Uptime:  time.Hour, // TODO: Track actual uptime
		Metrics: map[string]interface{}{
			"endpoint": c.config.BrokerEndpoint,
			"timeout":  c.config.Timeout,
			"retries":  c.config.MaxRetries,
		},
		LastChecked: time.Now(),
		Errors:      errors,
	}, nil
}

// GenerateInference performs AI inference using the 0G compute network
func (c *Client) GenerateInference(ctx context.Context, request *InferenceRequest) (*InferenceResponse, error) {
	startTime := time.Now()
	ctx, span := c.tracer.Start(ctx, "compute.generate_inference", trace.WithAttributes(
		attribute.String("model", request.Model),
		attribute.Int("max_tokens", request.MaxTokens),
		attribute.Bool("stream", request.Stream),
	))
	defer span.End()

	c.logger.Debug("Generating inference",
		zap.String("model", request.Model),
		zap.Int("max_tokens", request.MaxTokens),
		zap.Int("messages_count", len(request.Messages)),
	)

	// Increment request counter
	c.metrics.RequestsTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("model", request.Model),
		attribute.String("operation", "inference"),
	))

	var lastErr error
	var response *InferenceResponse

	// Retry logic with exponential backoff
	retryConfig := retry.RetryConfig{
		MaxAttempts: c.config.MaxRetries,
		BaseDelay:   time.Second,
		MaxDelay:    30 * time.Second,
		Multiplier:  2.0,
	}

	err := retry.WithExponentialBackoff(ctx, retryConfig, func() error {
		var err error
		response, err = c.doInferenceRequest(ctx, request)
		if err != nil {
			lastErr = err
		}
		return err
	}, isRetryableError)

	duration := time.Since(startTime)
	c.metrics.RequestDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(
		attribute.String("model", request.Model),
		attribute.String("operation", "inference"),
		attribute.Bool("success", err == nil),
	))

	if err != nil {
		span.RecordError(lastErr)
		c.metrics.RequestErrors.Add(ctx, 1, metric.WithAttributes(
			attribute.String("model", request.Model),
			attribute.String("error_type", classifyError(lastErr)),
		))
		return nil, fmt.Errorf("inference request failed after retries: %w", lastErr)
	}

	// Record token usage
	if response != nil && response.Usage.TotalTokens > 0 {
		c.metrics.TokensUsed.Add(ctx, int64(response.Usage.TotalTokens), metric.WithAttributes(
			attribute.String("model", request.Model),
		))
	}

	c.logger.Debug("Inference completed successfully",
		zap.String("model", request.Model),
		zap.Int("total_tokens", response.Usage.TotalTokens),
		zap.Duration("duration", duration),
	)

	return response, nil
}

// doInferenceRequest performs the actual HTTP request to the 0G compute network
func (c *Client) doInferenceRequest(ctx context.Context, request *InferenceRequest) (*InferenceResponse, error) {
	// Prepare request payload
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	url := fmt.Sprintf("%s/v1/chat/completions", c.providerURL)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set headers
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	// Add authentication headers
	if err := c.addAuthHeaders(httpReq, payload); err != nil {
		return nil, fmt.Errorf("failed to add authentication headers: %w", err)
	}

	// Execute HTTP request
	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer httpResp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Handle HTTP errors
	if httpResp.StatusCode >= 400 {
		var errorResp map[string]interface{}
		if err := json.Unmarshal(respBody, &errorResp); err == nil {
			if errorMsg, ok := errorResp["error"].(string); ok {
				return nil, &entities.ZeroGError{
					Code:      fmt.Sprintf("HTTP_%d", httpResp.StatusCode),
					Message:   errorMsg,
					Details:   errorResp,
					Retryable: httpResp.StatusCode >= 500,
					Timestamp: time.Now(),
				}
			}
		}
		return nil, &entities.ZeroGError{
			Code:      fmt.Sprintf("HTTP_%d", httpResp.StatusCode),
			Message:   fmt.Sprintf("HTTP error: %d %s", httpResp.StatusCode, httpResp.Status),
			Details:   map[string]interface{}{"response_body": string(respBody)},
			Retryable: httpResp.StatusCode >= 500,
			Timestamp: time.Now(),
		}
	}

	// Parse successful response
	var response InferenceResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &response, nil
}

// addAuthHeaders adds authentication headers for 0G network requests
func (c *Client) addAuthHeaders(req *http.Request, payload []byte) error {
	// Generate timestamp
	timestamp := time.Now().Unix()

	// Create signature payload (method + url + timestamp + body_hash)
	bodyHash := sha256.Sum256(payload)
	signaturePayload := fmt.Sprintf("%s\n%s\n%d\n%s",
		req.Method,
		req.URL.Path,
		timestamp,
		hex.EncodeToString(bodyHash[:]))

	// Sign the payload (simplified signature - in production, use proper cryptographic signing)
	signature := c.generateSignature(signaturePayload)

	// Add authentication headers
	req.Header.Set("X-0G-Timestamp", fmt.Sprintf("%d", timestamp))
	req.Header.Set("X-0G-Signature", signature)
	req.Header.Set("X-0G-Auth-Key", c.derivePublicKey())

	return nil
}

// generateSignature creates a signature for the request (simplified implementation)
func (c *Client) generateSignature(payload string) string {
	// In production, this should use proper cryptographic signing with the private key
	// This is a simplified implementation for demonstration
	hash := sha256.Sum256([]byte(payload + c.privateKey))
	return hex.EncodeToString(hash[:])
}

// derivePublicKey derives a public key identifier from the private key (simplified)
func (c *Client) derivePublicKey() string {
	// In production, derive the actual public key from the private key
	hash := sha256.Sum256([]byte(c.privateKey))
	return hex.EncodeToString(hash[:16]) // Use first 16 bytes as identifier
}

// discoverServices discovers available inference services
func (c *Client) discoverServices(ctx context.Context) ([]ServiceInfo, error) {
	ctx, span := c.tracer.Start(ctx, "compute.discover_services")
	defer span.End()

	c.metrics.ServiceDiscoveries.Add(ctx, 1)

	// Create discovery request
	url := fmt.Sprintf("%s/v1/services", c.providerURL)
	httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create discovery request: %w", err)
	}

	// Add authentication headers (empty payload for GET)
	if err := c.addAuthHeaders(httpReq, []byte{}); err != nil {
		return nil, fmt.Errorf("failed to add auth headers: %w", err)
	}

	// Execute request
	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("service discovery request failed: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != 200 {
		return nil, fmt.Errorf("service discovery failed with status: %d", httpResp.StatusCode)
	}

	// Parse response
	var services []ServiceInfo
	if err := json.NewDecoder(httpResp.Body).Decode(&services); err != nil {
		return nil, fmt.Errorf("failed to decode service discovery response: %w", err)
	}

	c.logger.Debug("Discovered services",
		zap.Int("service_count", len(services)),
	)

	return services, nil
}

// GetAvailableModels returns a list of available models
func (c *Client) GetAvailableModels(ctx context.Context) ([]ModelInfo, error) {
	services, err := c.discoverServices(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to discover services: %w", err)
	}

	var allModels []ModelInfo
	for _, service := range services {
		if service.Status == "active" {
			allModels = append(allModels, service.Models...)
		}
	}

	return allModels, nil
}

// isRetryableError determines if an error should trigger a retry
func isRetryableError(err error) bool {
	if zeroGErr, ok := err.(*entities.ZeroGError); ok {
		return zeroGErr.Retryable
	}

	// Network errors are generally retryable
	return true
}

// classifyError classifies errors for metrics
func classifyError(err error) string {
	if zeroGErr, ok := err.(*entities.ZeroGError); ok {
		return zeroGErr.Code
	}
	return "unknown_error"
}

// initClientMetrics initializes OpenTelemetry metrics for the client
func initClientMetrics(meter metric.Meter) (*ClientMetrics, error) {
	requestsTotal, err := meter.Int64Counter("zerog_compute_requests_total",
		metric.WithDescription("Total number of requests to 0G compute network"))
	if err != nil {
		return nil, err
	}

	requestDuration, err := meter.Float64Histogram("zerog_compute_request_duration_seconds",
		metric.WithDescription("Duration of requests to 0G compute network"))
	if err != nil {
		return nil, err
	}

	requestErrors, err := meter.Int64Counter("zerog_compute_request_errors_total",
		metric.WithDescription("Total number of request errors"))
	if err != nil {
		return nil, err
	}

	activeConnections, err := meter.Int64Gauge("zerog_compute_active_connections",
		metric.WithDescription("Number of active connections to 0G compute network"))
	if err != nil {
		return nil, err
	}

	tokensUsed, err := meter.Int64Counter("zerog_compute_tokens_used_total",
		metric.WithDescription("Total number of tokens used"))
	if err != nil {
		return nil, err
	}

	serviceDiscoveries, err := meter.Int64Counter("zerog_compute_service_discoveries_total",
		metric.WithDescription("Total number of service discoveries"))
	if err != nil {
		return nil, err
	}

	return &ClientMetrics{
		RequestsTotal:      requestsTotal,
		RequestDuration:    requestDuration,
		RequestErrors:      requestErrors,
		ActiveConnections:  activeConnections,
		TokensUsed:         tokensUsed,
		ServiceDiscoveries: serviceDiscoveries,
	}, nil
}
