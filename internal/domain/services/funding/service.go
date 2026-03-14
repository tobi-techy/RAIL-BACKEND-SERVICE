package funding

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/shopspring/decimal"
)

// generateIdempotencyKey creates a deterministic UUID from deposit attributes
// This ensures uniqueness across chains and prevents cross-chain replay attacks
func generateIdempotencyKey(chain, token, amount, txHash string) string {
	// Normalize inputs
	normalizedChain := strings.ToLower(strings.TrimSpace(chain))
	normalizedToken := strings.ToLower(strings.TrimSpace(token))
	normalizedAmount := strings.ToLower(strings.TrimSpace(amount))
	normalizedTxHash := strings.ToLower(strings.TrimSpace(txHash))

	// Create deterministic input string
	input := fmt.Sprintf("crypto-deposit:%s:%s:%s:%s", normalizedChain, normalizedToken, normalizedAmount, normalizedTxHash)

	// Generate SHA256 hash
	hash := sha256.Sum256([]byte(input))
	hashStr := fmt.Sprintf("%x", hash[:])

	// Create UUID from hash (using SHA1 as per UUID v5 spec for name-based UUIDs)
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(hashStr)).String()
}

// generateCorrelationID creates a unique correlation ID for tracing a deposit through the system
func generateCorrelationID() string {
	return uuid.New().String()
}

// LedgerBalanceView represents user balance from ledger
type LedgerBalanceView struct {
	USDCBalance       decimal.Decimal
	FiatExposure      decimal.Decimal
	PendingInvestment decimal.Decimal
	TotalValue        decimal.Decimal
}

// LedgerIntegration interface for ledger operations
type LedgerIntegration interface {
	RecordDeposit(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, depositID uuid.UUID, chain, txHash string) error
	CompensateDeposit(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, depositID uuid.UUID) error
	GetUserBalance(ctx context.Context, userID uuid.UUID) (*LedgerBalanceView, error)
}

// LimitsService interface for transaction limit validation
type LimitsService interface {
	ValidateDeposit(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) (*entities.LimitCheckResult, error)
	RecordDeposit(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error
}

// CacheClient interface for caching operations
type CacheClient interface {
	Get(ctx context.Context, key string, dest interface{}) error
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error
	Delete(ctx context.Context, key string) error
}

// AuditService interface for compliance audit logging
type AuditService interface {
	LogDeposit(ctx context.Context, userID uuid.UUID, depositID uuid.UUID, amount string, chain string, status string) error
}

// FundingNotificationService interface for sending funding-related notifications
type FundingNotificationService interface {
	NotifyDepositConfirmed(ctx context.Context, userID uuid.UUID, amount, chain, txHash string) error
	NotifyLargeBalanceChange(ctx context.Context, userID uuid.UUID, changeType string, amount decimal.Decimal, newBalance decimal.Decimal) error
	NotifyAllocationFailed(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, depositID uuid.UUID, reason string) error
}

// Service handles funding operations - deposit addresses, confirmations, balance conversion
type Service struct {
	depositRepo         DepositRepository
	walletRepo          WalletRepository
	managedWalletRepo   ManagedWalletRepository
	virtualAccountRepo  VirtualAccountRepository
	userRepo            UserRepository
	alpacaAccountLookup AlpacaAccountLookup
	bridgeWallets       BridgeDepositClient
	bridgeVAService     *BridgeVirtualAccountService
	alpacaAPI           AlpacaAdapter
	ledgerIntegration   LedgerIntegration
	limitsService       LimitsService
	validationService   *ValidationService
	auditService        AuditService
	notificationService FundingNotificationService
	allocationService   AllocationService
	cache               CacheClient
	config              *FundingConfig
	logger              *logger.Logger
}

// DepositRepository interface for deposit persistence
type DepositRepository interface {
	Create(ctx context.Context, deposit *entities.Deposit) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.Deposit, error)
	GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.Deposit, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status string, confirmedAt *time.Time) error
	GetByTxHash(ctx context.Context, txHash string) (*entities.Deposit, error)
	GetByIdempotencyKey(ctx context.Context, idempotencyKey string) (*entities.Deposit, error)
	DeletePendingDeposit(ctx context.Context, id uuid.UUID) error
}

// WalletRepository interface for wallet operations
type WalletRepository interface {
	GetByUserAndChain(ctx context.Context, userID uuid.UUID, chain entities.Chain) (*entities.Wallet, error)
	GetByAddress(ctx context.Context, address string) (*entities.Wallet, error)
	Create(ctx context.Context, wallet *entities.Wallet) error
}

// ManagedWalletRepository interface for managed wallet operations
type ManagedWalletRepository interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.ManagedWallet, error)
	GetByAddress(ctx context.Context, address string) (*entities.ManagedWallet, error)
	Create(ctx context.Context, wallet *entities.ManagedWallet) error
}

