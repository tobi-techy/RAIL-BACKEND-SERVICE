package miriam

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// ErrSuggestionNotFound is returned when a suggestion does not exist for the
// given user or has already been accepted/dismissed.
var ErrSuggestionNotFound = fmt.Errorf("suggestion not found or already actioned")

// MandateSuggestionRepository stores and retrieves mandate suggestions.
type MandateSuggestionRepository interface {
	CreateSuggestion(ctx context.Context, s *entities.MiriamMandateSuggestion) error
	ListPendingSuggestions(ctx context.Context, userID uuid.UUID) ([]entities.MiriamMandateSuggestion, error)
	// DismissSuggestion atomically verifies ownership (userID + pending status) and
	// marks the suggestion dismissed in a single query — no separate pre-check needed.
	DismissSuggestion(ctx context.Context, userID uuid.UUID, suggestionID uuid.UUID) error
	// AcceptSuggestion atomically verifies ownership (userID + pending status) and
	// creates the mandate in a transaction — no separate pre-check needed.
	AcceptSuggestion(ctx context.Context, userID uuid.UUID, suggestionID uuid.UUID) (*entities.MiriamAutopilotMandate, error)
}

// MandateProvider checks whether a user already has an active mandate of a given type.
type MandateProvider interface {
	HasActiveMandate(ctx context.Context, userID uuid.UUID, actionType string) (bool, error)
}

// LearningBiasProvider reads the historical bias for a mandate category.
// A positive bias means past executions of this category were mostly successful;
// negative means the user rejected or reverted them.
type LearningBiasProvider interface {
	GetLearningBias(ctx context.Context, userID uuid.UUID, category string) decimal.Decimal
}

// MandateSuggestionEngine analyzes user state and memory to propose new mandates.
type MandateSuggestionEngine struct {
	repo        MandateSuggestionRepository
	mandates    MandateProvider
	balances    BalanceProvider
	spending    SpendingProvider
	obligations ObligationProvider
	profiles    FinancialProfileProvider
	learning    LearningBiasProvider
	logger      *zap.Logger
}

// NewMandateSuggestionEngine creates a suggestion engine.
func NewMandateSuggestionEngine(
	repo MandateSuggestionRepository,
	mandates MandateProvider,
	balances BalanceProvider,
	spending SpendingProvider,
	obligations ObligationProvider,
	profiles FinancialProfileProvider,
	logger *zap.Logger,
) *MandateSuggestionEngine {
	return &MandateSuggestionEngine{
		repo: repo, mandates: mandates, balances: balances, spending: spending,
		obligations: obligations, profiles: profiles, logger: logger,
	}
}

// SetLearningBiasProvider injects the learning model so suggestion confidence
// adjusts based on historical mandate outcomes.
func (e *MandateSuggestionEngine) SetLearningBiasProvider(p LearningBiasProvider) {
	e.learning = p
}

// adjustConfidence scales a suggestion's confidence by the learning bias.
// Bias range: [-1, +1]. Positive = past successes → boost confidence.
// Negative = past rejections/failures → dampen confidence.
func (e *MandateSuggestionEngine) adjustConfidence(ctx context.Context, userID uuid.UUID, category string, base int) int {
	if e.learning == nil {
		return base
	}
	bias := e.learning.GetLearningBias(ctx, userID, category)
	// bias is [-1, +1], map to a [-20, +20] confidence adjustment
	adj := int(bias.Mul(decimal.NewFromInt(20)).IntPart())
	adjusted := base + adj
	if adjusted < 10 {
		adjusted = 10
	}
	if adjusted > 90 {
		adjusted = 90
	}
	return adjusted
}

