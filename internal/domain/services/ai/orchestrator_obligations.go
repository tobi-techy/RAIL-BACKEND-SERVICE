package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
)

const (
	ToolListFinancialObligations = "list_financial_obligations"
	ToolFindObligationPayments   = "find_obligation_payment_matches"
	ToolMarkObligationPaid       = "mark_obligation_paid"
)

type FinancialObligationManager interface {
	List(ctx context.Context, userID uuid.UUID, status, obligationType string) ([]entities.FinancialObligation, error)
	MarkPaid(ctx context.Context, userID, id uuid.UUID) (*entities.FinancialObligation, error)
	MarkCancelled(ctx context.Context, userID, id uuid.UUID) (*entities.FinancialObligation, error)
}

func (o *Orchestrator) SetFinancialObligationManager(m FinancialObligationManager) {
	o.obligationManager = m
}

func FinancialObligationTools() []infraai.Tool {
	return []infraai.Tool{
		{
			Name:        ToolListFinancialObligations,
			Description: "List the user's saved financial obligations so Miriam can answer bill, rent, debt, tax, subscription, payroll, vendor, invoice, and family-support questions without creating a new UI. Use before saying what money is already spoken for.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"status": map[string]interface{}{"type": "string", "enum": []string{"active", "paused", "paid", "cancelled", "all"}, "description": "Defaults to active"},
					"type":   map[string]interface{}{"type": "string", "enum": []string{"debt", "invoice", "payroll", "insurance", "education", "rent", "family_support", "tax", "subscription", "vendor_bill", "other"}},
				},
			},
		},
		{
			Name:        ToolMarkObligationPaid,
			Description: "Mark a saved financial obligation as paid after the user explicitly says it has been paid. Requires user confirmation before saving.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"obligation_id": map[string]interface{}{"type": "string", "description": "UUID of the obligation to mark paid"},
					"name":          map[string]interface{}{"type": "string", "description": "Optional obligation name for the confirmation sheet"},
				},
				"required": []string{"obligation_id"},
			},
		},
		{
			Name:        ToolFindObligationPayments,
			Description: "Find recent transactions that look like proof an active obligation was paid. Returns likely matches and a suggested mark_obligation_paid action. Use after bills, rent, subscriptions, tax, family support, invoices, or debts may have been paid.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"days": map[string]interface{}{"type": "integer", "description": "Lookback window in days. Defaults to 45, max 120."},
				},
			},
		},
	}
}

func (o *Orchestrator) executeListFinancialObligations(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	if o.obligationManager == nil {
		return map[string]interface{}{"error": "financial obligation service is unavailable"}, nil
	}

	status := valueStringArg(args["status"])
	if status == "" {
		status = entities.ObligationStatusActive
	}
	if status == "all" {
		status = ""
	}
	obligationType := valueStringArg(args["type"])

	obligations, err := o.obligationManager.List(ctx, userID, status, obligationType)
	if err != nil {
		return nil, fmt.Errorf("list financial obligations: %w", err)
	}

	now := time.Now().UTC()
	items := make([]map[string]interface{}, 0, len(obligations))
	monthlyTotal := decimal.Zero
	criticalTotal := decimal.Zero
	for _, obligation := range obligations {
		monthly := monthlyEquivalent(obligation, now)
		monthlyTotal = monthlyTotal.Add(monthly)
		if obligation.Priority == entities.ObligationPriorityCritical {
			criticalTotal = criticalTotal.Add(monthly)
		}

		item := map[string]interface{}{
			"id":                 obligation.ID.String(),
			"type":               obligation.Type,
			"name":               obligation.Name,
			"amount":             obligation.Amount.StringFixed(2),
			"currency":           obligation.Currency,
			"cadence":            obligation.Cadence,
			"priority":           obligation.Priority,
			"status":             obligation.Status,
			"monthly_equivalent": monthly.StringFixed(2),
		}
		if obligation.DueDate != nil {
			item["due_date"] = obligation.DueDate.Format("2006-01-02")
		}
		if obligation.DueDay != nil {
			item["due_day"] = *obligation.DueDay
		}
		if obligation.Counterparty != nil {
			item["counterparty"] = *obligation.Counterparty
		}
		items = append(items, item)
	}

	return map[string]interface{}{
		"count":                  len(items),
		"status_filter":          coalesceString(status, "all"),
		"type_filter":            obligationType,
		"monthly_total":          monthlyTotal.StringFixed(2),
		"critical_monthly_total": criticalTotal.StringFixed(2),
		"obligations":            items,
		"guidance":               "Use these obligations to explain what money is already spoken for, and stage changes through confirmation instead of asking the user to visit another screen.",
	}, nil
}

func (o *Orchestrator) createMarkObligationPaidAction(ctx context.Context, userID, convID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	if o.obligationManager == nil {
		return map[string]interface{}{"error": "financial obligation service is unavailable"}, nil
	}
	obligationID := valueStringArg(args["obligation_id"])
	if obligationID == "" {
		return map[string]interface{}{"error": "obligation_id is required"}, nil
	}
	if _, err := uuid.Parse(obligationID); err != nil {
		return map[string]interface{}{"error": "obligation_id must be a valid UUID"}, nil
	}

	params := map[string]interface{}{"obligation_id": obligationID}
	description := "Mark obligation as paid"
	if name := valueStringArg(args["name"]); name != "" {
		params["name"] = name
		description = fmt.Sprintf("Mark %s as paid", name)
	}
	action := &entities.PendingAction{
		ID:             uuid.New().String(),
		ConversationID: convID,
		UserID:         userID,
		Action:         ToolMarkObligationPaid,
		Description:    description,
		Params:         params,
		ExpiresAt:      time.Now().Add(pendingActionTTL),
		CreatedAt:      time.Now(),
	}
	if err := o.pending.Set(ctx, convID, action); err != nil {
		return nil, fmt.Errorf("store pending mark-paid action: %w", err)
	}
	return map[string]interface{}{"action_required": true, "pending_action": action}, nil
}

