package handlers

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/stack-service/stack_service/internal/domain/entities"
	"github.com/stack-service/stack_service/internal/domain/services/wallet"
	"github.com/stack-service/stack_service/internal/infrastructure/config"
	"github.com/stack-service/stack_service/pkg/logger"
)

// WalletHandlers contains the wallet-related HTTP handlers
type WalletHandlers struct {
	walletService *wallet.Service
	validator     *validator.Validate
	logger        *zap.Logger
}

// NewWalletHandlers creates a new instance of wallet handlers
func NewWalletHandlers(walletService *wallet.Service, logger *zap.Logger) *WalletHandlers {
	return &WalletHandlers{
		walletService: walletService,
		validator:     validator.New(),
		logger:        logger,
	}
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
func (h *WalletHandlers) GetWalletAddresses(c *gin.Context) {
	ctx := c.Request.Context()

	// Get user ID from authenticated context
	userID, err := getUserID(c)
	if err != nil {
		h.logger.Warn("Invalid or missing user ID", zap.Error(err))
		respondBadRequest(c, "Invalid or missing user ID", map[string]interface{}{"error": err.Error()})
		return
	}

	h.logger.Debug("Getting wallet addresses",
		zap.String("user_id", userID.String()),
		zap.String("request_id", getRequestID(c)))

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

		if isUserNotFoundError(err) {
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
func (h *WalletHandlers) GetWalletStatus(c *gin.Context) {
	ctx := c.Request.Context()

	// Get user ID from authenticated context
	userID, err := getUserID(c)
	if err != nil {
		h.logger.Warn("Invalid or missing user ID", zap.Error(err))
		respondBadRequest(c, "Invalid or missing user ID", map[string]interface{}{"error": err.Error()})
		return
	}

	h.logger.Debug("Getting wallet status",
		zap.String("user_id", userID.String()),
		zap.String("request_id", getRequestID(c)))

	// Get wallet status
	response, err := h.walletService.GetWalletStatus(ctx, userID)
	if err != nil {
		h.logger.Error("Failed to get wallet status",
			zap.Error(err),
			zap.String("user_id", userID.String()))

		if isUserNotFoundError(err) {
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
func (h *WalletHandlers) CreateWalletsForUser(c *gin.Context) {
	ctx := c.Request.Context()

	h.logger.Info("Manual wallet creation requested",
		zap.String("request_id", getRequestID(c)),
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
func (h *WalletHandlers) RetryWalletProvisioning(c *gin.Context) {
	ctx := c.Request.Context()

	h.logger.Info("Wallet provisioning retry requested",
		zap.String("request_id", getRequestID(c)),
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
func (h *WalletHandlers) HealthCheck(c *gin.Context) {
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
func (h *WalletHandlers) InitiateWalletCreation(c *gin.Context) {
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
		chains = []string{string(entities.ChainSOLDevnet)}
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
func (h *WalletHandlers) ProvisionWallets(c *gin.Context) {
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
func (h *WalletHandlers) GetWalletByChain(c *gin.Context) {
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
