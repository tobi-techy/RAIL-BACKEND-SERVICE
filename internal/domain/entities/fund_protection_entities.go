package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// === Withdrawal Protection Entities ===

// RecoveryStatus represents the status of a recovery request
type RecoveryStatus string

const (
	RecoveryStatusPending   RecoveryStatus = "pending"
	RecoveryStatusInReview  RecoveryStatus = "in_review"
	RecoveryStatusApproved  RecoveryStatus = "approved"
	RecoveryStatusCompleted RecoveryStatus = "completed"
	RecoveryStatusRejected  RecoveryStatus = "rejected"
	RecoveryStatusExpired   RecoveryStatus = "expired"
	RecoveryStatusCancelled RecoveryStatus = "cancelled"
)

// RecoveryType represents the type of recovery requested
type RecoveryType string

const (
	RecoveryTypePasswordReset RecoveryType = "password_reset"
	RecoveryTypeWalletAccess  RecoveryType = "wallet_access"
	RecoveryTypeAccountAccess RecoveryType = "account_access"
)

// RecoveryPriority represents the priority level of recovery
type RecoveryPriority string

const (
	RecoveryPriorityLow    RecoveryPriority = "low"
	RecoveryPriorityMedium RecoveryPriority = "medium"
	RecoveryPriorityHigh   RecoveryPriority = "high"
	RecoveryPriorityUrgent RecoveryPriority = "urgent"
)

// RecoveryRequest represents a fund recovery request
type RecoveryRequest struct {
	ID                     uuid.UUID              `json:"id" db:"id"`
	UserID                 uuid.UUID              `json:"user_id" db:"user_id"`
	RecoveryType           RecoveryType           `json:"recovery_type" db:"recovery_type"`
	Status                 RecoveryStatus         `json:"status" db:"status"`
	Priority               RecoveryPriority       `json:"priority" db:"priority"`
	Reason                 string                 `json:"reason" db:"reason"`
	ExpiresAt              time.Time              `json:"expires_at" db:"expires_at"`
	RequiredVerifications  []string               `json:"required_verifications" db:"required_verifications"`
	CompletedVerifications []string               `json:"completed_verifications,omitempty" db:"completed_verifications"`
	Metadata               map[string]interface{} `json:"metadata,omitempty" db:"metadata"`
	ProcessedBy            *uuid.UUID             `json:"processed_by,omitempty" db:"processed_by"`
	ProcessedAt            *time.Time             `json:"processed_at,omitempty" db:"processed_at"`
	Notes                  *string                `json:"notes,omitempty" db:"notes"`
	CreatedAt              time.Time              `json:"created_at" db:"created_at"`
	UpdatedAt              time.Time              `json:"updated_at" db:"updated_at"`

	// Related entities (not stored in DB)
	User *User `json:"user,omitempty"`
}

// IsExpired checks if the recovery request has expired
func (r *RecoveryRequest) IsExpired() bool {
	return time.Now().After(r.ExpiresAt)
}

// CanBeProcessed checks if the recovery request can be processed
func (r *RecoveryRequest) CanBeProcessed() bool {
	return r.Status == RecoveryStatusPending || r.Status == RecoveryStatusInReview
}

// RecoveryAction represents an action taken during recovery
type RecoveryAction struct {
	ID          uuid.UUID              `json:"id" db:"id"`
	RecoveryID  uuid.UUID              `json:"recovery_id" db:"recovery_id"`
	ActionType  RecoveryActionType     `json:"action_type" db:"action_type"`
	Step        string                 `json:"step" db:"step"`
	Status      RecoveryActionStatus   `json:"status" db:"status"`
	Details     map[string]interface{} `json:"details" db:"details"`
	PerformedAt time.Time              `json:"performed_at" db:"performed_at"`
	PerformedBy *uuid.UUID             `json:"performed_by,omitempty" db:"performed_by"`
	IPAddress   *string                `json:"ip_address,omitempty" db:"ip_address"`
	UserAgent   *string                `json:"user_agent,omitempty" db:"user_agent"`
}

// RecoveryActionType represents types of recovery actions
type RecoveryActionType string

const (
	RecoveryActionVerification RecoveryActionType = "verification"
	RecoveryActionReview       RecoveryActionType = "review"
	RecoveryActionRecovery     RecoveryActionType = "recovery"
	RecoveryActionRejection    RecoveryActionType = "rejection"
)