// GenerateSuggestions produces mandate recommendations based on state and memory.
func (e *MandateSuggestionEngine) GenerateSuggestions(ctx context.Context, userID uuid.UUID, state *entities.MiriamMoneyState, facts []entities.MiriamUserFact) ([]entities.MiriamMandateSuggestion, error) {
	var suggestions []entities.MiriamMandateSuggestion

	// 1. Suggest transfer_to_stash if user has regular surplus and no mandate
	if !e.hasActiveMandate(ctx, userID, entities.MiriamMandateTransferToStash) {
		if s := e.suggestTransferToStash(ctx, userID, state); s != nil {
			suggestions = append(suggestions, *s)
		}
	}

	// 2. Suggest stash_top_up if idle surplus detected
	if s := e.suggestStashTopUp(ctx, userID, state); s != nil {
		suggestions = append(suggestions, *s)
	}

	// 3. Suggest bill_reservation if obligations regularly exceed spend
	if s := e.suggestBillReservation(ctx, userID, state); s != nil {
		suggestions = append(suggestions, *s)
	}

	// 4. Memory-driven suggestions
	for _, f := range facts {
		switch {
		case f.Category == entities.FactCategoryGoal && f.Confidence.GreaterThanOrEqual(decimal.NewFromFloat(0.7)):
			if s := e.suggestGoalContribution(ctx, userID, state, f.Fact); s != nil {
				suggestions = append(suggestions, *s)
			}
		case f.Category == entities.FactCategoryStashBehavior && f.Confidence.GreaterThanOrEqual(decimal.NewFromFloat(0.7)):
			// Known stash behavior — tailor stash suggestions to match
			if containsRiskKeyword(f.Fact, "set-and-forget", "automatic", "regular") {
				if s := e.suggestTransferToStash(ctx, userID, state); s != nil {
					s.Name = "Automated quiet stash"
					s.Confidence = 75
					suggestions = append(suggestions, *s)
				}
			}
		case f.Category == entities.FactCategoryRiskPreference && f.Confidence.GreaterThanOrEqual(decimal.NewFromFloat(0.7)):
			if containsRiskKeyword(f.Fact, "aggressive", "growth", "maximize") {
				if s := e.suggestStashTopUp(ctx, userID, state); s != nil {
					s.SuggestedMaxAmount = s.SuggestedMaxAmount.Mul(decimal.NewFromFloat(1.5))
					s.Confidence = 70
					suggestions = append(suggestions, *s)
				}
			}
		}
	}

	// Persist suggestions
	for i := range suggestions {
		if e.repo != nil {
			_ = e.repo.CreateSuggestion(ctx, &suggestions[i])
		}
	}

	return suggestions, nil
}

func (e *MandateSuggestionEngine) suggestTransferToStash(ctx context.Context, userID uuid.UUID, state *entities.MiriamMoneyState) *entities.MiriamMandateSuggestion {
	spend, err := e.balances.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil || !spend.IsPositive() {
		return nil
	}

	// Need surplus beyond obligations + 14-day runway
	obligationBuffer := state.UpcomingObligations
	dailyOutflow := decimal.Zero
	if state.LiquidityRunwayDays > 0 {
		dailyOutflow = spend.Div(decimal.NewFromInt(int64(state.LiquidityRunwayDays)))
	}
	runwayBuffer := dailyOutflow.Mul(decimal.NewFromInt(14))
	surplus := spend.Sub(obligationBuffer).Sub(runwayBuffer)

	if surplus.LessThan(decimal.NewFromInt(20)) {
		return nil
	}

	// 30% of surplus, capped at $500 — proportional to the user's actual surplus
	maxAction := surplus.Mul(decimal.NewFromFloat(0.3))
	if maxAction.GreaterThan(decimal.NewFromInt(500)) {
		maxAction = decimal.NewFromInt(500)
	}
	maxDay := maxAction.Mul(decimal.NewFromInt(3)) // Allow 3x per day max

	return &entities.MiriamMandateSuggestion{
		ID:                  uuid.New(),
		UserID:              userID,
		Name:                "Quiet Stash moves",
		ActionType:          entities.MiriamMandateTransferToStash,
		Reasoning:           fmt.Sprintf("You consistently have $%s extra. I can quietly move up to $%s to Stash when it's safe.", surplus.StringFixed(0), maxAction.StringFixed(0)),
		SuggestedMaxAmount:  maxAction,
		SuggestedMaxDay:     maxDay,
		SuggestedMinBalance: obligationBuffer.Add(decimal.NewFromInt(100)),
		SuggestedCooldown:   1440, // 24 hours
		Confidence:          e.adjustConfidence(ctx, userID, entities.MiriamMandateTransferToStash, 60),
		CreatedAt:           time.Now().UTC(),
	}
}

func (e *MandateSuggestionEngine) suggestStashTopUp(ctx context.Context, userID uuid.UUID, state *entities.MiriamMoneyState) *entities.MiriamMandateSuggestion {
	if state.StashTarget.IsZero() {
		return nil
	}

	stash, err := e.balances.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		return nil
	}

	if stash.GreaterThanOrEqual(state.StashTarget) {
		return nil
	}

	spend, err := e.balances.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return nil
	}

	// Only suggest if spend has enough buffer
	buffer := spend.Sub(state.UpcomingObligations)
	if buffer.LessThan(decimal.NewFromInt(200)) {
		return nil
	}

	suggested := minDecimal(buffer.Mul(decimal.NewFromFloat(0.15)), state.StashTarget.Sub(stash))
	if suggested.LessThan(decimal.NewFromInt(10)) {
		return nil
	}

	return &entities.MiriamMandateSuggestion{
		ID:                  uuid.New(),
		UserID:              userID,
		Name:                "Stash top-up when surplus detected",
		ActionType:          MiriamMandateStashTopUp,
		Reasoning:           fmt.Sprintf("Your Stash is $%s below target. I can top it up when you have extra.", state.StashTarget.Sub(stash).StringFixed(0)),
		SuggestedMaxAmount:  suggested,
		SuggestedMaxDay:     suggested,
		SuggestedMinBalance: state.UpcomingObligations.Add(decimal.NewFromInt(150)),
		SuggestedCooldown:   10080, // weekly
		Confidence:          e.adjustConfidence(ctx, userID, MiriamMandateStashTopUp, 55),
		CreatedAt:           time.Now().UTC(),
	}
}

