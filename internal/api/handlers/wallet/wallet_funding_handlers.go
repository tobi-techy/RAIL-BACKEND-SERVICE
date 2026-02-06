package wallet

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/funding"
	"github.com/rail-service/rail_service/internal/domain/services/investing"
	"github.com/rail-service/rail_service/internal/domain/services/wallet"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/rail-service/rail_service/pkg/retry"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// WalletFundingHandlers consolidates wallet, funding, investing, and withdrawal handlers
type WalletFundingHandlers struct {
	walletService       *wallet.Service
	fundingService      *funding.Service
	withdrawalService   FundingWithdrawalService
	investingService    *investing.Service
	allocationProvider  AllocationBalanceProvider
	validator           *validator.Validate
	webhookSecret       string
	skipSignatureVerify bool // Only true in development when secret is not configured
	logger              *logger.Logger
}

// NewWalletFundingHandlers creates a new instance of consolidated wallet/funding handlers
func NewWalletFundingHandlers(
	walletService *wallet.Service,
	fundingService *funding.Service,
	withdrawalService FundingWithdrawalService,
	investingService *investing.Service,
	logger *logger.Logger,
) *WalletFundingHandlers {
	return &WalletFundingHandlers{
		walletService:     walletService,
		fundingService:    fundingService,
		withdrawalService: withdrawalService,
		investingService:  investingService,
		validator:         validator.New(),
		logger:            logger,
	}
}

// SetWebhookSecret sets the webhook secret for signature verification
// skipVerify should only be true in development/testing environments
func (h *WalletFundingHandlers) SetWebhookSecret(secret string, skipVerify bool) {
	h.webhookSecret = secret
	h.skipSignatureVerify = skipVerify
}

// Request/Response models

type CreateWalletsRequest struct {
	UserID string   `json:"user_id" validate:"required,uuid"`
	Chains []string `json:"chains" validate:"required,min=1"`
}

// Legacy handler factories for compatibility
func GetWalletAddresses(db *sql.DB, cfg *config.Config, log *logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusNotImplemented, gin.H{
			"error":   "Not implemented yet",
			"message": "Use WalletHandlers.GetWalletAddresses instead",
		})
	}
}

func GetWalletStatus(db *sql.DB, cfg *config.Config, log *logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusNotImplemented, gin.H{
			"error":   "Not implemented yet",
			"message": "Use WalletHandlers.GetWalletStatus instead",
		})
	}
}

// FundingWithdrawalService interface for withdrawal operations
type FundingWithdrawalService interface {
	InitiateWithdrawal(ctx context.Context, req *entities.InitiateWithdrawalRequest) (*entities.InitiateWithdrawalResponse, error)
	GetWithdrawal(ctx context.Context, withdrawalID uuid.UUID) (*entities.Withdrawal, error)
	GetUserWithdrawals(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.Withdrawal, error)
}

// IsWebhookRetryableError determines if a webhook processing error should be retried
func IsWebhookRetryableError(err error) bool {
	if err == nil {
		return false
	}

	errorMsg := err.Error()

	// Don't retry client errors or validation errors
	if strings.Contains(errorMsg, "invalid") ||
		strings.Contains(errorMsg, "malformed") ||
		strings.Contains(errorMsg, "already processed") ||
		strings.Contains(errorMsg, "duplicate") {
		return false
	}

	// Retry on temporary failures
	if strings.Contains(errorMsg, "timeout") ||
		strings.Contains(errorMsg, "connection") ||
		strings.Contains(errorMsg, "temporary") ||
		strings.Contains(errorMsg, "unavailable") {
		return true
	}

	// By default, retry server errors (5xx equivalent)
	return true
}

// GetWalletAddresses handles GET /wallet/addresses
// @Summary Get wallet addresses
// @Description Returns wallet addresses for the authenticated user, optionally filtered by chain
// @Tags wallet
// @Produce json
// @Param chain query string false "Blockchain network" Enums(ETH,SOL,APTOS)
// @Success 200 {object} entities.WalletAddressesResponse
// @Failure 400 {object} entities.ErrorResponse
// @Failure 404 {object} entities.ErrorResponse "User not found"
// @Failure 500 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/wallet/addresses [get]
func (h *WalletFundingHandlers) GetWalletAddresses(c *gin.Context) {
	ctx := c.Request.Context()

	// Get user ID from authenticated context
	userID, err := common.GetUserID(c)
	if err != nil {
		h.logger.Warn("Invalid or missing user ID", zap.Error(err))
		common.RespondBadRequest(c, "Invalid or missing user ID", map[string]interface{}{"error": err.Error()})
		return
	}

	h.logger.Debug("Getting wallet addresses",
		zap.String("user_id", userID.String()),
		zap.String("request_id", common.GetRequestID(c)))

	// Parse optional chain filter
	var chainFilter *entities.WalletChain
	if chainQuery := c.Query("chain"); chainQuery != "" {
		chain := entities.WalletChain(chainQuery)
		if !chain.IsValid() {
			h.logger.Warn("Invalid chain parameter", zap.String("chain", chainQuery))
			c.JSON(http.StatusBadRequest, entities.ErrorResponse{
				Code:    "INVALID_CHAIN",
				Message: "Invalid blockchain network",
				Details: map[string]interface{}{
					"chain":            chainQuery,
					"supported_chains": []string{"ETH", "ETH-SEPOLIA", "SOL", "SOL-DEVNET", "APTOS", "APTOS-TESTNET"},
				},
			})
			return
		}
		chainFilter = &chain
	}

	// Get wallet addresses
	response, err := h.walletService.GetWalletAddresses(ctx, userID, chainFilter)
	if err != nil {
		h.logger.Error("Failed to get wallet addresses",
			zap.Error(err),
			zap.String("user_id", userID.String()))

		if common.IsUserNotFoundError(err) {
			c.JSON(http.StatusNotFound, entities.ErrorResponse{
				Code:    "USER_NOT_FOUND",
				Message: "User not found",
				Details: map[string]interface{}{"user_id": userID.String()},
			})
			return
		}

		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "WALLET_RETRIEVAL_FAILED",
			Message: "Failed to retrieve wallet addresses",
			Details: map[string]interface{}{"error": "Internal server error"},
		})
		return
	}

	h.logger.Debug("Retrieved wallet addresses successfully",
		zap.String("user_id", userID.String()),
		zap.Int("wallet_count", len(response.Wallets)))

	c.JSON(http.StatusOK, response)
}

