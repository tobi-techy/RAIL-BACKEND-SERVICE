package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/logger"
)

// Mock implementations
type MockLedgerService struct {
	mock.Mock
}

func (m *MockLedgerService) GetSystemBufferBalance(ctx context.Context, accountType string) (decimal.Decimal, error) {
	args := m.Called(ctx, accountType)
	return args.Get(0).(decimal.Decimal), args.Error(1)
}

func (m *MockLedgerService) GetTotalUserFiatExposure(ctx context.Context) (decimal.Decimal, error) {
	args := m.Called(ctx)
	return args.Get(0).(decimal.Decimal), args.Error(1)
}

type MockCircleClient struct {
	mock.Mock
}

func (m *MockCircleClient) GetTotalUSDCBalance(ctx context.Context) (decimal.Decimal, error) {
	args := m.Called(ctx)
	return args.Get(0).(decimal.Decimal), args.Error(1)
}

type MockAlpacaClient struct {
	mock.Mock
}

func (m *MockAlpacaClient) GetTotalBuyingPower(ctx context.Context) (decimal.Decimal, error) {
	args := m.Called(ctx)
	return args.Get(0).(decimal.Decimal), args.Error(1)
}

type MockMetricsService struct {
	mock.Mock
}

func (m *MockMetricsService) RecordReconciliationRun(runType string) {
	m.Called(runType)
}

func (m *MockMetricsService) RecordReconciliationCompleted(runType string, total, passed, failed, exceptions int) {
	m.Called(runType, total, passed, failed, exceptions)
}

func (m *MockMetricsService) RecordCheckResult(checkType string, passed bool, duration time.Duration) {
	m.Called(checkType, passed, duration)
}

func (m *MockMetricsService) RecordExceptionAutoCorrected(checkType string) {
	m.Called(checkType)
}

func (m *MockMetricsService) RecordDiscrepancyAmount(checkType string, amount decimal.Decimal) {
	m.Called(checkType, amount)
}

func (m *MockMetricsService) RecordReconciliationAlert(checkType, severity string) {
	m.Called(checkType, severity)
}

