package entities

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Allocation errors
var (
	ErrSpendingLimitReached = errors.New("spending limit reached: 70% allocation depleted")
)

// Default allocation ratios (Rail MVP - non-negotiable)
// Per PRD: "70% → Spend Balance, 30% → Invest Engine"
var (
	DefaultSpendingRatio = decimal.NewFromFloat(0.70)
	DefaultStashRatio    = decimal.NewFromFloat(0.30)
)

// SmartAllocationMode represents the 70/30 allocation mode state for a user
type SmartAllocationMode struct {
	UserID         uuid.UUID       `json:"user_id" db:"user_id"`
	Active         bool            `json:"active" db:"active"`
	RatioSpending  decimal.Decimal `json:"ratio_spending" db:"ratio_spending"`
	RatioStash     decimal.Decimal `json:"ratio_stash" db:"ratio_stash"`
	PausedAt       *time.Time      `json:"paused_at,omitempty" db:"paused_at"`
	ResumedAt      *time.Time      `json:"resumed_at,omitempty" db:"resumed_at"`
	CreatedAt      time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at" db:"updated_at"`
}

// Validate validates the smart allocation mode
func (m *SmartAllocationMode) Validate() error {
	if m.UserID == uuid.Nil {
		return fmt.Errorf("user ID is required")
	}

	if m.RatioSpending.IsNegative() {
		return fmt.Errorf("spending ratio cannot be negative")
	}

	if m.RatioStash.IsNegative() {
		return fmt.Errorf("stash ratio cannot be negative")
	}

	if m.RatioSpending.GreaterThan(decimal.NewFromInt(1)) {
		return fmt.Errorf("spending ratio cannot exceed 1.0")
	}

	if m.RatioStash.GreaterThan(decimal.NewFromInt(1)) {
		return fmt.Errorf("stash ratio cannot exceed 1.0")
	}

	// Ensure ratios sum to 1.0 (with small tolerance for floating point)
	sum := m.RatioSpending.Add(m.RatioStash)
	one := decimal.NewFromInt(1)
	tolerance := decimal.NewFromFloat(0.0001)
	if sum.Sub(one).Abs().GreaterThan(tolerance) {
		return fmt.Errorf("ratios must sum to 1.0: spending=%s, stash=%s", 
			m.RatioSpending.String(), m.RatioStash.String())
	}

	return nil
}

// Pause marks the mode as paused
func (m *SmartAllocationMode) Pause() {
	now := time.Now()
	m.Active = false
	m.PausedAt = &now
}

// Resume marks the mode as active
func (m *SmartAllocationMode) Resume() {
	now := time.Now()
	m.Active = true
	m.ResumedAt = &now
	m.PausedAt = nil
}

// AllocationEventType represents the type of allocation event
type AllocationEventType string

const (
	AllocationEventTypeDeposit       AllocationEventType = "deposit"
	AllocationEventTypeFiatDeposit   AllocationEventType = "fiat_deposit"
	AllocationEventTypeCryptoDeposit AllocationEventType = "crypto_deposit"
	AllocationEventTypeCashback      AllocationEventType = "cashback"
	AllocationEventTypeRoundup       AllocationEventType = "roundup"
	AllocationEventTypeTransfer      AllocationEventType = "transfer"
)

// Validate checks if the event type is valid
func (t AllocationEventType) Validate() error {
	switch t {
	case AllocationEventTypeDeposit, AllocationEventTypeFiatDeposit, AllocationEventTypeCryptoDeposit,
		AllocationEventTypeCashback, AllocationEventTypeRoundup, AllocationEventTypeTransfer:
		return nil
	default:
		return fmt.Errorf("invalid allocation event type: %s", t)
	}
}

// AllocationEvent represents an audit record of fund allocation
type AllocationEvent struct {
	ID             uuid.UUID           `json:"id" db:"id"`
	UserID         uuid.UUID           `json:"user_id" db:"user_id"`
	TotalAmount    decimal.Decimal     `json:"total_amount" db:"total_amount"`
	StashAmount    decimal.Decimal     `json:"stash_amount" db:"stash_amount"`
	SpendingAmount decimal.Decimal     `json:"spending_amount" db:"spending_amount"`
	EventType      AllocationEventType `json:"event_type" db:"event_type"`
	SourceTxID     *string             `json:"source_tx_id,omitempty" db:"source_tx_id"`
	Metadata       map[string]any      `json:"metadata,omitempty" db:"metadata"`
	CreatedAt      time.Time           `json:"created_at" db:"created_at"`
}

// Validate validates the allocation event
func (e *AllocationEvent) Validate() error {
	if e.ID == uuid.Nil {
		return fmt.Errorf("event ID is required")
	}

	if e.UserID == uuid.Nil {
		return fmt.Errorf("user ID is required")
	}

	if e.TotalAmount.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("total amount must be positive")
	}

	if e.StashAmount.IsNegative() {
		return fmt.Errorf("stash amount cannot be negative")
	}

	if e.SpendingAmount.IsNegative() {
		return fmt.Errorf("spending amount cannot be negative")
	}

	// Verify amounts add up
	sum := e.StashAmount.Add(e.SpendingAmount)
	tolerance := decimal.NewFromFloat(0.0001)
	if sum.Sub(e.TotalAmount).Abs().GreaterThan(tolerance) {
		return fmt.Errorf("stash + spending must equal total: stash=%s, spending=%s, total=%s",
			e.StashAmount.String(), e.SpendingAmount.String(), e.TotalAmount.String())
	}

	if err := e.EventType.Validate(); err != nil {
		return err
	}

	return nil
}

