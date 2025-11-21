package handlers

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stack-service/stack_service/internal/adapters/alpaca"
	"github.com/stack-service/stack_service/internal/domain/entities"
	"github.com/stack-service/stack_service/internal/domain/services"
	"github.com/stack-service/stack_service/internal/infrastructure/zerog"
	"github.com/stack-service/stack_service/pkg/logger"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// IntegrationHandlers consolidates all external service integration handlers
type IntegrationHandlers struct {
	// Alpaca
	alpacaClient *alpaca.Client
	
	// Due
	dueService          *services.DueService
	notificationService *services.NotificationService
	
	// 0G
	storageClient    entities.ZeroGStorageClient
	inferenceGateway entities.ZeroGInferenceGateway
	namespaceManager *zerog.NamespaceManager
	
	// AI CFO
	aicfoService AICfoServiceInterface
	
	logger *zap.Logger
	tracer trace.Tracer
}

// AICfoServiceInterface defines the AI-CFO service interface
type AICfoServiceInterface interface {
	GenerateWeeklySummary(ctx context.Context, userID uuid.UUID, weekStart time.Time) (*services.AISummary, error)
	PerformOnDemandAnalysis(ctx context.Context, userID uuid.UUID, analysisType string, parameters map[string]interface{}) (*entities.InferenceResult, error)
	GetLatestSummary(ctx context.Context, userID uuid.UUID) (*services.AISummary, error)
	GetHealthStatus(ctx context.Context) (*entities.HealthStatus, error)
}

// NewIntegrationHandlers creates new integration handlers
func NewIntegrationHandlers(
	alpacaClient *alpaca.Client,
	dueService *services.DueService,
	notificationService *services.NotificationService,
	storageClient entities.ZeroGStorageClient,
	inferenceGateway entities.ZeroGInferenceGateway,
	namespaceManager *zerog.NamespaceManager,
	aicfoService AICfoServiceInterface,
	logger *logger.Logger,
) *IntegrationHandlers {
	return &IntegrationHandlers{
		alpacaClient:        alpacaClient,
		dueService:          dueService,
		notificationService: notificationService,
		storageClient:       storageClient,
		inferenceGateway:    inferenceGateway,
		namespaceManager:    namespaceManager,
		aicfoService:        aicfoService,
		logger:              logger.Zap(),
		tracer:              otel.Tracer("integration-handlers"),
	}
}

// ===== ALPACA HANDLERS =====

type AssetsResponse struct {
	Assets     []entities.AlpacaAssetResponse `json:"assets"`
	TotalCount int                            `json:"total_count"`
	Page       int                            `json:"page"`
	PageSize   int                            `json:"page_size"`
}

func (h *IntegrationHandlers) GetAssets(c *gin.Context) {
	query := make(map[string]string)
	status := c.DefaultQuery("status", "active")
	if status != "" {
		query["status"] = status
	}
	if assetClass := c.Query("asset_class"); assetClass != "" {
		query["asset_class"] = assetClass
	}
	if exchange := c.Query("exchange"); exchange != "" {
		query["exchange"] = exchange
	}
	tradable := c.DefaultQuery("tradable", "true")
	if tradable != "" {
		query["tradable"] = tradable
	}
	if fractionable := c.Query("fractionable"); fractionable != "" {
		query["fractionable"] = fractionable
	}
	if shortable := c.Query("shortable"); shortable != "" {
		query["shortable"] = shortable
	}
	if easyToBorrow := c.Query("easy_to_borrow"); easyToBorrow != "" {
		query["easy_to_borrow"] = easyToBorrow
	}

	assets, err := h.alpacaClient.ListAssets(c.Request.Context(), query)
	if err != nil {
		h.logger.Error("Failed to fetch assets", zap.Error(err))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "ASSETS_FETCH_ERROR",
			Message: "Failed to retrieve assets",
		})
		return
	}

	searchTerm := strings.ToLower(c.Query("search"))
	if searchTerm != "" {
		filtered := make([]entities.AlpacaAssetResponse, 0)
		for _, asset := range assets {
			if strings.Contains(strings.ToLower(asset.Symbol), searchTerm) ||
				strings.Contains(strings.ToLower(asset.Name), searchTerm) {
				filtered = append(filtered, asset)
			}
		}
		assets = filtered
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "100"))
	if pageSize < 1 {
		pageSize = 100
	}
	if pageSize > 500 {
		pageSize = 500
	}

	totalCount := len(assets)
	start := (page - 1) * pageSize
	end := start + pageSize
	if start >= totalCount {
		c.JSON(http.StatusOK, AssetsResponse{
			Assets:     []entities.AlpacaAssetResponse{},
			TotalCount: totalCount,
			Page:       page,
			PageSize:   pageSize,
		})
		return
	}
	if end > totalCount {
		end = totalCount
	}

	c.JSON(http.StatusOK, AssetsResponse{
		Assets:     assets[start:end],
		TotalCount: totalCount,
		Page:       page,
		PageSize:   pageSize,
	})
}

