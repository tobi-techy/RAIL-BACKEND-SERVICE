package miriam

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// DecisionRepository persists decisions for audit and learning.
type DecisionRepository interface {
	CreateDecision(ctx context.Context, d *entities.MiriamDecision) error
	RecentDecisions(ctx context.Context, userID uuid.UUID, limit int) ([]entities.MiriamDecision, error)
	GetDecision(ctx context.Context, decisionID uuid.UUID) (*entities.MiriamDecision, error)
	CreateOutcome(ctx context.Context, o *entities.MiriamDecisionOutcome) error
	GetLearningModel(ctx context.Context, userID uuid.UUID, category string) (*entities.MiriamLearningModel, error)
	UpsertLearningModel(ctx context.Context, m *entities.MiriamLearningModel) error
	ListOutcomesSince(ctx context.Context, userID uuid.UUID, category string, since time.Time) ([]entities.MiriamDecisionOutcome, error)
}

// MemoryReader reads user facts for decision context.
type MemoryReader interface {
	GetActiveFacts(ctx context.Context, userID uuid.UUID) ([]*entities.MiriamUserFact, error)
}

// DecisionEngine evaluates whether a mandate action should execute, delay,
// adjust, skip, or escalate — using predictions, memory, and learning.
type DecisionEngine struct {
	repo        DecisionRepository
	predictions *PredictiveEngine
	memory      MemoryReader
	logger      *zap.Logger
}

// NewDecisionEngine creates a decision engine.
func NewDecisionEngine(repo DecisionRepository, predictions *PredictiveEngine, memory MemoryReader, logger *zap.Logger) *DecisionEngine {
	return &DecisionEngine{
		repo: repo, predictions: predictions, memory: memory, logger: logger,
	}
}

// MakeDecision evaluates a mandate in context and returns a decision.
func (e *DecisionEngine) MakeDecision(ctx context.Context, dc *entities.DecisionContext) (*entities.MiriamDecision, error) {
	factors := e.serializeFactors(dc)
	originalAmount := e.calculateCandidateAmount(dc)

	decisionType := e.determineDecisionType(dc)
	adjustedAmount := e.calculateAdjustedAmount(dc, originalAmount)
	confidence := e.computeConfidence(dc)
	reason := e.generateReason(dc, decisionType, adjustedAmount)

	decision := &entities.MiriamDecision{
		ID:              uuid.New(),
		UserID:          dc.MoneyState.UserID,
		MandateID:       dc.Mandate.ID,
		DecisionType:    decisionType,
		Reason:          reason,
		ConfidenceScore: confidence,
		Factors:         factors,
		OriginalAmount:  originalAmount,
		AdjustedAmount:  adjustedAmount,
		CreatedAt:       time.Now().UTC(),
	}

	if e.repo != nil {
		if err := e.repo.CreateDecision(ctx, decision); err != nil && e.logger != nil {
			e.logger.Warn("decision recording failed", zap.Error(err))
		}
	}

	return decision, nil
}

// ShouldExecute returns true if the decision type is execute.
func ShouldExecute(d *entities.MiriamDecision) bool {
	return d.DecisionType == entities.DecisionExecute
}

// ExecuteAmount returns the amount to use for execution (adjusted if decision is adjust_amount).
func ExecuteAmount(d *entities.MiriamDecision) decimal.Decimal {
	if d.DecisionType == entities.DecisionAdjustAmount {
		return d.AdjustedAmount
	}
	return d.OriginalAmount
}

func (e *DecisionEngine) determineDecisionType(dc *entities.DecisionContext) string {
	// Critical predictions force escalation or delay
	if dc.Predictions != nil {
		for _, p := range dc.Predictions.ActivePredictions {
			if p.Severity == entities.SeverityCritical && p.Probability.GreaterThan(decimal.NewFromFloat(0.7)) {
				switch p.PredictionType {
				case entities.PredictionCashShortfall:
					return entities.DecisionSkip
				case entities.PredictionBillPressure:
					// If bill pressure and mandate is transfer_to_stash, skip
					if dc.Mandate.ActionType == entities.MiriamMandateTransferToStash {
						return entities.DecisionSkip
					}
					return entities.DecisionExecute
				case entities.PredictionIncomeGap:
					return entities.DecisionDelay
				}
			}
		}
	}

	// Memory facts can trigger escalation
	if e.shouldEscalateFromMemory(dc.MemoryFacts) {
		return entities.DecisionEscalate
	}

	// Low confidence predictions → delay
	if dc.Predictions != nil && dc.Predictions.RiskScore > 70 {
		return entities.DecisionDelay
	}

	// Recent failures → adjust amount
	if dc.LearningBias.LessThan(decimal.NewFromInt(-2)) {
		return entities.DecisionAdjustAmount
	}

	return entities.DecisionExecute
}