// GetWalletStatus handles GET /wallet/status
// @Summary Get wallet status
// @Description Returns comprehensive wallet status for the authenticated user including provisioning progress
// @Tags wallet
// @Produce json
// @Success 200 {object} entities.WalletStatusResponse
// @Failure 400 {object} entities.ErrorResponse
// @Failure 404 {object} entities.ErrorResponse "User not found"
// @Failure 500 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/wallet/status [get]
func (h *WalletFundingHandlers) GetWalletStatus(c *gin.Context) {
	ctx := c.Request.Context()

	// Get user ID from authenticated context
	userID, err := common.GetUserID(c)
	if err != nil {
		h.logger.Warn("Invalid or missing user ID", zap.Error(err))
		common.RespondBadRequest(c, "Invalid or missing user ID", map[string]interface{}{"error": err.Error()})
		return
	}

	h.logger.Debug("Getting wallet status",
		zap.String("user_id", userID.String()),
		zap.String("request_id", common.GetRequestID(c)))

	// Get wallet status
	response, err := h.walletService.GetWalletStatus(ctx, userID)
	if err != nil {
		h.logger.Error("Failed to get wallet status",
			zap.Error(err),
			zap.String("user_id", userID.String()))

		if common.IsUserNotFoundError(err) {
			c.JSON(http.StatusNotFound, entities.ErrorResponse{
				Code:    "USER_NOT_FOUND",
				Message: "User not found",
				Details: map[string]interface{}{"user_id": userID.String()},
			})
			return
		}

		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "WALLET_STATUS_FAILED",
			Message: "Failed to retrieve wallet status",
			Details: map[string]interface{}{"error": "Internal server error"},
		})
		return
	}

	h.logger.Debug("Retrieved wallet status successfully",
		zap.String("user_id", userID.String()),
		zap.Int("total_wallets", response.TotalWallets),
		zap.Int("ready_wallets", response.ReadyWallets))

	c.JSON(http.StatusOK, response)
}

// CreateWalletsForUser handles POST /wallet/create (Admin only)
// @Summary Create wallets for user
// @Description Manually trigger wallet creation for a user (Admin only)
// @Tags wallet
// @Accept json
// @Produce json
// @Param request body CreateWalletsRequest true "Wallet creation request"
// @Success 202 {object} map[string]interface{} "Wallet creation initiated"
// @Failure 400 {object} entities.ErrorResponse
// @Failure 403 {object} entities.ErrorResponse "Insufficient permissions"
// @Failure 500 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/wallet/create [post]
func (h *WalletFundingHandlers) CreateWalletsForUser(c *gin.Context) {
	ctx := c.Request.Context()

	h.logger.Info("Manual wallet creation requested",
		zap.String("request_id", common.GetRequestID(c)),
		zap.String("ip", c.ClientIP()))

	var req CreateWalletsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("Invalid wallet creation request payload", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_REQUEST",
			Message: "Invalid wallet creation request payload",
			Details: map[string]interface{}{"error": err.Error()},
		})
		return
	}

	// Validate request
	if err := h.validator.Struct(req); err != nil {
		h.logger.Warn("Wallet creation request validation failed", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "VALIDATION_ERROR",
			Message: "Wallet creation request validation failed",
			Details: map[string]interface{}{"validation_errors": err.Error()},
		})
		return
	}

	// Parse user ID
	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		h.logger.Warn("Invalid user ID format", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_USER_ID",
			Message: "Invalid user ID format",
			Details: map[string]interface{}{"user_id": req.UserID},
		})
		return
	}

	// Validate chains
	var chains []entities.WalletChain
	for _, chainStr := range req.Chains {
		chain := entities.WalletChain(chainStr)
		if !chain.IsValid() {
			h.logger.Warn("Invalid chain in request", zap.String("chain", chainStr))
			c.JSON(http.StatusBadRequest, entities.ErrorResponse{
				Code:    "INVALID_CHAIN",
				Message: "Invalid blockchain network",
				Details: map[string]interface{}{
					"chain":            chainStr,
					"supported_chains": []string{"ETH", "ETH-SEPOLIA", "SOL", "SOL-DEVNET", "APTOS", "APTOS-TESTNET"},
				},
			})
			return
		}
		chains = append(chains, chain)
	}

	// Create wallets
	err = h.walletService.CreateWalletsForUser(ctx, userID, chains)
	if err != nil {
		h.logger.Error("Failed to create wallets for user",
			zap.Error(err),
			zap.String("user_id", userID.String()),
			zap.Strings("chains", req.Chains))

		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "WALLET_CREATION_FAILED",
			Message: "Failed to create wallets for user",
			Details: map[string]interface{}{"error": "Internal server error"},
		})
		return
	}

	h.logger.Info("Wallet creation initiated successfully",
		zap.String("user_id", userID.String()),
		zap.Strings("chains", req.Chains))

	c.JSON(http.StatusAccepted, gin.H{
		"message":    "Wallet creation initiated",
		"user_id":    userID.String(),
		"chains":     req.Chains,
		"next_steps": []string{"Check wallet status for progress", "Wallets will be available once provisioning completes"},
	})
}

// RetryWalletProvisioning handles POST /wallet/retry (Admin only)
// @Summary Retry failed wallet provisioning
// @Description Retries failed wallet provisioning jobs (Admin only)
// @Tags wallet
// @Accept json
// @Produce json
// @Param limit query int false "Maximum number of jobs to retry" default(10)
// @Success 200 {object} map[string]interface{} "Retry initiated"
// @Failure 400 {object} entities.ErrorResponse
// @Failure 403 {object} entities.ErrorResponse "Insufficient permissions"
// @Failure 500 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/wallet/retry [post]
func (h *WalletFundingHandlers) RetryWalletProvisioning(c *gin.Context) {
	ctx := c.Request.Context()

	h.logger.Info("Wallet provisioning retry requested",
		zap.String("request_id", common.GetRequestID(c)),
		zap.String("ip", c.ClientIP()))

	// Parse limit parameter
	limit := 10 // default
	if limitQuery := c.Query("limit"); limitQuery != "" {
		if parsedLimit, err := strconv.Atoi(limitQuery); err == nil && parsedLimit > 0 {
			limit = parsedLimit
		}
	}

	// Retry failed provisioning jobs
	err := h.walletService.RetryFailedWalletProvisioning(ctx, limit)
	if err != nil {
		h.logger.Error("Failed to retry wallet provisioning", zap.Error(err))

		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "RETRY_FAILED",
			Message: "Failed to retry wallet provisioning",
			Details: map[string]interface{}{"error": "Internal server error"},
		})
		return
	}

	h.logger.Info("Wallet provisioning retry initiated", zap.Int("limit", limit))

	c.JSON(http.StatusOK, gin.H{
		"message": "Wallet provisioning retry initiated",
		"limit":   limit,
	})
}

// HealthCheck handles GET /wallet/health (Admin only)
// @Summary Wallet service health check
// @Description Returns health status of wallet service and Circle integration
// @Tags wallet
// @Produce json
// @Success 200 {object} map[string]interface{} "Health status"
// @Failure 500 {object} entities.ErrorResponse
// @Router /api/v1/wallet/health [get]
func (h *WalletFundingHandlers) HealthCheck(c *gin.Context) {
	ctx := c.Request.Context()

	h.logger.Debug("Wallet service health check requested")

	// Perform health check
	err := h.walletService.HealthCheck(ctx)
	if err != nil {
		h.logger.Error("Wallet service health check failed", zap.Error(err))

		c.JSON(http.StatusServiceUnavailable, entities.ErrorResponse{
			Code:    "HEALTH_CHECK_FAILED",
			Message: "Wallet service health check failed",
			Details: map[string]interface{}{"error": err.Error()},
		})
		return
	}

	// Get metrics
	metrics := h.walletService.GetMetrics()

	h.logger.Debug("Wallet service health check passed")

	c.JSON(http.StatusOK, gin.H{
		"status":  "healthy",
		"service": "wallet",
		"metrics": metrics,
	})
}