func (o *Orchestrator) executeMarkObligationPaid(ctx context.Context, userID uuid.UUID, params map[string]interface{}) (*entities.FinancialObligation, error) {
	if o.obligationManager == nil {
		return nil, fmt.Errorf("financial obligation service is unavailable")
	}
	obligationID := valueStringArg(params["obligation_id"])
	id, err := uuid.Parse(obligationID)
	if err != nil {
		return nil, fmt.Errorf("obligation_id must be a valid UUID")
	}
	return o.obligationManager.MarkPaid(ctx, userID, id)
}

func (o *Orchestrator) executeFindObligationPaymentMatches(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	if o.obligationManager == nil {
		return map[string]interface{}{"error": "financial obligation service is unavailable"}, nil
	}
	if o.spending == nil {
		return map[string]interface{}{"error": "spending transaction service is unavailable"}, nil
	}

	days := 45
	if value, ok := numberArg(args["days"]); ok && value > 0 {
		days = int(value)
	}
	if days > 120 {
		days = 120
	}

	obligations, err := o.obligationManager.List(ctx, userID, entities.ObligationStatusActive, "")
	if err != nil {
		return nil, fmt.Errorf("list active obligations: %w", err)
	}
	now := time.Now().UTC()
	transactions, err := o.spending.GetTransactions(ctx, userID, now.AddDate(0, 0, -days), now, 100)
	if err != nil {
		return nil, fmt.Errorf("list recent transactions: %w", err)
	}

	matches := make([]map[string]interface{}, 0)
	for _, obligation := range obligations {
		for _, tx := range transactions {
			score, reasons := obligationPaymentMatchScore(obligation, tx)
			if score < 55 {
				continue
			}
			matches = append(matches, map[string]interface{}{
				"obligation_id":      obligation.ID.String(),
				"obligation_name":    obligation.Name,
				"obligation_type":    obligation.Type,
				"obligation_amount":  obligation.Amount.StringFixed(2),
				"transaction_date":   tx.Date,
				"transaction_amount": tx.Amount.StringFixed(2),
				"transaction_label":  coalesceString(tx.Source, tx.Category),
				"confidence":         confidenceLabel(score),
				"score":              score,
				"reasons":            reasons,
				"suggested_action": map[string]interface{}{
					"type":          ToolMarkObligationPaid,
					"obligation_id": obligation.ID.String(),
					"name":          obligation.Name,
				},
			})
			if len(matches) >= 10 {
				return paymentMatchResult(matches, days), nil
			}
		}
	}
	return paymentMatchResult(matches, days), nil
}

func paymentMatchResult(matches []map[string]interface{}, days int) map[string]interface{} {
	return map[string]interface{}{
		"matches":       matches,
		"count":         len(matches),
		"lookback_days": days,
		"guidance":      "Ask the user to confirm before marking anything paid. If confidence is medium, phrase it as a question.",
	}
}

func obligationPaymentMatchScore(obligation entities.FinancialObligation, tx entities.SpendingTransaction) (int, []string) {
	score := 0
	reasons := []string{}

	if amountsClose(obligation.Amount, tx.Amount) {
		score += 55
		reasons = append(reasons, "amount_match")
	}

	label := strings.ToLower(strings.TrimSpace(tx.Source + " " + tx.Category))
	for _, token := range obligationTokens(obligation) {
		if token != "" && strings.Contains(label, token) {
			score += 25
			reasons = append(reasons, "name_match")
			break
		}
	}

	switch obligation.Type {
	case entities.ObligationTypeRent, entities.ObligationTypeTax, entities.ObligationTypeSubscription,
		entities.ObligationTypeInsurance, entities.ObligationTypeEducation, entities.ObligationTypeFamilySupport,
		entities.ObligationTypeDebt:
		score += 5
	}

	if score > 100 {
		score = 100
	}
	return score, reasons
}

func obligationTokens(obligation entities.FinancialObligation) []string {
	parts := []string{obligation.Name}
	if obligation.Counterparty != nil {
		parts = append(parts, *obligation.Counterparty)
	}
	tokens := []string{}
	for _, part := range parts {
		for _, token := range strings.Fields(strings.ToLower(part)) {
			token = strings.Trim(token, ".,:;!@#$%^&*()[]{}")
			if len(token) >= 4 {
				tokens = append(tokens, token)
			}
		}
	}
	return tokens
}

func amountsClose(expected, actual decimal.Decimal) bool {
	if expected.IsZero() || actual.IsZero() {
		return false
	}
	diff := expected.Sub(actual).Abs()
	tolerance := expected.Mul(decimal.NewFromFloat(0.02))
	if tolerance.LessThan(decimal.NewFromInt(1)) {
		tolerance = decimal.NewFromInt(1)
	}
	return diff.LessThanOrEqual(tolerance)
}

func confidenceLabel(score int) string {
	switch {
	case score >= 80:
		return "high"
	case score >= 60:
		return "medium"
	default:
		return "low"
	}
}
