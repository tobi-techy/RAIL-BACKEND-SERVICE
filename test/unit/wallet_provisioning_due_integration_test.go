package unit

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	walletprovisioning "github.com/rail-service/rail_service/internal/workers/wallet_provisioning"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap"
)

// MockWalletUserRepository implements the UserRepository interface for testing
type MockWalletUserRepository struct {
	mock.Mock
}

func (m *MockWalletUserRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.User, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.User), args.Error(1)
}

// MockDueService implements the DueService interface for testing
type MockDueService struct {
	mock.Mock
}

func (m *MockDueService) LinkCircleWallet(ctx context.Context, walletAddress, chain string) error {
	args := m.Called(ctx, walletAddress, chain)
	return args.Error(0)
}

// MockWalletRepository implements the WalletRepository interface for testing
type MockWalletRepository struct {
	mock.Mock
}

func (m *MockWalletRepository) Create(ctx context.Context, wallet *entities.ManagedWallet) error {
	args := m.Called(ctx, wallet)
	return args.Error(0)
}

func (m *MockWalletRepository) GetByUserAndChain(ctx context.Context, userID uuid.UUID, chain entities.WalletChain) (*entities.ManagedWallet, error) {
	args := m.Called(ctx, userID, chain)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.ManagedWallet), args.Error(1)
}

func (m *MockWalletRepository) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.ManagedWallet, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*entities.ManagedWallet), args.Error(1)
}

// MockWalletSetRepository implements the WalletSetRepository interface for testing
type MockWalletSetRepository struct {
	mock.Mock
}

func (m *MockWalletSetRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.WalletSet, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.WalletSet), args.Error(1)
}

func (m *MockWalletSetRepository) GetActive(ctx context.Context) (*entities.WalletSet, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.WalletSet), args.Error(1)
}

func (m *MockWalletSetRepository) Create(ctx context.Context, walletSet *entities.WalletSet) error {
	args := m.Called(ctx, walletSet)
	return args.Error(0)
}

func (m *MockWalletSetRepository) GetByCircleWalletSetID(ctx context.Context, circleWalletSetID string) (*entities.WalletSet, error) {
	args := m.Called(ctx, circleWalletSetID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.WalletSet), args.Error(1)
}

func (m *MockWalletSetRepository) Update(ctx context.Context, walletSet *entities.WalletSet) error {
	args := m.Called(ctx, walletSet)
	return args.Error(0)
}

// MockProvisioningJobRepository implements the ProvisioningJobRepository interface for testing
type MockProvisioningJobRepository struct {
	mock.Mock
}

func (m *MockProvisioningJobRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.WalletProvisioningJob, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.WalletProvisioningJob), args.Error(1)
}

func (m *MockProvisioningJobRepository) GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.WalletProvisioningJob, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.WalletProvisioningJob), args.Error(1)
}

func (m *MockProvisioningJobRepository) GetRetryableJobs(ctx context.Context, limit int) ([]*entities.WalletProvisioningJob, error) {
	args := m.Called(ctx, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*entities.WalletProvisioningJob), args.Error(1)
}

func (m *MockProvisioningJobRepository) Update(ctx context.Context, job *entities.WalletProvisioningJob) error {
	args := m.Called(ctx, job)
	return args.Error(0)
}

// MockCircleClient implements the CircleClient interface for testing
type MockCircleClient struct {
	mock.Mock
}

func (m *MockCircleClient) CreateWalletSet(ctx context.Context, name string, entitySecretCiphertext string) (*entities.CircleWalletSetResponse, error) {
	args := m.Called(ctx, name, entitySecretCiphertext)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.CircleWalletSetResponse), args.Error(1)
}

func (m *MockCircleClient) GetWalletSet(ctx context.Context, walletSetID string) (*entities.CircleWalletSetResponse, error) {
	args := m.Called(ctx, walletSetID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.CircleWalletSetResponse), args.Error(1)
}

func (m *MockCircleClient) CreateWallet(ctx context.Context, req entities.CircleWalletCreateRequest) (*entities.CircleWalletCreateResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.CircleWalletCreateResponse), args.Error(1)
}

