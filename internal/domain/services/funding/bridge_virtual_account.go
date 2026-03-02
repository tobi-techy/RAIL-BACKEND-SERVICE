package funding

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/bridge"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/shopspring/decimal"
)

// BridgeVirtualAccountService handles Bridge virtual account operations for fiat funding
type BridgeVirtualAccountService struct {
	bridgeClient        bridge.BridgeClient
	virtualAccountRepo  VirtualAccountRepository
	depositRepo         DepositRepository
	allocationService   AllocationService
	ledgerIntegration   LedgerIntegration
	notificationService FundingNotificationService
	logger              *logger.Logger
}

// AllocationService interface for 70/30 split processing
type AllocationService interface {
	ProcessIncomingFunds(ctx context.Context, req *entities.IncomingFundsRequest) error
}

// BridgeVirtualAccountRepository extends VirtualAccountRepository with Bridge-specific methods
type BridgeVirtualAccountRepository interface {
	VirtualAccountRepository
	GetByBridgeAccountID(ctx context.Context, bridgeAccountID string) (*entities.VirtualAccount, error)
	GetAccountsForMigration(ctx context.Context, limit int) ([]*entities.VirtualAccount, error)
	UpdateBridgeAccountID(ctx context.Context, id uuid.UUID, bridgeAccountID string) error
}

// NewBridgeVirtualAccountService creates a new Bridge virtual account service
func NewBridgeVirtualAccountService(
	bridgeClient bridge.BridgeClient,
	virtualAccountRepo VirtualAccountRepository,
	depositRepo DepositRepository,
	allocationService AllocationService,
	ledgerIntegration LedgerIntegration,
	logger *logger.Logger,
) *BridgeVirtualAccountService {
	return &BridgeVirtualAccountService{
		bridgeClient:       bridgeClient,
		virtualAccountRepo: virtualAccountRepo,
		depositRepo:        depositRepo,
		allocationService:  allocationService,
		ledgerIntegration:  ledgerIntegration,
		logger:             logger,
	}
}

// SetDepositRepository sets the deposit repository for fiat deposits
func (s *BridgeVirtualAccountService) SetDepositRepository(depositRepo DepositRepository) {
	s.depositRepo = depositRepo
}

// SetNotificationService sets the notification service for fiat deposit events
func (s *BridgeVirtualAccountService) SetNotificationService(notificationService FundingNotificationService) {
	s.notificationService = notificationService
}

// CreateVirtualAccountRequest represents a request to create a Bridge virtual account
type CreateBridgeVirtualAccountRequest struct {
	UserID           uuid.UUID
	BridgeCustomerID string
	Currency         string // "USD" or "GBP"
	DestinationChain bridge.PaymentRail
	WalletAddress    string // Destination wallet for converted USDC
}

