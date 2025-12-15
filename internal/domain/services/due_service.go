package services

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/adapters/due"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/rail-service/rail_service/pkg/logger"
)

// DueService handles Due API integration
type DueService struct {
	dueClient     *due.Client
	depositRepo   *repositories.DepositRepository
	balanceRepo   *repositories.BalanceRepository
	logger        *logger.Logger
}

// NewDueService creates a new Due service
func NewDueService(dueClient *due.Client, depositRepo *repositories.DepositRepository, balanceRepo *repositories.BalanceRepository, logger *logger.Logger) *DueService {
	return &DueService{
		dueClient:   dueClient,
		depositRepo: depositRepo,
		balanceRepo: balanceRepo,
		logger:      logger,
	}
}

// CreateDueAccount creates a Due account for a user
func (s *DueService) CreateDueAccount(ctx context.Context, userID uuid.UUID, email, name, country string) (string, error) {
	s.logger.Info("Creating Due account", "user_id", userID, "email", email)

	// Parse name into first and last name
	nameParts := strings.Fields(name)
	firstName := name
	lastName := ""
	if len(nameParts) >= 2 {
		firstName = nameParts[0]
		lastName = strings.Join(nameParts[1:], " ")
	}

	req := &entities.CreateAccountRequest{
		Type:      "individual",
		FirstName: firstName,
		LastName:  lastName,
		Email:     email,
		Country:   country,
	}

	resp, err := s.dueClient.CreateAccount(ctx, req)
	if err != nil {
		s.logger.Error("Failed to create Due account", "error", err)
		return "", fmt.Errorf("create Due account: %w", err)
	}

	s.logger.Info("Created Due account", "due_account_id", resp.AccountID)
	return resp.AccountID, nil
}

// GetKYCLink retrieves the KYC verification link for a Due account
func (s *DueService) GetKYCLink(ctx context.Context, dueAccountID string) (string, error) {
	// Determine base URL based on client configuration
	baseURL := "https://app.due.network" // Production default
	if strings.Contains(s.dueClient.Config().BaseURL, "sandbox") {
		baseURL = "https://app.sandbox.due.network"
	}

	// For now, return a placeholder KYC link since the response structure doesn't include KYC details
	return fmt.Sprintf("%s/kyc/%s", baseURL, dueAccountID), nil
}

// LinkCircleWallet links a Circle wallet to Due account
func (s *DueService) LinkCircleWallet(ctx context.Context, walletAddress, chain string) error {
	s.logger.Info("Linking Circle wallet to Due", "address", walletAddress, "chain", chain)

	// Format address according to Due requirements
	var formattedAddress string
	switch chain {
	case "SOL-DEVNET", "SOL":
		formattedAddress = fmt.Sprintf("solana:%s", walletAddress)
	case "MATIC", "MATIC-AMOY", "ETH", "ETH-SEPOLIA", "BASE", "BASE-SEPOLIA":
		formattedAddress = fmt.Sprintf("evm:%s", walletAddress)
	default:
		return fmt.Errorf("unsupported chain: %s", chain)
	}

	req := &due.LinkWalletRequest{
		Address: formattedAddress,
	}

	_, err := s.dueClient.LinkWallet(ctx, req)
	if err != nil {
		s.logger.Error("Failed to link wallet", "error", err)
		return fmt.Errorf("link wallet: %w", err)
	}

	s.logger.Info("Linked wallet successfully")
	return nil
}

// CreateUSDRecipient creates a USD bank recipient for off-ramping
func (s *DueService) CreateUSDRecipient(ctx context.Context, userID uuid.UUID, accountNumber, routingNumber, accountName string) (string, error) {
	s.logger.Info("Creating USD recipient", "user_id", userID)

	req := &due.CreateRecipientRequest{
		Name: accountName,
		Details: due.RecipientDetails{
			Schema:  "bank_us",
			Address: accountNumber,
		},
		IsExternal: true,
	}

	resp, err := s.dueClient.CreateRecipient(ctx, req)
	if err != nil {
		s.logger.Error("Failed to create recipient", "error", err)
		return "", fmt.Errorf("create recipient: %w", err)
	}

	s.logger.Info("Created USD recipient", "recipient_id", resp.ID)
	return resp.ID, nil
}

