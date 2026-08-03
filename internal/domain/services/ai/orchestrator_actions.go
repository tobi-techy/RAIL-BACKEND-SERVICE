package ai

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// Action tool names.
const (
	ToolTransferFunds            = "transfer_funds"
	ToolSetSavingsGoal           = "set_savings_goal"
	ToolCreateObligationReminder = "create_obligation_reminder"
	ToolConfirmAction            = "confirm_action"
	ToolCancelAction             = "cancel_action"
)

const pendingActionTTL = 5 * time.Minute

// FundsTransferer moves money between spend and stash.
//
// Implementations MUST use database-level locking (e.g. SELECT FOR UPDATE) to
// ensure the balance check and debit are atomic. The balance pre-check in
// executeTransfer is defense-in-depth only; the real protection against
// TOCTOU races must live in the FundsTransferer implementation.
type FundsTransferer interface {
	TransferSpendToStash(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) error
	TransferStashToSpend(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) error
	GetSpendBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
	GetStashBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
}

// EmergencyWithdrawer handles stash withdrawals during the lock period (with fee).
type EmergencyWithdrawer interface {
	// IsStashLocked returns true if the user has no open withdrawal window (funds are locked).
	IsStashLocked(ctx context.Context, userID uuid.UUID) (bool, error)
	EmergencyWithdrawalPreview(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) (*entities.EmergencyWithdrawalPreviewResponse, error)
	EmergencyStashToSpending(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) (*entities.EmergencyWithdrawalResult, error)
}

// GoalProtectionProvider checks whether a withdrawal would impact goal-allocated funds.
type GoalProtectionProvider interface {
	GetTotalGoalAllocated(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
	GetGoalAccounts(ctx context.Context, userID uuid.UUID) ([]*entities.LedgerAccount, error)
	GetUnallocatedStashBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
	GetWithdrawableStashBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
}

// UserAccountChecker verifies user account status before executing financial actions.
type UserAccountChecker interface {
	IsActiveAndUnfrozen(ctx context.Context, userID uuid.UUID) (active bool, frozen bool, err error)
}

// ActionAuditor persists action audit entries.
type ActionAuditor interface {
	RecordAction(ctx context.Context, entry *entities.ActionAuditEntry) error
}

// SavingsGoal represents a user's savings goal.
type SavingsGoal struct {
	Name      string `json:"name"`
	Target    string `json:"target"`
	Deadline  string `json:"deadline,omitempty"`
	CreatedAt string `json:"created_at"`
}

// SavingsGoalStore persists savings goals (Redis-backed).
type SavingsGoalStore interface {
	Set(ctx context.Context, userID uuid.UUID, goal *SavingsGoal) error
	Get(ctx context.Context, userID uuid.UUID) (*SavingsGoal, error)
}

// SharedGoalCreator creates shared goals from Miriam conversations.
type SharedGoalCreator interface {
	CreateGoalFromAI(ctx context.Context, userID uuid.UUID, name, targetAmount string, deadline *string) (uuid.UUID, error)
}

// AutomationCreator creates Miriam automation rules after confirmation.
type AutomationCreator interface {
	CreateAutomationFromAI(ctx context.Context, userID uuid.UUID, req AIServiceAutomationRequest) (*entities.MiriamAutomation, error)
}

// ObligationCreator creates manual obligations after confirmation.
type ObligationCreator interface {
	CreateObligationFromAI(ctx context.Context, userID uuid.UUID, req AIServiceObligationRequest) (*entities.FinancialObligation, error)
}

type AIServiceAutomationRequest struct {
	Name              string
	Description       *string
	TriggerType       string
	TriggerConfig     map[string]interface{}
	ActionType        string
	ActionConfig      map[string]interface{}
	MaxTriggersPerDay int
	CooldownMinutes   int
}

type AIServiceObligationRequest struct {
	Type         string
	Name         string
	Amount       decimal.Decimal
	Currency     string
	Cadence      string
	DueDay       *int
	Priority     string
	Counterparty *string
	Status       string
	Metadata     map[string]interface{}
}

// PendingActionStore persists pending actions (Redis-backed for multi-instance safety).
type PendingActionStore interface {
	Set(ctx context.Context, convID uuid.UUID, action *entities.PendingAction) error
	Get(ctx context.Context, convID uuid.UUID) *entities.PendingAction
	Delete(ctx context.Context, convID uuid.UUID)
}

// inMemoryPendingActions is the fallback when no Redis is available.
type inMemoryPendingActions struct {
	mu    sync.Mutex
	store map[string]*entities.PendingAction
}

func newInMemoryPendingActions() PendingActionStore {
	return &inMemoryPendingActions{store: make(map[string]*entities.PendingAction)}
}

func (p *inMemoryPendingActions) Set(_ context.Context, convID uuid.UUID, action *entities.PendingAction) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := convID.String()
	if existing, ok := p.store[key]; ok && !existing.IsExpired() {
		return fmt.Errorf("a pending action already exists for conversation %s", convID)
	}
	p.store[key] = action
	return nil
}

func (p *inMemoryPendingActions) Get(_ context.Context, convID uuid.UUID) *entities.PendingAction {
	p.mu.Lock()
	defer p.mu.Unlock()
	a, ok := p.store[convID.String()]
	if !ok || a.IsExpired() {
		delete(p.store, convID.String())
		return nil
	}
	return a
}

func (p *inMemoryPendingActions) Delete(_ context.Context, convID uuid.UUID) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.store, convID.String())
}

// SetFundsTransferer wires the funds transfer dependency.
func (o *AgentAdapter) SetFundsTransferer(f FundsTransferer) {
	o.fundsTransferer = f
}

// SetPendingActions replaces the default in-memory store with a custom implementation (e.g. Redis).
func (o *AgentAdapter) SetPendingActions(p PendingActionStore) {
	o.pending = p
}

