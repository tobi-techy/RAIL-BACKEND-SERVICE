package wallet

import (
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/wallet"
	"github.com/rail-service/rail_service/pkg/logger"
	"go.uber.org/zap"
)

// WalletHandlers handles wallet-related operations
type WalletHandlers struct {
	walletService *wallet.Service
	validator     *validator.Validate
	logger        *logger.Logger
}

// NewWalletHandlers creates a new WalletHandlers instance
func NewWalletHandlers(walletService *wallet.Service, logger *logger.Logger) *WalletHandlers {
	return &WalletHandlers{
		walletService: walletService,
		validator:     validator.New(),
		logger:        logger,
	}
}

// WalletCreationRequest represents a wallet creation request
type WalletCreationRequest struct {
	UserID string   `json:"user_id" validate:"required,uuid"`
	Chains []string `json:"chains" validate:"required,min=1"`
}

// GetWalletAddresses handles GET /wallet/addresses
func (h *WalletHandlers) GetWalletAddresses(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := common.GetUserID(c)
	if err != nil {
		h.logger.Warn("Invalid or missing user ID", zap.Error(err))
		common.RespondBadRequest(c, "Invalid or missing user ID", map[string]interface{}{"error": err.Error()})
		return
	}

	h.logger.Debug("Getting wallet addresses",
		zap.String("user_id", userID.String()),
		zap.String("request_id", common.GetRequestID(c)))

	chainFilter := h.parseChainFilter(c)
	if chainFilter != nil && !chainFilter.IsValid() {
		chainQuery := c.Query("chain")
		h.logger.Warn("Invalid chain parameter", zap.String("chain", chainQuery))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    common.ErrCodeInvalidChain,
			Message: "Invalid blockchain network",
			Details: map[string]interface{}{
				"chain":            chainQuery,
				"supported_chains": getSupportedChains(),
			},
		})
		return
	}

	response, err := h.walletService.GetWalletAddresses(ctx, userID, chainFilter)
	if err != nil {
		h.logger.Error("Failed to get wallet addresses",
			zap.Error(err),
			zap.String("user_id", userID.String()))

		if common.IsUserNotFoundError(err) {
			common.SendNotFound(c, common.ErrCodeUserNotFound, common.MsgUserNotFound)
			return
		}

		common.SendInternalError(c, "WALLET_RETRIEVAL_FAILED", "Failed to retrieve wallet addresses")
		return
	}

	h.logger.Debug("Retrieved wallet addresses successfully",
		zap.String("user_id", userID.String()),
		zap.Int("wallet_count", len(response.Wallets)))

	common.SendSuccess(c, response)
}

// GetWalletStatus handles GET /wallet/status
func (h *WalletHandlers) GetWalletStatus(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := common.GetUserID(c)
	if err != nil {
		h.logger.Warn("Invalid or missing user ID", zap.Error(err))
		common.RespondBadRequest(c, "Invalid or missing user ID", map[string]interface{}{"error": err.Error()})
		return
	}

	h.logger.Debug("Getting wallet status",
		zap.String("user_id", userID.String()),
		zap.String("request_id", common.GetRequestID(c)))

	response, err := h.walletService.GetWalletStatus(ctx, userID)
	if err != nil {
		h.logger.Error("Failed to get wallet status",
			zap.Error(err),
			zap.String("user_id", userID.String()))

		if common.IsUserNotFoundError(err) {
			common.SendNotFound(c, common.ErrCodeUserNotFound, common.MsgUserNotFound)
			return
		}

		common.SendInternalError(c, "WALLET_STATUS_FAILED", "Failed to retrieve wallet status")
		return
	}

	h.logger.Debug("Retrieved wallet status successfully",
		zap.String("user_id", userID.String()),
		zap.Int("total_wallets", response.TotalWallets),
		zap.Int("ready_wallets", response.ReadyWallets))

	common.SendSuccess(c, response)
}