// CreateVirtualAccount creates a new Bridge virtual account for fiat deposits
func (s *BridgeVirtualAccountService) CreateVirtualAccount(ctx context.Context, req *CreateBridgeVirtualAccountRequest) (*entities.VirtualAccount, error) {
	s.logger.Info("Creating Bridge virtual account",
		"user_id", req.UserID,
		"currency", req.Currency,
		"destination_chain", req.DestinationChain)

	// Determine source currency
	sourceCurrency := bridge.CurrencyUSD
	if req.Currency == "GBP" {
		// Bridge doesn't support GBP directly, would need FX conversion
		// For now, default to USD
		s.logger.Warn("GBP not directly supported, using USD", "user_id", req.UserID)
		sourceCurrency = bridge.CurrencyUSD
	}

	// Create Bridge virtual account request
	bridgeReq := &bridge.CreateVirtualAccountRequest{
		Source: bridge.VirtualAccountSource{
			Currency: sourceCurrency,
		},
		Destination: bridge.VirtualAccountDestination{
			Currency:    bridge.CurrencyUSDC,
			PaymentRail: req.DestinationChain,
			Address:     req.WalletAddress,
		},
	}

	// Create virtual account via Bridge API
	bridgeVA, err := s.bridgeClient.CreateVirtualAccount(ctx, req.BridgeCustomerID, bridgeReq)
	if err != nil {
		s.logger.Error("Failed to create Bridge virtual account",
			"user_id", req.UserID,
			"error", err)
		return nil, fmt.Errorf("create bridge virtual account: %w", err)
	}

	// Convert to domain entity
	now := time.Now()
	virtualAccount := &entities.VirtualAccount{
		ID:              uuid.New(),
		UserID:          req.UserID,
		BridgeAccountID: &bridgeVA.ID,
		AccountNumber:   bridgeVA.SourceDepositInstructions.BankAccountNumber,
		RoutingNumber:   bridgeVA.SourceDepositInstructions.BankRoutingNumber,
		Status:          mapBridgeVAStatus(bridgeVA.Status),
		Currency:        req.Currency,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	// Handle IBAN for international accounts
	if bridgeVA.SourceDepositInstructions.IBAN != "" {
		virtualAccount.AccountNumber = bridgeVA.SourceDepositInstructions.IBAN
	}

	// Store in database
	if err := s.virtualAccountRepo.Create(ctx, virtualAccount); err != nil {
		s.logger.Error("Failed to store virtual account",
			"user_id", req.UserID,
			"bridge_account_id", bridgeVA.ID,
			"error", err)
		return nil, fmt.Errorf("store virtual account: %w", err)
	}

	s.logger.Info("Bridge virtual account created successfully",
		"user_id", req.UserID,
		"virtual_account_id", virtualAccount.ID,
		"bridge_account_id", bridgeVA.ID,
		"account_number", maskAccountNumber(virtualAccount.AccountNumber))

	return virtualAccount, nil
}

// ProcessFiatDeposit processes an incoming fiat deposit from Bridge webhook
// Implements the automatic 70/30 split
func (s *BridgeVirtualAccountService) ProcessFiatDeposit(ctx context.Context, event *BridgeFiatDepositEvent) error {
	s.logger.Info("Processing Bridge fiat deposit",
		"bridge_account_id", event.VirtualAccountID,
		"amount", event.Amount,
		"currency", event.Currency)

	virtualAccountID := strings.TrimSpace(event.VirtualAccountID)
	if virtualAccountID == "" {
		return fmt.Errorf("virtual_account_id is required")
	}
	transactionRef := strings.TrimSpace(event.TransactionRef)
	if transactionRef == "" {
		return fmt.Errorf("transaction_ref is required for idempotent fiat deposit processing")
	}

	// Parse amount
	amount, err := decimal.NewFromString(strings.TrimSpace(event.Amount))
	if err != nil {
		return fmt.Errorf("invalid amount %q: %w", event.Amount, err)
	}
	if !amount.GreaterThan(decimal.Zero) {
		return fmt.Errorf("amount must be greater than zero")
	}

	bridgeRepo, ok := s.virtualAccountRepo.(BridgeVirtualAccountRepository)
	if !ok {
		return fmt.Errorf("virtual account repository does not support bridge account lookup")
	}

	// Get virtual account to find the user from Bridge account ID.
	virtualAccount, err := bridgeRepo.GetByBridgeAccountID(ctx, virtualAccountID)
	if err != nil {
		return fmt.Errorf("get virtual account: %w", err)
	}

	if virtualAccount == nil {
		s.logger.Warn("Virtual account not found for Bridge deposit",
			"bridge_account_id", virtualAccountID)
		return fmt.Errorf("virtual account not found: %s", virtualAccountID)
	}

	// Generate UUID-based idempotency key
	idempotencyKey := generateFiatIdempotencyKey(transactionRef, virtualAccountID, amount.String())

	// Create deposit record FIRST with "pending" status to establish idempotency lock
	// This prevents race conditions - the unique constraint on idempotency_key will reject duplicates
	now := time.Now()
	depositID := uuid.New()
	virtAccountUUID := virtualAccount.ID

	deposit := &entities.Deposit{
		ID:               depositID,
		IdempotencyKey:   idempotencyKey,
		UserID:           virtualAccount.UserID,
		VirtualAccountID: &virtAccountUUID,
		Chain:            entities.ChainFiat,
		TxHash:           transactionRef,
		Token:            entities.StablecoinUSDC,
		Amount:           amount,
		Status:           "pending",
		CreatedAt:        now,
	}

	if s.depositRepo != nil {
		if err := s.depositRepo.Create(ctx, deposit); err != nil {
			// Check for duplicate key violation - this is expected for idempotent requests
			if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
				s.logger.Info("Fiat deposit already processed (idempotent duplicate key)",
					"idempotency_key", idempotencyKey)
				return nil
			}
			s.logger.Error("Failed to create fiat deposit record",
				"user_id", virtualAccount.UserID,
				"deposit_id", depositID,
				"error", err)
			return fmt.Errorf("failed to create deposit: %w", err)
		}
	}

	// Record deposit to ledger after deposit record is created
	if err := s.ledgerIntegration.RecordDeposit(ctx, virtualAccount.UserID, amount, depositID, "fiat", transactionRef); err != nil {
		s.logger.Error("Failed to record deposit in ledger, deleting deposit record",
			"user_id", virtualAccount.UserID,
			"amount", amount,
			"error", err)
		// Compensation: delete the deposit record since ledger failed
		if delErr := s.depositRepo.Delete(ctx, depositID); delErr != nil {
			s.logger.Error("CRITICAL: Failed to delete deposit record after ledger failure",
				"deposit_id", depositID,
				"error", delErr)
		}
		return fmt.Errorf("record deposit: %w", err)
	}

	// Update deposit status to "confirmed" after ledger success
	if s.depositRepo != nil {
		confirmedAt := now
		if err := s.depositRepo.UpdateStatus(ctx, depositID, "confirmed", &confirmedAt); err != nil {
			s.logger.Warn("Failed to update deposit status to confirmed",
				"deposit_id", depositID,
				"error", err)
		}
	}

	// Process 70/30 allocation split
	sourceTxID := transactionRef
	allocationReq := &entities.IncomingFundsRequest{
		UserID:     virtualAccount.UserID,
		Amount:     amount,
		EventType:  entities.AllocationEventTypeFiatDeposit,
		DepositID:  &depositID,
		SourceTxID: &sourceTxID,
		Metadata: map[string]any{
			"source":            "bridge_fiat",
			"bridge_account_id": virtualAccountID,
			"original_currency": event.Currency,
			"transaction_ref":   transactionRef,
		},
	}

	if err := s.allocationService.ProcessIncomingFunds(ctx, allocationReq); err != nil {
		s.logger.Error("Failed to process allocation split - marking as pending_allocation",
			"user_id", virtualAccount.UserID,
			"amount", amount,
			"error", err)

		// Update deposit status to pending_allocation to track incomplete allocation
		if s.depositRepo != nil {
			if updateErr := s.depositRepo.UpdateStatus(ctx, depositID, "pending_allocation", nil); updateErr != nil {
				s.logger.Error("Failed to update deposit status to pending_allocation",
					"deposit_id", depositID,
					"error", updateErr)
			}
		}

		// Log detailed error for internal debugging (not exposed to user)
		s.logger.Error("Allocation failure details for operations team",
			"user_id", virtualAccount.UserID,
			"deposit_id", depositID,
			"error_message", err.Error(),
			"error_type", fmt.Sprintf("%T", err))

		// Notify user with generic message (don't expose internal error details)
		if s.notificationService != nil {
			if notifyErr := s.notificationService.NotifyAllocationFailed(
				ctx,
				virtualAccount.UserID,
				amount,
				depositID,
				"allocation_failed",
			); notifyErr != nil {
				s.logger.Error("Failed to send allocation failure notification",
					"user_id", virtualAccount.UserID,
					"deposit_id", depositID,
					"error", notifyErr)
			}
		}

		// Return error to trigger webhook retry for transient failures
		return fmt.Errorf("allocation processing failed: %w", err)
	}

	if err := s.allocationService.ProcessIncomingFunds(ctx, allocationReq); err != nil {
		s.logger.Error("Failed to process allocation split",
			"user_id", virtualAccount.UserID,
			"amount", amount,
			"error", err)
		// Don't fail the deposit - allocation can be retried
		// The funds are safely in the ledger

		// Notify user about allocation failure so they can take action
		if s.notificationService != nil {
			if notifyErr := s.notificationService.NotifyAllocationFailed(
				ctx,
				virtualAccount.UserID,
				amount,
				depositID,
				err.Error(),
			); notifyErr != nil {
				s.logger.Error("Failed to send allocation failure notification",
					"user_id", virtualAccount.UserID,
					"deposit_id", depositID,
					"error", notifyErr)
			}
		}
	}

	// Send fiat deposit confirmation notification
	if s.notificationService != nil {
		if notifyErr := s.notificationService.NotifyDepositConfirmed(
			ctx,
			virtualAccount.UserID,
			amount.String(),
			"fiat",
			transactionRef,
		); notifyErr != nil {
			s.logger.Warn("Failed to send fiat deposit notification",
				"error", notifyErr,
				"user_id", virtualAccount.UserID)
		}
	}

	s.logger.Info("Bridge fiat deposit processed successfully",
		"user_id", virtualAccount.UserID,
		"amount", amount,
		"deposit_id", depositID)

	return nil
}

