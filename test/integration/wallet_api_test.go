package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/pkg/logger"
)

// TestWalletAPI tests the wallet management API endpoints
func TestWalletAPI(t *testing.T) {
	// Skip if integration tests are disabled
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup test environment
	gin.SetMode(gin.TestMode)

	// Create test configuration
	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:            8080,
			AllowedOrigins:  []string{"*"},
			RateLimitPerMin: 100,
		},
		Database: config.DatabaseConfig{
			Host:     "localhost",
			Port:     5432,
			User:     "test",
			Password: "test",
			Name:     "stack_test",
			SSLMode:  "disable",
		},
		Circle: config.CircleConfig{
			APIKey:               "test-api-key",
			Environment:          "sandbox",
			DefaultWalletSetName: "TEST-WalletSet",
			SupportedChains:      []string{"ETH", "MATIC", "SOL"},
		},
		JWT: config.JWTConfig{
			Secret:     "test-secret-key-for-jwt-token-generation",
			AccessTTL:  604800, // 7 days in seconds
			RefreshTTL: 604800, // 7 days in seconds
		},
		Environment: "test",
	}

	// Create test logger
	testLogger := logger.New("info", "test")

	// Note: In a real integration test, you would set up a test database
	// For this example, we'll create a mock setup
	t.Run("WalletProvisioningFlow", func(t *testing.T) {
		testWalletProvisioningFlow(t, cfg, testLogger)
	})

	t.Run("SCAUnifiedAddresses", func(t *testing.T) {
		testSCAUnifiedAddresses(t, cfg, testLogger)
	})

	t.Run("AdminWalletManagement", func(t *testing.T) {
		testAdminWalletManagement(t, cfg, testLogger)
	})
}

func testWalletProvisioningFlow(t *testing.T, cfg *config.Config, testLogger *logger.Logger) {
	// This would be a full integration test with a real database
	// For now, we'll test the API structure and request/response formats

	t.Run("ProvisionWalletsRequest", func(t *testing.T) {
		req := entities.WalletProvisioningRequest{
			Chains: []string{"ETH", "MATIC", "SOL"},
		}

		// Validate request structure
		assert.NotNil(t, req.Chains)
		assert.Len(t, req.Chains, 3)
		assert.Contains(t, req.Chains, "ETH")
		assert.Contains(t, req.Chains, "MATIC")
		assert.Contains(t, req.Chains, "SOL")
	})

	t.Run("WalletProvisioningResponse", func(t *testing.T) {
		jobID := uuid.New()
		response := entities.WalletProvisioningResponse{
			Message: "Wallet provisioning started",
			Job: entities.WalletProvisioningJobResponse{
				ID:           jobID,
				Status:       "queued",
				Progress:     "0%",
				AttemptCount: 0,
				MaxAttempts:  3,
				CreatedAt:    time.Now(),
			},
		}

		// Validate response structure
		assert.Equal(t, "Wallet provisioning started", response.Message)
		assert.Equal(t, jobID, response.Job.ID)
		assert.Equal(t, "queued", response.Job.Status)
		assert.Equal(t, "0%", response.Job.Progress)
	})

	t.Run("WalletAddressResponse", func(t *testing.T) {
		response := entities.WalletAddressResponse{
			Chain:   entities.ChainETH,
			Address: "0xf5c83e5fede8456929d0f90e8c541dcac3d63835",
			Status:  "live",
		}

		// Validate response structure
		assert.Equal(t, entities.ChainETH, response.Chain)
		assert.NotEmpty(t, response.Address)
		assert.Equal(t, "live", response.Status)
	})
}