// Helper function to create test service
func setupTestService(t *testing.T) (*Service, *mocks.MockReconciliationRepository, *MockLedgerService, *MockCircleClient, *MockAlpacaClient) {
	reconciliationRepo := new(mocks.MockReconciliationRepository)
	ledgerRepo := new(mocks.MockLedgerRepository)
	depositRepo := new(mocks.MockDepositRepository)
	withdrawalRepo := new(mocks.MockWithdrawalRepository)
	conversionRepo := new(mocks.MockConversionRepository)
	ledgerService := new(MockLedgerService)
	circleClient := new(MockCircleClient)
	alpacaClient := new(MockAlpacaClient)
	metricsService := new(MockMetricsService)

	// Setup default mock expectations for metrics (called often)
	metricsService.On("RecordReconciliationRun", mock.Anything).Return()
	metricsService.On("RecordReconciliationCompleted", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()
	metricsService.On("RecordCheckResult", mock.Anything, mock.Anything, mock.Anything).Return()

	testLogger := logger.New("debug", "test")

	config := &Config{
		AutoCorrectLowSeverity: true,
		ToleranceCircle:        decimal.NewFromFloat(10.0),
		ToleranceAlpaca:        decimal.NewFromFloat(100.0),
		EnableAlerting:         true,
	}

	service := &Service{
		reconciliationRepo: reconciliationRepo,
		ledgerRepo:         ledgerRepo,
		depositRepo:        depositRepo,
		withdrawalRepo:     withdrawalRepo,
		conversionRepo:     conversionRepo,
		ledgerService:      ledgerService,
		circleClient:       circleClient,
		alpacaClient:       alpacaClient,
		logger:             testLogger,
		metricsService:     metricsService,
		config:             config,
	}

	return service, reconciliationRepo, ledgerService, circleClient, alpacaClient
}

func TestService_RunReconciliation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		runType        string
		setupMocks     func(*mocks.MockReconciliationRepository, *MockLedgerService, *MockCircleClient, *MockAlpacaClient)
		expectError    bool
		expectedPassed int
		expectedFailed int
	}{
		{
			name:    "successful reconciliation with no exceptions",
			runType: "hourly",
			setupMocks: func(repo *mocks.MockReconciliationRepository, ledger *MockLedgerService, circle *MockCircleClient, alpaca *MockAlpacaClient) {
				repo.On("CreateReport", mock.Anything, mock.AnythingOfType("*entities.ReconciliationReport")).Return(nil)
				repo.On("CreateCheck", mock.Anything, mock.AnythingOfType("*entities.ReconciliationCheck")).Return(nil)
				repo.On("UpdateReport", mock.Anything, mock.AnythingOfType("*entities.ReconciliationReport")).Return(nil)

				// Mock successful checks (all balanced)
				ledger.On("GetSystemBufferBalance", mock.Anything, "system_buffer_usdc").Return(decimal.NewFromFloat(10000.0), nil)
				circle.On("GetTotalUSDCBalance", mock.Anything).Return(decimal.NewFromFloat(10000.0), nil)

				ledger.On("GetTotalUserFiatExposure", mock.Anything).Return(decimal.NewFromFloat(50000.0), nil)
				alpaca.On("GetTotalBuyingPower", mock.Anything).Return(decimal.NewFromFloat(50000.0), nil)
			},
			expectError:    false,
			expectedPassed: 6, // All checks pass
			expectedFailed: 0,
		},
		{
			name:    "reconciliation with Circle balance mismatch",
			runType: "daily",
			setupMocks: func(repo *mocks.MockReconciliationRepository, ledger *MockLedgerService, circle *MockCircleClient, alpaca *MockAlpacaClient) {
				repo.On("CreateReport", mock.Anything, mock.AnythingOfType("*entities.ReconciliationReport")).Return(nil)
				repo.On("CreateCheck", mock.Anything, mock.AnythingOfType("*entities.ReconciliationCheck")).Return(nil)
				repo.On("CreateExceptionsBatch", mock.Anything, mock.AnythingOfType("[]*entities.ReconciliationException")).Return(nil)
				repo.On("UpdateReport", mock.Anything, mock.AnythingOfType("*entities.ReconciliationReport")).Return(nil)

				// Mock Circle balance discrepancy
				ledger.On("GetSystemBufferBalance", mock.Anything, "system_buffer_usdc").Return(decimal.NewFromFloat(10000.0), nil)
				circle.On("GetTotalUSDCBalance", mock.Anything).Return(decimal.NewFromFloat(9900.0), nil) // $100 difference

				ledger.On("GetTotalUserFiatExposure", mock.Anything).Return(decimal.NewFromFloat(50000.0), nil)
				alpaca.On("GetTotalBuyingPower", mock.Anything).Return(decimal.NewFromFloat(50000.0), nil)
			},
			expectError:    false,
			expectedPassed: 5,
			expectedFailed: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, repo, ledgerService, circleClient, alpacaClient := setupTestService(t)

			if tt.setupMocks != nil {
				tt.setupMocks(repo, ledgerService, circleClient, alpacaClient)
			}

			ctx := context.Background()
			report, err := service.RunReconciliation(ctx, tt.runType)

			if tt.expectError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.NotNil(t, report)
			assert.Equal(t, tt.runType, report.RunType)
			assert.Equal(t, entities.ReconciliationStatusCompleted, report.Status)
			assert.NotNil(t, report.CompletedAt)

			repo.AssertExpectations(t)
			ledgerService.AssertExpectations(t)
			circleClient.AssertExpectations(t)
			alpacaClient.AssertExpectations(t)
		})
	}
}

