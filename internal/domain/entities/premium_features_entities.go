package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/shopspring/decimal"
)

// ============================================================================
// Tier 1.2: Black Tax Optimizer
// ============================================================================

// FamilySupportBudget tracks how much a user wants to send to family/support monthly.
type FamilySupportBudget struct {
	UserID            uuid.UUID       `json:"user_id" db:"user_id"`
	MonthlyLimit      decimal.Decimal `json:"monthly_limit" db:"monthly_limit"`
	AlertThresholdPct int             `json:"alert_threshold_pct" db:"alert_threshold_pct"` // e.g. 80
	CreatedAt         time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at" db:"updated_at"`
}

// FamilySupportRecipient tracks a frequent family/support recipient.
type FamilySupportRecipient struct {
	ID                  uuid.UUID       `json:"id" db:"id"`
	UserID              uuid.UUID       `json:"user_id" db:"user_id"`
	RecipientName       string          `json:"recipient_name" db:"recipient_name"`
	RecipientIdentifier string          `json:"recipient_identifier" db:"recipient_identifier"` // phone/tag
	Relationship        string          `json:"relationship" db:"relationship"`                 // sibling, parent, etc.
	MonthlyAverage      decimal.Decimal `json:"monthly_average" db:"monthly_average"`
	TotalSentLifetime   decimal.Decimal `json:"total_sent_lifetime" db:"total_sent_lifetime"`
	LastSentAt          *time.Time      `json:"last_sent_at,omitempty" db:"last_sent_at"`
	SendCount           int             `json:"send_count" db:"send_count"`
	CreatedAt           time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at" db:"updated_at"`
}

// ============================================================================
// Tier 2.1: Scam Intelligence
// ============================================================================

// MerchantRiskLevel represents how risky a merchant is.
type MerchantRiskLevel string

const (
	MerchantRiskLow       MerchantRiskLevel = "low"
	MerchantRiskMedium    MerchantRiskLevel = "medium"
	MerchantRiskHigh      MerchantRiskLevel = "high"
	MerchantRiskConfirmed MerchantRiskLevel = "confirmed_scam"
)

// MerchantRiskPattern stores known scam/risky merchant patterns.
type MerchantRiskPattern struct {
	ID          uuid.UUID         `json:"id" db:"id"`
	Pattern     string            `json:"pattern" db:"pattern"`         // regex or substring
	RiskLevel   MerchantRiskLevel `json:"risk_level" db:"risk_level"`   // low, medium, high, confirmed_scam
	Category    string            `json:"category" db:"category"`       // phishing, pyramid, counterfeit, etc.
	Description string            `json:"description" db:"description"` // human-readable explanation
	ReportCount int               `json:"report_count" db:"report_count"`
	CreatedAt   time.Time         `json:"created_at" db:"created_at"`
}

// UserScamAlert stores alerts shown to users about suspicious merchants.
type UserScamAlert struct {
	ID            uuid.UUID  `json:"id" db:"id"`
	UserID        uuid.UUID  `json:"user_id" db:"user_id"`
	MerchantName  string     `json:"merchant_name" db:"merchant_name"`
	TransactionID *uuid.UUID `json:"transaction_id,omitempty" db:"transaction_id"`
	AlertType     string     `json:"alert_type" db:"alert_type"` // pattern_match, user_reported, anomaly
	RiskLevel     string     `json:"risk_level" db:"risk_level"`
	Reason        string     `json:"reason" db:"reason"`
	Dismissed     bool       `json:"dismissed" db:"dismissed"`
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
}

// ============================================================================
// Tier 2.2: Diaspora Tax Residency Tracker
// ============================================================================

// UserLocationLog tracks when a user enters/exits a country.
type UserLocationLog struct {
	ID        uuid.UUID  `json:"id" db:"id"`
	UserID    uuid.UUID  `json:"user_id" db:"user_id"`
	Country   string     `json:"country" db:"country"` // ISO 3166-1 alpha-2
	EnteredAt time.Time  `json:"entered_at" db:"entered_at"`
	ExitedAt  *time.Time `json:"exited_at,omitempty" db:"exited_at"`
	Source    string     `json:"source" db:"source"` // gps, manual, ip_address
	CreatedAt time.Time  `json:"created_at" db:"created_at"`
}