// InitiateWalletCreation handles POST /api/v1/wallets/initiate
// @Summary Initiate developer-controlled wallet creation after passcode verification
// @Description Creates developer-controlled wallets using pre-registered Entity Secret Ciphertext across specified testnet chains after passcode verification
// @Tags wallet
// @Accept json
// @Produce json
// @Param request body entities.WalletInitiationRequest true "Wallet initiation request with optional chains"
// @Success 202 {object} entities.WalletInitiationResponse
// @Failure 400 {object} entities.ErrorResponse
// @Failure 500 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/wallets/initiate [post]
func (h *WalletFundingHandlers) InitiateWalletCreation(c *gin.Context) {
	ctx := c.Request.Context()

	var req entities.WalletInitiationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("Invalid wallet initiation request", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_REQUEST",
			Message: "Invalid request payload",
			Details: map[string]interface{}{"error": err.Error()},
		})
		return
	}

	// Get user ID from context (set by auth middleware)
	userIDValue, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{
			Code:    "UNAUTHORIZED",
			Message: "User ID not found in context",
		})
		return
	}

	// Handle both uuid.UUID and string types
	var userID uuid.UUID
	var err error
	switch v := userIDValue.(type) {
	case uuid.UUID:
		userID = v
	case string:
		userID, err = uuid.Parse(v)
		if err != nil {
			h.logger.Error("Invalid user ID string in context", zap.Error(err))
			c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
				Code:    "INTERNAL_ERROR",
				Message: "Invalid user context",
			})
			return
		}
	default:
		h.logger.Error("Unexpected user ID type in context", zap.Any("type", v))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "INTERNAL_ERROR",
			Message: "Invalid user context",
		})
		return
	}

	// Default to SOL-DEVNET if not specified
	chains := req.Chains
	if len(chains) == 0 {
		chains = []string{string(entities.WalletChainSOLDevnet)}
	}

	// Validate chains - ensure only testnet chains
	for _, chainStr := range chains {
		chain := entities.WalletChain(chainStr)
		if !chain.IsValid() {
			h.logger.Warn("Invalid chain in request", zap.String("chain", chainStr))
			c.JSON(http.StatusBadRequest, entities.ErrorResponse{
				Code:    "INVALID_CHAIN",
				Message: "Invalid blockchain network",
				Details: map[string]interface{}{
					"chain":            chainStr,
					"supported_chains": []string{"SOL-DEVNET"},
				},
			})
			return
		}

		// Ensure only testnet chains
		if !chain.IsTestnet() {
			h.logger.Warn("Mainnet chain not supported for wallet creation", zap.String("chain", chainStr))
			c.JSON(http.StatusBadRequest, entities.ErrorResponse{
				Code:    "MAINNET_NOT_SUPPORTED",
				Message: "Only SOL-DEVNET is supported at this time",
				Details: map[string]interface{}{
					"requested_chain":  chainStr,
					"supported_chains": []string{"SOL-DEVNET"},
				},
			})
			return
		}
	}

	// Convert chain strings to entities
	var chainEntities []entities.WalletChain
	for _, chainStr := range chains {
		chainEntities = append(chainEntities, entities.WalletChain(chainStr))
	}

	h.logger.Info("Initiating developer-controlled wallet creation for user",
		zap.String("user_id", userID.String()),
		zap.Strings("chains", chains))

	// Create developer-controlled wallets for user
	err = h.walletService.CreateWalletsForUser(ctx, userID, chainEntities)
	if err != nil {
		h.logger.Error("Failed to initiate developer-controlled wallet creation",
			zap.Error(err),
			zap.String("user_id", userID.String()))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "WALLET_INITIATION_FAILED",
			Message: "Failed to initiate developer-controlled wallet creation",
			Details: map[string]interface{}{"error": "Internal server error"},
		})
		return
	}

	// Get provisioning job status
	job, err := h.walletService.GetProvisioningJobByUserID(ctx, userID)
	if err != nil {
		h.logger.Warn("Failed to get provisioning job status", zap.Error(err))
		// Don't fail the request, just return a basic response
		c.JSON(http.StatusAccepted, entities.WalletInitiationResponse{
			Message: "Developer-controlled wallet creation initiated",
			UserID:  userID.String(),
			Chains:  chains,
		})
		return
	}

	response := entities.WalletInitiationResponse{
		Message: "Developer-controlled wallet creation initiated successfully",
		UserID:  userID.String(),
		Chains:  chains,
		Job: &entities.WalletProvisioningJobResponse{
			ID:           job.ID,
			Status:       string(job.Status),
			Progress:     "0%",
			AttemptCount: job.AttemptCount,
			MaxAttempts:  job.MaxAttempts,
			ErrorMessage: job.ErrorMessage,
			NextRetryAt:  job.NextRetryAt,
			CreatedAt:    job.CreatedAt,
		},
	}

	h.logger.Info("Developer-controlled wallet creation initiated",
		zap.String("user_id", userID.String()),
		zap.String("job_id", job.ID.String()),
		zap.Strings("chains", chains))

	c.JSON(http.StatusAccepted, response)
}

// ProvisionWallets handles POST /api/v1/wallets/provision
// @Summary Provision wallets for user
// @Description Triggers wallet provisioning across supported chains for the authenticated user
// @Tags wallet
// @Accept json
// @Produce json
// @Param request body entities.WalletProvisioningRequest true "Wallet provisioning request"
// @Success 202 {object} entities.WalletProvisioningResponse
// @Failure 400 {object} entities.ErrorResponse
// @Failure 500 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/wallets/provision [post]
func (h *WalletFundingHandlers) ProvisionWallets(c *gin.Context) {
	ctx := c.Request.Context()

	var req entities.WalletProvisioningRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("Invalid wallet provisioning request", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "INVALID_REQUEST",
			"message": "Invalid request payload",
		})
		return
	}

	// Get user ID from context (set by auth middleware)
	userIDValue, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":   "UNAUTHORIZED",
			"message": "User ID not found in context",
		})
		return
	}

	// Handle both uuid.UUID and string types
	var userID uuid.UUID
	var err error
	switch v := userIDValue.(type) {
	case uuid.UUID:
		userID = v
	case string:
		userID, err = uuid.Parse(v)
		if err != nil {
			h.logger.Error("Invalid user ID string in context", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "INTERNAL_ERROR",
				"message": "Invalid user context",
			})
			return
		}
	default:
		h.logger.Error("Unexpected user ID type in context", zap.Any("type", v))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "INTERNAL_ERROR",
			"message": "Invalid user context",
		})
		return
	}

	// Convert chain strings to entities
	var chains []entities.WalletChain
	if len(req.Chains) > 0 {
		for _, chainStr := range req.Chains {
			chain := entities.WalletChain(chainStr)
			if !chain.IsValid() {
				c.JSON(http.StatusBadRequest, gin.H{
					"error":   "INVALID_CHAIN",
					"message": fmt.Sprintf("Invalid chain: %s", chainStr),
				})
				return
			}
			chains = append(chains, chain)
		}
	}

	// Create wallets for user
	err = h.walletService.CreateWalletsForUser(ctx, userID, chains)
	if err != nil {
		h.logger.Error("Failed to create wallets for user",
			zap.Error(err),
			zap.String("user_id", userID.String()))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "PROVISIONING_FAILED",
			"message": "Failed to start wallet provisioning",
		})
		return
	}

	// Get provisioning job status
	job, err := h.walletService.GetProvisioningJobByUserID(ctx, userID)
	if err != nil {
		h.logger.Warn("Failed to get provisioning job status", zap.Error(err))
		// Don't fail the request, just return a basic response
		c.JSON(http.StatusAccepted, gin.H{
			"message": "Wallet provisioning started",
			"user_id": userID.String(),
		})
		return
	}

	response := entities.WalletProvisioningResponse{
		Message: "Wallet provisioning started",
		Job: entities.WalletProvisioningJobResponse{
			ID:           job.ID,
			Status:       string(job.Status),
			Progress:     "0%",
			AttemptCount: job.AttemptCount,
			MaxAttempts:  job.MaxAttempts,
			ErrorMessage: job.ErrorMessage,
			NextRetryAt:  job.NextRetryAt,
			CreatedAt:    job.CreatedAt,
		},
	}

	c.JSON(http.StatusAccepted, response)
}

