package entities

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

const (
	GuardianModeObserve = "observe"
	GuardianModeNudge   = "nudge"
	GuardianModeStrict  = "strict"

	CapScopeDaily      = "daily"
	CapScopeWeekly     = "weekly"
	CapScopeMonthly    = "monthly"
	CapScopeCategory   = "category"
	CapScopeMerchant   = "merchant"
	CapScopeWithdrawal = "withdrawal"

	CapPeriodDay   = "day"
	CapPeriodWeek  = "week"
	CapPeriodMonth = "month"

	CapActionWarn       = "warn"
	CapActionRequirePin = "require_passcode"
	CapActionPauseCard  = "pause_card"
	CapActionDecline    = "decline"
)

// MoneyGuardSettings stores user-controlled anti-overspending preferences.
type MoneyGuardSettings struct {
	UserID                  uuid.UUID       `json:"user_id" db:"user_id"`
	GuardianMode            string          `json:"guardian_mode" db:"guardian_mode"`
	DecimalSweepEnabled     bool            `json:"decimal_sweep_enabled" db:"decimal_sweep_enabled"`
	StashRaidLimitPerMonth  int             `json:"stash_raid_limit_per_month" db:"stash_raid_limit_per_month"`
	CardCooldownMinutes     int             `json:"card_cooldown_minutes" db:"card_cooldown_minutes"`
	SafeToSpendFloor        decimal.Decimal `json:"safe_to_spend_floor" db:"safe_to_spend_floor"`
	RecoveryModeUntil       *time.Time      `json:"recovery_mode_until,omitempty" db:"recovery_mode_until"`
	LastWeeklyAutopsySentAt *time.Time      `json:"last_weekly_autopsy_sent_at,omitempty" db:"last_weekly_autopsy_sent_at"`
	CreatedAt               time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt               time.Time       `json:"updated_at" db:"updated_at"`
}

// SpendingCap stores a user cap by period, category, merchant, or withdrawal.
type SpendingCap struct {
	ID                uuid.UUID       `json:"id" db:"id"`
	UserID            uuid.UUID       `json:"user_id" db:"user_id"`
	Scope             string          `json:"scope" db:"scope"`
	ScopeValue        string          `json:"scope_value" db:"scope_value"`
	Period            string          `json:"period" db:"period"`
	LimitAmount       decimal.Decimal `json:"limit_amount" db:"limit_amount"`
	Currency          string          `json:"currency" db:"currency"`
	EnforcementAction string          `json:"enforcement_action" db:"enforcement_action"`
	IsActive          bool            `json:"is_active" db:"is_active"`
	CreatedAt         time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at" db:"updated_at"`
}

// MoneyGuardEvent records a guardrail decision or intervention.
type MoneyGuardEvent struct {
	ID         uuid.UUID       `json:"id" db:"id"`
	UserID     uuid.UUID       `json:"user_id" db:"user_id"`
	EventType  string          `json:"event_type" db:"event_type"`
	Severity   string          `json:"severity" db:"severity"`
	Amount     decimal.Decimal `json:"amount" db:"amount"`
	Currency   string          `json:"currency" db:"currency"`
	Merchant   string          `json:"merchant" db:"merchant"`
	Category   string          `json:"category" db:"category"`
	Action     string          `json:"action" db:"action"`
	Message    string          `json:"message" db:"message"`
	Metadata   json.RawMessage `json:"metadata" db:"metadata"`
	OccurredAt time.Time       `json:"occurred_at" db:"occurred_at"`
}

type SafeToSpendSnapshot struct {
	SpendBalance       decimal.Decimal `json:"spend_balance"`
	ProtectedAmount    decimal.Decimal `json:"protected_amount"`
	BudgetRemaining    decimal.Decimal `json:"budget_remaining"`
	DaysToPlan         int             `json:"days_to_plan"`
	DailySafeToSpend   decimal.Decimal `json:"daily_safe_to_spend"`
	WeeklySafeToSpend  decimal.Decimal `json:"weekly_safe_to_spend"`
	Status             string          `json:"status"`
	PrimaryConstraint  string          `json:"primary_constraint"`
	NextPaydayEstimate string          `json:"next_payday_estimate,omitempty"`
}

type MoneyGuardDecision struct {
	Mode          string              `json:"mode"`
	Allowed       bool                `json:"allowed"`
	Action        string              `json:"action"`
	Severity      string              `json:"severity"`
	Message       string              `json:"message"`
	Reasons       []string            `json:"reasons"`
	TriggeredCaps []SpendingCap       `json:"triggered_caps,omitempty"`
	SafeToSpend   SafeToSpendSnapshot `json:"safe_to_spend"`
	DecimalSweep  decimal.Decimal     `json:"decimal_sweep"`
}

type WeeklyAutopsy struct {
	PeriodStart        time.Time           `json:"period_start"`
	PeriodEnd          time.Time           `json:"period_end"`
	TotalOutflow       decimal.Decimal     `json:"total_outflow"`
	TransactionCount   int                 `json:"transaction_count"`
	TopCategory        string              `json:"top_category,omitempty"`
	TopCategoryAmount  decimal.Decimal     `json:"top_category_amount"`
	TopMerchant        string              `json:"top_merchant,omitempty"`
	TopMerchantAmount  decimal.Decimal     `json:"top_merchant_amount"`
	WhatHelped         []string            `json:"what_helped"`
	WhatHurt           []string            `json:"what_hurt"`
	RecommendedActions []string            `json:"recommended_actions"`
	SafeToSpend        SafeToSpendSnapshot `json:"safe_to_spend"`
	HabitScore         int                 `json:"habit_score"`
}

type RecoveryPlan struct {
	Status      string              `json:"status"`
	Days        int                 `json:"days"`
	DailyTarget decimal.Decimal     `json:"daily_target"`
	Steps       []string            `json:"steps"`
	SafeToSpend SafeToSpendSnapshot `json:"safe_to_spend"`
	HabitScore  int                 `json:"habit_score"`
}
