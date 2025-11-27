package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ReconciliationCheckType defines the type of reconciliation check
type ReconciliationCheckType string

const (
	ReconciliationCheckLedgerConsistency ReconciliationCheckType = "ledger_consistency"
	ReconciliationCheckCircleBalance     ReconciliationCheckType = "circle_balance"
	ReconciliationCheckAlpacaBalance     ReconciliationCheckType = "alpaca_balance"
	ReconciliationCheckDeposits          ReconciliationCheckType = "deposits"
	ReconciliationCheckConversionJobs    ReconciliationCheckType = "conversion_jobs"
	ReconciliationCheckWithdrawals       ReconciliationCheckType = "withdrawals"
)

// ReconciliationStatus represents the status of a reconciliation run
type ReconciliationStatus string

const (
	ReconciliationStatusPending    ReconciliationStatus = "pending"
	ReconciliationStatusInProgress ReconciliationStatus = "in_progress"
	ReconciliationStatusCompleted  ReconciliationStatus = "completed"
	ReconciliationStatusFailed     ReconciliationStatus = "failed"
)

// ExceptionSeverity represents the severity level of a reconciliation exception
type ExceptionSeverity string

const (
	ExceptionSeverityLow      ExceptionSeverity = "low"      // Auto-correctable
	ExceptionSeverityMedium   ExceptionSeverity = "medium"   // Requires monitoring
	ExceptionSeverityHigh     ExceptionSeverity = "high"     // Requires immediate attention
	ExceptionSeverityCritical ExceptionSeverity = "critical" // System integrity at risk
)

// ReconciliationReport represents a complete reconciliation run
type ReconciliationReport struct {
	ID             uuid.UUID            `json:"id"`
	RunType        string               `json:"run_type"` // hourly, daily
	Status         ReconciliationStatus `json:"status"`
	StartedAt      time.Time            `json:"started_at"`
	CompletedAt    *time.Time           `json:"completed_at,omitempty"`
	TotalChecks    int                  `json:"total_checks"`
	PassedChecks   int                  `json:"passed_checks"`
	FailedChecks   int                  `json:"failed_checks"`
	ExceptionsCount int                 `json:"exceptions_count"`
	ErrorMessage   string               `json:"error_message,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt      time.Time            `json:"created_at"`
}

// ReconciliationCheck represents a single check within a reconciliation run
type ReconciliationCheck struct {
	ID               uuid.UUID               `json:"id"`
	ReportID         uuid.UUID               `json:"report_id"`
	CheckType        ReconciliationCheckType `json:"check_type"`
	Status           ReconciliationStatus    `json:"status"`
	ExpectedValue    decimal.Decimal         `json:"expected_value"`
	ActualValue      decimal.Decimal         `json:"actual_value"`
	Difference       decimal.Decimal         `json:"difference"`
	Passed           bool                    `json:"passed"`
	ErrorMessage     string                  `json:"error_message,omitempty"`
	ExecutionTimeMs  int64                   `json:"execution_time_ms"`
	Metadata         map[string]interface{}  `json:"metadata,omitempty"`
	CreatedAt        time.Time               `json:"created_at"`
}

// ReconciliationException represents a discrepancy found during reconciliation
type ReconciliationException struct {
	ID               uuid.UUID               `json:"id"`
	ReportID         uuid.UUID               `json:"report_id"`
	CheckID          uuid.UUID               `json:"check_id"`
	CheckType        ReconciliationCheckType `json:"check_type"`
	Severity         ExceptionSeverity       `json:"severity"`
	Description      string                  `json:"description"`
	ExpectedValue    decimal.Decimal         `json:"expected_value"`
	ActualValue      decimal.Decimal         `json:"actual_value"`
	Difference       decimal.Decimal         `json:"difference"`
	Currency         string                  `json:"currency"`
	AffectedUserID   *uuid.UUID              `json:"affected_user_id,omitempty"`
	AffectedEntity   string                  `json:"affected_entity,omitempty"` // account_id, transaction_id, etc.
	AutoCorrected    bool                    `json:"auto_corrected"`
	CorrectionAction string                  `json:"correction_action,omitempty"`
	ResolvedAt       *time.Time              `json:"resolved_at,omitempty"`
	ResolvedBy       string                  `json:"resolved_by,omitempty"`
	ResolutionNotes  string                  `json:"resolution_notes,omitempty"`
	Metadata         map[string]interface{}  `json:"metadata,omitempty"`
	CreatedAt        time.Time               `json:"created_at"`
}

// ReconciliationCheckResult represents the result of running a reconciliation check
type ReconciliationCheckResult struct {
	CheckType     ReconciliationCheckType
	Passed        bool
	ExpectedValue decimal.Decimal
	ActualValue   decimal.Decimal
	Difference    decimal.Decimal
	Exceptions    []ReconciliationException
	ExecutionTime time.Duration
	ErrorMessage  string
	Metadata      map[string]interface{}
}

// NewReconciliationReport creates a new reconciliation report
func NewReconciliationReport(runType string) *ReconciliationReport {
	now := time.Now()
	return &ReconciliationReport{
		ID:        uuid.New(),
		RunType:   runType,
		Status:    ReconciliationStatusPending,
		StartedAt: now,
		CreatedAt: now,
		Metadata:  make(map[string]interface{}),
	}
}

// NewReconciliationException creates a new reconciliation exception
func NewReconciliationException(
	reportID, checkID uuid.UUID,
	checkType ReconciliationCheckType,
	severity ExceptionSeverity,
	description string,
	expected, actual decimal.Decimal,
	currency string,
) *ReconciliationException {
	return &ReconciliationException{
		ID:            uuid.New(),
		ReportID:      reportID,
		CheckID:       checkID,
		CheckType:     checkType,
		Severity:      severity,
		Description:   description,
		ExpectedValue: expected,
		ActualValue:   actual,
		Difference:    actual.Sub(expected),
		Currency:      currency,
		AutoCorrected: false,
		CreatedAt:     time.Now(),
		Metadata:      make(map[string]interface{}),
	}
}

// DetermineSeverity determines the severity level based on the discrepancy amount
func DetermineSeverity(difference decimal.Decimal, currency string) ExceptionSeverity {
	absDiff := difference.Abs()

	// Thresholds (configurable via environment)
	lowThreshold := decimal.NewFromFloat(1.0)      // $1
	mediumThreshold := decimal.NewFromFloat(100.0) // $100
	highThreshold := decimal.NewFromFloat(1000.0)  // $1000

	switch {
	case absDiff.LessThanOrEqual(lowThreshold):
		return ExceptionSeverityLow
	case absDiff.LessThanOrEqual(mediumThreshold):
		return ExceptionSeverityMedium
	case absDiff.LessThanOrEqual(highThreshold):
		return ExceptionSeverityHigh
	default:
		return ExceptionSeverityCritical
	}
}

// CanAutoCorrect determines if an exception can be automatically corrected
func (e *ReconciliationException) CanAutoCorrect() bool {
	return e.Severity == ExceptionSeverityLow && !e.AutoCorrected
}

// MarkCorrected marks the exception as auto-corrected
func (e *ReconciliationException) MarkCorrected(action string) {
	e.AutoCorrected = true
	e.CorrectionAction = action
	now := time.Now()
	e.ResolvedAt = &now
	e.ResolvedBy = "system"
}

// MarkResolved marks the exception as manually resolved
func (e *ReconciliationException) MarkResolved(resolvedBy, notes string) {
	now := time.Now()
	e.ResolvedAt = &now
	e.ResolvedBy = resolvedBy
	e.ResolutionNotes = notes
}
