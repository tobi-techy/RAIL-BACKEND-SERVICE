package pajfunding

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
	chainrailspkg "github.com/rail-service/rail_service/internal/infrastructure/adapters/chainrails"
	circlepkg "github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/paj"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"github.com/rail-service/rail_service/pkg/crypto"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// LedgerService for balance checks and debits on offramp.
type LedgerService interface {
	GetAccountBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error)
	CreateTransaction(ctx context.Context, userID uuid.UUID, accountType entities.AccountType, txType entities.TransactionType, amount decimal.Decimal, metadata map[string]interface{}) error
	ReverseTransaction(ctx context.Context, userID uuid.UUID, accountType entities.AccountType, originalTxID string, amount decimal.Decimal, metadata map[string]interface{}) error
}

// AllocationService handles the 70/30 deposit split.
type AllocationService interface {
	ProcessIncomingFunds(ctx context.Context, req *entities.IncomingFundsRequest) error
}

// DepositLedgerService credits USDC balance for incoming deposits (Debit = increase).
type DepositLedgerService interface {
	CreditUSDCBalance(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string, metadata map[string]interface{}) error
}

// DepositRepository persists deposit records for transaction history.
type DepositRepository interface {
	Create(ctx context.Context, deposit *entities.Deposit) error
}

// NotificationService sends user-facing push/in-app notifications.
type NotificationService interface {
	NotifyDepositConfirmed(ctx context.Context, userID uuid.UUID, amount, chain, txHash string) error
	NotifyDepositDetected(ctx context.Context, userID uuid.UUID, chain string) error
	NotifyWithdrawalCompleted(ctx context.Context, userID uuid.UUID, amount, destination string) error
	NotifyWithdrawalFailed(ctx context.Context, userID uuid.UUID, amount, reason string) error
}

// GameplayHooks triggers gameplay events (XP, streaks, challenges) on deposit.
type GameplayHooks interface {
	OnDeposit(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, depositID uuid.UUID)
}

// WalletProvider looks up a user's wallet address by chain.
type WalletProvider interface {
	GetWalletByUserAndChain(ctx context.Context, userID uuid.UUID, chain entities.WalletChain) (*entities.ManagedWallet, error)
}

// CircleTransferAdapter transfers USDC via Circle Programmable Wallets.
type CircleTransferAdapter interface {
	FindWalletWithUSDC(ctx context.Context, userRefID string) (walletID, tokenID, blockchain, address string, err error)
	TransferUSDC(ctx context.Context, walletID, tokenID, destinationAddress, amount string) (*circlepkg.Transaction, error)
}

// ChainRailsAdapter creates cross-chain transfer intents.
type ChainRailsAdapter interface {
	CreateIntent(ctx context.Context, req *chainrailspkg.CreateIntentRequest) (*chainrailspkg.CreateIntentResponse, error)
}