// WeeklyAllocationSummary represents aggregated allocation data for a week
type WeeklyAllocationSummary struct {
	ID                uuid.UUID       `json:"id" db:"id"`
	UserID            uuid.UUID       `json:"user_id" db:"user_id"`
	WeekStart         time.Time       `json:"week_start" db:"week_start"`
	WeekEnd           time.Time       `json:"week_end" db:"week_end"`
	TotalIncome       decimal.Decimal `json:"total_income" db:"total_income"`
	StashAdded        decimal.Decimal `json:"stash_added" db:"stash_added"`
	SpendingAdded     decimal.Decimal `json:"spending_added" db:"spending_added"`
	SpendingUsed      decimal.Decimal `json:"spending_used" db:"spending_used"`
	SpendingRemaining decimal.Decimal `json:"spending_remaining" db:"spending_remaining"`
	DeclinesCount     int             `json:"declines_count" db:"declines_count"`
	ModeActiveDays    int             `json:"mode_active_days" db:"mode_active_days"`
	CreatedAt         time.Time       `json:"created_at" db:"created_at"`
}

// Validate validates the weekly allocation summary
func (s *WeeklyAllocationSummary) Validate() error {
	if s.ID == uuid.Nil {
		return fmt.Errorf("summary ID is required")
	}

	if s.UserID == uuid.Nil {
		return fmt.Errorf("user ID is required")
	}

	if s.WeekEnd.Before(s.WeekStart) {
		return fmt.Errorf("week end must be after week start")
	}

	if s.TotalIncome.IsNegative() {
		return fmt.Errorf("total income cannot be negative")
	}

	if s.StashAdded.IsNegative() {
		return fmt.Errorf("stash added cannot be negative")
	}

	if s.SpendingAdded.IsNegative() {
		return fmt.Errorf("spending added cannot be negative")
	}

	if s.SpendingUsed.IsNegative() {
		return fmt.Errorf("spending used cannot be negative")
	}

	if s.SpendingRemaining.IsNegative() {
		return fmt.Errorf("spending remaining cannot be negative")
	}

	if s.DeclinesCount < 0 {
		return fmt.Errorf("declines count cannot be negative")
	}

	if s.ModeActiveDays < 0 || s.ModeActiveDays > 7 {
		return fmt.Errorf("mode active days must be between 0 and 7")
	}

	return nil
}

// AllocationBalances represents current allocation balances for a user
type AllocationBalances struct {
	UserID            uuid.UUID       `json:"user_id"`
	SpendingBalance   decimal.Decimal `json:"spending_balance"`
	StashBalance      decimal.Decimal `json:"stash_balance"`
	SpendingUsed      decimal.Decimal `json:"spending_used"`
	SpendingRemaining decimal.Decimal `json:"spending_remaining"`
	TotalBalance      decimal.Decimal `json:"total_balance"`
	ModeActive        bool            `json:"mode_active"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

// CalculateTotals calculates derived totals
func (b *AllocationBalances) CalculateTotals() {
	b.TotalBalance = b.SpendingBalance.Add(b.StashBalance)
	b.SpendingRemaining = b.SpendingBalance.Sub(b.SpendingUsed)
	if b.SpendingRemaining.IsNegative() {
		b.SpendingRemaining = decimal.Zero
	}
}

// IncomingFundsRequest represents a request to process incoming funds
type IncomingFundsRequest struct {
	UserID       uuid.UUID
	Amount       decimal.Decimal
	EventType    AllocationEventType
	SourceTxID   *string
	Metadata     map[string]any
	DepositID    *uuid.UUID // Link to deposit record if applicable
}

// Validate validates the incoming funds request
func (r *IncomingFundsRequest) Validate() error {
	if r.UserID == uuid.Nil {
		return fmt.Errorf("user ID is required")
	}

	if r.Amount.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("amount must be positive")
	}

	if err := r.EventType.Validate(); err != nil {
		return err
	}

	return nil
}

// AllocationRatios represents the allocation ratios for spending and stash
type AllocationRatios struct {
	SpendingRatio decimal.Decimal
	StashRatio    decimal.Decimal
}

// Validate validates the allocation ratios
func (r *AllocationRatios) Validate() error {
	if r.SpendingRatio.IsNegative() || r.SpendingRatio.GreaterThan(decimal.NewFromInt(1)) {
		return fmt.Errorf("spending ratio must be between 0 and 1")
	}

	if r.StashRatio.IsNegative() || r.StashRatio.GreaterThan(decimal.NewFromInt(1)) {
		return fmt.Errorf("stash ratio must be between 0 and 1")
	}

	sum := r.SpendingRatio.Add(r.StashRatio)
	one := decimal.NewFromInt(1)
	tolerance := decimal.NewFromFloat(0.0001)
	if sum.Sub(one).Abs().GreaterThan(tolerance) {
		return fmt.Errorf("ratios must sum to 1.0")
	}

	return nil
}

// DefaultAllocationRatios returns the default 70/30 ratios
func DefaultAllocationRatios() AllocationRatios {
	return AllocationRatios{
		SpendingRatio: decimal.NewFromFloat(0.70),
		StashRatio:    decimal.NewFromFloat(0.30),
	}
}