func (h *IntegrationHandlers) GetAsset(c *gin.Context) {
	symbolOrID := strings.ToUpper(strings.TrimSpace(c.Param("symbol_or_id")))
	if symbolOrID == "" {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_PARAMETER",
			Message: "Asset symbol or ID is required",
		})
		return
	}

	asset, err := h.alpacaClient.GetAsset(c.Request.Context(), symbolOrID)
	if err != nil {
		if apiErr, ok := err.(*entities.AlpacaErrorResponse); ok {
			if apiErr.Code == http.StatusNotFound {
				c.JSON(http.StatusNotFound, entities.ErrorResponse{
					Code:    "ASSET_NOT_FOUND",
					Message: "Asset not found",
					Details: map[string]interface{}{"symbol": symbolOrID},
				})
				return
			}
		}
		h.logger.Error("Failed to fetch asset", zap.Error(err))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "ASSET_FETCH_ERROR",
			Message: "Failed to retrieve asset details",
		})
		return
	}

	c.JSON(http.StatusOK, asset)
}

// ===== DUE HANDLERS =====

type CreateDueAccountRequest struct {
	Name    string `json:"name" binding:"required"`
	Email   string `json:"email" binding:"required,email"`
	Country string `json:"country" binding:"required,len=2"`
}

func (h *IntegrationHandlers) CreateDueAccount(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req CreateDueAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	dueAccountID, err := h.dueService.CreateDueAccount(c.Request.Context(), userID.(uuid.UUID), req.Email, req.Name, req.Country)
	if err != nil {
		h.logger.Error("Failed to create Due account", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create Due account"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":        "Due account created successfully",
		"due_account_id": dueAccountID,
	})
}

func (h *IntegrationHandlers) HandleDueWebhook(c *gin.Context) {
	var event map[string]interface{}
	if err := c.ShouldBindJSON(&event); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid webhook payload"})
		return
	}

	eventType, ok := event["type"].(string)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing event type"})
		return
	}

	h.logger.Info("Received Due webhook", zap.String("type", eventType))

	switch eventType {
	case "virtual_account.deposit":
		h.handleVirtualAccountDeposit(c, event)
	case "transfer.completed", "transfer.failed":
		h.handleTransferStatusChanged(c, event)
	case "kyc.status_changed":
		h.handleKYCStatusChanged(c, event)
	default:
		h.logger.Warn("Unknown webhook event type", zap.String("type", eventType))
	}

	c.JSON(http.StatusOK, gin.H{"message": "webhook processed"})
}

func (h *IntegrationHandlers) handleVirtualAccountDeposit(c *gin.Context, event map[string]interface{}) {
	data, ok := event["data"].(map[string]interface{})
	if !ok {
		h.logger.Error("Invalid virtual account deposit webhook data")
		return
	}

	virtualAccountID, _ := data["id"].(string)
	amount, _ := data["amount"].(string)
	currency, _ := data["currency"].(string)
	nonce, _ := data["nonce"].(string)
	transactionID, _ := data["transactionId"].(string)

	if err := h.dueService.HandleVirtualAccountDeposit(c.Request.Context(), virtualAccountID, amount, currency, transactionID, nonce); err != nil {
		h.logger.Error("Failed to handle virtual account deposit", zap.Error(err))
		return
	}

	h.logger.Info("Virtual account deposit processed successfully", zap.String("transaction_id", transactionID))
}

func (h *IntegrationHandlers) handleTransferStatusChanged(c *gin.Context, event map[string]interface{}) {
	data, ok := event["data"].(map[string]interface{})
	if !ok {
		h.logger.Error("Invalid transfer webhook data")
		return
	}

	transferID, _ := data["id"].(string)
	status, _ := data["status"].(string)

	h.logger.Info("Transfer status changed", zap.String("transfer_id", transferID), zap.String("status", status))
}

func (h *IntegrationHandlers) handleKYCStatusChanged(c *gin.Context, event map[string]interface{}) {
	data, ok := event["data"].(map[string]interface{})
	if !ok {
		h.logger.Error("Invalid KYC webhook data")
		return
	}

	accountID, _ := data["accountId"].(string)
	status, _ := data["status"].(string)

	h.logger.Info("KYC status changed", zap.String("account_id", accountID), zap.String("status", status))
}

// ===== 0G HANDLERS =====

type ZeroGHealthResponse struct {
	Overall   string                    `json:"overall"`
	Services  map[string]*ServiceHealth `json:"services"`
	CheckedAt time.Time                 `json:"checked_at"`
}