// UserTaxProfile stores a user's tax residency configuration.
type UserTaxProfile struct {
	UserID              uuid.UUID `json:"user_id" db:"user_id"`
	PrimaryTaxCountry   string    `json:"primary_tax_country" db:"primary_tax_country"`               // e.g. "NG"
	SecondaryTaxCountry *string   `json:"secondary_tax_country,omitempty" db:"secondary_tax_country"` // e.g. "GB"
	DaysInPrimary       int       `json:"days_in_primary" db:"days_in_primary"`
	DaysInSecondary     int       `json:"days_in_secondary" db:"days_in_secondary"`
	AlertThreshold      int       `json:"alert_threshold" db:"alert_threshold"`           // days before warning (e.g. 150 for 183-day rule)
	TaxYearStartMonth   int       `json:"tax_year_start_month" db:"tax_year_start_month"` // e.g. 4 for UK (April)
	TaxYearStartDay     int       `json:"tax_year_start_day" db:"tax_year_start_day"`     // e.g. 6 for UK
	CreatedAt           time.Time `json:"created_at" db:"created_at"`
	UpdatedAt           time.Time `json:"updated_at" db:"updated_at"`
}

// ============================================================================
// Tier 3.1: Financial Trauma Detection
// ============================================================================

// BehavioralHealthMetricType tracks different wellness metrics.
type BehavioralHealthMetricType string

const (
	MetricBalanceCheckFreq  BehavioralHealthMetricType = "balance_check_frequency"
	MetricAppAvoidance      BehavioralHealthMetricType = "app_avoidance_score"
	MetricPanicWithdrawal   BehavioralHealthMetricType = "panic_withdrawal_count"
	MetricLateNightSpending BehavioralHealthMetricType = "late_night_spending"
	MetricDeclinedTxStreak  BehavioralHealthMetricType = "declined_tx_streak"
	MetricMicroCheckCount   BehavioralHealthMetricType = "micro_balance_check"
)

// BehavioralHealthMetric stores a single snapshot of a user's financial behavior.
type BehavioralHealthMetric struct {
	ID          uuid.UUID                  `json:"id" db:"id"`
	UserID      uuid.UUID                  `json:"user_id" db:"user_id"`
	MetricType  BehavioralHealthMetricType `json:"metric_type" db:"metric_type"`
	Value       float64                    `json:"value" db:"value"` // normalized 0-100
	PeriodStart time.Time                  `json:"period_start" db:"period_start"`
	PeriodEnd   time.Time                  `json:"period_end" db:"period_end"`
	RecordedAt  time.Time                  `json:"recorded_at" db:"recorded_at"`
}

// FinancialWellnessScore aggregates behavioral metrics into an overall score.
type FinancialWellnessScore struct {
	UserID           uuid.UUID `json:"user_id" db:"user_id"`
	OverallScore     float64   `json:"overall_score" db:"overall_score"` // 0-100
	AnxietyScore     float64   `json:"anxiety_score" db:"anxiety_score"` // higher = more anxious
	AvoidanceScore   float64   `json:"avoidance_score" db:"avoidance_score"`
	ImpulsivityScore float64   `json:"impulsivity_score" db:"impulsivity_score"`
	ResilienceScore  float64   `json:"resilience_score" db:"resilience_score"`
	CalculatedAt     time.Time `json:"calculated_at" db:"calculated_at"`
	Recommendation   string    `json:"recommendation" db:"recommendation"`
}

// ============================================================================
// Tier 3.3: Panic Button
// ============================================================================

// EmergencyLock tracks when a user triggers the panic button.
type EmergencyLock struct {
	ID              uuid.UUID      `json:"id" db:"id"`
	UserID          uuid.UUID      `json:"user_id" db:"user_id"`
	LockedAt        time.Time      `json:"locked_at" db:"locked_at"`
	UnlockedAt      *time.Time     `json:"unlocked_at,omitempty" db:"unlocked_at"`
	Reason          string         `json:"reason" db:"reason"`             // stolen_phone, suspicious_activity, user_triggered
	TriggeredBy     string         `json:"triggered_by" db:"triggered_by"` // user, system, contact
	CardFrozen      bool           `json:"card_frozen" db:"card_frozen"`
	StashMoved      bool           `json:"stash_moved" db:"stash_moved"`
	ContactsAlerted bool           `json:"contacts_alerted" db:"contacts_alerted"`
	AlertedContacts pq.StringArray `json:"alerted_contacts,omitempty" db:"alerted_contacts"`
	Resolved        bool           `json:"resolved" db:"resolved"`
	CreatedAt       time.Time      `json:"created_at" db:"created_at"`
}

