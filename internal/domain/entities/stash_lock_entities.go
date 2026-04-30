package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

const (
	StashLockDuration   = 90 * 24 * time.Hour
	StashWindowDuration = 7 * 24 * time.Hour
)

const (
	StashCycleStatusLocked         = "locked"
	StashCycleStatusWindowOpen     = "window_open"
	StashCycleStatusWindowNotified = "window_notified"
	StashCycleStatusWithdrawn            = "withdrawn"
	StashCycleStatusRelocked             = "relocked"
	StashCycleStatusEmergencyWithdrawn   = "emergency_withdrawn"
)

type StashLockCycle struct {
	ID        uuid.UUID       `db:"id"`
	UserID    uuid.UUID       `db:"user_id"`
	DepositID uuid.UUID       `db:"deposit_id"`
	Amount    decimal.Decimal `db:"amount"`
	LockStart time.Time       `db:"lock_start"`
	LockEnd   time.Time       `db:"lock_end"`
	WindowEnd time.Time       `db:"window_end"`
	Status    string          `db:"status"`
	CreatedAt time.Time       `db:"created_at"`
	UpdatedAt time.Time       `db:"updated_at"`
}

// IsWithdrawable returns true if the lock period has ended and the cycle is in the open window.
func (c *StashLockCycle) IsWithdrawable(now time.Time) bool {
	open := c.Status == StashCycleStatusWindowOpen || c.Status == StashCycleStatusWindowNotified
	return open && now.After(c.LockEnd) && now.Before(c.WindowEnd)
}