// CreateUSDCToUSDVirtualAccount creates a virtual account that accepts USDC and settles to USD
func (s *DueService) CreateUSDCToUSDVirtualAccount(ctx context.Context, userID uuid.UUID, recipientID, chain string) (*entities.VirtualAccount, error) {
	s.logger.Info("Creating USDC->USD virtual account", "user_id", userID, "chain", chain)

	reference := fmt.Sprintf("user_%s_%s_usdc_usd", userID.String()[:8], chain)

	// Determine schema based on chain
	var schemaIn string
	switch chain {
	case "SOL-DEVNET", "SOL":
		schemaIn = "solana"
	case "MATIC", "MATIC-AMOY":
		schemaIn = "evm"
	default:
		return nil, fmt.Errorf("unsupported chain for virtual account: %s", chain)
	}

	req := &due.CreateVirtualAccountRequest{
		Destination: recipientID,
		SchemaIn:    schemaIn,
		CurrencyIn:  "USDC",
		RailOut:     "ach",
		CurrencyOut: "USD",
		Reference:   reference,
	}

	resp, err := s.dueClient.CreateVirtualAccount(ctx, req)
	if err != nil {
		s.logger.Error("Failed to create virtual account", "error", err)
		return nil, fmt.Errorf("create virtual account: %w", err)
	}

	// Convert to domain entity
	va := &entities.VirtualAccount{
		ID:              uuid.New(),
		UserID:          userID,
		DueAccountID:    resp.Nonce,
		AlpacaAccountID: recipientID,
		AccountNumber:   resp.Details.AccountNumber,
		RoutingNumber:   resp.Details.RoutingNumber,
		Status:          entities.VirtualAccountStatusActive,
		Currency:        "USD",
	}

	// For crypto virtual accounts, store the deposit address
	if resp.Details.Address != "" {
		va.AccountNumber = resp.Details.Address
	}

	s.logger.Info("Created virtual account", "va_id", va.ID, "due_account_id", va.DueAccountID)
	return va, nil
}

// GetKYCStatus retrieves current KYC status for a Due account
func (s *DueService) GetKYCStatus(ctx context.Context, accountID string) (due.KYCStatus, error) {
	kycResp, err := s.dueClient.GetKYCStatus(ctx, accountID)
	if err != nil {
		s.logger.Error("Failed to get KYC status", "error", err)
		return "", fmt.Errorf("get KYC status: %w", err)
	}

	return kycResp.Status, nil
}

// InitiateKYCProgrammatic initiates KYC process programmatically
func (s *DueService) InitiateKYCProgrammatic(ctx context.Context, accountID string) (*due.KYCInitiateResponse, error) {
	resp, err := s.dueClient.InitiateKYC(ctx, accountID)
	if err != nil {
		s.logger.Error("Failed to initiate KYC", "error", err)
		return nil, fmt.Errorf("initiate KYC: %w", err)
	}

	return resp, nil
}

// AcceptTermsOfService accepts Terms of Service for a user
func (s *DueService) AcceptTermsOfService(ctx context.Context, accountID, tosToken string) error {
	_, err := s.dueClient.AcceptTermsOfService(ctx, accountID, tosToken)
	if err != nil {
		s.logger.Error("Failed to accept ToS", "error", err)
		return fmt.Errorf("accept ToS: %w", err)
	}

	s.logger.Info("Successfully accepted Terms of Service", "account_id", accountID)
	return nil
}

// CreateTransfer initiates a transfer from virtual account to recipient
func (s *DueService) CreateTransfer(ctx context.Context, sourceID, destinationID, amount, currency, reference string) (*due.CreateTransferResponse, error) {
	req := &due.CreateTransferRequest{
		SourceID:      sourceID,
		DestinationID: destinationID,
		Amount:        amount,
		Currency:      currency,
		Reference:     reference,
	}

	resp, err := s.dueClient.CreateTransfer(ctx, req)
	if err != nil {
		s.logger.Error("Failed to create transfer", "error", err)
		return nil, fmt.Errorf("create transfer: %w", err)
	}

	s.logger.Info("Created transfer", "transfer_id", resp.ID, "status", resp.Status)
	return resp, nil
}

// GetTransfer retrieves transfer details
func (s *DueService) GetTransfer(ctx context.Context, transferID string) (*due.CreateTransferResponse, error) {
	resp, err := s.dueClient.GetTransfer(ctx, transferID)
	if err != nil {
		s.logger.Error("Failed to get transfer", "transfer_id", transferID, "error", err)
		return nil, fmt.Errorf("get transfer: %w", err)
	}

	return resp, nil
}