// WithdrawalLimitsChecker validates withdrawal amounts against daily/monthly limits.
type WithdrawalLimitsChecker interface {
	ValidateWithdrawal(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error
}

// DepositLimitsChecker validates deposit amounts against daily/monthly limits.
type DepositLimitsChecker interface {
	ValidateDeposit(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) (*entities.LimitCheckResult, error)
}

// Service handles Paj Cash NGN on/off ramp operations.
type Service struct {
	db                *sqlx.DB
	pajClient         *paj.Client
	ledger            LedgerService
	allocationService AllocationService
	depositLedger     DepositLedgerService
	depositRepo       DepositRepository
	notifier          NotificationService
	gameplayHooks     GameplayHooks
	walletProvider    WalletProvider
	circleTransfer    CircleTransferAdapter
	chainRailsAdapter ChainRailsAdapter
	limitsChecker     WithdrawalLimitsChecker
	depositLimits     DepositLimitsChecker
	redis             cache.RedisClient
	encryptionKey     string
	logger            *zap.Logger
}

func NewService(db *sqlx.DB, pajClient *paj.Client, ledger LedgerService, allocationService AllocationService, depositLedger DepositLedgerService, redis cache.RedisClient, encryptionKey string, logger *zap.Logger) *Service {
	return &Service{db: db, pajClient: pajClient, ledger: ledger, allocationService: allocationService, depositLedger: depositLedger, redis: redis, encryptionKey: encryptionKey, logger: logger}
}

// SetDepositRepository sets the deposit repository for persisting deposit records.
func (s *Service) SetDepositRepository(repo DepositRepository) { s.depositRepo = repo }

// SetNotificationService sets the notification service for deposit alerts.
func (s *Service) SetNotificationService(ns NotificationService) { s.notifier = ns }

// SetWalletProvider sets the wallet provider for looking up user wallet addresses.
func (s *Service) SetWalletProvider(wp WalletProvider) { s.walletProvider = wp }

// SetGameplayHooks sets the gameplay hooks for triggering XP/streak/challenge events.
func (s *Service) SetGameplayHooks(gh GameplayHooks) { s.gameplayHooks = gh }

// SetCircleTransfer sets the Circle transfer adapter for offramp.
func (s *Service) SetCircleTransfer(ct CircleTransferAdapter) { s.circleTransfer = ct }

// SetChainRailsAdapter sets the ChainRails adapter for cross-chain offramp.
func (s *Service) SetChainRailsAdapter(cr ChainRailsAdapter) { s.chainRailsAdapter = cr }

// evmToChainRails maps Circle EVM blockchain IDs to ChainRails source chain + USDC token.
var evmToChainRails = map[string]struct{ chain, token string }{
	"ETH-SEPOLIA":  {"ETHEREUM_TESTNET", "0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238"},
	"ETH":          {"ETHEREUM_MAINNET", "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"},
	"BASE-SEPOLIA": {"BASE_TESTNET", "0x036CbD53842c5426634e7929541eC2318f3dCF7e"},
	"BASE":         {"BASE_MAINNET", "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"},
	"ARB-SEPOLIA":  {"ARBITRUM_TESTNET", "0x75faf114eafb1BDbe2F0316DF893fd58CE46AA4d"},
	"ARB":          {"ARBITRUM_MAINNET", "0xaf88d065e77c8cC2239327C5EDb3A432268e5831"},
	"OP-SEPOLIA":   {"OPTIMISM_TESTNET", "0x5fd84259d66Cd46123540766Be93DFE6D43130D7"},
	"OP":           {"OPTIMISM_MAINNET", "0x0b2C639c533813f4Aa9D7837CAf62653d097Ff85"},
	"AVAX-FUJI":    {"AVALANCHE_TESTNET", "0x5425890298aed601595a70AB815c96711a31Bc65"},
	"AVAX":         {"AVALANCHE_MAINNET", "0xB97EF9Ef8734C71904D8002F8b6Bc66Dd9c48a6E"},
}

// executeCircleViaCRToPaj bridges EVM USDC to PAJ's Solana address via ChainRails.
// ChainRails handles the cross-chain delivery in one step.
func (s *Service) executeCircleViaCRToPaj(ctx context.Context, userID uuid.UUID, walletID, tokenID, blockchain, onChainAddress string, order *paj.OfframpOrder, totalHold decimal.Decimal, totalTransfer float64) {
	source, ok := evmToChainRails[blockchain]
	if !ok {
		s.logger.Error("unsupported EVM chain for ChainRails Paj bridge", zap.String("blockchain", blockchain))
		s.reverseHold(ctx, userID, order.ID, totalHold, "unsupported_chain")
		return
	}

	// Determine Solana dest chain based on environment
	solDest := "SOLANA_MAINNET"
	if strings.Contains(blockchain, "SEPOLIA") || strings.Contains(blockchain, "FUJI") || strings.Contains(blockchain, "AMOY") || strings.Contains(blockchain, "DEVNET") {
		solDest = "SOLANA_TESTNET"
	}

	amountMicro := decimal.NewFromFloat(totalTransfer).Shift(6).IntPart()

	// Step 1: Create ChainRails intent — source=EVM chain, dest=Solana, recipient=PAJ address
	intent, err := s.chainRailsAdapter.CreateIntent(ctx, &chainrailspkg.CreateIntentRequest{
		Amount:           fmt.Sprintf("%d", amountMicro),
		AmountSymbol:     "USDC",
		TokenIn:          source.token,
		SourceChain:      source.chain,
		DestinationChain: solDest,
		Recipient:        order.Address,
		Sender:           onChainAddress,
		RefundAddress:    onChainAddress,
		Metadata: map[string]interface{}{
			"paj_order_id": order.ID,
			"user_id":      userID.String(),
			"type":         "paj_offramp_bridge",
		},
	})
	if err != nil {
		s.logger.Error("ChainRails intent for Paj bridge failed — reversing hold",
			zap.Error(err), zap.String("paj_order_id", order.ID))
		s.reverseHold(ctx, userID, order.ID, totalHold, "chainrails_intent_failed")
		return
	}

	// Step 2: Fund the intent via Circle (same-chain EVM transfer to intent address)
	circleAmount := fmt.Sprintf("%.2f", totalTransfer)
	if intent.TotalAmountInAssetToken != "" && intent.AssetTokenDecimals > 0 {
		totalMicro, parseOk := new(big.Int).SetString(intent.TotalAmountInAssetToken, 10)
		if parseOk {
			divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(intent.AssetTokenDecimals)), nil)
			humanAmount := new(big.Float).Quo(new(big.Float).SetInt(totalMicro), new(big.Float).SetInt(divisor))
			circleAmount = humanAmount.Text('f', intent.AssetTokenDecimals)
		}
	}

	tx, txErr := s.circleTransfer.TransferUSDC(ctx, walletID, tokenID, intent.IntentAddress, circleAmount)
	if txErr != nil {
		s.logger.Error("Circle transfer to ChainRails intent for Paj failed — reversing hold",
			zap.Error(txErr), zap.String("paj_order_id", order.ID))
		s.reverseHold(ctx, userID, order.ID, totalHold, "circle_cr_transfer_failed")
		return
	}
	if tx.State == "DENIED" || tx.State == "FAILED" || tx.State == "CANCELLED" {
		s.reverseHold(ctx, userID, order.ID, totalHold, "circle_cr_"+string(tx.State))
		return
	}

	s.logger.Info("Circle→ChainRails→Paj bridge initiated",
		zap.String("circle_tx_id", tx.ID),
		zap.Int("cr_intent_id", intent.ID),
		zap.String("paj_order_id", order.ID),
		zap.String("source", source.chain),
		zap.String("dest", solDest))

	s.db.ExecContext(ctx, `UPDATE paj_orders SET bridge_transfer_id = $1 WHERE paj_order_id = $2`,
		fmt.Sprintf("circle-cr:%s:%d", tx.ID, intent.ID), order.ID)

	// Refund slippage buffer (totalHold includes 1% buffer + fee, totalTransfer is actual)
	actualAmount := decimal.NewFromFloat(totalTransfer)
	excess := totalHold.Sub(actualAmount)
	if excess.IsPositive() && s.ledger != nil {
		s.ledger.ReverseTransaction(ctx, userID, entities.AccountTypeSpendingBalance,
			"paj_offramp_slippage_refund_"+order.ID, excess, map[string]interface{}{
				"provider": "paj", "type": "slippage_refund", "paj_order_id": order.ID,
				"path": "circle_chainrails",
			})
	}
}

// SetLimitsChecker sets the withdrawal limits validator.
func (s *Service) SetLimitsChecker(lc WithdrawalLimitsChecker) { s.limitsChecker = lc }

// SetDepositLimits sets the deposit limits validator.
func (s *Service) SetDepositLimits(dl DepositLimitsChecker) { s.depositLimits = dl }

// --- Session management ---

// NeedsVerification checks if the user has a valid Paj session.
func (s *Service) NeedsVerification(ctx context.Context, userID uuid.UUID) bool {
	_, err := s.getSessionToken(ctx, userID)
	return err != nil
}

// Initiate triggers a Paj OTP to the user's email.
// Returns "already_verified" if user has a valid session.
// Skips if user already has a valid session.
func (s *Service) Initiate(ctx context.Context, userID uuid.UUID, email string) (alreadyVerified bool, err error) {
	if _, err := s.getSessionToken(ctx, userID); err == nil {
		return true, nil
	}
	_, err = s.pajClient.Initiate(ctx, email)
	return false, err
}

