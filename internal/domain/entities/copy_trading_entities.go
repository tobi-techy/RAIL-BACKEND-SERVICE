package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ConductorStatus represents the status of a conductor
type ConductorStatus string

const (
	ConductorStatusPending   ConductorStatus = "pending"
	ConductorStatusActive    ConductorStatus = "active"
	ConductorStatusSuspended ConductorStatus = "suspended"
)

// DraftStatus represents the status of a draft (copy relationship)
type DraftStatus string

const (
	DraftStatusActive    DraftStatus = "active"
	DraftStatusPaused    DraftStatus = "paused"
	DraftStatusUnlinking DraftStatus = "unlinking"
	DraftStatusUnlinked  DraftStatus = "unlinked"
)

// SignalType represents the type of trading signal
type SignalType string

const (
	SignalTypeBuy       SignalType = "BUY"
	SignalTypeSell      SignalType = "SELL"
	SignalTypeRebalance SignalType = "REBALANCE"
)

// SignalStatus represents the processing status of a signal
type SignalStatus string

const (
	SignalStatusPending    SignalStatus = "pending"
	SignalStatusProcessing SignalStatus = "processing"
	SignalStatusCompleted  SignalStatus = "completed"
	SignalStatusFailed     SignalStatus = "failed"
)

// ExecutionStatus represents the status of a signal execution for a drafter
type ExecutionStatus string

const (
	ExecutionStatusSuccess           ExecutionStatus = "success"
	ExecutionStatusPartial           ExecutionStatus = "partial"
	ExecutionStatusSkippedTooSmall   ExecutionStatus = "skipped_too_small"
	ExecutionStatusInsufficientFunds ExecutionStatus = "insufficient_funds"
	ExecutionStatusFailed            ExecutionStatus = "failed"
)

// Conductor represents a professional investor whose trades can be copied
type Conductor struct {
	ID             uuid.UUID       `json:"id" db:"id"`
	UserID         uuid.UUID       `json:"user_id" db:"user_id"`
	DisplayName    string          `json:"display_name" db:"display_name"`
	Bio            string          `json:"bio,omitempty" db:"bio"`
	AvatarURL      string          `json:"avatar_url,omitempty" db:"avatar_url"`
	Status         ConductorStatus `json:"status" db:"status"`
	FeeRate        decimal.Decimal `json:"fee_rate" db:"fee_rate"`
	SourceAUM      decimal.Decimal `json:"source_aum" db:"source_aum"`
	TotalReturn    decimal.Decimal `json:"total_return" db:"total_return"`
	WinRate        decimal.Decimal `json:"win_rate" db:"win_rate"`
	MaxDrawdown    decimal.Decimal `json:"max_drawdown" db:"max_drawdown"`
	SharpeRatio    decimal.Decimal `json:"sharpe_ratio" db:"sharpe_ratio"`
	TotalTrades    int             `json:"total_trades" db:"total_trades"`
	FollowersCount int             `json:"followers_count" db:"followers_count"`
	MinDraftAmount decimal.Decimal `json:"min_draft_amount" db:"min_draft_amount"`
	IsVerified     bool            `json:"is_verified" db:"is_verified"`
	VerifiedAt     *time.Time      `json:"verified_at,omitempty" db:"verified_at"`
	LastTradeAt    *time.Time      `json:"last_trade_at,omitempty" db:"last_trade_at"`
	CreatedAt      time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at" db:"updated_at"`
}