// BridgeDepositClient handles Bridge wallet and liquidation address creation
type BridgeDepositClient interface {
	IsSandbox() bool
	ListWallets(ctx context.Context, customerID string) ([]BridgeWalletInfo, error)
	CreateWallet(ctx context.Context, customerID string, chain string) (id string, address string, err error)
	ListLiquidationAddresses(ctx context.Context, customerID string) ([]BridgeLiquidationAddr, error)
	CreateLiquidationAddress(ctx context.Context, customerID string, sourceChain string, destinationChain string, destinationAddress string) (id string, address string, err error)
}

// BridgeWalletInfo is a minimal wallet summary from Bridge
type BridgeWalletInfo struct {
	ID    string
	Chain string
}

// BridgeLiquidationAddr is a minimal liquidation address summary from Bridge
type BridgeLiquidationAddr struct {
	ID       string
	Chain    string
	Currency string
	Address  string
}

// VirtualAccountRepository interface for virtual account persistence
type VirtualAccountRepository interface {
	Create(ctx context.Context, account *entities.VirtualAccount) error
	Update(ctx context.Context, account *entities.VirtualAccount) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.VirtualAccount, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.VirtualAccount, error)
	GetByAlpacaAccountID(ctx context.Context, alpacaAccountID string) (*entities.VirtualAccount, error)
	GetActiveByUserIDAndCurrency(ctx context.Context, userID uuid.UUID, currency string) (*entities.VirtualAccount, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status entities.VirtualAccountStatus) error
	ExistsByUserAndAlpacaAccount(ctx context.Context, userID uuid.UUID, alpacaAccountID string) (bool, error)
}

// AlpacaAdapter interface for Alpaca API integration
type AlpacaAdapter interface {
	GetAccount(ctx context.Context, accountID string) (*entities.AlpacaAccountResponse, error)
	InitiateInstantFunding(ctx context.Context, req *entities.AlpacaInstantFundingRequest) (*entities.AlpacaInstantFundingResponse, error)
	GetInstantFundingStatus(ctx context.Context, transferID string) (*entities.AlpacaInstantFundingResponse, error)
	GetAccountBalance(ctx context.Context, accountID string) (*entities.AlpacaAccountResponse, error)
	CreateJournal(ctx context.Context, req *entities.AlpacaJournalRequest) (*entities.AlpacaJournalResponse, error)
}

// UserRepository for looking up Bridge customer ID
type UserRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*entities.UserProfile, error)
}

// AlpacaAccountLookup resolves persisted account ownership.
type AlpacaAccountLookup interface {
	GetByAlpacaID(ctx context.Context, alpacaAccountID string) (*entities.AlpacaAccount, error)
}

// NewService creates a new funding service
func NewService(
	depositRepo DepositRepository,
	walletRepo WalletRepository,
	managedWalletRepo ManagedWalletRepository,
	virtualAccountRepo VirtualAccountRepository,
	alpacaAPI AlpacaAdapter,
	ledgerIntegration LedgerIntegration,
	logger *logger.Logger,
) *Service {
	return &Service{
		depositRepo:        depositRepo,
		walletRepo:         walletRepo,
		managedWalletRepo:  managedWalletRepo,
		virtualAccountRepo: virtualAccountRepo,
		alpacaAPI:          alpacaAPI,
		ledgerIntegration:  ledgerIntegration,
		config:             DefaultFundingConfig(),
		logger:             logger,
	}
}

// SetBridgeDepositClient sets the Bridge deposit client (replaces Circle for deposit addresses)
func (s *Service) SetBridgeDepositClient(b BridgeDepositClient) { s.bridgeWallets = b }

// SetUserRepo sets the user repository for Bridge customer ID lookup
func (s *Service) SetUserRepo(r UserRepository) { s.userRepo = r }

// SetValidationService sets the validation service (optional)
func (s *Service) SetValidationService(vs *ValidationService) {
	s.validationService = vs
}

// GetValidationService returns the validation service
func (s *Service) GetValidationService() *ValidationService {
	return s.validationService
}

// SetLimitsService sets the limits service for deposit/withdrawal validation (optional)
func (s *Service) SetLimitsService(ls LimitsService) {
	s.limitsService = ls
}

// SetCache sets the cache client (optional)
func (s *Service) SetCache(cache CacheClient) {
	s.cache = cache
}

// SetAuditService sets the audit service for compliance logging (optional)
func (s *Service) SetAuditService(as AuditService) {
	s.auditService = as
}

// SetDefaultWalletSetID sets the default wallet set ID for wallet creation
func (s *Service) SetDefaultWalletSetID(id uuid.UUID) {
	s.config.DefaultWalletSetID = id
}

// SetNotificationService sets the notification service (optional)
func (s *Service) SetNotificationService(ns FundingNotificationService) {
	s.notificationService = ns
}

