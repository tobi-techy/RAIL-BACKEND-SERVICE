package zerog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/stack-service/stack_service/internal/domain/entities"
	"github.com/stack-service/stack_service/internal/infrastructure/config"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// InferenceGateway implements the ZeroGInferenceGateway interface
type InferenceGateway struct {
	config        *config.ZeroGComputeConfig
	storageClient entities.ZeroGStorageClient
	logger        *zap.Logger
	tracer        trace.Tracer
	metrics       *InferenceMetrics
	httpClient    *http.Client
}

// InferenceMetrics contains observability metrics for inference operations
type InferenceMetrics struct {
	RequestsTotal   metric.Int64Counter
	RequestDuration metric.Float64Histogram
	RequestErrors   metric.Int64Counter
	TokensUsed      metric.Int64Counter
	TokensGenerated metric.Int64Counter
	ActiveRequests  metric.Int64UpDownCounter
	ModelUsage      metric.Int64Counter
}

// OpenAICompatibleRequest represents an OpenAI-compatible API request
type OpenAICompatibleRequest struct {
	Model            string        `json:"model"`
	Messages         []ChatMessage `json:"messages"`
	MaxTokens        int           `json:"max_tokens,omitempty"`
	Temperature      float64       `json:"temperature,omitempty"`
	TopP             float64       `json:"top_p,omitempty"`
	FrequencyPenalty float64       `json:"frequency_penalty,omitempty"`
	PresencePenalty  float64       `json:"presence_penalty,omitempty"`
	Stream           bool          `json:"stream,omitempty"`
	User             string        `json:"user,omitempty"`
}

// ChatMessage represents a chat message in the conversation
type ChatMessage struct {
	Role    string `json:"role"`    // system, user, assistant
	Content string `json:"content"` // message content
}

// OpenAICompatibleResponse represents an OpenAI-compatible API response
type OpenAICompatibleResponse struct {
	ID                string   `json:"id"`
	Object            string   `json:"object"`
	Created           int64    `json:"created"`
	Model             string   `json:"model"`
	Choices           []Choice `json:"choices"`
	Usage             *Usage   `json:"usage,omitempty"`
	SystemFingerprint string   `json:"system_fingerprint,omitempty"`
}

// Choice represents a completion choice
type Choice struct {
	Index        int          `json:"index"`
	Message      *ChatMessage `json:"message,omitempty"`
	FinishReason string       `json:"finish_reason,omitempty"`
}

// Usage represents token usage information
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// NewInferenceGateway creates a new 0G inference gateway
func NewInferenceGateway(cfg *config.ZeroGComputeConfig, storageClient entities.ZeroGStorageClient, logger *zap.Logger) (*InferenceGateway, error) {
	if cfg == nil {
		return nil, fmt.Errorf("compute config is required")
	}

	if cfg.BrokerEndpoint == "" {
		return nil, fmt.Errorf("broker endpoint is required")
	}

	if cfg.PrivateKey == "" {
		return nil, fmt.Errorf("private key is required")
	}

	tracer := otel.Tracer("zerog-inference")
	meter := otel.Meter("zerog-inference")

	// Initialize metrics
	metrics, err := initInferenceMetrics(meter)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize metrics: %w", err)
	}

	// Create HTTP client with timeouts
	httpClient := &http.Client{
		Timeout: 60 * time.Second, // TODO: Make configurable
		Transport: &http.Transport{
			IdleConnTimeout:     30 * time.Second,
			DisableKeepAlives:   false,
			MaxIdleConns:        10,
			MaxIdleConnsPerHost: 5,
		},
	}

	gateway := &InferenceGateway{
		config:        cfg,
		storageClient: storageClient,
		logger:        logger,
		tracer:        tracer,
		metrics:       metrics,
		httpClient:    httpClient,
	}

	logger.Info("0G inference gateway initialized",
		zap.String("broker_endpoint", cfg.BrokerEndpoint),
		zap.String("default_model", cfg.ModelConfig.DefaultModel),
	)

	return gateway, nil
}