// SetActionAuditor wires the action audit dependency.
// Deprecated: Use NewOrchestratorWithDeps instead.
func (o *AgentAdapter) SetActionAuditor(a ActionAuditor) {
	o.actionAuditor = a
	if reader, ok := a.(ActionHistoryReader); ok {
		o.actionHistory = reader
	}
}

// SetSavingsGoalStore wires the savings goal store dependency.
func (o *AgentAdapter) SetSavingsGoalStore(s SavingsGoalStore) {
	o.savingsGoalStore = s
}

// SetSharedGoalCreator wires the shared goal creator for unified goal persistence.
func (o *AgentAdapter) SetSharedGoalCreator(c SharedGoalCreator) {
	o.sharedGoalCreator = c
}

func (o *AgentAdapter) SetAutomationCreator(a AutomationCreator) {
	o.automationCreator = a
}

func (o *AgentAdapter) SetObligationCreator(c ObligationCreator) {
	o.obligationCreator = c
}

// SetAccountChecker wires the user account checker dependency.
func (o *AgentAdapter) SetAccountChecker(c UserAccountChecker) {
	o.accountChecker = c
}

// SetEmergencyWithdrawer wires the emergency stash withdrawal dependency.
func (o *AgentAdapter) SetEmergencyWithdrawer(e EmergencyWithdrawer) {
	o.emergencyWithdrawer = e
}

// SetGoalProtectionProvider wires the goal protection provider for withdrawal warnings.
func (o *AgentAdapter) SetGoalProtectionProvider(g GoalProtectionProvider) {
	o.goalProtection = g
}

// SetVoiceDailyLimiter wires the voice daily transfer cap.
func (o *AgentAdapter) SetVoiceDailyLimiter(l *VoiceDailyLimiter) {
	o.voiceLimiter = l
}

// SetRedisCache wires a Redis client used for short-TTL caching of voice
// hot-path reads (realtime dynamic vars, cost-ceiling). Best-effort only —
// every cached call falls back to direct computation if Redis is unavailable.
func (o *AgentAdapter) SetRedisCache(redis cache.RedisClient) {
	o.redis = redis
}

// checkUserCanTransact verifies the user is active and not frozen before financial actions.
func (o *AgentAdapter) checkUserCanTransact(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	if o.accountChecker == nil {
		return nil, nil
	}
	active, frozen, err := o.accountChecker.IsActiveAndUnfrozen(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("check account status: %w", err)
	}
	if !active {
		return map[string]interface{}{"error": "Your account is not active. Please contact support."}, nil
	}
	if frozen {
		return map[string]interface{}{"error": "Withdrawals are temporarily frozen on your account. Please contact support."}, nil
	}
	return nil, nil
}

// ActionTools returns the action-capable tools.
func ActionTools() []ai.Tool {
	return []ai.Tool{
		{
			Name:        ToolTransferFunds,
			Description: "Move money between Spend and Stash wallets. Call this when the user says 'move to stash', 'transfer to spend', 'put in stash', 'send to stash', or any variation. When in doubt, call this. A wasted call is fine; saying 'done' without calling is not.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"from":   map[string]interface{}{"type": "string", "enum": []string{"spend", "stash"}, "description": "Source wallet"},
					"to":     map[string]interface{}{"type": "string", "enum": []string{"spend", "stash"}, "description": "Destination wallet"},
					"amount": map[string]interface{}{"type": "number", "description": "Amount in USD to transfer"},
				},
				"required": []string{"from", "to", "amount"},
			},
		},
		{
			Name:        ToolSetSavingsGoal,
			Description: "Set a savings goal. Call this when the user says 'save for', 'I want to save', 'set a goal', 'saving up for', or mentions a target amount for something. After the goal is confirmed and created, ALWAYS suggest creating an automation to fund it (e.g. 'Want me to save $X every week toward this?'). If the response contains suggest_automation=true and goal_id, proactively offer to create a transfer_to_stash automation linked to that goal.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name":     map[string]interface{}{"type": "string", "description": "Goal name, e.g. 'Emergency Fund'"},
					"target":   map[string]interface{}{"type": "number", "description": "Target amount in USD"},
					"deadline": map[string]interface{}{"type": "string", "description": "Optional deadline in YYYY-MM-DD format"},
				},
				"required": []string{"name", "target"},
			},
		},
		{
			Name:        ToolCreateObligationReminder,
			Description: "Create a bill reminder or financial obligation. Call this when the user says 'remind me about rent', 'I have a bill due', 'track my subscription', 'I owe', or mentions any recurring payment. When in doubt, call this.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"type":         map[string]interface{}{"type": "string", "enum": []string{"debt", "invoice", "payroll", "insurance", "education", "rent", "family_support", "tax", "subscription", "vendor_bill", "other"}},
					"name":         map[string]interface{}{"type": "string"},
					"amount":       map[string]interface{}{"type": "number"},
					"currency":     map[string]interface{}{"type": "string"},
					"cadence":      map[string]interface{}{"type": "string", "enum": []string{"one_time", "weekly", "biweekly", "monthly", "quarterly", "annual"}},
					"due_day":      map[string]interface{}{"type": "integer"},
					"priority":     map[string]interface{}{"type": "string", "enum": []string{"critical", "high", "medium", "low"}},
					"counterparty": map[string]interface{}{"type": "string"},
					"metadata":     map[string]interface{}{"type": "object"},
				},
				"required": []string{"type", "name", "amount", "currency", "cadence"},
			},
		},
	}
}

// checkControlLevelGate returns an error result if the user's control level
// prevents action execution. Returns nil if actions are allowed.
func (o *AgentAdapter) checkControlLevelGate(ctx context.Context, userID uuid.UUID) map[string]interface{} {
	if o.memory == nil {
		return nil
	}
	level, err := o.memory.GetControlLevel(ctx, userID)
	if err != nil || level == "" {
		return nil
	}
	if level == entities.ControlLevelMonitor {
		return map[string]interface{}{
			"error": "You're in Monitor mode — I can only watch and advise, not take actions. If you want me to handle this, switch to Guided or Full Autopilot mode using set_control_level.",
		}
	}
	return nil
}

