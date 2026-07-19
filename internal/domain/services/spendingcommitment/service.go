package spendingcommitment

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

// Repository persists commitments and daily usage counters.
type Repository interface {
	GetCommitment(ctx context.Context, userID uuid.UUID) (*entities.SpendingCommitment, error)
	UpsertCommitment(ctx context.Context, c *entities.SpendingCommitment) error
	DeactivateCommitment(ctx context.Context, userID uuid.UUID) error
	GetOrCreateUsage(ctx context.Context, userID uuid.UUID, resetAt time.Time) (*entities.SpendingCommitmentUsage, error)
	ResetExpiredUsage(ctx context.Context, userID uuid.UUID, now, nextReset time.Time) error
	AtomicIncrementUsage(ctx context.Context, userID uuid.UUID, cents, limitCents int64) (bool, error)
	DecrementUsage(ctx context.Context, userID uuid.UUID, cents int64) error
}

// FeeCharger debits the flat increase fee via the ledger.
type FeeCharger interface {
	ChargeLimitIncreaseFee(ctx context.Context, userID uuid.UUID, fee decimal.Decimal, idempotencyKey string) error
}

// BalanceReader reads a user's spendable balance (USD) to gate the increase fee.
type BalanceReader interface {
	GetAccountBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error)
}

// ExchangeRateProvider converts non-USD amounts to USD for unified enforcement.
type ExchangeRateProvider interface {
	GetNGNRate(ctx context.Context) (decimal.Decimal, error)
}

// Service manages a user's self-imposed daily spending commitment.
type Service struct {
	repo        Repository
	ledger      FeeCharger
	balances    BalanceReader
	rate        ExchangeRateProvider
	increaseFee decimal.Decimal
	logger      *zap.Logger
}

func NewService(repo Repository, ledger FeeCharger, balances BalanceReader, increaseFee decimal.Decimal, logger *zap.Logger) *Service {
	if !increaseFee.IsPositive() {
		increaseFee = entities.DefaultSpendingCommitmentIncreaseFee
	}
	return &Service{repo: repo, ledger: ledger, balances: balances, increaseFee: increaseFee, logger: logger}
}

// SetRateProvider sets an optional exchange-rate provider for NGN normalization.
func (s *Service) SetRateProvider(rp ExchangeRateProvider) { s.rate = rp }

func nextDailyReset() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
}

// GetStatus returns the current commitment and today's usage.
func (s *Service) GetStatus(ctx context.Context, userID uuid.UUID) (*entities.CommitmentStatusResponse, error) {
	feeCents := s.increaseFee.Mul(decimal.NewFromInt(100)).IntPart()
	commitment, err := s.repo.GetCommitment(ctx, userID)
	if err != nil {
		return nil, err
	}
	if commitment == nil || !commitment.IsActive {
		return &entities.CommitmentStatusResponse{
			Active:           false,
			Currency:         "USD",
			IncreaseFeeCents: feeCents,
		}, nil
	}
	used, err := s.currentUsage(ctx, userID)
	if err != nil {
		return nil, err
	}
	remaining := commitment.DailyLimitCents - used.UsedCents
	if remaining < 0 {
		remaining = 0
	}
	return &entities.CommitmentStatusResponse{
		Active:           true,
		DailyLimitCents:  commitment.DailyLimitCents,
		UsedCents:        used.UsedCents,
		RemainingCents:   remaining,
		Currency:         commitment.Currency,
		ResetsAt:         used.ResetAt.Format(time.RFC3339),
		IncreaseFeeCents: feeCents,
		IncreaseCount:    commitment.IncreaseCount,
	}, nil
}

// SetCommitment sets or updates the daily cap. Lowering is free; raising charges
// a flat fee and requires ConfirmFee=true.
func (s *Service) SetCommitment(ctx context.Context, userID uuid.UUID, req entities.SetCommitmentRequest) (*entities.CommitmentStatusResponse, error) {
	if req.DailyLimitCents <= 0 {
		return nil, entities.ErrInvalidCommitmentLimit
	}
	currency := req.Currency
	if currency == "" {
		currency = "USD"
	}
	// Normalize the requested limit to USD cents for unified enforcement.
	limitUSDCents, err := s.toUSDCents(ctx, req.DailyLimitCents, currency)
	if err != nil {
		return nil, err
	}
	if limitUSDCents <= 0 {
		return nil, entities.ErrInvalidCommitmentLimit
	}

	existing, err := s.repo.GetCommitment(ctx, userID)
	if err != nil {
		return nil, err
	}

	isIncrease := existing != nil && existing.IsActive && limitUSDCents > existing.DailyLimitCents
	if isIncrease {
		if !req.ConfirmFee {
			return nil, entities.ErrIncreaseFeeUnconfirmed
		}
		if err := s.chargeIncreaseFee(ctx, userID, existing.IncreaseCount); err != nil {
			return nil, err
		}
	}

	next := &entities.SpendingCommitment{
		UserID:          userID,
		DailyLimitCents: limitUSDCents,
		Currency:        currency,
		IsActive:        true,
	}
	if existing != nil {
		next.IncreaseCount = existing.IncreaseCount
	}
	if isIncrease {
		now := time.Now().UTC()
		next.IncreaseCount++
		next.LastIncreasedAt = &now
	}
	if err := s.repo.UpsertCommitment(ctx, next); err != nil {
		return nil, err
	}
	return s.GetStatus(ctx, userID)
}