// reconcileFiatDeposit handles idempotent replay of fiat deposits
func (s *BridgeVirtualAccountService) reconcileFiatDeposit(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, depositID uuid.UUID, transactionRef, virtualAccountID, currency string) error {
	// Re-run allocation split for reconciliation
	sourceTxID := transactionRef
	allocationReq := &entities.IncomingFundsRequest{
		UserID:     userID,
		Amount:     amount,
		EventType:  entities.AllocationEventTypeFiatDeposit,
		DepositID:  &depositID,
		SourceTxID: &sourceTxID,
		Metadata: map[string]any{
			"source":            "bridge_fiat",
			"bridge_account_id": virtualAccountID,
			"original_currency": currency,
			"transaction_ref":   transactionRef,
		},
	}

	if err := s.allocationService.ProcessIncomingFunds(ctx, allocationReq); err != nil {
		s.logger.Error("Failed to reconcile allocation split",
			"user_id", userID,
			"deposit_id", depositID.String(),
			"error", err)
		// Don't fail - allocation service handles idempotency internally
	}

	return nil
}

// GetVirtualAccounts retrieves all virtual accounts for a user
func (s *BridgeVirtualAccountService) GetVirtualAccounts(ctx context.Context, userID uuid.UUID) ([]*entities.VirtualAccount, error) {
	return s.virtualAccountRepo.GetByUserID(ctx, userID)
}

