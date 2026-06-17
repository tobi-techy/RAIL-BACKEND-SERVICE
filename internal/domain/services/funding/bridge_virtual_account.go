package funding

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/bridge"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/rail-service/rail_service/pkg/metrics"
	"github.com/shopspring/decimal"
)

// BridgeVirtualAccountService handles Bridge virtual account operations for fiat funding
type BridgeVirtualAccountService struct {
	bridgeClient        bridge.BridgeClient
	virtualAccountRepo  VirtualAccountRepository
	depositRepo         DepositRepository
	allocationService   AllocationService
	complianceScreener  ComplianceScreener
	ledgerIntegration   LedgerIntegration
	notificationService FundingNotificationService
	gameplayHooks       FundingGameplayHooks
	walletProvider      WalletProvider
	validationService   *ValidationService
	logger              *logger.Logger
}

// WalletProvider interface for getting user wallet addresses
type WalletProvider interface {
	GetWalletByUserAndChain(ctx context.Context, userID uuid.UUID, chain entities.WalletChain) (*entities.ManagedWallet, error)
	CreateWalletForCustomer(ctx context.Context, customerID string, chain string) (*entities.ManagedWallet, error)
	SaveWallet(ctx context.Context, wallet *entities.ManagedWallet) error
	ListBridgeWallets(ctx context.Context, customerID string) ([]*entities.ManagedWallet, error)
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

// GetTOSLink returns the Bridge Terms of Service acceptance link for a customer
func (s *BridgeVirtualAccountService) GetTOSLink(ctx context.Context, bridgeCustomerID string) (string, error) {
	resp, err := s.bridgeClient.GetTOSLink(ctx, bridgeCustomerID)
	if err != nil {
		return "", fmt.Errorf("failed to get ToS link: %w", err)
	}
	return resp.URL, nil
}

// SetNotificationService sets the notification service for fiat deposit events
func (s *BridgeVirtualAccountService) SetNotificationService(notificationService FundingNotificationService) {
	s.notificationService = notificationService
}

// SetWalletProvider sets the wallet provider for getting user wallet addresses
func (s *BridgeVirtualAccountService) SetWalletProvider(walletProvider WalletProvider) {
	s.walletProvider = walletProvider
}

// SetComplianceScreener sets the compliance screening service (optional).
func (s *BridgeVirtualAccountService) SetComplianceScreener(cs ComplianceScreener) {
	s.complianceScreener = cs
}

// SetGameplayHooks sets the gameplay hooks for triggering XP/streak/challenge events.
func (s *BridgeVirtualAccountService) SetGameplayHooks(gh FundingGameplayHooks) {
	s.gameplayHooks = gh
}

// SetValidationService sets the validation service for deposit limit checks.
func (s *BridgeVirtualAccountService) SetValidationService(vs *ValidationService) {
	s.validationService = vs
}

// ProvisionVirtualAccounts creates virtual accounts for multiple currencies on KYC approval
func (s *BridgeVirtualAccountService) ProvisionVirtualAccounts(ctx context.Context, userID uuid.UUID, bridgeCustomerID string, currencies []string) error {
	if s.walletProvider == nil {
		return fmt.Errorf("wallet provider not configured")
	}

	// Try to find any available wallet for the USDC destination address.
	// Prefer mainnet Solana, fall back to devnet, then any EVM chain.
	walletChainsToTry := []entities.WalletChain{
		entities.WalletChainSolana,
		entities.WalletChainEthereum,
		entities.WalletChainPolygon,
		entities.WalletChainBase,
		entities.WalletChainAvalanche,
		entities.WalletChainCelo,
		entities.WalletChainArbitrum,
		entities.WalletChainOptimism,
	}

	var wallet *entities.ManagedWallet
	for _, chain := range walletChainsToTry {
		w, err := s.walletProvider.GetWalletByUserAndChain(ctx, userID, chain)
		if err == nil && w != nil && w.Address != "" {
			wallet = w
			break
		}
	}

	if wallet == nil {
		// No wallet in DB — check if one already exists on Bridge.
		bridgeWallets, err := s.walletProvider.ListBridgeWallets(ctx, bridgeCustomerID)
		if err == nil {
			for _, bw := range bridgeWallets {
				if bw.Address != "" {
					s.logger.Info("Found existing Bridge wallet, syncing to DB",
						"user_id", userID, "bridge_wallet_id", bw.BridgeWalletID, "chain", string(bw.Chain), "address", bw.Address)
					bw.UserID = userID
					if saveErr := s.walletProvider.SaveWallet(ctx, bw); saveErr != nil {
						s.logger.Error("Failed to save synced Bridge wallet", "error", saveErr)
						continue
					}
					wallet = bw
					break
				}
			}
		}
	}

	if wallet == nil {
		// No wallet on Bridge either — create one now.
		s.logger.Info("No wallet found on Bridge, creating new wallet",
			"user_id", userID, "bridge_customer_id", bridgeCustomerID)
		mw, err := s.walletProvider.CreateWalletForCustomer(ctx, bridgeCustomerID, string(entities.WalletChainSolana))
		if err != nil {
			// Creation may fail with a stale idempotency key (>24h) if the wallet
			// was already created on Bridge but never saved locally. Retry listing
			// as a recovery before giving up.
			s.logger.Warn("Wallet creation failed, retrying Bridge list as recovery",
				"error", err, "user_id", userID)
			retryWallets, listErr := s.walletProvider.ListBridgeWallets(ctx, bridgeCustomerID)
			if listErr == nil {
				for _, rw := range retryWallets {
					if rw.Address != "" {
						rw.UserID = userID
						if saveErr := s.walletProvider.SaveWallet(ctx, rw); saveErr == nil {
							mw = rw
							break
						}
					}
				}
			}
			if mw == nil {
				return fmt.Errorf("auto-provision wallet failed for user %s: %w", userID, err)
			}
		} else {
			mw.UserID = userID
			if err := s.walletProvider.SaveWallet(ctx, mw); err != nil {
				return fmt.Errorf("failed to save auto-provisioned wallet for user %s: %w", userID, err)
			}
		}
		wallet = mw
	}

	var (
		errs         []string
		successCount int
	)
	for _, currency := range currencies {
		// Check if account already exists for this currency
		existing, _ := s.virtualAccountRepo.GetActiveByUserIDAndCurrency(ctx, userID, currency)
		if existing != nil {
			s.logger.Info("Virtual account already exists",
				"user_id", userID,
				"currency", currency)
			successCount++
			continue
		}

		req := &CreateBridgeVirtualAccountRequest{
			UserID:           userID,
			BridgeCustomerID: bridgeCustomerID,
			Currency:         currency,
			DestinationChain: bridge.PaymentRailSolana,
			WalletAddress:    wallet.Address,
		}

		if _, err := s.CreateVirtualAccount(ctx, req); err != nil {
			s.logger.Error("Failed to provision virtual account",
				"user_id", userID,
				"currency", currency,
				"error", err)
			errs = append(errs, fmt.Sprintf("%s: %v", currency, err))
			continue
		}

		s.logger.Info("Provisioned virtual account",
			"user_id", userID,
			"currency", currency)
		successCount++
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to provision virtual accounts for %d of %d currencies: [%s]", len(errs), len(currencies), strings.Join(errs, "; "))
	}

	return nil
}

// CreateVirtualAccountRequest represents a request to create a Bridge virtual account
type CreateBridgeVirtualAccountRequest struct {
	UserID           uuid.UUID
	BridgeCustomerID string
	Currency         string // "USD" or "EUR"
	DestinationChain bridge.PaymentRail
	WalletAddress    string // Destination wallet for converted USDC
}

// DeveloperFeePercent is the fee charged on each deposit (0.5%)
const DeveloperFeePercent = "0.5"

// CreateVirtualAccount creates a new Bridge virtual account for fiat deposits
func (s *BridgeVirtualAccountService) CreateVirtualAccount(ctx context.Context, req *CreateBridgeVirtualAccountRequest) (*entities.VirtualAccount, error) {
	s.logger.Info("Creating Bridge virtual account",
		"user_id", req.UserID,
		"currency", req.Currency,
		"destination_chain", req.DestinationChain)

	// Validate wallet address
	if req.WalletAddress == "" {
		return nil, fmt.Errorf("wallet address is required")
	}

	// Determine source currency
	var sourceCurrency bridge.Currency
	var actualCurrency string
	switch strings.ToUpper(req.Currency) {
	case "EUR":
		sourceCurrency = bridge.CurrencyEUR
		actualCurrency = "EUR"
	case "USD":
		sourceCurrency = bridge.CurrencyUSD
		actualCurrency = "USD"
	case "GBP":
		sourceCurrency = bridge.CurrencyGBP
		actualCurrency = "GBP"
	case "MXN":
		sourceCurrency = bridge.CurrencyMXN
		actualCurrency = "MXN"
	case "BRL":
		sourceCurrency = bridge.CurrencyBRL
		actualCurrency = "BRL"
	default:
		return nil, fmt.Errorf("unsupported currency: %s (supported: USD, EUR, GBP, MXN, BRL)", req.Currency)
	}

	// Create Bridge virtual account request with 0.5% developer fee
	bridgeReq := &bridge.CreateVirtualAccountRequest{
		Source: bridge.VirtualAccountSource{
			Currency: sourceCurrency,
		},
		Destination: bridge.VirtualAccountDestination{
			Currency:    bridge.CurrencyUSDC,
			PaymentRail: req.DestinationChain,
			Address:     req.WalletAddress,
		},
		DeveloperFeePercent: DeveloperFeePercent,
	}

	// Create virtual account via Bridge API
	bridgeVA, err := s.bridgeClient.CreateVirtualAccount(ctx, req.BridgeCustomerID, bridgeReq)
	if err != nil {
		// If creation failed, check if VA already exists on Bridge for this currency
		s.logger.Warn("Bridge create VA failed, checking for existing",
			"user_id", req.UserID,
			"currency", req.Currency,
			"error", err)
		bridgeVA, err = s.findExistingBridgeVA(ctx, req.BridgeCustomerID, sourceCurrency)
		if err != nil {
			s.logger.Error("Failed to create or find Bridge virtual account",
				"user_id", req.UserID,
				"error", err)
			return nil, fmt.Errorf("create bridge virtual account: %w", err)
		}
	}

	// Convert to domain entity
	now := time.Now()
	virtualAccount := &entities.VirtualAccount{
		ID:               uuid.New(),
		UserID:           req.UserID,
		BridgeCustomerID: req.BridgeCustomerID,
		BridgeAccountID:  &bridgeVA.ID,
		AccountNumber:    bridgeVA.SourceDepositInstructions.BankAccountNumber,
		RoutingNumber:    bridgeVA.SourceDepositInstructions.BankRoutingNumber,
		BankName:         bridgeVA.SourceDepositInstructions.BankName,
		BeneficiaryName:  bridgeVA.SourceDepositInstructions.BankBeneficiaryName,
		BankAddress:      bridgeVA.SourceDepositInstructions.BankAddress,
		BeneficiaryAddr:  bridgeVA.SourceDepositInstructions.BankBeneficiaryAddress,
		PaymentRails:     pq.StringArray(bridgeVA.SourceDepositInstructions.PaymentRails),
		Status:           mapBridgeVAStatus(bridgeVA.Status),
		Currency:         actualCurrency,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	// Handle IBAN/BIC for EUR (SEPA) accounts
	if bridgeVA.SourceDepositInstructions.IBAN != "" {
		virtualAccount.AccountNumber = bridgeVA.SourceDepositInstructions.IBAN
		virtualAccount.RoutingNumber = bridgeVA.SourceDepositInstructions.BIC
	}

	// Handle GBP (Faster Payments): sort_code -> RoutingNumber, account_number -> AccountNumber
	if bridgeVA.SourceDepositInstructions.SortCode != "" {
		virtualAccount.AccountNumber = bridgeVA.SourceDepositInstructions.AccountNumber
		virtualAccount.RoutingNumber = bridgeVA.SourceDepositInstructions.SortCode
	}

	// Handle MXN (SPEI): clabe -> AccountNumber
	if bridgeVA.SourceDepositInstructions.CLABE != "" {
		virtualAccount.AccountNumber = bridgeVA.SourceDepositInstructions.CLABE
	}

	// Handle BRL (PIX): br_code -> AccountNumber
	if bridgeVA.SourceDepositInstructions.BRCode != "" {
		virtualAccount.AccountNumber = bridgeVA.SourceDepositInstructions.BRCode
	}

	// Use account_holder_name as beneficiary if bank_beneficiary_name is empty
	if virtualAccount.BeneficiaryName == "" && bridgeVA.SourceDepositInstructions.AccountHolderName != "" {
		virtualAccount.BeneficiaryName = bridgeVA.SourceDepositInstructions.AccountHolderName
	}

	// Store in database
	if err := s.virtualAccountRepo.Create(ctx, virtualAccount); err != nil {
		// If duplicate (VA already in our DB from a previous partial attempt), return existing
		if strings.Contains(err.Error(), "duplicate key") {
			existing, fetchErr := s.virtualAccountRepo.GetActiveByUserIDAndCurrency(ctx, req.UserID, actualCurrency)
			if fetchErr == nil && existing != nil {
				s.logger.Info("Virtual account already exists in DB, returning existing",
					"user_id", req.UserID,
					"currency", actualCurrency,
					"virtual_account_id", existing.ID)
				return existing, nil
			}
		}
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
// Implements the automatic 70/30 split atomically to prevent race conditions
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

	// Validate minimum deposit amount
	if s.validationService != nil {
		if err := s.validationService.ValidateDepositAmount(amount); err != nil {
			return fmt.Errorf("fiat deposit validation failed: %w", err)
		}
	} else if amount.LessThan(decimal.NewFromFloat(0.01)) {
		return fmt.Errorf("deposit amount %s is below minimum", amount.String())
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

	// Fix #5: Idempotency key uses only transaction_ref + virtual_account_id (not amount)
	// This prevents double-crediting if Bridge sends the same transaction_ref with different amounts
	idempotencyKey := generateFiatIdempotencyKey(transactionRef, virtualAccountID, "")

	// Validate daily deposit limit
	if s.validationService != nil {
		if err := s.validationService.ValidateDailyDepositLimit(ctx, virtualAccount.UserID, amount); err != nil {
			return fmt.Errorf("fiat deposit limit exceeded: %w", err)
		}
	}

	// Compliance screening — submit to Didit for AML/sanctions monitoring
	if s.complianceScreener != nil {
		screenStatus, screenErr := s.complianceScreener.ScreenTransaction(ctx, virtualAccount.UserID, transactionRef, "inbound", amount, event.Currency, "")
		if screenErr != nil {
			s.logger.Error("Compliance screening unavailable, blocking fiat deposit",
				"user_id", virtualAccount.UserID.String(), "ref", transactionRef, "error", screenErr)
			return fmt.Errorf("deposit held: compliance screening unavailable")
		}
		if screenStatus != "APPROVED" {
			s.logger.Warn("Fiat deposit not approved by compliance",
				"user_id", virtualAccount.UserID.String(), "ref", transactionRef, "status", screenStatus)
			return fmt.Errorf("deposit held: compliance status %s", screenStatus)
		}
	}

	// Create deposit record with "pending" status to establish idempotency lock
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
		CreatedAt:        time.Now(),
	}

	if s.depositRepo != nil {
		if err := s.depositRepo.Create(ctx, deposit); err != nil {
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

	// Fix #1 + #6: Atomically record deposit AND allocate to spending/stash in one ledger transaction.
	// This prevents the race where funds sit in usdc_balance and are spendable before allocation.
	// Calculate the 70/30 split amounts upfront.
	spendingRatio := decimal.NewFromFloat(0.70)
	spendingAmount := amount.Mul(spendingRatio)
	stashAmount := amount.Sub(spendingAmount) // remainder avoids precision drift

	if err := s.ledgerIntegration.RecordDepositWithAllocation(
		ctx, virtualAccount.UserID, amount, depositID, "fiat", transactionRef,
		spendingAmount, stashAmount,
	); err != nil {
		s.logger.Error("Failed to record deposit with allocation in ledger",
			"user_id", virtualAccount.UserID,
			"amount", amount,
			"error", err)
		// Fix #2: Clean up deposit record since the atomic ledger op failed
		if s.depositRepo != nil {
			if delErr := s.depositRepo.DeletePendingDeposit(ctx, depositID); delErr != nil {
				s.logger.Error("CRITICAL: Failed to delete deposit after ledger failure",
					"deposit_id", depositID, "deletion_error", delErr)
				if statusErr := s.depositRepo.UpdateStatus(ctx, depositID, "compensation_failed", nil); statusErr != nil {
					s.logger.Error("CRITICAL: Failed to mark deposit as compensation_failed",
						"deposit_id", depositID, "status_error", statusErr)
				}
			}
		}
		return fmt.Errorf("record deposit with allocation: %w", err)
	}

	// Fix #7: Use current time as confirmedAt, reflecting actual confirmation moment
	if s.depositRepo != nil {
		confirmedAt := time.Now()
		if err := s.depositRepo.UpdateStatus(ctx, depositID, "confirmed", &confirmedAt); err != nil {
			s.logger.Warn("Failed to update deposit status to confirmed",
				"deposit_id", depositID,
				"error", err)
		}
	}

	if metrics.Business != nil {
		metrics.Business.DepositsCompleted.WithLabelValues("fiat").Inc()
		metrics.Business.DepositAmount.WithLabelValues("fiat").Observe(amount.InexactFloat64())
	}

	// The allocation already happened atomically in the ledger transaction above.
	// Now call ProcessIncomingFunds for the side-effects only (event recording, yield routing,
	// auto-invest, notifications). The allocation service detects the ledger transaction already
	// exists via its idempotency key and skips the ledger transfer.
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
			"atomic_split":      true, // signal that ledger split already done
		},
	}

	if err := s.allocationService.ProcessIncomingFunds(ctx, allocationReq); err != nil {
		// Non-fatal: the ledger split already happened atomically.
		// Allocation side-effects (yield routing, auto-invest) will be retried by recovery worker.
		s.logger.Warn("Allocation side-effects failed (ledger split already committed)",
			"user_id", virtualAccount.UserID,
			"deposit_id", depositID,
			"error", err)
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

	// Trigger gameplay events (XP, streaks, challenges)
	if s.gameplayHooks != nil {
		s.gameplayHooks.OnDeposit(ctx, virtualAccount.UserID, amount, depositID)
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
				BankName:        acc.BankName,
				BeneficiaryName: acc.BeneficiaryName,
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
	CustomerID       string `json:"customer_id"` // Bridge customer ID (for logging/audit)
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
// mapBridgeChain converts a Bridge chain string to the Rail Chain constant.
func mapBridgeChain(chain string) entities.Chain {
	switch strings.ToLower(chain) {
	case "solana":
		return entities.ChainSOL
	case "ethereum":
		return entities.ChainETH
	case "polygon":
		return entities.ChainMATIC
	case "base":
		return entities.ChainBase
	case "avalanche", "avalanche_c_chain":
		return entities.ChainAvalanche
	case "arbitrum":
		return entities.ChainArbitrum
	case "optimism":
		return entities.ChainOptimism
	case "celo":
		return entities.ChainCELO
	default:
		return entities.ChainSOL
	}
}

func generateFiatIdempotencyKey(transactionRef, virtualAccountID, _ string) string {
	// Fix #5: Idempotency key uses only transaction_ref + virtual_account_id.
	// Amount is excluded so that if Bridge sends the same transaction_ref with a different
	// amount (e.g., adjustment), it's still detected as a duplicate rather than creating
	// a second credit.
	input := fmt.Sprintf("fiat-deposit:%s:%s", strings.ToLower(transactionRef), strings.ToLower(virtualAccountID))
	hash := sha256.Sum256([]byte(input))
	hashStr := fmt.Sprintf("%x", hash[:])
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(hashStr)).String()
}

// ProcessCryptoDeposit processes an incoming crypto deposit from a Bridge liquidation address
// and triggers the 70/30 allocation split.
func (s *BridgeVirtualAccountService) ProcessCryptoDeposit(ctx context.Context, userID uuid.UUID, transferID string, amount decimal.Decimal, chain string) error {
	if !amount.GreaterThan(decimal.Zero) {
		return fmt.Errorf("amount must be greater than zero")
	}

	idempotencyKey := generateFiatIdempotencyKey(transferID, userID.String(), amount.String())

	now := time.Now()
	depositID := uuid.New()

	// Compliance screening
	if s.complianceScreener != nil {
		screenStatus, screenErr := s.complianceScreener.ScreenTransaction(ctx, userID, transferID, "inbound", amount, "USDC", "")
		if screenErr != nil {
			s.logger.Error("Compliance screening unavailable, blocking crypto deposit",
				"user_id", userID.String(), "transfer_id", transferID, "error", screenErr)
			return fmt.Errorf("deposit held: compliance screening unavailable")
		}
		if screenStatus != "APPROVED" {
			s.logger.Warn("Crypto deposit not approved by compliance",
				"user_id", userID.String(), "transfer_id", transferID, "status", screenStatus)
			return fmt.Errorf("deposit held: compliance status %s", screenStatus)
		}
	}

	deposit := &entities.Deposit{
		ID:             depositID,
		IdempotencyKey: idempotencyKey,
		UserID:         userID,
		Chain:          mapBridgeChain(chain),
		TxHash:         transferID,
		Token:          entities.StablecoinUSDC,
		Amount:         amount,
		Status:         "pending",
		CreatedAt:      now,
	}

	if s.depositRepo != nil {
		if err := s.depositRepo.Create(ctx, deposit); err != nil {
			if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
				s.logger.Info("Crypto deposit already processed (idempotent)", "transfer_id", transferID)
				return nil
			}
			return fmt.Errorf("create deposit: %w", err)
		}
	}

	if err := s.ledgerIntegration.RecordDeposit(ctx, userID, amount, depositID, "crypto", transferID); err != nil {
		if s.depositRepo != nil {
			if delErr := s.depositRepo.DeletePendingDeposit(ctx, depositID); delErr != nil {
				s.logger.Error("Failed to delete pending deposit after ledger failure", "deposit_id", depositID, "error", delErr)
			}
		}
		return fmt.Errorf("record deposit: %w", err)
	}

	if s.depositRepo != nil {
		confirmedAt := now
		if err := s.depositRepo.UpdateStatus(ctx, depositID, "confirmed", &confirmedAt); err != nil {
			s.logger.Error("Failed to update deposit status to confirmed", "deposit_id", depositID, "error", err)
		}
	}

	// Notify user that deposit was received
	if s.notificationService != nil {
		if err := s.notificationService.NotifyDepositConfirmed(ctx, userID, amount.String(), chain, transferID); err != nil {
			s.logger.Warn("Failed to send deposit confirmation notification", "user_id", userID, "error", err)
		}
	}

	// Trigger gameplay events (XP, streaks, challenges)
	if s.gameplayHooks != nil {
		s.gameplayHooks.OnDeposit(ctx, userID, amount, depositID)
	}

	sourceTxID := transferID
	if err := s.allocationService.ProcessIncomingFunds(ctx, &entities.IncomingFundsRequest{
		UserID:     userID,
		Amount:     amount,
		EventType:  entities.AllocationEventTypeCryptoDeposit,
		DepositID:  &depositID,
		SourceTxID: &sourceTxID,
		Metadata: map[string]any{
			"source":      "bridge_liquidation_address",
			"transfer_id": transferID,
		},
	}); err != nil {
		if s.depositRepo != nil {
			if statusErr := s.depositRepo.UpdateStatus(ctx, depositID, "pending_allocation", nil); statusErr != nil {
				s.logger.Error("Failed to update deposit status to pending_allocation", "deposit_id", depositID, "error", statusErr)
			}
		}
		return fmt.Errorf("process allocation: %w", err)
	}

	return nil
}

// Helper functions

// findExistingBridgeVA lists a customer's virtual accounts on Bridge and returns one matching the currency
func (s *BridgeVirtualAccountService) findExistingBridgeVA(ctx context.Context, bridgeCustomerID string, currency bridge.Currency) (*bridge.VirtualAccount, error) {
	resp, err := s.bridgeClient.ListVirtualAccounts(ctx, bridgeCustomerID)
	if err != nil {
		return nil, fmt.Errorf("list bridge virtual accounts: %w", err)
	}
	for i := range resp.Data {
		if resp.Data[i].SourceDepositInstructions.Currency == currency {
			s.logger.Info("Found existing Bridge VA for currency",
				"bridge_account_id", resp.Data[i].ID,
				"currency", currency)
			return &resp.Data[i], nil
		}
	}
	return nil, fmt.Errorf("no existing bridge virtual account found for currency %s", currency)
}

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
