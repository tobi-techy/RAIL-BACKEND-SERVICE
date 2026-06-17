package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// StreakType represents the type of streak being tracked
type StreakType string

const (
	StreakTypeDeposit             StreakType = "deposit"
	StreakTypeNoSpend             StreakType = "no_spend"
	StreakTypeStashGrowth         StreakType = "stash_growth"
	StreakTypeRoundup             StreakType = "roundup"
	StreakTypeNoPanicWithdrawal   StreakType = "no_panic_withdrawal"
	StreakTypeWeeklyGoal          StreakType = "weekly_goal"
	StreakTypeEmergencyFundGrowth StreakType = "emergency_fund_growth"
)

// StreakResetDays defines how many days of inactivity before a streak resets
var StreakResetDays = map[StreakType]int{
	StreakTypeDeposit:             7,
	StreakTypeNoSpend:             1, // resets on any spend day
	StreakTypeStashGrowth:         7, // checked weekly
	StreakTypeRoundup:             7,
	StreakTypeNoPanicWithdrawal:   1, // resets on any panic withdrawal
	StreakTypeWeeklyGoal:          7,
	StreakTypeEmergencyFundGrowth: 7,
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
	{1, 0, "New Rider"},
	{2, 100, "Steady Saver"},
	{3, 300, "Budget Killer"},
	{4, 700, "Quiet Builder"},
	{5, 1500, "Wealth Pilot"},
	{6, 3000, "Rail Legend"},
	{7, 6000, "Rail Legend II"},
	{8, 12000, "Rail Legend III"},
	{9, 25000, "Rail Legend IV"},
	{10, 50000, "Rail Legend V"},
}

