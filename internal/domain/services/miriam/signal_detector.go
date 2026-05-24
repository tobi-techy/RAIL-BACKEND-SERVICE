package miriam

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// ContextSignalStore persists context signals.
type ContextSignalStore interface {
	Upsert(ctx context.Context, s *entities.UserContextSignal) error
	GetActiveByUser(ctx context.Context, userID uuid.UUID) ([]entities.UserContextSignal, error)
	GetByType(ctx context.Context, userID uuid.UUID, signalType string) ([]entities.UserContextSignal, error)
	Deactivate(ctx context.Context, id uuid.UUID) error
}

// SignalDetector populates context signals from transaction patterns,
// balance changes, and behavioral data.
type SignalDetector struct {
	store       ContextSignalStore
	spending    SpendingProvider
	obligations ObligationProvider
	balances    BalanceProvider
	logger      *zap.Logger
}

// NewSignalDetector creates a signal detector.
func NewSignalDetector(
	store ContextSignalStore,
	spending SpendingProvider,
	obligations ObligationProvider,
	balances BalanceProvider,
	logger *zap.Logger,
) *SignalDetector {
	return &SignalDetector{
		store: store, spending: spending, obligations: obligations,
		balances: balances, logger: logger,
	}
}

// DetectAndUpsert runs all signal detections for a user and upserts them.
func (d *SignalDetector) DetectAndUpsert(ctx context.Context, userID uuid.UUID) error {
	now := time.Now().UTC()

	if err := d.detectPayday(ctx, userID, now); err != nil && d.logger != nil {
		d.logger.Debug("payday detection failed", zap.String("user_id", userID.String()), zap.Error(err))
	}
	if err := d.detectSpendingSpike(ctx, userID, now); err != nil && d.logger != nil {
		d.logger.Debug("spending spike detection failed", zap.String("user_id", userID.String()), zap.Error(err))
	}
	if err := d.detectLowBalance(ctx, userID, now); err != nil && d.logger != nil {
		d.logger.Debug("low balance detection failed", zap.String("user_id", userID.String()), zap.Error(err))
	}
	if err := d.detectRecurringExpense(ctx, userID, now); err != nil && d.logger != nil {
		d.logger.Debug("recurring expense detection failed", zap.String("user_id", userID.String()), zap.Error(err))
	}

	return nil
}

func (d *SignalDetector) detectPayday(ctx context.Context, userID uuid.UUID, now time.Time) error {
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	flow, err := d.spending.GetMoneyFlow(ctx, userID, monthStart, now)
	if err != nil || flow == nil {
		return nil
	}

	if flow.DepositCount == 0 || flow.TotalDeposits.IsZero() {
		return nil
	}

	avgAmount := flow.TotalDeposits.Div(decimal.NewFromInt(int64(flow.DepositCount)))
	key := "monthly_deposit"
	dayOfMonth := now.Day()

	// Detect cadence from deposit count
	cadence := "monthly"
	if flow.DepositCount >= 4 {
		cadence = "weekly"
		key = "weekly_deposit"
	} else if flow.DepositCount >= 2 {
		cadence = "biweekly"
		key = "biweekly_deposit"
	}

	data := map[string]interface{}{
		"key":              key,
		"day_of_month":     dayOfMonth,
		"typical_amount":   avgAmount.InexactFloat64(),
		"source":           "deposit_pattern",
		"occurrence_count": flow.DepositCount,
		"cadence":          cadence,
	}

	return d.store.Upsert(ctx, &entities.UserContextSignal{
		ID:         uuid.NewSHA1(uuid.NameSpaceOID, []byte("signal-payday-"+userID.String())),
		UserID:     userID,
		SignalType: entities.SignalPaydayDetected,
		SignalData: mustJSON(data),
		Confidence: decimal.NewFromFloat(0.7 + float64(flow.DepositCount)*0.05),
		IsActive:   true,
		LastSeenAt: now,
	})
}