// Draft represents an active copy relationship between a drafter and conductor
type Draft struct {
	ID               uuid.UUID       `json:"id" db:"id"`
	DrafterID        uuid.UUID       `json:"drafter_id" db:"drafter_id"`
	ConductorID      uuid.UUID       `json:"conductor_id" db:"conductor_id"`
	Status           DraftStatus     `json:"status" db:"status"`
	AllocatedCapital decimal.Decimal `json:"allocated_capital" db:"allocated_capital"`
	CurrentAUM       decimal.Decimal `json:"current_aum" db:"current_aum"`
	StartValue       decimal.Decimal `json:"start_value" db:"start_value"`
	TotalProfitLoss  decimal.Decimal `json:"total_profit_loss" db:"total_profit_loss"`
	TotalFeesPaid    decimal.Decimal `json:"total_fees_paid" db:"total_fees_paid"`
	CopyRatio        decimal.Decimal `json:"copy_ratio" db:"copy_ratio"`
	AutoAdjust       bool            `json:"auto_adjust" db:"auto_adjust"`
	CreatedAt        time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at" db:"updated_at"`
	PausedAt         *time.Time      `json:"paused_at,omitempty" db:"paused_at"`
	UnlinkedAt       *time.Time      `json:"unlinked_at,omitempty" db:"unlinked_at"`

	// Joined fields for API responses
	Conductor *Conductor `json:"conductor,omitempty" db:"-"`
}

// Signal represents a trade executed by a conductor
type Signal struct {
	ID                  uuid.UUID       `json:"id" db:"id"`
	ConductorID         uuid.UUID       `json:"conductor_id" db:"conductor_id"`
	AssetTicker         string          `json:"asset_ticker" db:"asset_ticker"`
	AssetName           string          `json:"asset_name,omitempty" db:"asset_name"`
	SignalType          SignalType      `json:"signal_type" db:"signal_type"`
	Side                string          `json:"side" db:"side"`
	BaseQuantity        decimal.Decimal `json:"base_quantity" db:"base_quantity"`
	BasePrice           decimal.Decimal `json:"base_price" db:"base_price"`
	BaseValue           decimal.Decimal `json:"base_value" db:"base_value"`
	ConductorAUMAtSignal decimal.Decimal `json:"conductor_aum_at_signal" db:"conductor_aum_at_signal"`
	OrderID             string          `json:"order_id,omitempty" db:"order_id"`
	Status              SignalStatus    `json:"status" db:"status"`
	ProcessedCount      int             `json:"processed_count" db:"processed_count"`
	FailedCount         int             `json:"failed_count" db:"failed_count"`
	CreatedAt           time.Time       `json:"created_at" db:"created_at"`
	CompletedAt         *time.Time      `json:"completed_at,omitempty" db:"completed_at"`
}

// SignalExecutionLog tracks the execution of a copied trade for each drafter
type SignalExecutionLog struct {
	ID               uuid.UUID       `json:"id" db:"id"`
	DraftID          uuid.UUID       `json:"draft_id" db:"draft_id"`
	SignalID         uuid.UUID       `json:"signal_id" db:"signal_id"`
	ExecutedQuantity decimal.Decimal `json:"executed_quantity" db:"executed_quantity"`
	ExecutedPrice    decimal.Decimal `json:"executed_price" db:"executed_price"`
	ExecutedValue    decimal.Decimal `json:"executed_value" db:"executed_value"`
	Status           ExecutionStatus `json:"status" db:"status"`
	FeeApplied       decimal.Decimal `json:"fee_applied" db:"fee_applied"`
	ErrorMessage     string          `json:"error_message,omitempty" db:"error_message"`
	OrderID          string          `json:"order_id,omitempty" db:"order_id"`
	IdempotencyKey   string          `json:"idempotency_key" db:"idempotency_key"`
	CreatedAt        time.Time       `json:"created_at" db:"created_at"`
	ExecutedAt       *time.Time      `json:"executed_at,omitempty" db:"executed_at"`
}

// ConductorPerformanceHistory stores daily performance snapshots
type ConductorPerformanceHistory struct {
	ID               uuid.UUID       `json:"id" db:"id"`
	ConductorID      uuid.UUID       `json:"conductor_id" db:"conductor_id"`
	SnapshotDate     time.Time       `json:"snapshot_date" db:"snapshot_date"`
	AUM              decimal.Decimal `json:"aum" db:"aum"`
	DailyReturn      decimal.Decimal `json:"daily_return" db:"daily_return"`
	CumulativeReturn decimal.Decimal `json:"cumulative_return" db:"cumulative_return"`
	FollowersCount   int             `json:"followers_count" db:"followers_count"`
	TradesCount      int             `json:"trades_count" db:"trades_count"`
	CreatedAt        time.Time       `json:"created_at" db:"created_at"`
}

