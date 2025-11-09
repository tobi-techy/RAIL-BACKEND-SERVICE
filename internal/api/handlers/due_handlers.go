package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stack-service/stack_service/internal/adapters/due"
	"github.com/stack-service/stack_service/internal/domain/services"
	"github.com/stack-service/stack_service/pkg/logger"
)

// DueHandler handles Due API related requests
type DueHandler struct {
	dueService          *services.DueService
	notificationService *services.NotificationService
	logger              *logger.Logger
}

// NewDueHandler creates a new Due handler
func NewDueHandler(dueService *services.DueService, notificationService *services.NotificationService, logger *logger.Logger) *DueHandler {
	return &DueHandler{
		dueService:          dueService,
		notificationService: notificationService,
		logger:              logger,
	}
}

// CreateDueAccountRequest represents request to create Due account
type CreateDueAccountRequest struct {
	Name    string `json:"name" binding:"required"`
	Email   string `json:"email" binding:"required,email"`
	Country string `json:"country" binding:"required,len=2"`
}

// CreateDueAccount creates a Due account for the authenticated user
// @Summary Create Due account
// @Description Creates a Due account for KYC and virtual account management
// @Tags Due
// @Accept json
// @Produce json
// @Param request body CreateDueAccountRequest true "Account details"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/v1/due/account [post]
func (h *DueHandler) CreateDueAccount(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req CreateDueAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	dueAccountID, err := h.dueService.CreateDueAccount(c.Request.Context(), userID.(uuid.UUID), req.Email, req.Name, req.Country)
	if err != nil {
		h.logger.Error("Failed to create Due account", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create Due account"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":        "Due account created successfully",
		"due_account_id": dueAccountID,
	})
}

// GetKYCLink retrieves the KYC verification link
// @Summary Get KYC link
// @Description Retrieves the KYC verification link for the user's Due account
// @Tags Due
// @Produce json
// @Param due_account_id query string true "Due Account ID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/v1/due/kyc-link [get]
func (h *DueHandler) GetKYCLink(c *gin.Context) {
	dueAccountID := c.Query("due_account_id")
	if dueAccountID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "due_account_id is required"})
		return
	}

	kycLink, err := h.dueService.GetKYCLink(c.Request.Context(), dueAccountID)
	if err != nil {
		h.logger.Error("Failed to get KYC link", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get KYC link"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"kyc_link": kycLink,
	})
}

// LinkWalletRequest represents request to link wallet
type LinkWalletRequest struct {
	WalletAddress string `json:"wallet_address" binding:"required"`
	Chain         string `json:"chain" binding:"required"`
}

// LinkWallet links a Circle wallet to Due account
// @Summary Link wallet to Due
// @Description Links a Circle wallet address to the user's Due account
// @Tags Due
// @Accept json
// @Produce json
// @Param request body LinkWalletRequest true "Wallet details"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/v1/due/link-wallet [post]
func (h *DueHandler) LinkWallet(c *gin.Context) {
	var req LinkWalletRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.dueService.LinkCircleWallet(c.Request.Context(), req.WalletAddress, req.Chain); err != nil {
		h.logger.Error("Failed to link wallet", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to link wallet"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Wallet linked successfully",
	})
}

// CreateVirtualAccountRequest represents request to create virtual account
type CreateVirtualAccountRequest struct {
	AccountNumber string `json:"account_number" binding:"required"`
	RoutingNumber string `json:"routing_number" binding:"required"`
	AccountName   string `json:"account_name" binding:"required"`
	Chain         string `json:"chain" binding:"required"`
}