func (d *SignalDetector) detectSpendingSpike(ctx context.Context, userID uuid.UUID, now time.Time) error {
	thisWeekStart := now.AddDate(0, 0, -7)
	lastWeekStart := now.AddDate(0, 0, -14)

	thisWeek, err1 := d.spending.GetMoneyFlow(ctx, userID, thisWeekStart, now)
	lastWeek, err2 := d.spending.GetMoneyFlow(ctx, userID, lastWeekStart, thisWeekStart)

	if err1 != nil || err2 != nil || thisWeek == nil || lastWeek == nil {
		return nil
	}

	thisOut := totalOut(thisWeek)
	lastOut := totalOut(lastWeek)

	if !lastOut.IsPositive() {
		return nil
	}

	spikeRatio := thisOut.Div(lastOut).InexactFloat64()
	if spikeRatio < 1.5 {
		return nil
	}

	data := map[string]interface{}{
		"key":            "spending_spike",
		"category":       "overall",
		"current_amount": thisOut.InexactFloat64(),
		"average_amount": lastOut.InexactFloat64(),
		"spike_ratio":    spikeRatio,
	}

	return d.store.Upsert(ctx, &entities.UserContextSignal{
		ID:         uuid.NewSHA1(uuid.NameSpaceOID, []byte("signal-spike-"+userID.String())),
		UserID:     userID,
		SignalType: entities.SignalSpendingSpike,
		SignalData: mustJSON(data),
		Confidence: decimal.NewFromFloat(math.Min(0.95, 0.6+spikeRatio*0.1)),
		IsActive:   true,
		LastSeenAt: now,
	})
}

func (d *SignalDetector) detectLowBalance(ctx context.Context, userID uuid.UUID, now time.Time) error {
	spend, err := d.balances.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return nil
	}

	var upcoming decimal.Decimal
	if d.obligations != nil {
		obligations, err := d.obligations.ListActive(ctx, userID)
		if err == nil {
			for _, o := range obligations {
				if !usdLike(o.Currency) {
					continue
				}
				upcoming = upcoming.Add(o.Amount)
			}
		}
	}

	// Low balance: spend < upcoming obligations OR spend < $50
	isLow := false
	threshold := "obligation_coverage"
	if upcoming.IsPositive() && spend.LessThan(upcoming) {
		isLow = true
	} else if spend.LessThan(decimal.NewFromInt(50)) {
		isLow = true
		threshold = "absolute_floor"
	}

	if !isLow {
		return nil
	}

	data := map[string]interface{}{
		"key":            "low_balance",
		"spend_balance":  spend.InexactFloat64(),
		"upcoming_bills": upcoming.InexactFloat64(),
		"threshold_type": threshold,
	}

	return d.store.Upsert(ctx, &entities.UserContextSignal{
		ID:         uuid.NewSHA1(uuid.NameSpaceOID, []byte("signal-lowbal-"+userID.String())),
		UserID:     userID,
		SignalType: entities.SignalLowBalance,
		SignalData: mustJSON(data),
		Confidence: decimal.NewFromFloat(0.85),
		IsActive:   true,
		LastSeenAt: now,
	})
}

func (d *SignalDetector) detectRecurringExpense(ctx context.Context, userID uuid.UUID, now time.Time) error {
	// Detect recurring expenses from obligations
	if d.obligations == nil {
		return nil
	}

	obligations, err := d.obligations.ListActive(ctx, userID)
	if err != nil || len(obligations) == 0 {
		return nil
	}

	for _, o := range obligations {
		if !usdLike(o.Currency) || o.DueDay == nil {
			continue
		}

		key := strings.ToLower(o.Name)
		data := map[string]interface{}{
			"key":            "recurring_" + key,
			"category":       o.Type,
			"current_amount": o.Amount.InexactFloat64(),
			"average_amount": o.Amount.InexactFloat64(),
			"spike_ratio":    1.0,
			"due_day":        *o.DueDay,
			"cadence":        o.Cadence,
		}

		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("signal-recurring-"+userID.String()+"-"+o.ID.String()))
		if err := d.store.Upsert(ctx, &entities.UserContextSignal{
			ID:         id,
			UserID:     userID,
			SignalType: entities.SignalRecurringExpense,
			SignalData: mustJSON(data),
			Confidence: decimal.NewFromFloat(0.8),
			IsActive:   true,
			LastSeenAt: now,
		}); err != nil {
			return err
		}
	}

	return nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