// Verify confirms the OTP and caches the session token.
func (s *Service) Verify(ctx context.Context, userID uuid.UUID, email, otp, deviceUUID string) error {
	// Paj API expects a valid UUID for device.uuid, but our device_id is a
	// SHA-256 fingerprint hash. Derive a deterministic UUID v5 from it.
	pajDeviceUUID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(deviceUUID)).String()

	resp, err := s.pajClient.Verify(ctx, email, otp, paj.DeviceSignature{
		UUID: pajDeviceUUID, Device: "Rail", OS: "iOS",
	})
	if err != nil {
		return fmt.Errorf("paj verify: %w", err)
	}

	encrypted, err := crypto.Encrypt(resp.Token, s.encryptionKey)
	if err != nil {
		return fmt.Errorf("encrypt session token: %w", err)
	}

	expiresAt, _ := time.Parse(time.RFC3339, resp.ExpiresAt)
	if expiresAt.IsZero() {
		expiresAt = time.Now().Add(24 * time.Hour)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO paj_sessions (user_id, session_token_encrypted, expires_at, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			session_token_encrypted = EXCLUDED.session_token_encrypted,
			expires_at = EXCLUDED.expires_at,
			updated_at = NOW()`,
		userID, encrypted, expiresAt)
	return err
}

func (s *Service) getSessionToken(ctx context.Context, userID uuid.UUID) (string, error) {
	var encrypted string
	var expiresAt time.Time
	err := s.db.QueryRowContext(ctx,
		`SELECT session_token_encrypted, expires_at FROM paj_sessions WHERE user_id = $1`,
		userID).Scan(&encrypted, &expiresAt)
	if err != nil {
		return "", fmt.Errorf("no paj session: %w", err)
	}
	if time.Now().After(expiresAt) {
		return "", fmt.Errorf("paj session expired")
	}
	return crypto.Decrypt(encrypted, s.encryptionKey)
}

func (s *Service) invalidateSessionIfUnauthorized(ctx context.Context, userID uuid.UUID, err error) error {
	if !paj.IsUnauthorized(err) {
		return err
	}
	if s.db != nil {
		if _, dbErr := s.db.ExecContext(ctx, `DELETE FROM paj_sessions WHERE user_id = $1`, userID); dbErr != nil {
			s.logger.Warn("failed to invalidate rejected paj session",
				zap.String("user_id", userID.String()), zap.Error(dbErr))
		}
	}
	return fmt.Errorf("paj session expired: %w", err)
}

// --- Rates ---

const pajRatesCacheKey = "paj:rates"
const pajRatesCacheTTL = 5 * time.Minute
const pajBanksCacheKey = "paj:banks"
const pajBanksCacheTTL = 24 * time.Hour

func (s *Service) GetRates(ctx context.Context) (*paj.RateResponse, error) {
	// Try cache first.
	if s.redis != nil {
		var cached paj.RateResponse
		if err := s.redis.Get(ctx, pajRatesCacheKey, &cached); err == nil {
			return &cached, nil
		}
	}

	rates, err := s.pajClient.GetRates(ctx)
	if err != nil {
		// On upstream failure, try returning stale cache.
		if s.redis != nil {
			var stale paj.RateResponse
			if cacheErr := s.redis.Get(ctx, pajRatesCacheKey, &stale); cacheErr == nil {
				s.logger.Warn("paj rates upstream failed, serving stale cache", zap.Error(err))
				return &stale, nil
			}
		}
		return nil, err
	}

	// Cache the fresh response.
	if s.redis != nil {
		if cacheErr := s.redis.Set(ctx, pajRatesCacheKey, rates, pajRatesCacheTTL); cacheErr != nil {
			s.logger.Warn("failed to cache paj rates", zap.Error(cacheErr))
		}
	}
	return rates, nil
}

// --- Banks ---

func (s *Service) GetBanks(ctx context.Context, userID uuid.UUID) ([]paj.Bank, error) {
	token, err := s.getSessionToken(ctx, userID)
	if err != nil {
		return nil, err
	}

	if s.redis != nil {
		var cached []paj.Bank
		if err := s.redis.Get(ctx, pajBanksCacheKey, &cached); err == nil && len(cached) > 0 {
			return cached, nil
		}
	}

	bankCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	banks, err := s.pajClient.GetBanks(bankCtx, token)
	if err != nil {
		if paj.IsUnauthorized(err) {
			return nil, s.invalidateSessionIfUnauthorized(ctx, userID, err)
		}
		if s.redis != nil {
			var stale []paj.Bank
			if cacheErr := s.redis.Get(ctx, pajBanksCacheKey, &stale); cacheErr == nil && len(stale) > 0 {
				s.logger.Warn("paj banks upstream failed, serving cached banks", zap.Error(err))
				return stale, nil
			}
		}
		return nil, s.invalidateSessionIfUnauthorized(ctx, userID, err)
	}
	if s.redis != nil && len(banks) > 0 {
		if cacheErr := s.redis.Set(ctx, pajBanksCacheKey, banks, pajBanksCacheTTL); cacheErr != nil {
			s.logger.Warn("failed to cache paj banks", zap.Error(cacheErr))
		}
	}
	return banks, nil
}

func (s *Service) ResolveBankAccount(ctx context.Context, userID uuid.UUID, bankID, accountNumber string) (*paj.ResolvedAccount, error) {
	token, err := s.getSessionToken(ctx, userID)
	if err != nil {
		return nil, err
	}
	account, err := s.pajClient.ResolveBankAccount(ctx, token, bankID, accountNumber)
	if err != nil {
		return nil, s.invalidateSessionIfUnauthorized(ctx, userID, err)
	}
	return account, nil
}

func (s *Service) AddBankAccount(ctx context.Context, userID uuid.UUID, bankID, accountNumber string) (*paj.SavedBankAccount, error) {
	token, err := s.getSessionToken(ctx, userID)
	if err != nil {
		return nil, err
	}
	account, err := s.pajClient.AddBankAccount(ctx, token, bankID, accountNumber)
	if err != nil {
		if paj.IsUnauthorized(err) {
			return nil, s.invalidateSessionIfUnauthorized(ctx, userID, err)
		}
		// Ignore "already exists" — the bank account is already saved on Paj's side.
		if strings.Contains(err.Error(), "already exists") {
			return &paj.SavedBankAccount{AccountNumber: accountNumber, Bank: bankID}, nil
		}
		return nil, err
	}
	return account, nil
}

func (s *Service) GetBankAccounts(ctx context.Context, userID uuid.UUID) ([]paj.SavedBankAccount, error) {
	token, err := s.getSessionToken(ctx, userID)
	if err != nil {
		return nil, err
	}
	accounts, err := s.pajClient.GetBankAccounts(ctx, token)
	if err != nil {
		return nil, s.invalidateSessionIfUnauthorized(ctx, userID, err)
	}
	return accounts, nil
}

// --- Onramp (NGN → USDC) ---

func (s *Service) CreateOnrampOrder(ctx context.Context, userID uuid.UUID, fiatAmount float64, currency string) (*paj.OnrampOrder, error) {
	token, err := s.getSessionToken(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Enforce NGN minimum deposit
	if fiatAmount < 500 {
		return nil, fmt.Errorf("minimum deposit is ₦500")
	}

	// Enforce deposit limits (estimate USDC from fiat using cached rate)
	if s.depositLimits != nil {
		rates, rateErr := s.pajClient.GetRates(ctx)
		if rateErr == nil && rates != nil && rates.OnRampRate.Rate > 0 {
			estimatedUSDC := decimal.NewFromFloat(fiatAmount).Div(decimal.NewFromFloat(rates.OnRampRate.Rate))
			if result, limErr := s.depositLimits.ValidateDeposit(ctx, userID, estimatedUSDC); limErr != nil || (result != nil && !result.Allowed) {
				msg := "deposit limit exceeded"
				if result != nil && result.Reason != "" {
					msg = result.Reason
				}
				return nil, fmt.Errorf("%s", msg)
			}
		}
	}

	// Look up user's Circle wallet address so USDC goes directly to them.
	// Prefer Solana (PAJ settles on Solana), fall back to any wallet with address.
	var recipient string
	if s.circleTransfer != nil {
		_, _, blockchain, address, findErr := s.circleTransfer.FindWalletWithUSDC(ctx, userID.String())
		if findErr == nil && address != "" && strings.Contains(strings.ToUpper(blockchain), "SOL") {
			recipient = address
		}
	}
	if recipient == "" && s.walletProvider != nil {
		wallet, err := s.walletProvider.GetWalletByUserAndChain(ctx, userID, entities.WalletChainSolana)
		if err == nil && wallet != nil && wallet.Address != "" {
			recipient = wallet.Address
		} else {
			s.logger.Warn("could not resolve user Solana wallet for onramp",
				zap.String("user_id", userID.String()), zap.Error(err))
		}
	}

	order, err := s.pajClient.CreateOnrampOrder(ctx, token, fiatAmount, currency, recipient)
	if err != nil {
		return nil, s.invalidateSessionIfUnauthorized(ctx, userID, err)
	}

	// Persist order for webhook reconciliation.
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO paj_orders (user_id, paj_order_id, order_type, status, fiat_amount, token_amount, currency, fee, pay_account_number, pay_account_name, pay_bank, used_user_wallet)
		VALUES ($1, $2, 'onramp', 'pending', $3, $4, $5, $6, $7, $8, $9, $10)`,
		userID, order.ID, order.FiatAmount, order.Amount, currency, order.Fee,
		order.AccountNumber, order.AccountName, order.Bank, recipient != "")
	if err != nil {
		s.logger.Error("failed to persist paj onramp order", zap.Error(err), zap.String("paj_order_id", order.ID))
	}

	return order, nil
}

