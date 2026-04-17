package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// StreakType represents the type of streak being tracked
type StreakType string

const (
	StreakTypeDeposit     StreakType = "deposit"
	StreakTypeNoSpend     StreakType = "no_spend"
	StreakTypeStashGrowth StreakType = "stash_growth"
	StreakTypeRoundup     StreakType = "roundup"
)

// StreakResetDays defines how many days of inactivity before a streak resets
var StreakResetDays = map[StreakType]int{
	StreakTypeDeposit:     7,
	StreakTypeNoSpend:     1, // resets on any spend day
	StreakTypeStashGrowth: 7, // checked weekly
	StreakTypeRoundup:     7,
}

// UserStreak tracks a user's streak for a specific behavior
type UserStreak struct {
	ID             uuid.UUID  `json:"id" db:"id"`
	UserID         uuid.UUID  `json:"user_id" db:"user_id"`
	StreakType     StreakType  `json:"streak_type" db:"streak_type"`
	CurrentCount   int        `json:"current_count" db:"current_count"`
	LongestCount   int        `json:"longest_count" db:"longest_count"`
	LastActivityAt *time.Time `json:"last_activity_at" db:"last_activity_at"`
	StartedAt      *time.Time `json:"started_at" db:"started_at"`
	BrokenAt       *time.Time `json:"broken_at" db:"broken_at"`
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at" db:"updated_at"`
}

// XP level thresholds — level N requires LevelThresholds[N-1] total XP
var LevelThresholds = []struct {
	Level int
	XP    int64
	Title string
}{
	{1, 0, "Newcomer"},
	{2, 100, "Starter"},
	{3, 300, "Builder"},
	{4, 700, "Consistent"},
	{5, 1500, "Disciplined"},
	{6, 3000, "Wealth Builder"},
	{7, 6000, "Money Master"},
	{8, 12000, "Rail OG"},
	{9, 25000, "Top 1%"},
	{10, 50000, "Legend"},
}

// LevelForXP returns the level and title for a given XP total
func LevelForXP(totalXP int64) (int, string) {
	level, title := 1, "Newcomer"
	for _, t := range LevelThresholds {
		if totalXP >= t.XP {
			level, title = t.Level, t.Title
		}
	}
	return level, title
}