func (e *MandateSuggestionEngine) suggestBillReservation(ctx context.Context, userID uuid.UUID, state *entities.MiriamMoneyState) *entities.MiriamMandateSuggestion {
	if !state.UpcomingObligations.IsPositive() {
		return nil
	}

	spend, err := e.balances.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return nil
	}

	// If obligations regularly exceed spend, suggest reservation
		if state.UpcomingObligations.GreaterThan(spend.Mul(decimal.NewFromFloat(0.6))) {
		reserveAmount := state.UpcomingObligations.Mul(decimal.NewFromFloat(0.5))
		return &entities.MiriamMandateSuggestion{
			ID:                  uuid.New(),
			UserID:              userID,
			Name:                "Bill reservation",
			ActionType:          MiriamMandateBillReservation,
			Reasoning:           fmt.Sprintf("Your upcoming bills ($%s) are a big chunk of your balance. I can set aside money automatically.", state.UpcomingObligations.StringFixed(0)),
			SuggestedMaxAmount:  reserveAmount,
			SuggestedMaxDay:     reserveAmount,
			SuggestedMinBalance: decimal.NewFromInt(50),
			SuggestedCooldown:   43200, // monthly
			Confidence:          e.adjustConfidence(ctx, userID, MiriamMandateBillReservation, 65),
			CreatedAt:           time.Now().UTC(),
		}
	}

	return nil
}

func (e *MandateSuggestionEngine) suggestGoalContribution(ctx context.Context, userID uuid.UUID, state *entities.MiriamMoneyState, goalFact string) *entities.MiriamMandateSuggestion {
	spend, err := e.balances.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil || !spend.IsPositive() {
		return nil
	}

	buffer := spend.Sub(state.UpcomingObligations)
	if buffer.LessThan(decimal.NewFromInt(50)) {
		return nil
	}

	amount := buffer.Mul(decimal.NewFromFloat(0.1))
	if amount.LessThan(decimal.NewFromInt(5)) {
		return nil
	}

	return &entities.MiriamMandateSuggestion{
		ID:                  uuid.New(),
		UserID:              userID,
		Name:                fmt.Sprintf("Goal contribution: %s", truncateString(goalFact, 40)),
		ActionType:          MiriamMandateGoalContribution,
		Reasoning:           fmt.Sprintf("You mentioned: \"%s\". I can quietly contribute to this goal when you have extra.", goalFact),
		SuggestedMaxAmount:  amount,
		SuggestedMaxDay:     amount,
		SuggestedMinBalance: state.UpcomingObligations.Add(decimal.NewFromInt(100)),
		SuggestedCooldown:   10080,
		Confidence:          e.adjustConfidence(ctx, userID, MiriamMandateGoalContribution, 50),
		CreatedAt:           time.Now().UTC(),
	}
}

// ListSuggestions returns pending mandate suggestions for a user.
func (e *MandateSuggestionEngine) ListSuggestions(ctx context.Context, userID uuid.UUID) ([]entities.MiriamMandateSuggestion, error) {
	if e.repo == nil {
		return []entities.MiriamMandateSuggestion{}, nil
	}
	suggestions, err := e.repo.ListPendingSuggestions(ctx, userID)
	if err != nil {
		return nil, err
	}
	if suggestions == nil {
		return []entities.MiriamMandateSuggestion{}, nil
	}
	return suggestions, nil
}

// Accept converts a pending suggestion into an active mandate.
// Ownership and pending-status checks are performed atomically in the repository.
func (e *MandateSuggestionEngine) Accept(ctx context.Context, userID uuid.UUID, suggestionID uuid.UUID) (*entities.MiriamAutopilotMandate, error) {
	if e.repo == nil {
		return nil, fmt.Errorf("suggestion service unavailable")
	}
	return e.repo.AcceptSuggestion(ctx, userID, suggestionID)
}

// Dismiss marks a suggestion as dismissed for the user.
// Ownership and pending-status checks are performed atomically in the repository.
func (e *MandateSuggestionEngine) Dismiss(ctx context.Context, userID uuid.UUID, suggestionID uuid.UUID) error {
	if e.repo == nil {
		return fmt.Errorf("suggestion service unavailable")
	}
	return e.repo.DismissSuggestion(ctx, userID, suggestionID)
}

func (e *MandateSuggestionEngine) hasActiveMandate(ctx context.Context, userID uuid.UUID, actionType string) bool {
	if e.mandates == nil {
		return false
	}
	exists, err := e.mandates.HasActiveMandate(ctx, userID, actionType)
	if err != nil {
		return false
	}
	return exists
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