// GenerateWeeklySummary creates an AI-generated weekly portfolio summary
func (g *InferenceGateway) GenerateWeeklySummary(ctx context.Context, request *entities.WeeklySummaryRequest) (*entities.InferenceResult, error) {
	startTime := time.Now()
	ctx, span := g.tracer.Start(ctx, "inference.generate_weekly_summary", trace.WithAttributes(
		attribute.String("user_id", request.UserID.String()),
		attribute.String("week_start", request.WeekStart.Format("2006-01-02")),
	))
	defer span.End()

	g.metrics.RequestsTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("operation", "weekly_summary"),
		attribute.String("model", g.config.ModelConfig.DefaultModel),
	))
	g.metrics.ActiveRequests.Add(ctx, 1)
	defer g.metrics.ActiveRequests.Add(ctx, -1)

	g.logger.Info("Generating weekly summary",
		zap.String("user_id", request.UserID.String()),
		zap.String("week_start", request.WeekStart.Format("2006-01-02")),
		zap.Float64("total_value", request.PortfolioData.TotalValue),
	)

	// Build the prompt for weekly summary
	prompt, err := g.buildWeeklySummaryPrompt(request)
	if err != nil {
		span.RecordError(err)
		g.recordError(ctx, "weekly_summary", err)
		return nil, fmt.Errorf("failed to build prompt: %w", err)
	}

	// Make inference request
	result, err := g.makeInferenceRequest(ctx, "weekly_summary", prompt, request.UserID.String())
	if err != nil {
		span.RecordError(err)
		g.recordError(ctx, "weekly_summary", err)
		return nil, fmt.Errorf("inference request failed: %w", err)
	}

	// Store detailed analysis as artifact
	if err := g.storeArtifact(ctx, result, request.UserID.String(), "weekly_summary"); err != nil {
		g.logger.Warn("Failed to store artifact", zap.Error(err))
		// Don't fail the entire operation if artifact storage fails
	}

	// Record success metrics
	duration := time.Since(startTime)
	g.metrics.RequestDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(
		attribute.String("operation", "weekly_summary"),
		attribute.Bool("success", true),
	))

	g.logger.Info("Weekly summary generated successfully",
		zap.String("user_id", request.UserID.String()),
		zap.String("request_id", result.RequestID),
		zap.Int("tokens_used", result.TokensUsed),
		zap.Duration("duration", duration),
	)

	return result, nil
}

// AnalyzeOnDemand performs on-demand portfolio analysis
func (g *InferenceGateway) AnalyzeOnDemand(ctx context.Context, request *entities.AnalysisRequest) (*entities.InferenceResult, error) {
	startTime := time.Now()
	ctx, span := g.tracer.Start(ctx, "inference.analyze_on_demand", trace.WithAttributes(
		attribute.String("user_id", request.UserID.String()),
		attribute.String("analysis_type", request.AnalysisType),
	))
	defer span.End()

	g.metrics.RequestsTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("operation", "on_demand_analysis"),
		attribute.String("analysis_type", request.AnalysisType),
		attribute.String("model", g.config.ModelConfig.DefaultModel),
	))
	g.metrics.ActiveRequests.Add(ctx, 1)
	defer g.metrics.ActiveRequests.Add(ctx, -1)

	g.logger.Info("Performing on-demand analysis",
		zap.String("user_id", request.UserID.String()),
		zap.String("analysis_type", request.AnalysisType),
		zap.Float64("total_value", request.PortfolioData.TotalValue),
	)

	// Build the prompt for on-demand analysis
	prompt, err := g.buildAnalysisPrompt(request)
	if err != nil {
		span.RecordError(err)
		g.recordError(ctx, "on_demand_analysis", err)
		return nil, fmt.Errorf("failed to build prompt: %w", err)
	}

	// Make inference request
	result, err := g.makeInferenceRequest(ctx, "on_demand_analysis", prompt, request.UserID.String())
	if err != nil {
		span.RecordError(err)
		g.recordError(ctx, "on_demand_analysis", err)
		return nil, fmt.Errorf("inference request failed: %w", err)
	}

	// Store detailed analysis as artifact
	if err := g.storeArtifact(ctx, result, request.UserID.String(), request.AnalysisType); err != nil {
		g.logger.Warn("Failed to store artifact", zap.Error(err))
		// Don't fail the entire operation if artifact storage fails
	}

	// Record success metrics
	duration := time.Since(startTime)
	g.metrics.RequestDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(
		attribute.String("operation", "on_demand_analysis"),
		attribute.String("analysis_type", request.AnalysisType),
		attribute.Bool("success", true),
	))

	g.logger.Info("On-demand analysis completed successfully",
		zap.String("user_id", request.UserID.String()),
		zap.String("analysis_type", request.AnalysisType),
		zap.String("request_id", result.RequestID),
		zap.Int("tokens_used", result.TokensUsed),
		zap.Duration("duration", duration),
	)

	return result, nil
}

