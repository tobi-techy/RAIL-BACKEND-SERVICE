package ai

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
)

// Execution Engine (spec 5.2) tool names. The tools themselves are implemented
// once in the core registry (services/ai/tools/execution.go) and served to both
// chat paths; this file routes the mutating ones through the streaming path's
// pending-action confirm flow so the app renders its confirmation card.
const (
	ToolGetUpcomingBills     = "get_upcoming_bills"
	ToolSetupBillAutopay     = "setup_bill_autopay"
	ToolAuditSubscriptions   = "audit_subscriptions"
	ToolCancelSubscription   = "cancel_subscription"
	ToolGetInvestmentOptions = "get_investment_options"
	ToolExecuteInvestment    = "execute_investment"
	ToolGetYieldStatus       = "get_yield_status"
	ToolOptimizeYield        = "optimize_yield"
	ToolBlockMerchant        = "block_merchant"
	ToolUnblockMerchant      = "unblock_merchant"
	ToolListBlockedMerchants = "list_blocked_merchants"
	ToolListTradeConductors  = "list_trade_conductors"
	ToolResearchTrader       = "research_trader"
	ToolGetCopyTradingStatus = "get_copy_trading_status"
	ToolCopyTrader           = "copy_trader"
	ToolPauseTradeCopying    = "pause_trade_copying"
	ToolResumeTradeCopying   = "resume_trade_copying"
	ToolStopTradeCopying     = "stop_trade_copying"

	// Nigerian bill payments (Airbills).
	ToolPayBill             = "pay_bill"
	ToolAutomateBill        = "automate_bill"
	ToolSaveBillBeneficiary = "save_bill_beneficiary"
)

// isExecutionActionTool reports whether the tool is a mutating Execution
// Engine tool that must go through the pending-action confirm flow in chat.
func isExecutionActionTool(name string) bool {
	switch name {
	case ToolSetupBillAutopay, ToolCancelSubscription, ToolExecuteInvestment,
		ToolOptimizeYield, ToolBlockMerchant, ToolUnblockMerchant,
		ToolCopyTrader, ToolPauseTradeCopying, ToolResumeTradeCopying,
		ToolStopTradeCopying,
		ToolPayBill, ToolAutomateBill, ToolSaveBillBeneficiary,
		// Mandate acceptance re-runs the core registry tool with confirm=true.
		"accept_mandate_suggestion", "create_miriam_mandate",
		// P2P + receipt split + automation create — execute via registry on confirm.
		"send_money", "split_receipt", "create_automation":
		return true
	default:
		return false
	}
}

