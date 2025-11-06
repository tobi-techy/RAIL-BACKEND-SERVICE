package services

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stack-service/stack_service/internal/domain/entities"
	"github.com/stack-service/stack_service/internal/infrastructure/zerog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// AICfoService provides AI-powered CFO functionality
type AICfoService struct {
	inferenceGateway   entities.ZeroGInferenceGateway
	storageClient      entities.ZeroGStorageClient
	namespaceManager   *zerog.NamespaceManager
	notificationService *NotificationService
	
	// Repositories for data access
	portfolioRepo     PortfolioRepository
	positionsRepo     PositionsRepository
	balanceRepo       BalanceRepository
	aiSummariesRepo   AISummariesRepository
	userRepo          UserRepository
	
	logger            *zap.Logger
	tracer            trace.Tracer
	metrics           *AICfoMetrics
}

// Repository interfaces
type PortfolioRepository interface {
	GetPortfolioPerformance(ctx context.Context, userID uuid.UUID, startDate, endDate time.Time) ([]*entities.PerformancePoint, error)
	GetPortfolioValue(ctx context.Context, userID uuid.UUID, date time.Time) (decimal.Decimal, error)
}

type PositionsRepository interface {
	GetPositionsByUser(ctx context.Context, userID uuid.UUID) ([]*Position, error)
	GetPositionMetrics(ctx context.Context, userID uuid.UUID) (*entities.PortfolioMetrics, error)
}

type BalanceRepository interface {
	GetUserBalance(ctx context.Context, userID uuid.UUID) (*Balance, error)
}

type AISummariesRepository interface {
	Create(ctx context.Context, summary *AISummary) error
	GetLatestByUserID(ctx context.Context, userID uuid.UUID) (*AISummary, error)
	GetByUserAndWeek(ctx context.Context, userID uuid.UUID, weekStart time.Time) (*AISummary, error)
	Update(ctx context.Context, summary *AISummary) error
}

type UserRepository interface {
	GetUserPreferences(ctx context.Context, userID uuid.UUID) (*entities.UserPreferences, error)
}

// Domain models
type Position struct {
	ID           uuid.UUID       `json:"id"`
	UserID       uuid.UUID       `json:"user_id"`
	BasketID     uuid.UUID       `json:"basket_id"`
	BasketName   string          `json:"basket_name"`
	Quantity     decimal.Decimal `json:"quantity"`
	AvgPrice     decimal.Decimal `json:"avg_price"`
	MarketValue  decimal.Decimal `json:"market_value"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

type Balance struct {
	UserID         uuid.UUID       `json:"user_id"`
	BuyingPower    decimal.Decimal `json:"buying_power"`
	PendingDeposits decimal.Decimal `json:"pending_deposits"`
	Currency       string          `json:"currency"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

type AISummary struct {
	ID          uuid.UUID  `json:"id"`
	UserID      uuid.UUID  `json:"user_id"`
	WeekStart   time.Time  `json:"week_start"`
	SummaryMD   string     `json:"summary_md"`
	ArtifactURI string     `json:"artifact_uri"`
	CreatedAt   time.Time  `json:"created_at"`
}

// AICfoMetrics contains observability metrics for the AI-CFO service
type AICfoMetrics struct {
	SummariesGenerated   metric.Int64Counter
	AnalysesPerformed    metric.Int64Counter
	ProcessingDuration   metric.Float64Histogram
	ErrorsTotal          metric.Int64Counter
	ActiveUsers          metric.Int64Gauge
}

// NewAICfoService creates a new AI-CFO service
func NewAICfoService(
	inferenceGateway entities.ZeroGInferenceGateway,
	storageClient entities.ZeroGStorageClient,
	namespaceManager *zerog.NamespaceManager,
	notificationService *NotificationService,
	portfolioRepo PortfolioRepository,
	positionsRepo PositionsRepository,
	balanceRepo BalanceRepository,
	aiSummariesRepo AISummariesRepository,
	userRepo UserRepository,
	logger *zap.Logger,
) (*AICfoService, error) {
	
	tracer := otel.Tracer("aicfo-service")
	meter := otel.Meter("aicfo-service")

	// Initialize metrics
	metrics, err := initAICfoMetrics(meter)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize metrics: %w", err)
	}

	service := &AICfoService{
		inferenceGateway:   inferenceGateway,
		storageClient:      storageClient,
		namespaceManager:   namespaceManager,
		notificationService: notificationService,
		portfolioRepo:      portfolioRepo,
		positionsRepo:      positionsRepo,
		balanceRepo:        balanceRepo,
		aiSummariesRepo:    aiSummariesRepo,
		userRepo:           userRepo,
		logger:             logger,
		tracer:             tracer,
		metrics:            metrics,
	}

	logger.Info("AI-CFO service initialized successfully")
	return service, nil
}

