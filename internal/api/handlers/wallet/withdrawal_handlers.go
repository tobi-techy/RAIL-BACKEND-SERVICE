package wallet

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/api/middleware"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// WithdrawalServiceInterface defines the interface for withdrawal operations
type WithdrawalServiceInterface interface {
	InitiateCryptoWithdrawal(ctx context.Context, req *entities.InitiateCryptoWithdrawalRequest) (*entities.InitiateWithdrawalResponse, error)
	InitiateFiatWithdrawal(ctx context.Context, req *entities.InitiateFiatWithdrawalRequest) (*entities.InitiateWithdrawalResponse, error)
	GetWithdrawal(ctx context.Context, userID, withdrawalID uuid.UUID) (*entities.Withdrawal, error)
	GetUserWithdrawals(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.Withdrawal, error)
	CancelWithdrawal(ctx context.Context, userID, withdrawalID uuid.UUID) error
	GetWithdrawalFee(ctx context.Context, withdrawalType entities.WithdrawalType, amount decimal.Decimal, currency entities.WithdrawalCurrency) (*entities.WithdrawalFee, error)
}

// WalletProvider interface for getting user's Circle wallet
type WalletProvider interface {
	GetUserWalletByChain(ctx context.Context, userID uuid.UUID, chain string) (*entities.ManagedWallet, error)
}

// WithdrawalHandlers handles withdrawal-related operations
type WithdrawalHandlers struct {
	withdrawalService WithdrawalServiceInterface
	walletProvider    WalletProvider
	validator         *validator.Validate
	logger            *logger.Logger
}

// NewWithdrawalHandlers creates a new WithdrawalHandlers instance
func NewWithdrawalHandlers(withdrawalService WithdrawalServiceInterface, walletProvider WalletProvider, logger *logger.Logger) *WithdrawalHandlers {
	return &WithdrawalHandlers{
		withdrawalService: withdrawalService,
		walletProvider:    walletProvider,
		validator:         validator.New(),
		logger:            logger,
	}
}

// CryptoWithdrawalRequest represents the HTTP request for crypto withdrawal
type CryptoWithdrawalRequest struct {
	Amount             string `json:"amount" binding:"required"`
	DestinationAddress string `json:"destination_address" binding:"required"`
	DestinationChain   string `json:"destination_chain"` // optional, defaults to SOL-DEVNET
}

// FiatWithdrawalRequest represents the HTTP request for fiat withdrawal
// Only requires routing number - bank account created during withdrawal process
type FiatWithdrawalRequest struct {
	Amount            string `json:"amount" binding:"required"`
	Currency          string `json:"currency" binding:"required,oneof=USD EUR"`
	AccountHolderName string `json:"account_holder_name" binding:"required,min=2,max=255"`
	AccountNumber     string `json:"account_number,omitempty"`
	RoutingNumber     string `json:"routing_number,omitempty"`
	IBAN              string `json:"iban,omitempty"`
	BIC               string `json:"bic,omitempty"`
}

// WithdrawalFeeRequest represents the HTTP request for fee calculation
type WithdrawalFeeRequest struct {
	WithdrawalType string `form:"type" binding:"required,oneof=crypto fiat"`
	Amount         string `form:"amount" binding:"required"`
	Currency       string `form:"currency" binding:"required,oneof=USDC USD EUR"`
}

// InitiateCryptoWithdrawal handles POST /api/v1/withdrawals/crypto
func (h *WithdrawalHandlers) InitiateCryptoWithdrawal(c *gin.Context) {
	var req CryptoWithdrawalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, "Invalid request format: "+err.Error())
		return
	}

	userID, ok := h.extractUserID(c)
	if !ok {
		return
	}

	amount, err := parsePositiveDecimal(req.Amount)
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidAmount, err.Error())
		return
	}
	if !h.validateWithdrawalAmountPolicy(c, userID, amount) {
		return
	}

	idempotencyKey, err := getIdempotencyKey(c)
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, err.Error())
		return
	}

	// Determine destination chain (default to SOL-DEVNET for testnet)
	destChain := req.DestinationChain
	if destChain == "" {
		destChain = string(entities.WalletChainSOLDevnet)
	}

	// Validate destination address format for the target chain
	if err := validateCryptoAddress(req.DestinationAddress, destChain); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, err.Error())
		return
	}

	// The source is always the user's spending wallet (SOL-DEVNET for testnet).
	// Cross-chain routing (CCTP) is handled by the withdrawal service.
	wallet, err := h.walletProvider.GetUserWalletByChain(c.Request.Context(), userID, string(entities.WalletChainSOLDevnet))
	if err != nil {
		h.logger.Error("Failed to get user wallet", "error", err, "user_id", userID)
		common.SendBadRequest(c, "NO_WALLET", "No wallet found for user")
		return
	}
	if strings.TrimSpace(wallet.CircleWalletID) == "" {
		h.logger.Error("User wallet has no Circle wallet ID", "user_id", userID)
		common.SendInternalError(c, "PROVIDER_NOT_CONFIGURED", "Withdrawal provider is not available for this account")
		return
	}

	serviceReq := &entities.InitiateCryptoWithdrawalRequest{
		UserID:             userID,
		Amount:             amount,
		DestinationAddress: req.DestinationAddress,
		DestinationChain:   destChain,
		SourceChain:        string(wallet.Chain),
		SourceAccount:      entities.WithdrawalSourceSpendingBalance,
		CircleWalletID:     wallet.CircleWalletID,
		IdempotencyKey:     idempotencyKey,
	}

	response, err := h.withdrawalService.InitiateCryptoWithdrawal(c.Request.Context(), serviceReq)
	if err != nil {
		h.handleWithdrawalError(c, err, userID, req.Amount)
		return
	}

	c.JSON(http.StatusOK, response)
}

