package inference

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/stack-service/stack_service/internal/config"
	"github.com/stack-service/stack_service/internal/domain/entities"
	"github.com/stack-service/stack_service/internal/zerog/clients"
	"github.com/stack-service/stack_service/internal/zerog/compute"
	"github.com/stack-service/stack_service/internal/zerog/prompts"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// Gateway provides 0G inference/compute functionality
type Gateway struct {
	config        *config.ComputeConfig
	storageClient *clients.StorageClient
	computeClient *compute.Client
	promptManager *prompts.TemplateManager
	logger        *zap.Logger
	tracer        trace.Tracer

	// Model configuration
	defaultModel  string
	maxTokens     int
	temperature   float32
}

// NewGateway creates a new inference gateway
func NewGateway(
	config config.ComputeConfig, 
	storageClient *clients.StorageClient, 
	privateKey string,
	logger *zap.Logger,
) (*Gateway, error) {
	// Create compute client
	computeClient, err := compute.NewClient(&config, privateKey, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create compute client: %w", err)
	}

	// Create prompt template manager
	promptManager := prompts.NewTemplateManager()

	gateway := &Gateway{
		config:        &config,
		storageClient: storageClient,
		computeClient: computeClient,
		promptManager: promptManager,
		logger:        logger,
		tracer:        otel.Tracer("0g-inference-gateway"),
		defaultModel:  "gpt-3.5-turbo", // Default model, can be configured
		maxTokens:     2000,
		temperature:   0.7,
	}

	logger.Info("0G inference gateway initialized",
		zap.String("endpoint", config.Endpoint),
		zap.String("default_model", gateway.defaultModel),
	)

	return gateway, nil
}

// HealthCheck verifies inference gateway connectivity
func (g *Gateway) HealthCheck(ctx context.Context) (*entities.HealthStatus, error) {
	ctx, span := g.tracer.Start(ctx, "gateway.health_check")
	defer span.End()

	return g.computeClient.HealthCheck(ctx)
}

// GenerateWeeklySummary creates an AI-generated weekly portfolio summary
func (g *Gateway) GenerateWeeklySummary(ctx context.Context, request *entities.WeeklySummaryRequest) (*entities.InferenceResult, error) {
	startTime := time.Now()
	ctx, span := g.tracer.Start(ctx, "gateway.generate_weekly_summary", trace.WithAttributes(
		attribute.String("user_id", request.UserID.String()),
		attribute.String("week_start", request.WeekStart.Format("2006-01-02")),
	))
	defer span.End()

	g.logger.Info("Generating weekly summary",
		zap.String("user_id", request.UserID.String()),
		zap.String("week_start", request.WeekStart.Format("2006-01-02")),
	)

	// Create prompt context
	promptContext := &prompts.WeeklySummaryContext{
		UserID:      request.UserID,
		WeekStart:   request.WeekStart,
		WeekEnd:     request.WeekEnd,
		Portfolio:   request.PortfolioData,
		Preferences: request.Preferences,
		MarketContext: prompts.CreateDefaultMarketContext(),
	}

	// Add previous week data if available
	if request.PreviousWeek != nil {
		promptContext.PreviousWeek = request.PreviousWeek.PortfolioData
	}

	// Validate context
	if err := prompts.ValidateWeeklySummaryContext(promptContext); err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("invalid prompt context: %w", err)
	}

	// Generate prompts
	systemPrompt, userPrompt, err := g.promptManager.GenerateWeeklySummaryPrompt(promptContext)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to generate prompts: %w", err)
	}

	// Create inference request
	inferenceReq := &compute.InferenceRequest{
		Model: g.selectModelForTask("weekly_summary"),
		Messages: []compute.ChatMessage{
			{
				Role:    "system",
				Content: systemPrompt,
			},
			{
				Role:    "user",
				Content: userPrompt,
			},
		},
		MaxTokens:   g.maxTokens,
		Temperature: g.temperature,
		Stream:      false,
		Metadata: map[string]interface{}{
			"task_type": "weekly_summary",
			"user_id":   request.UserID.String(),
			"week_start": request.WeekStart.Format("2006-01-02"),
		},
	}

	// Execute inference
	response, err := g.computeClient.GenerateInference(ctx, inferenceReq)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("inference request failed: %w", err)
	}

	// Extract generated content
	var content string
	if len(response.Choices) > 0 {
		content = response.Choices[0].Message.Content
	} else {
		return nil, fmt.Errorf("no content generated")
	}

	// Store artifact in 0G storage if needed
	artifactURI, err := g.storeArtifact(ctx, request.UserID, "weekly_summary", content)
	if err != nil {
		g.logger.Warn("Failed to store artifact in 0G storage", zap.Error(err))
		// Don't fail the entire operation
		artifactURI = ""
	}

	// Build result
	result := &entities.InferenceResult{
		RequestID:      uuid.New().String(),
		Content:        content,
		ContentType:    "text/markdown",
		Metadata: map[string]interface{}{
			"task_type":   "weekly_summary",
			"model":       response.Model,
			"user_id":     request.UserID.String(),
			"week_start":  request.WeekStart.Format("2006-01-02"),
			"week_end":    request.WeekEnd.Format("2006-01-02"),
			"prompt_chars": len(systemPrompt) + len(userPrompt),
		},
		TokensUsed:     response.Usage.TotalTokens,
		ProcessingTime: time.Since(startTime),
		Model:          response.Model,
		CreatedAt:      time.Now(),
		ArtifactURI:    artifactURI,
	}

	g.logger.Info("Weekly summary generated successfully",
		zap.String("user_id", request.UserID.String()),
		zap.String("request_id", result.RequestID),
		zap.Int("tokens_used", result.TokensUsed),
		zap.Duration("processing_time", result.ProcessingTime),
	)

	return result, nil
}