type ServiceHealth struct {
	Status  string                 `json:"status"`
	Latency string                 `json:"latency"`
	Version string                 `json:"version,omitempty"`
	Metrics map[string]interface{} `json:"metrics,omitempty"`
	Errors  []string               `json:"errors,omitempty"`
}

func (h *IntegrationHandlers) ZeroGHealthCheck(c *gin.Context) {
	ctx, span := h.tracer.Start(c.Request.Context(), "handler.zerog_health_check")
	defer span.End()

	response := &ZeroGHealthResponse{
		Overall:   entities.HealthStatusHealthy,
		Services:  make(map[string]*ServiceHealth),
		CheckedAt: time.Now(),
	}

	storageHealth := h.checkStorageHealth(ctx)
	response.Services["storage"] = storageHealth
	if storageHealth.Status != entities.HealthStatusHealthy {
		response.Overall = entities.HealthStatusDegraded
	}

	inferenceHealth := h.checkInferenceHealth(ctx)
	response.Services["inference"] = inferenceHealth
	if inferenceHealth.Status != entities.HealthStatusHealthy {
		if response.Overall == entities.HealthStatusHealthy {
			response.Overall = entities.HealthStatusDegraded
		} else {
			response.Overall = entities.HealthStatusUnhealthy
		}
	}

	c.JSON(http.StatusOK, response)
}

func (h *IntegrationHandlers) checkStorageHealth(ctx context.Context) *ServiceHealth {
	start := time.Now()
	health, err := h.storageClient.HealthCheck(ctx)
	if err != nil {
		return &ServiceHealth{
			Status:  entities.HealthStatusUnhealthy,
			Latency: time.Since(start).String(),
			Errors:  []string{err.Error()},
		}
	}
	return &ServiceHealth{
		Status:  health.Status,
		Latency: health.Latency.String(),
		Version: health.Version,
		Metrics: health.Metrics,
		Errors:  health.Errors,
	}
}

func (h *IntegrationHandlers) checkInferenceHealth(ctx context.Context) *ServiceHealth {
	start := time.Now()
	health, err := h.inferenceGateway.HealthCheck(ctx)
	if err != nil {
		return &ServiceHealth{
			Status:  entities.HealthStatusUnhealthy,
			Latency: time.Since(start).String(),
			Errors:  []string{err.Error()},
		}
	}
	return &ServiceHealth{
		Status:  health.Status,
		Latency: health.Latency.String(),
		Version: health.Version,
		Metrics: health.Metrics,
		Errors:  health.Errors,
	}
}

// ===== AI CFO HANDLERS =====

type SummaryResponse struct {
	ID          string                 `json:"id"`
	UserID      string                 `json:"user_id"`
	WeekStart   string                 `json:"week_start"`
	Title       string                 `json:"title"`
	Content     string                 `json:"content"`
	CreatedAt   time.Time              `json:"created_at"`
	ArtifactURI string                 `json:"artifact_uri,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

type AnalysisRequest struct {
	AnalysisType string                 `json:"analysis_type" binding:"required"`
	Parameters   map[string]interface{} `json:"parameters,omitempty"`
}

type AnalysisResponse struct {
	RequestID       string                 `json:"request_id"`
	AnalysisType    string                 `json:"analysis_type"`
	Content         string                 `json:"content"`
	ContentType     string                 `json:"content_type"`
	Insights        []AnalysisInsight      `json:"insights"`
	Recommendations []string               `json:"recommendations"`
	Metadata        map[string]interface{} `json:"metadata"`
	TokensUsed      int                    `json:"tokens_used"`
	ProcessingTime  string                 `json:"processing_time"`
	CreatedAt       time.Time              `json:"created_at"`
	ArtifactURI     string                 `json:"artifact_uri,omitempty"`
}

type AnalysisInsight struct {
	Type        string  `json:"type"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Impact      string  `json:"impact"`
	Confidence  float64 `json:"confidence"`
}

func (h *IntegrationHandlers) GetLatestSummary(c *gin.Context) {
	ctx, span := h.tracer.Start(c.Request.Context(), "handler.get_latest_summary")
	defer span.End()

	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{
			Message: "User not authenticated",
			Code:    "UNAUTHORIZED",
		})
		return
	}

	userUUID, ok := userID.(uuid.UUID)
	if !ok {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{
			Message: "Invalid user ID format",
			Code:    "INVALID_USER_ID",
		})
		return
	}

	summary, err := h.aicfoService.GetLatestSummary(ctx, userUUID)
	if err != nil {
		h.logger.Error("Failed to get latest summary", zap.Error(err), zap.String("user_id", userUUID.String()))
		if err.Error() == "no summaries found for user" {
			c.JSON(http.StatusNotFound, entities.ErrorResponse{
				Message: "No weekly summaries found",
				Code:    "NOT_FOUND",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Message: "Failed to retrieve summary",
			Code:    "INTERNAL_ERROR",
		})
		return
	}

	c.JSON(http.StatusOK, SummaryResponse{
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
	})
}