// InitiateFiatWithdrawal handles POST /api/v1/withdrawals/fiat
func (h *WithdrawalHandlers) InitiateFiatWithdrawal(c *gin.Context) {
	var req FiatWithdrawalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, "Invalid request format: "+err.Error())
		return
	}

	userID, ok := h.extractUserID(c)
	if !ok {
		return
	}

	if err := validateFiatDestination(req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, err.Error())
		return
	}

	amount, err := parsePositiveDecimal(req.Amount)
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidAmount, err.Error())
		return
	}
	if !h.validateWithdrawalAmountPolicy(c, userID, amount) {
		return
	}

	idempotencyKey, err := getIdempotencyKey(c)
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, err.Error())
		return
	}

	currency := entities.WithdrawalCurrencyUSD
	if req.Currency == "EUR" {
		currency = entities.WithdrawalCurrencyEUR
	}

	serviceReq := &entities.InitiateFiatWithdrawalRequest{
		UserID:            userID,
		Amount:            amount,
		Currency:          currency,
		AccountHolderName: strings.TrimSpace(req.AccountHolderName),
		AccountNumber:     strings.ReplaceAll(strings.TrimSpace(req.AccountNumber), " ", ""),
		RoutingNumber:     strings.ReplaceAll(strings.TrimSpace(req.RoutingNumber), " ", ""),
		IBAN:              normalizeIBAN(req.IBAN),
		BIC:               strings.ToUpper(strings.TrimSpace(req.BIC)),
		SourceAccount:     entities.WithdrawalSourceSpendingBalance, // Default to spending
		IdempotencyKey:    idempotencyKey,
	}

	response, err := h.withdrawalService.InitiateFiatWithdrawal(c.Request.Context(), serviceReq)
	if err != nil {
		h.handleWithdrawalError(c, err, userID, req.Amount)
		return
	}

	c.JSON(http.StatusOK, response)
}

// GetWithdrawalFees handles GET /api/v1/withdrawals/fees
func (h *WithdrawalHandlers) GetWithdrawalFees(c *gin.Context) {
	var req WithdrawalFeeRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, "Invalid request: "+err.Error())
		return
	}

	withdrawalType := entities.WithdrawalTypeCrypto
	if req.WithdrawalType == "fiat" {
		withdrawalType = entities.WithdrawalTypeFiat
	}

	amount, err := parsePositiveDecimal(req.Amount)
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidAmount, err.Error())
		return
	}

	currency := entities.WithdrawalCurrencyUSDC
	switch req.Currency {
	case "USD":
		currency = entities.WithdrawalCurrencyUSD
	case "EUR":
		currency = entities.WithdrawalCurrencyEUR
	}

	fee, err := h.withdrawalService.GetWithdrawalFee(
		c.Request.Context(),
		withdrawalType,
		amount,
		currency,
	)
	if err != nil {
		h.logger.Error("Failed to get withdrawal fee", "error", err)
		common.SendInternalError(c, "FEE_CALCULATION_ERROR", "Failed to calculate fee")
		return
	}

	c.JSON(http.StatusOK, fee)
}

