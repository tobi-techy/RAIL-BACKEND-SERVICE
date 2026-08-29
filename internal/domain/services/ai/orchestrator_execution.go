package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
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

	// BRIJ flight bookings.
	ToolCreateFlightIntent  = "create_flight_intent"
	ToolBookFlight          = "book_flight"
	ToolSaveTravelPassenger = "save_travel_passenger"
	ToolRequestFlightRefund = "request_flight_refund"

	// Bank transfers + crypto sends.
	ToolSendToBank = "send_to_bank"
	ToolSendCrypto = "send_crypto"
)

// isExecutionActionTool moved to execution_tiers.go — it is derived from the
// canonical core.StageConfirmTools set (plus streaming-only extras) so the
// prompt tier list and this enforcement check can never drift apart.

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
	case "send_money":
		name := arg("recipient_name")
		if name != "" {
			return fmt.Sprintf("Send $%s to %s (%s)", arg("amount"), name, arg("identifier"))
		}
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
	case ToolCreateFlightIntent:
		return fmt.Sprintf("Lock this flight (intent %s) and prepare it for booking", arg("offer_id"))
	case ToolBookFlight:
		name := flightPassengerName(args)
		if name == "" {
			name = "the selected traveler"
		}
		// The charge shown at execution comes from the persisted intent, never
		// from the model; the confirmation states the Spend impact instead of a
		// model-supplied amount.
		if totalUSD, ok := args["total_usd"].(string); ok && totalUSD != "" {
			line := fmt.Sprintf("Book flight for %s — $%s total (fare + Rail fee) charged from Spend", name, totalUSD)
			if ngn, ok := args["total_ngn"].(string); ok && ngn != "" {
				line += fmt.Sprintf(" (≈₦%s)", ngn)
			}
			return line
		}
		return fmt.Sprintf("Book flight for %s — escrow and the Rail fee are charged from Spend", name)
	case ToolSaveTravelPassenger:
		return fmt.Sprintf("Save traveler profile %q", arg("label"))
	case ToolRequestFlightRefund:
		return fmt.Sprintf("Request refund for flight %s: %s", arg("intent_id"), arg("reason"))
	case ToolSendToBank:
		acctName := arg("account_name")
		bankName := arg("bank_name")
		if bankName == "" {
			bankName = arg("bank_code")
		}
		if acctName != "" {
			return fmt.Sprintf("Send ₦%s to %s — %s %s", arg("amount"), acctName, bankName, arg("account_number"))
		}
		return fmt.Sprintf("Send ₦%s to %s %s", arg("amount"), bankName, arg("account_number"))
	case ToolSendCrypto:
		addr := arg("destination_address")
		if len(addr) > 12 {
			addr = addr[:6] + "..." + addr[len(addr)-4:]
		}
		return fmt.Sprintf("Send $%s USDC to %s", arg("amount"), addr)
	default:
		return "Execute " + name
	}
}

// flightPassengerName extracts a display name from the book_flight passenger
// object (given_name + family_name), so the confirmation card never reads
// "Book flight for ".
func flightPassengerName(args map[string]interface{}) string {
	if args == nil {
		return ""
	}
	raw, ok := args["passenger"]
	if !ok {
		return ""
	}
	var p map[string]interface{}
	switch t := raw.(type) {
	case map[string]interface{}:
		p = t
	case string:
		_ = json.Unmarshal([]byte(t), &p)
	}
	if p == nil {
		return ""
	}
	given, _ := p["given_name"].(string)
	family, _ := p["family_name"].(string)
	return strings.TrimSpace(given + " " + family)
}

// quoteFlightBooking resolves the exact charge for a staged book_flight from
// the persisted intent (never model-supplied numbers) and attaches it to the
// pending-action params so the confirmation card shows the fare + Rail fee
// breakdown with USD and NGN totals. Best effort: on any failure the params
// are left unchanged and the card falls back to its generic wording.
func (o *AgentAdapter) quoteFlightBooking(ctx context.Context, userID uuid.UUID, params map[string]interface{}) {
	intentID := ""
	if v, ok := params["intent_id"].(string); ok {
		intentID = strings.TrimSpace(v)
	}
	if intentID == "" || o.travel == nil {
		return
	}
	quote, err := o.travel.QuoteIntent(ctx, userID, intentID)
	if err != nil {
		o.logger.Warn("failed to resolve flight quote for confirmation card",
			zap.Error(err), zap.String("intent_id", intentID), zap.String("user_id", userID.String()))
		return
	}
	totalUSD, _ := quote["total_usd"].(string)
	if strings.TrimSpace(totalUSD) == "" {
		return
	}
	total, err := decimal.NewFromString(totalUSD)
	if err != nil || !total.IsPositive() {
		o.logger.Warn("failed to parse flight total for confirmation card",
			zap.Error(err), zap.String("total_usd", totalUSD), zap.String("intent_id", intentID), zap.String("user_id", userID.String()))
		return
	}
	quoteParams := map[string]interface{}{
		"intent_id":    intentID,
		"fare_usd":     quote["fare_usd"],
		"rail_fee_usd": quote["rail_fee_usd"],
		"total_usd":    totalUSD,
	}
	params["total_usd"] = totalUSD
	// The NGN total is only attached when a live rate resolves; without one the
	// card shows the exact USD amount and omits the naira equivalent rather
	// than fabricating a conversion from a fallback rate.
	if o.currencyRates != nil {
		if r, rerr := o.currencyRates.GetLatestRate(ctx, "USD", "NGN"); rerr == nil && r.IsPositive() {
			ngn := total.Mul(r).Round(0)
			quoteParams["ngn_rate"] = r.String()
			quoteParams["total_ngn"] = ngn.String()
			params["total_ngn"] = ngn.String()
		}
	}
	params["quote"] = quoteParams
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

	// Resolve the exact charge for book_flight from the persisted intent so the
	// confirmation card never shows a model-supplied amount. The NGN equivalent
	// is attached only when a live rate resolves.
	if tc.Name == ToolBookFlight {
		o.quoteFlightBooking(ctx, userID, params)
	}

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
