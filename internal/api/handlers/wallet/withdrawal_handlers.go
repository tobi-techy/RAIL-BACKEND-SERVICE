package wallet

import (
	"context"
	stderrors "errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/api/middleware"
	"github.com/rail-service/rail_service/internal/domain/entities"
	domainerrors "github.com/rail-service/rail_service/internal/domain/errors"
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
	GetWithdrawalFee(ctx context.Context, withdrawalType entities.WithdrawalType, amount decimal.Decimal, currency entities.WithdrawalCurrency, sourceChain, destChain string) (*entities.WithdrawalFee, error)
	EmergencyWithdrawalPreview(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) (*entities.EmergencyWithdrawalPreviewResponse, error)
	EmergencyStashToSpending(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) (*entities.EmergencyWithdrawalResult, error)
	FundStash(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) (*entities.FundStashResult, error)
}

// WalletProvider interface for getting user's Bridge-managed wallet
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
	Currency           string `json:"currency,omitempty"` // USDC, USDT, EURC, PYUSD, USDG (defaults to USDC)
	DestinationAddress string `json:"destination_address" binding:"required"`
	DestinationChain   string `json:"destination_chain"`        // optional, defaults to SOL
	SourceAccount      string `json:"source_account,omitempty"` // spending_balance (default) or stash_balance
	Emergency          bool   `json:"emergency,omitempty"`      // bypass stash lock with penalty fee
	Category           string `json:"category,omitempty"`
	Narration          string `json:"narration,omitempty"`
}

// FiatWithdrawalRequest represents the HTTP request for fiat withdrawal
// Only requires routing number - bank account created during withdrawal process
type FiatWithdrawalRequest struct {
	Amount            string `json:"amount" binding:"required"`
	Currency          string `json:"currency" binding:"required,oneof=USD EUR GBP NGN"`
	AccountHolderName string `json:"account_holder_name" binding:"required,min=2,max=255"`
	AccountNumber     string `json:"account_number,omitempty"`
	RoutingNumber     string `json:"routing_number,omitempty"`
	IBAN              string `json:"iban,omitempty"`
	BIC               string `json:"bic,omitempty"`
	SourceAccount     string `json:"source_account,omitempty"` // spending_balance (default) or stash_balance
	Emergency         bool   `json:"emergency,omitempty"`      // bypass stash lock with penalty fee
	Category          string `json:"category,omitempty"`
	Narration         string `json:"narration,omitempty"`
}

// WithdrawalFeeRequest represents the HTTP request for fee calculation
type WithdrawalFeeRequest struct {
	WithdrawalType string `form:"type" binding:"required,oneof=crypto fiat"`
	Amount         string `form:"amount" binding:"required"`
	Currency       string `form:"currency" binding:"required,oneof=USDC USDT EURC PYUSD USDG USD EUR GBP NGN"`
	SourceChain    string `form:"source_chain"`
	DestChain      string `form:"dest_chain"`
}

const (
	maxWithdrawalCategoryLength  = 64
	maxWithdrawalNarrationLength = 280
)