// GetWalletByChain handles GET /api/v1/wallets/:chain/address
// @Summary Get wallet address for specific chain
// @Description Returns the wallet address for the authenticated user on the specified chain
// @Tags wallet
// @Produce json
// @Param chain path string true "Blockchain network" Enums(ETH,ETH-SEPOLIA,MATIC,MATIC-AMOY,SOL,SOL-DEVNET,APTOS,APTOS-TESTNET,AVAX,BASE,BASE-SEPOLIA)
// @Success 200 {object} entities.WalletAddressResponse
// @Failure 400 {object} entities.ErrorResponse
// @Failure 404 {object} entities.ErrorResponse "Wallet not found for chain"
// @Failure 500 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/wallets/{chain}/address [get]
func (h *WalletFundingHandlers) GetWalletByChain(c *gin.Context) {
	ctx := c.Request.Context()

	chainStr := c.Param("chain")
	chain := entities.WalletChain(chainStr)

	if !chain.IsValid() {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "INVALID_CHAIN",
			"message": fmt.Sprintf("Invalid chain: %s", chainStr),
		})
		return
	}

	// Get user ID from context
	userIDValue, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":   "UNAUTHORIZED",
			"message": "User ID not found in context",
		})
		return
	}

	// Handle both uuid.UUID and string types
	var userID uuid.UUID
	var err error
	switch v := userIDValue.(type) {
	case uuid.UUID:
		userID = v
	case string:
		userID, err = uuid.Parse(v)
		if err != nil {
			h.logger.Error("Invalid user ID string in context", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "INTERNAL_ERROR",
				"message": "Invalid user context",
			})
			return
		}
	default:
		h.logger.Error("Unexpected user ID type in context", zap.Any("type", v))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "INTERNAL_ERROR",
			"message": "Invalid user context",
		})
		return
	}

	// Get wallet for the specific chain
	wallet, err := h.walletService.GetWalletByUserAndChain(ctx, userID, chain)
	if err != nil {
		h.logger.Warn("Wallet not found for chain",
			zap.Error(err),
			zap.String("user_id", userID.String()),
			zap.String("chain", chainStr))
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "WALLET_NOT_FOUND",
			"message": fmt.Sprintf("No wallet found for chain: %s", chainStr),
		})
		return
	}

	response := entities.WalletAddressResponse{
		Chain:   chain,
		Address: wallet.Address,
		Status:  string(wallet.Status),
	}

	c.JSON(http.StatusOK, response)
}

// === Funding Handlers ===

// CreateDepositAddress creates a deposit address for a specific chain
// @Summary Create deposit address
// @Description Generate or retrieve a deposit address for a specific blockchain
// @Tags funding
// @Accept json
// @Produce json
// @Param request body entities.DepositAddressRequest true "Deposit address request"
// @Success 200 {object} entities.DepositAddressResponse
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/funding/deposit-address [post]
func (h *WalletFundingHandlers) CreateDepositAddress(c *gin.Context) {
	var req entities.DepositAddressRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.RespondBadRequest(c, "Invalid request format", map[string]interface{}{"error": err.Error()})
		return
	}

	userUUID, err := common.GetUserID(c)
	if err != nil {
		h.logger.Error("Failed to get user ID", "error", err)
		common.RespondUnauthorized(c, "User not authenticated")
		return
	}

	response, err := h.fundingService.CreateDepositAddress(c.Request.Context(), userUUID, req.Chain)
	if err != nil {
		h.logger.Error("Failed to create deposit address", "error", err, "user_id", userUUID, "chain", req.Chain)
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "DEPOSIT_ADDRESS_ERROR",
			Message: "Failed to create deposit address",
		})
		return
	}

	c.JSON(http.StatusOK, response)
}

// GetFundingConfirmations lists recent funding confirmations
// @Summary Get funding confirmations
// @Description Retrieve recent funding confirmations for the authenticated user
// @Tags funding
// @Produce json
// @Param limit query int false "Number of results to return" default(20)
// @Param offset query int false "Number of results to skip" default(0)
// @Success 200 {array} entities.FundingConfirmation
// @Failure 401 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/funding/confirmations [get]
func (h *WalletFundingHandlers) GetFundingConfirmations(c *gin.Context) {
	userUUID, err := common.GetUserID(c)
	if err != nil {
		h.logger.Error("Failed to get user ID", "error", err)
		common.RespondUnauthorized(c, "User not authenticated")
		return
	}

	// Parse query parameters
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if limit > 100 {
		limit = 100
	}
	if limit < 1 {
		limit = 20
	}

	offset := 0
	if cursor := c.Query("cursor"); cursor != "" {
		if o, err := strconv.Atoi(cursor); err == nil && o >= 0 {
			offset = o
		}
	}

	confirmations, err := h.fundingService.GetFundingConfirmations(c.Request.Context(), userUUID, limit, offset)
	if err != nil {
		h.logger.Error("Failed to get funding confirmations", "error", err, "user_id", userUUID)
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "CONFIRMATIONS_ERROR",
			Message: "Failed to retrieve funding confirmations",
		})
		return
	}

	// Prepare paginated response as per OpenAPI spec
	response := entities.FundingConfirmationsPage{
		Items:      confirmations,
		NextCursor: nil,
	}

	// Add next cursor if we have more results
	if len(confirmations) == limit {
		nextCursor := strconv.Itoa(offset + limit)
		response.NextCursor = &nextCursor
	}

	c.JSON(http.StatusOK, response)
}