// GetDepositInstructions retrieves deposit instructions for a virtual account
func (s *BridgeVirtualAccountService) GetDepositInstructions(ctx context.Context, userID uuid.UUID, currency string) (*DepositInstructions, error) {
	accounts, err := s.virtualAccountRepo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get virtual accounts: %w", err)
	}

	// Find account matching currency with Bridge provider
	for _, acc := range accounts {
		if acc.Currency == currency && acc.BridgeAccountID != nil {
			return &DepositInstructions{
				AccountNumber:   acc.AccountNumber,
				RoutingNumber:   acc.RoutingNumber,
				BankName:        "Bridge Partner Bank",
				BeneficiaryName: "RAIL User Account",
				Currency:        acc.Currency,
				Provider:        "bridge",
			}, nil
		}
	}

	return nil, fmt.Errorf("no %s virtual account found", currency)
}

// BridgeFiatDepositEvent represents a fiat deposit event from Bridge webhook
type BridgeFiatDepositEvent struct {
	VirtualAccountID string `json:"virtual_account_id"`
	Amount           string `json:"amount"`
	Currency         string `json:"currency"`
	TransactionRef   string `json:"transaction_ref"`
	Status           string `json:"status"`
}

// DepositInstructions contains bank details for fiat deposits
type DepositInstructions struct {
	AccountNumber   string `json:"account_number"`
	RoutingNumber   string `json:"routing_number,omitempty"`
	IBAN            string `json:"iban,omitempty"`
	BIC             string `json:"bic,omitempty"`
	BankName        string `json:"bank_name"`
	BeneficiaryName string `json:"beneficiary_name"`
	Currency        string `json:"currency"`
	Provider        string `json:"provider"`
}

// generateFiatIdempotencyKey creates a deterministic UUID for fiat deposits
func generateFiatIdempotencyKey(transactionRef, virtualAccountID, amount string) string {
	input := fmt.Sprintf("fiat-deposit:%s:%s:%s", strings.ToLower(transactionRef), strings.ToLower(virtualAccountID), strings.ToLower(amount))
	hash := sha256.Sum256([]byte(input))
	hashStr := fmt.Sprintf("%x", hash[:])
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(hashStr)).String()
}

// Helper functions

func mapBridgeVAStatus(status bridge.VirtualAccountStatus) entities.VirtualAccountStatus {
	switch status {
	case bridge.VirtualAccountStatusActivated:
		return entities.VirtualAccountStatusActive
	case bridge.VirtualAccountStatusDeactivated:
		return entities.VirtualAccountStatusClosed
	default:
		return entities.VirtualAccountStatusPending
	}
}

func maskAccountNumber(accountNumber string) string {
	if len(accountNumber) <= 4 {
		return "****"
	}
	return "****" + accountNumber[len(accountNumber)-4:]
}