func TestService_CheckCircleBalance(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		ledgerBalance    decimal.Decimal
		circleBalance    decimal.Decimal
		expectPassed     bool
		expectExceptions int
		expectedSeverity entities.ExceptionSeverity
	}{
		{
			name:             "balances match exactly",
			ledgerBalance:    decimal.NewFromFloat(10000.0),
			circleBalance:    decimal.NewFromFloat(10000.0),
			expectPassed:     true,
			expectExceptions: 0,
		},
		{
			name:             "small difference within tolerance",
			ledgerBalance:    decimal.NewFromFloat(10000.0),
			circleBalance:    decimal.NewFromFloat(10005.0),
			expectPassed:     true,
			expectExceptions: 0,
		},
		{
			name:             "medium discrepancy",
			ledgerBalance:    decimal.NewFromFloat(10000.0),
			circleBalance:    decimal.NewFromFloat(10050.0),
			expectPassed:     false,
			expectExceptions: 1,
			expectedSeverity: entities.ExceptionSeverityMedium,
		},
		{
			name:             "large discrepancy",
			ledgerBalance:    decimal.NewFromFloat(10000.0),
			circleBalance:    decimal.NewFromFloat(11500.0),
			expectPassed:     false,
			expectExceptions: 1,
			expectedSeverity: entities.ExceptionSeverityCritical,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, _, ledgerService, circleClient, _ := setupTestService(t)

			ledgerService.On("GetSystemBufferBalance", mock.Anything, "system_buffer_usdc").Return(tt.ledgerBalance, nil)
			circleClient.On("GetTotalUSDCBalance", mock.Anything).Return(tt.circleBalance, nil)

			ctx := context.Background()
			reportID := uuid.New()

			result, err := service.CheckCircleBalance(ctx, reportID)

			require.NoError(t, err)
			assert.NotNil(t, result)
			assert.Equal(t, tt.expectPassed, result.Passed)
			assert.Equal(t, tt.expectExceptions, len(result.Exceptions))

			if tt.expectExceptions > 0 {
				assert.Equal(t, tt.expectedSeverity, result.Exceptions[0].Severity)
				assert.Equal(t, entities.ReconciliationCheckCircleBalance, result.Exceptions[0].CheckType)
			}

			ledgerService.AssertExpectations(t)
			circleClient.AssertExpectations(t)
		})
	}
}

func TestService_CheckAlpacaBalance(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		fiatExposure      decimal.Decimal
		alpacaBuyingPower decimal.Decimal
		expectPassed      bool
		expectExceptions  int
	}{
		{
			name:              "balances match exactly",
			fiatExposure:      decimal.NewFromFloat(50000.0),
			alpacaBuyingPower: decimal.NewFromFloat(50000.0),
			expectPassed:      true,
			expectExceptions:  0,
		},
		{
			name:              "within tolerance (pending orders)",
			fiatExposure:      decimal.NewFromFloat(50000.0),
			alpacaBuyingPower: decimal.NewFromFloat(50050.0),
			expectPassed:      true,
			expectExceptions:  0,
		},
		{
			name:              "significant discrepancy",
			fiatExposure:      decimal.NewFromFloat(50000.0),
			alpacaBuyingPower: decimal.NewFromFloat(51000.0),
			expectPassed:      false,
			expectExceptions:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, _, ledgerService, _, alpacaClient := setupTestService(t)

			ledgerService.On("GetTotalUserFiatExposure", mock.Anything).Return(tt.fiatExposure, nil)
			alpacaClient.On("GetTotalBuyingPower", mock.Anything).Return(tt.alpacaBuyingPower, nil)

			ctx := context.Background()
			reportID := uuid.New()

			result, err := service.CheckAlpacaBalance(ctx, reportID)

			require.NoError(t, err)
			assert.NotNil(t, result)
			assert.Equal(t, tt.expectPassed, result.Passed)
			assert.Equal(t, tt.expectExceptions, len(result.Exceptions))

			ledgerService.AssertExpectations(t)
			alpacaClient.AssertExpectations(t)
		})
	}
}

func TestDetermineSeverity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		difference       decimal.Decimal
		currency         string
		expectedSeverity entities.ExceptionSeverity
	}{
		{
			name:             "less than $1 - low severity",
			difference:       decimal.NewFromFloat(0.50),
			currency:         "USD",
			expectedSeverity: entities.ExceptionSeverityLow,
		},
		{
			name:             "exactly $1 - low severity",
			difference:       decimal.NewFromFloat(1.0),
			currency:         "USD",
			expectedSeverity: entities.ExceptionSeverityLow,
		},
		{
			name:             "$50 - medium severity",
			difference:       decimal.NewFromFloat(50.0),
			currency:         "USD",
			expectedSeverity: entities.ExceptionSeverityMedium,
		},
		{
			name:             "$100 - medium severity",
			difference:       decimal.NewFromFloat(100.0),
			currency:         "USD",
			expectedSeverity: entities.ExceptionSeverityMedium,
		},
		{
			name:             "$500 - high severity",
			difference:       decimal.NewFromFloat(500.0),
			currency:         "USD",
			expectedSeverity: entities.ExceptionSeverityHigh,
		},
		{
			name:             "$5000 - critical severity",
			difference:       decimal.NewFromFloat(5000.0),
			currency:         "USD",
			expectedSeverity: entities.ExceptionSeverityCritical,
		},
		{
			name:             "negative difference - absolute value used",
			difference:       decimal.NewFromFloat(-150.0),
			currency:         "USD",
			expectedSeverity: entities.ExceptionSeverityHigh,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			severity := entities.DetermineSeverity(tt.difference, tt.currency)
			assert.Equal(t, tt.expectedSeverity, severity)
		})
	}
}