// --- Offramp (USDC → NGN) ---

// RailNGNWithdrawalFee is Rail's flat fee for NGN withdrawals (in USDC).
// ₦30 at ~₦1500/USD ≈ $0.02.
const RailNGNWithdrawalFee = 0.02

func (s *Service) CreateOfframpOrder(ctx context.Context, userID uuid.UUID, bankID, accountNumber string, fiatAmount float64, currency string) (*OfframpResult, error) {
	// Quick sanity check before any API calls.
	if fiatAmount < 500 {
		return nil, fmt.Errorf("minimum withdrawal is ₦500")
	}

	// P0: Distributed lock — prevent concurrent offramp requests per user.
	unlock, lockErr := s.acquireOfframpLock(ctx, userID)
	if lockErr != nil {
		return nil, fmt.Errorf("withdrawal in progress, please wait")
	}
	defer unlock()

	// P1: Idempotency — reject duplicate requests within a 30-second window.
	idempotencyKey := fmt.Sprintf("paj-offramp:%s:%s:%.0f", userID, bankID, fiatAmount)
	var exists bool
	s.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM paj_orders WHERE user_id = $1 AND bank_id = $2 AND fiat_amount = $3 AND created_at > NOW() - interval '30 seconds' AND status != 'failed')`,
		userID, bankID, fiatAmount).Scan(&exists)
	if exists {
		return nil, fmt.Errorf("duplicate withdrawal request, please wait")
	}
	_ = idempotencyKey // used for logging context

	token, err := s.getSessionToken(ctx, userID)
	if err != nil {
		return nil, s.invalidateSessionIfUnauthorized(ctx, userID, err)
	}

	// Get the rate first to calculate the USDC amount we need to debit.
	rates, err := s.pajClient.GetRates(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get rates: %w", err)
	}
	if rates.OffRampRate.Rate <= 0 {
		return nil, fmt.Errorf("offramp rate unavailable")
	}
	if rates.OffRampRate.Rate < 100 || rates.OffRampRate.Rate > 10000 {
		return nil, fmt.Errorf("offramp rate out of bounds: %.2f", rates.OffRampRate.Rate)
	}

	// Circle requires ≥ $1 USDC per transfer. Enforce the NGN equivalent.
	minNGN := rates.OffRampRate.Rate * 1.05 // ₦ equivalent of ~$1.05 (with buffer)
	if fiatAmount < minNGN {
		return nil, fmt.Errorf("minimum withdrawal is ₦%.0f", minNGN)
	}

	// Estimate USDC amount: fiatAmount / rate. Add 1% buffer for rate slippage.
	estimatedUSDC := decimal.NewFromFloat(fiatAmount).Div(decimal.NewFromFloat(rates.OffRampRate.Rate))
	estimatedUSDC = estimatedUSDC.Mul(decimal.NewFromFloat(1.01)).Round(2)

	// Rail's flat fee for NGN withdrawals.
	railFee := decimal.NewFromFloat(RailNGNWithdrawalFee)

	// Total hold = estimated USDC (with slippage buffer) + Rail fee.
	// Paj's fee is included in order.Amount (Paj deducts it from the token amount).
	totalHold := estimatedUSDC.Add(railFee)

	// P2: Withdrawal limits — enforce daily/monthly caps.
	if s.limitsChecker != nil {
		if limErr := s.limitsChecker.ValidateWithdrawal(ctx, userID, estimatedUSDC); limErr != nil {
			return nil, limErr
		}
	}

	// Check spend balance — user needs enough for amount + Rail fee.
	if s.ledger != nil {
		balance, err := s.ledger.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
		if err != nil {
			return nil, fmt.Errorf("failed to check balance: %w", err)
		}
		if balance.LessThan(totalHold) {
			return nil, fmt.Errorf("insufficient balance: have %s USDC, need ~%s USDC (incl. $%s fee) for ₦%.0f",
				balance.String(), totalHold.String(), railFee.String(), fiatAmount)
		}

		// Debit spend balance (hold = estimated USDC + Rail fee).
		err = s.ledger.CreateTransaction(ctx, userID, entities.AccountTypeSpendingBalance,
			entities.TransactionTypeWithdrawal, totalHold, map[string]interface{}{
				"provider": "paj", "type": "offramp_hold", "fiat_amount": fiatAmount,
				"currency": currency, "rail_fee": railFee.String(),
			})
		if err != nil {
			return nil, fmt.Errorf("failed to debit balance: %w", err)
		}
	}

	// Create Paj offramp order.
	order, err := s.pajClient.CreateOfframpOrder(ctx, token, bankID, accountNumber, fiatAmount, currency)
	if err != nil {
		// Reverse the full hold on Paj failure.
		if s.ledger != nil {
			reverseKey := fmt.Sprintf("paj_offramp_failed_%s_%d", userID, time.Now().UnixNano())
			reverseErr := s.ledger.ReverseTransaction(ctx, userID, entities.AccountTypeSpendingBalance,
				reverseKey, totalHold, map[string]interface{}{
					"provider": "paj", "type": "offramp_reversal", "reason": err.Error(),
				})
			if reverseErr != nil {
				s.logger.Error("CRITICAL: failed to reverse ledger hold after Paj failure",
					zap.Error(reverseErr), zap.String("user_id", userID.String()),
					zap.String("amount", totalHold.String()))
			}
		}
		return nil, err
	}

	_, dbErr := s.db.ExecContext(ctx, `
		INSERT INTO paj_orders (user_id, paj_order_id, order_type, status, fiat_amount, token_amount, currency, rate, fee, bank_id, bank_account_number, bank_account_name, paj_deposit_address, hold_amount)
		VALUES ($1, $2, 'offramp', 'pending', $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		userID, order.ID, order.FiatAmount, order.Amount, currency, order.Rate, order.Fee,
		bankID, accountNumber, s.resolveAccountName(ctx, token, accountNumber), order.Address, totalHold)
	if dbErr != nil {
		s.logger.Error("CRITICAL: failed to persist paj offramp order — reversing hold",
			zap.Error(dbErr), zap.String("paj_order_id", order.ID))
		if s.ledger != nil {
			s.ledger.ReverseTransaction(ctx, userID, entities.AccountTypeSpendingBalance,
				"paj_offramp_db_fail_"+order.ID, totalHold, map[string]interface{}{
					"provider": "paj", "type": "offramp_db_failure_reversal", "paj_order_id": order.ID,
				})
		}
		return nil, fmt.Errorf("failed to record withdrawal order")
	}

	// Validate prerequisites for transfer.
	if s.circleTransfer == nil {
		s.logger.Error("Circle transfer adapter not configured for Paj offramp — reversing hold",
			zap.String("paj_order_id", order.ID))
		s.reverseHold(ctx, userID, order.ID, totalHold, "no_transfer_config")
		return nil, fmt.Errorf("withdrawal infrastructure not available")
	}
	if order.Address == "" {
		s.logger.Error("Paj order missing deposit address — reversing hold",
			zap.String("paj_order_id", order.ID))
		s.reverseHold(ctx, userID, order.ID, totalHold, "no_deposit_address")
		return nil, fmt.Errorf("withdrawal service returned invalid response")
	}

	// Execute transfer asynchronously — user gets immediate response.
	// Circle is the primary wallet provider.
	go s.executeCircleTransferToPaj(userID, order, totalHold, estimatedUSDC, railFee)

	return &OfframpResult{
		Order:   order,
		RailFee: RailNGNWithdrawalFee,
	}, nil
}