// executionActionDescription builds the human-readable summary shown on the
// pending-action confirmation card.
func executionActionDescription(name string, args map[string]interface{}) string {
	arg := func(key string) string {
		if args == nil {
			return ""
		}
		if v, ok := args[key].(string); ok {
			return v
		}
		if f, ok := args[key].(float64); ok {
			return strconv.FormatFloat(f, 'f', -1, 64)
		}
		return ""
	}
	switch name {
	case "accept_mandate_suggestion":
		return "Activate this quiet-money mandate so Miriam can act within its limits"
	case "create_miriam_mandate":
		return "Create a new Miriam autopilot mandate"
	case "send_money":
		return fmt.Sprintf("Send $%s to %s", arg("amount"), arg("identifier"))
	case "split_receipt":
		return fmt.Sprintf("Split receipt with %s", arg("participants"))
	case "create_automation":
		return fmt.Sprintf("Create automation: %s", arg("name"))
	case ToolSetupBillAutopay:
		if payee := arg("payee"); payee != "" {
			label := arg("payee_name")
			if label == "" {
				label = payee
			}
			return fmt.Sprintf("Set up bill pay: send the bill amount to %s on each due date, with a payment receipt", label)
		}
		return "Set up auto-pay: move the bill amount from Stash to Spend before each due date"
	case ToolCancelSubscription:
		desc := fmt.Sprintf("Cancel subscription %q", arg("name"))
		if block, _ := args["block_merchant"].(bool); block {
			desc += " and block the merchant on your card"
		}
		return desc
	case ToolExecuteInvestment:
		return fmt.Sprintf("Place a %s order of $%s on basket %s", arg("side"), arg("amount"), arg("basket_id"))
	case ToolOptimizeYield:
		return fmt.Sprintf("Move $%s from Spend into your yield-earning Stash", arg("amount"))
	case ToolBlockMerchant:
		return fmt.Sprintf("Block %q on your Rail card — future charges will decline", arg("merchant"))
	case ToolUnblockMerchant:
		return fmt.Sprintf("Unblock %q on your Rail card", arg("merchant"))
	case ToolCopyTrader:
		trader := arg("conductor")
		if trader == "" {
			trader = arg("conductor_id")
		}
		return fmt.Sprintf("Allocate $%s to automatically mirror %s's trades", arg("amount"), trader)
	case ToolPauseTradeCopying:
		return "Pause copy trading (positions stay, no new trades mirror)"
	case ToolResumeTradeCopying:
		return "Resume copy trading"
	case ToolStopTradeCopying:
		return "Stop copy trading and unlink (positions are kept, nothing is sold)"
	case ToolPayBill:
		cat := arg("category")
		recipient := arg("recipient")
		if amt := arg("amount_ngn"); amt != "" {
			return fmt.Sprintf("Pay ₦%s %s to %s", amt, cat, recipient)
		}
		return fmt.Sprintf("Pay %s bill for %s", cat, recipient)
	case ToolAutomateBill:
		return fmt.Sprintf("Automate %s payment of ₦%s to %s (%s)", arg("category"), arg("amount_ngn"), arg("recipient"), arg("schedule"))
	case ToolSaveBillBeneficiary:
		return fmt.Sprintf("Save bill beneficiary %q (%s)", arg("label"), arg("category"))
	default:
		return "Execute " + name
	}
}

// createExecutionAction stages a mutating Execution Engine tool call as a
// pending action so the client can render a confirmation card. On confirm,
// ConfirmAction executes it via the core tool registry.
func (o *AgentAdapter) createExecutionAction(ctx context.Context, userID, convID uuid.UUID, tc ai.ToolCall) (map[string]interface{}, error) {
	// Fund-moving execution tools get the same account-status precheck as
	// transfers and withdrawals.
	if IsFundMovingAction(tc.Name) {
		if blocked, err := o.checkUserCanTransact(ctx, userID); blocked != nil || err != nil {
			if err != nil {
				return nil, err
			}
			return blocked, nil
		}
	}

	params := make(map[string]interface{}, len(tc.Arguments))
	for k, v := range tc.Arguments {
		params[k] = v
	}
	// The confirm flag is granted by the user's confirmation, never by the model.
	delete(params, "confirm")

	action := &entities.PendingAction{
		ID:             uuid.New().String(),
		ConversationID: convID,
		UserID:         userID,
		Action:         tc.Name,
		Description:    executionActionDescription(tc.Name, params),
		Params:         params,
		ExpiresAt:      time.Now().Add(pendingActionTTL),
		CreatedAt:      time.Now(),
	}

	if err := o.pending.Set(ctx, convID, action); err != nil {
		return nil, fmt.Errorf("store pending %s action: %w", tc.Name, err)
	}

	return map[string]interface{}{
		"action_required": true,
		"pending_action":  action,
	}, nil
}

// executeConfirmedExecutionAction runs a confirmed Execution Engine action
// through the core tool registry with the confirm flag set.
func (o *AgentAdapter) executeConfirmedExecutionAction(ctx context.Context, userID uuid.UUID, action *entities.PendingAction) error {
	if o.agent == nil {
		return fmt.Errorf("agent unavailable")
	}
	params := make(map[string]interface{}, len(action.Params)+1)
	for k, v := range action.Params {
		params[k] = v
	}
	params["confirm"] = true
	_, err := o.agent.ExecuteToolStrict(ctx, userID, action.Action, params)
	return err
}