// executeActionTool handles action tool calls by creating a pending action
// instead of executing immediately. Returns a confirmation payload for the frontend.
func (o *AgentAdapter) executeActionTool(ctx context.Context, userID, convID uuid.UUID, tc ai.ToolCall) (map[string]interface{}, error) {
	if blocked := o.checkControlLevelGate(ctx, userID); blocked != nil {
		return blocked, nil
	}

	switch tc.Name {
	case ToolTransferFunds:
		return o.createTransferAction(ctx, userID, convID, tc.Arguments)
	case ToolSetSavingsGoal:
		return o.createSavingsGoalAction(ctx, userID, convID, tc.Arguments)
	case ToolSendReport:
		return o.executeSendReport(ctx, userID, convID, tc.Arguments)
	case ToolSetBudget:
		return o.createSetBudgetAction(ctx, userID, convID, tc.Arguments)
	case ToolCreateAutomation:
		return o.createAutomationAction(ctx, userID, convID, tc.Arguments)
	case ToolCreateObligationReminder:
		return o.createObligationReminderAction(ctx, userID, convID, tc.Arguments)
	case ToolInitiateWithdrawal:
		return o.createWithdrawalAction(ctx, userID, convID, tc.Arguments)
	case ToolMarkObligationPaid:
		return o.createMarkObligationPaidAction(ctx, userID, convID, tc.Arguments)
	case ToolProtectSubscription:
		return o.createProtectSubscriptionAction(ctx, userID, convID, tc.Arguments)
	case ToolMarkSubscriptionCancelled:
		return o.createMarkSubscriptionCancelledAction(ctx, userID, convID, tc.Arguments)
	case ToolIgnoreSubscription:
		return o.createIgnoreSubscriptionAction(ctx, userID, convID, tc.Arguments)
	case ToolSplitReceipt:
		return o.createSplitReceiptAction(ctx, userID, convID, tc.Arguments)
	case ToolUpdateFinancialProfile:
		return o.createFinancialProfileAction(ctx, userID, convID, tc.Arguments)
	default:
		if isExecutionActionTool(tc.Name) {
			return o.createExecutionAction(ctx, userID, convID, tc)
		}
		return nil, fmt.Errorf("unknown action tool: %s", tc.Name)
	}
}

func (o *AgentAdapter) createTransferAction(ctx context.Context, userID, convID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	// R11-1: Check user account status before allowing transfer creation
	if blocked, err := o.checkUserCanTransact(ctx, userID); blocked != nil || err != nil {
		if err != nil {
			return nil, err
		}
		return blocked, nil
	}

	from, _ := args["from"].(string)
	to, _ := args["to"].(string)
	amountF, _ := args["amount"].(float64)

	if from == "" || to == "" || amountF <= 0 {
		return map[string]interface{}{"error": "Invalid transfer parameters"}, nil
	}
	if from == to {
		return map[string]interface{}{"error": "Source and destination must be different"}, nil
	}
	if from != "spend" && from != "stash" {
		return map[string]interface{}{"error": "Source must be 'spend' or 'stash'"}, nil
	}
	if to != "spend" && to != "stash" {
		return map[string]interface{}{"error": "Destination must be 'spend' or 'stash'"}, nil
	}
	if amountF > 500 {
		return map[string]interface{}{"error": "For safety, single transfers are capped at $500. Want me to move $500 now and you can do the rest from the app? Or I can split it into multiple moves."}, nil
	}
	// Transfers up to the $500 cap stage a confirm card; the confirm endpoint
	// enforces the passcode/biometric step-up for all fund-moving actions
	// (see ConfirmAction + IsFundMovingAction), so the card itself is the
	// verification — no separate >$100 refusal needed.

	amount := decimal.NewFromFloat(amountF)

	// Check balance
	var balance decimal.Decimal
	var err error
	if from == "spend" {
		balance, err = o.fundsTransferer.GetSpendBalance(ctx, userID)
	} else {
		balance, err = o.fundsTransferer.GetStashBalance(ctx, userID)
	}
	if err != nil {
		return nil, fmt.Errorf("check balance: %w", err)
	}
	if balance.LessThan(amount) {
		return map[string]interface{}{
			"error":             "Insufficient balance",
			"available_balance": balance.StringFixed(2),
			"requested_amount":  amount.StringFixed(2),
		}, nil
	}

	var destinationBalance decimal.Decimal
	if to == "spend" {
		destinationBalance, err = o.fundsTransferer.GetSpendBalance(ctx, userID)
	} else {
		destinationBalance, err = o.fundsTransferer.GetStashBalance(ctx, userID)
	}
	if err != nil {
		return nil, fmt.Errorf("check destination balance: %w", err)
	}

	// When moving from stash, first check protected goals, then stash lock.
	var emergencyPreview *entities.EmergencyWithdrawalPreviewResponse
	if from == "stash" {
		// Hard block: protected goals cannot be raided
		if o.goalProtection != nil {
			withdrawable, gpErr := o.goalProtection.GetWithdrawableStashBalance(ctx, userID)
			if gpErr != nil {
				return nil, fmt.Errorf("check goal protection: %w", gpErr)
			}
			if amount.GreaterThan(withdrawable) {
				return map[string]interface{}{
					"error":            "Protected goals block this transfer",
					"withdrawable":     withdrawable.StringFixed(2),
					"requested_amount": amount.StringFixed(2),
					"message":          fmt.Sprintf("You can only move $%s from stash — the rest is locked in protected goals. Unprotect a goal first, or transfer a smaller amount.", withdrawable.StringFixed(2)),
				}, nil
			}
		}

		// Emergency withdrawal preview if stash is locked
		if o.emergencyWithdrawer != nil {
			locked, lockErr := o.emergencyWithdrawer.IsStashLocked(ctx, userID)
			if lockErr != nil {
				return nil, fmt.Errorf("check stash lock: %w", lockErr)
			}
			if locked {
				preview, previewErr := o.emergencyWithdrawer.EmergencyWithdrawalPreview(ctx, userID, amount)
				if previewErr != nil {
					return nil, fmt.Errorf("emergency withdrawal preview: %w", previewErr)
				}
				emergencyPreview = preview
				if balance.LessThan(amount.Add(preview.FeeAmount)) {
					return map[string]interface{}{
						"error":             "Insufficient stash balance after early withdrawal fee",
						"available_balance": balance.StringFixed(2),
						"requested_amount":  amount.StringFixed(2),
						"fee_amount":        preview.FeeAmount.StringFixed(2),
						"total_needed":      amount.Add(preview.FeeAmount).StringFixed(2),
					}, nil
				}
			}
		}
	}

	impact := map[string]interface{}{
		"source":                     from,
		"destination":                to,
		"amount":                     amount.StringFixed(2),
		"source_balance_before":      balance.StringFixed(2),
		"source_balance_after":       balance.Sub(amount).StringFixed(2),
		"destination_balance_before": destinationBalance.StringFixed(2),
		"destination_balance_after":  destinationBalance.Add(amount).StringFixed(2),
	}

	// Soft warning: unprotected goals would be impacted
	if from == "stash" && o.goalProtection != nil {
		unallocated, err := o.goalProtection.GetUnallocatedStashBalance(ctx, userID)
		if err == nil && amount.GreaterThan(unallocated) {
			goalImpact := amount.Sub(unallocated)
			impact["goal_warning"] = true
			impact["unallocated_stash"] = unallocated.StringFixed(2)
			impact["goal_impact_amount"] = goalImpact.StringFixed(2)
			impact["goal_warning_message"] = fmt.Sprintf(
				"⚠️ Only $%s of your stash is unallocated. This transfer will pull $%s from your savings goals.",
				unallocated.StringFixed(2), goalImpact.StringFixed(2),
			)
		}
	}

	if emergencyPreview != nil {
		impact["emergency_withdrawal"] = true
		impact["fee_percent"] = emergencyPreview.FeePercent.StringFixed(2)
		impact["fee_amount"] = emergencyPreview.FeeAmount.StringFixed(2)
		impact["fee_tier"] = emergencyPreview.FeeTier
		impact["lock_age_days"] = emergencyPreview.LockAgeDays
		impact["net_amount"] = emergencyPreview.NetAmount.StringFixed(2)
		impact["source_balance_after"] = balance.Sub(amount).Sub(emergencyPreview.FeeAmount).StringFixed(2)
	}

	description := fmt.Sprintf("Move $%s from %s to %s", amount.StringFixed(2), from, to)
	if emergencyPreview != nil {
		description = fmt.Sprintf("Early withdrawal: move $%s from stash to spend (fee: $%s)", amount.StringFixed(2), emergencyPreview.FeeAmount.StringFixed(2))
	}

	action := &entities.PendingAction{
		ID:             uuid.New().String(),
		ConversationID: convID,
		UserID:         userID,
		Action:         ToolTransferFunds,
		Description:    description,
		Params:         map[string]interface{}{"from": from, "to": to, "amount": amount.StringFixed(2), "impact": impact},
		ExpiresAt:      time.Now().Add(pendingActionTTL),
		CreatedAt:      time.Now(),
	}

	if err := o.pending.Set(ctx, convID, action); err != nil {
		return nil, fmt.Errorf("store pending transfer action: %w", err)
	}

	return map[string]interface{}{
		"action_required": true,
		"pending_action":  action,
		"impact":          impact,
	}, nil
}