// Deactivate turns off the commitment. This loosens the cap, so it charges the
// flat increase fee (unless no active commitment exists).
func (s *Service) Deactivate(ctx context.Context, userID uuid.UUID, confirmFee bool) (*entities.CommitmentStatusResponse, error) {
	existing, err := s.repo.GetCommitment(ctx, userID)
	if err != nil {
		return nil, err
	}
	if existing == nil || !existing.IsActive {
		return s.GetStatus(ctx, userID)
	}
	if !confirmFee {
		return nil, entities.ErrIncreaseFeeUnconfirmed
	}
	if err := s.chargeIncreaseFee(ctx, userID, existing.IncreaseCount); err != nil {
		return nil, err
	}
	if err := s.repo.DeactivateCommitment(ctx, userID); err != nil {
		return nil, err
	}
	return s.GetStatus(ctx, userID)
}

func (s *Service) chargeIncreaseFee(ctx context.Context, userID uuid.UUID, increaseCount int) error {
	if s.balances != nil {
		balance, err := s.balances.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
		if err != nil {
			return fmt.Errorf("check spend balance: %w", err)
		}
		if balance.LessThan(s.increaseFee) {
			return entities.ErrInsufficientForFee
		}
	}
	idemKey := fmt.Sprintf("limit-increase-fee-%s-%d", userID.String(), increaseCount)
	if err := s.ledger.ChargeLimitIncreaseFee(ctx, userID, s.increaseFee, idemKey); err != nil {
		return entities.ErrInsufficientForFee
	}
	return nil
}

// CheckOutflow returns ErrCommitmentExceeded if amount would breach the daily cap.
// No-op when the user has no active commitment.
func (s *Service) CheckOutflow(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, currency string) error {
	commitment, err := s.repo.GetCommitment(ctx, userID)
	if err != nil || commitment == nil || !commitment.IsActive {
		return nil // fail-open on lookup error: enforcement must never block money movement on infra fault
	}
	usage, err := s.currentUsage(ctx, userID)
	if err != nil {
		return nil
	}
	cents, err := s.toUSDCents(ctx, amount.Mul(decimal.NewFromInt(100)).IntPart(), currency)
	if err != nil {
		return nil
	}
	if usage.UsedCents+cents > commitment.DailyLimitCents {
		return entities.ErrCommitmentExceeded
	}
	return nil
}

// RecordOutflow adds a completed outflow to today's usage counter.
func (s *Service) RecordOutflow(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, currency string) error {
	commitment, err := s.repo.GetCommitment(ctx, userID)
	if err != nil || commitment == nil || !commitment.IsActive {
		return nil
	}
	if _, err := s.currentUsage(ctx, userID); err != nil {
		return err
	}
	cents, err := s.toUSDCents(ctx, amount.Mul(decimal.NewFromInt(100)).IntPart(), currency)
	if err != nil || cents <= 0 {
		return nil
	}
	// Increment unconditionally (allow the counter to exceed the cap so a
	// settled-above-limit outflow is reflected); cap enforcement happens in
	// CheckOutflow before authorization.
	if _, err := s.repo.AtomicIncrementUsage(ctx, userID, cents, int64(1<<62)); err != nil {
		s.logger.Warn("record commitment outflow failed", zap.String("user_id", userID.String()), zap.Error(err))
	}
	return nil
}

// ReleaseOutflow reverses a previously recorded outflow (e.g. reversed card hold).
func (s *Service) ReleaseOutflow(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, currency string) error {
	commitment, err := s.repo.GetCommitment(ctx, userID)
	if err != nil || commitment == nil || !commitment.IsActive {
		return nil
	}
	cents, err := s.toUSDCents(ctx, amount.Mul(decimal.NewFromInt(100)).IntPart(), currency)
	if err != nil || cents <= 0 {
		return nil
	}
	return s.repo.DecrementUsage(ctx, userID, cents)
}

func (s *Service) currentUsage(ctx context.Context, userID uuid.UUID) (*entities.SpendingCommitmentUsage, error) {
	reset := nextDailyReset()
	if _, err := s.repo.GetOrCreateUsage(ctx, userID, reset); err != nil {
		return nil, err
	}
	if err := s.repo.ResetExpiredUsage(ctx, userID, time.Now().UTC(), reset); err != nil {
		s.logger.Warn("reset commitment usage failed", zap.String("user_id", userID.String()), zap.Error(err))
	}
	return s.repo.GetOrCreateUsage(ctx, userID, reset)
}

// toUSDCents converts a cents amount in the given currency to USD cents.
func (s *Service) toUSDCents(ctx context.Context, cents int64, currency string) (int64, error) {
	switch strings.ToUpper(strings.TrimSpace(currency)) {
	case "", "USD", "USDC":
		return cents, nil
	case "NGN":
		rate := s.ngnRate(ctx)
		if !rate.IsPositive() {
			return 0, fmt.Errorf("NGN exchange rate unavailable")
		}
		usd := decimal.NewFromInt(cents).Div(rate).Round(0)
		return usd.IntPart(), nil
	default:
		return cents, nil
	}
}

func (s *Service) ngnRate(ctx context.Context) decimal.Decimal {
	if s.rate == nil {
		return decimal.Zero
	}
	rate, err := s.rate.GetNGNRate(ctx)
	if err != nil {
		return decimal.Zero
	}
	return rate
}