// SetBridgeVAService sets the Bridge virtual account service (optional)
func (s *Service) SetBridgeVAService(bva *BridgeVirtualAccountService) {
	s.bridgeVAService = bva
}

// GetVirtualAccounts retrieves all virtual accounts for a user
func (s *Service) GetVirtualAccounts(ctx context.Context, userID uuid.UUID) ([]*entities.VirtualAccount, error) {
	if s.bridgeVAService == nil {
		return nil, fmt.Errorf("virtual account service not configured")
	}
	return s.bridgeVAService.GetVirtualAccounts(ctx, userID)
}

// GetTOSLink returns the Bridge ToS acceptance link for a customer
func (s *Service) GetTOSLink(ctx context.Context, bridgeCustomerID string) (string, error) {
	if s.bridgeVAService == nil {
		return "", fmt.Errorf("virtual account service not configured")
	}
	return s.bridgeVAService.GetTOSLink(ctx, bridgeCustomerID)
}

// SetAlpacaAccountLookup sets the alpaca account ownership lookup service (optional).
func (s *Service) SetAlpacaAccountLookup(lookup AlpacaAccountLookup) {
	s.alpacaAccountLookup = lookup
}

// SetAllocationService sets the allocation service for automatic 70/30 split (optional)
func (s *Service) SetAllocationService(as AllocationService) {
	s.allocationService = as
}

// CreateDepositAddress generates or retrieves deposit address for a chain
func (s *Service) CreateDepositAddress(ctx context.Context, userID uuid.UUID, chain entities.Chain) (*entities.DepositAddressResponse, error) {
	// Check rate limit for new address creation
	if s.validationService != nil {
		if err := s.validationService.CheckDepositRateLimit(ctx, userID); err != nil {
			return nil, err
		}
	}

	// Check if user already has a wallet for this chain in the legacy wallets table.
	wallet, err := s.walletRepo.GetByUserAndChain(ctx, userID, chain)
	if err == nil && wallet != nil {
		s.logger.Info("Using existing wallet address", "user_id", userID, "chain", chain, "address", wallet.Address)
		return &entities.DepositAddressResponse{
			Chain:   chain,
			Address: wallet.Address,
		}, nil
	}

	// Check managed_wallets (custody wallets + liquidation addresses) — these are the
	// primary wallet type for testnet chains like MATIC-AMOY, AVAX-FUJI, SOL-DEVNET.
	if s.managedWalletRepo != nil {
		managedWallets, mErr := s.managedWalletRepo.GetByUserID(ctx, userID)
		if mErr == nil {
			for _, mw := range managedWallets {
				if matchesManagedWalletChain(mw.Chain, chain) && !strings.HasPrefix(mw.Address, "0xdeadbeef") {
					s.logger.Info("Using existing managed wallet address",
						"user_id", userID, "chain", chain,
						"managed_chain", mw.Chain, "address", mw.Address)
					return &entities.DepositAddressResponse{
						Chain:   chain,
						Address: mw.Address,
					}, nil
				}
			}
		}
	}

	// Create deposit address via Bridge (Bridge Wallet + Liquidation Address)
	if s.bridgeWallets == nil || s.userRepo == nil {
		return nil, fmt.Errorf("Bridge deposit client not configured")
	}

	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil || user.BridgeCustomerID == nil || *user.BridgeCustomerID == "" {
		return nil, fmt.Errorf("user has no Bridge customer ID — complete onboarding first")
	}
	customerID := *user.BridgeCustomerID
	walletChain := entities.WalletChain(chain)
	bridgeRail := walletChain.ToBridgePaymentRail()

	// Step 1: ensure custody wallet exists (skip in sandbox — Bridge Wallets not available)
	custodyChain := "solana"
	var bridgeWalletID string
	var destinationAddress string // used in sandbox instead of bridge_wallet_id

	if s.bridgeWallets.IsSandbox() {
		// Sandbox: Bridge Wallet API unavailable — use chain-appropriate destination address
		isEVM := bridgeRail == "polygon" || bridgeRail == "avalanche_c_chain" || bridgeRail == "ethereum" || bridgeRail == "base"
		if isEVM {
			destinationAddress = "0x3e1837fcc9796e6b9f32435af594970aba2d57ea" // test EVM address
		} else {
			destinationAddress = s.config.PlatformSolanaAddress
			if destinationAddress == "" {
				destinationAddress = "9kV3ZMehKVyxfHKCcaDLye3P9HHw2MP4jtQa2gKBUmCs" // fallback test address
			}
		}
	} else {
		wallets, err := s.bridgeWallets.ListWallets(ctx, customerID)
		if err != nil {
			return nil, fmt.Errorf("failed to list Bridge wallets: %w", err)
		}
		for _, w := range wallets {
			if w.Chain == custodyChain {
				bridgeWalletID = w.ID
				break
			}
		}
		if bridgeWalletID == "" {
			id, _, err := s.bridgeWallets.CreateWallet(ctx, customerID, custodyChain)
			if err != nil {
				return nil, fmt.Errorf("failed to create Bridge custody wallet: %w", err)
			}
			bridgeWalletID = id
		}
	}

	// Step 2: ensure liquidation address exists for the requested chain
	las, err := s.bridgeWallets.ListLiquidationAddresses(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("failed to list liquidation addresses: %w", err)
	}
	var laAddress string
	var laID string
	for _, la := range las {
		if la.Chain == bridgeRail && la.Currency == "usdc" && !strings.HasPrefix(la.Address, "0xdeadbeef") {
			laAddress = la.Address
			laID = la.ID
			break
		}
	}
	if laAddress == "" {
		laID, laAddress, err = s.bridgeWallets.CreateLiquidationAddress(ctx, customerID, bridgeRail, bridgeRail, destinationAddress)
		if err != nil {
			return nil, fmt.Errorf("failed to create liquidation address: %w", err)
		}
		// Persist
		mw := &entities.ManagedWallet{
			ID:             uuid.New(),
			UserID:         userID,
			Chain:          walletChain,
			Address:        laAddress,
			BridgeWalletID: laID,
			AccountType:    entities.AccountTypeLiquidationAddr,
			Status:         "live",
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}
		if err := s.managedWalletRepo.Create(ctx, mw); err != nil {
			s.logger.Warn("Failed to persist liquidation address", "error", err)
		}
	}
	_ = laID

	s.logger.Info("Deposit address ready", "user_id", userID, "chain", chain, "address", laAddress)

	return &entities.DepositAddressResponse{
		Chain:   chain,
		Address: laAddress,
	}, nil
}

