package stashlock

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// Repository persists stash lock cycles.
type Repository interface {
	Create(ctx context.Context, cycle *entities.StashLockCycle) error
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.StashLockCycle, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status string) error
	GetExpiredWindows(ctx context.Context, now time.Time, limit int) ([]*entities.StashLockCycle, error)
}

// Service manages the 90-day lock / 7-day window cycle for stash funds.
type Service struct {
	repo   Repository
	logger *zap.Logger
}

func NewService(repo Repository, logger *zap.Logger) *Service {
	return &Service{repo: repo, logger: logger}
}

// RecordDeposit creates a new lock cycle when stash funds are deposited.
func (s *Service) RecordDeposit(ctx context.Context, userID, depositID uuid.UUID, amount decimal.Decimal) error {
	now := time.Now()
	lockEnd := now.Add(entities.StashLockDuration)
	cycle := &entities.StashLockCycle{
		ID:        uuid.New(),
		UserID:    userID,
		DepositID: depositID,
		Amount:    amount,
		LockStart: now,
		LockEnd:   lockEnd,
		WindowEnd: lockEnd.Add(entities.StashWindowDuration),
		Status:    entities.StashCycleStatusLocked,
	}
	return s.repo.Create(ctx, cycle)
}

// CanWithdraw returns true if the user has at least one cycle in an open window.
func (s *Service) CanWithdraw(ctx context.Context, userID uuid.UUID) (bool, time.Time, error) {
	cycles, err := s.repo.GetByUserID(ctx, userID)
	if err != nil {
		return false, time.Time{}, err
	}
	now := time.Now()
	for _, c := range cycles {
		if c.IsWithdrawable(now) {
			return true, c.WindowEnd, nil
		}
	}
	return false, time.Time{}, nil
}

// NextUnlockTime returns when the earliest locked cycle becomes withdrawable.
func (s *Service) NextUnlockTime(ctx context.Context, userID uuid.UUID) (*time.Time, error) {
	cycles, err := s.repo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	var earliest *time.Time
	for _, c := range cycles {
		if c.Status == entities.StashCycleStatusLocked {
			t := c.LockEnd
			if earliest == nil || t.Before(*earliest) {
				earliest = &t
			}
		}
	}
	return earliest, nil
}

// MarkWithdrawn marks all open-window cycles as withdrawn for a user.
func (s *Service) MarkWithdrawn(ctx context.Context, userID uuid.UUID) error {
	cycles, err := s.repo.GetByUserID(ctx, userID)
	if err != nil {
		return err
	}
	now := time.Now()
	for _, c := range cycles {
		if c.IsWithdrawable(now) {
			if err := s.repo.UpdateStatus(ctx, c.ID, entities.StashCycleStatusWithdrawn); err != nil {
				return fmt.Errorf("stashlock: mark withdrawn: %w", err)
			}
		}
	}
	return nil
}

// RelockExpiredWindows finds cycles whose 7-day window has passed without withdrawal
// and starts a new 90-day lock. Run this as a daily cron job.
func (s *Service) RelockExpiredWindows(ctx context.Context) (int, error) {
	const batchSize = 500
	now := time.Now()
	total := 0
	for {
		expired, err := s.repo.GetExpiredWindows(ctx, now, batchSize)
		if err != nil {
			return total, err
		}
		if len(expired) == 0 {
			break
		}
		for _, c := range expired {
			lockEnd := now.Add(entities.StashLockDuration)
			newCycle := &entities.StashLockCycle{
				ID:        uuid.New(),
				UserID:    c.UserID,
				DepositID: c.DepositID,
				Amount:    c.Amount,
				LockStart: now,
				LockEnd:   lockEnd,
				WindowEnd: lockEnd.Add(entities.StashWindowDuration),
				Status:    entities.StashCycleStatusLocked,
			}
			// Create new cycle first — if this fails, old cycle stays window_open (safe).
			if err := s.repo.Create(ctx, newCycle); err != nil {
				s.logger.Error("Failed to create relock cycle", zap.String("user_id", c.UserID.String()), zap.Error(err))
				continue
			}
			// Only mark old cycle relocked after new one is persisted.
			if err := s.repo.UpdateStatus(ctx, c.ID, entities.StashCycleStatusRelocked); err != nil {
				s.logger.Error("Failed to mark cycle relocked", zap.String("cycle_id", c.ID.String()), zap.Error(err))
				continue
			}
			total++
		}
		if len(expired) < batchSize {
			break
		}
	}
	s.logger.Info("Relocked expired stash windows", zap.Int("count", total))
	return total, nil
}