// GenerateWeeklySummary generates an AI-powered weekly portfolio summary
func (s *AICfoService) GenerateWeeklySummary(ctx context.Context, userID uuid.UUID, weekStart time.Time) (*AISummary, error) {
	startTime := time.Now()
	ctx, span := s.tracer.Start(ctx, "aicfo.generate_weekly_summary", trace.WithAttributes(
		attribute.String("user_id", userID.String()),
		attribute.String("week_start", weekStart.Format("2006-01-02")),
	))
	defer span.End()

	s.logger.Info("Generating weekly summary",
		zap.String("user_id", userID.String()),
		zap.String("week_start", weekStart.Format("2006-01-02")),
	)

	// Check if summary already exists
	existingSummary, err := s.aiSummariesRepo.GetByUserAndWeek(ctx, userID, weekStart)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("failed to check existing summary: %w", err)
	}
	if existingSummary != nil {
		s.logger.Info("Weekly summary already exists",
			zap.String("user_id", userID.String()),
			zap.String("week_start", weekStart.Format("2006-01-02")),
		)
		return existingSummary, nil
	}

	// Gather portfolio data
	portfolioData, err := s.gatherPortfolioData(ctx, userID, weekStart)
	if err != nil {
		span.RecordError(err)
		s.recordError(ctx, "weekly_summary", err)
		return nil, fmt.Errorf("failed to gather portfolio data: %w", err)
	}

	// Get user preferences
	preferences, err := s.userRepo.GetUserPreferences(ctx, userID)
	if err != nil {
		s.logger.Warn("Failed to get user preferences, using defaults", zap.Error(err))
		preferences = s.getDefaultPreferences()
	}

	// Get previous week's data for comparison
	previousWeekStart := weekStart.AddDate(0, 0, -7)
	previousWeekData, err := s.gatherPortfolioData(ctx, userID, previousWeekStart)
	if err != nil {
		s.logger.Debug("No previous week data available", zap.Error(err))
		previousWeekData = nil
	}

	// Build inference request
	weekEnd := weekStart.AddDate(0, 0, 6)
	request := &entities.WeeklySummaryRequest{
		UserID:        userID,
		WeekStart:     weekStart,
		WeekEnd:       weekEnd,
		PortfolioData: portfolioData,
		Preferences:   preferences,
	}

	if previousWeekData != nil {
		request.PreviousWeek = &entities.WeeklySummaryRequest{
			UserID:        userID,
			WeekStart:     previousWeekStart,
			WeekEnd:       previousWeekStart.AddDate(0, 0, 6),
			PortfolioData: previousWeekData,
			Preferences:   preferences,
		}
	}

	// Generate summary using AI inference
	result, err := s.inferenceGateway.GenerateWeeklySummary(ctx, request)
	if err != nil {
		span.RecordError(err)
		s.recordError(ctx, "weekly_summary", err)
		return nil, fmt.Errorf("failed to generate summary: %w", err)
	}

	// Store summary in database
	summary := &AISummary{
		ID:          uuid.New(),
		UserID:      userID,
		WeekStart:   weekStart,
		SummaryMD:   result.Content,
		ArtifactURI: result.ArtifactURI,
		CreatedAt:   time.Now(),
	}

	if err := s.aiSummariesRepo.Create(ctx, summary); err != nil {
		span.RecordError(err)
		s.recordError(ctx, "weekly_summary", err)
		return nil, fmt.Errorf("failed to store summary: %w", err)
	}

	// Store additional summary data in 0G storage
	if err := s.storeSummaryInStorage(ctx, summary, result); err != nil {
		s.logger.Warn("Failed to store summary in 0G storage", zap.Error(err))
		// Don't fail the entire operation
	}

	// Send notification to user if notification service is available
	if s.notificationService != nil {
		if err := s.sendWeeklySummaryNotification(ctx, userID, summary); err != nil {
			s.logger.Warn("Failed to send weekly summary notification", zap.Error(err))
			// Don't fail the entire operation
		}
	}

	// Record success metrics
	duration := time.Since(startTime)
	s.metrics.SummariesGenerated.Add(ctx, 1)
	s.metrics.ProcessingDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(
		attribute.String("operation", "weekly_summary"),
		attribute.Bool("success", true),
	))

	s.logger.Info("Weekly summary generated successfully",
		zap.String("user_id", userID.String()),
		zap.String("summary_id", summary.ID.String()),
		zap.Duration("duration", duration),
	)

	return summary, nil
}

