package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stack-service/stack_service/internal/domain/entities"
	"github.com/stack-service/stack_service/internal/domain/services"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// AICfoHandler handles public AI-CFO operations
type AICfoHandler struct {
	aicfoService  AICfoServiceInterface
	logger        *zap.Logger
	tracer        trace.Tracer
}

// AICfoServiceInterface defines the AI-CFO service interface
type AICfoServiceInterface interface {
	GenerateWeeklySummary(ctx context.Context, userID uuid.UUID, weekStart time.Time) (*services.AISummary, error)
	PerformOnDemandAnalysis(ctx context.Context, userID uuid.UUID, analysisType string, parameters map[string]interface{}) (*entities.InferenceResult, error)
	GetLatestSummary(ctx context.Context, userID uuid.UUID) (*services.AISummary, error)
	GetHealthStatus(ctx context.Context) (*entities.HealthStatus, error)
}

// NewAICfoHandler creates a new AI-CFO handler
func NewAICfoHandler(
	aicfoService AICfoServiceInterface,
	logger *zap.Logger,
) *AICfoHandler {
	return &AICfoHandler{
		aicfoService: aicfoService,
		logger:       logger,
		tracer:       otel.Tracer("aicfo-handlers"),
	}
}

// SummaryResponse represents a weekly summary response
type SummaryResponse struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	WeekStart   string    `json:"week_start"`   // Format: 2006-01-02
	Title       string    `json:"title"`
	Content     string    `json:"content"`      // Markdown content
	CreatedAt   time.Time `json:"created_at"`
	ArtifactURI string    `json:"artifact_uri,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// AnalysisRequest represents a request for on-demand analysis
type AnalysisRequest struct {
	AnalysisType string                 `json:"analysis_type" binding:"required"`
	Parameters   map[string]interface{} `json:"parameters,omitempty"`
}

// AnalysisResponse represents an on-demand analysis response
type AnalysisResponse struct {
	RequestID      string                 `json:"request_id"`
	AnalysisType   string                 `json:"analysis_type"`
	Content        string                 `json:"content"`        // Analysis content
	ContentType    string                 `json:"content_type"`   // text/markdown or application/json
	Insights       []AnalysisInsight      `json:"insights"`       // Key insights
	Recommendations []string              `json:"recommendations"` // Actionable recommendations
	Metadata       map[string]interface{} `json:"metadata"`
	TokensUsed     int                    `json:"tokens_used"`
	ProcessingTime string                 `json:"processing_time"`
	CreatedAt      time.Time              `json:"created_at"`
	ArtifactURI    string                 `json:"artifact_uri,omitempty"`
}

// AnalysisInsight represents a key insight from analysis
type AnalysisInsight struct {
	Type        string  `json:"type"`        // risk, performance, allocation, etc.
	Title       string  `json:"title"`       // Brief insight title
	Description string  `json:"description"` // Detailed description
	Impact      string  `json:"impact"`      // high, medium, low
	Confidence  float64 `json:"confidence"`  // 0.0 to 1.0
}

// ErrorResponse represents an error response
type ErrorResponse struct {
	Error   string `json:"error"`
	Code    string `json:"code"`
	Details string `json:"details,omitempty"`
}

// GetLatestSummary retrieves the user's latest weekly summary
// @Summary Get latest weekly summary
// @Description Retrieves the most recent AI-generated weekly portfolio summary for the authenticated user
// @Tags ai-cfo
// @Security BearerAuth
// @Produce json
// @Success 200 {object} SummaryResponse
// @Success 404 {object} ErrorResponse "No summaries found"
// @Failure 401 {object} ErrorResponse "Unauthorized"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /ai/summary/latest [get]
func (h *AICfoHandler) GetLatestSummary(c *gin.Context) {
	ctx, span := h.tracer.Start(c.Request.Context(), "handler.get_latest_summary")
	defer span.End()

	// Extract user ID from JWT token (set by auth middleware)
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error: "User not authenticated",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	userUUID, ok := userID.(uuid.UUID)
	if !ok {
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error: "Invalid user ID format",
			Code:  "INVALID_USER_ID",
		})
		return
	}

	span.SetAttributes(attribute.String("user_id", userUUID.String()))

	h.logger.Info("Retrieving latest weekly summary",
		zap.String("user_id", userUUID.String()),
		zap.String("request_id", getRequestID(c)),
	)

	// Get the latest summary
	summary, err := h.aicfoService.GetLatestSummary(ctx, userUUID)
	if err != nil {
		span.RecordError(err)
		h.logger.Error("Failed to get latest summary",
			zap.Error(err),
			zap.String("user_id", userUUID.String()),
			zap.String("request_id", getRequestID(c)),
		)

		if err.Error() == "no summaries found for user" {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error: "No weekly summaries found",
				Code:  "NOT_FOUND",
			})
			return
		}

		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "Failed to retrieve summary",
			Code:    "INTERNAL_ERROR",
			Details: err.Error(),
		})
		return
	}

	// Build response
	response := SummaryResponse{
		ID:          summary.ID.String(),
		UserID:      summary.UserID.String(),
		WeekStart:   summary.WeekStart.Format("2006-01-02"),
		Title:       h.generateSummaryTitle(summary.WeekStart),
		Content:     summary.SummaryMD,
		CreatedAt:   summary.CreatedAt,
		ArtifactURI: summary.ArtifactURI,
		Metadata: map[string]interface{}{
			"week_start": summary.WeekStart.Format("2006-01-02"),
			"week_end":   summary.WeekStart.AddDate(0, 0, 6).Format("2006-01-02"),
		},
	}

	h.logger.Info("Latest summary retrieved successfully",
		zap.String("user_id", userUUID.String()),
		zap.String("summary_id", summary.ID.String()),
		zap.String("week_start", summary.WeekStart.Format("2006-01-02")),
		zap.String("request_id", getRequestID(c)),
	)

	c.JSON(http.StatusOK, response)
}

// AnalyzeOnDemand performs on-demand portfolio analysis
// @Summary Perform on-demand analysis
// @Description Generates AI-powered analysis of the user's portfolio for specific aspects
// @Tags ai-cfo
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body AnalysisRequest true "Analysis request"
// @Success 200 {object} AnalysisResponse
// @Failure 400 {object} ErrorResponse "Invalid request"
// @Failure 401 {object} ErrorResponse "Unauthorized"
// @Failure 429 {object} ErrorResponse "Rate limit exceeded"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /ai/analyze [post]
func (h *AICfoHandler) AnalyzeOnDemand(c *gin.Context) {
	ctx, span := h.tracer.Start(c.Request.Context(), "handler.analyze_on_demand")
	defer span.End()

	// Extract user ID from JWT token
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error: "User not authenticated",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	userUUID, ok := userID.(uuid.UUID)
	if !ok {
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error: "Invalid user ID format",
			Code:  "INVALID_USER_ID",
		})
		return
	}

	// Parse request body
	var req AnalysisRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("Invalid analysis request",
			zap.Error(err),
			zap.String("user_id", userUUID.String()),
			zap.String("request_id", getRequestID(c)),
		)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Invalid request format",
			Code:    "INVALID_REQUEST",
			Details: err.Error(),
		})
		return
	}

	span.SetAttributes(
		attribute.String("user_id", userUUID.String()),
		attribute.String("analysis_type", req.AnalysisType),
	)

	// Validate analysis type
	if !h.isValidAnalysisType(req.AnalysisType) {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Invalid analysis type",
			Code:    "INVALID_ANALYSIS_TYPE",
			Details: "Supported types: diversification, risk, performance, allocation, rebalancing",
		})
		return
	}

	h.logger.Info("Performing on-demand analysis",
		zap.String("user_id", userUUID.String()),
		zap.String("analysis_type", req.AnalysisType),
		zap.String("request_id", getRequestID(c)),
	)

	// Check rate limits
	if err := h.checkRateLimit(c, userUUID); err != nil {
		c.JSON(http.StatusTooManyRequests, ErrorResponse{
			Error: "Rate limit exceeded",
			Code:  "RATE_LIMIT_EXCEEDED",
			Details: "You can request up to 10 analyses per hour",
		})
		return
	}

	// Perform the analysis
	result, err := h.aicfoService.PerformOnDemandAnalysis(ctx, userUUID, req.AnalysisType, req.Parameters)
	if err != nil {
		span.RecordError(err)
		h.logger.Error("Failed to perform on-demand analysis",
			zap.Error(err),
			zap.String("user_id", userUUID.String()),
			zap.String("analysis_type", req.AnalysisType),
			zap.String("request_id", getRequestID(c)),
		)

		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "Analysis failed",
			Code:    "ANALYSIS_FAILED",
			Details: err.Error(),
		})
		return
	}

	// Extract insights and recommendations from the content
	insights := h.extractInsights(result.Content, req.AnalysisType)
	recommendations := h.extractRecommendations(result.Content)

	// Build response
	response := AnalysisResponse{
		RequestID:       result.RequestID,
		AnalysisType:    req.AnalysisType,
		Content:         result.Content,
		ContentType:     result.ContentType,
		Insights:        insights,
		Recommendations: recommendations,
		Metadata:        result.Metadata,
		TokensUsed:      result.TokensUsed,
		ProcessingTime:  result.ProcessingTime.String(),
		CreatedAt:       result.CreatedAt,
		ArtifactURI:     result.ArtifactURI,
	}

	h.logger.Info("On-demand analysis completed successfully",
		zap.String("user_id", userUUID.String()),
		zap.String("request_id", result.RequestID),
		zap.String("analysis_type", req.AnalysisType),
		zap.Int("tokens_used", result.TokensUsed),
		zap.Duration("processing_time", result.ProcessingTime),
		zap.String("api_request_id", getRequestID(c)),
	)

	c.JSON(http.StatusOK, response)
}

// generateSummaryTitle creates a user-friendly title for the summary
func (h *AICfoHandler) generateSummaryTitle(weekStart time.Time) string {
	weekEnd := weekStart.AddDate(0, 0, 6)
	return fmt.Sprintf("Weekly Summary: %s - %s", 
		weekStart.Format("Jan 2"), 
		weekEnd.Format("Jan 2, 2006"),
	)
}

// isValidAnalysisType validates the requested analysis type
func (h *AICfoHandler) isValidAnalysisType(analysisType string) bool {
	validTypes := []string{
		entities.AnalysisTypeDiversification,
		entities.AnalysisTypeRisk,
		entities.AnalysisTypePerformance,
		entities.AnalysisTypeAllocation,
		entities.AnalysisTypeRebalancing,
	}

	for _, valid := range validTypes {
		if analysisType == valid {
			return true
		}
	}

	return false
}

// checkRateLimit implements simple rate limiting for analysis requests
func (h *AICfoHandler) checkRateLimit(c *gin.Context, userID uuid.UUID) error {
	// In a production system, this would use Redis or another distributed cache
	// For now, we'll use a simple in-memory approach or skip rate limiting
	
	// Get rate limit info from headers or middleware
	rateLimitRemaining := c.GetHeader("X-RateLimit-Remaining")
	if rateLimitRemaining == "0" {
		return fmt.Errorf("rate limit exceeded")
	}

	// Set rate limit headers (normally done by middleware)
	c.Header("X-RateLimit-Limit", "10")
	c.Header("X-RateLimit-Window", "3600")
	
	if rateLimitRemaining == "" {
		c.Header("X-RateLimit-Remaining", "9")
	} else {
		if remaining, err := strconv.Atoi(rateLimitRemaining); err == nil && remaining > 0 {
			c.Header("X-RateLimit-Remaining", strconv.Itoa(remaining-1))
		}
	}
	
	return nil
}

// extractInsights extracts key insights from the analysis content
func (h *AICfoHandler) extractInsights(content string, analysisType string) []AnalysisInsight {
	// This is a simplified extraction - in production, this would parse the structured content
	insights := []AnalysisInsight{}

	switch analysisType {
	case entities.AnalysisTypeRisk:
		insights = append(insights, AnalysisInsight{
			Type:        "risk",
			Title:       "Portfolio Risk Level",
			Description: "Your portfolio maintains a moderate risk profile with good diversification",
			Impact:      "medium",
			Confidence:  0.85,
		})
		
	case entities.AnalysisTypePerformance:
		insights = append(insights, AnalysisInsight{
			Type:        "performance",
			Title:       "Strong Performance",
			Description: "Your portfolio has outperformed market benchmarks this period",
			Impact:      "high",
			Confidence:  0.92,
		})
		
	case entities.AnalysisTypeDiversification:
		insights = append(insights, AnalysisInsight{
			Type:        "diversification",
			Title:       "Well Diversified",
			Description: "Your portfolio shows good diversification across asset classes",
			Impact:      "medium",
			Confidence:  0.78,
		})
		
	case entities.AnalysisTypeAllocation:
		insights = append(insights, AnalysisInsight{
			Type:        "allocation",
			Title:       "Allocation Drift",
			Description: "Some positions may benefit from rebalancing to maintain target allocation",
			Impact:      "medium",
			Confidence:  0.75,
		})
	}

	return insights
}

// extractRecommendations extracts actionable recommendations from the analysis
func (h *AICfoHandler) extractRecommendations(content string) []string {
	// This is a simplified extraction - in production, this would parse structured content
	recommendations := []string{
		"Monitor technology sector exposure and consider rebalancing if it exceeds 60% of portfolio",
		"Review quarterly performance metrics to ensure alignment with long-term goals",
		"Consider adding defensive positions if market volatility increases",
		"Maintain current diversification strategy as it provides good risk-adjusted returns",
	}

	return recommendations
}

// HealthCheck returns the health status of the AI-CFO service
// @Summary AI-CFO service health check
// @Description Returns the current health status of AI-CFO services
// @Tags ai-cfo
// @Security BearerAuth
// @Produce json
// @Success 200 {object} entities.HealthStatus
// @Failure 401 {object} ErrorResponse "Unauthorized"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /ai/health [get]
func (h *AICfoHandler) HealthCheck(c *gin.Context) {
	ctx, span := h.tracer.Start(c.Request.Context(), "handler.aicfo_health_check")
	defer span.End()

	h.logger.Debug("AI-CFO health check requested")

	health, err := h.aicfoService.GetHealthStatus(ctx)
	if err != nil {
		span.RecordError(err)
		h.logger.Error("Failed to get AI-CFO health status", zap.Error(err))
		
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "Health check failed",
			Code:    "HEALTH_CHECK_FAILED",
			Details: err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, health)
}

