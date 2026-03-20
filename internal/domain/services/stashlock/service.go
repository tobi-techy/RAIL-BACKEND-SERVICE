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
	GetUnlockedPending(ctx context.Context, now time.Time, limit int) ([]*entities.StashLockCycle, error)
	// RelockCycle atomically marks oldID as relocked and inserts newCycle in one transaction.
	RelockCycle(ctx context.Context, oldID uuid.UUID, newCycle *entities.StashLockCycle) error
}

// Notifier sends push notifications for stash lock events.
type Notifier interface {
	NotifyStashWindowOpen(ctx context.Context, userID uuid.UUID, windowEnd time.Time) error
}

// Service manages the 90-day lock / 7-day window cycle for stash funds.
type Service struct {
	repo     Repository
	notifier Notifier
	logger   *zap.Logger
}

func NewService(repo Repository, logger *zap.Logger) *Service {
	return &Service{repo: repo, logger: logger}
}

// SetNotifier wires push notifications for window-open events.
func (s *Service) SetNotifier(n Notifier) {
	s.notifier = n
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
	const (
		batchSize     = 500
		maxIterations = 100 // safety cap: 50 000 cycles max per run
	)
	now := time.Now()
	total := 0
	for i := 0; i < maxIterations; i++ {
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
			if err := s.repo.RelockCycle(ctx, c.ID, newCycle); err != nil {
				s.logger.Error("Failed to relock cycle atomically",
					zap.String("cycle_id", c.ID.String()),
					zap.String("user_id", c.UserID.String()),
					zap.Error(err))
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

// NotifyWindowsOpened notifies users whose lock period just ended (status=locked, lock_end < now).
// Call this from a daily cron. Non-fatal: logs errors and continues.
func (s *Service) NotifyWindowsOpened(ctx context.Context) (int, error) {
	if s.notifier == nil {
		return 0, nil
	}
	now := time.Now()
	cycles, err := s.repo.GetUnlockedPending(ctx, now, 500)
	if err != nil {
		return 0, fmt.Errorf("GetUnlockedPending: %w", err)
	}
	count := 0
	for _, c := range cycles {
		if err := s.notifier.NotifyStashWindowOpen(ctx, c.UserID, c.WindowEnd); err != nil {
			s.logger.Warn("Failed to notify stash window open", zap.String("user_id", c.UserID.String()), zap.Error(err))
			continue
		}
		// Mark as notified so this cycle is excluded from future runs.
		if err := s.repo.UpdateStatus(ctx, c.ID, entities.StashCycleStatusWindowNotified); err != nil {
			s.logger.Warn("Failed to mark cycle window_notified", zap.String("cycle_id", c.ID.String()), zap.Error(err))
			continue
		}
		count++
	}
	s.logger.Info("Notified stash window open", zap.Int("count", count))
	return count, nil
}