// PerformOnDemandAnalysis performs on-demand portfolio analysis
func (s *AICfoService) PerformOnDemandAnalysis(ctx context.Context, userID uuid.UUID, analysisType string, parameters map[string]interface{}) (*entities.InferenceResult, error) {
	startTime := time.Now()
	ctx, span := s.tracer.Start(ctx, "aicfo.perform_analysis", trace.WithAttributes(
		attribute.String("user_id", userID.String()),
		attribute.String("analysis_type", analysisType),
	))
	defer span.End()

	s.logger.Info("Performing on-demand analysis",
		zap.String("user_id", userID.String()),
		zap.String("analysis_type", analysisType),
	)

	// Validate analysis type
	if !s.isValidAnalysisType(analysisType) {
		return nil, fmt.Errorf("invalid analysis type: %s", analysisType)
	}

	// Gather current portfolio data
	portfolioData, err := s.gatherPortfolioData(ctx, userID, time.Now())
	if err != nil {
		span.RecordError(err)
		s.recordError(ctx, "on_demand_analysis", err)
		return nil, fmt.Errorf("failed to gather portfolio data: %w", err)
	}

	// Get user preferences
	preferences, err := s.userRepo.GetUserPreferences(ctx, userID)
	if err != nil {
		s.logger.Warn("Failed to get user preferences, using defaults", zap.Error(err))
		preferences = s.getDefaultPreferences()
	}

	// Build analysis request
	request := &entities.AnalysisRequest{
		UserID:        userID,
		AnalysisType:  analysisType,
		PortfolioData: portfolioData,
		Preferences:   preferences,
		Parameters:    parameters,
	}

	// Perform analysis using AI inference
	result, err := s.inferenceGateway.AnalyzeOnDemand(ctx, request)
	if err != nil {
		span.RecordError(err)
		s.recordError(ctx, "on_demand_analysis", err)
		return nil, fmt.Errorf("failed to perform analysis: %w", err)
	}

	// Record success metrics
	duration := time.Since(startTime)
	s.metrics.AnalysesPerformed.Add(ctx, 1, metric.WithAttributes(
		attribute.String("analysis_type", analysisType),
	))
	s.metrics.ProcessingDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(
		attribute.String("operation", "on_demand_analysis"),
		attribute.String("analysis_type", analysisType),
		attribute.Bool("success", true),
	))

	s.logger.Info("On-demand analysis completed successfully",
		zap.String("user_id", userID.String()),
		zap.String("analysis_type", analysisType),
		zap.String("request_id", result.RequestID),
		zap.Duration("duration", duration),
	)

	return result, nil
}

// GetLatestSummary retrieves the latest weekly summary for a user
func (s *AICfoService) GetLatestSummary(ctx context.Context, userID uuid.UUID) (*AISummary, error) {
	ctx, span := s.tracer.Start(ctx, "aicfo.get_latest_summary", trace.WithAttributes(
		attribute.String("user_id", userID.String()),
	))
	defer span.End()

	summary, err := s.aiSummariesRepo.GetLatestByUserID(ctx, userID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("no summaries found for user")
		}
		span.RecordError(err)
		return nil, fmt.Errorf("failed to get latest summary: %w", err)
	}

	return summary, nil
}