// AnalyzeOnDemand performs on-demand portfolio analysis
func (g *Gateway) AnalyzeOnDemand(ctx context.Context, request *entities.AnalysisRequest) (*entities.InferenceResult, error) {
	startTime := time.Now()
	ctx, span := g.tracer.Start(ctx, "gateway.analyze_on_demand", trace.WithAttributes(
		attribute.String("user_id", request.UserID.String()),
		attribute.String("analysis_type", request.AnalysisType),
	))
	defer span.End()

	g.logger.Info("Performing on-demand analysis",
		zap.String("user_id", request.UserID.String()),
		zap.String("analysis_type", request.AnalysisType),
	)

	// Create prompt context
	promptContext := &prompts.OnDemandAnalysisContext{
		UserID:        request.UserID,
		AnalysisType:  request.AnalysisType,
		Portfolio:     request.PortfolioData,
		Preferences:   request.Preferences,
		Parameters:    request.Parameters,
		MarketContext: prompts.CreateDefaultMarketContext(),
	}

	// Validate context
	if err := prompts.ValidateAnalysisContext(promptContext); err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("invalid analysis context: %w", err)
	}

	// Generate prompts
	systemPrompt, userPrompt, err := g.promptManager.GenerateOnDemandAnalysisPrompt(promptContext)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to generate analysis prompts: %w", err)
	}

	// Create inference request
	inferenceReq := &compute.InferenceRequest{
		Model: g.selectModelForTask(request.AnalysisType),
		Messages: []compute.ChatMessage{
			{
				Role:    "system",
				Content: systemPrompt,
			},
			{
				Role:    "user",
				Content: userPrompt,
			},
		},
		MaxTokens:   g.getMaxTokensForAnalysis(request.AnalysisType),
		Temperature: 0.3, // Lower temperature for analysis tasks
		Stream:      false,
		Metadata: map[string]interface{}{
			"task_type":     "on_demand_analysis",
			"analysis_type": request.AnalysisType,
			"user_id":       request.UserID.String(),
		},
	}

	// Execute inference
	response, err := g.computeClient.GenerateInference(ctx, inferenceReq)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("analysis inference request failed: %w", err)
	}

	// Extract generated content
	var content string
	if len(response.Choices) > 0 {
		content = response.Choices[0].Message.Content
	} else {
		return nil, fmt.Errorf("no analysis content generated")
	}

	// Store artifact in 0G storage if needed
	artifactURI, err := g.storeArtifact(ctx, request.UserID, request.AnalysisType, content)
	if err != nil {
		g.logger.Warn("Failed to store analysis artifact", zap.Error(err))
		artifactURI = ""
	}

	// Build result
	result := &entities.InferenceResult{
		RequestID:      uuid.New().String(),
		Content:        content,
		ContentType:    "text/markdown",
		Metadata: map[string]interface{}{
			"task_type":     "on_demand_analysis",
			"analysis_type": request.AnalysisType,
			"model":         response.Model,
			"user_id":       request.UserID.String(),
			"prompt_chars":  len(systemPrompt) + len(userPrompt),
		},
		TokensUsed:     response.Usage.TotalTokens,
		ProcessingTime: time.Since(startTime),
		Model:          response.Model,
		CreatedAt:      time.Now(),
		ArtifactURI:    artifactURI,
	}

	g.logger.Info("On-demand analysis completed successfully",
		zap.String("user_id", request.UserID.String()),
		zap.String("analysis_type", request.AnalysisType),
		zap.String("request_id", result.RequestID),
		zap.Int("tokens_used", result.TokensUsed),
		zap.Duration("processing_time", result.ProcessingTime),
	)

	return result, nil
}