// executeCircleTransferToPaj sends USDC from the user's Circle wallet to Paj's deposit address.
// For Solana wallets: direct transfer. For EVM wallets: bridges via ChainRails.
func (s *Service) executeCircleTransferToPaj(userID uuid.UUID, order *paj.OfframpOrder, totalHold, estimatedUSDC, railFee decimal.Decimal) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	transferAmount := math.Round(order.Amount*100) / 100
	totalTransfer := transferAmount + RailNGNWithdrawalFee

	if s.circleTransfer == nil {
		s.logger.Error("Circle transfer adapter not configured — reversing hold",
			zap.String("paj_order_id", order.ID))
		s.reverseHold(ctx, userID, order.ID, totalHold, "no_circle_adapter")
		return
	}

	walletID, tokenID, blockchain, onChainAddress, err := s.circleTransfer.FindWalletWithUSDC(ctx, userID.String())
	if err != nil {
		s.logger.Error("async: no Circle wallet with USDC for Paj offramp — reversing hold",
			zap.Error(err), zap.String("user_id", userID.String()),
			zap.String("paj_order_id", order.ID))
		s.reverseHold(ctx, userID, order.ID, totalHold, "no_usdc_wallet")
		return
	}

	isSolana := strings.Contains(strings.ToUpper(blockchain), "SOL")

	if isSolana {
		// Direct transfer — wallet is already on Solana, send straight to PAJ
		tx, txErr := s.circleTransfer.TransferUSDC(ctx, walletID, tokenID, order.Address, fmt.Sprintf("%.2f", totalTransfer))
		if txErr != nil {
			s.logger.Error("async: Circle SOL transfer to Paj failed — reversing hold",
				zap.Error(txErr), zap.String("paj_order_id", order.ID))
			s.reverseHold(ctx, userID, order.ID, totalHold, "circle_transfer_failed")
			return
		}
		if tx.State == "DENIED" || tx.State == "FAILED" || tx.State == "CANCELLED" {
			s.reverseHold(ctx, userID, order.ID, totalHold, "circle_transfer_"+string(tx.State))
			return
		}
		s.logger.Info("Circle SOL transfer to Paj initiated",
			zap.String("circle_tx_id", tx.ID), zap.String("paj_order_id", order.ID))
		s.db.ExecContext(ctx, `UPDATE paj_orders SET bridge_transfer_id = $1 WHERE paj_order_id = $2`,
			"circle:"+tx.ID, order.ID)
		// Refund slippage buffer
		actualAmount := decimal.NewFromFloat(totalTransfer)
		excess := totalHold.Sub(actualAmount)
		if excess.IsPositive() && s.ledger != nil {
			s.ledger.ReverseTransaction(ctx, userID, entities.AccountTypeSpendingBalance,
				"paj_offramp_slippage_refund_"+order.ID, excess, map[string]interface{}{
					"provider": "paj", "type": "slippage_refund", "paj_order_id": order.ID,
					"path": "circle_sol",
				})
		}
		return
	}

	// EVM wallet — use ChainRails to bridge to PAJ's Solana address
	if s.chainRailsAdapter != nil {
		s.executeCircleViaCRToPaj(ctx, userID, walletID, tokenID, blockchain, onChainAddress, order, totalHold, totalTransfer)
		return
	}

	s.logger.Error("USDC on EVM but no ChainRails — cannot bridge to Solana for Paj",
		zap.String("blockchain", blockchain), zap.String("user_id", userID.String()))
	s.reverseHold(ctx, userID, order.ID, totalHold, "no_chainrails_evm")
}