// gatherPortfolioData aggregates portfolio performance metrics for a user
func (s *AICfoService) gatherPortfolioData(ctx context.Context, userID uuid.UUID, weekStart time.Time) (*entities.PortfolioMetrics, error) {
	ctx, span := s.tracer.Start(ctx, "aicfo.gather_portfolio_data", trace.WithAttributes(
		attribute.String("user_id", userID.String()),
		attribute.String("week_start", weekStart.Format("2006-01-02")),
	))
	defer span.End()

	// Get positions metrics
	portfolioMetrics, err := s.positionsRepo.GetPositionMetrics(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get position metrics: %w", err)
	}

	// Get historical performance data
	weekEnd := weekStart.AddDate(0, 0, 6)
	performanceHistory, err := s.portfolioRepo.GetPortfolioPerformance(ctx, userID, weekStart, weekEnd)
	if err != nil {
		s.logger.Warn("Failed to get performance history", zap.Error(err))
		// Continue without historical data
		performanceHistory = []*entities.PerformancePoint{}
	}

valPerformanceHistory := make([]entities.PerformancePoint, len(performanceHistory))
	for i, p := range performanceHistory {
		valPerformanceHistory[i] = *p
	}
	portfolioMetrics.PerformanceHistory = valPerformanceHistory

	// Calculate additional metrics
	if err := s.calculateRiskMetrics(ctx, portfolioMetrics); err != nil {
		s.logger.Warn("Failed to calculate risk metrics", zap.Error(err))
		// Set default risk metrics
		portfolioMetrics.RiskMetrics = &entities.RiskMetrics{
			Volatility:      0.15,
			Beta:           1.0,
			SharpeRatio:    0.8,
			MaxDrawdown:    0.10,
			VaR:            0.05,
			Diversification: 0.75,
		}
	}

	return portfolioMetrics, nil
}

// calculateRiskMetrics computes risk analysis for the portfolio
func (s *AICfoService) calculateRiskMetrics(ctx context.Context, metrics *entities.PortfolioMetrics) error {
	if len(metrics.PerformanceHistory) < 2 {
		return fmt.Errorf("insufficient performance history for risk calculation")
	}

	// Calculate volatility (standard deviation of returns)
	returns := make([]float64, len(metrics.PerformanceHistory)-1)
	for i := 1; i < len(metrics.PerformanceHistory); i++ {
		prev := metrics.PerformanceHistory[i-1].Value
		curr := metrics.PerformanceHistory[i].Value
		if prev != 0 {
			returns[i-1] = (curr - prev) / prev
		}
	}

	volatility := s.calculateStandardDeviation(returns)
	
	// Calculate maximum drawdown
	maxDrawdown := s.calculateMaxDrawdown(metrics.PerformanceHistory)

	// Calculate diversification score based on position weights
	diversification := s.calculateDiversificationScore(metrics.Positions)

	// Estimate other metrics (in production, these would be more sophisticated)
	metrics.RiskMetrics = &entities.RiskMetrics{
		Volatility:      volatility,
		Beta:           1.0, // TODO: Calculate actual beta against market
		SharpeRatio:    s.calculateSharpeRatio(returns, volatility),
		MaxDrawdown:    maxDrawdown,
		VaR:            volatility * 1.65, // 95% VaR approximation
		Diversification: diversification,
	}

	return nil
}

// calculateStandardDeviation computes the standard deviation of a slice of returns
func (s *AICfoService) calculateStandardDeviation(returns []float64) float64 {
	if len(returns) == 0 {
		return 0
	}

	// Calculate mean
	sum := 0.0
	for _, r := range returns {
		sum += r
	}
	mean := sum / float64(len(returns))

	// Calculate variance
	variance := 0.0
	for _, r := range returns {
		variance += math.Pow(r-mean, 2)
	}
	variance = variance / float64(len(returns))

	return math.Sqrt(variance)
}