// ListRecipients retrieves all recipients with pagination
func (s *DueService) ListRecipients(ctx context.Context, limit, offset int) (*due.ListRecipientsResponse, error) {
	resp, err := s.dueClient.ListRecipients(ctx, limit, offset)
	if err != nil {
		s.logger.Error("Failed to list recipients", "error", err)
		return nil, fmt.Errorf("list recipients: %w", err)
	}
	return resp, nil
}

// GetRecipient retrieves a recipient by ID
func (s *DueService) GetRecipient(ctx context.Context, recipientID string) (*due.CreateRecipientResponse, error) {
	resp, err := s.dueClient.GetRecipient(ctx, recipientID)
	if err != nil {
		s.logger.Error("Failed to get recipient", "recipient_id", recipientID, "error", err)
		return nil, fmt.Errorf("get recipient: %w", err)
	}
	return resp, nil
}

// ListVirtualAccounts retrieves all virtual accounts with filters
func (s *DueService) ListVirtualAccounts(ctx context.Context, destination, schemaIn, currencyIn, railOut, currencyOut string) (*due.ListVirtualAccountsResponse, error) {
	filters := &due.VirtualAccountFilters{
		Destination: destination,
		SchemaIn:    schemaIn,
		CurrencyIn:  currencyIn,
		RailOut:     railOut,
		CurrencyOut: currencyOut,
	}

	resp, err := s.dueClient.ListVirtualAccounts(ctx, filters)
	if err != nil {
		s.logger.Error("Failed to list virtual accounts", "error", err)
		return nil, fmt.Errorf("list virtual accounts: %w", err)
	}
	return resp, nil
}

// ListTransfers retrieves transfers with pagination and filters
func (s *DueService) ListTransfers(ctx context.Context, limit int, order string, status due.TransferStatus) (*due.ListTransfersResponse, error) {
	filters := &due.TransferFilters{
		Limit:  limit,
		Order:  order,
		Status: status,
	}

	resp, err := s.dueClient.ListTransfers(ctx, filters)
	if err != nil {
		s.logger.Error("Failed to list transfers", "error", err)
		return nil, fmt.Errorf("list transfers: %w", err)
	}
	return resp, nil
}

// GetChannels retrieves available payment channels
func (s *DueService) GetChannels(ctx context.Context) (*due.ChannelsResponse, error) {
	resp, err := s.dueClient.GetChannels(ctx)
	if err != nil {
		s.logger.Error("Failed to get channels", "error", err)
		return nil, fmt.Errorf("get channels: %w", err)
	}
	return resp, nil
}

// CreateQuote creates a quote for a transfer
func (s *DueService) CreateQuote(ctx context.Context, sender, recipient, amount, currency string) (*due.QuoteResponse, error) {
	req := &due.CreateQuoteRequest{
		Sender:    sender,
		Recipient: recipient,
		Amount:    amount,
		Currency:  currency,
	}

	resp, err := s.dueClient.CreateQuote(ctx, req)
	if err != nil {
		s.logger.Error("Failed to create quote", "error", err)
		return nil, fmt.Errorf("create quote: %w", err)
	}

	s.logger.Info("Created quote", "quote_id", resp.ID, "fx_rate", resp.FXRate)
	return resp, nil
}

// ListWallets retrieves all linked wallets
func (s *DueService) ListWallets(ctx context.Context) (*due.ListWalletsResponse, error) {
	resp, err := s.dueClient.ListWallets(ctx)
	if err != nil {
		s.logger.Error("Failed to list wallets", "error", err)
		return nil, fmt.Errorf("list wallets: %w", err)
	}
	return resp, nil
}

// GetWallet retrieves a wallet by ID
func (s *DueService) GetWallet(ctx context.Context, walletID string) (*due.LinkWalletResponse, error) {
	resp, err := s.dueClient.GetWallet(ctx, walletID)
	if err != nil {
		s.logger.Error("Failed to get wallet", "wallet_id", walletID, "error", err)
		return nil, fmt.Errorf("get wallet: %w", err)
	}
	return resp, nil
}

