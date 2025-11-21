//go:build integration
// +build integration

package integration

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stack-service/stack_service/internal/domain/entities"
	"github.com/stack-service/stack_service/internal/domain/services"
	"github.com/stack-service/stack_service/internal/infrastructure/config"
	"github.com/stack-service/stack_service/internal/infrastructure/zerog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

type fakeEmailMessage struct {
	To      string
	Subject string
	HTML    string
	Text    string
}

type fakeEmailService struct {
	messages []fakeEmailMessage
}

func (f *fakeEmailService) SendCustomEmail(_ context.Context, to, subject, htmlContent, textContent string) error {
	f.messages = append(f.messages, fakeEmailMessage{
		To:      to,
		Subject: subject,
		HTML:    htmlContent,
		Text:    textContent,
	})
	return nil
}

// TestAICFOIntegration tests the complete AI-CFO functionality end-to-end
func TestAICFOIntegration(t *testing.T) {
	// Skip integration tests if not explicitly enabled
	if testing.Short() {
		t.Skip("Skipping AI-CFO integration test in short mode")
	}

	logger := zaptest.NewLogger(t)
	ctx := context.Background()

	t.Run("Weekly Summary Generation Flow", func(t *testing.T) {
		testWeeklySummaryFlow(t, ctx, logger)
	})

	t.Run("On-Demand Analysis Flow", func(t *testing.T) {
		testOnDemandAnalysisFlow(t, ctx, logger)
	})

	// TODO: Uncomment when prompts package is implemented
	// t.Run("Prompt Template System", func(t *testing.T) {
	// 	testPromptTemplateSystem(t, ctx, logger)
	// })
}

// testWeeklySummaryFlow tests the complete weekly summary generation
func testWeeklySummaryFlow(t *testing.T, ctx context.Context, logger *zap.Logger) {
	// Create mock repositories
	mockAISummariesRepo := &MockAISummariesRepo{}
	mockPortfolioRepo := &MockPortfolioRepo{}
	mockPositionsRepo := &MockPositionsRepo{}
	mockBalanceRepo := &MockBalanceRepo{}
	mockUserRepo := &MockUserRepo{}

	// Create mock 0G components
	mockInferenceGateway := &MockInferenceGateway{}
	mockStorageClient := &MockStorageClient{}

	// Create a real namespace manager with mock components
	namespaces := &config.ZeroGNamespaces{
		AISummaries:  "ai-summaries/",
		AIArtifacts:  "ai-artifacts/",
		ModelPrompts: "model-prompts/",
	}
	mockNamespaceManager := zerog.NewNamespaceManager(mockStorageClient, namespaces, logger)

	mockEmailService := &fakeEmailService{}

	// Create notification service
	notificationService, err := services.NewNotificationService(mockEmailService, logger)
	require.NoError(t, err)

	// Create AI-CFO service
	aicfoService, err := services.NewAICfoService(
		mockInferenceGateway,
		mockStorageClient,
		mockNamespaceManager,
		notificationService,
		mockPortfolioRepo,
		mockPositionsRepo,
		mockBalanceRepo,
		mockAISummariesRepo,
		mockUserRepo,
		logger,
	)
	require.NoError(t, err)

	// Test data
	userID := uuid.New()
	weekStart := time.Now().Truncate(24 * time.Hour)

	// Test weekly summary generation
	summary, err := aicfoService.GenerateWeeklySummary(ctx, userID, weekStart)
	require.NoError(t, err)
	assert.NotNil(t, summary)
	assert.Equal(t, userID, summary.UserID)
	assert.Equal(t, weekStart, summary.WeekStart)
	assert.NotEmpty(t, summary.SummaryMD)

	// Verify the summary was stored
	assert.True(t, mockAISummariesRepo.CreateCalled)

	// Test retrieving the latest summary
	latestSummary, err := aicfoService.GetLatestSummary(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, summary.ID, latestSummary.ID)
}