// InitiateCryptoWithdrawal handles POST /api/v1/withdrawals/crypto
func (h *WithdrawalHandlers) InitiateCryptoWithdrawal(c *gin.Context) {
	var req CryptoWithdrawalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, "Invalid request format: "+err.Error())
		return
	}

	h.logger.Info("Crypto withdrawal request received",
		"amount", req.Amount,
		"destination", req.DestinationAddress,
		"chain", req.DestinationChain)

	userID, ok := h.extractUserID(c)
	if !ok {
		return
	}

	amount, err := parsePositiveDecimal(req.Amount)
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidAmount, err.Error())
		return
	}
	if amount.LessThan(decimal.NewFromInt(1)) {
		common.SendBadRequest(c, common.ErrCodeInvalidAmount, "Minimum withdrawal amount is $1.00")
		return
	}
	if !h.validateWithdrawalAmountPolicy(c, userID, amount) {
		return
	}

	category, err := normalizeCategory(req.Category)
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, err.Error())
		return
	}
	narration, err := normalizeNarration(req.Narration)
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, err.Error())
		return
	}

	idempotencyKey, err := getIdempotencyKey(c)
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, err.Error())
		return
	}

	// Determine destination chain (default to SOL)
	destChain := req.DestinationChain
	if destChain == "" {
		destChain = string(entities.WalletChainSolana)
	}

	// Validate destination address format for the target chain
	if err := validateCryptoAddress(req.DestinationAddress, destChain); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, err.Error())
		return
	}

	// The source is always the user's spending wallet.
	// Try destination chain wallet first, fall back to Solana.
	wallet, err := h.walletProvider.GetUserWalletByChain(c.Request.Context(), userID, destChain)
	if err != nil {
		wallet, err = h.walletProvider.GetUserWalletByChain(c.Request.Context(), userID, string(entities.WalletChainSolana))
	}
	if err != nil {
		h.logger.Error("Failed to get user wallet", "error", err, "user_id", userID)
		common.SendBadRequest(c, "NO_WALLET", "No wallet found for user")
		return
	}
	if wallet.CircleWalletID == "" && strings.TrimSpace(wallet.BridgeWalletID) == "" {
		h.logger.Error("User wallet has no provider wallet ID", "user_id", userID)
		common.SendInternalError(c, "PROVIDER_NOT_CONFIGURED", "Withdrawal provider is not available for this account")
		return
	}

	// Parse withdrawal currency (default to USDC)
	withdrawalCurrency := entities.WithdrawalCurrencyUSDC
	if req.Currency != "" {
		withdrawalCurrency = entities.WithdrawalCurrency(strings.ToUpper(strings.TrimSpace(req.Currency)))
		if !withdrawalCurrency.IsStablecoinCurrency() {
			common.SendBadRequest(c, common.ErrCodeInvalidRequest, "Unsupported crypto withdrawal currency. Supported: USDC, USDT, EURC, PYUSD, USDG")
			return
		}
	}

	serviceReq := &entities.InitiateCryptoWithdrawalRequest{
		UserID:              userID,
		Amount:              amount,
		Currency:            withdrawalCurrency,
		DestinationAddress:  req.DestinationAddress,
		DestinationChain:    destChain,
		SourceChain:         string(wallet.Chain),
		SourceAccount:       resolveSourceAccount(req.SourceAccount),
		BridgeWalletID:      wallet.BridgeWalletID,
		CircleWalletID:      wallet.CircleWalletID,
		SourceWalletAddress: wallet.Address,
		Category:            category,
		Narration:           narration,
		Emergency:           req.Emergency,
		IdempotencyKey:      idempotencyKey,
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
	if amount.LessThan(decimal.NewFromInt(1)) {
		common.SendBadRequest(c, common.ErrCodeInvalidAmount, "Minimum withdrawal amount is $1.00")
		return
	}
	if !h.validateWithdrawalAmountPolicy(c, userID, amount) {
		return
	}

	category, err := normalizeCategory(req.Category)
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, err.Error())
		return
	}
	narration, err := normalizeNarration(req.Narration)
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, err.Error())
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
	} else if req.Currency == "GBP" {
		currency = entities.WithdrawalCurrencyGBP
	} else if req.Currency == "NGN" {
		currency = entities.WithdrawalCurrencyNGN
	}

	wallet, err := h.walletProvider.GetUserWalletByChain(c.Request.Context(), userID, string(entities.WalletChainSolana))
	if err != nil {
		h.logger.Error("Failed to get user wallet for fiat withdrawal", "error", err, "user_id", userID)
		common.SendBadRequest(c, "NO_WALLET", "No wallet found for user")
		return
	}
	if strings.TrimSpace(wallet.BridgeWalletID) == "" {
		h.logger.Error("User wallet has no Bridge wallet ID", "user_id", userID)
		common.SendInternalError(c, "PROVIDER_NOT_CONFIGURED", "Withdrawal provider is not available for this account")
		return
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
		SourceAccount:     resolveSourceAccount(req.SourceAccount),
		BridgeWalletID:    wallet.BridgeWalletID,
		Category:          category,
		Narration:         narration,
		Emergency:         req.Emergency,
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
	case "GBP":
		currency = entities.WithdrawalCurrencyGBP
	case "NGN":
		currency = entities.WithdrawalCurrencyNGN
	}

	fee, err := h.withdrawalService.GetWithdrawalFee(
		c.Request.Context(),
		withdrawalType,
		amount,
		currency,
		req.SourceChain,
		req.DestChain,
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

	var domainErr *domainerrors.DomainError
	if stderrors.As(err, &domainErr) {
		code := domainerrors.GetErrorCode(domainErr)
		details := domainerrors.GetErrorDetails(domainErr)
		switch {
		case stderrors.Is(domainErr, domainerrors.ErrInvalidInput):
			common.NewError(http.StatusBadRequest, code).Message(domainErr.Error()).Details(details).Send(c)
			return
		case stderrors.Is(domainErr, domainerrors.ErrConflict):
			common.NewError(http.StatusConflict, code).Message(domainErr.Error()).Details(details).Send(c)
			return
		case stderrors.Is(domainErr, domainerrors.ErrServiceUnavailable):
			common.NewError(http.StatusServiceUnavailable, code).Message(domainErr.Error()).Details(details).Send(c)
			return
		}
	}

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
	case strings.Contains(errLower, "bridge validation error"),
		strings.Contains(errLower, "api parameter invalid"):
		h.logger.Warn("Bridge API parameter error", "raw_error", errMsg, "user_id", userID)
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, "Invalid withdrawal parameters. Please check your details and try again.")
	case strings.Contains(errMsg, "bridge client not configured"),
		strings.Contains(errMsg, "bridge wallet ID not provided"),
		strings.Contains(errMsg, "bridge wallet ID is required"):
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
		strings.Contains(errMsg, "bridge transfer failed"),
		strings.Contains(errMsg, "failed to execute transfer"):
		// Surface the inner Bridge error if it's user-actionable
		innerMsg := "Transfer execution failed. Please try again."
		if strings.Contains(errMsg, "Invalid destination address") {
			innerMsg = "Invalid destination address for this chain."
		} else if strings.Contains(errMsg, "Insufficient") {
			innerMsg = "Insufficient balance in custody wallet."
		}
		common.SendBadRequest(c, "TRANSFER_FAILED", innerMsg)
	case strings.Contains(errMsg, "not whitelisted"):
		common.SendBadRequest(c, common.ErrCodeAddressNotWhitelisted, errMsg)
	case strings.Contains(errMsg, "cooling period"):
		common.SendBadRequest(c, common.ErrCodeAddressNotWhitelisted, errMsg)
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
	// Validate account holder name
	accountHolderName := strings.TrimSpace(req.AccountHolderName)
	if len(accountHolderName) < 2 {
		return fmt.Errorf("account_holder_name must be at least 2 characters")
	}
	if len(accountHolderName) > 255 {
		return fmt.Errorf("account_holder_name must not exceed 255 characters")
	}
	// Check for suspicious characters in name (only allow letters, spaces, hyphens, apostrophes, periods)
	for _, ch := range accountHolderName {
		if !unicode.IsLetter(ch) && ch != ' ' && ch != '-' && ch != '\'' && ch != '.' {
			return fmt.Errorf("account_holder_name contains invalid characters")
		}
	}

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
	case "GBP":
		sortCode := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(req.RoutingNumber), " ", ""), "-", "")
		account := strings.ReplaceAll(strings.TrimSpace(req.AccountNumber), " ", "")
		if len(sortCode) != 6 || !isDigits(sortCode) {
			return fmt.Errorf("sort code (routing_number) must be exactly 6 digits for GBP withdrawals")
		}
		if len(account) != 8 || !isDigits(account) {
			return fmt.Errorf("account_number must be exactly 8 digits for GBP withdrawals")
		}
	case "NGN":
		// NGN withdrawals use PAJ integration — bank details validated by PAJ
	default:
		return fmt.Errorf("unsupported fiat currency: %s", currency)
	}
	return nil
}