// API Request/Response types

// CreateDraftRequest represents a request to start copying a conductor
type CreateDraftRequest struct {
	ConductorID      uuid.UUID       `json:"conductor_id" binding:"required"`
	AllocatedCapital decimal.Decimal `json:"allocated_capital" binding:"required"`
	CopyRatio        decimal.Decimal `json:"copy_ratio"` // Optional, defaults to 1.0
	AutoAdjust       bool            `json:"auto_adjust"`
}

// ResizeDraftRequest represents a request to adjust allocated capital
type ResizeDraftRequest struct {
	NewAllocatedCapital decimal.Decimal `json:"new_allocated_capital" binding:"required"`
}

// ConductorListResponse represents the response for listing conductors
type ConductorListResponse struct {
	Conductors []ConductorSummary `json:"conductors"`
	Total      int                `json:"total"`
	Page       int                `json:"page"`
	PageSize   int                `json:"page_size"`
}

// ConductorSummary is a condensed view of a conductor for listing
type ConductorSummary struct {
	ID             uuid.UUID       `json:"id"`
	DisplayName    string          `json:"display_name"`
	AvatarURL      string          `json:"avatar_url,omitempty"`
	TotalReturn    decimal.Decimal `json:"total_return"`
	WinRate        decimal.Decimal `json:"win_rate"`
	FollowersCount int             `json:"followers_count"`
	FeeRate        decimal.Decimal `json:"fee_rate"`
	MinDraftAmount decimal.Decimal `json:"min_draft_amount"`
	IsVerified     bool            `json:"is_verified"`
	SourceAUM      decimal.Decimal `json:"source_aum"`
}

// DraftSummary is a condensed view of a draft for listing
type DraftSummary struct {
	ID               uuid.UUID       `json:"id"`
	ConductorID      uuid.UUID       `json:"conductor_id"`
	ConductorName    string          `json:"conductor_name"`
	Status           DraftStatus     `json:"status"`
	AllocatedCapital decimal.Decimal `json:"allocated_capital"`
	CurrentAUM       decimal.Decimal `json:"current_aum"`
	TotalProfitLoss  decimal.Decimal `json:"total_profit_loss"`
	ReturnPct        decimal.Decimal `json:"return_pct"`
	CreatedAt        time.Time       `json:"created_at"`
}

// MinimumTradeValue is the minimum trade value in USD
var MinimumTradeValue = decimal.NewFromFloat(1.00)

// ConductorApplicationStatus represents the status of a conductor application
type ConductorApplicationStatus string

const (
	ConductorApplicationStatusPending  ConductorApplicationStatus = "pending"
	ConductorApplicationStatusApproved ConductorApplicationStatus = "approved"
	ConductorApplicationStatusRejected ConductorApplicationStatus = "rejected"
)

// ConductorApplication represents a user's application to become a conductor
type ConductorApplication struct {
	ID                  uuid.UUID                  `json:"id" db:"id"`
	UserID              uuid.UUID                  `json:"user_id" db:"user_id"`
	DisplayName         string                     `json:"display_name" db:"display_name"`
	Bio                 string                     `json:"bio" db:"bio"`
	InvestmentStrategy  string                     `json:"investment_strategy" db:"investment_strategy"`
	Experience          string                     `json:"experience" db:"experience"`
	SocialLinks         map[string]string          `json:"social_links,omitempty" db:"social_links"`
	Status              ConductorApplicationStatus `json:"status" db:"status"`
	ReviewedBy          *uuid.UUID                 `json:"reviewed_by,omitempty" db:"reviewed_by"`
	ReviewedAt          *time.Time                 `json:"reviewed_at,omitempty" db:"reviewed_at"`
	RejectionReason     string                     `json:"rejection_reason,omitempty" db:"rejection_reason"`
	CreatedAt           time.Time                  `json:"created_at" db:"created_at"`
	UpdatedAt           time.Time                  `json:"updated_at" db:"updated_at"`
}