// GetWithdrawal handles GET /api/v1/withdrawals/:withdrawalId
func (h *WithdrawalHandlers) GetWithdrawal(c *gin.Context) {
	userID, ok := h.extractUserID(c)
	if !ok {
		return
	}

	withdrawalID, err := uuid.Parse(c.Param("withdrawalId"))
	if err != nil {
		common.SendBadRequest(c, "INVALID_WITHDRAWAL_ID", "Invalid withdrawal ID format")
		return
	}

	withdrawal, err := h.withdrawalService.GetWithdrawal(c.Request.Context(), userID, withdrawalID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			common.SendNotFound(c, common.ErrCodeWithdrawalNotFound, "Withdrawal not found")
			return
		}
		if strings.Contains(err.Error(), "does not belong") {
			common.SendNotFound(c, common.ErrCodeWithdrawalNotFound, "Withdrawal not found")
			return
		}
		h.logger.Error("Failed to get withdrawal", "error", err, "withdrawal_id", withdrawalID)
		common.SendInternalError(c, "WITHDRAWAL_ERROR", "Failed to retrieve withdrawal")
		return
	}

	c.JSON(http.StatusOK, withdrawal)
}

// GetUserWithdrawals handles GET /api/v1/withdrawals
func (h *WithdrawalHandlers) GetUserWithdrawals(c *gin.Context) {
	userID, ok := h.extractUserID(c)
	if !ok {
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	withdrawals, err := h.withdrawalService.GetUserWithdrawals(c.Request.Context(), userID, limit, offset)
	if err != nil {
		h.logger.Error("Failed to get user withdrawals", "error", err, "user_id", userID)
		common.SendInternalError(c, "WITHDRAWAL_ERROR", "Failed to retrieve withdrawals")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"withdrawals": withdrawals,
		"count":       len(withdrawals),
	})
}

// CancelWithdrawal handles DELETE /api/v1/withdrawals/:withdrawalId
func (h *WithdrawalHandlers) CancelWithdrawal(c *gin.Context) {
	userID, ok := h.extractUserID(c)
	if !ok {
		return
	}

	withdrawalID, err := uuid.Parse(c.Param("withdrawalId"))
	if err != nil {
		common.SendBadRequest(c, "INVALID_WITHDRAWAL_ID", "Invalid withdrawal ID format")
		return
	}

	if err := h.withdrawalService.CancelWithdrawal(c.Request.Context(), userID, withdrawalID); err != nil {
		errMsg := err.Error()
		switch {
		case strings.Contains(errMsg, "not found"):
			common.SendNotFound(c, common.ErrCodeWithdrawalNotFound, "Withdrawal not found")
		case strings.Contains(errMsg, "does not belong"):
			common.SendNotFound(c, common.ErrCodeWithdrawalNotFound, "Withdrawal not found")
		case strings.Contains(errMsg, "cannot cancel"):
			common.SendBadRequest(c, "CANCEL_NOT_ALLOWED", errMsg)
		default:
			h.logger.Error("Failed to cancel withdrawal", "error", err, "withdrawal_id", withdrawalID)
			common.SendInternalError(c, "WITHDRAWAL_ERROR", "Failed to cancel withdrawal")
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Withdrawal cancelled"})
}

func (h *WithdrawalHandlers) extractUserID(c *gin.Context) (uuid.UUID, bool) {
	userID, exists := c.Get("user_id")
	if !exists {
		common.SendUnauthorized(c, "User not authenticated")
		return uuid.Nil, false
	}

	userUUID, ok := userID.(uuid.UUID)
	if !ok {
		common.SendInternalError(c, common.ErrCodeInternalError, "Invalid user ID format")
		return uuid.Nil, false
	}

	return userUUID, true
}

func (h *WithdrawalHandlers) handleWithdrawalError(c *gin.Context, err error, userID uuid.UUID, amount string) {
	h.logger.Error("Failed to initiate withdrawal",
		"error", err,
		"error_type", fmt.Sprintf("%T", err),
		"user_id", userID,
		"amount", amount,
		"request_id", c.GetString("request_id"))

	errMsg := err.Error()
	errLower := strings.ToLower(errMsg)

	switch {
	case strings.Contains(errMsg, "insufficient"):
		common.SendBadRequest(c, common.ErrCodeInsufficientFunds, "Insufficient balance for withdrawal")
	case strings.Contains(errMsg, "minimum"):
		common.SendBadRequest(c, common.ErrCodeInvalidAmount, "Withdrawal amount below minimum")
	case strings.Contains(errMsg, "PAYMASTER_SOL_ATA_CREATION_NOT_ALLOWED"):
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, "Destination Solana wallet must create USDC ATA before withdrawal")
	case strings.Contains(errMsg, "invalid request:"):
		// Strip the prefix only if it's at the start (not wrapped)
		msg := errMsg
		if idx := strings.Index(errMsg, "invalid request: "); idx >= 0 {
			msg = errMsg[idx+len("invalid request: "):]
		}
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, msg)
	case strings.Contains(errLower, "circle validation error 400"),
		strings.Contains(errLower, "api parameter invalid"):
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, "Invalid withdrawal parameters")
	case strings.Contains(errMsg, "circle client not configured"),
		strings.Contains(errMsg, "circle wallet ID not provided"),
		strings.Contains(errMsg, "circle wallet ID is required"):
		common.SendInternalError(c, "PROVIDER_NOT_CONFIGURED", "Withdrawal provider is not available")
	case strings.Contains(errLower, "entity secret"):
		common.SendInternalError(c, "PROVIDER_NOT_CONFIGURED", "Withdrawal provider configuration is invalid")
	case strings.Contains(errMsg, "limit exceeded"):
		common.SendBadRequest(c, "LIMIT_EXCEEDED", errMsg)
	case strings.Contains(errMsg, "bank account"):
		common.SendBadRequest(c, "BANK_ACCOUNT_ERROR", errMsg)
	case strings.Contains(errMsg, "not verified"):
		common.SendBadRequest(c, "BANK_ACCOUNT_NOT_VERIFIED", "Bank account must be verified before withdrawal")
	case strings.Contains(errMsg, "currency"):
		common.SendBadRequest(c, "CURRENCY_MISMATCH", errMsg)
	case strings.Contains(errMsg, "cctp burn failed"),
		strings.Contains(errMsg, "circle transfer failed"),
		strings.Contains(errMsg, "failed to execute transfer"):
		common.SendInternalError(c, "TRANSFER_FAILED", "Transfer execution failed. Please try again.")
	case strings.Contains(errMsg, "failed to post ledger"),
		strings.Contains(errMsg, "failed to create withdrawal"):
		common.SendInternalError(c, "WITHDRAWAL_ERROR", "Failed to record withdrawal. Please try again.")
	default:
		common.SendInternalError(c, "WITHDRAWAL_ERROR", "Failed to initiate withdrawal")
	}
}