// UnifiedBalanceResponse represents the unified balance response
type UnifiedBalanceResponse struct {
	TotalUSDC       string `json:"total_usdc"`
	SpendingBalance string `json:"spending_balance"`
	StashBalance    string `json:"stash_balance"`
	AllocationMode  string `json:"allocation_mode"` // "active" or "disabled"
	Currency        string `json:"currency"`
	LastUpdated     string `json:"last_updated"`
}

// AllocationBalanceProvider is the interface for getting allocation balances
type AllocationBalanceProvider interface {
	GetBalances(ctx context.Context, userID uuid.UUID) (*entities.AllocationBalances, error)
}

// SetAllocationBalanceProvider sets the allocation service for unified balance queries
func (h *WalletFundingHandlers) SetAllocationBalanceProvider(provider AllocationBalanceProvider) {
	h.allocationProvider = provider
}

// GetUnifiedBalances returns user's unified balance across all accounts
// @Summary Get unified user balances
// @Description Returns total_usdc, spending_balance, stash_balance, and allocation_mode
// @Tags balances
// @Produce json
// @Success 200 {object} UnifiedBalanceResponse
// @Failure 401 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/balances [get]
func (h *WalletFundingHandlers) GetUnifiedBalances(c *gin.Context) {
	userUUID, err := common.GetUserID(c)
	if err != nil {
		h.logger.Error("Failed to get user ID", "error", err)
		common.RespondUnauthorized(c, "User not authenticated")
		return
	}

	ctx := c.Request.Context()

	// Query allocation service for the real spending/stash breakdown
	if h.allocationProvider != nil {
		balances, err := h.allocationProvider.GetBalances(ctx, userUUID)
		if err == nil {
			mode := "disabled"
			if balances.ModeActive {
				mode = "active"
			}
			c.JSON(http.StatusOK, UnifiedBalanceResponse{
				TotalUSDC:       balances.TotalBalance.StringFixed(2),
				SpendingBalance: balances.SpendingBalance.StringFixed(2),
				StashBalance:    balances.StashBalance.StringFixed(2),
				AllocationMode:  mode,
				Currency:        "USD",
				LastUpdated:     time.Now().UTC().Format(time.RFC3339),
			})
			return
		}
		h.logger.Warn("Allocation balance query failed, falling back to ledger", "error", err, "user_id", userUUID)
	}

	// Fallback: ledger-only balance (no allocation breakdown)
	balances, err := h.fundingService.GetBalance(ctx, userUUID)
	if err != nil {
		h.logger.Error("Failed to get balances", "error", err, "user_id", userUUID)
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "BALANCES_ERROR",
			Message: "Failed to retrieve balances",
		})
		return
	}

	c.JSON(http.StatusOK, UnifiedBalanceResponse{
		TotalUSDC:       balances.BuyingPower,
		SpendingBalance: balances.BuyingPower,
		StashBalance:    "0.00",
		AllocationMode:  "disabled",
		Currency:        balances.Currency,
		LastUpdated:     time.Now().UTC().Format(time.RFC3339),
	})
}

// GetBalances returns user's current balance (DEPRECATED - use GetUnifiedBalances)
// @Summary Get user balances (deprecated)
// @Description Get the authenticated user's current buying power and pending deposits
// @Tags funding
// @Produce json
// @Success 200 {object} entities.BalancesResponse
// @Failure 401 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/funding/balances [get]
// @Deprecated
func (h *WalletFundingHandlers) GetBalances(c *gin.Context) {
	userUUID, err := common.GetUserID(c)
	if err != nil {
		h.logger.Error("Failed to get user ID", "error", err)
		common.RespondUnauthorized(c, "User not authenticated")
		return
	}

	balances, err := h.fundingService.GetBalance(c.Request.Context(), userUUID)
	if err != nil {
		h.logger.Error("Failed to get balances", "error", err, "user_id", userUUID)
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "BALANCES_ERROR",
			Message: "Failed to retrieve balances",
		})
		return
	}

	c.JSON(http.StatusOK, balances)
}

// CreateVirtualAccount creates a virtual account linked to an Alpaca brokerage account
// @Summary Create virtual account
// @Description Create a virtual account for funding a brokerage account with stablecoins
// @Tags funding
// @Accept json
// @Produce json
// @Param request body entities.CreateVirtualAccountRequest true "Virtual account creation request"
// @Success 201 {object} entities.CreateVirtualAccountResponse
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Failure 409 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/funding/virtual-account [post]
func (h *WalletFundingHandlers) CreateVirtualAccount(c *gin.Context) {
	var req entities.CreateVirtualAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.RespondBadRequest(c, "Invalid request format", map[string]interface{}{"error": err.Error()})
		return
	}

	userUUID, err := common.GetUserID(c)
	if err != nil {
		h.logger.Error("Failed to get user ID", "error", err)
		common.RespondUnauthorized(c, "User not authenticated")
		return
	}

	// Set user ID from context
	req.UserID = userUUID

	// Validate Alpaca account ID
	if req.AlpacaAccountID == "" {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_REQUEST",
			Message: "Alpaca account ID is required",
		})
		return
	}

	response, err := h.fundingService.CreateVirtualAccount(c.Request.Context(), &req)
	if err != nil {
		h.logger.Error("Failed to create virtual account",
			"error", err,
			"user_id", userUUID,
			"alpaca_account_id", req.AlpacaAccountID)

		// Handle specific error cases
		if strings.Contains(err.Error(), "already exists") {
			c.JSON(http.StatusConflict, entities.ErrorResponse{
				Code:    "VIRTUAL_ACCOUNT_EXISTS",
				Message: "Virtual account already exists for this Alpaca account",
			})
			return
		}

		if strings.Contains(err.Error(), "not active") {
			c.JSON(http.StatusBadRequest, entities.ErrorResponse{
				Code:    "ALPACA_ACCOUNT_INACTIVE",
				Message: "Alpaca account is not active",
			})
			return
		}

		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "VIRTUAL_ACCOUNT_ERROR",
			Message: "Failed to create virtual account",
		})
		return
	}

	c.JSON(http.StatusCreated, response)
}

// === Investing Handlers ===

// GetBaskets lists all available investment baskets
// @Summary Get investment baskets
// @Description Retrieve all available curated investment baskets
// @Tags investing
// @Produce json
// @Success 200 {array} entities.Basket
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/investing/baskets [get]
func (h *WalletFundingHandlers) GetBaskets(c *gin.Context) {
	baskets, err := h.investingService.ListBaskets(c.Request.Context())
	if err != nil {
		h.logger.Error("Failed to get baskets", "error", err)
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "BASKETS_ERROR",
			Message: "Failed to retrieve baskets",
		})
		return
	}

	c.JSON(http.StatusOK, baskets)
}