// --- Webhook processing ---

// HandleWebhook processes a Paj webhook and updates order status.
// Since Paj has no webhook signature verification, we verify by polling
// the transaction status directly from Paj's API.
func (s *Service) HandleWebhook(ctx context.Context, payload *paj.WebhookPayload) error {
	var orderUserID uuid.UUID
	var currentStatus, orderType string

	err := s.db.QueryRowContext(ctx,
		`SELECT user_id, status, order_type FROM paj_orders WHERE paj_order_id = $1`,
		payload.ID).Scan(&orderUserID, &currentStatus, &orderType)
	if err == sql.ErrNoRows {
		s.logger.Warn("paj webhook for unknown order", zap.String("paj_order_id", payload.ID))
		return nil
	}
	if err != nil {
		return fmt.Errorf("lookup paj order: %w", err)
	}

	if currentStatus == "completed" || currentStatus == "failed" {
		return nil
	}

	// Verify by polling Paj directly — don't trust unsigned webhook payload.
	token, err := s.getSessionToken(ctx, orderUserID)
	if err != nil {
		// SECURITY: Never trust unsigned webhook payloads for financial state transitions.
		// If the session is expired, drop the webhook. The user can check status manually,
		// or a reconciliation worker can poll PAJ later.
		s.logger.Warn("cannot verify paj webhook — session expired, dropping",
			zap.String("paj_order_id", payload.ID), zap.String("order_type", orderType))
		return nil
	}

	tx, err := s.pajClient.GetTransaction(ctx, token, payload.ID)
	if err != nil {
		err = s.invalidateSessionIfUnauthorized(ctx, orderUserID, err)
		s.logger.Warn("paj transaction verification failed, dropping webhook",
			zap.String("paj_order_id", payload.ID), zap.Error(err))
		return nil // Don't trust unverified payload.
	}

	// Use only the verified status from Paj.
	newStatus := mapPajStatus(tx.Status)

	_, err = s.db.ExecContext(ctx, `
		UPDATE paj_orders SET
			status = $1, token_amount = $2, rate = $3,
			last_webhook_status = $4, last_webhook_at = NOW(), updated_at = NOW()
		WHERE paj_order_id = $5 AND status NOT IN ('completed', 'failed')`,
		newStatus, tx.Amount, tx.Rate, tx.Status, payload.ID)
	if err != nil {
		return fmt.Errorf("update paj order: %w", err)
	}

	// Credit user's spend balance when onramp completes (USDC arrived in custody).
	s.creditOnrampIfCompleted(ctx, orderUserID, payload.ID, newStatus, tx)

	// Notify user of onramp status changes
	if orderType == "onramp" && s.notifier != nil {
		if newStatus == "paid" || newStatus == "processing" {
			_ = s.notifier.NotifyDepositDetected(ctx, orderUserID, "NGN")
		}
	}

	// Reverse the ledger hold when an offramp fails.
	s.reverseOfframpIfFailed(ctx, orderUserID, payload.ID, orderType, newStatus)

	// Send push notification for offramp terminal states.
	s.notifyOfframpStatus(ctx, orderUserID, payload.ID, orderType, newStatus, tx)

	s.logger.Info("paj order status updated",
		zap.String("paj_order_id", payload.ID),
		zap.String("type", orderType),
		zap.String("status", newStatus))
	return nil
}

// PollOrderStatus checks order status directly from Paj API.
// Verifies the order belongs to the requesting user.
func (s *Service) PollOrderStatus(ctx context.Context, userID uuid.UUID, pajOrderID string) (*paj.PajTransaction, error) {
	// Verify ownership before polling.
	var ownerID uuid.UUID
	var orderType string
	err := s.db.QueryRowContext(ctx,
		`SELECT user_id, order_type FROM paj_orders WHERE paj_order_id = $1`, pajOrderID).Scan(&ownerID, &orderType)
	if err != nil {
		return nil, fmt.Errorf("order not found")
	}
	if ownerID != userID {
		return nil, fmt.Errorf("order not found")
	}

	token, err := s.getSessionToken(ctx, userID)
	if err != nil {
		return nil, err
	}

	tx, err := s.pajClient.GetTransaction(ctx, token, pajOrderID)
	if err != nil {
		return nil, s.invalidateSessionIfUnauthorized(ctx, userID, err)
	}

	// Update local order status from Paj's response.
	newStatus := mapPajStatus(tx.Status)
	s.db.ExecContext(ctx, `
		UPDATE paj_orders SET status = $1, token_amount = $2, rate = $3, updated_at = NOW()
		WHERE paj_order_id = $4 AND status NOT IN ('completed', 'failed')`,
		newStatus, tx.Amount, tx.Rate, pajOrderID)

	// Credit ledger if onramp just completed (same logic as webhook path).
	s.creditOnrampIfCompleted(ctx, userID, pajOrderID, newStatus, tx)

	// Reverse the ledger hold if offramp failed (same logic as webhook path).
	s.reverseOfframpIfFailed(ctx, userID, pajOrderID, orderType, newStatus)

	// Send push notification for offramp terminal states.
	s.notifyOfframpStatus(ctx, userID, pajOrderID, orderType, newStatus, tx)

	return tx, nil
}