func normalizeCategory(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", nil
	}
	if len(trimmed) > maxWithdrawalCategoryLength {
		return "", fmt.Errorf("category must be at most %d characters", maxWithdrawalCategoryLength)
	}
	return trimmed, nil
}

func normalizeNarration(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", nil
	}
	if len(trimmed) > maxWithdrawalNarrationLength {
		return "", fmt.Errorf("narration must be at most %d characters", maxWithdrawalNarrationLength)
	}
	return trimmed, nil
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
		strings.Contains(chainUpper, "ARB"), strings.Contains(chainUpper, "OP"),
		strings.Contains(chainUpper, "BSC"), strings.Contains(chainUpper, "BNB"),
		strings.Contains(chainUpper, "MONAD"), strings.Contains(chainUpper, "HYPEREVM"),
		strings.Contains(chainUpper, "LISK"):
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
	case strings.Contains(chainUpper, "STARK"):
		// Starknet addresses are 0x-prefixed hex, up to 66 characters (0x + 64 hex)
		if len(addr) < 42 || len(addr) > 66 {
			return fmt.Errorf("invalid Starknet address: must be 42-66 characters")
		}
		if !strings.HasPrefix(addr, "0x") {
			return fmt.Errorf("invalid Starknet address: must start with 0x")
		}
		hexPart := addr[2:]
		for _, c := range hexPart {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return fmt.Errorf("invalid Starknet address: must be valid hex after 0x")
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

// adminRecoveryMaxAmount caps a single admin recovery so a compromised admin
// token cannot drain an account in one call. Mirrors the internal deposit cap.
var adminRecoveryMaxAmount = decimal.NewFromInt(10000)

// AdminWithdrawalHandlers handles admin withdrawal operations
type AdminWithdrawalHandlers struct {
	withdrawalService WithdrawalServiceInterface
	walletProvider    WalletProvider
	logger            *zap.Logger
}

// NewAdminWithdrawalHandlers creates a new AdminWithdrawalHandlers instance
func NewAdminWithdrawalHandlers(withdrawalService WithdrawalServiceInterface, walletProvider WalletProvider, logger *zap.Logger) *AdminWithdrawalHandlers {
	return &AdminWithdrawalHandlers{
		withdrawalService: withdrawalService,
		walletProvider:    walletProvider,
		logger:            logger,
	}
}

// AdminRecoverCryptoWithdrawalRequest is the payload for an ops-led crypto recovery.
// It exists for pivot/support cases where a user cannot authenticate: the admin
// session is the authority, and the reason + admin identity are logged.
type AdminRecoverCryptoWithdrawalRequest struct {
	UserID             string `json:"user_id" binding:"required"`
	Amount             string `json:"amount" binding:"required"`
	Currency           string `json:"currency,omitempty"`
	DestinationAddress string `json:"destination_address" binding:"required"`
	DestinationChain   string `json:"destination_chain,omitempty"`
	// SourceChain selects WHICH of the user's wallets the funds leave from.
	// Required when the money sits on a different chain than the destination
	// (e.g. funds on BASE, destination SOL). Defaults to the destination chain.
	SourceChain    string `json:"source_chain,omitempty"`
	SourceAccount  string `json:"source_account,omitempty"`
	Reason         string `json:"reason" binding:"required"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// adminRecoveryParams holds the validated inputs for an admin recovery.
type adminRecoveryParams struct {
	targetUserID uuid.UUID
	amount       decimal.Decimal
	currency     entities.WithdrawalCurrency
	sourceChain  string
	destChain    string
	destAddress  string
	source       entities.WithdrawalSourceAccount
	reason       string
}

// bindAdminRecoveryParams parses and validates the admin recovery payload,
// writing the error response itself and returning ok=false on failure.
func bindAdminRecoveryParams(c *gin.Context) (*adminRecoveryParams, bool) {
	var req AdminRecoverCryptoWithdrawalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, "Invalid request format: "+err.Error())
		return nil, false
	}

	reason := strings.TrimSpace(req.Reason)
	if reason == "" || len(reason) > maxWithdrawalNarrationLength {
		common.SendBadRequest(c, common.ErrCodeMissingField, "a reason (1-280 chars) is required for admin recovery")
		return nil, false
	}

	targetUserID, err := uuid.Parse(strings.TrimSpace(req.UserID))
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidUserID, "Invalid user ID format")
		return nil, false
	}

	amount, err := parsePositiveDecimal(req.Amount)
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidAmount, err.Error())
		return nil, false
	}
	if amount.LessThan(decimal.NewFromInt(1)) {
		common.SendBadRequest(c, common.ErrCodeInvalidAmount, "Minimum recovery amount is $1.00")
		return nil, false
	}
	if amount.GreaterThan(adminRecoveryMaxAmount) {
		common.SendBadRequest(c, common.ErrCodeInvalidAmount,
			"amount exceeds the admin recovery cap of $"+adminRecoveryMaxAmount.String())
		return nil, false
	}

	currency := entities.WithdrawalCurrencyUSDC
	if strings.TrimSpace(req.Currency) != "" {
		currency = entities.WithdrawalCurrency(strings.ToUpper(strings.TrimSpace(req.Currency)))
		if !currency.IsStablecoinCurrency() {
			common.SendBadRequest(c, common.ErrCodeInvalidRequest,
				"Unsupported recovery currency. Supported: USDC, USDT, EURC, PYUSD, USDG")
			return nil, false
		}
	}

	destChain := strings.TrimSpace(req.DestinationChain)
	if destChain == "" {
		destChain = string(entities.WalletChainSolana)
	}
	if err := validateCryptoAddress(req.DestinationAddress, destChain); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, err.Error())
		return nil, false
	}

	return &adminRecoveryParams{
		targetUserID: targetUserID,
		amount:       amount,
		currency:     currency,
		sourceChain:  strings.ToUpper(strings.TrimSpace(req.SourceChain)),
		destChain:    destChain,
		destAddress:  req.DestinationAddress,
		source:       resolveSourceAccount(req.SourceAccount),
		reason:       reason,
	}, true
}

// maxAdminRecoveryActorLength bounds the acting-identity label recorded for an
// internal-key recovery so the audit log cannot be flooded.
const maxAdminRecoveryActorLength = 120

// AdminRecoverCryptoWithdrawal handles POST /api/v1/admin/withdrawals/recover.
// It runs the same ledger + provider path as a user withdrawal, but the admin
// JWT (enforced by the admin route group) replaces the passcode session. Every
// call requires a reason and is logged with the acting admin and caller IP.
func (h *AdminWithdrawalHandlers) AdminRecoverCryptoWithdrawal(c *gin.Context) {
	h.recoverCryptoWithdrawal(c, c.GetString("user_id"), "admin_jwt")
}

// InternalRecoverCryptoWithdrawal handles POST /internal/withdrawals/recover.
// Same recovery path, but authorised by the service internal key (plus the
// optional HMAC request signature) instead of an admin session, so ops tooling
// and automation can return funds when no admin login is available.
//
// The internal key is a shared secret with no per-person identity, so the
// caller must name itself in X-Internal-Actor. That label — never the key —
// is what lands in the audit log alongside the reason and target user.
func (h *AdminWithdrawalHandlers) InternalRecoverCryptoWithdrawal(c *gin.Context) {
	actor := strings.TrimSpace(c.GetHeader("X-Internal-Actor"))
	if actor == "" || len(actor) > maxAdminRecoveryActorLength {
		common.SendBadRequest(c, common.ErrCodeMissingField,
			"X-Internal-Actor header is required (1-120 chars) so the recovery is attributable")
		return
	}
	h.recoverCryptoWithdrawal(c, actor, "internal_key")
}

// recoverCryptoWithdrawal is the shared core for both auth modes. actor is the
// human or service that authorised the call; actorSource records which auth
// path accepted it.
func (h *AdminWithdrawalHandlers) recoverCryptoWithdrawal(c *gin.Context, actor, actorSource string) {
	params, ok := bindAdminRecoveryParams(c)
	if !ok {
		return
	}

	if h.walletProvider == nil {
		common.SendInternalError(c, common.ErrCodeServiceUnavailable, "Wallet provider is not configured")
		return
	}

	// Pick the wallet the funds actually leave from. source_chain lets ops
	// recover money sitting on a different chain than the destination (e.g.
	// BASE -> SOL); without it we keep the original behaviour and look up the
	// destination chain's wallet, falling back to Solana.
	lookupChain := params.sourceChain
	if lookupChain == "" {
		lookupChain = params.destChain
	}
	wallet, err := h.walletProvider.GetUserWalletByChain(c.Request.Context(), params.targetUserID, lookupChain)
	if (err != nil || wallet == nil) && params.sourceChain == "" {
		wallet, err = h.walletProvider.GetUserWalletByChain(c.Request.Context(), params.targetUserID, string(entities.WalletChainSolana))
	}
	if err != nil || wallet == nil {
		common.SendBadRequest(c, "NO_WALLET", "No wallet found for user on chain "+lookupChain)
		return
	}
	if wallet.CircleWalletID == "" && strings.TrimSpace(wallet.BridgeWalletID) == "" {
		common.SendInternalError(c, "PROVIDER_NOT_CONFIGURED", "Withdrawal provider is not available for this account")
		return
	}

	idempotencyKey, err := getIdempotencyKey(c)
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, err.Error())
		return
	}
	if idempotencyKey == "" {
		idempotencyKey = uuid.New().String()
	}

	serviceReq := &entities.InitiateCryptoWithdrawalRequest{
		UserID:              params.targetUserID,
		Amount:              params.amount,
		Currency:            params.currency,
		DestinationAddress:  params.destAddress,
		DestinationChain:    params.destChain,
		SourceChain:         string(wallet.Chain),
		SourceAccount:       params.source,
		BridgeWalletID:      wallet.BridgeWalletID,
		CircleWalletID:      wallet.CircleWalletID,
		SourceWalletAddress: wallet.Address,
		Category:            "admin_recovery",
		Narration:           params.reason,
		IdempotencyKey:      idempotencyKey,
	}

	response, err := h.withdrawalService.InitiateCryptoWithdrawal(c.Request.Context(), serviceReq)
	if err != nil {
		h.logger.Error("Crypto recovery failed",
			zap.Error(err),
			zap.String("actor", actor),
			zap.String("actor_source", actorSource),
			zap.String("target_user_id", params.targetUserID.String()),
			zap.String("amount", params.amount.String()),
			zap.String("source_chain", string(wallet.Chain)),
			zap.String("destination_chain", params.destChain),
			zap.String("destination", params.destAddress),
			zap.String("reason", params.reason))
		h.respondRecoveryError(c, err)
		return
	}

	h.logger.Warn("CRYPTO RECOVERY initiated",
		zap.String("actor", actor),
		zap.String("actor_source", actorSource),
		zap.String("target_user_id", params.targetUserID.String()),
		zap.String("amount", params.amount.String()),
		zap.String("currency", string(params.currency)),
		zap.String("source_chain", string(wallet.Chain)),
		zap.String("destination_chain", params.destChain),
		zap.String("destination", params.destAddress),
		zap.String("source_account", string(serviceReq.SourceAccount)),
		zap.String("reason", params.reason),
		zap.String("withdrawal_id", response.WithdrawalID.String()),
		zap.String("caller_ip", c.ClientIP()))

	payload := gin.H{
		"withdrawal_id": response.WithdrawalID,
		"status":        response.Status,
		"message":       response.Message,
		"reason":        params.reason,
		"actor":         actor,
		"actor_source":  actorSource,
	}
	// Keep the admin-specific field for the admin route so existing clients
	// reading admin_id keep working.
	if actorSource == "admin_jwt" {
		payload["admin_id"] = actor
	}

	c.JSON(http.StatusAccepted, payload)
}

// respondRecoveryError maps service errors onto HTTP responses, mirroring the
// user-facing withdrawal error mapping.
func (h *AdminWithdrawalHandlers) respondRecoveryError(c *gin.Context, err error) {
	var domainErr *domainerrors.DomainError
	if stderrors.As(err, &domainErr) {
		code := domainerrors.GetErrorCode(domainErr)
		details := domainerrors.GetErrorDetails(domainErr)
		switch {
		case stderrors.Is(domainErr, domainerrors.ErrInvalidInput):
			common.NewError(http.StatusBadRequest, code).Message(domainErr.Error()).Details(details).Send(c)
			return
		case stderrors.Is(domainErr, domainerrors.ErrConflict):
			common.NewError(http.StatusConflict, code).Message(domainErr.Error()).Details(details).Send(c)
			return
		case stderrors.Is(domainErr, domainerrors.ErrServiceUnavailable):
			common.NewError(http.StatusServiceUnavailable, code).Message(domainErr.Error()).Details(details).Send(c)
			return
		}
	}

	errMsg := err.Error()
	switch {
	case strings.Contains(errMsg, "insufficient"):
		common.SendBadRequest(c, common.ErrCodeInsufficientFunds, "Insufficient balance for recovery")
	case strings.Contains(errMsg, "invalid request"):
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, errMsg)
	case strings.Contains(errMsg, "unsupported"):
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, errMsg)
	default:
		common.SendInternalError(c, common.ErrCodeWithdrawalFailed, "Failed to initiate admin recovery")
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

// resolveSourceAccount maps the HTTP source_account string to the entity type.
func resolveSourceAccount(raw string) entities.WithdrawalSourceAccount {
	if strings.TrimSpace(strings.ToLower(raw)) == "stash_balance" {
		return entities.WithdrawalSourceStashBalance
	}
	return entities.WithdrawalSourceSpendingBalance
}

// EmergencyWithdrawalPreview handles GET /api/v1/withdrawals/emergency/preview?amount=X
func (h *WithdrawalHandlers) EmergencyWithdrawalPreview(c *gin.Context) {
	userID, ok := h.extractUserID(c)
	if !ok {
		return
	}
	amount, err := parsePositiveDecimal(c.Query("amount"))
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidAmount, err.Error())
		return
	}
	preview, err := h.withdrawalService.EmergencyWithdrawalPreview(c.Request.Context(), userID, amount)
	if err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "no locked") {
			common.SendBadRequest(c, "NO_LOCKED_CYCLES", "No locked stash cycles found")
			return
		}
		h.logger.Error("Emergency withdrawal preview failed", "error", err, "user_id", userID)
		common.SendInternalError(c, "PREVIEW_ERROR", "Failed to calculate emergency withdrawal fee")
		return
	}
	c.JSON(http.StatusOK, preview)
}

// EmergencyStashToSpendingRequest is the HTTP request for emergency stash-to-spending transfer.
type EmergencyStashToSpendingRequest struct {
	Amount string `json:"amount" binding:"required"`
}

// EmergencyStashToSpending handles POST /api/v1/withdrawals/emergency/to-spending
func (h *WithdrawalHandlers) EmergencyStashToSpending(c *gin.Context) {
	userID, ok := h.extractUserID(c)
	if !ok {
		return
	}
	var req EmergencyStashToSpendingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, "Invalid request: "+err.Error())
		return
	}
	amount, err := parsePositiveDecimal(req.Amount)
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidAmount, err.Error())
		return
	}
	idempotencyKey, err := getIdempotencyKey(c)
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, err.Error())
		return
	}
	if idempotencyKey == "" {
		idempotencyKey = uuid.New().String()
	}

	result, err := h.withdrawalService.EmergencyStashToSpending(c.Request.Context(), userID, amount, idempotencyKey)
	if err != nil {
		errMsg := err.Error()
		switch {
		case strings.Contains(errMsg, "insufficient"):
			common.SendBadRequest(c, common.ErrCodeInsufficientFunds, errMsg)
		case strings.Contains(errMsg, "no locked"):
			common.SendBadRequest(c, "NO_LOCKED_CYCLES", "No locked stash cycles found")
		default:
			h.logger.Error("Emergency stash-to-spending failed", "error", err, "user_id", userID)
			common.SendInternalError(c, "EMERGENCY_WITHDRAWAL_ERROR", "Failed to execute emergency withdrawal")
		}
		return
	}
	c.JSON(http.StatusOK, result)
}

// FundStashRequest is the HTTP request for funding stash from spending balance.
type FundStashRequest struct {
	Amount string `json:"amount" binding:"required"`
}

// FundStash handles POST /api/v1/funding/stash
func (h *WithdrawalHandlers) FundStash(c *gin.Context) {
	userID, ok := h.extractUserID(c)
	if !ok {
		return
	}
	var req FundStashRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, "Invalid request: "+err.Error())
		return
	}
	amount, err := parsePositiveDecimal(req.Amount)
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidAmount, err.Error())
		return
	}
	idempotencyKey, err := getIdempotencyKey(c)
	if err != nil {
		h.logger.Error("Invalid idempotency key for fund stash", "error", err, "user_id", userID)
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, "Missing or invalid idempotency key")
		return
	}
	if idempotencyKey == "" {
		h.logger.Error("Missing idempotency key for fund stash", "user_id", userID)
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, "Idempotency key is required for fund transfers")
		return
	}

	result, err := h.withdrawalService.FundStash(c.Request.Context(), userID, amount, idempotencyKey)
	if err != nil {
		errMsg := err.Error()
		switch {
		case strings.Contains(errMsg, "insufficient"):
			common.SendBadRequest(c, common.ErrCodeInsufficientFunds, errMsg)
		case strings.Contains(errMsg, "minimum"):
			common.SendBadRequest(c, common.ErrCodeInvalidAmount, errMsg)
		default:
			h.logger.Error("Fund stash failed", "error", err, "user_id", userID)
			common.SendInternalError(c, "FUND_STASH_ERROR", "Failed to fund stash")
		}
		return
	}
	c.JSON(http.StatusOK, result)
}