func (e *DecisionEngine) calculateAdjustedAmount(dc *entities.DecisionContext, original decimal.Decimal) decimal.Decimal {
	if original.LessThanOrEqual(decimal.Zero) {
		return decimal.Zero
	}

	amount := original

	// Apply learning bias
	if dc.LearningBias.LessThan(decimal.NewFromInt(-2)) {
		amount = amount.Mul(decimal.NewFromFloat(0.50))
	} else if dc.LearningBias.GreaterThan(decimal.NewFromInt(3)) {
		amount = amount.Mul(decimal.NewFromFloat(1.10))
	}

	// Reduce amount if predictions show cash shortfall risk
	if dc.Predictions != nil {
		for _, p := range dc.Predictions.ActivePredictions {
			if p.PredictionType == entities.PredictionCashShortfall && p.Severity == entities.SeverityHigh {
				amount = amount.Mul(decimal.NewFromFloat(0.70))
				break
			}
		}
	}

	// Memory overrides: if user expressed financial fear, reduce
	for _, f := range dc.MemoryFacts {
		if f.Category == entities.FactCategoryFear {
			amount = amount.Mul(decimal.NewFromFloat(0.80))
			break
		}
	}

	return amount.RoundBank(2)
}

func (e *DecisionEngine) calculateCandidateAmount(dc *entities.DecisionContext) decimal.Decimal {
	return dc.Mandate.MaxAmountPerAction
}

func (e *DecisionEngine) computeConfidence(dc *entities.DecisionContext) int {
	score := 50

	if dc.MoneyState.ConfidenceScore >= 75 {
		score += 20
	} else if dc.MoneyState.ConfidenceScore >= 50 {
		score += 10
	}

	if dc.Predictions != nil && len(dc.Predictions.ActivePredictions) > 0 {
		score -= 10 // Predictions introduce uncertainty
	}

	if dc.LearningBias.GreaterThan(decimal.NewFromInt(-1)) && dc.LearningBias.LessThan(decimal.NewFromInt(2)) {
		score += 10 // Neutral learning bias increases confidence
	}

	if len(dc.MemoryFacts) > 0 {
		score += 5
	}

	if score > 100 {
		return 100
	}
	if score < 0 {
		return 0
	}
	return score
}

func (e *DecisionEngine) generateReason(dc *entities.DecisionContext, decisionType string, amount decimal.Decimal) string {
	switch decisionType {
	case entities.DecisionExecute:
		return fmt.Sprintf("Executing $%s transfer based on current financial state.", amount.StringFixed(2))
	case entities.DecisionDelay:
		return "Delaying action due to elevated risk predictions. Will re-evaluate on next cycle."
	case entities.DecisionAdjustAmount:
		return fmt.Sprintf("Reducing transfer to $%s based on recent user feedback.", amount.StringFixed(2))
	case entities.DecisionSkip:
		return "Skipping action to protect Spend balance. Upcoming obligations or shortfall risk detected."
	case entities.DecisionEscalate:
		return "This action needs your confirmation before proceeding."
	default:
		return "Decision made based on current financial intelligence."
	}
}

func (e *DecisionEngine) serializeFactors(dc *entities.DecisionContext) json.RawMessage {
	factors := map[string]interface{}{
		"money_state_confidence": dc.MoneyState.ConfidenceLevel,
		"money_state_anomalies":  dc.MoneyState.AnomalyCount,
		"runway_days":            dc.MoneyState.LiquidityRunwayDays,
		"event_type":             dc.EventType,
		"mandate_type":           dc.Mandate.ActionType,
		"learning_bias":          dc.LearningBias.StringFixed(4),
		"memory_fact_count":      len(dc.MemoryFacts),
	}

	if dc.Predictions != nil {
		factors["risk_score"] = dc.Predictions.RiskScore
		factors["active_predictions"] = len(dc.Predictions.ActivePredictions)
		factors["top_risk"] = dc.Predictions.TopRisk
	}

	raw, err := json.Marshal(factors)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

func (e *DecisionEngine) shouldEscalateFromMemory(facts []entities.MiriamUserFact) bool {
	for _, f := range facts {
		if f.Category == entities.FactCategoryFear && f.Confidence.GreaterThanOrEqual(decimal.NewFromFloat(0.7)) {
			return true
		}
	}
	return false
}

// RecordOutcome records the outcome of a decision for learning.
func (e *DecisionEngine) RecordOutcome(ctx context.Context, decisionID, userID uuid.UUID, outcome string, feedbackScore decimal.Decimal) error {
	if e.repo == nil {
		return nil
	}
	o := &entities.MiriamDecisionOutcome{
		ID:            uuid.New(),
		UserID:        userID,
		DecisionID:    decisionID,
		Outcome:       outcome,
		FeedbackScore: feedbackScore,
		CreatedAt:     time.Now().UTC(),
	}
	return e.repo.CreateOutcome(ctx, o)
}

func (e *DecisionEngine) GetLearningBias(ctx context.Context, userID uuid.UUID, category string) decimal.Decimal {
	if e.repo == nil {
		return decimal.Zero
	}
	model, err := e.repo.GetLearningModel(ctx, userID, category)
	if err != nil || model == nil {
		return decimal.Zero
	}
	return model.CurrentBias
}
