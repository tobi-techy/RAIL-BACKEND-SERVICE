package pajfunding

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
	bridgepkg "github.com/rail-service/rail_service/internal/infrastructure/adapters/bridge"
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
	NotifyWithdrawalCompleted(ctx context.Context, userID uuid.UUID, amount, destination string) error
	NotifyWithdrawalFailed(ctx context.Context, userID uuid.UUID, amount, reason string) error
}

// GameplayHooks triggers gameplay events (XP, streaks, challenges) on deposit.
type GameplayHooks interface {
	OnDeposit(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, depositID uuid.UUID)
}

// WalletProvider looks up a user's Bridge wallet address by chain.
type WalletProvider interface {
	GetWalletByUserAndChain(ctx context.Context, userID uuid.UUID, chain entities.WalletChain) (*entities.ManagedWallet, error)
}

// BridgeCustomerIDProvider resolves a user's Bridge customer ID for transfer API calls.
type BridgeCustomerIDProvider interface {
	GetBridgeCustomerID(ctx context.Context, userID uuid.UUID) (string, error)
}

// WithdrawalLimitsChecker validates withdrawal amounts against daily/monthly limits.
type WithdrawalLimitsChecker interface {
	ValidateWithdrawal(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error
}

// BridgeCryptoTransferAdapter transfers USDC from a Bridge wallet to an external address.
type BridgeCryptoTransferAdapter interface {
	TransferFunds(ctx context.Context, req *bridgepkg.CreateTransferRequest) (*bridgepkg.Transfer, error)
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
	bridgeTransfer    BridgeCryptoTransferAdapter
	bridgeCustomerID  BridgeCustomerIDProvider
	limitsChecker     WithdrawalLimitsChecker
	pajChain          string // blockchain for Paj deposit addresses (e.g. "solana")
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

// SetWalletProvider sets the wallet provider for looking up user Bridge wallet addresses.
func (s *Service) SetWalletProvider(wp WalletProvider) { s.walletProvider = wp }

// SetBridgeTransfer sets the Bridge transfer adapter for sending USDC to Paj deposit addresses.
func (s *Service) SetBridgeTransfer(bt BridgeCryptoTransferAdapter, chain string, customerIDProvider BridgeCustomerIDProvider) {
	s.bridgeTransfer = bt
	s.pajChain = chain
	s.bridgeCustomerID = customerIDProvider
}

// SetGameplayHooks sets the gameplay hooks for triggering XP/streak/challenge events.
func (s *Service) SetGameplayHooks(gh GameplayHooks) { s.gameplayHooks = gh }

// SetLimitsChecker sets the withdrawal limits validator.
func (s *Service) SetLimitsChecker(lc WithdrawalLimitsChecker) { s.limitsChecker = lc }

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

// --- Rates ---

const pajRatesCacheKey = "paj:rates"
const pajRatesCacheTTL = 5 * time.Minute

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
	return s.pajClient.GetBanks(ctx, token)
}

func (s *Service) ResolveBankAccount(ctx context.Context, userID uuid.UUID, bankID, accountNumber string) (*paj.ResolvedAccount, error) {
	token, err := s.getSessionToken(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.pajClient.ResolveBankAccount(ctx, token, bankID, accountNumber)
}

func (s *Service) AddBankAccount(ctx context.Context, userID uuid.UUID, bankID, accountNumber string) (*paj.SavedBankAccount, error) {
	token, err := s.getSessionToken(ctx, userID)
	if err != nil {
		return nil, err
	}
	account, err := s.pajClient.AddBankAccount(ctx, token, bankID, accountNumber)
	if err != nil {
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
	return s.pajClient.GetBankAccounts(ctx, token)
}

// --- Onramp (NGN → USDC) ---

func (s *Service) CreateOnrampOrder(ctx context.Context, userID uuid.UUID, fiatAmount float64, currency string) (*paj.OnrampOrder, error) {
	token, err := s.getSessionToken(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Enforce NGN minimum deposit
	if fiatAmount < 100 {
		return nil, fmt.Errorf("minimum deposit is ₦100")
	}

	// Look up user's Bridge Solana wallet so USDC goes directly to them.
	var recipient string
	if s.walletProvider != nil {
		wallet, err := s.walletProvider.GetWalletByUserAndChain(ctx, userID, entities.WalletChainSolana)
		if err == nil && wallet != nil && wallet.Address != "" {
			recipient = wallet.Address
		} else {
			s.logger.Warn("could not resolve user Solana wallet, falling back to company wallet",
				zap.String("user_id", userID.String()), zap.Error(err))
		}
	}

	order, err := s.pajClient.CreateOnrampOrder(ctx, token, fiatAmount, currency, recipient)
	if err != nil {
		return nil, err
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
		return nil, err
	}

	// Get the rate first to calculate the USDC amount we need to debit.
	rates, err := s.pajClient.GetRates(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get rates: %w", err)
	}
	if rates.OffRampRate.Rate <= 0 {
		return nil, fmt.Errorf("offramp rate unavailable")
	}

	// Bridge requires ≥ $1 USDC per transfer. Enforce the NGN equivalent.
	bridgeMinNGN := rates.OffRampRate.Rate * 1.05 // ₦ equivalent of ~$1.05 (with buffer)
	if fiatAmount < bridgeMinNGN {
		return nil, fmt.Errorf("minimum withdrawal is ₦%.0f", bridgeMinNGN)
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
			reverseErr := s.ledger.ReverseTransaction(ctx, userID, entities.AccountTypeSpendingBalance,
				"paj_offramp_failed", totalHold, map[string]interface{}{
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
		INSERT INTO paj_orders (user_id, paj_order_id, order_type, status, fiat_amount, token_amount, currency, rate, fee, bank_id, bank_account_number, paj_deposit_address, hold_amount)
		VALUES ($1, $2, 'offramp', 'pending', $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		userID, order.ID, order.FiatAmount, order.Amount, currency, order.Rate, order.Fee,
		bankID, accountNumber, order.Address, totalHold)
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

	// Validate prerequisites for Bridge transfer.
	if s.bridgeTransfer == nil || s.walletProvider == nil {
		s.logger.Error("bridge transfer not configured for Paj offramp — reversing hold",
			zap.String("paj_order_id", order.ID))
		s.reverseHold(ctx, userID, order.ID, totalHold, "no_bridge_config")
		return nil, fmt.Errorf("withdrawal infrastructure not available")
	}
	if order.Address == "" {
		s.logger.Error("Paj order missing deposit address — reversing hold",
			zap.String("paj_order_id", order.ID))
		s.reverseHold(ctx, userID, order.ID, totalHold, "no_deposit_address")
		return nil, fmt.Errorf("withdrawal service returned invalid response")
	}

	// Transfer USDC from user's Bridge wallet to Paj's deposit address.
	{
		walletChain := entities.WalletChainSolana // default
		paymentRail := bridgepkg.PaymentRailSolana
		if s.pajChain != "" {
			switch s.pajChain {
			case "BASE":
				walletChain = entities.WalletChainBase
				paymentRail = bridgepkg.PaymentRailBase
			case "POLYGON":
				walletChain = entities.WalletChainPolygon
				paymentRail = bridgepkg.PaymentRailPolygon
			case "ETHEREUM":
				walletChain = entities.WalletChainEthereum
				paymentRail = bridgepkg.PaymentRailEthereum
			}
		}

		wallet, walletErr := s.walletProvider.GetWalletByUserAndChain(ctx, userID, walletChain)
		if walletErr != nil || wallet == nil || wallet.BridgeWalletID == "" {
			s.logger.Error("failed to get user Bridge wallet for Paj offramp — reversing hold",
				zap.Error(walletErr), zap.String("user_id", userID.String()),
				zap.String("paj_order_id", order.ID))
			s.reverseHold(ctx, userID, order.ID, totalHold, "no_wallet")
			return nil, fmt.Errorf("failed to get wallet for withdrawal")
		}

		// Bridge requires the Bridge customer ID (not Rail user ID) for OnBehalfOf.
		bridgeCustID, custErr := s.bridgeCustomerID.GetBridgeCustomerID(ctx, userID)
		if custErr != nil || bridgeCustID == "" {
			s.logger.Error("failed to get Bridge customer ID for Paj offramp — reversing hold",
				zap.Error(custErr), zap.String("user_id", userID.String()))
			s.reverseHold(ctx, userID, order.ID, totalHold, "no_bridge_customer")
			return nil, fmt.Errorf("failed to get customer ID for withdrawal")
		}

		// Round to 2 decimal places to match Bridge API precision requirement.
		// This ensures the stored and transferred amounts are identical.
		transferAmount := math.Round(order.Amount*100) / 100

		transfer, transferErr := s.bridgeTransfer.TransferFunds(ctx, &bridgepkg.CreateTransferRequest{
			OnBehalfOf:   bridgeCustID,
			Amount:       fmt.Sprintf("%.2f", transferAmount),
			Source: bridgepkg.TransferSource{
				PaymentRail:    bridgepkg.PaymentRail("bridge_wallet"),
				Currency:       bridgepkg.CurrencyUSDC,
				BridgeWalletID: wallet.BridgeWalletID,
			},
			Destination: bridgepkg.TransferDestination{
				PaymentRail: paymentRail,
				Currency:    bridgepkg.CurrencyUSDC,
				ToAddress:   order.Address,
			},
		})
		if transferErr != nil {
			s.logger.Error("CRITICAL: Bridge transfer to Paj deposit address failed",
				zap.Error(transferErr), zap.String("user_id", userID.String()),
				zap.String("paj_order_id", order.ID), zap.String("amount", totalHold.String()))
			s.reverseHold(ctx, userID, order.ID, totalHold, "transfer_failed")
			return nil, fmt.Errorf("failed to send USDC to Paj: %w", transferErr)
		}

		// Store the Bridge transfer ID for reconciliation.
		s.db.ExecContext(ctx, `UPDATE paj_orders SET bridge_transfer_id = $1 WHERE paj_order_id = $2`,
			transfer.ID, order.ID)

		// Refund slippage buffer: we debited estimatedUSDC (with 1% buffer)
		// but Paj only needs transferAmount (rounded to 2dp). Return the excess.
		// Rail fee is NOT refunded — it's our revenue.
		actualAmount := decimal.NewFromFloat(transferAmount)
		excess := estimatedUSDC.Sub(actualAmount)
		if excess.IsPositive() && s.ledger != nil {
			refundErr := s.ledger.ReverseTransaction(ctx, userID, entities.AccountTypeSpendingBalance,
				"paj_offramp_slippage_refund_"+order.ID, excess, map[string]interface{}{
					"provider": "paj", "type": "slippage_refund", "paj_order_id": order.ID,
					"estimated": estimatedUSDC.String(), "actual": actualAmount.String(),
				})
			if refundErr != nil {
				s.logger.Error("failed to refund slippage buffer (non-fatal, user overcharged by dust)",
					zap.Error(refundErr), zap.String("user_id", userID.String()),
					zap.String("excess", excess.String()), zap.String("paj_order_id", order.ID))
			}
		}

		s.logger.Info("Bridge transfer to Paj initiated",
			zap.String("paj_order_id", order.ID),
			zap.String("bridge_transfer_id", transfer.ID),
			zap.String("amount_to_paj", actualAmount.String()),
			zap.String("developer_fee", railFee.String()))
	}

	return &OfframpResult{
		Order:   order,
		RailFee: RailNGNWithdrawalFee,
	}, nil
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
		s.logger.Warn("cannot verify paj webhook — no session, dropping",
			zap.String("paj_order_id", payload.ID))
		return nil // Drop unverifiable webhooks — frontend polling will catch up.
	}

	tx, err := s.pajClient.GetTransaction(ctx, token, payload.ID)
	if err != nil {
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
		return nil, err
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
	// We only need to credit here as a fallback when the company wallet was used.
	var recipientIsUserWallet bool
	s.db.QueryRowContext(ctx,
		`SELECT pay_account_name FROM paj_orders WHERE id = $1`, claimedID).Scan(&recipientIsUserWallet)
	// Check if the order's recipient was a user wallet (stored during CreateOnrampOrder).
	var usedUserWallet bool
	s.db.QueryRowContext(ctx,
		`SELECT COALESCE(used_user_wallet, false) FROM paj_orders WHERE id = $1`, claimedID).Scan(&usedUserWallet)
	if usedUserWallet {
		// Bridge webhook handles crediting AND gameplay hooks via ProcessCircleWebhook.
		// Only send notification here; do NOT call OnDeposit to avoid double XP.
		if s.notifier != nil {
			creditAmount := decimal.NewFromFloat(tx.USDCAmount)
			s.notifier.NotifyDepositConfirmed(ctx, userID, creditAmount.StringFixed(2), "NGN", pajOrderID)
		}
		return
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

// GetOrders returns the user's Paj order history for transaction display.
func (s *Service) GetOrders(ctx context.Context, userID uuid.UUID) ([]PajOrder, error) {
	var orders []PajOrder
	err := s.db.SelectContext(ctx, &orders, `
		SELECT paj_order_id, order_type, status, COALESCE(fiat_amount, 0) as fiat_amount,
			COALESCE(token_amount, 0) as token_amount, COALESCE(currency, 'NGN') as currency,
			COALESCE(rate, 0) as rate, COALESCE(fee, 0) as fee,
			bank_id, bank_account_number, created_at
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
			return func() {}, nil // fail open — don't block withdrawals on lock errors
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
