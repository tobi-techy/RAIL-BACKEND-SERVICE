package pajfunding

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
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
	return s.pajClient.AddBankAccount(ctx, token, bankID, accountNumber)
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

	order, err := s.pajClient.CreateOnrampOrder(ctx, token, fiatAmount, currency)
	if err != nil {
		return nil, err
	}

	// Persist order for webhook reconciliation.
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO paj_orders (user_id, paj_order_id, order_type, status, fiat_amount, token_amount, currency, fee, pay_account_number, pay_account_name, pay_bank)
		VALUES ($1, $2, 'onramp', 'pending', $3, $4, $5, $6, $7, $8, $9)`,
		userID, order.ID, order.FiatAmount, order.Amount, currency, order.Fee,
		order.AccountNumber, order.AccountName, order.Bank)
	if err != nil {
		s.logger.Error("failed to persist paj onramp order", zap.Error(err), zap.String("paj_order_id", order.ID))
	}

	return order, nil
}

// --- Offramp (USDC → NGN) ---

func (s *Service) CreateOfframpOrder(ctx context.Context, userID uuid.UUID, bankID, accountNumber string, fiatAmount float64, currency string) (*paj.OfframpOrder, error) {
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

	// Estimate USDC amount: fiatAmount / rate. Add 2% buffer for rate slippage.
	estimatedUSDC := decimal.NewFromFloat(fiatAmount).Div(decimal.NewFromFloat(rates.OffRampRate.Rate))
	estimatedUSDC = estimatedUSDC.Mul(decimal.NewFromFloat(1.02)).Round(2)

	// Check spend balance.
	if s.ledger != nil {
		balance, err := s.ledger.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
		if err != nil {
			return nil, fmt.Errorf("failed to check balance: %w", err)
		}
		if balance.LessThan(estimatedUSDC) {
			return nil, fmt.Errorf("insufficient balance: have %s USDC, need ~%s USDC for ₦%.0f", balance.String(), estimatedUSDC.String(), fiatAmount)
		}

		// Debit spend balance (hold).
		err = s.ledger.CreateTransaction(ctx, userID, entities.AccountTypeSpendingBalance,
			entities.TransactionTypeWithdrawal, estimatedUSDC, map[string]interface{}{
				"provider": "paj", "type": "offramp_hold", "fiat_amount": fiatAmount, "currency": currency,
			})
		if err != nil {
			return nil, fmt.Errorf("failed to debit balance: %w", err)
		}
	}

	// Create Paj offramp order.
	order, err := s.pajClient.CreateOfframpOrder(ctx, token, bankID, accountNumber, fiatAmount, currency)
	if err != nil {
		// Reverse the ledger hold on Paj failure.
		if s.ledger != nil {
			reverseErr := s.ledger.ReverseTransaction(ctx, userID, entities.AccountTypeSpendingBalance,
				"paj_offramp_failed", estimatedUSDC, map[string]interface{}{
					"provider": "paj", "type": "offramp_reversal", "reason": err.Error(),
				})
			if reverseErr != nil {
				s.logger.Error("CRITICAL: failed to reverse ledger hold after Paj failure",
					zap.Error(reverseErr), zap.String("user_id", userID.String()),
					zap.String("amount", estimatedUSDC.String()))
			}
		}
		return nil, err
	}

	_, dbErr := s.db.ExecContext(ctx, `
		INSERT INTO paj_orders (user_id, paj_order_id, order_type, status, fiat_amount, token_amount, currency, rate, fee, bank_id, bank_account_number, paj_deposit_address, hold_amount)
		VALUES ($1, $2, 'offramp', 'pending', $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		userID, order.ID, order.FiatAmount, order.Amount, currency, order.Rate, order.Fee,
		bankID, accountNumber, order.Address, estimatedUSDC)
	if dbErr != nil {
		s.logger.Error("failed to persist paj offramp order", zap.Error(dbErr), zap.String("paj_order_id", order.ID))
	}

	return order, nil
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

func mapPajStatus(pajStatus string) string {
	switch pajStatus {
	case "INIT":
		return "pending"
	case "PAID":
		return "paid"
	case "COMPLETED":
		return "completed"
	default:
		return "failed"
	}
}