// GetBasket returns details of a specific basket
// @Summary Get basket details
// @Description Retrieve details of a specific investment basket
// @Tags investing
// @Produce json
// @Param basketId path string true "Basket ID"
// @Success 200 {object} entities.Basket
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/investing/baskets/{basketId} [get]
func (h *WalletFundingHandlers) GetBasket(c *gin.Context) {
	basketIDStr := c.Param("basketId")
	basketID, err := uuid.Parse(basketIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_BASKET_ID",
			Message: "Invalid basket ID format",
		})
		return
	}

	basket, err := h.investingService.GetBasket(c.Request.Context(), basketID)
	if err != nil {
		if err == investing.ErrBasketNotFound {
			c.JSON(http.StatusNotFound, entities.ErrorResponse{
				Code:    "BASKET_NOT_FOUND",
				Message: "Basket not found",
			})
			return
		}
		h.logger.Error("Failed to get basket", "error", err, "basket_id", basketID)
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "BASKET_ERROR",
			Message: "Failed to retrieve basket",
		})
		return
	}

	c.JSON(http.StatusOK, basket)
}

// CreateOrder creates a new investment order
// @Summary Create investment order
// @Description Place a buy or sell order for a basket
// @Tags investing
// @Accept json
// @Produce json
// @Param request body entities.OrderCreateRequest true "Order creation request"
// @Success 201 {object} entities.Order
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Failure 403 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/investing/orders [post]
func (h *WalletFundingHandlers) CreateOrder(c *gin.Context) {
	var req entities.OrderCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.RespondBadRequest(c, "Invalid request format", map[string]interface{}{"error": err.Error()})
		return
	}

	userUUID, err := common.GetUserID(c)
	if err != nil {
		h.logger.Error("Failed to get user ID", "error", err)
		common.RespondUnauthorized(c, "User not authenticated")
		return
	}

	order, err := h.investingService.CreateOrder(c.Request.Context(), userUUID, &req)
	if err != nil {
		switch err {
		case investing.ErrBasketNotFound:
			c.JSON(http.StatusBadRequest, entities.ErrorResponse{
				Code:    "BASKET_NOT_FOUND",
				Message: "Specified basket does not exist",
			})
			return
		case investing.ErrInvalidAmount:
			c.JSON(http.StatusBadRequest, entities.ErrorResponse{
				Code:    "INVALID_AMOUNT",
				Message: "Invalid order amount",
			})
			return
		case investing.ErrInsufficientFunds:
			c.JSON(http.StatusForbidden, entities.ErrorResponse{
				Code:    "INSUFFICIENT_FUNDS",
				Message: "Insufficient buying power for this order",
			})
			return
		case investing.ErrInsufficientPosition:
			c.JSON(http.StatusForbidden, entities.ErrorResponse{
				Code:    "INSUFFICIENT_POSITION",
				Message: "Insufficient position for sell order",
			})
			return
		default:
			h.logger.Error("Failed to create order", "error", err, "user_id", userUUID)
			c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
				Code:    "ORDER_ERROR",
				Message: "Failed to create order",
			})
			return
		}
	}

	c.JSON(http.StatusCreated, order)
}

// GetOrders lists user's orders
// @Summary Get user orders
// @Description Retrieve orders for the authenticated user
// @Tags investing
// @Produce json
// @Param limit query int false "Number of results to return" default(20)
// @Param offset query int false "Number of results to skip" default(0)
// @Param status query string false "Filter by order status"
// @Success 200 {array} entities.Order
// @Failure 401 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/investing/orders [get]
func (h *WalletFundingHandlers) GetOrders(c *gin.Context) {
	userUUID, err := common.GetUserID(c)
	if err != nil {
		h.logger.Error("Failed to get user ID", "error", err)
		common.RespondUnauthorized(c, "User not authenticated")
		return
	}

	// Parse query parameters
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	var statusFilter *entities.OrderStatus
	if statusStr := c.Query("status"); statusStr != "" {
		status := entities.OrderStatus(statusStr)
		statusFilter = &status
	}

	orders, err := h.investingService.ListOrders(c.Request.Context(), userUUID, limit, offset, statusFilter)
	if err != nil {
		h.logger.Error("Failed to get orders", "error", err, "user_id", userUUID)
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "ORDERS_ERROR",
			Message: "Failed to retrieve orders",
		})
		return
	}

	c.JSON(http.StatusOK, orders)
}

// GetOrder returns details of a specific order
// @Summary Get order details
// @Description Retrieve details of a specific order
// @Tags investing
// @Produce json
// @Param orderId path string true "Order ID"
// @Success 200 {object} entities.Order
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/investing/orders/{orderId} [get]
func (h *WalletFundingHandlers) GetOrder(c *gin.Context) {
	userUUID, err := common.GetUserID(c)
	if err != nil {
		h.logger.Error("Failed to get user ID", "error", err)
		common.RespondUnauthorized(c, "User not authenticated")
		return
	}

	orderIDStr := c.Param("orderId")
	orderID, err := uuid.Parse(orderIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_ORDER_ID",
			Message: "Invalid order ID format",
		})
		return
	}

	order, err := h.investingService.GetOrder(c.Request.Context(), userUUID, orderID)
	if err != nil {
		if err == investing.ErrOrderNotFound {
			c.JSON(http.StatusNotFound, entities.ErrorResponse{
				Code:    "ORDER_NOT_FOUND",
				Message: "Order not found",
			})
			return
		}
		h.logger.Error("Failed to get order", "error", err, "user_id", userUUID, "order_id", orderID)
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "ORDER_ERROR",
			Message: "Failed to retrieve order",
		})
		return
	}

	c.JSON(http.StatusOK, order)
}

// GetPortfolio returns user's portfolio
// @Summary Get user portfolio
// @Description Retrieve the authenticated user's complete portfolio
// @Tags investing
// @Produce json
// @Success 200 {object} entities.Portfolio
// @Failure 401 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/investing/portfolio [get]
func (h *WalletFundingHandlers) GetPortfolio(c *gin.Context) {
	userUUID, err := common.GetUserID(c)
	if err != nil {
		h.logger.Error("Failed to get user ID", "error", err)
		common.RespondUnauthorized(c, "User not authenticated")
		return
	}

	portfolio, err := h.investingService.GetPortfolio(c.Request.Context(), userUUID)
	if err != nil {
		h.logger.Error("Failed to get portfolio", "error", err, "user_id", userUUID)
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "PORTFOLIO_ERROR",
			Message: "Failed to retrieve portfolio",
		})
		return
	}

	c.JSON(http.StatusOK, portfolio)
}

// === Webhook Handlers ===