// CreateWalletsForUser handles POST /wallet/create (Admin only)
func (h *WalletHandlers) CreateWalletsForUser(c *gin.Context) {
	ctx := c.Request.Context()

	h.logger.Info("Manual wallet creation requested",
		zap.String("request_id", common.GetRequestID(c)),
		zap.String("ip", c.ClientIP()))

	var req WalletCreationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("Invalid wallet creation request payload", zap.Error(err))
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, "Invalid wallet creation request payload")
		return
	}

	if err := h.validator.Struct(req); err != nil {
		h.logger.Warn("Wallet creation request validation failed", zap.Error(err))
		common.SendBadRequest(c, "VALIDATION_ERROR", "Wallet creation request validation failed", map[string]interface{}{"validation": err.Error()})
		return
	}

	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		h.logger.Warn("Invalid user ID format", zap.Error(err))
		common.SendBadRequest(c, common.ErrCodeInvalidUserID, "Invalid user ID format")
		return
	}

	chains, err := h.validateChains(req.Chains)
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidChain, err.Error())
		return
	}

	if err := h.walletService.CreateWalletsForUser(ctx, userID, chains); err != nil {
		h.logger.Error("Failed to create wallets for user",
			zap.Error(err),
			zap.String("user_id", userID.String()),
			zap.Strings("chains", req.Chains))

		common.SendInternalError(c, common.ErrCodeWalletCreationFailed, "Failed to create wallets for user")
		return
	}

	h.logger.Info("Wallet creation initiated successfully",
		zap.String("user_id", userID.String()),
		zap.Strings("chains", req.Chains))

	common.SendAccepted(c, gin.H{
		"message":    "Wallet creation initiated",
		"user_id":    userID.String(),
		"chains":     req.Chains,
		"next_steps": []string{"Check wallet status for progress", "Wallets will be available once provisioning completes"},
	})
}

// RetryWalletProvisioning handles POST /wallet/retry (Admin only)
func (h *WalletHandlers) RetryWalletProvisioning(c *gin.Context) {
	ctx := c.Request.Context()

	h.logger.Info("Wallet provisioning retry requested",
		zap.String("request_id", common.GetRequestID(c)),
		zap.String("ip", c.ClientIP()))

	limit := h.parseLimit(c, 10)

	if err := h.walletService.RetryFailedWalletProvisioning(ctx, limit); err != nil {
		h.logger.Error("Failed to retry wallet provisioning", zap.Error(err))
		common.SendInternalError(c, "RETRY_FAILED", "Failed to retry wallet provisioning")
		return
	}

	h.logger.Info("Wallet provisioning retry initiated", zap.Int("limit", limit))

	common.SendSuccess(c, gin.H{
		"message": "Wallet provisioning retry initiated",
		"limit":   limit,
	})
}

// HealthCheck handles GET /wallet/health (Admin only)
func (h *WalletHandlers) HealthCheck(c *gin.Context) {
	ctx := c.Request.Context()

	h.logger.Debug("Wallet service health check requested")

	if err := h.walletService.HealthCheck(ctx); err != nil {
		h.logger.Error("Wallet service health check failed", zap.Error(err))
		common.SendServiceUnavailable(c, "Wallet service health check failed")
		return
	}

	metrics := h.walletService.GetMetrics()

	h.logger.Debug("Wallet service health check passed")

	common.SendSuccess(c, gin.H{
		"status":  "healthy",
		"service": "wallet",
		"metrics": metrics,
	})
}