// creditOnrampIfCompleted credits the user's USDC balance and triggers the 70/30
// allocation split when an onramp order completes.
// Called from both HandleWebhook and PollOrderStatus to ensure credit happens
// regardless of which path detects the completion first.
func (s *Service) creditOnrampIfCompleted(ctx context.Context, userID uuid.UUID, pajOrderID, newStatus string, tx *paj.PajTransaction) {
	if newStatus != "completed" || tx.USDCAmount <= 0 {
		return
	}

	// Idempotency: atomically claim this order for crediting.
	// Only one caller (webhook or poll) wins the UPDATE.
	var claimedID uuid.UUID
	err := s.db.QueryRowContext(ctx,
		`UPDATE paj_orders SET deposit_id = gen_random_uuid()
		 WHERE paj_order_id = $1 AND order_type = 'onramp' AND deposit_id IS NULL
		 RETURNING id`, pajOrderID).Scan(&claimedID)
	if err != nil {
		return // Already credited or not an onramp — no-op.
	}

	// When PAJ sends USDC to the user's Bridge wallet, Bridge detects the
	// on-chain deposit and processes it via the normal webhook flow
	// (ProcessCryptoDeposit → allocation split → notification).
	// Check if the order's recipient was a user wallet (stored during CreateOnrampOrder).
	var usedUserWallet bool
	s.db.QueryRowContext(ctx,
		`SELECT COALESCE(used_user_wallet, false) FROM paj_orders WHERE id = $1`, claimedID).Scan(&usedUserWallet)
	if usedUserWallet {
		// Bridge webhook normally handles crediting via ProcessCryptoDeposit.
		// However, the webhook may be delayed or fail silently. Check if the
		// deposit was already credited by looking for a matching ledger entry
		// or deposit record. If found, skip to avoid double-crediting.
		creditAmount := decimal.NewFromFloat(tx.USDCAmount)
		idempotencyKey := "paj-onramp-" + pajOrderID

		var alreadyCredited bool
		// Check 1: Did our own fallback already run? (idempotency_key is on ledger_transactions)
		_ = s.db.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM ledger_transactions WHERE idempotency_key = $1)`,
			idempotencyKey).Scan(&alreadyCredited)
		if !alreadyCredited {
			// Check 2: Did Bridge webhook already credit via deposits table?
			// Match on user + amount within the last hour to catch the Bridge-created record.
			_ = s.db.QueryRowContext(ctx,
				`SELECT EXISTS(SELECT 1 FROM deposits WHERE user_id = $1 AND status IN ('completed','confirmed') AND created_at > NOW() - INTERVAL '1 hour' AND amount = $2)`,
				userID, creditAmount).Scan(&alreadyCredited)
		}
		if alreadyCredited {
			if s.notifier != nil {
				s.notifier.NotifyDepositConfirmed(ctx, userID, creditAmount.StringFixed(2), "NGN", pajOrderID)
			}
			return
		}
		s.logger.Warn("Bridge webhook has not credited PAJ onramp deposit, falling back to direct credit",
			zap.String("user_id", userID.String()),
			zap.String("paj_order_id", pajOrderID),
			zap.String("amount", creditAmount.String()))
		// Fall through to direct credit + allocation below.
	}

	creditAmount := decimal.NewFromFloat(tx.USDCAmount)
	idempotencyKey := "paj-onramp-" + pajOrderID

	// Step 1: Credit USDC balance (Debit entry = increase for asset accounts).
	if s.depositLedger != nil {
		err = s.depositLedger.CreditUSDCBalance(ctx, userID, creditAmount, idempotencyKey, map[string]interface{}{
			"provider": "paj", "type": "onramp_credit", "paj_order_id": pajOrderID,
			"fiat_amount": tx.FiatAmount, "currency": tx.Currency,
		})
		if err != nil {
			s.logger.Error("CRITICAL: failed to credit USDC balance after onramp completion",
				zap.Error(err), zap.String("user_id", userID.String()),
				zap.String("paj_order_id", pajOrderID), zap.Float64("amount", tx.USDCAmount))
			// Roll back the claim so a retry can pick it up.
			s.db.ExecContext(ctx, `UPDATE paj_orders SET deposit_id = NULL WHERE id = $1`, claimedID)
			return
		}
	}

	// Step 2: Trigger 70/30 allocation split (same as Bridge virtual account flow).
	if s.allocationService != nil {
		sourceTxID := "paj-onramp:" + pajOrderID
		depositID := claimedID
		allocationReq := &entities.IncomingFundsRequest{
			UserID:     userID,
			Amount:     creditAmount,
			EventType:  entities.AllocationEventTypeFiatDeposit,
			DepositID:  &depositID,
			SourceTxID: &sourceTxID,
			Metadata: map[string]any{
				"source":       "paj_onramp",
				"paj_order_id": pajOrderID,
				"fiat_amount":  tx.FiatAmount,
				"currency":     tx.Currency,
			},
		}
		if err := s.allocationService.ProcessIncomingFunds(ctx, allocationReq); err != nil {
			s.logger.Error("Failed to process allocation split for PAJ onramp — will retry on next webhook/poll",
				zap.Error(err), zap.String("user_id", userID.String()),
				zap.String("paj_order_id", pajOrderID), zap.String("amount", creditAmount.String()))
			// Reset claim so next webhook/poll retries the allocation.
			// USDC credit is idempotent (same key), so re-entry is safe.
			s.db.ExecContext(ctx, `UPDATE paj_orders SET deposit_id = NULL WHERE id = $1`, claimedID)
			return // Don't persist deposit record, notify, or trigger gameplay until allocation succeeds.
		}
	}

	// Step 3: Persist deposit record so it appears in transaction history.
	if s.depositRepo != nil {
		now := time.Now()
		deposit := &entities.Deposit{
			ID:             claimedID,
			IdempotencyKey: idempotencyKey,
			CorrelationID:  "paj-onramp:" + pajOrderID,
			UserID:         userID,
			Chain:          entities.ChainSOL,
			TxHash:         tx.Signature,
			Token:          entities.StablecoinUSDC,
			Amount:         creditAmount,
			Status:         "confirmed",
			ConfirmedAt:    &now,
			CreatedAt:      now,
		}
		if err := s.depositRepo.Create(ctx, deposit); err != nil {
			s.logger.Warn("Failed to persist PAJ deposit record (non-fatal, ledger already credited)",
				zap.Error(err), zap.String("paj_order_id", pajOrderID))
		}
	}

	// Step 4: Notify user that deposit arrived.
	if s.notifier != nil {
		if err := s.notifier.NotifyDepositConfirmed(ctx, userID, creditAmount.StringFixed(2), "NGN", pajOrderID); err != nil {
			s.logger.Warn("Failed to send PAJ deposit notification", zap.Error(err))
		}
	}

	// Step 5: Trigger gameplay events (XP, streaks, challenges).
	if s.gameplayHooks != nil {
		s.gameplayHooks.OnDeposit(ctx, userID, creditAmount, claimedID)
	}
}

// reverseOfframpIfFailed reverses the spending balance hold when an offramp order fails.
// Uses the same idempotency pattern as creditOnrampIfCompleted.
func (s *Service) reverseOfframpIfFailed(ctx context.Context, userID uuid.UUID, pajOrderID, orderType, newStatus string) {
	if newStatus != "failed" || orderType != "offramp" || s.ledger == nil {
		return
	}

	// Idempotency: atomically claim this order for reversal.
	var holdAmount decimal.Decimal
	err := s.db.QueryRowContext(ctx,
		`UPDATE paj_orders SET deposit_id = gen_random_uuid()
		 WHERE paj_order_id = $1 AND order_type = 'offramp' AND deposit_id IS NULL
		 RETURNING COALESCE(hold_amount, token_amount)`, pajOrderID).Scan(&holdAmount)
	if err != nil || holdAmount.IsZero() || holdAmount.IsNegative() {
		return // Already reversed or no amount to reverse.
	}

	err = s.ledger.ReverseTransaction(ctx, userID, entities.AccountTypeSpendingBalance,
		"paj-offramp-"+pajOrderID, holdAmount, map[string]interface{}{
			"provider": "paj", "type": "offramp_failure_reversal", "paj_order_id": pajOrderID,
		})
	if err != nil {
		s.logger.Error("CRITICAL: failed to reverse offramp hold after failure",
			zap.Error(err), zap.String("user_id", userID.String()),
			zap.String("paj_order_id", pajOrderID), zap.String("amount", holdAmount.String()))
		// Reset claim so next webhook/poll retries the reversal.
		s.db.ExecContext(ctx, `UPDATE paj_orders SET deposit_id = NULL WHERE paj_order_id = $1`, pajOrderID)
	}
}

// OfframpResult wraps the Paj order with Rail-specific fee info for the API response.
type OfframpResult struct {
	Order   *paj.OfframpOrder
	RailFee float64
}

// PajOrder represents a persisted Paj order for transaction history.
type PajOrder struct {
	ID                string    `db:"paj_order_id" json:"orderId"`
	OrderType         string    `db:"order_type" json:"orderType"`
	Status            string    `db:"status" json:"status"`
	FiatAmount        float64   `db:"fiat_amount" json:"fiatAmount"`
	TokenAmount       float64   `db:"token_amount" json:"tokenAmount"`
	Currency          string    `db:"currency" json:"currency"`
	Rate              float64   `db:"rate" json:"rate"`
	Fee               float64   `db:"fee" json:"fee"`
	BankID            *string   `db:"bank_id" json:"bankId,omitempty"`
	BankAccountNumber *string   `db:"bank_account_number" json:"bankAccountNumber,omitempty"`
	BankAccountName   *string   `db:"bank_account_name" json:"bankAccountName,omitempty"`
	CreatedAt         time.Time `db:"created_at" json:"createdAt"`
}

// notifyOfframpStatus sends a push notification when an offramp reaches a terminal state.
func (s *Service) notifyOfframpStatus(ctx context.Context, userID uuid.UUID, pajOrderID, orderType, status string, tx *paj.PajTransaction) {
	if s.notifier == nil || orderType != "offramp" {
		return
	}
	amount := fmt.Sprintf("₦%.0f", tx.FiatAmount)
	switch status {
	case "completed":
		s.notifier.NotifyWithdrawalCompleted(ctx, userID, amount, "bank account")
	case "failed":
		s.notifier.NotifyWithdrawalFailed(ctx, userID, amount, "Transaction failed. Funds returned to your balance.")
	}
}

// resolveAccountName looks up the account holder name from Paj saved bank accounts.
func (s *Service) resolveAccountName(ctx context.Context, token, accountNumber string) *string {
	accounts, err := s.pajClient.GetBankAccounts(ctx, token)
	if err != nil {
		return nil
	}
	for _, a := range accounts {
		if a.AccountNumber == accountNumber {
			return &a.AccountName
		}
	}
	return nil
}

// GetOrders returns the user's Paj order history for transaction display.
func (s *Service) GetOrders(ctx context.Context, userID uuid.UUID) ([]PajOrder, error) {
	var orders []PajOrder
	err := s.db.SelectContext(ctx, &orders, `
		SELECT paj_order_id, order_type, status, COALESCE(fiat_amount, 0) as fiat_amount,
			COALESCE(token_amount, 0) as token_amount, COALESCE(currency, 'NGN') as currency,
			COALESCE(rate, 0) as rate, COALESCE(fee, 0) as fee,
			bank_id, bank_account_number, bank_account_name, created_at
		FROM paj_orders WHERE user_id = $1
		  AND (status = 'completed' OR status = 'paid'
		       OR (status IN ('pending', 'failed') AND created_at > NOW() - interval '24 hours'))
		ORDER BY created_at DESC LIMIT 50`, userID)
	if err != nil {
		return nil, fmt.Errorf("get paj orders: %w", err)
	}
	return orders, nil
}

// reverseHold reverses the full ledger hold for a failed offramp and marks the order
// as failed with deposit_id set (prevents double-reversal by worker or webhook).
func (s *Service) reverseHold(ctx context.Context, userID uuid.UUID, pajOrderID string, amount decimal.Decimal, reason string) {
	// Mark order as failed and claim it (same idempotency as worker/webhook).
	s.db.ExecContext(ctx, `
		UPDATE paj_orders SET status = 'failed', deposit_id = gen_random_uuid(), updated_at = NOW()
		WHERE paj_order_id = $1 AND status = 'pending'`, pajOrderID)

	if s.ledger == nil {
		return
	}
	err := s.ledger.ReverseTransaction(ctx, userID, entities.AccountTypeSpendingBalance,
		"paj_offramp_"+reason+"_"+pajOrderID, amount, map[string]interface{}{
			"provider": "paj", "type": "offramp_" + reason + "_reversal", "paj_order_id": pajOrderID,
		})
	if err != nil {
		s.logger.Error("CRITICAL: failed to reverse offramp hold",
			zap.Error(err), zap.String("user_id", userID.String()),
			zap.String("paj_order_id", pajOrderID), zap.String("amount", amount.String()))
	}
}

// acquireOfframpLock acquires a PostgreSQL advisory lock for the user's offramp operations.
func (s *Service) acquireOfframpLock(ctx context.Context, userID uuid.UUID) (func(), error) {
	h := fnv.New64a()
	b := [16]byte(userID)
	h.Write(b[:])
	// Use a different key space than withdrawal service (add offset)
	key := int64(binary.BigEndian.Uint64(h.Sum(nil)[:8])) + 1000000

	deadline := time.Now().Add(5 * time.Second)
	for {
		var acquired bool
		err := s.db.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&acquired)
		if err != nil {
			s.logger.Error("advisory lock query failed", zap.Error(err), zap.String("user_id", userID.String()))
			return nil, fmt.Errorf("failed to acquire offramp lock: %w", err)
		}
		if acquired {
			return func() {
				s.db.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", key)
			}, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout acquiring offramp lock")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func mapPajStatus(pajStatus string) string {
	switch pajStatus {
	case "INIT":
		return "pending"
	case "PAID":
		return "paid"
	case "COMPLETED":
		return "completed"
	case "FAILED":
		return "failed"
	default:
		return "pending"
	}
}
