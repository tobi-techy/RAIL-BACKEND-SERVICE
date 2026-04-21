package subscription

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// Repository defines data access for subscriptions
type Repository interface {
	CreateSubscription(ctx context.Context, s *entities.Subscription) error
	GetSubscription(ctx context.Context, userID uuid.UUID) (*entities.Subscription, error)
	UpdateSubscription(ctx context.Context, s *entities.Subscription) error
	CreateCharge(ctx context.Context, c *entities.SubscriptionCharge) error
	GetDueSubscriptions(ctx context.Context) ([]*entities.Subscription, error)
	CountFailedCharges(ctx context.Context, subscriptionID uuid.UUID, periodStart time.Time) (int, error)
}

// LedgerService handles the actual balance debit/credit
type LedgerService interface {
	GetAccountBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error)
	GetOrCreateUserAccount(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (*entities.LedgerAccount, error)
	GetSystemAccount(ctx context.Context, accountType entities.AccountType) (*entities.LedgerAccount, error)
	CreateTransaction(ctx context.Context, req *entities.CreateTransactionRequest) (*entities.LedgerTransaction, error)
}

// PushNotifier sends push notifications
type PushNotifier interface {
	SendToUser(ctx context.Context, userID uuid.UUID, title, body string, data map[string]interface{}) error
}

// CacheStore for caching pro status
type CacheStore interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}

const maxRetries = 3

// Service manages Pro subscriptions
type Service struct {
	repo     Repository
	ledger   LedgerService
	notifier PushNotifier
	cache    CacheStore
	logger   *zap.Logger
}

func NewService(repo Repository, ledger LedgerService, notifier PushNotifier, logger *zap.Logger) *Service {
	return &Service{repo: repo, ledger: ledger, notifier: notifier, logger: logger}
}

func (s *Service) SetCache(cache CacheStore) { s.cache = cache }

// SetNotifier sets the push notifier (called after DI wiring resolves push provider)
func (s *Service) SetNotifier(n PushNotifier) { s.notifier = n }

// Subscribe creates a new Pro subscription and charges immediately
func (s *Service) Subscribe(ctx context.Context, userID uuid.UUID, plan string) (*entities.Subscription, error) {
	// Check for existing active subscription
	existing, err := s.repo.GetSubscription(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get subscription: %w", err)
	}
	if existing != nil && existing.IsActive() {
		return existing, nil // Already subscribed
	}

	// Validate plan
	days, ok := entities.PlanDuration[plan]
	if !ok {
		plan = "pro_monthly"
		days = 30
	}

	now := time.Now()
	periodEnd := now.Add(time.Duration(days) * 24 * time.Hour)

	sub := &entities.Subscription{
		ID:                 uuid.New(),
		UserID:             userID,
		Plan:               plan,
		Status:             entities.SubscriptionStatusPastDue,
		StartedAt:          now,
		CurrentPeriodStart: now,
		CurrentPeriodEnd:   periodEnd,
	}

	if err := s.repo.CreateSubscription(ctx, sub); err != nil {
		return nil, fmt.Errorf("create subscription: %w", err)
	}

	// Charge immediately — only activate if charge succeeds
	if err := s.ChargeSubscription(ctx, sub); err != nil {
		s.logger.Error("Failed initial subscription charge", zap.Error(err))
	} else {
		sub.Status = entities.SubscriptionStatusActive
		if err := s.repo.UpdateSubscription(ctx, sub); err != nil {
			s.logger.Error("Failed to activate subscription after charge", zap.Error(err))
		}
	}

	s.invalidateCache(ctx, userID)
	return sub, nil
}

// Cancel marks subscription as cancelled (stays active until period end)
func (s *Service) Cancel(ctx context.Context, userID uuid.UUID) error {
	sub, err := s.repo.GetSubscription(ctx, userID)
	if err != nil || sub == nil {
		return fmt.Errorf("no subscription found")
	}
	now := time.Now()
	sub.Status = entities.SubscriptionStatusCancelled
	sub.CancelledAt = &now
	if err := s.repo.UpdateSubscription(ctx, sub); err != nil {
		return fmt.Errorf("update subscription: %w", err)
	}
	s.invalidateCache(ctx, userID)
	return nil
}

// GetSubscription returns the user's subscription
func (s *Service) GetSubscription(ctx context.Context, userID uuid.UUID) (*entities.Subscription, error) {
	return s.repo.GetSubscription(ctx, userID)
}

// IsProUser checks if a user has an active Pro subscription (cached)
func (s *Service) IsProUser(ctx context.Context, userID uuid.UUID) (bool, error) {
	if s.cache != nil {
		key := fmt.Sprintf("pro:%s", userID)
		if val, err := s.cache.Get(ctx, key); err == nil && val != "" {
			return val == "1", nil
		}
	}

	sub, err := s.repo.GetSubscription(ctx, userID)
	if err != nil {
		return false, err
	}
	isPro := sub != nil && sub.IsActive()

	if s.cache != nil {
		key := fmt.Sprintf("pro:%s", userID)
		val := "0"
		if isPro {
			val = "1"
		}
		s.cache.Set(ctx, key, val, 5*time.Minute)
	}
	return isPro, nil
}