func (h *WithdrawalHandlers) validateWithdrawalAmountPolicy(c *gin.Context, userID uuid.UUID, amount decimal.Decimal) bool {
	cfgValue, hasCfg := c.Get("withdrawal_security_config")
	storeValue, hasStore := c.Get("withdrawal_security_store")
	if !hasCfg || !hasStore {
		return true
	}

	cfg, ok := cfgValue.(middleware.WithdrawalSecurityConfig)
	if !ok {
		h.logger.Error("Invalid withdrawal security config in request context")
		common.SendInternalError(c, common.ErrCodeInternalError, "Failed to apply withdrawal security policy")
		return false
	}

	store, ok := storeValue.(middleware.WithdrawalSecurityStore)
	if !ok {
		h.logger.Error("Invalid withdrawal security store in request context")
		common.SendInternalError(c, common.ErrCodeInternalError, "Failed to apply withdrawal security policy")
		return false
	}

	if err := middleware.ValidateWithdrawalAmount(c.Request.Context(), store, cfg, userID, amount); err != nil {
		common.SendBadRequest(c, "LIMIT_EXCEEDED", err.Error())
		return false
	}

	return true
}

func parsePositiveDecimal(raw string) (decimal.Decimal, error) {
	normalized := strings.TrimSpace(raw)
	amount, err := decimal.NewFromString(normalized)
	if err != nil {
		return decimal.Zero, fmt.Errorf("amount must be a valid decimal string")
	}
	if !amount.GreaterThan(decimal.Zero) {
		return decimal.Zero, fmt.Errorf("amount must be greater than zero")
	}
	return amount, nil
}

func getIdempotencyKey(c *gin.Context) (string, error) {
	key := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if key == "" {
		key = strings.TrimSpace(c.GetHeader("X-Idempotency-Key"))
	}
	if len(key) > 255 {
		return "", fmt.Errorf("idempotency key must be at most 255 characters")
	}
	return key, nil
}