// GetServiceInfo returns information about available inference services
func (g *Gateway) GetServiceInfo(ctx context.Context) (*entities.ServiceInfo, error) {
	ctx, span := g.tracer.Start(ctx, "gateway.get_service_info")
	defer span.End()

	// Get available models from compute client
	models, err := g.computeClient.GetAvailableModels(ctx)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to get available models: %w", err)
	}

	// Convert to entities format
	serviceModels := make([]entities.ModelInfo, len(models))
	for i, model := range models {
		serviceModels[i] = entities.ModelInfo{
			ModelID:     model.ID,
			Name:        model.Name,
			Description: model.Description,
			MaxTokens:   model.MaxTokens,
			InputCost:   model.InputPrice,
			OutputCost:  model.OutputPrice,
			Version:     "1.0.0",
			UpdatedAt:   time.Now(),
		}
	}

	return &entities.ServiceInfo{
		ProviderID:  "0g-compute",
		ServiceName: "0G Compute Network",
		Models:      serviceModels,
		Pricing: &entities.PricingInfo{
			Currency:      "USD",
			BaseRate:      0.0,
			TokenRate:     0.002,
			MinimumCharge: 0.0,
		},
		Capabilities: []string{"text-generation", "analysis", "summarization"},
		Status:       "active",
		Metadata: map[string]interface{}{
			"endpoint":     g.config.Endpoint,
			"max_retries":  g.config.MaxRetries,
			"timeout":      g.config.Timeout,
			"default_model": g.defaultModel,
		},
	}, nil
}

// selectModelForTask selects the appropriate model for a given task
func (g *Gateway) selectModelForTask(taskType string) string {
	// Model selection logic based on task type
	switch taskType {
	case "weekly_summary":
		return g.defaultModel // Use default model for summaries
	case entities.AnalysisTypeRisk, entities.AnalysisTypePerformance:
		return g.defaultModel // Use default model for complex analysis
	case entities.AnalysisTypeDiversification, entities.AnalysisTypeAllocation, entities.AnalysisTypeRebalancing:
		return g.defaultModel // Use default model for portfolio analysis
	default:
		return g.defaultModel
	}
}

// getMaxTokensForAnalysis returns appropriate token limits for different analysis types
func (g *Gateway) getMaxTokensForAnalysis(analysisType string) int {
	switch analysisType {
	case entities.AnalysisTypeRisk, entities.AnalysisTypePerformance:
		return 1500 // More detailed analysis
	case entities.AnalysisTypeDiversification, entities.AnalysisTypeAllocation:
		return 1200 // Medium length analysis
	case entities.AnalysisTypeRebalancing:
		return 1000 // Focused analysis
	default:
		return g.maxTokens
	}
}

// storeArtifact stores analysis results in 0G storage
func (g *Gateway) storeArtifact(ctx context.Context, userID uuid.UUID, taskType, content string) (string, error) {
	if g.storageClient == nil {
		return "", fmt.Errorf("storage client not available")
	}

	// Create metadata for the artifact
	metadata := map[string]string{
		"user_id":   userID.String(),
		"task_type": taskType,
		"timestamp": time.Now().Format(time.RFC3339),
		"content_type": "text/markdown",
	}

	// Store in the AI artifacts using the actual storage client interface
	storageID, err := g.storageClient.Store(ctx, []byte(content), metadata)
	if err != nil {
		return "", fmt.Errorf("failed to store artifact: %w", err)
	}

	// Create URI from storage ID and namespace
	artifactURI := fmt.Sprintf("0g://%s/%s", entities.NamespaceAIArtifacts, storageID)

	g.logger.Debug("Artifact stored successfully",
		zap.String("user_id", userID.String()),
		zap.String("task_type", taskType),
		zap.String("storage_id", storageID),
		zap.String("artifact_uri", artifactURI),
		zap.Int("content_size", len(content)),
	)

	return artifactURI, nil
}

// GetMetrics returns gateway operational metrics
func (g *Gateway) GetMetrics() map[string]interface{} {
	return map[string]interface{}{
		"endpoint":      g.config.Endpoint,
		"max_retries":   g.config.MaxRetries,
		"timeout":       g.config.Timeout,
		"default_model": g.defaultModel,
		"max_tokens":    g.maxTokens,
		"temperature":   g.temperature,
		"timestamp":     time.Now(),
	}
}