// LevelForXP returns the level and title for a given XP total
func LevelForXP(totalXP int64) (int, string) {
	level, title := 1, "New Rider"
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
	BridgeTransferred   bool         `json:"bridge_transferred" db:"bridge_transferred"`
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

// --- Rings (Apple Fitness style) ---

// DailyRing tracks a user's 3-ring progress for a single day
type DailyRing struct {
	ID          uuid.UUID       `json:"id" db:"id"`
	UserID      uuid.UUID       `json:"user_id" db:"user_id"`
	RingDate    time.Time       `json:"ring_date" db:"ring_date"`
	SpendTarget decimal.Decimal `json:"spend_target" db:"spend_target"`
	SpendActual decimal.Decimal `json:"spend_actual" db:"spend_actual"`
	SaveTarget  decimal.Decimal `json:"save_target" db:"save_target"`
	SaveActual  decimal.Decimal `json:"save_actual" db:"save_actual"`
	GrowTarget  decimal.Decimal `json:"grow_target" db:"grow_target"`
	GrowActual  decimal.Decimal `json:"grow_actual" db:"grow_actual"`
	SpendClosed bool            `json:"spend_closed" db:"spend_closed"`
	SaveClosed  bool            `json:"save_closed" db:"save_closed"`
	GrowClosed  bool            `json:"grow_closed" db:"grow_closed"`
	AllClosed   bool            `json:"all_closed" db:"all_closed"`
	CreatedAt   time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at" db:"updated_at"`
}

// RingProgress returns 0.0-1.0 progress for each ring
// SpendProgress is inverted: spending LESS than target = 1.0 (closed), spending MORE = proportionally less
func (r *DailyRing) SpendProgress() float64 {
	if r.SpendTarget.IsZero() {
		return 0
	}
	if r.SpendActual.LessThanOrEqual(r.SpendTarget) {
		return 1.0 // under budget = ring closed
	}
	// Over budget: show how far over (1.0 - overage ratio), clamped to 0
	ratio, _ := r.SpendActual.Div(r.SpendTarget).Float64()
	p := 2.0 - ratio // at 2x overspend, progress = 0
	if p < 0 {
		return 0
	}
	return p
}

func (r *DailyRing) SaveProgress() float64 {
	if r.SaveTarget.IsZero() {
		return 0
	}
	p, _ := r.SaveActual.Div(r.SaveTarget).Float64()
	if p > 1 {
		return 1
	}
	return p
}

func (r *DailyRing) GrowProgress() float64 {
	if r.GrowTarget.IsZero() {
		return 0
	}
	p, _ := r.GrowActual.Div(r.GrowTarget).Float64()
	if p > 1 {
		return 1
	}
	return p
}

// --- Boosts (Cash App style) ---

type BoostType string

const (
	BoostTypeCashbackStash   BoostType = "cashback_stash"
	BoostTypePointsMultiplier BoostType = "points_multiplier"
	BoostTypeSetAside        BoostType = "set_aside"
	BoostTypeNoSpendBonus    BoostType = "no_spend_bonus"
)

type BoostStatus string

const (
	BoostStatusActive    BoostStatus = "active"
	BoostStatusCompleted BoostStatus = "completed"
	BoostStatusExpired   BoostStatus = "expired"
)

// Boost defines a boost template
type Boost struct {
	ID             uuid.UUID       `json:"id" db:"id"`
	Name           string          `json:"name" db:"name"`
	Description    string          `json:"description" db:"description"`
	BoostType      BoostType       `json:"boost_type" db:"boost_type"`
	Category       *string         `json:"category,omitempty" db:"category"`
	RewardValue    decimal.Decimal `json:"reward_value" db:"reward_value"`
	RewardUnit     string          `json:"reward_unit" db:"reward_unit"`
	ConditionType  string          `json:"condition_type" db:"condition_type"`
	ConditionValue decimal.Decimal `json:"condition_value" db:"condition_value"`
	DurationDays   int             `json:"duration_days" db:"duration_days"`
	IsActive       bool            `json:"is_active" db:"is_active"`
	CreatedAt      time.Time       `json:"created_at" db:"created_at"`
}

// UserBoost tracks a user's active/completed boost
type UserBoost struct {
	ID           uuid.UUID       `json:"id" db:"id"`
	UserID       uuid.UUID       `json:"user_id" db:"user_id"`
	BoostID      uuid.UUID       `json:"boost_id" db:"boost_id"`
	Status       BoostStatus     `json:"status" db:"status"`
	ActivatedAt  time.Time       `json:"activated_at" db:"activated_at"`
	ExpiresAt    time.Time       `json:"expires_at" db:"expires_at"`
	Progress     decimal.Decimal `json:"progress" db:"progress"`
	RewardEarned decimal.Decimal `json:"reward_earned" db:"reward_earned"`
	CreatedAt    time.Time       `json:"created_at" db:"created_at"`
	Boost        *Boost          `json:"boost,omitempty" db:"-"`
}

// --- Rail Points (Starbucks style) ---

type PointEventType string

const (
	PointEventEarn   PointEventType = "earn"
	PointEventSpend  PointEventType = "spend"
	PointEventExpire PointEventType = "expire"
	PointEventBonus  PointEventType = "bonus"
)

// Rail point earn sources
const (
	PointSourceDeposit          = "deposit"
	PointSourceStreakDay         = "streak_day"
	PointSourceChallengeComplete = "challenge_complete"
	PointSourceBoostComplete    = "boost_complete"
	PointSourceRingsClosed      = "rings_closed"
	PointSourceReferral         = "referral"
	PointSourceGraceDayPurchase = "grace_day_purchase"
	PointSourceCardSkinPurchase = "card_skin_purchase"
)

// RailPoints tracks a user's point balance
type RailPoints struct {
	ID              uuid.UUID `json:"id" db:"id"`
	UserID          uuid.UUID `json:"user_id" db:"user_id"`
	Balance         int64     `json:"balance" db:"balance"`
	LifetimeEarned  int64     `json:"lifetime_earned" db:"lifetime_earned"`
	LifetimeSpent   int64     `json:"lifetime_spent" db:"lifetime_spent"`
	CreatedAt       time.Time `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time `json:"updated_at" db:"updated_at"`
}

// RailPointEvent records a single point transaction
type RailPointEvent struct {
	ID          uuid.UUID      `json:"id" db:"id"`
	UserID      uuid.UUID      `json:"user_id" db:"user_id"`
	EventType   PointEventType `json:"event_type" db:"event_type"`
	Amount      int64          `json:"amount" db:"amount"`
	Source      string         `json:"source" db:"source"`
	SourceID    *uuid.UUID     `json:"source_id,omitempty" db:"source_id"`
	Description string         `json:"description" db:"description"`
	CreatedAt   time.Time      `json:"created_at" db:"created_at"`
}

// Point costs
const (
	GraceDayPointCost = 500
)

// --- Grace Days (Duolingo streak freeze) ---

// GraceDay tracks a user's streak freeze inventory
type GraceDay struct {
	ID         uuid.UUID  `json:"id" db:"id"`
	UserID     uuid.UUID  `json:"user_id" db:"user_id"`
	Remaining  int        `json:"remaining" db:"remaining"`
	UsedTotal  int        `json:"used_total" db:"used_total"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty" db:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at" db:"updated_at"`
}

// --- Weekly Recap (Nike Run Club style) ---

// WeeklyRecap holds a user's weekly coaching summary
type WeeklyRecap struct {
	ID                  uuid.UUID       `json:"id" db:"id"`
	UserID              uuid.UUID       `json:"user_id" db:"user_id"`
	WeekStart           time.Time       `json:"week_start" db:"week_start"`
	WeekEnd             time.Time       `json:"week_end" db:"week_end"`
	TotalDeposited      decimal.Decimal `json:"total_deposited" db:"total_deposited"`
	TotalSpent          decimal.Decimal `json:"total_spent" db:"total_spent"`
	TotalSaved          decimal.Decimal `json:"total_saved" db:"total_saved"`
	TotalGrown          decimal.Decimal `json:"total_grown" db:"total_grown"`
	SpendVsLastWeekPct  decimal.Decimal `json:"spend_vs_last_week_pct" db:"spend_vs_last_week_pct"`
	RingsClosed         int             `json:"rings_closed" db:"rings_closed"`
	StreakDays          int             `json:"streak_days" db:"streak_days"`
	PointsEarned        int64           `json:"points_earned" db:"points_earned"`
	BadgesEarned        int             `json:"badges_earned" db:"badges_earned"`
	CoachingMessage     string          `json:"coaching_message" db:"coaching_message"`
	CreatedAt           time.Time       `json:"created_at" db:"created_at"`
}