func validateFiatDestination(req FiatWithdrawalRequest) error {
	currency := strings.TrimSpace(req.Currency)
	switch currency {
	case "USD":
		routing := strings.ReplaceAll(strings.TrimSpace(req.RoutingNumber), " ", "")
		account := strings.ReplaceAll(strings.TrimSpace(req.AccountNumber), " ", "")
		if len(routing) != 9 || !isDigits(routing) {
			return fmt.Errorf("routing_number must be exactly 9 digits for USD withdrawals")
		}
		if len(account) < 4 || len(account) > 17 || !isDigits(account) {
			return fmt.Errorf("account_number must be 4-17 digits for USD withdrawals")
		}
	case "EUR":
		iban := normalizeIBAN(req.IBAN)
		if len(iban) < 15 || len(iban) > 34 {
			return fmt.Errorf("iban must be between 15 and 34 characters for EUR withdrawals")
		}
		if !isAlphaNumeric(iban) {
			return fmt.Errorf("iban must contain only letters and digits")
		}
		bic := strings.ToUpper(strings.TrimSpace(req.BIC))
		if bic != "" {
			if (len(bic) != 8 && len(bic) != 11) || !isAlphaNumeric(bic) {
				return fmt.Errorf("bic must be 8 or 11 alphanumeric characters")
			}
		}
	default:
		return fmt.Errorf("unsupported fiat currency: %s", currency)
	}
	return nil
}

func isDigits(v string) bool {
	if v == "" {
		return false
	}
	for _, ch := range v {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func isAlphaNumeric(v string) bool {
	if v == "" {
		return false
	}
	for _, ch := range v {
		if (ch >= '0' && ch <= '9') || (ch >= 'A' && ch <= 'Z') {
			continue
		}
		return false
	}
	return true
}

func validateCryptoAddress(address, chain string) error {
	if address == "" {
		return fmt.Errorf("destination address is required")
	}

	chainUpper := strings.ToUpper(chain)
	addr := strings.TrimSpace(address)

	switch {
	case strings.Contains(chainUpper, "SOL"):
		// Solana addresses are base58 encoded, 32-44 characters
		if len(addr) < 32 || len(addr) > 44 {
			return fmt.Errorf("invalid Solana address: must be 32-44 characters")
		}
		// Basic base58 check (alphanumeric except 0, O, I, l)
		validChars := "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
		for _, c := range addr {
			if !strings.ContainsRune(validChars, c) {
				return fmt.Errorf("invalid Solana address: contains invalid characters")
			}
		}
	case strings.Contains(chainUpper, "ETH"), strings.Contains(chainUpper, "MATIC"),
		strings.Contains(chainUpper, "AVAX"), strings.Contains(chainUpper, "BASE"),
		strings.Contains(chainUpper, "ARB"), strings.Contains(chainUpper, "OP"):
		// EVM addresses are 0x-prefixed hex, 42 characters
		if len(addr) != 42 {
			return fmt.Errorf("invalid EVM address: must be 42 characters (0x + 40 hex)")
		}
		if !strings.HasPrefix(addr, "0x") {
			return fmt.Errorf("invalid EVM address: must start with 0x")
		}
		hexPart := addr[2:]
		for _, c := range hexPart {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return fmt.Errorf("invalid EVM address: must be valid hex after 0x")
			}
		}
	default:
		// For unknown chains, just check basic length
		if len(addr) < 20 || len(addr) > 64 {
			return fmt.Errorf("invalid address: length must be between 20-64 characters")
		}
	}

	return nil
}

func normalizeIBAN(raw string) string {
	trimmed := strings.TrimSpace(raw)
	withoutSpaces := strings.ReplaceAll(trimmed, " ", "")
	return strings.ToUpper(withoutSpaces)
}

// AdminWithdrawalHandlers handles admin withdrawal operations
type AdminWithdrawalHandlers struct {
	withdrawalService WithdrawalServiceInterface
	logger            *zap.Logger
}

// NewAdminWithdrawalHandlers creates a new AdminWithdrawalHandlers instance
func NewAdminWithdrawalHandlers(withdrawalService WithdrawalServiceInterface, logger *zap.Logger) *AdminWithdrawalHandlers {
	return &AdminWithdrawalHandlers{
		withdrawalService: withdrawalService,
		logger:            logger,
	}
}

// AdminGetWithdrawals handles GET /api/v1/admin/withdrawals
func (h *AdminWithdrawalHandlers) AdminGetWithdrawals(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	userIDStr := c.Query("user_id")
	if userIDStr == "" {
		c.JSON(http.StatusOK, gin.H{
			"items": []interface{}{},
			"count": 0,
			"note":  "Please provide user_id filter to view withdrawals",
		})
		return
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidUserID, "Invalid user ID format")
		return
	}

	withdrawals, err := h.withdrawalService.GetUserWithdrawals(c.Request.Context(), userID, limit, offset)
	if err != nil {
		h.logger.Error("Failed to get withdrawals", zap.Error(err))
		common.SendInternalError(c, "WITHDRAWAL_ERROR", "Failed to retrieve withdrawals")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"items": withdrawals,
		"count": len(withdrawals),
	})
}
