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

// MandateSuggestionRepository stores and retrieves mandate suggestions.
type MandateSuggestionRepository interface {
	CreateSuggestion(ctx context.Context, s *entities.MiriamMandateSuggestion) error
	ListPendingSuggestions(ctx context.Context, userID uuid.UUID) ([]entities.MiriamMandateSuggestion, error)
	DismissSuggestion(ctx context.Context, suggestionID uuid.UUID) error
	AcceptSuggestion(ctx context.Context, suggestionID uuid.UUID) (*entities.MiriamAutopilotMandate, error)
}

// MandateProvider checks whether a user already has an active mandate of a given type.
type MandateProvider interface {
	HasActiveMandate(ctx context.Context, userID uuid.UUID, actionType string) (bool, error)
}

// MandateSuggestionEngine analyzes user state and memory to propose new mandates.
type MandateSuggestionEngine struct {
	repo        MandateSuggestionRepository
	mandaes     MandateProvider
	balances    BalanceProvider
	spending    SpendingProvider
	obligations ObligationProvider
	profiles    FinancialProfileProvider
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
		repo: repo, mandaes: mandates, balances: balances, spending: spending,
		obligations: obligations, profiles: profiles, logger: logger,
	}
}

// GenerateSuggestions produces mandate recommendations based on state and memory.
func (e *MandateSuggestionEngine) GenerateSuggestions(ctx context.Context, userID uuid.UUID, state *entities.MiriamMoneyState, facts []entities.MiriamUserFact) ([]entities.MiriamMandateSuggestion, error) {
	var suggestions []entities.MiriamMandateSuggestion

	// 1. Suggest transfer_to_stash if user has regular surplus and no mandate
	if !e.hasActiveMandate(ctx, userID, entities.MiriamMandateTransferToStash) {
		if s := e.suggestTransferToStash(userID, state); s != nil {
			suggestions = append(suggestions, *s)
		}
	}

	// 2. Suggest stash_top_up if idle surplus detected
	if s := e.suggestStashTopUp(userID, state); s != nil {
		suggestions = append(suggestions, *s)
	}

	// 3. Suggest bill_reservation if obligations regularly exceed spend
	if s := e.suggestBillReservation(userID, state); s != nil {
		suggestions = append(suggestions, *s)
	}

	// 4. Memory-driven suggestions
	for _, f := range facts {
		switch {
		case f.Category == entities.FactCategoryGoal && f.Confidence.GreaterThanOrEqual(decimal.NewFromFloat(0.7)):
			if s := e.suggestGoalContribution(userID, state, f.Fact); s != nil {
				suggestions = append(suggestions, *s)
			}
		case f.Category == entities.FactCategoryStashBehavior && f.Confidence.GreaterThanOrEqual(decimal.NewFromFloat(0.7)):
			// Known stash behavior — tailor stash suggestions to match
			if containsRiskKeyword(f.Fact, "set-and-forget", "automatic", "regular") {
				if s := e.suggestTransferToStash(userID, state); s != nil {
					s.Name = "Automated quiet stash"
					s.Confidence = 75
					suggestions = append(suggestions, *s)
				}
			}
		case f.Category == entities.FactCategoryRiskPreference && f.Confidence.GreaterThanOrEqual(decimal.NewFromFloat(0.7)):
			if containsRiskKeyword(f.Fact, "aggressive", "growth", "maximize") {
				if s := e.suggestStashTopUp(userID, state); s != nil {
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

func (e *MandateSuggestionEngine) suggestTransferToStash(userID uuid.UUID, state *entities.MiriamMoneyState) *entities.MiriamMandateSuggestion {
	spend, err := e.balances.GetAccountBalance(context.Background(), userID, entities.AccountTypeSpendingBalance)
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

	maxAction := minDecimal(surplus.Mul(decimal.NewFromFloat(0.3)), decimal.NewFromInt(100))
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
		Confidence:          60,
		CreatedAt:           time.Now().UTC(),
	}
}

func (e *MandateSuggestionEngine) suggestStashTopUp(userID uuid.UUID, state *entities.MiriamMoneyState) *entities.MiriamMandateSuggestion {
	if state.StashTarget.IsZero() {
		return nil
	}

	stash, err := e.balances.GetAccountBalance(context.Background(), userID, entities.AccountTypeStashBalance)
	if err != nil {
		return nil
	}

	if stash.GreaterThanOrEqual(state.StashTarget) {
		return nil
	}

	spend, err := e.balances.GetAccountBalance(context.Background(), userID, entities.AccountTypeSpendingBalance)
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
		Confidence:          55,
		CreatedAt:           time.Now().UTC(),
	}
}

func (e *MandateSuggestionEngine) suggestBillReservation(userID uuid.UUID, state *entities.MiriamMoneyState) *entities.MiriamMandateSuggestion {
	if !state.UpcomingObligations.IsPositive() {
		return nil
	}

	spend, err := e.balances.GetAccountBalance(context.Background(), userID, entities.AccountTypeSpendingBalance)
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
			Confidence:          65,
			CreatedAt:           time.Now().UTC(),
		}
	}

	return nil
}

func (e *MandateSuggestionEngine) suggestGoalContribution(userID uuid.UUID, state *entities.MiriamMoneyState, goalFact string) *entities.MiriamMandateSuggestion {
	spend, err := e.balances.GetAccountBalance(context.Background(), userID, entities.AccountTypeSpendingBalance)
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
		Confidence:          50,
		CreatedAt:           time.Now().UTC(),
	}
}

func (e *MandateSuggestionEngine) hasActiveMandate(ctx context.Context, userID uuid.UUID, actionType string) bool {
	if e.mandaes == nil {
		return false
	}
	exists, err := e.mandaes.HasActiveMandate(ctx, userID, actionType)
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