// testOnDemandAnalysisFlow tests on-demand analysis functionality
func testOnDemandAnalysisFlow(t *testing.T, ctx context.Context, logger *zap.Logger) {
	// Create mock components
	mockInferenceGateway := &MockInferenceGateway{}
	mockStorageClient := &MockStorageClient{}

	// Create a real namespace manager with mock components
	namespaces := &config.ZeroGNamespaces{
		AISummaries:  "ai-summaries/",
		AIArtifacts:  "ai-artifacts/",
		ModelPrompts: "model-prompts/",
	}
	mockNamespaceManager := zerog.NewNamespaceManager(mockStorageClient, namespaces, logger)

	mockEmailService := &fakeEmailService{}

	// Create notification service
	notificationService, err := services.NewNotificationService(mockEmailService, logger)
	require.NoError(t, err)

	// Create AI-CFO service (minimal setup for analysis testing)
	aicfoService, err := services.NewAICfoService(
		mockInferenceGateway,
		mockStorageClient,
		mockNamespaceManager,
		notificationService,
		&MockPortfolioRepo{},
		&MockPositionsRepo{},
		&MockBalanceRepo{},
		&MockAISummariesRepo{},
		&MockUserRepo{},
		logger,
	)
	require.NoError(t, err)

	// Test data
	userID := uuid.New()
	analysisTypes := []string{
		entities.AnalysisTypeRisk,
		entities.AnalysisTypePerformance,
		entities.AnalysisTypeDiversification,
		entities.AnalysisTypeAllocation,
		entities.AnalysisTypeRebalancing,
	}

	// Test each analysis type
	for _, analysisType := range analysisTypes {
		t.Run(fmt.Sprintf("Analysis_%s", analysisType), func(t *testing.T) {
			result, err := aicfoService.PerformOnDemandAnalysis(ctx, userID, analysisType, map[string]interface{}{})
			require.NoError(t, err)
			assert.NotNil(t, result)
			assert.Equal(t, analysisType, result.Metadata["analysis_type"])
			assert.NotEmpty(t, result.Content)
			assert.Equal(t, "text/markdown", result.ContentType)
		})
	}
}

// testPromptTemplateSystem tests the prompt template generation
func testPromptTemplateSystem(t *testing.T, ctx context.Context, logger *zap.Logger) {
	templateManager := prompts.NewTemplateManager()

	// Test weekly summary prompt generation
	t.Run("Weekly Summary Prompts", func(t *testing.T) {
		weeklyContext := &prompts.WeeklySummaryContext{
			UserID:    uuid.New(),
			WeekStart: time.Now().Truncate(24 * time.Hour),
			WeekEnd:   time.Now().Truncate(24*time.Hour).AddDate(0, 0, 6),
			Portfolio: &entities.PortfolioMetrics{
				TotalValue:     100000.0,
				TotalReturnPct: 5.2,
				WeekChangePct:  1.3,
				DayChangePct:   0.2,
				MonthChangePct: 3.8,
				Positions: []entities.PositionMetrics{
					{
						BasketName:      "Tech Growth",
						Weight:          0.6,
						UnrealizedPLPct: 8.5,
						CurrentValue:    60000.0,
					},
					{
						BasketName:      "Balanced Fund",
						Weight:          0.4,
						UnrealizedPLPct: 2.1,
						CurrentValue:    40000.0,
					},
				},
			},
			Preferences: &entities.UserPreferences{
				RiskTolerance:  "moderate",
				PreferredStyle: "detailed",
				FocusAreas:     []string{"performance", "risk"},
			},
			MarketContext: prompts.CreateDefaultMarketContext(),
		}

		// Validate context
		err := prompts.ValidateWeeklySummaryContext(weeklyContext)
		require.NoError(t, err)

		// Generate prompts
		systemPrompt, userPrompt, err := templateManager.GenerateWeeklySummaryPrompt(weeklyContext)
		require.NoError(t, err)
		assert.NotEmpty(t, systemPrompt)
		assert.NotEmpty(t, userPrompt)
		assert.Contains(t, systemPrompt, "Chief Financial Officer")
		assert.Contains(t, userPrompt, "Tech Growth")
		assert.Contains(t, userPrompt, "$100000.00")
	})

	// Test on-demand analysis prompts
	analysisTypes := []string{
		entities.AnalysisTypeRisk,
		entities.AnalysisTypePerformance,
		entities.AnalysisTypeDiversification,
	}

	for _, analysisType := range analysisTypes {
		t.Run(fmt.Sprintf("Analysis_%s_Prompts", analysisType), func(t *testing.T) {
			analysisContext := &prompts.OnDemandAnalysisContext{
				UserID:       uuid.New(),
				AnalysisType: analysisType,
				Portfolio: &entities.PortfolioMetrics{
					TotalValue:     75000.0,
					TotalReturnPct: 3.5,
				},
				Preferences: &entities.UserPreferences{
					RiskTolerance: "conservative",
				},
				MarketContext: prompts.CreateDefaultMarketContext(),
			}

			// Validate context
			err := prompts.ValidateAnalysisContext(analysisContext)
			require.NoError(t, err)

			// Generate prompts
			systemPrompt, userPrompt, err := templateManager.GenerateOnDemandAnalysisPrompt(analysisContext)
			require.NoError(t, err)
			assert.NotEmpty(t, systemPrompt)
			assert.NotEmpty(t, userPrompt)
			assert.Contains(t, systemPrompt, analysisType)
			assert.Contains(t, userPrompt, "$75000.00")
		})
	}
}