// EmergencyContact stores trusted contacts for the panic button.
type EmergencyContact struct {
	ID        uuid.UUID `json:"id" db:"id"`
	UserID    uuid.UUID `json:"user_id" db:"user_id"`
	Name      string    `json:"name" db:"name"`
	Phone     string    `json:"phone" db:"phone"`
	Email     *string   `json:"email,omitempty" db:"email"`
	Relation  string    `json:"relation" db:"relation"`
	Priority  int       `json:"priority" db:"priority"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// ============================================================================
// Shared: Receipt Splitting (Tier 1.4)
// ============================================================================

// ReceiptSplit tracks a split receipt request.
type ReceiptSplit struct {
	ID          uuid.UUID          `json:"id" db:"id"`
	ReceiptID   uuid.UUID          `json:"receipt_id" db:"receipt_id"`
	UserID      uuid.UUID          `json:"user_id" db:"user_id"`
	SplitType   string             `json:"split_type" db:"split_type"`
	TotalAmount decimal.Decimal    `json:"total_amount" db:"total_amount"`
	YourShare   decimal.Decimal    `json:"your_share" db:"your_share"`
	Currency    string             `json:"currency" db:"currency"`
	Status      string             `json:"status" db:"status"`
	Message     *string            `json:"message,omitempty" db:"message"`
	ExpiresAt   *time.Time         `json:"expires_at,omitempty" db:"expires_at"`
	Items       []ReceiptSplitItem `json:"items" db:"items"`
	CreatedAt   time.Time          `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at" db:"updated_at"`

	// Joined
	Participants []ReceiptSplitParticipant `json:"participants,omitempty" db:"-"`
}

// ReceiptSplitItem tracks who pays for what.
type ReceiptSplitItem struct {
	ID            uuid.UUID       `json:"id" db:"id"`
	SplitID       uuid.UUID       `json:"split_id" db:"split_id"`
	ItemName      string          `json:"item_name" db:"item_name"`
	Amount        decimal.Decimal `json:"amount" db:"amount"`
	AssignedTo    string          `json:"assigned_to" db:"assigned_to"` // rail_tag, phone, or name
	Paid          bool            `json:"paid" db:"paid"`
	P2PTransferID *uuid.UUID      `json:"p2p_transfer_id,omitempty" db:"p2p_transfer_id"`
	CreatedAt     time.Time       `json:"created_at" db:"created_at"`
}

// Participant statuses for receipt split tracking.
const (
	ParticipantPending   = "pending"
	ParticipantRequested = "requested"
	ParticipantPaid      = "paid"
	ParticipantDeclined  = "declined"
	ParticipantExpired   = "expired"
)

// ReceiptSplitParticipant tracks each person's share in a split.
type ReceiptSplitParticipant struct {
	ID              uuid.UUID       `json:"id" db:"id"`
	SplitID         uuid.UUID       `json:"split_id" db:"split_id"`
	RailTag         string          `json:"rail_tag" db:"rail_tag"`
	ParticipantUID  *uuid.UUID      `json:"participant_user_id,omitempty" db:"participant_user_id"`
	Amount          decimal.Decimal `json:"amount" db:"amount"`
	Status          string          `json:"status" db:"status"`
	P2PTransferID   *uuid.UUID      `json:"p2p_transfer_id,omitempty" db:"p2p_transfer_id"`
	ReminderCount   int             `json:"reminder_count" db:"reminder_count"`
	LastRemindedAt  *time.Time      `json:"last_reminded_at,omitempty" db:"last_reminded_at"`
	PaidAt          *time.Time      `json:"paid_at,omitempty" db:"paid_at"`
	CreatedAt       time.Time       `json:"created_at" db:"created_at"`
}

// ============================================================================
// Shared: Visa Proof Generator (Tier 3.2)
// ============================================================================

// VisaProofRequest tracks a generated proof-of-funds document.
type VisaProofRequest struct {
	ID               uuid.UUID       `json:"id" db:"id"`
	UserID           uuid.UUID       `json:"user_id" db:"user_id"`
	VisaCountry      string          `json:"visa_country" db:"visa_country"` // e.g. "GB", "US"
	VisaType         string          `json:"visa_type" db:"visa_type"`       // student, work, tourist
	BankBalance      decimal.Decimal `json:"bank_balance" db:"bank_balance"`
	StashBalance     decimal.Decimal `json:"stash_balance" db:"stash_balance"`
	TotalHoldings    decimal.Decimal `json:"total_holdings" db:"total_holdings"`
	AvgMonthlyInflow decimal.Decimal `json:"avg_monthly_inflow" db:"avg_monthly_inflow"`
	DocumentURL      *string         `json:"document_url,omitempty" db:"document_url"`
	Status           string          `json:"status" db:"status"` // generating, ready, expired
	ExpiresAt        time.Time       `json:"expires_at" db:"expires_at"`
	CreatedAt        time.Time       `json:"created_at" db:"created_at"`
}

// ============================================================================
// Shared: Currency Shield / Exchange Rate
// ============================================================================

// CurrencyExchangeRate stores daily rates for shield calculations.
type CurrencyExchangeRate struct {
	ID        uuid.UUID       `json:"id" db:"id"`
	FromCode  string          `json:"from_code" db:"from_code"`
	ToCode    string          `json:"to_code" db:"to_code"`
	Rate      decimal.Decimal `json:"rate" db:"rate"`
	Date      time.Time       `json:"date" db:"date"`
	CreatedAt time.Time       `json:"created_at" db:"created_at"`
}