// RecoveryActionStatus represents the status of a recovery action
type RecoveryActionStatus string

const (
	RecoveryActionStatusPending   RecoveryActionStatus = "pending"
	RecoveryActionStatusCompleted RecoveryActionStatus = "completed"
	RecoveryActionStatusFailed    RecoveryActionStatus = "failed"
)

// RecoveryInitiationResponse represents the response after initiating recovery
type RecoveryInitiationResponse struct {
	RecoveryID uuid.UUID `json:"recoveryId"`
	Message    string    `json:"message"`
	ExpiresAt  time.Time `json:"expiresAt"`
	NextSteps  []string  `json:"nextSteps"`
}

// RecoveryStatusResponse represents the status of a recovery request
type RecoveryStatusResponse struct {
	RecoveryID uuid.UUID        `json:"recoveryId"`
	Status     RecoveryStatus   `json:"status"`
	Priority   RecoveryPriority `json:"priority"`
	Progress   string           `json:"progress"`
	ExpiresAt  time.Time        `json:"expiresAt"`
	CreatedAt  time.Time        `json:"createdAt"`
	NextSteps  []string         `json:"nextSteps"`
}

// === Fee Transparency Entities ===

// FeeEstimate represents a fee estimate for an operation
type FeeEstimate struct {
	OperationType   string           `json:"operationType"`
	EstimatedAmount decimal.Decimal  `json:"estimatedAmount"`
	FeeBreakdown    *FeeBreakdown    `json:"feeBreakdown"`
	ExchangeRate    *decimal.Decimal `json:"exchangeRate,omitempty"`
	FinalAmount     decimal.Decimal  `json:"finalAmount"`
	Currency        string           `json:"currency"`
	ExpiresAt       time.Time        `json:"expiresAt"`
}

// FeeBreakdown represents a detailed fee breakdown
type FeeBreakdown struct {
	OperationType   string          `json:"operationType"`
	OperationAmount decimal.Decimal `json:"operationAmount"`
	BaseFee         decimal.Decimal `json:"baseFee"`
	PercentageFee   decimal.Decimal `json:"percentageFee"`
	NetworkFee      decimal.Decimal `json:"networkFee"`
	TotalFee        decimal.Decimal `json:"totalFee"`
	Currency        string          `json:"currency"`
	FeeDescription  string          `json:"feeDescription"`
	Transparent     bool            `json:"transparent"`
}

// FeeSummary represents a summary of fees for a period
type FeeSummary struct {
	UserID         string                     `json:"userId"`
	Period         string                     `json:"period"`
	StartDate      time.Time                  `json:"startDate"`
	EndDate        time.Time                  `json:"endDate"`
	TotalFeesPaid  decimal.Decimal            `json:"totalFeesPaid"`
	FeesByType     map[string]decimal.Decimal `json:"feesByType"`
	OperationCount int                        `json:"operationCount"`
	Currency       string                     `json:"currency"`
}

// FeeConfig represents fee configuration for an operation
type FeeConfig struct {
	OperationType  string          `json:"operation_type"`
	BaseFee        decimal.Decimal `json:"base_fee"`
	PercentageFee  decimal.Decimal `json:"percentage_fee"`
	MinFee         decimal.Decimal `json:"min_fee"`
	MaxFee         decimal.Decimal `json:"max_fee"`
	Currency       string          `json:"currency"`
	EffectiveFrom  time.Time       `json:"effective_from"`
	EffectiveUntil *time.Time      `json:"effective_until,omitempty"`
	IsActive       bool            `json:"is_active"`
	Description    string          `json:"description"`
}

// FeeSchedule represents the complete fee schedule
type FeeSchedule struct {
	LastUpdated time.Time             `json:"lastUpdated"`
	Fees        map[string]*FeeConfig `json:"fees"`
	Disclaimer  string                `json:"disclaimer"`
}

// === User Education Entities ===

// RiskWarning represents a risk warning for a user action
type RiskWarning struct {
	WarningType       string `json:"warningType"`
	Severity          string `json:"severity"` // low, medium, high, critical
	Title             string `json:"title"`
	Message           string `json:"message"`
	RecommendedAction string `json:"recommendedAction"`
	LearnMoreURL      string `json:"learnMoreUrl,omitempty"`
	DisplayCondition  string `json:"displayCondition,omitempty"` // when to show this warning
}