// CreateVirtualAccount creates a USDC->USD virtual account
// @Summary Create virtual account
// @Description Creates a virtual account that accepts USDC deposits and settles to USD bank account
// @Tags Due
// @Accept json
// @Produce json
// @Param request body CreateVirtualAccountRequest true "Virtual account details"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/v1/due/virtual-account [post]
func (h *DueHandler) CreateVirtualAccount(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req CreateVirtualAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Create USD recipient first
	recipientID, err := h.dueService.CreateUSDRecipient(
		c.Request.Context(),
		userID.(uuid.UUID),
		req.AccountNumber,
		req.RoutingNumber,
		req.AccountName,
	)
	if err != nil {
		h.logger.Error("Failed to create recipient", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create recipient"})
		return
	}

	// Create virtual account
	va, err := h.dueService.CreateUSDCToUSDVirtualAccount(
		c.Request.Context(),
		userID.(uuid.UUID),
		recipientID,
		req.Chain,
	)
	if err != nil {
		h.logger.Error("Failed to create virtual account", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create virtual account"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":          "Virtual account created successfully",
		"virtual_account":  va,
		"deposit_address":  va.AccountNumber,
		"recipient_id":     recipientID,
	})
}

// HandleWebhook handles Due webhook events
// @Summary Handle Due webhook
// @Description Processes webhook events from Due API
// @Tags Due
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Router /api/v1/webhooks/due [post]
func (h *DueHandler) HandleWebhook(c *gin.Context) {
	var event map[string]interface{}
	if err := c.ShouldBindJSON(&event); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid webhook payload"})
		return
	}

	eventType, ok := event["type"].(string)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing event type"})
		return
	}

	h.logger.Info("Received Due webhook", "type", eventType)

	switch eventType {
	case "virtual_account.deposit":
		h.handleVirtualAccountDeposit(c, event)
	case "transfer.completed", "transfer.failed":
		h.handleTransferStatusChanged(c, event)
	case "kyc.status_changed":
		h.handleKYCStatusChanged(c, event)
	default:
		h.logger.Warn("Unknown webhook event type", "type", eventType)
	}

	c.JSON(http.StatusOK, gin.H{"message": "webhook processed"})
}

func (h *DueHandler) handleVirtualAccountDeposit(c *gin.Context, event map[string]interface{}) {
	data, ok := event["data"].(map[string]interface{})
	if !ok {
		h.logger.Error("Invalid virtual account deposit webhook data")
		return
	}

	virtualAccountID, _ := data["id"].(string)
	amount, _ := data["amount"].(string)
	currency, _ := data["currency"].(string)
	nonce, _ := data["nonce"].(string)
	transactionID, _ := data["transactionId"].(string)

	h.logger.Info("Virtual account deposit received",
		"virtual_account_id", virtualAccountID,
		"amount", amount,
		"currency", currency,
		"nonce", nonce,
		"transaction_id", transactionID)

	// Initiate off-ramp process
	if err := h.dueService.HandleVirtualAccountDeposit(c.Request.Context(), virtualAccountID, amount, currency, transactionID, nonce); err != nil {
		h.logger.Error("Failed to handle virtual account deposit", "error", err)
		return
	}

	h.logger.Info("Virtual account deposit processed successfully", "transaction_id", transactionID)
}

func (h *DueHandler) handleTransferStatusChanged(c *gin.Context, event map[string]interface{}) {
	data, ok := event["data"].(map[string]interface{})
	if !ok {
		h.logger.Error("Invalid transfer webhook data")
		return
	}

	transferID, _ := data["id"].(string)
	status, _ := data["status"].(string)

	h.logger.Info("Transfer status changed",
		"transfer_id", transferID,
		"status", status)

	// Retrieve transfer details to get user context
	transfer, err := h.dueService.GetTransfer(c.Request.Context(), transferID)
	if err != nil {
		h.logger.Error("Failed to retrieve transfer details", "transfer_id", transferID, "error", err)
		return
	}

	// Extract amount from destination
	amount := transfer.Destination.Amount

	// Notify user based on status
	if status == "completed" {
		h.logger.Info("Transfer completed successfully", "transfer_id", transferID, "amount", amount)
		// Note: userID would need to be extracted from transfer reference or stored separately
	} else if status == "failed" {
		h.logger.Warn("Transfer failed", "transfer_id", transferID)
	}
}

func (h *DueHandler) handleKYCStatusChanged(c *gin.Context, event map[string]interface{}) {
	data, ok := event["data"].(map[string]interface{})
	if !ok {
		h.logger.Error("Invalid KYC webhook data")
		return
	}

	accountID, _ := data["accountId"].(string)
	status, _ := data["status"].(string)

	h.logger.Info("KYC status changed",
		"account_id", accountID,
		"status", status)

	// Retrieve account details to verify status
	account, err := h.dueService.GetAccount(c.Request.Context(), accountID)
	if err != nil {
		h.logger.Error("Failed to retrieve account details", "account_id", accountID, "error", err)
		return
	}

	h.logger.Info("KYC status update processed",
		"account_id", accountID,
		"status", status,
		"account_email", account.Email)
}