// matchesManagedWalletChain checks if a managed wallet's chain matches the requested deposit chain.
func matchesManagedWalletChain(walletChain entities.WalletChain, depositChain entities.Chain) bool {
	switch depositChain {
	case entities.ChainMATIC, entities.ChainMATICAmoy:
		return walletChain == entities.WalletChainMATICAmoy || walletChain == entities.WalletChainPolygon
	case entities.ChainAVAX, entities.ChainAVAXFuji:
		return walletChain == entities.WalletChainAVAXFuji || walletChain == entities.WalletChainAvalanche
	case entities.ChainSOL, entities.ChainSOLDevnet:
		return walletChain == entities.WalletChainSOLDevnet || walletChain == entities.WalletChainSolana
	case entities.ChainBASE, entities.ChainBASESepolia:
		return walletChain == entities.WalletChainBase || walletChain == entities.WalletChainBASESepolia
	default:
		return string(walletChain) == string(depositChain)
	}
}

// GetFundingConfirmations retrieves recent funding confirmations for user
func (s *Service) GetFundingConfirmations(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.FundingConfirmation, error) {
	deposits, err := s.depositRepo.GetByUserID(ctx, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get deposits: %w", err)
	}

	confirmations := make([]*entities.FundingConfirmation, len(deposits))
	for i, deposit := range deposits {
		var confirmedAt time.Time
		if deposit.ConfirmedAt != nil {
			confirmedAt = *deposit.ConfirmedAt
		} else {
			confirmedAt = deposit.CreatedAt
		}
		confirmations[i] = &entities.FundingConfirmation{
			ID:             deposit.ID,
			IdempotencyKey: deposit.IdempotencyKey,
			Chain:          deposit.Chain,
			TxHash:         deposit.TxHash,
			Token:          deposit.Token,
			Amount:         deposit.Amount.String(),
			Status:         deposit.Status,
			ConfirmedAt:    confirmedAt,
		}
	}

	return confirmations, nil
}

// GetFundingConfirmationByID retrieves a single funding confirmation by deposit ID.
func (s *Service) GetFundingConfirmationByID(ctx context.Context, userID, depositID uuid.UUID) (*entities.FundingConfirmation, error) {
	deposit, err := s.depositRepo.GetByID(ctx, depositID)
	if err != nil {
		return nil, fmt.Errorf("failed to get deposit: %w", err)
	}
	if deposit == nil || deposit.UserID != userID {
		return nil, fmt.Errorf("deposit not found")
	}

	confirmedAt := deposit.CreatedAt
	if deposit.ConfirmedAt != nil {
		confirmedAt = *deposit.ConfirmedAt
	}

	return &entities.FundingConfirmation{
		ID:             deposit.ID,
		IdempotencyKey: deposit.IdempotencyKey,
		Chain:          deposit.Chain,
		TxHash:         deposit.TxHash,
		Token:          deposit.Token,
		Amount:         deposit.Amount.String(),
		Status:         deposit.Status,
		ConfirmedAt:    confirmedAt,
	}, nil
}