func (m *MockCircleClient) GetWallet(ctx context.Context, walletID string) (*entities.CircleWalletCreateResponse, error) {
	args := m.Called(ctx, walletID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.CircleWalletCreateResponse), args.Error(1)
}

// MockAuditService implements the AuditService interface for testing
type MockAuditService struct {
	mock.Mock
}

func (m *MockAuditService) LogWalletEvent(ctx context.Context, userID uuid.UUID, action, entity string, before, after interface{}) error {
	args := m.Called(ctx, userID, action, entity, before, after)
	return args.Error(0)
}

func (m *MockAuditService) LogWalletWorkerEvent(ctx context.Context, userID uuid.UUID, action, entity string, before, after interface{}, resourceID *string, status string, errorMsg *string) error {
	args := m.Called(ctx, userID, action, entity, before, after, resourceID, status, errorMsg)
	return args.Error(0)
}

func TestWalletProvisioningWithDueLinking(t *testing.T) {
	// Setup mocks
	mockWalletRepo := &MockWalletRepository{}
	mockWalletSetRepo := &MockWalletSetRepository{}
	mockJobRepo := &MockProvisioningJobRepository{}
	mockCircleClient := &MockCircleClient{}
	mockAuditService := &MockAuditService{}
	mockUserRepo := &MockWalletUserRepository{}
	mockDueService := &MockDueService{}

	// Test data
	userID := uuid.New()
	jobID := uuid.New()
	walletSetID := uuid.New()
	dueAccountID := "due_account_123"
	walletAddress := "9WzDXwBbmkg8ZTbNMqUxvQRAyrZzDsGYdLVL9zYtAWWM"

	// Create test user with Due account
	testUser := &entities.User{
		ID:           userID,
		Email:        "test@example.com",
		DueAccountID: &dueAccountID,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	// Create test job
	testJob := &entities.WalletProvisioningJob{
		ID:           jobID,
		UserID:       userID,
		Chains:       []string{"SOL-DEVNET"},
		Status:       entities.ProvisioningStatusQueued,
		AttemptCount: 0,
		MaxAttempts:  5,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	// Create test wallet set
	testWalletSet := &entities.WalletSet{
		ID:                walletSetID,
		Name:              "test-wallet-set",
		CircleWalletSetID: "circle_set_123",
		Status:            entities.WalletSetStatusActive,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}

	// Create Circle wallet response
	circleWalletResp := &entities.CircleWalletCreateResponse{
		Wallet: entities.CircleWalletData{
			ID:      "circle_wallet_123",
			Address: walletAddress,
			State:   "LIVE",
		},
	}

	// Setup expectations
	mockJobRepo.On("GetByID", mock.Anything, jobID).Return(testJob, nil)
	mockJobRepo.On("Update", mock.Anything, mock.AnythingOfType("*entities.WalletProvisioningJob")).Return(nil)
	
	mockWalletSetRepo.On("GetActive", mock.Anything).Return(testWalletSet, nil)
	
	mockWalletRepo.On("GetByUserAndChain", mock.Anything, userID, entities.WalletChainSOLDevnet).Return(nil, assert.AnError)
	mockWalletRepo.On("Create", mock.Anything, mock.AnythingOfType("*entities.ManagedWallet")).Return(nil)
	
	mockCircleClient.On("CreateWallet", mock.Anything, mock.AnythingOfType("entities.CircleWalletCreateRequest")).Return(circleWalletResp, nil)
	
	mockAuditService.On("LogWalletWorkerEvent", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	
	// Setup user repository to return user with Due account
	mockUserRepo.On("GetByID", mock.Anything, userID).Return(testUser, nil)
	
	// Setup Due service to expect wallet linking
	mockDueService.On("LinkCircleWallet", mock.Anything, walletAddress, "SOL-DEVNET").Return(nil)

	// Create worker
	config := walletprovisioning.DefaultConfig()
	logger := zap.NewNop()

	worker := walletprovisioning.NewWorker(
		mockWalletRepo,
		mockWalletSetRepo,
		mockJobRepo,
		mockCircleClient,
		mockAuditService,
		mockUserRepo,
		mockDueService,
		config,
		logger,
	)

	// Execute test
	ctx := context.Background()
	err := worker.ProcessJob(ctx, jobID)

	// Assertions
	assert.NoError(t, err)
	
	// Verify all mocks were called as expected
	mockUserRepo.AssertExpectations(t)
	mockDueService.AssertExpectations(t)
	mockWalletRepo.AssertExpectations(t)
	mockCircleClient.AssertExpectations(t)
	mockAuditService.AssertExpectations(t)
}

func TestWalletProvisioningWithoutDueAccount(t *testing.T) {
	// Setup mocks
	mockWalletRepo := &MockWalletRepository{}
	mockWalletSetRepo := &MockWalletSetRepository{}
	mockJobRepo := &MockProvisioningJobRepository{}
	mockCircleClient := &MockCircleClient{}
	mockAuditService := &MockAuditService{}
	mockUserRepo := &MockWalletUserRepository{}
	mockDueService := &MockDueService{}

	// Test data
	userID := uuid.New()
	jobID := uuid.New()
	walletSetID := uuid.New()
	walletAddress := "9WzDXwBbmkg8ZTbNMqUxvQRAyrZzDsGYdLVL9zYtAWWM"

	// Create test user WITHOUT Due account
	testUser := &entities.User{
		ID:           userID,
		Email:        "test@example.com",
		DueAccountID: nil, // No Due account
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	// Create test job
	testJob := &entities.WalletProvisioningJob{
		ID:           jobID,
		UserID:       userID,
		Chains:       []string{"SOL-DEVNET"},
		Status:       entities.ProvisioningStatusQueued,
		AttemptCount: 0,
		MaxAttempts:  5,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	// Create test wallet set
	testWalletSet := &entities.WalletSet{
		ID:                walletSetID,
		Name:              "test-wallet-set",
		CircleWalletSetID: "circle_set_123",
		Status:            entities.WalletSetStatusActive,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}

	// Create Circle wallet response
	circleWalletResp := &entities.CircleWalletCreateResponse{
		Wallet: entities.CircleWalletData{
			ID:      "circle_wallet_123",
			Address: walletAddress,
			State:   "LIVE",
		},
	}

	// Setup expectations
	mockJobRepo.On("GetByID", mock.Anything, jobID).Return(testJob, nil)
	mockJobRepo.On("Update", mock.Anything, mock.AnythingOfType("*entities.WalletProvisioningJob")).Return(nil)
	
	mockWalletSetRepo.On("GetActive", mock.Anything).Return(testWalletSet, nil)
	
	mockWalletRepo.On("GetByUserAndChain", mock.Anything, userID, entities.WalletChainSOLDevnet).Return(nil, assert.AnError)
	mockWalletRepo.On("Create", mock.Anything, mock.AnythingOfType("*entities.ManagedWallet")).Return(nil)
	
	mockCircleClient.On("CreateWallet", mock.Anything, mock.AnythingOfType("entities.CircleWalletCreateRequest")).Return(circleWalletResp, nil)
	
	mockAuditService.On("LogWalletWorkerEvent", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	
	// Setup user repository to return user WITHOUT Due account
	mockUserRepo.On("GetByID", mock.Anything, userID).Return(testUser, nil)
	
	// Due service should NOT be called since user has no Due account

	// Create worker
	config := walletprovisioning.DefaultConfig()
	logger := zap.NewNop()

	worker := walletprovisioning.NewWorker(
		mockWalletRepo,
		mockWalletSetRepo,
		mockJobRepo,
		mockCircleClient,
		mockAuditService,
		mockUserRepo,
		mockDueService,
		config,
		logger,
	)

	// Execute test
	ctx := context.Background()
	err := worker.ProcessJob(ctx, jobID)

	// Assertions
	assert.NoError(t, err)
	
	// Verify mocks were called as expected
	mockUserRepo.AssertExpectations(t)
	mockWalletRepo.AssertExpectations(t)
	mockCircleClient.AssertExpectations(t)
	mockAuditService.AssertExpectations(t)
	
	// Verify Due service was NOT called
	mockDueService.AssertNotCalled(t, "LinkCircleWallet")
}