// GetKYCStatus retrieves current KYC status
// @Summary Get KYC status
// @Description Retrieves current KYC status for a Due account
// @Tags Due
// @Accept json
// @Produce json
// @Param due_account_id query string true "Due Account ID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/v1/due/kyc-status [get]
func (h *DueHandler) GetKYCStatus(c *gin.Context) {
	dueAccountID := c.Query("due_account_id")
	if dueAccountID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "due_account_id is required"})
		return
	}

	status, err := h.dueService.GetKYCStatus(c.Request.Context(), dueAccountID)
	if err != nil {
		h.logger.Error("Failed to get KYC status", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get KYC status"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"kyc_status": status,
	})
}

// InitiateKYC initiates KYC process programmatically
// @Summary Initiate KYC
// @Description Initiates KYC process programmatically and returns session details
// @Tags Due
// @Accept json
// @Produce json
// @Param due_account_id query string true "Due Account ID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/v1/due/initiate-kyc [post]
func (h *DueHandler) InitiateKYC(c *gin.Context) {
	dueAccountID := c.Query("due_account_id")
	if dueAccountID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "due_account_id is required"})
		return
	}

	resp, err := h.dueService.InitiateKYCProgrammatic(c.Request.Context(), dueAccountID)
	if err != nil {
		h.logger.Error("Failed to initiate KYC", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to initiate KYC"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"kyc_session": resp,
	})
}

// AcceptTermsOfService accepts Terms of Service
// @Summary Accept Terms of Service
// @Description Accepts Terms of Service for a Due account
// @Tags Due
// @Accept json
// @Produce json
// @Param request body AcceptTOSRequest true "ToS acceptance details"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/v1/due/accept-tos [post]
func (h *DueHandler) AcceptTermsOfService(c *gin.Context) {
	var req AcceptTOSRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	err := h.dueService.AcceptTermsOfService(c.Request.Context(), req.DueAccountID, req.ToSToken)
	if err != nil {
		h.logger.Error("Failed to accept ToS", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to accept Terms of Service"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Terms of Service accepted successfully",
	})
}

// CreateTransferRequest represents transfer creation request
type CreateTransferRequest struct {
	SourceID      string `json:"source_id" binding:"required"`
	DestinationID string `json:"destination_id" binding:"required"`
	Amount        string `json:"amount" binding:"required"`
	Currency      string `json:"currency" binding:"required"`
	Reference     string `json:"reference" binding:"required"`
}

// AcceptTOSRequest represents ToS acceptance request
type AcceptTOSRequest struct {
	DueAccountID string `json:"due_account_id" binding:"required"`
	ToSToken     string `json:"tos_token" binding:"required"`
}

// CreateTransfer creates a transfer from virtual account to recipient
// @Summary Create transfer
// @Description Creates a transfer from virtual account to recipient for USDC to USD conversion
// @Tags Due
// @Accept json
// @Produce json
// @Param request body CreateTransferRequest true "Transfer details"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/v1/due/transfer [post]
func (h *DueHandler) CreateTransfer(c *gin.Context) {
	var req CreateTransferRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	transfer, err := h.dueService.CreateTransfer(
		c.Request.Context(),
		req.SourceID,
		req.DestinationID,
		req.Amount,
		req.Currency,
		req.Reference,
	)
	if err != nil {
		h.logger.Error("Failed to create transfer", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create transfer"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":  "Transfer created successfully",
		"transfer": transfer,
	})
}

// GetTransfer retrieves transfer details
// @Summary Get transfer
// @Description Retrieves transfer details by ID
// @Tags Due
// @Produce json
// @Param transfer_id path string true "Transfer ID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/v1/due/transfer/{transfer_id} [get]
func (h *DueHandler) GetTransfer(c *gin.Context) {
	transferID := c.Param("transfer_id")
	if transferID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "transfer_id is required"})
		return
	}

	transfer, err := h.dueService.GetTransfer(c.Request.Context(), transferID)
	if err != nil {
		h.logger.Error("Failed to get transfer", "transfer_id", transferID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get transfer"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"transfer": transfer,
	})
}