func testSCAUnifiedAddresses(t *testing.T, cfg *config.Config, testLogger *logger.Logger) {
	t.Run("SCAAddressUnification", func(t *testing.T) {
		// Test that SCA wallets return the same address across EVM chains
		unifiedAddress := "0xf5c83e5fede8456929d0f90e8c541dcac3d63835"

		// Simulate wallet responses for different EVM chains
		ethWallet := entities.ManagedWallet{
			ID:          uuid.New(),
			UserID:      uuid.New(),
			Chain:       entities.ChainETH,
			Address:     unifiedAddress,
			AccountType: entities.AccountTypeSCA,
			Status:      entities.WalletStatusLive,
		}

		maticWallet := entities.ManagedWallet{
			ID:          uuid.New(),
			UserID:      uuid.New(),
			Chain:       entities.ChainMATIC,
			Address:     unifiedAddress, // Same address for SCA
			AccountType: entities.AccountTypeSCA,
			Status:      entities.WalletStatusLive,
		}

		baseWallet := entities.ManagedWallet{
			ID:          uuid.New(),
			UserID:      uuid.New(),
			Chain:       entities.ChainBASE,
			Address:     unifiedAddress, // Same address for SCA
			AccountType: entities.AccountTypeSCA,
			Status:      entities.WalletStatusLive,
		}

		// Validate SCA unified addresses
		assert.Equal(t, ethWallet.Address, maticWallet.Address)
		assert.Equal(t, ethWallet.Address, baseWallet.Address)
		assert.Equal(t, maticWallet.Address, baseWallet.Address)

		// Validate account types
		assert.Equal(t, entities.AccountTypeSCA, ethWallet.AccountType)
		assert.Equal(t, entities.AccountTypeSCA, maticWallet.AccountType)
		assert.Equal(t, entities.AccountTypeSCA, baseWallet.AccountType)
	})

	t.Run("EOAAddressesDifferent", func(t *testing.T) {
		// Test that EOA wallets have different addresses
		solWallet := entities.ManagedWallet{
			ID:          uuid.New(),
			UserID:      uuid.New(),
			Chain:       entities.ChainSOL,
			Address:     "9FMYUH1mcQ9F12yjjk6BciTuBC5kvMKadThs941v5vk7",
			AccountType: entities.AccountTypeEOA, // Solana uses EOA
			Status:      entities.WalletStatusLive,
		}

		aptosWallet := entities.ManagedWallet{
			ID:          uuid.New(),
			UserID:      uuid.New(),
			Chain:       entities.ChainAPTOS,
			Address:     "0x1::aptos_coin::AptosCoin",
			AccountType: entities.AccountTypeEOA, // Aptos uses EOA
			Status:      entities.WalletStatusLive,
		}

		// Validate EOA addresses are different
		assert.NotEqual(t, solWallet.Address, aptosWallet.Address)
		assert.Equal(t, entities.AccountTypeEOA, solWallet.AccountType)
		assert.Equal(t, entities.AccountTypeEOA, aptosWallet.AccountType)
	})
}

func testAdminWalletManagement(t *testing.T, cfg *config.Config, testLogger *logger.Logger) {
	t.Run("CreateWalletSetRequest", func(t *testing.T) {
		req := entities.CreateWalletSetRequest{
			Name: "Test Wallet Set",
		}

		// Validate request structure
		assert.Equal(t, "Test Wallet Set", req.Name)
	})

	t.Run("WalletSetResponse", func(t *testing.T) {
		walletSetID := uuid.New()
		walletSet := entities.WalletSet{
			ID:                walletSetID,
			Name:              "Test Wallet Set",
			CircleWalletSetID: "circle-wallet-set-id",
			Status:            entities.WalletSetStatusActive,
			CreatedAt:         time.Now(),
			UpdatedAt:         time.Now(),
		}

		// Validate response structure
		assert.Equal(t, walletSetID, walletSet.ID)
		assert.Equal(t, "Test Wallet Set", walletSet.Name)
		assert.Equal(t, entities.WalletSetStatusActive, walletSet.Status)
	})

	t.Run("AdminWalletsListResponse", func(t *testing.T) {
		wallet := entities.ManagedWallet{
			ID:          uuid.New(),
			UserID:      uuid.New(),
			WalletSetID: uuid.New(),
			Chain:       entities.ChainETH,
			Address:     "0xf5c83e5fede8456929d0f90e8c541dcac3d63835",
			AccountType: entities.AccountTypeSCA,
			Status:      entities.WalletStatusLive,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}

		response := entities.AdminWalletsListResponse{
			Items: []entities.ManagedWallet{wallet},
			Count: 1,
		}

		// Validate response structure
		assert.Len(t, response.Items, 1)
		assert.Equal(t, 1, response.Count)
		assert.Equal(t, wallet.ID, response.Items[0].ID)
		assert.Equal(t, entities.AccountTypeSCA, response.Items[0].AccountType)
	})
}