// ChargeSubscription debits spend balance and credits subscription revenue
func (s *Service) ChargeSubscription(ctx context.Context, sub *entities.Subscription) error {
	priceStr, ok := entities.PlanPrice[sub.Plan]
	if !ok {
		priceStr = entities.ProSubscriptionPrice
	}
	amount, _ := decimal.NewFromString(priceStr)

	// Check balance first
	balance, err := s.ledger.GetAccountBalance(ctx, sub.UserID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return fmt.Errorf("get balance: %w", err)
	}

	charge := &entities.SubscriptionCharge{
		ID:             uuid.New(),
		SubscriptionID: sub.ID,
		UserID:         sub.UserID,
		Amount:         amount,
		PeriodStart:    sub.CurrentPeriodStart,
		PeriodEnd:      sub.CurrentPeriodEnd,
	}

	if balance.LessThan(amount) {
		charge.Status = entities.ChargeStatusInsufficientFunds
		s.repo.CreateCharge(ctx, charge)
		s.handleFailedCharge(ctx, sub)
		return fmt.Errorf("insufficient spend balance")
	}

	// Execute ledger transaction
	spendAccount, err := s.ledger.GetOrCreateUserAccount(ctx, sub.UserID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return fmt.Errorf("get spend account: %w", err)
	}
	revenueAccount, err := s.ledger.GetSystemAccount(ctx, entities.AccountTypeSubscriptionRevenue)
	if err != nil {
		return fmt.Errorf("get revenue account: %w", err)
	}

	desc := fmt.Sprintf("Rail Pro subscription: %s", sub.CurrentPeriodStart.Format("Jan 2006"))
	idempotencyKey := fmt.Sprintf("sub-charge-%s-%s", sub.ID, sub.CurrentPeriodStart.Format("2006-01-02"))
	refType := "subscription_charge"

	tx, err := s.ledger.CreateTransaction(ctx, &entities.CreateTransactionRequest{
		UserID:          &sub.UserID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		ReferenceType:   &refType,
		IdempotencyKey:  idempotencyKey,
		Description:     &desc,
		Entries: []entities.CreateEntryRequest{
			{AccountID: spendAccount.ID, EntryType: entities.EntryTypeCredit, Amount: amount, Currency: "USD", Description: &desc},
			{AccountID: revenueAccount.ID, EntryType: entities.EntryTypeDebit, Amount: amount, Currency: "USD", Description: &desc},
		},
	})
	if err != nil {
		charge.Status = entities.ChargeStatusFailed
		s.repo.CreateCharge(ctx, charge)
		return fmt.Errorf("ledger transaction: %w", err)
	}

	now := time.Now()
	charge.Status = entities.ChargeStatusCompleted
	charge.LedgerTransactionID = &tx.ID
	charge.ChargedAt = &now
	s.repo.CreateCharge(ctx, charge)

	return nil
}

func (s *Service) handleFailedCharge(ctx context.Context, sub *entities.Subscription) {
	failCount, _ := s.repo.CountFailedCharges(ctx, sub.ID, sub.CurrentPeriodStart)
	if failCount >= maxRetries {
		sub.Status = entities.SubscriptionStatusExpired
		s.repo.UpdateSubscription(ctx, sub)
		s.invalidateCache(ctx, sub.UserID)
		if s.notifier != nil {
			s.notifier.SendToUser(ctx, sub.UserID,
				"Rail Pro Expired",
				"Your subscription has expired after multiple failed charges. Subscribe again anytime.",
				map[string]interface{}{"type": "subscription_expired"})
		}
	} else {
		sub.Status = entities.SubscriptionStatusPastDue
		s.repo.UpdateSubscription(ctx, sub)
		if s.notifier != nil {
			s.notifier.SendToUser(ctx, sub.UserID,
				"Rail Pro Payment Failed",
				"We couldn't charge your spend balance for Rail Pro. Add funds to keep your subscription active.",
				map[string]interface{}{"type": "subscription_past_due"})
		}
	}
}

// RenewDueSubscriptions finds and charges all due subscriptions. Called by billing worker.
func (s *Service) RenewDueSubscriptions(ctx context.Context) (charged, failed int) {
	subs, err := s.repo.GetDueSubscriptions(ctx)
	if err != nil {
		s.logger.Error("Failed to get due subscriptions", zap.Error(err))
		return
	}
	for _, sub := range subs {
		// Calculate new period but don't mutate until charge succeeds
		newPeriodStart := sub.CurrentPeriodEnd
		newPeriodEnd := sub.CurrentPeriodEnd.Add(30 * 24 * time.Hour)

		// Set new period for the charge (idempotency key uses period_start)
		sub.CurrentPeriodStart = newPeriodStart
		sub.CurrentPeriodEnd = newPeriodEnd

		if err := s.ChargeSubscription(ctx, sub); err != nil {
			// Revert period so handleFailedCharge uses the original period
			sub.CurrentPeriodStart = newPeriodStart
			sub.CurrentPeriodEnd = newPeriodStart // keep end = old end so it gets picked up again
			s.logger.Warn("Subscription charge failed", zap.String("user_id", sub.UserID.String()), zap.Error(err))
			failed++
			continue
		}
		sub.Status = entities.SubscriptionStatusActive
		s.repo.UpdateSubscription(ctx, sub)
		s.invalidateCache(ctx, sub.UserID)
		charged++
	}
	return
}

func (s *Service) invalidateCache(ctx context.Context, userID uuid.UUID) {
	if s.cache != nil {
		s.cache.Delete(ctx, fmt.Sprintf("pro:%s", userID))
	}
}