// Mock implementations for testing

type MockAISummariesRepo struct {
	CreateCalled    bool
	GetLatestCalled bool
	GetByWeekCalled bool
	summaries       map[string]*services.AISummary
}

func (m *MockAISummariesRepo) CreateSummary(ctx context.Context, summary *services.AISummary) error {
	m.CreateCalled = true
	if m.summaries == nil {
		m.summaries = make(map[string]*services.AISummary)
	}
	m.summaries[summary.ID.String()] = summary
	return nil
}

func (m *MockAISummariesRepo) GetLatestSummary(ctx context.Context, userID uuid.UUID) (*services.AISummary, error) {
	m.GetLatestCalled = true
	// Return the first summary found for simplicity
	for _, summary := range m.summaries {
		if summary.UserID == userID {
			return summary, nil
		}
	}
	return nil, fmt.Errorf("no summaries found for user")
}

func (m *MockAISummariesRepo) GetSummaryByWeek(ctx context.Context, userID uuid.UUID, weekStart time.Time) (*services.AISummary, error) {
	m.GetByWeekCalled = true
	return nil, sql.ErrNoRows // Simulate no existing summary
}

func (m *MockAISummariesRepo) UpdateSummary(ctx context.Context, summary *services.AISummary) error {
	return nil
}

type MockPortfolioRepo struct{}

func (m *MockPortfolioRepo) GetPortfolioPerformance(ctx context.Context, userID uuid.UUID, startDate, endDate time.Time) ([]*entities.PerformancePoint, error) {
	return []*entities.PerformancePoint{
		{Date: startDate, Value: 95000.0, PnL: -5000.0},
		{Date: endDate, Value: 100000.0, PnL: 0.0},
	}, nil
}

func (m *MockPortfolioRepo) GetPortfolioValue(ctx context.Context, userID uuid.UUID, date time.Time) (decimal.Decimal, error) {
	return decimal.NewFromFloat(100000.0), nil
}

type MockPositionsRepo struct{}

func (m *MockPositionsRepo) GetPositionsByUser(ctx context.Context, userID uuid.UUID) ([]*services.Position, error) {
	return []*services.Position{
		{
			ID:          uuid.New(),
			UserID:      userID,
			BasketName:  "Tech Growth",
			Quantity:    decimal.NewFromFloat(100.0),
			MarketValue: decimal.NewFromFloat(60000.0),
		},
		{
			ID:          uuid.New(),
			UserID:      userID,
			BasketName:  "Balanced Fund",
			Quantity:    decimal.NewFromFloat(200.0),
			MarketValue: decimal.NewFromFloat(40000.0),
		},
	}, nil
}

func (m *MockPositionsRepo) GetPositionMetrics(ctx context.Context, userID uuid.UUID) (*entities.PortfolioMetrics, error) {
	return &entities.PortfolioMetrics{
		TotalValue:     100000.0,
		TotalReturnPct: 5.2,
		WeekChangePct:  1.3,
		DayChangePct:   0.2,
		MonthChangePct: 3.8,
		Positions: []entities.PositionMetrics{
			{
				BasketName:      "Tech Growth",
				Weight:          0.6,
				UnrealizedPLPct: 8.5,
				CurrentValue:    60000.0,
			},
			{
				BasketName:      "Balanced Fund",
				Weight:          0.4,
				UnrealizedPLPct: 2.1,
				CurrentValue:    40000.0,
			},
		},
		AllocationByBasket: map[string]float64{
			"Technology": 60.0,
			"Balanced":   40.0,
		},
		RiskMetrics: &entities.RiskMetrics{
			Volatility:      0.15,
			SharpeRatio:     0.8,
			MaxDrawdown:     0.10,
			Diversification: 0.75,
		},
	}, nil
}

type MockBalanceRepo struct{}