// GetBalance returns user's current balance from ledger with caching
func (s *Service) GetBalance(ctx context.Context, userID uuid.UUID) (*entities.BalancesResponse, error) {
	// Try cache first
	if s.cache != nil {
		var cached entities.BalancesResponse
		cacheKey := BalanceCacheKey(userID)
		if err := s.cache.Get(ctx, cacheKey, &cached); err == nil {
			s.logger.Debug("Balance retrieved from cache", "user_id", userID.String())
			return &cached, nil
		}
	}

	s.logger.Info("Fetching user balance from ledger", "user_id", userID.String())

	// Get balance from ledger integration
	ledgerBalance, err := s.ledgerIntegration.GetUserBalance(ctx, userID)
	if err != nil {
		s.logger.Error("Failed to get ledger balance", "error", err, "user_id", userID.String())
		return &entities.BalancesResponse{
			BuyingPower:     "0.00",
			PendingDeposits: "0.00",
			Currency:        "USD",
		}, nil
	}

	// USDC balance is available funds, fiat exposure is buying power at broker
	// Total buying power = USDC + fiat exposure
	totalBuyingPower := ledgerBalance.USDCBalance.Add(ledgerBalance.FiatExposure)

	response := &entities.BalancesResponse{
		BuyingPower:     totalBuyingPower.String(),
		PendingDeposits: ledgerBalance.PendingInvestment.String(),
		Currency:        "USD",
	}

	// Cache the result
	if s.cache != nil && s.config != nil {
		cacheKey := BalanceCacheKey(userID)
		_ = s.cache.Set(ctx, cacheKey, response, s.config.BalanceCacheTTL)
	}

	s.logger.Info("Balance retrieved from ledger",
		"user_id", userID.String(),
		"usdc_balance", ledgerBalance.USDCBalance.String(),
		"fiat_exposure", ledgerBalance.FiatExposure.String(),
		"pending_investment", ledgerBalance.PendingInvestment.String(),
		"total_buying_power", totalBuyingPower.String())

	return response, nil
}

