package context

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/ai/memory"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// NairaOrderSummary is a lightweight view of recent paj orders.
type NairaOrderSummary struct {
	OrderType   string
	FiatAmount  float64
	TokenAmount float64
	Rate        float64
	Currency    string
	CreatedAt   time.Time
}

// MonoSpendingAnalysis is the minimal shape needed for context assembly.
type MonoSpendingAnalysis struct {
	TotalDebits      int64
	TotalCredits     int64
	SavingsRate      float64
	TransactionCount int
	Period           struct{ Days int }
}

// AnomalyResult is a minimal shape used by the anomaly context slot.
type AnomalyResult struct {
	Severity    string
	Title       string
	Description string
}

// PortfolioStats is the minimal portfolio shape for coaching context.
type PortfolioStats struct {
	TotalValue decimal.Decimal
}

// ToneProfile is a minimal view of the user's learned messaging style.
type ToneProfile struct {
	SampleCount   int
	PreferredName string
	Brevity       decimal.Decimal
	LanguageStyle string
	LocaleStyle   string
}

// JourneySignals is the shared struct used by journey/orchestrator code.
type JourneySignals struct {
	User         *entities.UserProfile
	Phase        OnboardingPhase
	MessageCount int
	HasFunded    bool
	DepositCount int
	MonoLinked   bool
}

// OnboardingPhase classifies where a user is in their Rail journey.
type OnboardingPhase string

const (
	PhaseFirstConversation    OnboardingPhase = "first_conversation"
	PhaseOnboardingIncomplete OnboardingPhase = "onboarding_incomplete"
	PhaseOnboardedNotFunded   OnboardingPhase = "onboarded_not_funded"
	PhaseFundedNewbie         OnboardingPhase = "funded_newbie"
	PhaseEstablished          OnboardingPhase = "established"
)

// ContextDeps is the slim dependency struct for context assembly.
// All external calls are behind function fields to avoid import cycles
// between the context subpackage and the root ai package.
type ContextDeps struct {
	// Provider functions — each accepts context for timeout/cancellation.
	GetBalanceFn           func(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error)
	GetUserCountryFn       func(ctx context.Context, userID uuid.UUID) (string, error)
	GetFinancialProfileFn  func(ctx context.Context, userID uuid.UUID) (*entities.FinancialProfile, error)
	ListActiveObligationsFn func(ctx context.Context, userID uuid.UUID) ([]entities.FinancialObligation, error)
	GetPortfolioStatsFn    func(ctx context.Context, userID uuid.UUID) (*PortfolioStats, error)
	GetLatestRateFn        func(ctx context.Context, from, to string) (decimal.Decimal, error)
	GetMoneyStateFn        func(ctx context.Context, userID uuid.UUID) (*entities.MiriamMoneyState, error)
	SearchMemoryRankedFn   func(ctx context.Context, userID, query string, limit int) ([]string, error)
	GetAnomaliesFn         func(ctx context.Context, userID uuid.UUID) ([]AnomalyResult, error)
	GetWorkingMemoryFn     func(ctx context.Context, userID uuid.UUID) *memory.WorkingMemoryEntry
	GetActiveThreadFn      func(ctx context.Context, userID uuid.UUID) string
	GetFinancialEventsFn   func(ctx context.Context, userID uuid.UUID) string
	GetEnrichmentSummaryFn func(ctx context.Context, userID uuid.UUID) (string, error)
	GetPendingActionFn     func(ctx context.Context, convID uuid.UUID) *entities.PendingAction
	GetMonoSpendingFn      func(ctx context.Context, userID uuid.UUID, days int) (*MonoSpendingAnalysis, error)
	GetBankUploadSummaryFn func(ctx context.Context, userID uuid.UUID) (int, []string, error)
	GetNairaOrdersFn       func(ctx context.Context, userID uuid.UUID, limit int) ([]NairaOrderSummary, error)

	// Complex context builders passed as functions.
	BuildMemoryContextFn  func(ctx context.Context, userID uuid.UUID, message string) string
	ToneProfileFn         func(ctx context.Context, userID uuid.UUID) *ToneProfile
	MemoryCallbacksFn     func(ctx context.Context, userID uuid.UUID, limit int) ([]string, error)
	JourneySignalsFn      func(ctx context.Context, userID uuid.UUID) (JourneySignals, bool)
	JourneyBlockFn        func(ctx context.Context, userID uuid.UUID, sigs JourneySignals) string
	ControlLevelFn        func(ctx context.Context, userID uuid.UUID) string
	BankStatementBuildFn  func(ctx context.Context, userID uuid.UUID) string

	Cache  *ContextCache
	Logger *zap.Logger
}
