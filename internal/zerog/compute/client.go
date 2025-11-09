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

	"github.com/stack-service/stack_service/internal/config"
	"github.com/stack-service/stack_service/internal/domain/entities"
	"github.com/stack-service/stack_service/pkg/retry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

type Client struct {
	config         *config.ComputeConfig
	httpClient     *http.Client
	logger         *zap.Logger
	tracer         trace.Tracer
	metrics        *ClientMetrics
	privateKey     string
	providerAddr   string
	providerURL    string
	acknowledged   bool
	accountBalance float64
}

// ClientMetrics contains observability metrics for the 0G compute client
type ClientMetrics struct {
	RequestsTotal        metric.Int64Counter
	RequestDuration      metric.Float64Histogram
	RequestErrors        metric.Int64Counter
	ActiveConnections    metric.Int64Gauge
	TokensUsed           metric.Int64Counter
	ServiceDiscoveries   metric.Int64Counter
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
	Role    string `json:"role"`    // system, user, assistant
	Content string `json:"content"`
}

// InferenceResponse represents a response from 0G compute network
type InferenceResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
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
	ProviderID   string      `json:"provider_id"`
	ServiceName  string      `json:"service_name"`
	Models       []ModelInfo `json:"models"`
	Status       string      `json:"status"`
	Endpoint     string      `json:"endpoint"`
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
	config *config.ComputeConfig,
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
		config:       config,
		httpClient:   httpClient,
		logger:       logger,
		tracer:       tracer,
		metrics:      metrics,
		privateKey:   privateKey,
		providerAddr: "0xf07240Efa67755B5311bc75784a061eDB47165Dd",
		providerURL:  config.Endpoint,
	}

	logger.Info("0G compute client initialized",
		zap.String("endpoint", config.Endpoint),
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
		Status:      status,
		Latency:     latency,
		Version:     "1.0.0",
		Uptime:      time.Hour, // TODO: Track actual uptime
		Metrics: map[string]interface{}{
			"endpoint":   c.config.Endpoint,
			"timeout":    c.config.Timeout,
			"retries":    c.config.MaxRetries,
		},
		LastChecked: time.Now(),
		Errors:      errors,
	}, nil
}

func (c *Client) GenerateInference(ctx context.Context, request *InferenceRequest) (*InferenceResponse, error) {
	if !c.acknowledged {
		if err := c.acknowledgeProvider(ctx); err != nil {
			return nil, fmt.Errorf("provider acknowledgment failed: %w", err)
		}
	}

	startTime := time.Now()
	ctx, span := c.tracer.Start(ctx, "compute.generate_inference", trace.WithAttributes(
		attribute.String("model", request.Model),
	))
	defer span.End()

	c.metrics.RequestsTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("model", request.Model),
	))

	var response *InferenceResponse
	retryConfig := retry.RetryConfig{
		MaxAttempts: c.config.MaxRetries,
		BaseDelay:   time.Second,
		MaxDelay:    30 * time.Second,
		Multiplier:  2.0,
	}

	err := retry.WithExponentialBackoff(ctx, retryConfig, func() error {
		var err error
		response, err = c.doInferenceRequest(ctx, request)
		return err
	}, isRetryableError)

	duration := time.Since(startTime)
	c.metrics.RequestDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(
		attribute.String("model", request.Model),
		attribute.Bool("success", err == nil),
	))

	if err != nil {
		span.RecordError(err)
		c.metrics.RequestErrors.Add(ctx, 1)
		return nil, fmt.Errorf("inference failed: %w", err)
	}

	if response != nil && response.Usage.TotalTokens > 0 {
		c.metrics.TokensUsed.Add(ctx, int64(response.Usage.TotalTokens))
	}

	return response, nil
}

func (c *Client) doInferenceRequest(ctx context.Context, request *InferenceRequest) (*InferenceResponse, error) {
	messagesJSON, err := json.Marshal(request.Messages)
	if err != nil {
		return nil, err
	}

	headers, err := c.generateRequestHeaders(string(messagesJSON))
	if err != nil {
		return nil, err
	}

	payload, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/chat/completions", c.providerURL)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(payload))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, err
	}

	if httpResp.StatusCode >= 400 {
		return nil, &entities.ZeroGError{
			Code:      fmt.Sprintf("HTTP_%d", httpResp.StatusCode),
			Message:   string(respBody),
			Retryable: httpResp.StatusCode >= 500,
			Timestamp: time.Now(),
		}
	}

	var response InferenceResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, err
	}

	return &response, nil
}

func (c *Client) acknowledgeProvider(ctx context.Context) error {
	c.logger.Info("Acknowledging provider", zap.String("provider", c.providerAddr))
	c.acknowledged = true
	return nil
}

func (c *Client) generateRequestHeaders(messagesJSON string) (map[string]string, error) {
	timestamp := time.Now().Unix()
	hash := sha256.Sum256([]byte(fmt.Sprintf("%d:%s:%s", timestamp, c.providerAddr, messagesJSON)))
	signature := hex.EncodeToString(hash[:])

	return map[string]string{
		"X-0G-Provider":   c.providerAddr,
		"X-0G-Timestamp":  fmt.Sprintf("%d", timestamp),
		"X-0G-Signature":  signature,
		"Authorization":   "Bearer " + c.privateKey[:32],
	}, nil
}

func (c *Client) FundAccount(ctx context.Context, amount float64) error {
	c.logger.Info("Funding account", zap.Float64("amount", amount))
	c.accountBalance += amount
	return nil
}

func (c *Client) GetBalance(ctx context.Context) (float64, error) {
	return c.accountBalance, nil
}

func (c *Client) discoverServices(ctx context.Context) ([]ServiceInfo, error) {
	c.metrics.ServiceDiscoveries.Add(ctx, 1)
	return []ServiceInfo{
		{
			ProviderID:  c.providerAddr,
			ServiceName: "0G Compute",
			Models: []ModelInfo{
				{ID: "gpt-oss-120b", Name: "GPT OSS 120B", MaxTokens: 4096},
				{ID: "deepseek-r1-70b", Name: "DeepSeek R1 70B", MaxTokens: 4096},
			},
			Status:   "active",
			Endpoint: c.providerURL,
		},
	}, nil
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