// ProcessChainDeposit processes incoming chain deposit webhook
func (s *Service) ProcessChainDeposit(ctx context.Context, webhook *entities.ChainDepositWebhook) error {
	// Generate or use provided correlation ID for distributed tracing
	correlationID := webhook.CorrelationID
	if correlationID == "" {
		correlationID = generateCorrelationID()
	}

	if s.allocationService == nil {
		s.logger.Error("allocationService is not configured — deposits will land in ledger but will NOT be split; check DI wiring")
	}
	s.logger.Info("Processing chain deposit", "correlation_id", correlationID, "chain", webhook.Chain, "tx_hash", webhook.TxHash, "amount", webhook.Amount)

	// Parse amount directly to decimal (never use float for money)
	amount, err := decimal.NewFromString(webhook.Amount)
	if err != nil {
		return fmt.Errorf("invalid deposit amount %q: %w", webhook.Amount, err)
	}

	// Validate minimum deposit amount
	if s.validationService != nil {
		if err := s.validationService.ValidateDepositAmount(amount); err != nil {
			s.logger.Warn("Deposit below minimum amount", "tx_hash", webhook.TxHash, "amount", amount.String())
			return err
		}
	} else if amount.LessThan(decimal.NewFromFloat(entities.MinDepositAmountUSDC)) {
		return fmt.Errorf("deposit amount %s is below minimum %v USDC", amount.String(), entities.MinDepositAmountUSDC)
	}

	// Validate maximum deposit amount (anti-money laundering protection)
	maxAmountCents := decimal.NewFromInt(entities.MaxDepositAmountMinorUnits) // This is in cents
	maxAmountWhole := maxAmountCents.Div(decimal.NewFromInt(100))             // Convert cents to dollars
	if amount.GreaterThan(maxAmountWhole) {
		s.logger.Warn("Deposit exceeds maximum amount",
			"tx_hash", webhook.TxHash,
			"amount", amount.String(),
			"max", maxAmountWhole.String())
		return fmt.Errorf("deposit amount %s exceeds maximum %v USDC", amount.String(), maxAmountWhole.String())
	}

	// Generate UUID-based idempotency key from: chain + token + amount + txHash
	// This ensures uniqueness across chains and prevents cross-chain replay attacks
	idempotencyKey := generateIdempotencyKey(string(webhook.Chain), string(webhook.Token), webhook.Amount, webhook.TxHash)

	// Check if deposit already exists using idempotency key (primary) and txHash (fallback)
	existingDeposit, err := s.depositRepo.GetByIdempotencyKey(ctx, idempotencyKey)
	if err != nil && err.Error() != "deposit not found" {
		return fmt.Errorf("failed to check existing deposit: %w", err)
	}

	// Fallback: check by txHash for backward compatibility with older deposits
	if existingDeposit == nil {
		existingDeposit, err = s.depositRepo.GetByTxHash(ctx, webhook.TxHash)
		if err != nil && err.Error() != "deposit not found" {
			return fmt.Errorf("failed to check existing deposit by tx hash: %w", err)
		}
		// If found by txHash, migrate to new idempotency key
		if existingDeposit != nil && existingDeposit.IdempotencyKey == "" {
			existingDeposit.IdempotencyKey = idempotencyKey
			// Note: We don't update here to avoid write conflicts, just use it for reconciliation
		}
	}

	if existingDeposit != nil {
		s.logger.Info("Deposit already processed", "tx_hash", webhook.TxHash, "deposit_id", existingDeposit.ID.String(), "status", existingDeposit.Status)
		// Only reconcile if the deposit row exists but ledger/allocation never completed (pending state).
		// Confirmed deposits are fully settled — re-running RecordDeposit would double-credit the ledger.
		if existingDeposit.Status == "pending" {
			if err := s.ledgerIntegration.RecordDeposit(
				ctx,
				existingDeposit.UserID,
				existingDeposit.Amount,
				existingDeposit.ID,
				string(existingDeposit.Chain),
				existingDeposit.TxHash,
			); err != nil {
				return fmt.Errorf("existing deposit found but failed to reconcile ledger: %w", err)
			}
			confirmedAt := time.Now()
			_ = s.depositRepo.UpdateStatus(ctx, existingDeposit.ID, "confirmed", &confirmedAt)

			if s.allocationService != nil {
				allocationReq := &entities.IncomingFundsRequest{
					UserID:     existingDeposit.UserID,
					Amount:     existingDeposit.Amount,
					EventType:  entities.AllocationEventTypeCryptoDeposit,
					DepositID:  &existingDeposit.ID,
					SourceTxID: &existingDeposit.TxHash,
					Metadata: map[string]any{
						"source":  "crypto",
						"chain":   string(existingDeposit.Chain),
						"token":   string(existingDeposit.Token),
						"tx_hash": existingDeposit.TxHash,
					},
				}
				if err := s.allocationService.ProcessIncomingFunds(ctx, allocationReq); err != nil {
					s.logger.Error("Failed to reconcile allocation split for pending deposit",
						"user_id", existingDeposit.UserID,
						"deposit_id", existingDeposit.ID.String(),
						"error", err)
				}
			}
		}
		return nil
	}

	token := webhook.Token
	if token == "" {
		// Defensive fallback for webhook variants that omit token metadata.
		s.logger.Warn("Deposit webhook missing token; defaulting to USDC",
			"tx_hash", webhook.TxHash,
			"chain", webhook.Chain)
		token = entities.StablecoinUSDC
	}

	// Find the wallet to get user ID.
	// Prefer legacy wallets table for backward compatibility, then fall back to managed_wallets.
	var userID uuid.UUID
	wallet, err := s.walletRepo.GetByAddress(ctx, webhook.Address)
	if err != nil {
		managedWallet, managedErr := s.managedWalletRepo.GetByAddress(ctx, webhook.Address)
		if managedErr != nil {
			return fmt.Errorf("failed to find wallet for address %s: legacy_error=%v managed_error=%w", webhook.Address, err, managedErr)
		}
		userID = managedWallet.UserID
	} else {
		userID = wallet.UserID
	}

	// USDC is pegged 1:1 to USD; no conversion needed.
	usdAmount := amount

	// Validate against user's deposit limits (if limits service is configured)
	if s.limitsService != nil {
		result, err := s.limitsService.ValidateDeposit(ctx, userID, usdAmount)
		if err != nil {
			s.logger.Warn("Deposit limit validation failed",
				"user_id", userID.String(),
				"amount", usdAmount.String(),
				"error", err.Error(),
				"limit_type", result.LimitType,
			)
			return fmt.Errorf("deposit limit exceeded: %w", err)
		}
	}

	// Generate idempotency key (must be done after we have all details)
	idempotencyKey = generateIdempotencyKey(string(webhook.Chain), string(token), amount.String(), webhook.TxHash)

	// Create deposit record FIRST with "pending" status to establish idempotency lock
	// The unique constraint on idempotency_key prevents race conditions
	now := time.Now()
	deposit := &entities.Deposit{
		ID:             uuid.New(),
		IdempotencyKey: idempotencyKey,
		CorrelationID:  correlationID,
		UserID:         userID,
		Chain:          webhook.Chain,
		TxHash:         webhook.TxHash,
		Token:          token,
		Amount:         amount,
		Status:         "pending",
		CreatedAt:      now,
	}

	// Create deposit record first - this establishes the idempotency lock
	if err := s.depositRepo.Create(ctx, deposit); err != nil {
		// Check for duplicate key violation - this is expected idempotent behavior
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			s.logger.Info("Deposit already processed (idempotent duplicate key)",
				"idempotency_key", idempotencyKey)
			return nil
		}
		s.logger.Error("Failed to create deposit record",
			"user_id", userID,
			"error", err)
		return fmt.Errorf("failed to create deposit: %w", err)
	}

	// Record deposit in ledger after deposit record is created
	if err := s.ledgerIntegration.RecordDeposit(ctx, userID, usdAmount, deposit.ID, string(webhook.Chain), webhook.TxHash); err != nil {
		s.logger.Error("Failed to record deposit in ledger, deleting deposit record",
			"user_id", userID,
			"amount", usdAmount,
			"error", err)
		// Compensation: delete the deposit record since ledger failed
		if delErr := s.depositRepo.DeletePendingDeposit(ctx, deposit.ID); delErr != nil {
			s.logger.Error("CRITICAL: Failed to delete deposit after ledger failure",
				"deposit_id", deposit.ID,
				"error", delErr)
		}
		return fmt.Errorf("failed to record deposit in ledger: %w", err)
	}

	// Update deposit status to "confirmed" after ledger success
	confirmedAt := now
	if err := s.depositRepo.UpdateStatus(ctx, deposit.ID, "confirmed", &confirmedAt); err != nil {
		s.logger.Error("Failed to update deposit status to confirmed after ledger success",
			"deposit_id", deposit.ID,
			"error", err)
		return fmt.Errorf("failed to update deposit status to confirmed: %w", err)
	}

	// Process automatic 70/30 allocation split
	// This is the core system rule: every deposit is automatically split
	if s.allocationService != nil {
		allocationReq := &entities.IncomingFundsRequest{
			UserID:     userID,
			Amount:     usdAmount,
			EventType:  entities.AllocationEventTypeCryptoDeposit,
			DepositID:  &deposit.ID,
			SourceTxID: &webhook.TxHash,
			Metadata: map[string]any{
				"source":  "crypto",
				"chain":   string(webhook.Chain),
				"token":   string(token),
				"tx_hash": webhook.TxHash,
			},
		}
		if err := s.allocationService.ProcessIncomingFunds(ctx, allocationReq); err != nil {
			// Deposit is confirmed and credited to ledger. Allocation failed but the
			// deposit recovery worker will retry any confirmed deposit without a completed
			// allocation event. Keep status as "confirmed" — do NOT use an invalid status.
			s.logger.Error("Allocation split failed — deposit confirmed, recovery worker will retry",
				"user_id", userID,
				"deposit_id", deposit.ID,
				"amount", usdAmount,
				"error_type", fmt.Sprintf("%T", err),
				"error", err)

			// Notify user with generic message (don't expose internal error details)
			if s.notificationService != nil {
				if notifyErr := s.notificationService.NotifyAllocationFailed(
					ctx,
					userID,
					usdAmount,
					deposit.ID,
					"allocation_failed",
				); notifyErr != nil {
					s.logger.Error("Failed to send allocation failure notification",
						"user_id", userID,
						"deposit_id", deposit.ID,
						"error", notifyErr)
				}
			}
		} else {
			s.logger.Info("Automatic 70/30 allocation split completed",
				"user_id", userID,
				"amount", usdAmount)
		}
	}

	// Record deposit usage against limits
	if s.limitsService != nil {
		if err := s.limitsService.RecordDeposit(ctx, userID, usdAmount); err != nil {
			s.logger.Warn("Failed to record deposit usage", "error", err, "user_id", userID.String())
			// Don't fail the deposit, just log the warning
		}
	}

	// Create audit log entry for compliance
	if s.auditService != nil {
		if err := s.auditService.LogDeposit(ctx, userID, deposit.ID, usdAmount.String(), string(webhook.Chain), deposit.Status); err != nil {
			s.logger.Warn("Failed to create audit log for deposit", "error", err, "deposit_id", deposit.ID.String())
			// Don't fail the deposit, audit logging is non-critical
		}
	}

	// Send deposit confirmation notification
	if s.notificationService != nil {
		if err := s.notificationService.NotifyDepositConfirmed(ctx, userID, usdAmount.String(), string(webhook.Chain), webhook.TxHash); err != nil {
			s.logger.Warn("Failed to send deposit notification", "error", err, "user_id", userID.String())
		}
		// Notify for large deposits (>= $1000)
		largeDepositThreshold := decimal.NewFromInt(1000)
		if usdAmount.GreaterThanOrEqual(largeDepositThreshold) {
			// Get new balance for notification
			if balance, err := s.ledgerIntegration.GetUserBalance(ctx, userID); err == nil {
				_ = s.notificationService.NotifyLargeBalanceChange(ctx, userID, "deposit", usdAmount, balance.TotalValue)
			}
		}
	}

	// Invalidate balance cache so next balance fetch gets fresh data
	if s.cache != nil {
		cacheKey := BalanceCacheKey(userID)
		if err := s.cache.Delete(ctx, cacheKey); err != nil {
			s.logger.Warn("Failed to invalidate balance cache", "error", err, "user_id", userID.String())
		}
	}

	s.logger.Info("Deposit processed successfully",
		"user_id", userID,
		"amount", webhook.Amount,
		"usd_amount", usdAmount.String(),
		"tx_hash", webhook.TxHash,
	)

	return nil
}

