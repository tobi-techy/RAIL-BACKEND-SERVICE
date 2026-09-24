package confirmation

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// Narrow execution seams. The confirmation primitive never imports concrete
// domain services — owning domains register adapters in DI wiring. Same card,
// different payloads, different handlers.

// P2PSender moves money between users. Implemented by the P2P service.
type P2PSender interface {
	Send(ctx context.Context, senderID uuid.UUID, req *entities.P2PSendRequest) (*entities.P2PTransferResponse, error)
}

// TransferSendExecutor builds a P2P send from the card payload.
// Payload: identifier|to|recipient, amount, note?. Idempotent via actionId.
func TransferSendExecutor(sender P2PSender) Executor {
	return func(ctx context.Context, userID uuid.UUID, c *entities.Confirmation) (string, error) {
		if sender == nil {
			return "", fmt.Errorf("p2p sender not wired (fail-closed)")
		}
		identifier := firstNonEmpty(c.Payload, "identifier", "to", "recipient", "to_name")
		amount := firstNonEmpty(c.Payload, "amount", "amount_ngn", "amount_usd")
		if identifier == "" || amount == "" {
			return "", fmt.Errorf("transfer.send needs identifier + amount")
		}
		var note string
		if n, ok := c.Payload["note"].(string); ok {
			note = n
		}
		resp, err := sender.Send(ctx, userID, &entities.P2PSendRequest{
			Identifier:     identifier,
			Amount:         amount,
			Note:           note,
			IdempotencyKey: c.ExecuteKey,
		})
		if err != nil {
			return "", err
		}
		if resp != nil && resp.Transfer != nil && resp.Transfer.ID != uuid.Nil {
			return fmt.Sprintf("Sent %s (ref %s)", amount, resp.Transfer.ID.String()[:8]), nil
		}
		return fmt.Sprintf("Sent %s", amount), nil
	}
}

// InvestBuyer places a single-asset order. Implemented by the investment
// execution service (orders adjust target allocation, no price guarantee).
type InvestBuyer interface {
	PlaceOrder(ctx context.Context, userID uuid.UUID, req *entities.InvestmentOrderRequest, actor entities.InvestmentActor) (*entities.InvestmentOrderResponse, error)
}

// InvestBuyExecutor builds a buy order from the card payload.
// Payload: symbol|asset, amount_usd|notional|amount, strategy_id?.
func InvestBuyExecutor(buyer InvestBuyer) Executor {
	return investExecutor(buyer, "buy")
}

// InvestSellExecutor builds a sell order from the card payload.
// Payload: symbol|asset, amount_usd|notional|amount, strategy_id?.
func InvestSellExecutor(buyer InvestBuyer) Executor {
	return investExecutor(buyer, "sell")
}

func investExecutor(buyer InvestBuyer, side string) Executor {
	return func(ctx context.Context, userID uuid.UUID, c *entities.Confirmation) (string, error) {
		if buyer == nil {
			return "", fmt.Errorf("invest buyer not wired (fail-closed)")
		}
		symbol := firstNonEmpty(c.Payload, "symbol", "asset", "ticker")
		amountRaw := firstNonEmpty(c.Payload, "amount_usd", "notional", "amount")
		strategyID := firstNonEmpty(c.Payload, "strategy_id")
		if symbol == "" || amountRaw == "" {
			return "", fmt.Errorf("invest.%s needs symbol + amount_usd", side)
		}
		amount, err := decimal.NewFromString(strings.TrimSpace(strings.TrimPrefix(amountRaw, "$")))
		if err != nil || !amount.IsPositive() {
			return "", fmt.Errorf("invest.%s needs a positive amount_usd (got %q)", side, amountRaw)
		}
		resp, err := buyer.PlaceOrder(ctx, userID, &entities.InvestmentOrderRequest{
			StrategyID:     strategyID,
			Symbol:         strings.ToUpper(symbol),
			Side:           side,
			AmountUSD:      amount,
			IdempotencyKey: c.ExecuteKey,
		}, entities.InvestmentActorMiriam)
		if err != nil {
			return "", err
		}
		if resp != nil && resp.Execution != nil {
			return fmt.Sprintf("Order placed (%s)", strings.TrimSpace(string(resp.Execution.Status))), nil
		}
		verb := "Bought"
		if side == "sell" {
			verb = "Sold"
		}
		return fmt.Sprintf("%s %s", verb, strings.ToUpper(symbol)), nil
	}
}

// SaveRuleUpdater changes an existing sweep/save automation.
// Implemented by the automation service (Update).
type SaveRuleUpdater interface {
	Update(ctx context.Context, userID, id uuid.UUID, req *UpdateAutomationFields) (*struct{}, error)
}

// UpdateAutomationFields mirrors automation.UpdateAutomationRequest without
// importing the automation package (keeps the primitive dependency-free).
type UpdateAutomationFields struct {
	Name          *string
	Description   *string
	IsActive      *bool
	TriggerConfig map[string]any
	ActionConfig  map[string]any
}

// SaveSweepExecutor changes the save rule named by the card payload.
// Payload: automation_id|rule_id, percentage|amount (+ optional destination).
func SaveSweepExecutor(updater SaveRuleUpdater) Executor {
	return func(ctx context.Context, userID uuid.UUID, c *entities.Confirmation) (string, error) {
		if updater == nil {
			return "", fmt.Errorf("save-rule updater not wired (fail-closed)")
		}
		ruleID := firstNonEmpty(c.Payload, "automation_id", "rule_id")
		if ruleID == "" {
			return "", fmt.Errorf("save.sweep needs automation_id")
		}
		id, err := uuid.Parse(ruleID)
		if err != nil {
			return "", fmt.Errorf("save.sweep needs a valid automation_id")
		}
		actionCfg := map[string]any{}
		if pct := firstNonEmpty(c.Payload, "percentage", "percent", "pct"); pct != "" {
			actionCfg["percentage"] = pct
		}
		if amt := firstNonEmpty(c.Payload, "amount"); amt != "" {
			actionCfg["amount"] = amt
		}
		if dest := firstNonEmpty(c.Payload, "destination", "to"); dest != "" {
			actionCfg["destination"] = dest
		}
		if len(actionCfg) == 0 {
			return "", fmt.Errorf("save.sweep needs percentage or amount")
		}
		if _, err := updater.Update(ctx, userID, id, &UpdateAutomationFields{ActionConfig: actionCfg}); err != nil {
			return "", err
		}
		if pct, ok := actionCfg["percentage"]; ok {
			return fmt.Sprintf("Save rule now parks %v of inflow", pct), nil
		}
		return fmt.Sprintf("Save rule updated (%v per inflow)", actionCfg["amount"]), nil
	}
}

func firstNonEmpty(p map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := p[k]; ok && v != nil {
			if s := strings.TrimSpace(fmt.Sprintf("%v", v)); s != "" {
				return s
			}
		}
	}
	return ""
}