func (o *AgentAdapter) createSavingsGoalAction(ctx context.Context, userID, convID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	name, _ := args["name"].(string)
	targetF, _ := args["target"].(float64)
	deadline, _ := args["deadline"].(string)

	if name == "" || targetF <= 0 {
		return map[string]interface{}{"error": "Invalid goal parameters"}, nil
	}

	target := decimal.NewFromFloat(targetF)
	params := map[string]interface{}{"name": name, "target": target.StringFixed(2)}
	desc := fmt.Sprintf("Set savings goal '%s' for $%s", name, target.StringFixed(2))
	if deadline != "" {
		if _, err := time.Parse("2006-01-02", deadline); err != nil {
			return map[string]interface{}{"error": "Invalid deadline format, use YYYY-MM-DD"}, nil
		}
		params["deadline"] = deadline
		desc += fmt.Sprintf(" by %s", deadline)
	}

	action := &entities.PendingAction{
		ID:             uuid.New().String(),
		ConversationID: convID,
		UserID:         userID,
		Action:         ToolSetSavingsGoal,
		Description:    desc,
		Params:         params,
		ExpiresAt:      time.Now().Add(pendingActionTTL),
		CreatedAt:      time.Now(),
	}

	if err := o.pending.Set(ctx, convID, action); err != nil {
		return nil, fmt.Errorf("store pending savings goal action: %w", err)
	}

	return map[string]interface{}{
		"action_required": true,
		"pending_action":  action,
	}, nil
}

// PeekPendingAction returns the pending action for a conversation without
// executing it, after verifying it belongs to userID. The API layer uses this
// to decide whether passcode step-up is required before ConfirmAction (TM-004,
// TM-006) without granting the model a way to self-confirm.
func (o *AgentAdapter) PeekPendingAction(ctx context.Context, userID, convID uuid.UUID) (*entities.PendingAction, bool) {
	action := o.pending.Get(ctx, convID)
	if action == nil || action.UserID != userID {
		return nil, false
	}
	return action, true
}