// CreateVirtualAccount provisions a Bridge virtual account for the user.
func (s *Service) CreateVirtualAccount(ctx context.Context, req *entities.CreateVirtualAccountRequest) (*entities.CreateVirtualAccountResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("request is required")
	}
	if s.virtualAccountRepo == nil {
		return nil, fmt.Errorf("virtual account repository not configured")
	}
	if s.bridgeVAService == nil {
		return nil, fmt.Errorf("bridge virtual account service not configured")
	}
	if req.BridgeCustomerID == "" {
		return nil, fmt.Errorf("bridge customer ID is required")
	}

	currency := strings.ToUpper(strings.TrimSpace(req.Currency))
	if currency == "" {
		currency = "USD"
	}

	// Return existing account if already provisioned for this currency
	existing, _ := s.virtualAccountRepo.GetActiveByUserIDAndCurrency(ctx, req.UserID, currency)
	if existing != nil {
		return &entities.CreateVirtualAccountResponse{
			VirtualAccount: existing,
			Message:        "Virtual account already exists",
		}, nil
	}

	if err := s.bridgeVAService.ProvisionVirtualAccounts(ctx, req.UserID, req.BridgeCustomerID, []string{currency}); err != nil {
		return nil, fmt.Errorf("failed to provision virtual account: %w", err)
	}

	va, err := s.virtualAccountRepo.GetActiveByUserIDAndCurrency(ctx, req.UserID, currency)
	if err != nil || va == nil {
		return nil, fmt.Errorf("virtual account provisioned but could not be retrieved")
	}

	return &entities.CreateVirtualAccountResponse{
		VirtualAccount: va,
		Message:        "Virtual account created successfully",
	}, nil
}