// EducationalContent represents educational content
type EducationalContent struct {
	ID                uuid.UUID `json:"id"`
	Title             string    `json:"title"`
	Content           string    `json:"content"`
	ContentType       string    `json:"contentType"` // article, video, quiz, etc.
	Difficulty        string    `json:"difficulty"`  // beginner, intermediate, advanced
	Topic             string    `json:"topic"`
	EstimatedReadTime int       `json:"estimatedReadTime"` // in minutes
	Prerequisites     []string  `json:"prerequisites,omitempty"`
	Tags              []string  `json:"tags"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

// UserEducationProgress represents a user's education progress
type UserEducationProgress struct {
	UserID           uuid.UUID `json:"userId" db:"user_id"`
	CompletedModules int       `json:"completedModules" db:"completed_modules"`
	TotalModules     int       `json:"totalModules" db:"total_modules"`
	CurrentStreak    int       `json:"currentStreak" db:"current_streak"`
	LongestStreak    int       `json:"longestStreak" db:"longest_streak"`
	LastActivityAt   time.Time `json:"lastActivityAt" db:"last_activity_at"`
	CreatedAt        time.Time `json:"createdAt" db:"created_at"`
	UpdatedAt        time.Time `json:"updatedAt" db:"updated_at"`

	// Calculated fields
	CompletionRate float64 `json:"completionRate"`
	Level          string  `json:"level"` // beginner, intermediate, advanced
}

// CalculateCompletionRate calculates the completion rate
func (p *UserEducationProgress) CalculateCompletionRate() {
	if p.TotalModules > 0 {
		p.CompletionRate = float64(p.CompletedModules) / float64(p.TotalModules)
	}

	// Determine level
	switch {
	case p.CompletedModules < 5:
		p.Level = "beginner"
	case p.CompletedModules < 10:
		p.Level = "intermediate"
	default:
		p.Level = "advanced"
	}
}

// EducationEvent represents an education-related event
type EducationEvent struct {
	UserID    uuid.UUID              `json:"userId"`
	EventType string                 `json:"eventType"`
	Details   map[string]interface{} `json:"details"`
	CreatedAt time.Time              `json:"createdAt"`
}

// EducationStats represents education system statistics
type EducationStats struct {
	TotalUsers            int       `json:"totalUsers"`
	ActiveLearners        int       `json:"activeLearners"`
	CompletedModules      int       `json:"completedModules"`
	AverageCompletionRate float64   `json:"averageCompletionRate"`
	PopularTopics         []string  `json:"popularTopics"`
	LastUpdated           time.Time `json:"lastUpdated"`
}

// AIAdvice represents AI-generated personalized advice
type AIAdvice struct {
	UserID      uuid.UUID `json:"userId"`
	AdviceType  string    `json:"adviceType"`
	Title       string    `json:"title"`
	Content     string    `json:"content"`
	Confidence  float64   `json:"confidence"`
	Suggestions []string  `json:"suggestions"`
	GeneratedAt time.Time `json:"generatedAt"`
}

// RiskAnalysis represents a user's risk analysis
type RiskAnalysis struct {
	UserID          uuid.UUID      `json:"userId"`
	RiskLevel       string         `json:"riskLevel"`   // low, medium, high
	OverallRisk     float64        `json:"overallRisk"` // 0-10 scale
	Concerns        []*RiskConcern `json:"concerns"`
	Recommendations []string       `json:"recommendations"`
	AnalyzedAt      time.Time      `json:"analyzedAt"`
}

// RiskConcern represents a specific risk concern
type RiskConcern struct {
	Type        string  `json:"type"`
	Severity    string  `json:"severity"`
	Description string  `json:"description"`
	Impact      float64 `json:"impact"` // 0-10 scale
}

// === Transaction Limits Entities ===

// TransactionLimitType represents different types of transaction limits
type TransactionLimitType string

const (
	LimitTypeDaily   TransactionLimitType = "daily"
	LimitTypeWeekly  TransactionLimitType = "weekly"
	LimitTypeMonthly TransactionLimitType = "monthly"
)

// TransactionLimits represents user transaction limits
type TransactionLimits struct {
	UserID               uuid.UUID                  `json:"userId" db:"user_id"`
	DailyLimit           decimal.Decimal            `json:"dailyLimit" db:"daily_limit"`
	WeeklyLimit          decimal.Decimal            `json:"weeklyLimit" db:"weekly_limit"`
	MonthlyLimit         decimal.Decimal            `json:"monthlyLimit" db:"monthly_limit"`
	RequireDualAuthAbove decimal.Decimal            `json:"requireDualAuthAbove" db:"require_dual_auth_above"`
	LimitsByType         map[string]decimal.Decimal `json:"limitsByType" db:"limits_by_type"`
	IsActive             bool                       `json:"isActive" db:"is_active"`
	CreatedAt            time.Time                  `json:"createdAt" db:"created_at"`
	UpdatedAt            time.Time                  `json:"updatedAt" db:"updated_at"`
}

// TransactionTracking represents transaction usage tracking
type TransactionTracking struct {
	UserID         uuid.UUID       `json:"userId" db:"user_id"`
	Date           time.Time       `json:"date" db:"date"`
	DailyTotal     decimal.Decimal `json:"dailyTotal" db:"daily_total"`
	WeeklyTotal    decimal.Decimal `json:"weeklyTotal" db:"weekly_total"`
	MonthlyTotal   decimal.Decimal `json:"monthlyTotal" db:"monthly_total"`
	LastActivityAt *time.Time      `json:"lastActivityAt" db:"last_activity_at"`
	CreatedAt      time.Time       `json:"createdAt" db:"created_at"`
	UpdatedAt      time.Time       `json:"updatedAt" db:"updated_at"`
}

// LimitCheckResult represents the result of a limit check
type LimitCheckResult struct {
	CanTransact      bool            `json:"canTransact"`
	RequireApproval  bool            `json:"requireApproval"`
	DailyRemaining   decimal.Decimal `json:"dailyRemaining"`
	WeeklyRemaining  decimal.Decimal `json:"weeklyRemaining"`
	MonthlyRemaining decimal.Decimal `json:"monthlyRemaining"`
	Violations       []string        `json:"violations,omitempty"`
}

// === API Request/Response Models ===

// UpdateLimitsRequest represents a request to update transaction limits
type UpdateLimitsRequest struct {
	DailyLimit           *string `json:"dailyLimit,omitempty"`
	WeeklyLimit          *string `json:"weeklyLimit,omitempty"`
	MonthlyLimit         *string `json:"monthlyLimit,omitempty"`
	RequireDualAuthAbove *string `json:"requireDualAuthAbove,omitempty"`
}

// LimitsResponse represents transaction limits response
type LimitsResponse struct {
	DailyLimit           string    `json:"dailyLimit"`
	WeeklyLimit          string    `json:"weeklyLimit"`
	MonthlyLimit         string    `json:"monthlyLimit"`
	RequireDualAuthAbove string    `json:"requireDualAuthAbove"`
	DailyUsed            string    `json:"dailyUsed"`
	WeeklyUsed           string    `json:"weeklyUsed"`
	MonthlyUsed          string    `json:"monthlyUsed"`
	DailyRemaining       string    `json:"dailyRemaining"`
	WeeklyRemaining      string    `json:"weeklyRemaining"`
	MonthlyRemaining     string    `json:"monthlyRemaining"`
	LastUpdated          time.Time `json:"lastUpdated"`
}

// RecoveryRequestRequest represents a recovery request
type RecoveryRequestRequest struct {
	Email  string `json:"email" validate:"required,email"`
	Reason string `json:"reason" validate:"required,min=10,max=500"`
}

// VerifyRecoveryRequest represents a recovery verification request
type VerifyRecoveryRequest struct {
	RecoveryID uuid.UUID `json:"recoveryId" validate:"required"`
	Step       string    `json:"step" validate:"required,oneof=email phone document"`
	Code       string    `json:"code" validate:"required,len=6"`
}

// FeeDisclosure represents fee disclosure for a transaction
type FeeDisclosure struct {
	TransactionType string        `json:"transactionType"`
	Amount          string        `json:"amount"`
	FeeBreakdown    *FeeBreakdown `json:"feeBreakdown"`
	TotalAmount     string        `json:"totalAmount"`
	DisclosureText  string        `json:"disclosureText"`
}
