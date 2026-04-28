package transaction

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

type TransactionControlService struct {
	logger      *zap.Logger
	userChecker UserChecker
}

// UserChecker verifies user account status before allowing transactions.
type UserChecker interface {
	GetByID(ctx context.Context, id uuid.UUID) (*entities.UserProfile, error)
}

func NewTransactionControlService(logger *zap.Logger, userChecker UserChecker) *TransactionControlService {
	return &TransactionControlService{logger: logger, userChecker: userChecker}
}

func (s *TransactionControlService) CheckLimit(ctx context.Context, userID uuid.UUID, limitType entities.LimitType, amount decimal.Decimal, limit *entities.TransactionLimit) error {
	// Check user account status before evaluating limits
	if s.userChecker != nil {
		user, err := s.userChecker.GetByID(ctx, userID)
		if err != nil {
			return fmt.Errorf("failed to verify user status: %w", err)
		}
		if !user.IsActive {
			return fmt.Errorf("account is not active")
		}
		if user.WithdrawalsFrozen {
			return fmt.Errorf("account withdrawals are frozen")
		}
	}

	if limit.ResetAt.Before(time.Now()) {
		limit.UsedAmount = decimal.Zero
		limit.ResetAt = s.calculateNextReset(limit.Period)
	}

	newUsed := limit.UsedAmount.Add(amount)
	if newUsed.GreaterThan(limit.MaxAmount) {
		return fmt.Errorf("transaction limit exceeded: %s limit of %s", limit.Period, limit.MaxAmount.String())
	}

	return nil
}

func (s *TransactionControlService) calculateNextReset(period entities.LimitPeriod) time.Time {
	now := time.Now()
	switch period {
	case entities.LimitPeriodDaily:
		return now.AddDate(0, 0, 1).Truncate(24 * time.Hour)
	case entities.LimitPeriodWeekly:
		return now.AddDate(0, 0, 7).Truncate(24 * time.Hour)
	case entities.LimitPeriodMonthly:
		return now.AddDate(0, 1, 0).Truncate(24 * time.Hour)
	default:
		return now.AddDate(0, 0, 1).Truncate(24 * time.Hour)
	}
}

func (s *TransactionControlService) CalculateFraudScore(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, txType string) (decimal.Decimal, map[string]interface{}) {
	score := decimal.Zero
	factors := make(map[string]interface{})

	if amount.GreaterThan(decimal.NewFromInt(10000)) {
		score = score.Add(decimal.NewFromFloat(0.3))
		factors["high_amount"] = true
	}

	factors["tx_type"] = txType
	
	return score, factors
}