// UserXP tracks a user's total XP and current level
type UserXP struct {
	ID           uuid.UUID `json:"id" db:"id"`
	UserID       uuid.UUID `json:"user_id" db:"user_id"`
	TotalXP      int64     `json:"total_xp" db:"total_xp"`
	CurrentLevel int       `json:"current_level" db:"current_level"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time `json:"updated_at" db:"updated_at"`
}

// XPEvent records a single XP award
type XPEvent struct {
	ID          uuid.UUID  `json:"id" db:"id"`
	UserID      uuid.UUID  `json:"user_id" db:"user_id"`
	EventType   string     `json:"event_type" db:"event_type"`
	XPAmount    int        `json:"xp_amount" db:"xp_amount"`
	SourceID    *uuid.UUID `json:"source_id,omitempty" db:"source_id"`
	Description string     `json:"description" db:"description"`
	CreatedAt   time.Time  `json:"created_at" db:"created_at"`
}

// XP event type constants
const (
	XPEventFirstDeposit  = "first_deposit"
	XPEventDeposit       = "deposit"
	XPEventStreakDay     = "streak_day"
	XPEventChallenge     = "challenge_complete"
	XPEventRoundup       = "roundup"
	XPEventMilestone     = "milestone"
	XPEventReferral      = "referral"
	XPEventFirstCardTx   = "first_card_tx"
)

// ChallengeType represents the type of challenge
type ChallengeType string

const (
	ChallengeTypeWeekly  ChallengeType = "weekly"
	ChallengeTypeMonthly ChallengeType = "monthly"
	ChallengeTypeOnetime ChallengeType = "onetime"
)

// ChallengeStatus represents the status of a user's challenge
type ChallengeStatus string

const (
	ChallengeStatusActive    ChallengeStatus = "active"
	ChallengeStatusCompleted ChallengeStatus = "completed"
	ChallengeStatusExpired   ChallengeStatus = "expired"
)

// Challenge defines a challenge template
type Challenge struct {
	ID           uuid.UUID       `json:"id" db:"id"`
	ChallengeType ChallengeType  `json:"challenge_type" db:"challenge_type"`
	Title        string          `json:"title" db:"title"`
	Description  string          `json:"description" db:"description"`
	TargetMetric string          `json:"target_metric" db:"target_metric"`
	TargetValue  decimal.Decimal `json:"target_value" db:"target_value"`
	XPReward     int             `json:"xp_reward" db:"xp_reward"`
	ActiveFrom   *time.Time      `json:"active_from" db:"active_from"`
	ActiveUntil  *time.Time      `json:"active_until" db:"active_until"`
	IsActive     bool            `json:"is_active" db:"is_active"`
	ProOnly      bool            `json:"pro_only" db:"pro_only"`
	CreatedAt    time.Time       `json:"created_at" db:"created_at"`
}

// UserChallenge tracks a user's progress on a challenge
type UserChallenge struct {
	ID          uuid.UUID       `json:"id" db:"id"`
	UserID      uuid.UUID       `json:"user_id" db:"user_id"`
	ChallengeID uuid.UUID       `json:"challenge_id" db:"challenge_id"`
	Progress    decimal.Decimal `json:"progress" db:"progress"`
	Status      ChallengeStatus `json:"status" db:"status"`
	StartedAt   time.Time       `json:"started_at" db:"started_at"`
	CompletedAt *time.Time      `json:"completed_at,omitempty" db:"completed_at"`
	CreatedAt   time.Time       `json:"created_at" db:"created_at"`
	// Joined fields
	Challenge *Challenge `json:"challenge,omitempty" db:"-"`
}

// AchievementRarity represents the rarity of an achievement
type AchievementRarity string

const (
	RarityCommon    AchievementRarity = "common"
	RarityUncommon  AchievementRarity = "uncommon"
	RarityRare      AchievementRarity = "rare"
	RarityEpic      AchievementRarity = "epic"
	RarityLegendary AchievementRarity = "legendary"
)

// Achievement defines a badge/achievement template
type Achievement struct {
	ID             uuid.UUID         `json:"id" db:"id"`
	Name           string            `json:"name" db:"name"`
	Description    string            `json:"description" db:"description"`
	ConditionType  string            `json:"condition_type" db:"condition_type"`
	ConditionValue decimal.Decimal   `json:"condition_value" db:"condition_value"`
	Rarity         AchievementRarity `json:"rarity" db:"rarity"`
	Icon           string            `json:"icon" db:"icon"`
	CreatedAt      time.Time         `json:"created_at" db:"created_at"`
}

// UserAchievement records a user unlocking an achievement
type UserAchievement struct {
	ID            uuid.UUID    `json:"id" db:"id"`
	UserID        uuid.UUID    `json:"user_id" db:"user_id"`
	AchievementID uuid.UUID    `json:"achievement_id" db:"achievement_id"`
	UnlockedAt    time.Time    `json:"unlocked_at" db:"unlocked_at"`
	Achievement   *Achievement `json:"achievement,omitempty" db:"-"`
}

// SubscriptionStatus represents the status of a subscription
type SubscriptionStatus string

const (
	SubscriptionStatusActive    SubscriptionStatus = "active"
	SubscriptionStatusCancelled SubscriptionStatus = "cancelled"
	SubscriptionStatusExpired   SubscriptionStatus = "expired"
	SubscriptionStatusPastDue   SubscriptionStatus = "past_due"
)

// Subscription tracks a user's Pro subscription
type Subscription struct {
	ID                 uuid.UUID          `json:"id" db:"id"`
	UserID             uuid.UUID          `json:"user_id" db:"user_id"`
	Plan               string             `json:"plan" db:"plan"`
	Status             SubscriptionStatus `json:"status" db:"status"`
	StartedAt          time.Time          `json:"started_at" db:"started_at"`
	CurrentPeriodStart time.Time          `json:"current_period_start" db:"current_period_start"`
	CurrentPeriodEnd   time.Time          `json:"current_period_end" db:"current_period_end"`
	CancelledAt        *time.Time         `json:"cancelled_at,omitempty" db:"cancelled_at"`
	CreatedAt          time.Time          `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time          `json:"updated_at" db:"updated_at"`
}

// IsActive returns true if the subscription is currently active (including cancelled but not yet expired)
func (s *Subscription) IsActive() bool {
	if s == nil {
		return false
	}
	now := time.Now()
	return (s.Status == SubscriptionStatusActive || s.Status == SubscriptionStatusCancelled) &&
		now.Before(s.CurrentPeriodEnd)
}

// ChargeStatus represents the status of a subscription charge
type ChargeStatus string

const (
	ChargeStatusPending           ChargeStatus = "pending"
	ChargeStatusCompleted         ChargeStatus = "completed"
	ChargeStatusFailed            ChargeStatus = "failed"
	ChargeStatusInsufficientFunds ChargeStatus = "insufficient_funds"
)

// SubscriptionCharge records a billing attempt
type SubscriptionCharge struct {
	ID                  uuid.UUID    `json:"id" db:"id"`
	SubscriptionID      uuid.UUID    `json:"subscription_id" db:"subscription_id"`
	UserID              uuid.UUID    `json:"user_id" db:"user_id"`
	Amount              decimal.Decimal `json:"amount" db:"amount"`
	LedgerTransactionID *uuid.UUID   `json:"ledger_transaction_id,omitempty" db:"ledger_transaction_id"`
	Status              ChargeStatus `json:"status" db:"status"`
	PeriodStart         time.Time    `json:"period_start" db:"period_start"`
	PeriodEnd           time.Time    `json:"period_end" db:"period_end"`
	ChargedAt           *time.Time   `json:"charged_at,omitempty" db:"charged_at"`
	CreatedAt           time.Time    `json:"created_at" db:"created_at"`
}

// ProSubscriptionPrice is the monthly price in USD
const ProSubscriptionPrice = "4.99"

// ProYearlyPrice is the yearly price in USD (save ~17%)
const ProYearlyPrice = "49.99"

// PlanDuration maps plan type to billing period
var PlanDuration = map[string]int{
	"pro_monthly": 30,
	"pro_yearly":  365,
}

// PlanPrice maps plan type to price
var PlanPrice = map[string]string{
	"pro_monthly": "4.99",
	"pro_yearly":  "49.99",
}