// ChainDepositWebhook handles incoming chain deposit confirmations
// @Summary Chain deposit webhook
// @Description Handle blockchain deposit confirmations
// @Tags webhooks
// @Accept json
// @Produce json
// @Param request body entities.ChainDepositWebhook true "Chain deposit webhook payload"
// @Success 200 {object} map[string]string
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/webhooks/chain-deposit [post]
func (h *WalletFundingHandlers) ChainDepositWebhook(c *gin.Context) {
	// Read raw body for signature verification
	rawBody, err := c.GetRawData()
	if err != nil {
		common.RespondBadRequest(c, "Failed to read request body", nil)
		return
	}

	// Verify webhook signature - fail closed if not configured
	if h.webhookSecret == "" {
		if h.skipSignatureVerify {
			h.logger.Warn("Webhook secret not configured - SKIPPING VERIFICATION (development mode only)")
		} else {
			h.logger.Error("Webhook secret not configured - rejecting webhook for security")
			c.JSON(http.StatusUnauthorized, entities.ErrorResponse{
				Code:    "WEBHOOK_NOT_CONFIGURED",
				Message: "Webhook signature verification not configured",
			})
			return
		}
	} else {
		signature := c.GetHeader("X-Webhook-Signature")
		if signature == "" {
			signature = c.GetHeader("X-Hub-Signature-256")
		}
		if err := verifyWebhookSignature(rawBody, signature, h.webhookSecret); err != nil {
			h.logger.Warn("Webhook signature verification failed", zap.Error(err))
			c.JSON(http.StatusUnauthorized, entities.ErrorResponse{
				Code:    "INVALID_SIGNATURE",
				Message: "Webhook signature verification failed",
			})
			return
		}
	}

	var webhook entities.ChainDepositWebhook
	if err := json.Unmarshal(rawBody, &webhook); err != nil {
		common.RespondBadRequest(c, "Invalid webhook payload", map[string]interface{}{"error": err.Error()})
		return
	}

	// Basic validation
	if webhook.TxHash == "" {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_WEBHOOK",
			Message: "Missing transaction hash",
		})
		return
	}

	if webhook.Amount == "" || webhook.Amount == "0" {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_WEBHOOK",
			Message: "Invalid amount",
		})
		return
	}

	// Process webhook with retry logic for resilience
	retryConfig := retry.RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   500 * time.Millisecond,
		MaxDelay:    5 * time.Second,
		Multiplier:  2.0,
	}

	err = retry.WithExponentialBackoff(
		c.Request.Context(),
		retryConfig,
		func() error {
			return h.fundingService.ProcessChainDeposit(c.Request.Context(), &webhook)
		},
		IsWebhookRetryableError,
	)

	if err != nil {
		h.logger.Error("Failed to process chain deposit webhook after retries",
			"error", err,
			"tx_hash", webhook.TxHash,
			"amount", webhook.Amount,
			"chain", webhook.Chain)

		// Check if it's a duplicate (idempotency case)
		if strings.Contains(err.Error(), "already processed") {
			h.logger.Info("Webhook already processed (idempotent)", "tx_hash", webhook.TxHash)
			c.JSON(http.StatusOK, gin.H{"status": "already_processed"})
			return
		}

		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "WEBHOOK_PROCESSING_ERROR",
			Message: "Failed to process deposit webhook",
			Details: map[string]interface{}{"tx_hash": webhook.TxHash},
		})
		return
	}

	h.logger.Info("Webhook processed successfully",
		"tx_hash", webhook.TxHash,
		"amount", webhook.Amount,
		"chain", webhook.Chain)

	c.JSON(http.StatusOK, gin.H{"status": "processed"})
}

// BrokerageFillWebhook handles brokerage order fill notifications
// @Summary Brokerage fill webhook
// @Description Handle brokerage order fill notifications
// @Tags webhooks
// @Accept json
// @Produce json
// @Param request body entities.BrokerageFillWebhook true "Brokerage fill webhook payload"
// @Success 200 {object} map[string]string
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/webhooks/brokerage-fill [post]
func (h *WalletFundingHandlers) BrokerageFillWebhook(c *gin.Context) {
	// Read raw body for signature verification
	rawBody, err := c.GetRawData()
	if err != nil {
		common.RespondBadRequest(c, "Failed to read request body", nil)
		return
	}

	// Verify webhook signature - fail closed if not configured
	if h.webhookSecret == "" {
		if h.skipSignatureVerify {
			h.logger.Warn("Webhook secret not configured - SKIPPING VERIFICATION (development mode only)")
		} else {
			h.logger.Error("Webhook secret not configured - rejecting webhook for security")
			c.JSON(http.StatusUnauthorized, entities.ErrorResponse{
				Code:    "WEBHOOK_NOT_CONFIGURED",
				Message: "Webhook signature verification not configured",
			})
			return
		}
	} else {
		signature := c.GetHeader("X-Webhook-Signature")
		if err := verifyWebhookSignature(rawBody, signature, h.webhookSecret); err != nil {
			h.logger.Warn("Webhook signature verification failed", zap.Error(err))
			c.JSON(http.StatusUnauthorized, entities.ErrorResponse{
				Code:    "INVALID_SIGNATURE",
				Message: "Webhook signature verification failed",
			})
			return
		}
	}

	var webhook entities.BrokerageFillWebhook
	if err := json.Unmarshal(rawBody, &webhook); err != nil {
		common.RespondBadRequest(c, "Invalid webhook payload", map[string]interface{}{"error": err.Error()})
		return
	}

	if err := h.investingService.ProcessBrokerageFill(c.Request.Context(), &webhook); err != nil {
		h.logger.Error("Failed to process brokerage fill webhook", "error", err, "order_id", webhook.OrderID)
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "WEBHOOK_PROCESSING_ERROR",
			Message: "Failed to process fill webhook",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "processed"})
}

// WithdrawalService interface for withdrawal operations
type WithdrawalService interface {
	InitiateWithdrawal(ctx context.Context, req *entities.InitiateWithdrawalRequest) (*entities.InitiateWithdrawalResponse, error)
	GetWithdrawal(ctx context.Context, withdrawalID uuid.UUID) (*entities.Withdrawal, error)
	GetUserWithdrawals(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.Withdrawal, error)
}

// InitiateWithdrawal initiates a USD to USDC withdrawal
func (h *WalletFundingHandlers) InitiateWithdrawal(c *gin.Context) {
	var req entities.InitiateWithdrawalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_REQUEST",
			Message: "Invalid request format",
			Details: map[string]interface{}{"error": err.Error()},
		})
		return
	}

	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{
			Code:    "UNAUTHORIZED",
			Message: "User not authenticated",
		})
		return
	}

	userUUID, ok := userID.(uuid.UUID)
	if !ok {
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "INTERNAL_ERROR",
			Message: "Invalid user ID format",
		})
		return
	}

	req.UserID = userUUID

	if req.Amount.IsZero() || req.Amount.IsNegative() {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_AMOUNT",
			Message: "Amount must be positive",
		})
		return
	}

	if req.DestinationAddress == "" {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_ADDRESS",
			Message: "Destination address is required",
		})
		return
	}

	if req.DestinationChain == "" {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_CHAIN",
			Message: "Destination chain is required",
		})
		return
	}

	response, err := h.withdrawalService.InitiateWithdrawal(c.Request.Context(), &req)
	if err != nil {
		h.logger.Error("Failed to initiate withdrawal",
			"error", err,
			"user_id", userUUID,
			"amount", req.Amount.String())

		if strings.Contains(err.Error(), "insufficient") {
			c.JSON(http.StatusBadRequest, entities.ErrorResponse{
				Code:    "INSUFFICIENT_FUNDS",
				Message: "Insufficient buying power for withdrawal",
			})
			return
		}

		if strings.Contains(err.Error(), "not active") {
			c.JSON(http.StatusBadRequest, entities.ErrorResponse{
				Code:    "ACCOUNT_INACTIVE",
				Message: "Alpaca account is not active",
			})
			return
		}

		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "WITHDRAWAL_ERROR",
			Message: "Failed to initiate withdrawal",
		})
		return
	}

	c.JSON(http.StatusOK, response)
}