// calculateMaxDrawdown computes the maximum drawdown from performance history
func (s *AICfoService) calculateMaxDrawdown(history []entities.PerformancePoint) float64 {
	if len(history) < 2 {
		return 0
	}

	maxValue := history[0].Value
	maxDrawdown := 0.0

	for _, point := range history[1:] {
		if point.Value > maxValue {
			maxValue = point.Value
		}
		
		drawdown := (maxValue - point.Value) / maxValue
		if drawdown > maxDrawdown {
			maxDrawdown = drawdown
		}
	}

	return maxDrawdown
}

// calculateDiversificationScore computes a diversification score based on position weights
func (s *AICfoService) calculateDiversificationScore(positions []entities.PositionMetrics) float64 {
	if len(positions) == 0 {
		return 0
	}

	// Calculate Herfindahl-Hirschman Index (HHI) and convert to diversification score
	hhi := 0.0
	for _, pos := range positions {
		hhi += pos.Weight * pos.Weight
	}

	// Convert HHI to diversification score (0-1, where 1 is perfectly diversified)
	maxHHI := 1.0 // All in one position
	minHHI := 1.0 / float64(len(positions)) // Equally weighted
	
	if maxHHI == minHHI {
		return 1.0
	}

	return (maxHHI - hhi) / (maxHHI - minHHI)
}

// calculateSharpeRatio computes the Sharpe ratio for the returns
func (s *AICfoService) calculateSharpeRatio(returns []float64, volatility float64) float64 {
	if len(returns) == 0 || volatility == 0 {
		return 0
	}

	// Calculate mean return
	sum := 0.0
	for _, r := range returns {
		sum += r
	}
	meanReturn := sum / float64(len(returns))

	// Risk-free rate (assume 2% annually, convert to period)
	riskFreeRate := 0.02 / 252 // Daily risk-free rate approximation

	return (meanReturn - riskFreeRate) / volatility
}

// storeSummaryInStorage stores the summary content in 0G storage
func (s *AICfoService) storeSummaryInStorage(ctx context.Context, summary *AISummary, result *entities.InferenceResult) error {
	summaryStorage := s.namespaceManager.AISummaries()
	
	summaryData := []byte(summary.SummaryMD)
	
	_, err := summaryStorage.StoreWeeklySummary(
		ctx,
		summary.UserID.String(),
		summary.WeekStart,
		summaryData,
		"weekly_summary",
	)
	if err != nil {
		return fmt.Errorf("failed to store summary in 0G storage: %w", err)
	}

	return nil
}

// getDefaultPreferences returns default user preferences
func (s *AICfoService) getDefaultPreferences() *entities.UserPreferences {
	return &entities.UserPreferences{
		RiskTolerance:  "moderate",
		PreferredStyle: "summary",
		FocusAreas:     []string{"performance", "risk", "allocation"},
		Language:       "en",
		NotificationSettings: map[string]bool{
			"weekly_summaries": true,
			"risk_alerts":      true,
			"rebalancing":      true,
		},
	}
}

// isValidAnalysisType checks if the analysis type is supported
func (s *AICfoService) isValidAnalysisType(analysisType string) bool {
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

// recordError records error metrics
func (s *AICfoService) recordError(ctx context.Context, operation string, err error) {
	var errorCode string
	if zeroGErr, ok := err.(*entities.ZeroGError); ok {
		errorCode = zeroGErr.Code
	} else {
		errorCode = "internal_error"
	}

	s.metrics.ErrorsTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("operation", operation),
		attribute.String("error_code", errorCode),
	))
}