// CreateConductorApplicationRequest represents a request to apply as a conductor
type CreateConductorApplicationRequest struct {
	DisplayName        string            `json:"display_name" binding:"required,min=2,max=50"`
	Bio                string            `json:"bio" binding:"required,min=10,max=500"`
	InvestmentStrategy string            `json:"investment_strategy" binding:"required,min=20,max=1000"`
	Experience         string            `json:"experience" binding:"required,min=10,max=500"`
	SocialLinks        map[string]string `json:"social_links,omitempty"`
}

// ReviewConductorApplicationRequest represents an admin review of an application
type ReviewConductorApplicationRequest struct {
	Approved        bool   `json:"approved"`
	RejectionReason string `json:"rejection_reason,omitempty"`
}

// Track represents a curated portfolio strategy created by a conductor
type Track struct {
	ID             uuid.UUID       `json:"id" db:"id"`
	ConductorID    uuid.UUID       `json:"conductor_id" db:"conductor_id"`
	Name           string          `json:"name" db:"name"`
	Description    string          `json:"description" db:"description"`
	RiskLevel      string          `json:"risk_level" db:"risk_level"` // low, medium, high
	IsActive       bool            `json:"is_active" db:"is_active"`
	FollowersCount int             `json:"followers_count" db:"followers_count"`
	TotalReturn    decimal.Decimal `json:"total_return" db:"total_return"`
	CreatedAt      time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at" db:"updated_at"`

	// Joined fields
	Allocations []TrackAllocation `json:"allocations,omitempty" db:"-"`
	Conductor   *Conductor        `json:"conductor,omitempty" db:"-"`
}

// TrackAllocation represents an asset allocation within a track
type TrackAllocation struct {
	ID           uuid.UUID       `json:"id" db:"id"`
	TrackID      uuid.UUID       `json:"track_id" db:"track_id"`
	AssetTicker  string          `json:"asset_ticker" db:"asset_ticker"`
	AssetName    string          `json:"asset_name" db:"asset_name"`
	TargetWeight decimal.Decimal `json:"target_weight" db:"target_weight"` // Percentage (0-100)
	CreatedAt    time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at" db:"updated_at"`
}

// CreateTrackRequest represents a request to create a new track
type CreateTrackRequest struct {
	Name        string                        `json:"name" binding:"required,min=2,max=100"`
	Description string                        `json:"description" binding:"required,min=10,max=500"`
	RiskLevel   string                        `json:"risk_level" binding:"required,oneof=low medium high"`
	Allocations []CreateTrackAllocationRequest `json:"allocations" binding:"required,min=1,max=20"`
}

// CreateTrackAllocationRequest represents an allocation in a track creation request
type CreateTrackAllocationRequest struct {
	AssetTicker  string          `json:"asset_ticker" binding:"required"`
	AssetName    string          `json:"asset_name" binding:"required"`
	TargetWeight decimal.Decimal `json:"target_weight" binding:"required"`
}

// UpdateTrackRequest represents a request to update track allocations
type UpdateTrackRequest struct {
	Name        *string                       `json:"name,omitempty"`
	Description *string                       `json:"description,omitempty"`
	RiskLevel   *string                       `json:"risk_level,omitempty"`
	Allocations []CreateTrackAllocationRequest `json:"allocations,omitempty"`
}

// TrackSummary is a condensed view of a track for listing
type TrackSummary struct {
	ID             uuid.UUID       `json:"id"`
	ConductorID    uuid.UUID       `json:"conductor_id"`
	ConductorName  string          `json:"conductor_name"`
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	RiskLevel      string          `json:"risk_level"`
	FollowersCount int             `json:"followers_count"`
	TotalReturn    decimal.Decimal `json:"total_return"`
	IsActive       bool            `json:"is_active"`
}