// ListRecipients retrieves all recipients with pagination
// @Summary List recipients
// @Description Retrieves all recipients with pagination
// @Tags Due
// @Produce json
// @Param limit query int false "Limit" default(50)
// @Param offset query int false "Offset" default(0)
// @Success 200 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/v1/due/recipients [get]
func (h *DueHandler) ListRecipients(c *gin.Context) {
	limit := 50
	offset := 0

	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if o := c.Query("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	resp, err := h.dueService.ListRecipients(c.Request.Context(), limit, offset)
	if err != nil {
		h.logger.Error("Failed to list recipients", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list recipients"})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// GetRecipient retrieves a recipient by ID
// @Summary Get recipient
// @Description Retrieves a recipient by ID
// @Tags Due
// @Produce json
// @Param recipient_id path string true "Recipient ID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/v1/due/recipients/{recipient_id} [get]
func (h *DueHandler) GetRecipient(c *gin.Context) {
	recipientID := c.Param("recipient_id")
	if recipientID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "recipient_id is required"})
		return
	}

	resp, err := h.dueService.GetRecipient(c.Request.Context(), recipientID)
	if err != nil {
		h.logger.Error("Failed to get recipient", "recipient_id", recipientID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get recipient"})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// ListVirtualAccounts retrieves all virtual accounts with filters
// @Summary List virtual accounts
// @Description Retrieves all virtual accounts with optional filters
// @Tags Due
// @Produce json
// @Param destination query string false "Destination ID"
// @Param schema_in query string false "Schema In (e.g., solana, evm)"
// @Param currency_in query string false "Currency In (e.g., USDC)"
// @Param rail_out query string false "Rail Out (e.g., ach)"
// @Param currency_out query string false "Currency Out (e.g., USD)"
// @Success 200 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/v1/due/virtual-accounts [get]
func (h *DueHandler) ListVirtualAccounts(c *gin.Context) {
	destination := c.Query("destination")
	schemaIn := c.Query("schema_in")
	currencyIn := c.Query("currency_in")
	railOut := c.Query("rail_out")
	currencyOut := c.Query("currency_out")

	resp, err := h.dueService.ListVirtualAccounts(c.Request.Context(), destination, schemaIn, currencyIn, railOut, currencyOut)
	if err != nil {
		h.logger.Error("Failed to list virtual accounts", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list virtual accounts"})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// ListTransfers retrieves transfers with pagination and filters
// @Summary List transfers
// @Description Retrieves transfers with pagination and optional filters
// @Tags Due
// @Produce json
// @Param limit query int false "Limit" default(50)
// @Param order query string false "Order (asc/desc)" default(desc)
// @Param status query string false "Status filter"
// @Success 200 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/v1/due/transfers [get]
func (h *DueHandler) ListTransfers(c *gin.Context) {
	limit := 50
	order := "desc"
	var status due.TransferStatus

	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if o := c.Query("order"); o != "" {
		order = o
	}
	if s := c.Query("status"); s != "" {
		status = due.TransferStatus(s)
	}

	resp, err := h.dueService.ListTransfers(c.Request.Context(), limit, order, status)
	if err != nil {
		h.logger.Error("Failed to list transfers", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list transfers"})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// GetChannels retrieves available payment channels
// @Summary Get payment channels
// @Description Retrieves available payment channels and methods
// @Tags Due
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/v1/due/channels [get]
func (h *DueHandler) GetChannels(c *gin.Context) {
	resp, err := h.dueService.GetChannels(c.Request.Context())
	if err != nil {
		h.logger.Error("Failed to get channels", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get channels"})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// CreateQuoteRequest represents quote creation request
type CreateQuoteRequest struct {
	Sender    string `json:"sender" binding:"required"`
	Recipient string `json:"recipient" binding:"required"`
	Amount    string `json:"amount" binding:"required"`
	Currency  string `json:"currency" binding:"required"`
}

// CreateQuote creates a quote for a transfer
// @Summary Create transfer quote
// @Description Creates a quote for a transfer with FX rate and fees
// @Tags Due
// @Accept json
// @Produce json
// @Param request body CreateQuoteRequest true "Quote details"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/v1/due/quote [post]
func (h *DueHandler) CreateQuote(c *gin.Context) {
	var req CreateQuoteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.dueService.CreateQuote(c.Request.Context(), req.Sender, req.Recipient, req.Amount, req.Currency)
	if err != nil {
		h.logger.Error("Failed to create quote", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create quote"})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// ListWallets retrieves all linked wallets
// @Summary List wallets
// @Description Retrieves all linked wallets
// @Tags Due
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/v1/due/wallets [get]
func (h *DueHandler) ListWallets(c *gin.Context) {
	resp, err := h.dueService.ListWallets(c.Request.Context())
	if err != nil {
		h.logger.Error("Failed to list wallets", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list wallets"})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// GetWalletByID retrieves a wallet by ID
// @Summary Get wallet
// @Description Retrieves a wallet by ID
// @Tags Due
// @Produce json
// @Param wallet_id path string true "Wallet ID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/v1/due/wallets/{wallet_id} [get]
func (h *DueHandler) GetWalletByID(c *gin.Context) {
	walletID := c.Param("wallet_id")
	if walletID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "wallet_id is required"})
		return
	}

	resp, err := h.dueService.GetWallet(c.Request.Context(), walletID)
	if err != nil {
		h.logger.Error("Failed to get wallet", "wallet_id", walletID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get wallet"})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// CreateWebhookEndpointRequest represents webhook endpoint creation request
type CreateWebhookEndpointRequest struct {
	URL         string   `json:"url" binding:"required,url"`
	Events      []string `json:"events" binding:"required,min=1"`
	Description string   `json:"description"`
}

// CreateWebhookEndpoint creates a webhook endpoint
// @Summary Create webhook endpoint
// @Description Creates a webhook endpoint for receiving Due events
// @Tags Due
// @Accept json
// @Produce json
// @Param request body CreateWebhookEndpointRequest true "Webhook details"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/v1/due/webhooks [post]
func (h *DueHandler) CreateWebhookEndpoint(c *gin.Context) {
	var req CreateWebhookEndpointRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.dueService.CreateWebhookEndpoint(c.Request.Context(), req.URL, req.Events, req.Description)
	if err != nil {
		h.logger.Error("Failed to create webhook endpoint", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create webhook endpoint"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Webhook endpoint created successfully",
		"webhook": resp,
	})
}

// ListWebhookEndpoints retrieves all webhook endpoints
// @Summary List webhook endpoints
// @Description Retrieves all webhook endpoints
// @Tags Due
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/v1/due/webhooks [get]
func (h *DueHandler) ListWebhookEndpoints(c *gin.Context) {
	resp, err := h.dueService.ListWebhookEndpoints(c.Request.Context())
	if err != nil {
		h.logger.Error("Failed to list webhook endpoints", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list webhook endpoints"})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// DeleteWebhookEndpoint deletes a webhook endpoint
// @Summary Delete webhook endpoint
// @Description Deletes a webhook endpoint by ID
// @Tags Due
// @Produce json
// @Param webhook_id path string true "Webhook ID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/v1/due/webhooks/{webhook_id} [delete]
func (h *DueHandler) DeleteWebhookEndpoint(c *gin.Context) {
	webhookID := c.Param("webhook_id")
	if webhookID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "webhook_id is required"})
		return
	}

	err := h.dueService.DeleteWebhookEndpoint(c.Request.Context(), webhookID)
	if err != nil {
		h.logger.Error("Failed to delete webhook endpoint", "webhook_id", webhookID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete webhook endpoint"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Webhook endpoint deleted successfully",
	})
}

// GetDueAccount retrieves Due account details
// @Summary Get Due account
// @Description Retrieves Due account details including KYC and ToS status
// @Tags Due
// @Produce json
// @Param account_id query string true "Due Account ID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/v1/due/account [get]
func (h *DueHandler) GetDueAccount(c *gin.Context) {
	accountID := c.Query("account_id")
	if accountID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "account_id is required"})
		return
	}

	resp, err := h.dueService.GetAccount(c.Request.Context(), accountID)
	if err != nil {
		h.logger.Error("Failed to get account", "account_id", accountID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get account"})
		return
	}

	c.JSON(http.StatusOK, resp)
}