// GetHealthStatus returns the health status of the AI-CFO service
func (s *AICfoService) GetHealthStatus(ctx context.Context) (*entities.HealthStatus, error) {
	ctx, span := s.tracer.Start(ctx, "aicfo.health_check")
	defer span.End()

	startTime := time.Now()
	errors := []string{}

	// Check inference gateway
	inferenceHealth, err := s.inferenceGateway.HealthCheck(ctx)
	if err != nil || inferenceHealth.Status != entities.HealthStatusHealthy {
		errors = append(errors, "inference gateway unhealthy")
	}

	// Check storage client
	storageHealth, err := s.storageClient.HealthCheck(ctx)
	if err != nil || storageHealth.Status != entities.HealthStatusHealthy {
		errors = append(errors, "storage client unhealthy")
	}

	// Determine overall status
	status := entities.HealthStatusHealthy
	if len(errors) > 0 {
		if len(errors) < 2 {
			status = entities.HealthStatusDegraded
		} else {
			status = entities.HealthStatusUnhealthy
		}
	}

	return &entities.HealthStatus{
		Status:      status,
		Latency:     time.Since(startTime),
		Version:     "1.0.0",
		Uptime:      24 * time.Hour, // TODO: Track actual uptime
		Metrics: map[string]interface{}{
			"inference_available": inferenceHealth != nil,
			"storage_available":   storageHealth != nil,
			"error_count":         len(errors),
		},
		LastChecked: time.Now(),
		Errors:      errors,
	}, nil
}

// sendWeeklySummaryNotification sends a notification for a new weekly summary
func (s *AICfoService) sendWeeklySummaryNotification(ctx context.Context, userID uuid.UUID, summary *AISummary) error {
	ctx, span := s.tracer.Start(ctx, "aicfo.send_notification")
	defer span.End()

	// Get user preferences to check if notifications are enabled
	preferences, err := s.userRepo.GetUserPreferences(ctx, userID)
	if err != nil {
		s.logger.Debug("Failed to get user preferences for notification, using defaults", zap.Error(err))
		preferences = s.getDefaultPreferences()
	}

	// Check if weekly summary notifications are enabled
	if !preferences.NotificationSettings["weekly_summaries"] {
		s.logger.Debug("Weekly summary notifications disabled for user",
			zap.String("user_id", userID.String()),
		)
		return nil
	}

	// Send notification using the simple notification service
	message := fmt.Sprintf("Weekly Summary Ready - %s. Your AI-powered portfolio analysis is now available.", summary.WeekStart.Format("Jan 2, 2006"))
	
	if err := s.notificationService.NotifyOffRampSuccess(ctx, userID, message); err != nil {
		s.logger.Warn("Failed to send notification", zap.Error(err))
		// Don't fail for notification errors
	}

	s.logger.Info("Weekly summary notifications sent successfully",
		zap.String("user_id", userID.String()),
		zap.String("summary_id", summary.ID.String()),
		zap.String("week_start", summary.WeekStart.Format("2006-01-02")),
	)

	return nil
}

// initAICfoMetrics initializes OpenTelemetry metrics for the AI-CFO service
func initAICfoMetrics(meter metric.Meter) (*AICfoMetrics, error) {
	summariesGenerated, err := meter.Int64Counter("aicfo_summaries_generated_total",
		metric.WithDescription("Total number of weekly summaries generated"))
	if err != nil {
		return nil, err
	}

	analysesPerformed, err := meter.Int64Counter("aicfo_analyses_performed_total",
		metric.WithDescription("Total number of on-demand analyses performed"))
	if err != nil {
		return nil, err
	}

	processingDuration, err := meter.Float64Histogram("aicfo_processing_duration_seconds",
		metric.WithDescription("Duration of AI-CFO operations in seconds"))
	if err != nil {
		return nil, err
	}

	errorsTotal, err := meter.Int64Counter("aicfo_errors_total",
		metric.WithDescription("Total number of AI-CFO errors"))
	if err != nil {
		return nil, err
	}

	activeUsers, err := meter.Int64Gauge("aicfo_active_users",
		metric.WithDescription("Number of users with recent AI-CFO activity"))
	if err != nil {
		return nil, err
	}

	return &AICfoMetrics{
		SummariesGenerated: summariesGenerated,
		AnalysesPerformed:  analysesPerformed,
		ProcessingDuration: processingDuration,
		ErrorsTotal:        errorsTotal,
		ActiveUsers:        activeUsers,
	}, nil
}