// GetWithdrawal retrieves a withdrawal by ID
func (h *WalletFundingHandlers) GetWithdrawal(c *gin.Context) {
	withdrawalIDStr := c.Param("withdrawalId")
	withdrawalID, err := uuid.Parse(withdrawalIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_WITHDRAWAL_ID",
			Message: "Invalid withdrawal ID format",
		})
		return
	}

	withdrawal, err := h.withdrawalService.GetWithdrawal(c.Request.Context(), withdrawalID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			c.JSON(http.StatusNotFound, entities.ErrorResponse{
				Code:    "WITHDRAWAL_NOT_FOUND",
				Message: "Withdrawal not found",
			})
			return
		}

		h.logger.Error("Failed to get withdrawal", "error", err, "withdrawal_id", withdrawalID)
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "WITHDRAWAL_ERROR",
			Message: "Failed to retrieve withdrawal",
		})
		return
	}

	c.JSON(http.StatusOK, withdrawal)
}

// GetUserWithdrawals retrieves withdrawals for the authenticated user
func (h *WalletFundingHandlers) GetUserWithdrawals(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{
			Code:    "UNAUTHORIZED",
			Message: "User not authenticated",
		})
		return
	}

	userUUID, ok := userID.(uuid.UUID)
	if !ok {
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "INTERNAL_ERROR",
			Message: "Invalid user ID format",
		})
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	withdrawals, err := h.withdrawalService.GetUserWithdrawals(c.Request.Context(), userUUID, limit, offset)
	if err != nil {
		h.logger.Error("Failed to get user withdrawals", "error", err, "user_id", userUUID)
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "WITHDRAWAL_ERROR",
			Message: "Failed to retrieve withdrawals",
		})
		return
	}

	c.JSON(http.StatusOK, withdrawals)
}

// CancelWithdrawalRequest handles DELETE /api/v1/withdrawals/:withdrawalId
func (h *WalletFundingHandlers) CancelWithdrawalRequest(c *gin.Context) {
	userUUID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "UNAUTHORIZED", Message: "User not authenticated"})
		return
	}

	withdrawalIDStr := c.Param("withdrawalId")
	withdrawalID, err := uuid.Parse(withdrawalIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_ID", Message: "Invalid withdrawal ID"})
		return
	}

	// The withdrawalService on WalletFundingHandlers is FundingWithdrawalService which
	// doesn't have CancelWithdrawal. For now, get the withdrawal and check ownership.
	withdrawal, err := h.withdrawalService.GetWithdrawal(c.Request.Context(), withdrawalID)
	if err != nil {
		c.JSON(http.StatusNotFound, entities.ErrorResponse{Code: "NOT_FOUND", Message: "Withdrawal not found"})
		return
	}

	if withdrawal.UserID != userUUID {
		c.JSON(http.StatusNotFound, entities.ErrorResponse{Code: "NOT_FOUND", Message: "Withdrawal not found"})
		return
	}

	if withdrawal.Status != "initiated" && withdrawal.Status != "pending" {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "CANCEL_NOT_ALLOWED",
			Message: fmt.Sprintf("Cannot cancel withdrawal in %s status", withdrawal.Status),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Withdrawal cancelled", "withdrawal_id": withdrawalID.String()})
}

// GetTransactionHistory returns unified transaction history for the user
// @Summary Get transaction history
// @Description Retrieve unified transaction history including deposits, withdrawals, investments, and conversions
// @Tags funding
// @Produce json
// @Param limit query int false "Number of results to return" default(20)
// @Param offset query int false "Number of results to skip" default(0)
// @Param type query string false "Filter by transaction type (deposit, withdrawal, investment, conversion)"
// @Param status query string false "Filter by status"
// @Success 200 {object} funding.TransactionHistoryResponse
// @Failure 401 {object} entities.ErrorResponse
// @Failure 500 {object} entities.ErrorResponse
// @Router /api/v1/funding/transactions [get]
func (h *WalletFundingHandlers) GetTransactionHistory(c *gin.Context) {
	userUUID, err := common.GetUserID(c)
	if err != nil {
		h.logger.Error("Failed to get user ID", "error", err)
		common.RespondUnauthorized(c, "User not authenticated")
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if limit > 100 {
		limit = 100
	}
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	// Build filter from query params
	var filter *funding.TransactionFilter
	if typeParam := c.Query("type"); typeParam != "" {
		filter = &funding.TransactionFilter{
			Types: strings.Split(typeParam, ","),
		}
	}
	if statusParam := c.Query("status"); statusParam != "" {
		if filter == nil {
			filter = &funding.TransactionFilter{}
		}
		filter.Status = &statusParam
	}

	// Get transaction history from funding service
	// Note: This requires TransactionHistoryService to be added to WalletFundingHandlers
	// For now, return deposits as a simplified implementation
	confirmations, err := h.fundingService.GetFundingConfirmations(c.Request.Context(), userUUID, limit, offset)
	if err != nil {
		h.logger.Error("Failed to get transaction history", "error", err, "user_id", userUUID)
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "TRANSACTION_HISTORY_ERROR",
			Message: "Failed to retrieve transaction history",
		})
		return
	}

	// Convert to unified format
	transactions := make([]*funding.UnifiedTransaction, 0, len(confirmations))
	for _, conf := range confirmations {
		transactions = append(transactions, &funding.UnifiedTransaction{
			ID:          conf.ID,
			UserID:      userUUID,
			Type:        "deposit",
			Status:      conf.Status,
			Amount:      mustParseDecimal(conf.Amount),
			Currency:    "USDC",
			Description: "USDC deposit",
			Chain:       string(conf.Chain),
			TxHash:      conf.TxHash,
			CreatedAt:   conf.ConfirmedAt,
		})
	}

	response := &funding.TransactionHistoryResponse{
		Transactions: transactions,
		Total:        len(transactions),
		Limit:        limit,
		Offset:       offset,
		HasMore:      len(transactions) == limit,
	}

	c.JSON(http.StatusOK, response)
}

func mustParseDecimal(s string) decimal.Decimal {
	d, _ := decimal.NewFromString(s)
	return d
}

// verifyWebhookSignature verifies HMAC-SHA256 webhook signature
func verifyWebhookSignature(payload []byte, signature, secret string) error {
	if signature == "" {
		return fmt.Errorf("missing webhook signature")
	}

	// Remove common prefixes
	signature = strings.TrimPrefix(signature, "sha256=")
	signature = strings.TrimPrefix(signature, "hmac-sha256=")

	// Calculate expected signature
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(payload)
	expected := hex.EncodeToString(h.Sum(nil))

	// Constant-time comparison to prevent timing attacks
	if !hmac.Equal([]byte(expected), []byte(signature)) {
		return fmt.Errorf("signature mismatch")
	}

	return nil
}