func (h *IntegrationHandlers) AnalyzeOnDemand(c *gin.Context) {
	ctx, span := h.tracer.Start(c.Request.Context(), "handler.analyze_on_demand")
	defer span.End()

	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Message: "User not authenticated", Code: "UNAUTHORIZED"})
		return
	}

	userUUID := userID.(uuid.UUID)
	var req AnalysisRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Message: "Invalid request format", Code: "INVALID_REQUEST"})
		return
	}

	if !h.isValidAnalysisType(req.AnalysisType) {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Message: "Invalid analysis type", Code: "INVALID_ANALYSIS_TYPE"})
		return
	}

	result, err := h.aicfoService.PerformOnDemandAnalysis(ctx, userUUID, req.AnalysisType, req.Parameters)
	if err != nil {
		h.logger.Error("Failed to perform analysis", zap.Error(err))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Message: "Analysis failed", Code: "ANALYSIS_FAILED"})
		return
	}

	c.JSON(http.StatusOK, AnalysisResponse{
		RequestID:       result.RequestID,
		AnalysisType:    req.AnalysisType,
		Content:         result.Content,
		ContentType:     result.ContentType,
		Insights:        h.extractInsights(result.Content, req.AnalysisType),
		Recommendations: h.extractRecommendations(result.Content),
		Metadata:        result.Metadata,
		TokensUsed:      result.TokensUsed,
		ProcessingTime:  result.ProcessingTime.String(),
		CreatedAt:       result.CreatedAt,
		ArtifactURI:     result.ArtifactURI,
	})
}

func (h *IntegrationHandlers) AICfoHealthCheck(c *gin.Context) {
	ctx, span := h.tracer.Start(c.Request.Context(), "handler.aicfo_health_check")
	defer span.End()

	health, err := h.aicfoService.GetHealthStatus(ctx)
	if err != nil {
		h.logger.Error("Failed to get AI-CFO health status", zap.Error(err))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Message: "Health check failed", Code: "HEALTH_CHECK_FAILED"})
		return
	}

	c.JSON(http.StatusOK, health)
}

func (h *IntegrationHandlers) generateSummaryTitle(weekStart time.Time) string {
	return "Weekly Summary: " + weekStart.Format("Jan 2") + " - " + weekStart.AddDate(0, 0, 6).Format("Jan 2, 2006")
}

func (h *IntegrationHandlers) isValidAnalysisType(analysisType string) bool {
	validTypes := []string{entities.AnalysisTypeDiversification, entities.AnalysisTypeRisk, entities.AnalysisTypePerformance, entities.AnalysisTypeAllocation, entities.AnalysisTypeRebalancing}
	for _, valid := range validTypes {
		if analysisType == valid {
			return true
		}
	}
	return false
}

func (h *IntegrationHandlers) extractInsights(content string, analysisType string) []AnalysisInsight {
	insights := []AnalysisInsight{}
	switch analysisType {
	case entities.AnalysisTypeRisk:
		insights = append(insights, AnalysisInsight{Type: "risk", Title: "Portfolio Risk Level", Description: "Your portfolio maintains a moderate risk profile with good diversification", Impact: "medium", Confidence: 0.85})
	case entities.AnalysisTypePerformance:
		insights = append(insights, AnalysisInsight{Type: "performance", Title: "Strong Performance", Description: "Your portfolio has outperformed market benchmarks this period", Impact: "high", Confidence: 0.92})
	case entities.AnalysisTypeDiversification:
		insights = append(insights, AnalysisInsight{Type: "diversification", Title: "Well Diversified", Description: "Your portfolio shows good diversification across asset classes", Impact: "medium", Confidence: 0.78})
	case entities.AnalysisTypeAllocation:
		insights = append(insights, AnalysisInsight{Type: "allocation", Title: "Allocation Drift", Description: "Some positions may benefit from rebalancing to maintain target allocation", Impact: "medium", Confidence: 0.75})
	}
	return insights
}

func (h *IntegrationHandlers) extractRecommendations(content string) []string {
	return []string{
		"Monitor technology sector exposure and consider rebalancing if it exceeds 60% of portfolio",
		"Review quarterly performance metrics to ensure alignment with long-term goals",
		"Consider adding defensive positions if market volatility increases",
		"Maintain current diversification strategy as it provides good risk-adjusted returns",
	}
}