// HealthCheck verifies connectivity to 0G compute network
func (g *InferenceGateway) HealthCheck(ctx context.Context) (*entities.HealthStatus, error) {
	startTime := time.Now()
	ctx, span := g.tracer.Start(ctx, "inference.health_check")
	defer span.End()

	endpoint := strings.TrimSpace(g.config.BrokerEndpoint)
	if endpoint == "" {
		err := fmt.Errorf("broker endpoint not configured")
		span.RecordError(err)
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, endpoint, nil)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to create health check request: %w", err)
	}

	if token := strings.TrimSpace(g.config.PrivateKey); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := g.httpClient.Do(req)
	if err != nil {
		span.RecordError(err)
		g.recordError(ctx, "health_check", err)
		return nil, fmt.Errorf("health check request failed: %w", err)
	}
	defer resp.Body.Close()

	latency := time.Since(startTime)
	status := entities.HealthStatusHealthy
	var errors []string
	if resp.StatusCode >= http.StatusBadRequest {
		status = entities.HealthStatusDegraded
		errors = append(errors, fmt.Sprintf("unexpected status code: %d", resp.StatusCode))
	}

	result := &entities.HealthStatus{
		Status:  status,
		Latency: latency,
		Version: "",
		Uptime:  0,
		Metrics: map[string]interface{}{
			"provider_id": g.config.ProviderID,
			"status_code": resp.StatusCode,
		},
		LastChecked: time.Now(),
		Errors:      errors,
	}

	g.logger.Info("0G compute health check completed",
		zap.String("status", result.Status),
		zap.Duration("latency", result.Latency),
	)

	return result, nil
}

// GetServiceInfo returns information about available inference services
func (g *InferenceGateway) GetServiceInfo(ctx context.Context) (*entities.ServiceInfo, error) {
	ctx, span := g.tracer.Start(ctx, "inference.get_service_info")
	defer span.End()

	return nil, fmt.Errorf("service discovery not implemented for broker endpoint %s", strings.TrimSpace(g.config.BrokerEndpoint))
}