func TestReconciliationException_CanAutoCorrect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		severity         entities.ExceptionSeverity
		autoCorrected    bool
		expectCanCorrect bool
	}{
		{
			name:             "low severity not corrected",
			severity:         entities.ExceptionSeverityLow,
			autoCorrected:    false,
			expectCanCorrect: true,
		},
		{
			name:             "low severity already corrected",
			severity:         entities.ExceptionSeverityLow,
			autoCorrected:    true,
			expectCanCorrect: false,
		},
		{
			name:             "medium severity not corrected",
			severity:         entities.ExceptionSeverityMedium,
			autoCorrected:    false,
			expectCanCorrect: false,
		},
		{
			name:             "high severity not corrected",
			severity:         entities.ExceptionSeverityHigh,
			autoCorrected:    false,
			expectCanCorrect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exception := &entities.ReconciliationException{
				Severity:      tt.severity,
				AutoCorrected: tt.autoCorrected,
			}

			canCorrect := exception.CanAutoCorrect()
			assert.Equal(t, tt.expectCanCorrect, canCorrect)
		})
	}
}

func TestService_filterHighPriorityExceptions(t *testing.T) {
	t.Parallel()

	service, _, _, _, _ := setupTestService(t)

	exceptions := []*entities.ReconciliationException{
		{Severity: entities.ExceptionSeverityLow},
		{Severity: entities.ExceptionSeverityMedium},
		{Severity: entities.ExceptionSeverityHigh},
		{Severity: entities.ExceptionSeverityCritical},
		{Severity: entities.ExceptionSeverityLow},
		{Severity: entities.ExceptionSeverityHigh},
	}

	highPriority := service.filterHighPriorityExceptions(exceptions)

	assert.Len(t, highPriority, 3) // 2 high + 1 critical
	for _, exc := range highPriority {
		assert.True(t, exc.Severity == entities.ExceptionSeverityHigh || exc.Severity == entities.ExceptionSeverityCritical)
	}
}

func TestReconciliationException_MarkCorrected(t *testing.T) {
	exception := &entities.ReconciliationException{
		ID:            uuid.New(),
		Severity:      entities.ExceptionSeverityLow,
		AutoCorrected: false,
	}

	action := "Test correction action"
	exception.MarkCorrected(action)

	assert.True(t, exception.AutoCorrected)
	assert.Equal(t, action, exception.CorrectionAction)
	assert.NotNil(t, exception.ResolvedAt)
	assert.Equal(t, "system", exception.ResolvedBy)
}

func TestReconciliationException_MarkResolved(t *testing.T) {
	exception := &entities.ReconciliationException{
		ID:       uuid.New(),
		Severity: entities.ExceptionSeverityHigh,
	}

	resolvedBy := "admin@example.com"
	notes := "Manually investigated and resolved"
	exception.MarkResolved(resolvedBy, notes)

	assert.NotNil(t, exception.ResolvedAt)
	assert.Equal(t, resolvedBy, exception.ResolvedBy)
	assert.Equal(t, notes, exception.ResolutionNotes)
}

func TestNewReconciliationReport(t *testing.T) {
	runType := "hourly"
	report := entities.NewReconciliationReport(runType)

	assert.NotEqual(t, uuid.Nil, report.ID)
	assert.Equal(t, runType, report.RunType)
	assert.Equal(t, entities.ReconciliationStatusPending, report.Status)
	assert.NotNil(t, report.Metadata)
	assert.False(t, report.StartedAt.IsZero())
	assert.False(t, report.CreatedAt.IsZero())
}