// participantsFromParams normalizes staged split participants from either a
// comma-separated string (core/messaging tools) or a JSON array (stream path).
func participantsFromParams(raw interface{}) []string {
	var out []string
	switch v := raw.(type) {
	case string:
		for _, p := range strings.Split(v, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
	case []interface{}:
		for _, p := range v {
			if s, ok := p.(string); ok {
				s = strings.TrimSpace(s)
				if s != "" {
					out = append(out, s)
				}
			}
		}
	case []string:
		for _, s := range v {
			s = strings.TrimSpace(s)
			if s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// IsFundMovingAction reports whether a staged action type moves money and
// therefore warrants the same passcode step-up that the direct withdrawal and
// stash-transfer routes enforce.
func IsFundMovingAction(action string) bool {
	switch action {
	case ToolTransferFunds, ToolInitiateWithdrawal,
		// Execution Engine tools that move real money get the same step-up.
		// setup_bill_autopay creates a standing stash→spend transfer, which the
		// app's own automation endpoints also gate behind passcode consent.
		ToolExecuteInvestment, ToolOptimizeYield, ToolCopyTrader, ToolSetupBillAutopay,
		// Airbills bill payments debit the user's Spend balance in USDC.
		ToolPayBill, ToolAutomateBill,
		// P2P send / receipt splits debit or reserve Spend (claim links for non-users).
		"send_money", ToolSplitReceipt,
		// Automations can schedule recurring transfers — require Face ID step-up.
		ToolCreateAutomation,
		// BRIJ flight bookings hold the user's Spend balance for escrow + fee.
		ToolBookFlight:
		return true
	default:
		return false
	}
}

// ConfirmAction executes a pending action after user confirmation.
func (o *AgentAdapter) ConfirmAction(ctx context.Context, userID, convID uuid.UUID) (*entities.PendingAction, error) {
	action := o.pending.Get(ctx, convID)
	if action == nil {
		// Check if it was expired (Get auto-deletes expired)
		return nil, fmt.Errorf("action_expired: That action timed out. Just ask me again and I'll set it up fresh — it only takes a sec.")
	}
	if action.UserID != userID {
		return nil, fmt.Errorf("action does not belong to user")
	}

	var execErr error
	switch action.Action {
	case ToolTransferFunds:
		execErr = o.executeTransfer(ctx, userID, action)
	case ToolSetSavingsGoal:
		// Prefer SharedGoal creation (persistent, ledger-backed sub-account)
		if o.sharedGoalCreator != nil {
			name := fmt.Sprintf("%v", action.Params["name"])
			target := fmt.Sprintf("%v", action.Params["target"])
			var deadline *string
			if d, ok := action.Params["deadline"].(string); ok && d != "" {
				deadline = &d
			}
			goalID, err := o.sharedGoalCreator.CreateGoalFromAI(ctx, userID, name, target, deadline)
			if err == nil {
				action.Params["goal_id"] = goalID.String()
				action.Params["suggest_automation"] = true
			}
			execErr = err
		} else if o.savingsGoalStore != nil {
			// Fallback to Redis (legacy)
			goal := &SavingsGoal{
				Name:      fmt.Sprintf("%v", action.Params["name"]),
				Target:    fmt.Sprintf("%v", action.Params["target"]),
				CreatedAt: time.Now().UTC().Format(time.RFC3339),
			}
			if d, ok := action.Params["deadline"].(string); ok {
				goal.Deadline = d
			}
			execErr = o.savingsGoalStore.Set(ctx, userID, goal)
		}
		o.logger.Info("Savings goal set via Miriam",
			zap.String("user_id", userID.String()),
			zap.Any("params", action.Params))
	case ToolSendReport:
		execErr = o.executeSendReportAction(ctx, userID, action)
	case ToolSetBudget:
		_, execErr = o.executeSetBudget(ctx, userID, action.Params)
	case ToolCreateAutomation:
		// Prefer core registry: understands both full {trigger_type,action_type,...}
		// and simple messaging-tool {name,type,schedule,amount,from,to} shapes.
		// Falls back to the stream-only executor when no agent is wired.
		if o.agent != nil {
			execErr = o.executeConfirmedExecutionAction(ctx, userID, action)
		} else {
			_, execErr = o.executeCreateAutomation(ctx, userID, action.Params)
		}
	case ToolCreateObligationReminder:
		_, execErr = o.executeCreateObligationReminder(ctx, userID, action.Params)
	case ToolInitiateWithdrawal:
		execErr = o.executeWithdrawal(ctx, userID, action)
	case ToolMarkObligationPaid:
		if o.obligationManager == nil {
			execErr = fmt.Errorf("obligation service unavailable")
		} else {
			_, execErr = o.executeMarkObligationPaid(ctx, userID, action.Params)
		}
	case ToolProtectSubscription:
		if o.obligationCreator == nil {
			execErr = fmt.Errorf("subscription service unavailable")
		} else {
			_, execErr = o.executeProtectSubscription(ctx, userID, action.Params)
		}
	case ToolMarkSubscriptionCancelled:
		if o.obligationCreator == nil {
			execErr = fmt.Errorf("subscription service unavailable")
		} else {
			_, execErr = o.executeMarkSubscriptionCancelled(ctx, userID, action.Params)
		}
	case ToolIgnoreSubscription:
		o.logger.Info("Subscription ignored via Miriam",
			zap.String("user_id", userID.String()),
			zap.Any("params", action.Params))
	case ToolSplitReceipt, "send_money":
		// Prefer the core registry (real P2P) so messaging + app share one path.
		// Falls back to stream-only receiptSplitter for split when registry is unavailable.
		if o.agent != nil {
			execErr = o.executeConfirmedExecutionAction(ctx, userID, action)
		} else if action.Action == ToolSplitReceipt && o.receiptSplitter != nil {
			receiptIDStr, ok := action.Params["receipt_id"].(string)
			if !ok || receiptIDStr == "" {
				execErr = fmt.Errorf("missing or invalid receipt_id in action params")
			} else if receiptID, parseErr := uuid.Parse(receiptIDStr); parseErr != nil {
				execErr = fmt.Errorf("invalid receipt_id format: %w", parseErr)
			} else {
				participants := participantsFromParams(action.Params["participants"])
				if len(participants) == 0 {
					execErr = fmt.Errorf("invalid or missing participants in action params")
				} else {
					message, _ := action.Params["message"].(string)
					_, execErr = o.receiptSplitter.ExecuteSplit(ctx, userID, receiptID, participants, message)
				}
			}
		} else {
			execErr = fmt.Errorf("%s service unavailable", action.Action)
		}
	case ToolUpdateFinancialProfile:
		if o.financialProfile == nil {
			execErr = fmt.Errorf("financial profile service is unavailable")
		} else {
			_, execErr = o.executeUpdateFinancialProfile(ctx, userID, action.Params)
		}
	default:
		if isExecutionActionTool(action.Action) {
			execErr = o.executeConfirmedExecutionAction(ctx, userID, action)
		} else {
			execErr = fmt.Errorf("unknown action: %s", action.Action)
		}
	}

	o.pending.Delete(ctx, convID)

	status := "executed"
	errMsg := ""
	if execErr != nil {
		status = "failed"
		errMsg = execErr.Error()
	}
	o.auditAction(ctx, userID, convID, action, status, errMsg)

	// initiate_withdrawal triggers the standard withdrawal notification pipeline
	// (NotifyWithdrawalSubmitted/Completed/Failed) and is intentionally excluded
	// here — emitting another push would duplicate the user-facing message.
	if o.moneyMoveNotifier != nil && action.Action == ToolTransferFunds {
		go o.notifyMoneyMoved(userID, action, execErr == nil, errMsg)
	}

	if execErr != nil {
		return nil, execErr
	}
	return action, nil
}

// auditVoiceTransfer records a voice-direct transfer in the action audit log
// so it surfaces in the user's transaction feed. The voice path bypasses the
// pending/confirm flow, so without this call the action would otherwise be
// invisible outside the live conversation.
//
// Uses a background context with a short timeout because the request ctx may
// already be cancelled by the time we get here (Gin may have flushed, the
// voice client may have hung up); we still want the audit row to land — the
// money already moved at the ledger.
func (o *AgentAdapter) auditVoiceTransfer(userID uuid.UUID, action, from, to string, amount decimal.Decimal, succeeded bool, errMsg string) {
	if o.actionAuditor == nil {
		return
	}
	status := "executed"
	if !succeeded {
		status = "failed"
	}
	entry := &entities.ActionAuditEntry{
		ID:             uuid.New(),
		UserID:         userID,
		ConversationID: uuid.Nil,
		Action:         action,
		Params: map[string]interface{}{
			"from":   from,
			"to":     to,
			"amount": amount.StringFixed(2),
			"source": "voice_direct",
		},
		Status:       status,
		ErrorMessage: errMsg,
		CreatedAt:    time.Now(),
	}
	bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := o.actionAuditor.RecordAction(bgCtx, entry); err != nil {
		o.logger.Warn("voice direct transfer audit failed", zap.Error(err))
	}
}

// notifyMoneyMoved fires a push notification on a confirmed Miriam money move.
// Runs in a goroutine because the caller is the API request path and we don't
// want notification latency on the response.
func (o *AgentAdapter) notifyMoneyMoved(userID uuid.UUID, action *entities.PendingAction, succeeded bool, errMsg string) {
	if o.moneyMoveNotifier == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	from, _ := action.Params["from"].(string)
	to, _ := action.Params["to"].(string)
	amount := decimal.Zero
	if a, ok := action.Params["amount"].(string); ok {
		if d, err := decimal.NewFromString(a); err == nil {
			amount = d
		}
	}
	emergency := false
	if impact, ok := action.Params["impact"].(map[string]interface{}); ok {
		if _, isEmergency := impact["emergency_withdrawal"]; isEmergency {
			emergency = true
		}
	}

	if err := o.moneyMoveNotifier.NotifyMiriamMovedFunds(ctx, userID, action.Action, from, to, amount, emergency, succeeded, errMsg); err != nil {
		o.logger.Warn("miriam money-move notification failed",
			zap.String("user_id", userID.String()),
			zap.String("action", action.Action),
			zap.Error(err))
	}
}

// CancelAction discards a pending action.
func (o *AgentAdapter) CancelAction(ctx context.Context, userID, convID uuid.UUID) error {
	action := o.pending.Get(ctx, convID)
	if action == nil {
		return nil // Already gone or expired — safe to ignore
	}
	if action.UserID != userID {
		return fmt.Errorf("action does not belong to user")
	}
	o.pending.Delete(ctx, convID)
	o.auditAction(ctx, userID, convID, action, "cancelled", "")
	return nil
}

func (o *AgentAdapter) executeTransfer(ctx context.Context, userID uuid.UUID, action *entities.PendingAction) error {
	// R11-1: Re-check account status at execution time
	if o.accountChecker != nil {
		active, frozen, err := o.accountChecker.IsActiveAndUnfrozen(ctx, userID)
		if err != nil {
			return fmt.Errorf("check account status: %w", err)
		}
		if !active {
			return fmt.Errorf("account is not active")
		}
		if frozen {
			return fmt.Errorf("withdrawals are frozen on this account")
		}
	}

	from, _ := action.Params["from"].(string)
	amountStr, _ := action.Params["amount"].(string)
	amount, err := decimal.NewFromString(amountStr)
	if err != nil {
		return fmt.Errorf("invalid amount: %w", err)
	}

	// Re-check balance at execution time — defense-in-depth only.
	// The real TOCTOU protection MUST be in the FundsTransferer implementation
	// via database-level locking (SELECT FOR UPDATE or similar).
	var balance decimal.Decimal
	if from == "spend" {
		balance, err = o.fundsTransferer.GetSpendBalance(ctx, userID)
	} else {
		balance, err = o.fundsTransferer.GetStashBalance(ctx, userID)
	}
	if err != nil {
		return fmt.Errorf("check balance: %w", err)
	}
	if balance.LessThan(amount) {
		return fmt.Errorf("insufficient balance: available $%s, requested $%s", balance.StringFixed(2), amount.StringFixed(2))
	}

	// Use action ID as idempotency key to prevent double-execution
	if from == "spend" {
		return o.fundsTransferer.TransferSpendToStash(ctx, userID, amount, action.ID)
	}

	// If the action was flagged as emergency at creation time, go straight to emergency path.
	if o.emergencyWithdrawer != nil {
		if impact, ok := action.Params["impact"].(map[string]interface{}); ok {
			if _, isEmergency := impact["emergency_withdrawal"]; isEmergency {
				_, execErr := o.emergencyWithdrawer.EmergencyStashToSpending(ctx, userID, amount, action.ID)
				return execErr
			}
		}
	}

	return o.fundsTransferer.TransferStashToSpend(ctx, userID, amount, action.ID)
}

func (o *AgentAdapter) auditAction(ctx context.Context, userID, convID uuid.UUID, action *entities.PendingAction, status, errMsg string) {
	if o.actionAuditor == nil {
		return
	}
	entry := &entities.ActionAuditEntry{
		ID:             uuid.New(),
		UserID:         userID,
		ConversationID: convID,
		Action:         action.Action,
		Params:         action.Params,
		Status:         status,
		ErrorMessage:   errMsg,
		CreatedAt:      time.Now(),
	}
	if err := o.actionAuditor.RecordAction(ctx, entry); err != nil {
		o.logger.Error("failed to audit action", zap.Error(err))
	}
}

// executeActionToolDirect executes action tools immediately without the pending/confirm flow.
// Used in voice mode where AssemblyAI handles confirmation conversationally.
func (o *AgentAdapter) executeActionToolDirect(ctx context.Context, userID uuid.UUID, tc ai.ToolCall) (map[string]interface{}, error) {
	if tc.Arguments == nil {
		tc.Arguments = make(map[string]interface{})
	}
	if blocked := o.checkControlLevelGate(ctx, userID); blocked != nil {
		return blocked, nil
	}
	o.logger.Info("executeActionToolDirect called",
		zap.String("user_id", userID.String()),
		zap.String("tool", tc.Name),
		zap.Strings("arg_keys", toolArgumentKeys(tc.Arguments)))
	// Execution Engine tools live in the core registry; their confirm-argument
	// contract handles conversational confirmation in voice mode.
	if isExecutionActionTool(tc.Name) {
		if o.agent == nil {
			return map[string]interface{}{"error": "This action isn't available right now."}, nil
		}
		data, err := o.agent.ExecuteToolStrict(ctx, userID, tc.Name, tc.Arguments)
		if err != nil {
			return map[string]interface{}{"error": err.Error()}, nil
		}
		return data, nil
	}
	switch tc.Name {
	case ToolTransferFunds:
		if o.fundsTransferer == nil {
			o.logger.Error("fundsTransferer is nil")
			return map[string]interface{}{"error": "Transfer service unavailable"}, nil
		}
		if blocked, err := o.checkUserCanTransact(ctx, userID); blocked != nil || err != nil {
			if err != nil {
				return map[string]interface{}{"error": "Unable to verify account status"}, nil
			}
			return blocked, nil
		}
		from, _ := tc.Arguments["from"].(string)
		to, _ := tc.Arguments["to"].(string)
		amountF, _ := tc.Arguments["amount"].(float64)
		if from == "" || to == "" || amountF <= 0 {
			return map[string]interface{}{"error": "Invalid transfer parameters"}, nil
		}
		if amountF > 500 {
			return map[string]interface{}{"error": "Single transfers are capped at $500 for safety. Ask me to transfer $500 or less."}, nil
		}
		amount := decimal.NewFromFloat(amountF)
		key := uuid.New().String()
		var err error
		if from == "spend" && to == "stash" {
			err = o.fundsTransferer.TransferSpendToStash(ctx, userID, amount, key)
		} else if from == "stash" && to == "spend" {
			err = o.fundsTransferer.TransferStashToSpend(ctx, userID, amount, key)
		} else {
			return map[string]interface{}{"error": "Invalid from/to combination"}, nil
		}
		errMsg := ""
		if err != nil {
			errMsg = err.Error()
		}
		// Persist to the audit log so the action shows up in the user's
		// transaction feed alongside the confirm-flow transfers.
		o.auditVoiceTransfer(userID, ToolTransferFunds, from, to, amount, err == nil, errMsg)
		if o.moneyMoveNotifier != nil {
			// Voice-direct transfers don't go through the stash-lock window
			// (the emergency path is confirm-only), so emergency is always false here.
			go func(succeeded bool, em string) {
				nctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if err := o.moneyMoveNotifier.NotifyMiriamMovedFunds(nctx, userID, ToolTransferFunds, from, to, amount, false, succeeded, em); err != nil {
					o.logger.Warn("voice direct transfer notification failed",
						zap.String("user_id", userID.String()),
						zap.String("from", from),
						zap.String("to", to),
						zap.String("amount", amount.String()),
						zap.Error(err))
				}
			}(err == nil, errMsg)
		}
		if err != nil {
			o.logger.Error("voice direct transfer failed", zap.Error(err), zap.String("user_id", userID.String()))
			return map[string]interface{}{"error": "Transfer failed. Please try again or use the app."}, nil
		}
		spend, _ := o.fundsTransferer.GetSpendBalance(ctx, userID)
		stash, _ := o.fundsTransferer.GetStashBalance(ctx, userID)
		return map[string]interface{}{
			"success":     true,
			"transferred": amount.StringFixed(2),
			"from":        from,
			"to":          to,
			"new_spend":   spend.StringFixed(2),
			"new_stash":   stash.StringFixed(2),
		}, nil

	case ToolInitiateWithdrawal:
		return map[string]interface{}{
			"error": "Withdrawals from voice need app confirmation with a verified bank destination. Open Withdraw to continue.",
		}, nil

	case ToolSetSavingsGoal:
		name, _ := tc.Arguments["name"].(string)
		targetF, _ := tc.Arguments["target"].(float64)
		deadline, _ := tc.Arguments["deadline"].(string)
		if name == "" || targetF <= 0 {
			return map[string]interface{}{"error": "Need a goal name and target amount"}, nil
		}
		if o.savingsGoalStore != nil {
			goal := &SavingsGoal{
				Name:      name,
				Target:    decimal.NewFromFloat(targetF).StringFixed(2),
				CreatedAt: time.Now().UTC().Format(time.RFC3339),
			}
			if deadline != "" {
				goal.Deadline = deadline
			}
			if err := o.savingsGoalStore.Set(ctx, userID, goal); err != nil {
				return map[string]interface{}{"error": err.Error()}, nil
			}
		}
		return map[string]interface{}{
			"success": true,
			"message": fmt.Sprintf("Savings goal '%s' set for $%.0f", name, targetF),
		}, nil

	case ToolCreateAutomation:
		if o.automationProvider == nil {
			return map[string]interface{}{"error": "Automation service unavailable"}, nil
		}
		result, err := o.executeCreateAutomation(ctx, userID, tc.Arguments)
		if err != nil {
			return map[string]interface{}{"error": err.Error()}, nil
		}
		return result, nil

	case ToolCreateObligationReminder:
		if o.obligationCreator == nil {
			return map[string]interface{}{"error": "Obligation service unavailable"}, nil
		}
		ob, err := o.executeCreateObligationReminder(ctx, userID, tc.Arguments)
		if err != nil {
			return map[string]interface{}{"error": err.Error()}, nil
		}
		return map[string]interface{}{
			"success": true,
			"message": fmt.Sprintf("Obligation '%s' created", ob.Name),
		}, nil

	case ToolMarkObligationPaid:
		if o.obligationManager == nil {
			return map[string]interface{}{"error": "Obligation service unavailable"}, nil
		}
		ob, err := o.executeMarkObligationPaid(ctx, userID, tc.Arguments)
		if err != nil {
			return map[string]interface{}{"error": err.Error()}, nil
		}
		return map[string]interface{}{
			"success": true,
			"message": fmt.Sprintf("'%s' marked as paid", ob.Name),
		}, nil

	case ToolSetBudget:
		if o.budgetProvider == nil {
			return map[string]interface{}{"error": "Budget service unavailable"}, nil
		}
		result, err := o.executeSetBudget(ctx, userID, tc.Arguments)
		if err != nil {
			return map[string]interface{}{"error": err.Error()}, nil
		}
		return result, nil

	case ToolProtectSubscription:
		if o.obligationCreator == nil {
			return map[string]interface{}{"error": "Subscription service unavailable"}, nil
		}
		ob, err := o.executeProtectSubscription(ctx, userID, tc.Arguments)
		if err != nil {
			return map[string]interface{}{"error": err.Error()}, nil
		}
		return map[string]interface{}{
			"success": true,
			"message": fmt.Sprintf("Subscription '%s' protected", ob.Name),
		}, nil

	case ToolMarkSubscriptionCancelled:
		if o.obligationManager == nil && o.obligationCreator == nil {
			return map[string]interface{}{"error": "Subscription service unavailable"}, nil
		}
		ob, err := o.executeMarkSubscriptionCancelled(ctx, userID, tc.Arguments)
		if err != nil {
			return map[string]interface{}{"error": err.Error()}, nil
		}
		return map[string]interface{}{
			"success": true,
			"message": fmt.Sprintf("Subscription '%s' marked as cancelled", ob.Name),
		}, nil

	case ToolUpdateFinancialProfile:
		result, err := o.executeUpdateFinancialProfile(ctx, userID, tc.Arguments)
		if err != nil {
			return map[string]interface{}{"error": err.Error()}, nil
		}
		return result, nil

	case ToolSendReport:
		return map[string]interface{}{"message": "Report will be sent to your email."}, nil

	case ToolSplitReceipt:
		return map[string]interface{}{"message": "Receipt splitting needs to be done in the app with the receipt image."}, nil

	case ToolIgnoreSubscription:
		return map[string]interface{}{"success": true, "message": "Subscription ignored"}, nil

	default:
		return map[string]interface{}{"message": "This action needs to be completed in the app."}, nil
	}
}

func toolArgumentKeys(args map[string]interface{}) []string {
	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// isActionTool returns true if the tool name is an action tool.
func isActionTool(name string) bool {
	return name == ToolTransferFunds || name == ToolSetSavingsGoal || name == ToolSendReport || name == ToolSetBudget || name == ToolCreateAutomation || name == ToolCreateObligationReminder || name == ToolMarkObligationPaid || name == ToolProtectSubscription || name == ToolMarkSubscriptionCancelled || name == ToolIgnoreSubscription || name == ToolSplitReceipt || name == ToolUpdateFinancialProfile || name == ToolInitiateWithdrawal || isExecutionActionTool(name)
}

func (o *AgentAdapter) canCreateActionTool(name string) bool {
	switch name {
	case ToolTransferFunds, ToolSetSavingsGoal:
		return o.fundsTransferer != nil
	case ToolSendReport:
		return o.reportEmail != nil
	case ToolSetBudget:
		return o.budgetProvider != nil
	case ToolCreateAutomation:
		return o.automationProvider != nil
	case ToolCreateObligationReminder:
		return o.obligationCreator != nil
	case ToolMarkObligationPaid:
		return o.obligationManager != nil
	case ToolProtectSubscription:
		return o.obligationCreator != nil
	case ToolMarkSubscriptionCancelled:
		return o.obligationManager != nil || o.obligationCreator != nil
	case ToolIgnoreSubscription:
		return true
	case ToolSplitReceipt:
		return o.receiptHistory != nil
	case ToolInitiateWithdrawal:
		return o.withdrawalInitiator != nil
	case ToolUpdateFinancialProfile:
		return o.financialProfile != nil
	default:
		// Execution Engine tools run through the core registry on confirm.
		if isExecutionActionTool(name) {
			return o.agent != nil
		}
		return false
	}
}