// InitiateWalletCreation handles POST /api/v1/wallets/initiate
func (h *WalletHandlers) InitiateWalletCreation(c *gin.Context) {
	ctx := c.Request.Context()

	var req entities.WalletInitiationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("Invalid wallet initiation request", zap.Error(err))
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, "Invalid request payload")
		return
	}

	userID, err := h.extractUserIDFromContext(c)
	if err != nil {
		return // Error already sent
	}

	chains, err := h.validateInitiationChains(req.Chains)
	if err != nil {
		return // Error already sent by validation method
	}

	h.logger.Info("Initiating developer-controlled wallet creation for user",
		zap.String("user_id", userID.String()),
		zap.Strings("chains", chainStrings(chains)))

	if err := h.walletService.CreateWalletsForUser(ctx, userID, chains); err != nil {
		h.logger.Error("Failed to initiate developer-controlled wallet creation",
			zap.Error(err),
			zap.String("user_id", userID.String()))
		common.SendInternalError(c, "WALLET_INITIATION_FAILED", "Failed to initiate developer-controlled wallet creation")
		return
	}

	job, err := h.walletService.GetProvisioningJobByUserID(ctx, userID)
	if err != nil {
		h.logger.Warn("Failed to get provisioning job status", zap.Error(err))
		common.SendAccepted(c, entities.WalletInitiationResponse{
			Message: "Developer-controlled wallet creation initiated",
			UserID:  userID.String(),
			Chains:  chainStrings(chains),
		})
		return
	}

	response := entities.WalletInitiationResponse{
		Message: "Developer-controlled wallet creation initiated successfully",
		UserID:  userID.String(),
		Chains:  chainStrings(chains),
		Job:     buildJobResponse(job),
	}

	h.logger.Info("Developer-controlled wallet creation initiated",
		zap.String("user_id", userID.String()),
		zap.String("job_id", job.ID.String()),
		zap.Strings("chains", chainStrings(chains)))

	common.SendAccepted(c, response)
}

