package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type YieldBalanceSnapshot struct {
	ID         uuid.UUID       `db:"id"`
	UserID     uuid.UUID       `db:"user_id"`
	Balance    decimal.Decimal `db:"balance"`
	RecordedAt time.Time       `db:"recorded_at"`
}

type YieldDistribution struct {
	ID               uuid.UUID       `db:"id"`
	PeriodStart      time.Time       `db:"period_start"`
	PeriodEnd        time.Time       `db:"period_end"`
	TotalReward      decimal.Decimal `db:"total_reward"`
	TotalTWB         decimal.Decimal `db:"total_twb"`
	TotalDistributed decimal.Decimal `db:"total_distributed"`
	Remainder        decimal.Decimal `db:"remainder"` // dust rolled to next period
	Status           string          `db:"status"`    // pending | completed | failed
	CreatedAt        time.Time       `db:"created_at"`
}

type YieldDistributionUser struct {
	ID             uuid.UUID       `db:"id"`
	DistributionID uuid.UUID       `db:"distribution_id"`
	UserID         uuid.UUID       `db:"user_id"`
	TWB            decimal.Decimal `db:"twb"`
	SharePct       decimal.Decimal `db:"share_pct"`
	RewardAmount   decimal.Decimal `db:"reward_amount"`
	CreditedAt     *time.Time      `db:"credited_at"`
}
