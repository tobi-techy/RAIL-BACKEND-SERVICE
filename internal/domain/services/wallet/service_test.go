package wallet

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap/zaptest"

	"github.com/stack-service/stack_service/internal/domain/entities"
)

// Mock implementations for testing
type mockWalletRepository struct {
	mock.Mock
}

func (m *mockWalletRepository) Create(ctx context.Context, wallet *entities.ManagedWallet) error {
	args := m.Called(ctx, wallet)
	return args.Error(0)
}

func (m *mockWalletRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.ManagedWallet, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.ManagedWallet), args.Error(1)
}

func (m *mockWalletRepository) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.ManagedWallet, error) {
	args := m.Called(ctx, userID)
	return args.Get(0).([]*entities.ManagedWallet), args.Error(1)
}

func (m *mockWalletRepository) GetByUserAndChain(ctx context.Context, userID uuid.UUID, chain entities.WalletChain) (*entities.ManagedWallet, error) {
	args := m.Called(ctx, userID, chain)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.ManagedWallet), args.Error(1)
}

func (m *mockWalletRepository) GetByCircleWalletID(ctx context.Context, circleWalletID string) (*entities.ManagedWallet, error) {
	args := m.Called(ctx, circleWalletID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.ManagedWallet), args.Error(1)
}

func (m *mockWalletRepository) Update(ctx context.Context, wallet *entities.ManagedWallet) error {
	args := m.Called(ctx, wallet)
	return args.Error(0)
}

func (m *mockWalletRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status entities.WalletStatus) error {
	args := m.Called(ctx, id, status)
	return args.Error(0)
}

type mockWalletSetRepository struct {
	mock.Mock
}

func (m *mockWalletSetRepository) Create(ctx context.Context, walletSet *entities.WalletSet) error {
	args := m.Called(ctx, walletSet)
	return args.Error(0)
}

func (m *mockWalletSetRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.WalletSet, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.WalletSet), args.Error(1)
}

func (m *mockWalletSetRepository) GetByCircleWalletSetID(ctx context.Context, circleWalletSetID string) (*entities.WalletSet, error) {
	args := m.Called(ctx, circleWalletSetID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.WalletSet), args.Error(1)
}

func (m *mockWalletSetRepository) GetActive(ctx context.Context) (*entities.WalletSet, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.WalletSet), args.Error(1)
}

func (m *mockWalletSetRepository) Update(ctx context.Context, walletSet *entities.WalletSet) error {
	args := m.Called(ctx, walletSet)
	return args.Error(0)
}

type mockWalletProvisioningJobRepository struct {
	mock.Mock
}

func (m *mockWalletProvisioningJobRepository) Create(ctx context.Context, job *entities.WalletProvisioningJob) error {
	args := m.Called(ctx, job)
	return args.Error(0)
}

func (m *mockWalletProvisioningJobRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.WalletProvisioningJob, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.WalletProvisioningJob), args.Error(1)
}

func (m *mockWalletProvisioningJobRepository) GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.WalletProvisioningJob, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.WalletProvisioningJob), args.Error(1)
}

func (m *mockWalletProvisioningJobRepository) GetRetryableJobs(ctx context.Context, limit int) ([]*entities.WalletProvisioningJob, error) {
	args := m.Called(ctx, limit)
	return args.Get(0).([]*entities.WalletProvisioningJob), args.Error(1)
}

func (m *mockWalletProvisioningJobRepository) Update(ctx context.Context, job *entities.WalletProvisioningJob) error {
	args := m.Called(ctx, job)
	return args.Error(0)
}

type mockCircleClient struct {
	mock.Mock
}

func (m *mockCircleClient) CreateWalletSet(ctx context.Context, name string, entitySecretCiphertext string) (*entities.CircleWalletSetResponse, error) {
	args := m.Called(ctx, name, entitySecretCiphertext)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.CircleWalletSetResponse), args.Error(1)
}

func (m *mockCircleClient) GetWalletSet(ctx context.Context, walletSetID string) (*entities.CircleWalletSetResponse, error) {
	args := m.Called(ctx, walletSetID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.CircleWalletSetResponse), args.Error(1)
}

func (m *mockCircleClient) CreateWallet(ctx context.Context, req entities.CircleWalletCreateRequest) (*entities.CircleWalletCreateResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.CircleWalletCreateResponse), args.Error(1)
}

func (m *mockCircleClient) GetWallet(ctx context.Context, walletID string) (*entities.CircleWalletCreateResponse, error) {
	args := m.Called(ctx, walletID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.CircleWalletCreateResponse), args.Error(1)
}

func (m *mockCircleClient) HealthCheck(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *mockCircleClient) GetMetrics() map[string]interface{} {
	args := m.Called()
	return args.Get(0).(map[string]interface{})
}

type mockAuditService struct {
	mock.Mock
}

func (m *mockAuditService) LogWalletEvent(ctx context.Context, userID uuid.UUID, action, entity string, before, after interface{}) error {
	args := m.Called(ctx, userID, action, entity, before, after)
	return args.Error(0)
}

type mockEntitySecretService struct {
	mock.Mock
}

func (m *mockEntitySecretService) GenerateEntitySecretCiphertext(ctx context.Context) (string, error) {
	args := m.Called(ctx)
	return args.String(0), args.Error(1)
}