func (m *MockBalanceRepo) GetUserBalance(ctx context.Context, userID uuid.UUID) (*services.Balance, error) {
	return &services.Balance{
		UserID:      userID,
		BuyingPower: decimal.NewFromFloat(10000.0),
		Currency:    "USD",
	}, nil
}

type MockUserRepo struct{}

func (m *MockUserRepo) GetUserPreferences(ctx context.Context, userID uuid.UUID) (*entities.UserPreferences, error) {
	return &entities.UserPreferences{
		RiskTolerance:  "moderate",
		PreferredStyle: "detailed",
		FocusAreas:     []string{"performance", "risk"},
		Language:       "en",
		NotificationSettings: map[string]bool{
			"weekly_summaries": true,
			"risk_alerts":      true,
		},
	}, nil
}

type MockInferenceGateway struct{}

func (m *MockInferenceGateway) GenerateWeeklySummary(ctx context.Context, request *entities.WeeklySummaryRequest) (*entities.InferenceResult, error) {
	return &entities.InferenceResult{
		RequestID:      uuid.New().String(),
		Content:        "# Weekly Portfolio Summary\n\nYour portfolio performed well this week with a 1.3% gain...",
		ContentType:    "text/markdown",
		TokensUsed:     150,
		ProcessingTime: 2 * time.Second,
		Model:          "gpt-3.5-turbo",
		CreatedAt:      time.Now(),
		Metadata: map[string]interface{}{
			"task_type": "weekly_summary",
			"model":     "gpt-3.5-turbo",
		},
	}, nil
}

func (m *MockInferenceGateway) AnalyzeOnDemand(ctx context.Context, request *entities.AnalysisRequest) (*entities.InferenceResult, error) {
	return &entities.InferenceResult{
		RequestID:      uuid.New().String(),
		Content:        fmt.Sprintf("# %s Analysis\n\nDetailed analysis of your portfolio...", request.AnalysisType),
		ContentType:    "text/markdown",
		TokensUsed:     120,
		ProcessingTime: 1500 * time.Millisecond,
		Model:          "gpt-3.5-turbo",
		CreatedAt:      time.Now(),
		Metadata: map[string]interface{}{
			"task_type":     "on_demand_analysis",
			"analysis_type": request.AnalysisType,
		},
	}, nil
}

func (m *MockInferenceGateway) HealthCheck(ctx context.Context) (*entities.HealthStatus, error) {
	return &entities.HealthStatus{
		Status:  entities.HealthStatusHealthy,
		Latency: 50 * time.Millisecond,
	}, nil
}

func (m *MockInferenceGateway) GetServiceInfo(ctx context.Context) (*entities.ServiceInfo, error) {
	return &entities.ServiceInfo{
		ProviderID:  "mock-provider",
		ServiceName: "Mock Inference Service",
		Status:      "active",
	}, nil
}

type MockStorageClient struct{}

func (m *MockStorageClient) Store(ctx context.Context, namespace string, data []byte, metadata map[string]string) (*entities.StorageResult, error) {
	return &entities.StorageResult{
		URI:      "mock://storage/test-artifact",
		Hash:     "abc123",
		Size:     int64(len(data)),
		StoredAt: time.Now(),
	}, nil
}

func (m *MockStorageClient) Retrieve(ctx context.Context, uri string) (*entities.StorageData, error) {
	return &entities.StorageData{
		Data:     []byte("mock data"),
		URI:      uri,
		Size:     9,
		StoredAt: time.Now(),
	}, nil
}

func (m *MockStorageClient) HealthCheck(ctx context.Context) (*entities.HealthStatus, error) {
	return &entities.HealthStatus{Status: entities.HealthStatusHealthy}, nil
}

func (m *MockStorageClient) ListObjects(ctx context.Context, namespace string, prefix string) ([]entities.StorageObject, error) {
	return []entities.StorageObject{}, nil
}

func (m *MockStorageClient) Delete(ctx context.Context, uri string) error {
	return nil
}

type MockNamespaceManager struct{}

func (m *MockNamespaceManager) AISummaries() interface{} {
	return &MockStorageNamespace{}
}

type MockStorageNamespace struct{}

func (m *MockStorageNamespace) StoreWeeklySummary(ctx context.Context, userID string, weekStart time.Time, data []byte, contentType string) (*entities.StorageResult, error) {
	return &entities.StorageResult{
		URI:      "mock://storage/summary",
		Size:     int64(len(data)),
		StoredAt: time.Now(),
	}, nil
}