// makeInferenceRequest makes a request to the 0G compute network
func (g *InferenceGateway) makeInferenceRequest(ctx context.Context, operation string, prompt string, userID string) (*entities.InferenceResult, error) {
	requestID := uuid.New().String()

	// Build OpenAI-compatible request
	apiRequest := &OpenAICompatibleRequest{
		Model: g.config.ModelConfig.DefaultModel,
		Messages: []ChatMessage{
			{
				Role:    "system",
				Content: "You are an AI-powered Chief Financial Officer (AI-CFO) providing professional investment analysis and portfolio insights. Provide clear, actionable insights while avoiding specific trading recommendations.",
			},
			{
				Role:    "user",
				Content: prompt,
			},
		},
		MaxTokens:        g.config.ModelConfig.MaxTokens,
		Temperature:      g.config.ModelConfig.Temperature,
		TopP:             g.config.ModelConfig.TopP,
		FrequencyPenalty: g.config.ModelConfig.FrequencyPenalty,
		PresencePenalty:  g.config.ModelConfig.PresencePenalty,
		Stream:           false,
		User:             userID,
	}

	endpoint := strings.TrimSpace(g.config.BrokerEndpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("broker endpoint not configured")
	}

	payload, err := json.Marshal(apiRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal inference request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create inference request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token := strings.TrimSpace(g.config.PrivateKey); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	start := time.Now()
	resp, err := g.httpClient.Do(req)
	if err != nil {
		g.recordError(ctx, operation, err)
		return nil, fmt.Errorf("inference request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		g.recordError(ctx, operation, err)
		return nil, fmt.Errorf("failed to read inference response: %w", err)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		err := fmt.Errorf("inference service returned status %d: %s", resp.StatusCode, string(respBody))
		g.recordError(ctx, operation, err)
		return nil, err
	}

	var apiResponse OpenAICompatibleResponse
	if err := json.Unmarshal(respBody, &apiResponse); err != nil {
		g.recordError(ctx, operation, err)
		return nil, fmt.Errorf("failed to parse inference response: %w", err)
	}

	if len(apiResponse.Choices) == 0 || apiResponse.Choices[0].Message == nil {
		err := fmt.Errorf("inference response missing content")
		g.recordError(ctx, operation, err)
		return nil, err
	}

	content := apiResponse.Choices[0].Message.Content
	if strings.TrimSpace(content) == "" {
		content = "No content returned from inference provider."
	}

	processingTime := time.Since(start)
	createdAt := time.Now()
	if apiResponse.Created > 0 {
		createdAt = time.Unix(apiResponse.Created, 0)
	}

	tokensUsed := 0
	tokensGenerated := 0
	if apiResponse.Usage != nil {
		tokensUsed = apiResponse.Usage.TotalTokens
		tokensGenerated = apiResponse.Usage.CompletionTokens
	}

	result := &entities.InferenceResult{
		RequestID:   requestID,
		Content:     content,
		ContentType: "text/markdown",
		Metadata: map[string]interface{}{
			"operation":     operation,
			"model":         apiResponse.Model,
			"provider":      g.config.ProviderID,
			"response_id":   apiResponse.ID,
			"finish_reason": apiResponse.Choices[0].FinishReason,
		},
		TokensUsed:     tokensUsed,
		ProcessingTime: processingTime,
		Model:          apiResponse.Model,
		CreatedAt:      createdAt,
		ArtifactURI:    "",
	}

	g.metrics.TokensUsed.Add(ctx, int64(tokensUsed))
	g.metrics.TokensGenerated.Add(ctx, int64(tokensGenerated))
	g.metrics.ModelUsage.Add(ctx, 1, metric.WithAttributes(
		attribute.String("model", result.Model),
		attribute.String("operation", operation),
	))

	return result, nil
}

// buildWeeklySummaryPrompt constructs the prompt for weekly summary generation
func (g *InferenceGateway) buildWeeklySummaryPrompt(request *entities.WeeklySummaryRequest) (string, error) {
	var promptBuilder strings.Builder

	promptBuilder.WriteString("# Weekly Portfolio Summary Request\n\n")
	promptBuilder.WriteString(fmt.Sprintf("**Week Period**: %s to %s\n\n",
		request.WeekStart.Format("January 2, 2006"),
		request.WeekEnd.Format("January 2, 2006")))

	// Portfolio performance data
	if request.PortfolioData != nil {
		promptBuilder.WriteString("## Portfolio Performance\n")
		promptBuilder.WriteString(fmt.Sprintf("- **Total Value**: $%.2f\n", request.PortfolioData.TotalValue))
		promptBuilder.WriteString(fmt.Sprintf("- **Week Change**: $%.2f (%.2f%%)\n",
			request.PortfolioData.WeekChange, request.PortfolioData.WeekChangePct))
		promptBuilder.WriteString(fmt.Sprintf("- **Total Return**: $%.2f (%.2f%%)\n",
			request.PortfolioData.TotalReturn, request.PortfolioData.TotalReturnPct))

		// Position details
		if len(request.PortfolioData.Positions) > 0 {
			promptBuilder.WriteString("\n### Position Performance\n")
			for _, pos := range request.PortfolioData.Positions {
				promptBuilder.WriteString(fmt.Sprintf("- **%s**: $%.2f (%.2f%% of portfolio), P&L: $%.2f (%.2f%%)\n",
					pos.BasketName, pos.CurrentValue, pos.Weight*100, pos.UnrealizedPL, pos.UnrealizedPLPct))
			}
		}

		// Risk metrics
		if request.PortfolioData.RiskMetrics != nil {
			promptBuilder.WriteString("\n### Risk Analysis\n")
			promptBuilder.WriteString(fmt.Sprintf("- **Volatility**: %.2f%%\n", request.PortfolioData.RiskMetrics.Volatility*100))
			promptBuilder.WriteString(fmt.Sprintf("- **Max Drawdown**: %.2f%%\n", request.PortfolioData.RiskMetrics.MaxDrawdown*100))
			promptBuilder.WriteString(fmt.Sprintf("- **Diversification Score**: %.2f/1.0\n", request.PortfolioData.RiskMetrics.Diversification))
		}
	}

	// User preferences
	if request.Preferences != nil {
		promptBuilder.WriteString(fmt.Sprintf("\n## User Preferences\n"))
		promptBuilder.WriteString(fmt.Sprintf("- **Risk Tolerance**: %s\n", request.Preferences.RiskTolerance))
		promptBuilder.WriteString(fmt.Sprintf("- **Preferred Style**: %s\n", request.Preferences.PreferredStyle))
		if len(request.Preferences.FocusAreas) > 0 {
			promptBuilder.WriteString(fmt.Sprintf("- **Focus Areas**: %s\n", strings.Join(request.Preferences.FocusAreas, ", ")))
		}
	}

	promptBuilder.WriteString("\n---\n\n")
	promptBuilder.WriteString("Please provide a comprehensive weekly portfolio summary in markdown format that includes:\n\n")
	promptBuilder.WriteString("1. **Executive Summary** - Key highlights and overall performance\n")
	promptBuilder.WriteString("2. **Performance Analysis** - Detailed breakdown of gains/losses and contributing factors\n")
	promptBuilder.WriteString("3. **Risk Assessment** - Current risk exposure and portfolio health\n")
	promptBuilder.WriteString("4. **Market Context** - Relevant market conditions that affected performance\n")
	promptBuilder.WriteString("5. **Key Observations** - Notable trends or changes in the portfolio\n")
	promptBuilder.WriteString("6. **Looking Ahead** - Considerations for the upcoming week\n\n")
	promptBuilder.WriteString("**Important**: Focus on educational insights and avoid specific trading recommendations. Include appropriate disclaimers about investment risks.\n")

	return promptBuilder.String(), nil
}

// buildAnalysisPrompt constructs the prompt for on-demand analysis
func (g *InferenceGateway) buildAnalysisPrompt(request *entities.AnalysisRequest) (string, error) {
	var promptBuilder strings.Builder

	promptBuilder.WriteString(fmt.Sprintf("# On-Demand Portfolio Analysis: %s\n\n", strings.Title(request.AnalysisType)))

	// Portfolio data
	if request.PortfolioData != nil {
		promptBuilder.WriteString("## Current Portfolio\n")
		promptBuilder.WriteString(fmt.Sprintf("- **Total Value**: $%.2f\n", request.PortfolioData.TotalValue))
		promptBuilder.WriteString(fmt.Sprintf("- **Total Return**: $%.2f (%.2f%%)\n",
			request.PortfolioData.TotalReturn, request.PortfolioData.TotalReturnPct))

		if len(request.PortfolioData.Positions) > 0 {
			promptBuilder.WriteString("\n### Current Positions\n")
			for _, pos := range request.PortfolioData.Positions {
				promptBuilder.WriteString(fmt.Sprintf("- **%s**: $%.2f (%.2f%%)\n",
					pos.BasketName, pos.CurrentValue, pos.Weight*100))
			}
		}
	}

	// Analysis-specific instructions
	promptBuilder.WriteString("\n---\n\n")
	switch request.AnalysisType {
	case entities.AnalysisTypeDiversification:
		promptBuilder.WriteString("Please analyze the portfolio's diversification across:\n")
		promptBuilder.WriteString("- Asset classes and sectors\n- Geographic exposure\n- Risk concentration\n- Recommendations for improving diversification\n")
	case entities.AnalysisTypeRisk:
		promptBuilder.WriteString("Please provide a comprehensive risk analysis including:\n")
		promptBuilder.WriteString("- Current risk levels and exposure\n- Risk-adjusted performance metrics\n- Stress testing scenarios\n- Risk mitigation recommendations\n")
	case entities.AnalysisTypePerformance:
		promptBuilder.WriteString("Please analyze portfolio performance including:\n")
		promptBuilder.WriteString("- Performance attribution by holdings\n- Benchmark comparison\n- Historical performance trends\n- Performance optimization insights\n")
	case entities.AnalysisTypeAllocation:
		promptBuilder.WriteString("Please review portfolio allocation including:\n")
		promptBuilder.WriteString("- Current vs target allocation\n- Allocation efficiency analysis\n- Rebalancing opportunities\n- Strategic allocation recommendations\n")
	default:
		promptBuilder.WriteString("Please provide a comprehensive analysis of the requested aspect of the portfolio.\n")
	}

	promptBuilder.WriteString("\n**Important**: Provide actionable insights while avoiding specific trading recommendations. Include appropriate investment disclaimers.\n")

	return promptBuilder.String(), nil
}

// storeArtifact stores detailed analysis data as an artifact in 0G storage
func (g *InferenceGateway) storeArtifact(ctx context.Context, result *entities.InferenceResult, userID string, analysisType string) error {
	if g.storageClient == nil {
		return fmt.Errorf("storage client not available")
	}

	// Create detailed artifact data
	artifactData := map[string]interface{}{
		"request_id":      result.RequestID,
		"user_id":         userID,
		"analysis_type":   analysisType,
		"content":         result.Content,
		"metadata":        result.Metadata,
		"tokens_used":     result.TokensUsed,
		"processing_time": result.ProcessingTime.Seconds(),
		"model":           result.Model,
		"created_at":      result.CreatedAt.UTC().Format(time.RFC3339),
		"version":         "1.0",
	}

	// Serialize to JSON
	jsonData, err := json.Marshal(artifactData)
	if err != nil {
		return fmt.Errorf("failed to serialize artifact data: %w", err)
	}

	// Store in 0G storage
	metadata := map[string]string{
		"content_type":  "application/json",
		"user_id":       userID,
		"analysis_type": analysisType,
		"request_id":    result.RequestID,
		"created_at":    result.CreatedAt.UTC().Format(time.RFC3339),
	}

	storageResult, err := g.storageClient.Store(ctx, entities.NamespaceAIArtifacts, jsonData, metadata)
	if err != nil {
		return fmt.Errorf("failed to store artifact: %w", err)
	}

	// Update result with artifact URI
	result.ArtifactURI = storageResult.URI

	g.logger.Info("Artifact stored successfully",
		zap.String("request_id", result.RequestID),
		zap.String("artifact_uri", result.ArtifactURI),
		zap.Int64("size", storageResult.Size),
	)

	return nil
}

// recordError records error metrics
func (g *InferenceGateway) recordError(ctx context.Context, operation string, err error) {
	var errorCode string
	if zeroGErr, ok := err.(*entities.ZeroGError); ok {
		errorCode = zeroGErr.Code
	} else {
		errorCode = entities.ErrorCodeInternalError
	}

	g.metrics.RequestErrors.Add(ctx, 1, metric.WithAttributes(
		attribute.String("operation", operation),
		attribute.String("error_code", errorCode),
	))
}

// initInferenceMetrics initializes OpenTelemetry metrics for inference operations
func initInferenceMetrics(meter metric.Meter) (*InferenceMetrics, error) {
	requestsTotal, err := meter.Int64Counter("zerog_inference_requests_total",
		metric.WithDescription("Total number of inference requests"))
	if err != nil {
		return nil, err
	}

	requestDuration, err := meter.Float64Histogram("zerog_inference_request_duration_seconds",
		metric.WithDescription("Duration of inference requests in seconds"))
	if err != nil {
		return nil, err
	}

	requestErrors, err := meter.Int64Counter("zerog_inference_request_errors_total",
		metric.WithDescription("Total number of inference request errors"))
	if err != nil {
		return nil, err
	}

	tokensUsed, err := meter.Int64Counter("zerog_inference_tokens_used_total",
		metric.WithDescription("Total tokens used for inference"))
	if err != nil {
		return nil, err
	}

	tokensGenerated, err := meter.Int64Counter("zerog_inference_tokens_generated_total",
		metric.WithDescription("Total tokens generated by inference"))
	if err != nil {
		return nil, err
	}

	activeRequests, err := meter.Int64UpDownCounter("zerog_inference_active_requests",
		metric.WithDescription("Number of active inference requests"))
	if err != nil {
		return nil, err
	}

	modelUsage, err := meter.Int64Counter("zerog_inference_model_usage_total",
		metric.WithDescription("Total usage count by model"))
	if err != nil {
		return nil, err
	}

	return &InferenceMetrics{
		RequestsTotal:   requestsTotal,
		RequestDuration: requestDuration,
		RequestErrors:   requestErrors,
		TokensUsed:      tokensUsed,
		TokensGenerated: tokensGenerated,
		ActiveRequests:  activeRequests,
		ModelUsage:      modelUsage,
	}, nil
}