// CreateWebhookEndpoint creates a webhook endpoint
func (s *DueService) CreateWebhookEndpoint(ctx context.Context, url string, events []string, description string) (*due.WebhookEndpointResponse, error) {
	req := &due.CreateWebhookRequest{
		URL:         url,
		Events:      events,
		Description: description,
	}

	resp, err := s.dueClient.CreateWebhookEndpoint(ctx, req)
	if err != nil {
		s.logger.Error("Failed to create webhook endpoint", "error", err)
		return nil, fmt.Errorf("create webhook endpoint: %w", err)
	}

	s.logger.Info("Created webhook endpoint", "id", resp.ID, "url", resp.URL)
	return resp, nil
}

// ListWebhookEndpoints retrieves all webhook endpoints
func (s *DueService) ListWebhookEndpoints(ctx context.Context) (*due.ListWebhookEndpointsResponse, error) {
	resp, err := s.dueClient.ListWebhookEndpoints(ctx)
	if err != nil {
		s.logger.Error("Failed to list webhook endpoints", "error", err)
		return nil, fmt.Errorf("list webhook endpoints: %w", err)
	}
	return resp, nil
}

// DeleteWebhookEndpoint deletes a webhook endpoint
func (s *DueService) DeleteWebhookEndpoint(ctx context.Context, webhookID string) error {
	err := s.dueClient.DeleteWebhookEndpoint(ctx, webhookID)
	if err != nil {
		s.logger.Error("Failed to delete webhook endpoint", "webhook_id", webhookID, "error", err)
		return fmt.Errorf("delete webhook endpoint: %w", err)
	}

	s.logger.Info("Deleted webhook endpoint", "webhook_id", webhookID)
	return nil
}

// GetAccount retrieves Due account details
func (s *DueService) GetAccount(ctx context.Context, accountID string) (*entities.CreateAccountResponse, error) {
	resp, err := s.dueClient.GetAccount(ctx, accountID)
	if err != nil {
		s.logger.Error("Failed to get account", "account_id", accountID, "error", err)
		return nil, fmt.Errorf("get account: %w", err)
	}
	return resp, nil
}

// HandleVirtualAccountDeposit handles a virtual account deposit event
func (s *DueService) HandleVirtualAccountDeposit(ctx context.Context, virtualAccountID, amount, currency, transactionID, nonce string) error {
	s.logger.Info("Handling virtual account deposit",
		"virtual_account_id", virtualAccountID,
		"amount", amount,
		"currency", currency,
		"transaction_id", transactionID,
		"nonce", nonce)

	// Parse amount
	depositAmount, err := decimal.NewFromString(amount)
	if err != nil {
		s.logger.Error("Invalid deposit amount", "amount", amount, "error", err)
		return fmt.Errorf("invalid amount: %w", err)
	}

	// Check if deposit already exists by off-ramp transaction ID
	existingDeposit, err := s.depositRepo.GetByOffRampTxID(ctx, transactionID)
	if err == nil && existingDeposit != nil {
		s.logger.Info("Deposit already processed", "transaction_id", transactionID)
		return nil
	}

	// Parse virtual account ID to get user ID (assuming nonce contains user reference)
	// Format: "user_{userID}_{chain}_usdc_usd" or similar
	var userID uuid.UUID
	if strings.HasPrefix(nonce, "user_") {
		parts := strings.Split(nonce, "_")
		if len(parts) >= 2 {
			// Try to parse the user ID portion
			if parsedID, err := uuid.Parse(parts[1]); err == nil {
				userID = parsedID
			}
		}
	}

	if userID == uuid.Nil {
		s.logger.Error("Could not extract user ID from nonce", "nonce", nonce)
		return fmt.Errorf("invalid nonce format: cannot extract user ID")
	}

	// Create deposit record
	now := time.Now()
	deposit := &entities.Deposit{
		ID:                 uuid.New(),
		UserID:             userID,
		Amount:             depositAmount,
		Status:             "off_ramp_completed",
		OffRampTxID:        &transactionID,
		OffRampCompletedAt: &now,
		CreatedAt:          now,
	}

	if err := s.depositRepo.Create(ctx, deposit); err != nil {
		s.logger.Error("Failed to create deposit record", "error", err)
		return fmt.Errorf("create deposit: %w", err)
	}

	// Update user balance - add to buying power
	if err := s.balanceRepo.AddBuyingPower(ctx, userID, depositAmount); err != nil {
		s.logger.Error("Failed to update user balance", "error", err, "user_id", userID.String())
		return fmt.Errorf("update balance: %w", err)
	}

	s.logger.Info("Virtual account deposit handled successfully",
		"transaction_id", transactionID,
		"user_id", userID.String(),
		"amount", depositAmount.String())
	return nil
}