// ProvisionWallets handles POST /api/v1/wallets/provision
func (h *WalletHandlers) ProvisionWallets(c *gin.Context) {
	ctx := c.Request.Context()

	var req entities.WalletProvisioningRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("Invalid wallet provisioning request", zap.Error(err))
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, "Invalid request payload")
		return
	}

	userID, err := h.extractUserIDFromContext(c)
	if err != nil {
		return // Error already sent
	}

	var chains []entities.WalletChain
	if len(req.Chains) > 0 {
		for _, chainStr := range req.Chains {
			chain := entities.WalletChain(chainStr)
			if !chain.IsValid() {
				common.SendBadRequest(c, common.ErrCodeInvalidChain, fmt.Sprintf("Invalid chain: %s", chainStr))
				return
			}
			chains = append(chains, chain)
		}
	}

	if err := h.walletService.CreateWalletsForUser(ctx, userID, chains); err != nil {
		h.logger.Error("Failed to create wallets for user",
			zap.Error(err),
			zap.String("user_id", userID.String()))
		common.SendInternalError(c, common.ErrCodeProvisioningFailed, "Failed to start wallet provisioning")
		return
	}

	job, err := h.walletService.GetProvisioningJobByUserID(ctx, userID)
	if err != nil {
		h.logger.Warn("Failed to get provisioning job status", zap.Error(err))
		common.SendAccepted(c, gin.H{
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

	common.SendAccepted(c, response)
}

// GetWalletByChain handles GET /api/v1/wallets/:chain/address
func (h *WalletHandlers) GetWalletByChain(c *gin.Context) {
	ctx := c.Request.Context()

	chainStr := c.Param("chain")
	chain := entities.WalletChain(chainStr)

	if !chain.IsValid() {
		common.SendBadRequest(c, common.ErrCodeInvalidChain, fmt.Sprintf("Invalid chain: %s", chainStr))
		return
	}

	userID, err := h.extractUserIDFromContext(c)
	if err != nil {
		return // Error already sent
	}

	w, err := h.walletService.GetWalletByUserAndChain(ctx, userID, chain)
	if err != nil {
		h.logger.Warn("Wallet not found for chain",
			zap.Error(err),
			zap.String("user_id", userID.String()),
			zap.String("chain", chainStr))
		common.SendNotFound(c, common.ErrCodeWalletNotFound, fmt.Sprintf("No wallet found for chain: %s", chainStr))
		return
	}

	response := entities.WalletAddressResponse{
		Chain:   chain,
		Address: w.Address,
		Status:  string(w.Status),
	}

	common.SendSuccess(c, response)
}

// Helper methods

func (h *WalletHandlers) parseChainFilter(c *gin.Context) *entities.WalletChain {
	chainQuery := c.Query("chain")
	if chainQuery == "" {
		return nil
	}
	chain := entities.WalletChain(chainQuery)
	return &chain
}

func (h *WalletHandlers) parseLimit(c *gin.Context, defaultLimit int) int {
	limit := defaultLimit
	if limitQuery := c.Query("limit"); limitQuery != "" {
		if parsedLimit, err := strconv.Atoi(limitQuery); err == nil && parsedLimit > 0 {
			limit = parsedLimit
		}
	}
	return limit
}

func (h *WalletHandlers) validateChains(chainStrs []string) ([]entities.WalletChain, error) {
	var chains []entities.WalletChain
	for _, chainStr := range chainStrs {
		chain := entities.WalletChain(chainStr)
		if !chain.IsValid() {
			return nil, fmt.Errorf("invalid chain: %s", chainStr)
		}
		chains = append(chains, chain)
	}
	return chains, nil
}

func (h *WalletHandlers) validateInitiationChains(chainStrs []string) ([]entities.WalletChain, error) {
	chains := chainStrs
	if len(chains) == 0 {
		chains = []string{string(entities.WalletChainSOLDevnet)}
	}

	var chainEntities []entities.WalletChain
	for _, chainStr := range chains {
		chain := entities.WalletChain(chainStr)
		if !chain.IsValid() {
			h.logger.Warn("Invalid chain in request", zap.String("chain", chainStr))
			return nil, fmt.Errorf("invalid chain: %s", chainStr)
		}

		if !chain.IsTestnet() {
			h.logger.Warn("Mainnet chain not supported for wallet creation", zap.String("chain", chainStr))
			return nil, fmt.Errorf("only testnet chains supported")
		}

		chainEntities = append(chainEntities, chain)
	}

	return chainEntities, nil
}

func (h *WalletHandlers) extractUserIDFromContext(c *gin.Context) (uuid.UUID, error) {
	userIDValue, exists := c.Get("user_id")
	if !exists {
		common.SendUnauthorized(c, "User ID not found in context")
		return uuid.Nil, fmt.Errorf("user ID not found")
	}

	switch v := userIDValue.(type) {
	case uuid.UUID:
		return v, nil
	case string:
		userID, err := uuid.Parse(v)
		if err != nil {
			h.logger.Error("Invalid user ID string in context", zap.Error(err))
			common.SendInternalError(c, common.ErrCodeInternalError, "Invalid user context")
			return uuid.Nil, err
		}
		return userID, nil
	default:
		h.logger.Error("Unexpected user ID type in context", zap.Any("type", v))
		common.SendInternalError(c, common.ErrCodeInternalError, "Invalid user context")
		return uuid.Nil, fmt.Errorf("invalid user ID type")
	}
}

// Helper functions

func getSupportedChains() []string {
	return []string{"ETH", "ETH-SEPOLIA", "SOL", "SOL-DEVNET", "APTOS", "APTOS-TESTNET"}
}

func chainStrings(chains []entities.WalletChain) []string {
	result := make([]string, len(chains))
	for i, c := range chains {
		result[i] = string(c)
	}
	return result
}

func buildJobResponse(job *entities.WalletProvisioningJob) *entities.WalletProvisioningJobResponse {
	return &entities.WalletProvisioningJobResponse{
		ID:           job.ID,
		Status:       string(job.Status),
		Progress:     "0%",
		AttemptCount: job.AttemptCount,
		MaxAttempts:  job.MaxAttempts,
		ErrorMessage: job.ErrorMessage,
		NextRetryAt:  job.NextRetryAt,
		CreatedAt:    job.CreatedAt,
	}
}