// InitiateBrokerFunding initiates funding to Alpaca brokerage account after off-ramp completion
func (s *Service) InitiateBrokerFunding(ctx context.Context, depositID uuid.UUID, alpacaAccountID string, amount decimal.Decimal) error {
	s.logger.Info("Initiating broker funding",
		"deposit_id", depositID.String(),
		"alpaca_account_id", alpacaAccountID,
		"amount", amount.String())

	// Verify Alpaca account is active
	alpacaAccount, err := s.alpacaAPI.GetAccount(ctx, alpacaAccountID)
	if err != nil {
		s.logger.Error("Failed to get Alpaca account", "error", err, "alpaca_account_id", alpacaAccountID)
		return fmt.Errorf("failed to get Alpaca account: %w", err)
	}

	if alpacaAccount.Status != entities.AlpacaAccountStatusActive {
		s.logger.Error("Alpaca account not active",
			"alpaca_account_id", alpacaAccountID,
			"status", alpacaAccount.Status)
		return fmt.Errorf("Alpaca account not active: %s", alpacaAccount.Status)
	}

	// Create instant funding transfer to extend buying power immediately
	instantFundingReq := &entities.AlpacaInstantFundingRequest{
		AccountNo:       alpacaAccount.AccountNumber,
		SourceAccountNo: "SI", // Source account for instant funding
		Amount:          amount,
	}

	instantFundingResp, err := s.alpacaAPI.InitiateInstantFunding(ctx, instantFundingReq)
	if err != nil {
		s.logger.Error("Failed to initiate instant funding",
			"error", err,
			"alpaca_account_id", alpacaAccountID,
			"amount", amount.String())
		return fmt.Errorf("failed to initiate instant funding: %w", err)
	}

	s.logger.Info("Instant funding initiated successfully",
		"transfer_id", instantFundingResp.ID,
		"status", instantFundingResp.Status,
		"deadline", instantFundingResp.Deadline,
		"alpaca_account_id", alpacaAccountID)

	// Update deposit status to broker_funded
	now := time.Now()
	if err := s.depositRepo.UpdateStatus(ctx, depositID, "broker_funded", &now); err != nil {
		s.logger.Error("Failed to update deposit status",
			"error", err,
			"deposit_id", depositID.String())
		return fmt.Errorf("failed to update deposit status: %w", err)
	}

	s.logger.Info("Broker funding completed",
		"deposit_id", depositID.String(),
		"transfer_id", instantFundingResp.ID,
		"alpaca_account_id", alpacaAccountID)

	return nil
}