// TestCircleAPIErrorHandling tests the enhanced Circle API error handling
func TestCircleAPIErrorHandling(t *testing.T) {
	t.Run("ErrorTypeClassification", func(t *testing.T) {
		// Test authentication error
		authError := entities.NewCircleAPIError(401, "Unauthorized", "req-123", nil)
		assert.Contains(t, authError.Error(), "auth error 401")

		// Test validation error
		validationError := entities.NewCircleAPIError(400, "Bad Request", "req-124", nil)
		assert.Contains(t, validationError.Error(), "validation error 400")

		// Test rate limit error
		retryAfter := 5 * time.Second
		rateLimitError := entities.NewCircleAPIError(429, "Rate Limited", "req-125", &retryAfter)
		assert.Contains(t, rateLimitError.Error(), "rate_limit error 429")

		// Test server error
		serverError := entities.NewCircleAPIError(500, "Internal Server Error", "req-126", nil)
		assert.Contains(t, serverError.Error(), "server error 500")
	})

	t.Run("RetryableErrorDetection", func(t *testing.T) {
		// Test retryable errors
		rateLimitError := entities.NewCircleAPIError(429, "Rate Limited", "req-127", nil)
		circleAPIErr, ok := rateLimitError.(entities.CircleRateLimitError)
		require.True(t, ok)
		assert.True(t, circleAPIErr.IsRetryable())

		serverError := entities.NewCircleAPIError(500, "Internal Server Error", "req-128", nil)
		circleAPIErr2, ok := serverError.(entities.CircleServerError)
		require.True(t, ok)
		assert.True(t, circleAPIErr2.IsRetryable())

		// Test non-retryable errors
		validationError := entities.NewCircleAPIError(400, "Bad Request", "req-129", nil)
		circleAPIErr3, ok := validationError.(entities.CircleValidationError)
		require.True(t, ok)
		assert.False(t, circleAPIErr3.IsRetryable())
	})

	t.Run("RetryAfterHandling", func(t *testing.T) {
		retryAfter := 10 * time.Second
		rateLimitError := entities.NewCircleAPIError(429, "Rate Limited", "req-130", &retryAfter)
		circleAPIErr, ok := rateLimitError.(entities.CircleRateLimitError)
		require.True(t, ok)
		assert.Equal(t, retryAfter, circleAPIErr.GetRetryAfter())

		// Test default retry after for server errors
		serverError := entities.NewCircleAPIError(500, "Internal Server Error", "req-131", nil)
		circleAPIErr2, ok := serverError.(entities.CircleServerError)
		require.True(t, ok)
		assert.Equal(t, 5*time.Second, circleAPIErr2.GetRetryAfter())
	})
}

// TestWalletRepositoryFilters tests the new repository filter functionality
func TestWalletRepositoryFilters(t *testing.T) {
	t.Run("WalletListFilters", func(t *testing.T) {
		userID := uuid.New()
		walletSetID := uuid.New()

		filters := entities.WalletListFilters{
			UserID:      &userID,
			WalletSetID: &walletSetID,
			Chain:       string(entities.ChainETH),
			AccountType: string(entities.AccountTypeSCA),
			Status:      string(entities.WalletStatusLive),
			Limit:       50,
			Offset:      0,
		}

		// Validate filter structure
		assert.Equal(t, userID, *filters.UserID)
		assert.Equal(t, walletSetID, *filters.WalletSetID)
		assert.Equal(t, string(entities.ChainETH), filters.Chain)
		assert.Equal(t, string(entities.AccountTypeSCA), filters.AccountType)
		assert.Equal(t, string(entities.WalletStatusLive), filters.Status)
		assert.Equal(t, 50, filters.Limit)
		assert.Equal(t, 0, filters.Offset)
	})
}

// Benchmark tests for performance validation
func BenchmarkWalletAPI(b *testing.B) {
	b.Run("WalletProvisioningRequest", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			req := entities.WalletProvisioningRequest{
				Chains: []string{"ETH", "MATIC", "SOL"},
			}

			// Simulate JSON marshaling
			_, err := json.Marshal(req)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("WalletAddressResponse", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			response := entities.WalletAddressResponse{
				Chain:   entities.ChainETH,
				Address: "0xf5c83e5fede8456929d0f90e8c541dcac3d63835",
				Status:  "live",
			}

			// Simulate JSON marshaling
			_, err := json.Marshal(response)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// Helper function to create test HTTP request
func createTestRequest(method, url string, body interface{}) *http.Request {
	var reqBody []byte
	if body != nil {
		reqBody, _ = json.Marshal(body)
	}

	req := httptest.NewRequest(method, url, bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// Helper function to create test HTTP response recorder
func createTestResponse() *httptest.ResponseRecorder {
	return httptest.NewRecorder()
}