type mockOnboardingService struct {
	mock.Mock
}

func (m *mockOnboardingService) ProcessWalletCreationComplete(ctx context.Context, userID uuid.UUID) error {
	args := m.Called(ctx, userID)
	return args.Error(0)
}

// Test Service Creation
func TestNewService(t *testing.T) {
	// Setup mocks
	walletRepo := &mockWalletRepository{}
	walletSetRepo := &mockWalletSetRepository{}
	provisioningJobRepo := &mockWalletProvisioningJobRepository{}
	circleClient := &mockCircleClient{}
	auditService := &mockAuditService{}
	entitySecretService := &mockEntitySecretService{}
	onboardingService := &mockOnboardingService{}

	// Create logger for testing
	logger := zaptest.NewLogger(t)

	config := Config{
		WalletSetNamePrefix: "TEST",
		SupportedChains:     []entities.WalletChain{entities.ChainSOLDevnet},
	}

	service := NewService(
		walletRepo,
		walletSetRepo,
		provisioningJobRepo,
		circleClient,
		auditService,
		entitySecretService,
		onboardingService,
		logger,
		config,
	)

	assert.NotNil(t, service)
	assert.Equal(t, config, service.config)
	assert.Equal(t, walletRepo, service.walletRepo)
}

// Test GetWalletAddresses
func TestService_GetWalletAddresses(t *testing.T) {
	// Setup mocks
	walletRepo := &mockWalletRepository{}
	walletSetRepo := &mockWalletSetRepository{}
	provisioningJobRepo := &mockWalletProvisioningJobRepository{}
	circleClient := &mockCircleClient{}
	auditService := &mockAuditService{}
	entitySecretService := &mockEntitySecretService{}
	onboardingService := &mockOnboardingService{}
	logger := zaptest.NewLogger(t)

	config := Config{
		SupportedChains: []entities.WalletChain{entities.ChainSOLDevnet},
	}

	service := NewService(
		walletRepo,
		walletSetRepo,
		provisioningJobRepo,
		circleClient,
		auditService,
		entitySecretService,
		onboardingService,
		logger,
		config,
	)

	ctx := context.Background()
	userID := uuid.New()

	// Test case: no wallets found
	walletRepo.On("GetByUserID", ctx, userID).Return([]*entities.ManagedWallet{}, nil).Once()

	response, err := service.GetWalletAddresses(ctx, userID, nil)
	assert.NoError(t, err)
	assert.NotNil(t, response)
	assert.Empty(t, response.Wallets)

	walletRepo.AssertExpectations(t)
}

// Test GetWalletStatus
func TestService_GetWalletStatus(t *testing.T) {
	// Setup mocks
	walletRepo := &mockWalletRepository{}
	walletSetRepo := &mockWalletSetRepository{}
	provisioningJobRepo := &mockWalletProvisioningJobRepository{}
	circleClient := &mockCircleClient{}
	auditService := &mockAuditService{}
	entitySecretService := &mockEntitySecretService{}
	onboardingService := &mockOnboardingService{}
	logger := zaptest.NewLogger(t)

	config := Config{
		SupportedChains: []entities.WalletChain{entities.ChainSOLDevnet},
	}

	service := NewService(
		walletRepo,
		walletSetRepo,
		provisioningJobRepo,
		circleClient,
		auditService,
		entitySecretService,
		onboardingService,
		logger,
		config,
	)

	ctx := context.Background()
	userID := uuid.New()

	// Test case: no wallets and no provisioning job
	walletRepo.On("GetByUserID", ctx, userID).Return([]*entities.ManagedWallet{}, nil).Once()
	provisioningJobRepo.On("GetByUserID", ctx, userID).Return(nil, nil).Once()

	response, err := service.GetWalletStatus(ctx, userID)
	assert.NoError(t, err)
	assert.NotNil(t, response)
	assert.Equal(t, userID, response.UserID)
	assert.Equal(t, 0, response.TotalWallets)
	assert.Nil(t, response.ProvisioningJob)

	walletRepo.AssertExpectations(t)
	provisioningJobRepo.AssertExpectations(t)
}

// Test HealthCheck
func TestService_HealthCheck(t *testing.T) {
	// Setup mocks
	walletRepo := &mockWalletRepository{}
	walletSetRepo := &mockWalletSetRepository{}
	provisioningJobRepo := &mockWalletProvisioningJobRepository{}
	circleClient := &mockCircleClient{}
	auditService := &mockAuditService{}
	entitySecretService := &mockEntitySecretService{}
	onboardingService := &mockOnboardingService{}
	logger := zaptest.NewLogger(t)

	config := Config{
		SupportedChains: []entities.WalletChain{entities.ChainSOLDevnet},
	}

	service := NewService(
		walletRepo,
		walletSetRepo,
		provisioningJobRepo,
		circleClient,
		auditService,
		entitySecretService,
		onboardingService,
		logger,
		config,
	)

	ctx := context.Background()

	// Test case: health check passes
	circleClient.On("HealthCheck", ctx).Return(nil).Once()
	walletSetRepo.On("GetActive", ctx).Return(&entities.WalletSet{}, nil).Once()

	err := service.HealthCheck(ctx)
	assert.NoError(t, err)

	circleClient.AssertExpectations(t)
	walletSetRepo.AssertExpectations(t)
}
