package di

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	aiservice "github.com/rail-service/rail_service/internal/domain/services/ai"
	aicore "github.com/rail-service/rail_service/internal/domain/services/ai/core"
	"github.com/rail-service/rail_service/internal/domain/services/automation"
	"github.com/rail-service/rail_service/internal/domain/services/billpay"
	"github.com/rail-service/rail_service/internal/domain/services/copytrading"
	"github.com/rail-service/rail_service/internal/domain/services/investing"
	moneyguardservice "github.com/rail-service/rail_service/internal/domain/services/moneyguard"
	obligationservice "github.com/rail-service/rail_service/internal/domain/services/obligation"
	p2pservice "github.com/rail-service/rail_service/internal/domain/services/p2p"
	rampsvc "github.com/rail-service/rail_service/internal/domain/services/ramp"
	"github.com/rail-service/rail_service/internal/domain/services/withdrawal"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/airbills"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/publictrades"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/shopspring/decimal"
)

// Execution Engine (spec 5.2) provider adapters. Each wraps an existing
// domain service so the AI tools execute through the same code paths (and the
// same checks) as the app's own endpoints.

// --- BillPayProvider ---

func buildBillPayProvider(c *Container) aicore.BillPayProvider {
	if c.FinancialObligationService == nil || c.AutomationService == nil {
		return nil
	}
	return &billPayExecAdapter{obligations: c.FinancialObligationService, automations: c.AutomationService}
}

type billPayExecAdapter struct {
	obligations *obligationservice.Service
	automations *automation.Service
}

func (a *billPayExecAdapter) GetUpcomingBills(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
	obligations, err := a.obligations.ListActive(ctx, userID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	out := make([]map[string]interface{}, 0, len(obligations))
	for _, ob := range obligations {
		bill := map[string]interface{}{
			"id":       ob.ID.String(),
			"name":     ob.Name,
			"type":     ob.Type,
			"amount":   ob.Amount.StringFixed(2),
			"currency": ob.Currency,
			"cadence":  ob.Cadence,
			"priority": ob.Priority,
		}
		if ob.Counterparty != nil {
			bill["counterparty"] = *ob.Counterparty
		}
		if ob.DueDay != nil {
			bill["due_day"] = *ob.DueDay
			due := time.Date(now.Year(), now.Month(), *ob.DueDay, 0, 0, 0, 0, time.UTC)
			if due.Before(now) {
				due = due.AddDate(0, 1, 0)
			}
			bill["next_due"] = due.Format("2006-01-02")
			bill["days_until_due"] = int(due.Sub(now).Hours() / 24)
		} else if ob.DueDate != nil {
			bill["next_due"] = ob.DueDate.Format("2006-01-02")
		}
		out = append(out, bill)
	}
	return out, nil
}

func (a *billPayExecAdapter) SetupAutoPay(ctx context.Context, userID uuid.UUID, obligationID, payeeIdentifier, payeeName string) (map[string]interface{}, error) {
	obID, err := uuid.Parse(obligationID)
	if err != nil {
		return nil, fmt.Errorf("invalid obligation id: %s", obligationID)
	}
	ob, err := a.obligations.Get(ctx, userID, obID)
	if err != nil {
		return nil, fmt.Errorf("look up bill: %w", err)
	}
	if ob.DueDay == nil {
		return nil, fmt.Errorf("bill %q has no due day — set a due day first, then I can auto-pay it", ob.Name)
	}
	amount, _ := ob.Amount.Float64()

	payeeIdentifier = strings.TrimSpace(payeeIdentifier)
	actionType := entities.ActionTransferToSpend
	name := "Auto-pay: " + ob.Name
	daysBeforeDue := 2
	behavior := "2 days before each due date the bill amount moves from Stash to Spend and the user is notified"
	baseConfig := map[string]interface{}{
		"amount":      amount,
		"from_wallet": "stash",
		"to_wallet":   "spend",
	}
	if payeeIdentifier != "" {
		actionType = entities.ActionSendP2P
		name = "Pay bill: " + ob.Name
		daysBeforeDue = 1 // pay when the due day arrives
		label := payeeName
		if label == "" {
			label = payeeIdentifier
		}
		behavior = fmt.Sprintf("on each due date $%s is sent to %s and the user gets a payment receipt; non-Rail payees receive a claim link they can pay out to their bank", ob.Amount.StringFixed(2), label)
		baseConfig = map[string]interface{}{
			"amount":           amount,
			"payee_identifier": payeeIdentifier,
			"payee_name":       payeeName,
			"bill_name":        ob.Name,
		}
	}

	// Transfer automations refuse to execute without passcode consent stamps
	// (ensureTransferReauthorization). This tool is registered fund-moving, so
	// the confirm endpoint has already required an in-app passcode/biometric
	// session before we get here — stamping now mirrors the app's own
	// automation endpoints, and consent expires on the same 90-day window.
	actionConfig := automation.StampTransferConsent(baseConfig, time.Now().UTC())
	created, err := a.automations.Create(ctx, userID, &automation.CreateAutomationRequest{
		Name:        name,
		TriggerType: entities.TriggerObligationDue,
		TriggerConfig: map[string]interface{}{
			"days_before_due": daysBeforeDue,
		},
		ActionType:        actionType,
		ActionConfig:      actionConfig,
		MaxTriggersPerDay: 1,
		CooldownMinutes:   60 * 24 * 7, // at most once a week per bill
		ObligationID:      &obID,
	})
	if err != nil {
		return nil, err
	}
	result := map[string]interface{}{
		"status":        "autopay_created",
		"automation_id": created.ID.String(),
		"bill":          ob.Name,
		"amount":        ob.Amount.StringFixed(2),
		"behavior":      behavior,
	}
	if payeeIdentifier != "" {
		result["payee"] = payeeIdentifier
		if payeeName != "" {
			result["payee_name"] = payeeName
		}
	}
	return result, nil
}

// p2pBillPayerAdapter lets pay-bill automations send real money via P2P.
// Non-Rail payees (email/phone) receive a claim link they can pay out to a bank.
type p2pBillPayerAdapter struct {
	p2p *p2pservice.Service
}

func (a *p2pBillPayerAdapter) SendP2P(ctx context.Context, senderID uuid.UUID, identifier, amount, note, idempotencyKey string) (string, error) {
	resp, err := a.p2p.Send(ctx, senderID, &entities.P2PSendRequest{
		Identifier:     identifier,
		Amount:         amount,
		Note:           note,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		return "", err
	}
	if resp == nil || resp.Transfer == nil {
		return "", fmt.Errorf("p2p transfer returned no reference")
	}
	return resp.Transfer.ID.String(), nil
}

// --- BillsProvider (Nigerian bill payments via Airbills) ---

func buildBillsProvider(c *Container) aicore.BillsProvider {
	if c.BillPayService == nil {
		return nil
	}
	return &billsExecAdapter{bills: c.BillPayService, automations: c.AutomationService}
}

type billsExecAdapter struct {
	bills       *billpay.Service
	automations *automation.Service
}

func productsToMaps(products []airbills.Product) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(products))
	for _, p := range products {
		amount := p.Amount
		if amount == 0 {
			amount = p.ProdAmount
		}
		item := map[string]interface{}{
			"prod_id": p.ProdID,
			"name":    p.Name,
			"amount":  amount,
		}
		if p.ElectID != "" {
			item["elect_id"] = p.ElectID
		}
		if p.NetworkID != "" {
			item["network_id"] = p.NetworkID
		}
		out = append(out, item)
	}
	return out
}

func (a *billsExecAdapter) ListProviders(ctx context.Context, category string) ([]map[string]interface{}, error) {
	var (
		products []airbills.Product
		err      error
	)
	switch strings.ToLower(strings.TrimSpace(category)) {
	case billpay.CategoryElectricity:
		products, err = a.bills.ListElectricityDiscos(ctx)
	case billpay.CategoryCable:
		products, err = a.bills.ListCablePackages(ctx)
	case billpay.CategoryBetting:
		products, err = a.bills.ListBettingProviders(ctx)
	case billpay.CategoryTransport:
		products, err = a.bills.ListTransportProviders(ctx)
	case billpay.CategoryData:
		products, err = a.bills.ListDataPlans(ctx, "")
	default:
		return nil, fmt.Errorf("no provider list for category %q", category)
	}
	if err != nil {
		return nil, err
	}
	return productsToMaps(products), nil
}

func (a *billsExecAdapter) GetDataPlans(ctx context.Context, networkID string) ([]map[string]interface{}, error) {
	products, err := a.bills.ListDataPlans(ctx, networkID)
	if err != nil {
		return nil, err
	}
	return productsToMaps(products), nil
}

func (a *billsExecAdapter) GetCablePackages(ctx context.Context) ([]map[string]interface{}, error) {
	products, err := a.bills.ListCablePackages(ctx)
	if err != nil {
		return nil, err
	}
	return productsToMaps(products), nil
}

func (a *billsExecAdapter) ValidateMeter(ctx context.Context, meterNo, electID string) (map[string]interface{}, error) {
	name, err := a.bills.ValidateMeter(ctx, meterNo, electID)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"meter_no": meterNo, "account_name": name}, nil
}

func (a *billsExecAdapter) DetectNetwork(ctx context.Context, phone string) (map[string]interface{}, error) {
	id, name, err := a.bills.DetectNetwork(ctx, phone)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"network_id": id, "network": name}, nil
}

func (a *billsExecAdapter) ListBeneficiaries(ctx context.Context, userID uuid.UUID, category string) ([]map[string]interface{}, error) {
	list, err := a.bills.ListBeneficiaries(ctx, userID, category)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]interface{}, 0, len(list))
	for _, b := range list {
		item := map[string]interface{}{
			"id":        b.ID.String(),
			"label":     b.Label,
			"category":  b.Category,
			"recipient": b.Recipient,
		}
		if b.NetworkID != "" {
			item["network_id"] = b.NetworkID
		}
		if b.ProdID != "" {
			item["prod_id"] = b.ProdID
		}
		if b.ElectID != "" {
			item["elect_id"] = b.ElectID
		}
		if b.RecipientName != "" {
			item["recipient_name"] = b.RecipientName
		}
		out = append(out, item)
	}
	return out, nil
}

func (a *billsExecAdapter) GetPaymentHistory(ctx context.Context, userID uuid.UUID, limit int) ([]map[string]interface{}, error) {
	payments, err := a.bills.GetPaymentHistory(ctx, userID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]interface{}, 0, len(payments))
	for _, p := range payments {
		out = append(out, map[string]interface{}{
			"id":          p.ID.String(),
			"category":    p.Category,
			"recipient":   p.Recipient,
			"amount_ngn":  p.AmountNGN,
			"amount_usdc": p.AmountUSDC,
			"status":      p.Status,
			"created_at":  p.CreatedAt.Format(time.RFC3339),
		})
	}
	return out, nil
}

func (a *billsExecAdapter) PayBill(ctx context.Context, userID uuid.UUID, category, recipient, networkID, prodID, electID string, amountNGN float64) (map[string]interface{}, error) {
	res, err := a.bills.PayBill(ctx, userID, billpay.PayBillRequest{
		Category:  category,
		Recipient: recipient,
		NetworkID: networkID,
		ProdID:    prodID,
		ElectID:   electID,
		AmountNGN: amountNGN,
	})
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"status":      res.Status,
		"order_id":    res.OrderID.String(),
		"category":    res.Category,
		"recipient":   res.Recipient,
		"amount_ngn":  res.AmountNGN,
		"amount_usdc": res.AmountUSDC,
	}, nil
}

func (a *billsExecAdapter) AutomateBill(ctx context.Context, userID uuid.UUID, category, recipient, networkID, prodID, electID string, amountNGN float64, schedule string) (map[string]interface{}, error) {
	if a.automations == nil {
		return nil, fmt.Errorf("automations are not available")
	}
	if !billpay.IsCategorySupported(category) {
		return nil, fmt.Errorf("unsupported bill category: %q", category)
	}
	// The automate_bill tool is fund-moving, so the confirm endpoint has already
	// required an in-app Face ID / passcode session — stamp consent now so the
	// automation engine's ensureTransferReauthorization gate passes (90-day window).
	actionConfig := automation.StampTransferConsent(map[string]interface{}{
		"category":   category,
		"recipient":  recipient,
		"network_id": networkID,
		"prod_id":    prodID,
		"elect_id":   electID,
		"amount_ngn": amountNGN,
	}, time.Now().UTC())

	created, err := a.automations.Create(ctx, userID, &automation.CreateAutomationRequest{
		Name:        fmt.Sprintf("Auto-pay %s: %s", category, recipient),
		TriggerType: entities.TriggerSchedule,
		TriggerConfig: map[string]interface{}{
			"schedule": schedule,
		},
		ActionType:        entities.ActionPayUtilityBill,
		ActionConfig:      actionConfig,
		MaxTriggersPerDay: 1,
	})
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"status":        "automation_created",
		"automation_id": created.ID.String(),
		"category":      category,
		"recipient":     recipient,
		"amount_ngn":    amountNGN,
		"schedule":      schedule,
	}, nil
}

func (a *billsExecAdapter) SaveBeneficiary(ctx context.Context, userID uuid.UUID, label, category, recipient, networkID, prodID, electID string) (map[string]interface{}, error) {
	b, err := a.bills.SaveBeneficiary(ctx, userID, billpay.Beneficiary{
		Label:     label,
		Category:  category,
		Recipient: recipient,
		NetworkID: networkID,
		ProdID:    prodID,
		ElectID:   electID,
	})
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"status":    "saved",
		"id":        b.ID.String(),
		"label":     b.Label,
		"category":  b.Category,
		"recipient": b.Recipient,
	}, nil
}

// --- SubscriptionAuditProvider ---

func buildSubscriptionAuditProvider(c *Container, recurring aiservice.RecurringExpenseDetector) aicore.SubscriptionAuditProvider {
	if recurring == nil {
		return nil
	}
	return &subscriptionAuditAdapter{
		recurring:   recurring,
		obligations: c.FinancialObligationService,
		guard:       c.MoneyGuardService,
	}
}

type subscriptionAuditAdapter struct {
	recurring   aiservice.RecurringExpenseDetector
	obligations *obligationservice.Service
	guard       *moneyguardservice.Service
}

const unusedAfterDays = 45

func (a *subscriptionAuditAdapter) AuditSubscriptions(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	expenses, err := a.recurring.DetectRecurring(ctx, userID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	subs := make([]map[string]interface{}, 0, len(expenses))
	monthlyTotal := decimal.Zero
	staleSavings := decimal.Zero
	for _, e := range expenses {
		monthlyCost := e.AvgAmount
		if e.Frequency == "weekly" {
			monthlyCost = e.AvgAmount.Mul(decimal.NewFromFloat(4.33))
		}
		item := map[string]interface{}{
			"merchant":     e.Merchant,
			"frequency":    e.Frequency,
			"avg_amount":   e.AvgAmount.StringFixed(2),
			"monthly_cost": monthlyCost.StringFixed(2),
			"charges":      e.Count,
			"last_charge":  e.LastSeen,
		}
		if last, perr := time.Parse("2006-01-02", e.LastSeen); perr == nil {
			if int(now.Sub(last).Hours()/24) > unusedAfterDays {
				item["possibly_unused"] = true
				staleSavings = staleSavings.Add(monthlyCost)
			}
		}
		monthlyTotal = monthlyTotal.Add(monthlyCost)
		subs = append(subs, item)
	}

	result := map[string]interface{}{
		"subscriptions":             subs,
		"count":                     len(subs),
		"monthly_total":             monthlyTotal.StringFixed(2),
		"potential_monthly_savings": staleSavings.StringFixed(2),
	}

	// Include tracked subscription obligations so Miriam sees protected /
	// already-cancelled ones too.
	if a.obligations != nil {
		tracked, terr := a.obligations.List(ctx, userID, obligationservice.ListFilter{Type: "subscription"})
		if terr == nil && len(tracked) > 0 {
			trackedOut := make([]map[string]interface{}, 0, len(tracked))
			for _, ob := range tracked {
				trackedOut = append(trackedOut, map[string]interface{}{
					"id":     ob.ID.String(),
					"name":   ob.Name,
					"amount": ob.Amount.StringFixed(2),
					"status": ob.Status,
				})
			}
			result["tracked_subscriptions"] = trackedOut
		}
	}
	return result, nil
}

func (a *subscriptionAuditAdapter) CancelSubscription(ctx context.Context, userID uuid.UUID, name string, blockMerchant bool) (map[string]interface{}, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("subscription name is required")
	}
	steps := make([]string, 0, 3)

	if a.obligations != nil {
		tracked, err := a.obligations.List(ctx, userID, obligationservice.ListFilter{Type: "subscription"})
		if err == nil {
			for _, ob := range tracked {
				if strings.EqualFold(ob.Name, name) || strings.Contains(strings.ToLower(ob.Name), strings.ToLower(name)) {
					cancelled := entities.ObligationStatusCancelled
					if _, uerr := a.obligations.Update(ctx, userID, ob.ID, obligationservice.UpdateRequest{Status: &cancelled}); uerr == nil {
						steps = append(steps, fmt.Sprintf("marked tracked subscription %q cancelled", ob.Name))
					}
					break
				}
			}
		}
	}

	if blockMerchant {
		if a.guard == nil {
			return nil, fmt.Errorf("merchant blocking is unavailable")
		}
		if _, err := a.guard.BlockMerchant(ctx, userID, name); err != nil {
			return nil, fmt.Errorf("block merchant: %w", err)
		}
		steps = append(steps, fmt.Sprintf("blocked %q on the Rail card — renewal charges will decline", name))
	}

	if len(steps) == 0 {
		steps = append(steps, "no tracked subscription matched — nothing changed in Rail")
	}
	return map[string]interface{}{
		"status":   "done",
		"steps":    steps,
		"reminder": "Rail can't cancel with the merchant directly — the user should also cancel in the merchant's app or site to stop billing on other payment methods.",
	}, nil
}

// --- InvestmentExecutionProvider ---

func buildInvestmentExecProvider(c *Container) aicore.InvestmentExecutionProvider {
	if c.InvestingService == nil {
		return nil
	}
	return &investmentExecAdapter{investing: c.InvestingService}
}

type investmentExecAdapter struct {
	investing *investing.Service
}

func (a *investmentExecAdapter) ListInvestmentOptions(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
	baskets, err := a.investing.ListBaskets(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]interface{}, 0, len(baskets))
	for _, b := range baskets {
		if b == nil {
			continue
		}
		out = append(out, map[string]interface{}{
			"id":          b.ID.String(),
			"name":        b.Name,
			"description": b.Description,
			"risk_level":  b.RiskLevel,
		})
	}
	return out, nil
}

func (a *investmentExecAdapter) ExecuteInvestment(ctx context.Context, userID uuid.UUID, basketID, side, amount string) (map[string]interface{}, error) {
	bID, err := uuid.Parse(basketID)
	if err != nil {
		return nil, fmt.Errorf("invalid basket id: %s", basketID)
	}
	if _, err := decimal.NewFromString(amount); err != nil {
		return nil, fmt.Errorf("invalid amount: %s", amount)
	}
	key := uuid.New().String()
	order, err := a.investing.CreateOrder(ctx, userID, &entities.OrderCreateRequest{
		BasketID:       bID,
		Side:           entities.OrderSide(side),
		Amount:         amount,
		IdempotencyKey: &key,
	})
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"status":   string(order.Status),
		"order_id": order.ID.String(),
		"side":     string(order.Side),
		"amount":   order.Amount.StringFixed(2),
	}, nil
}

// --- YieldOptimizationProvider ---

func buildYieldOptProvider(c *Container, funds aiservice.FundsTransferer) aicore.YieldOptimizationProvider {
	if funds == nil {
		return nil
	}
	return &yieldOptAdapter{funds: funds}
}

type yieldOptAdapter struct {
	funds aiservice.FundsTransferer
}

// yieldOptMaxTransfer mirrors the chat transfer cap so Miriam can't move more
// in one yield sweep than she can in one transfer.
var yieldOptMaxTransfer = decimal.NewFromInt(500)

func (a *yieldOptAdapter) GetYieldStatus(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	spend, err := a.funds.GetSpendBalance(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get spend balance: %w", err)
	}
	stash, err := a.funds.GetStashBalance(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get stash balance: %w", err)
	}
	total := spend.Add(stash)
	status := map[string]interface{}{
		"spend_balance": spend.StringFixed(2),
		"stash_balance": stash.StringFixed(2),
		"earning_yield": stash.StringFixed(2),
		"idle_cash":     spend.StringFixed(2),
		"note":          "Stash is the yield-bearing side; Spend earns nothing.",
	}
	if total.IsPositive() {
		status["pct_earning"] = stash.Div(total).Mul(decimal.NewFromInt(100)).StringFixed(1)
	}
	// Suggest sweeping idle spend above a small buffer, capped at the
	// per-transfer limit. Miriam decides whether to offer it.
	buffer := decimal.NewFromInt(50)
	if suggest := spend.Sub(buffer); suggest.IsPositive() {
		if suggest.GreaterThan(yieldOptMaxTransfer) {
			suggest = yieldOptMaxTransfer
		}
		status["suggested_move"] = suggest.StringFixed(2)
	}
	return status, nil
}

func (a *yieldOptAdapter) OptimizeYield(ctx context.Context, userID uuid.UUID, amount string) (map[string]interface{}, error) {
	amt, err := decimal.NewFromString(amount)
	if err != nil || !amt.IsPositive() {
		return nil, fmt.Errorf("invalid amount: %s", amount)
	}
	if amt.GreaterThan(yieldOptMaxTransfer) {
		return nil, fmt.Errorf("single moves are capped at $%s for safety — ask for a smaller amount", yieldOptMaxTransfer.StringFixed(0))
	}
	spend, err := a.funds.GetSpendBalance(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get spend balance: %w", err)
	}
	if spend.LessThan(amt) {
		return nil, fmt.Errorf("insufficient spend balance: $%s available", spend.StringFixed(2))
	}
	if err := a.funds.TransferSpendToStash(ctx, userID, amt, uuid.New().String()); err != nil {
		return nil, err
	}
	newSpend, _ := a.funds.GetSpendBalance(ctx, userID)
	newStash, _ := a.funds.GetStashBalance(ctx, userID)
	return map[string]interface{}{
		"status":        "moved",
		"amount":        amt.StringFixed(2),
		"spend_balance": newSpend.StringFixed(2),
		"stash_balance": newStash.StringFixed(2),
	}, nil
}

// --- MerchantBlockProvider ---

func buildMerchantBlockProvider(c *Container) aicore.MerchantBlockProvider {
	if c.MoneyGuardService == nil {
		return nil
	}
	return &merchantBlockAdapter{guard: c.MoneyGuardService}
}

type merchantBlockAdapter struct {
	guard *moneyguardservice.Service
}

func (a *merchantBlockAdapter) BlockMerchant(ctx context.Context, userID uuid.UUID, merchant string) (map[string]interface{}, error) {
	cap, err := a.guard.BlockMerchant(ctx, userID, merchant)
	if err != nil {
		return nil, err
	}
	result := map[string]interface{}{
		"status":   "blocked",
		"merchant": cap.ScopeValue,
		"effect":   "future card charges from this merchant will be declined",
	}
	// Observe mode downgrades decline caps to warnings; tell the user instead
	// of letting them believe the block is enforced.
	if settings, err := a.guard.GetSettings(ctx, userID); err == nil && settings.GuardianMode == entities.GuardianModeObserve {
		result["warning"] = "your Money Guard is in observe mode, so charges will be flagged but not declined; switch to nudge or strict mode to enforce the block"
	}
	return result, nil
}

func (a *merchantBlockAdapter) UnblockMerchant(ctx context.Context, userID uuid.UUID, merchant string) (map[string]interface{}, error) {
	if err := a.guard.UnblockMerchant(ctx, userID, merchant); err != nil {
		return nil, err
	}
	return map[string]interface{}{"status": "unblocked", "merchant": merchant}, nil
}

func (a *merchantBlockAdapter) ListBlockedMerchants(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
	caps, err := a.guard.ListBlockedMerchants(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]interface{}, 0, len(caps))
	for _, c := range caps {
		out = append(out, map[string]interface{}{
			"merchant": c.ScopeValue,
			"since":    c.CreatedAt.Format("2006-01-02"),
		})
	}
	return out, nil
}

// publicTradesSourceAdapter bridges the publictrades client into the domain
// copytrading.PublicTradesSource interface.
type publicTradesSourceAdapter struct {
	client *publictrades.Client
}

func (a *publicTradesSourceAdapter) GetFigureTrades(ctx context.Context, figureKey string, since time.Time, limit int) ([]copytrading.PublicTrade, error) {
	trades, err := a.client.GetFigureTrades(ctx, figureKey, since, limit)
	if err != nil {
		return nil, err
	}
	out := make([]copytrading.PublicTrade, 0, len(trades))
	for _, t := range trades {
		out = append(out, copytrading.PublicTrade{
			Ticker:          t.Ticker,
			AssetName:       t.AssetName,
			Side:            t.Side,
			AmountMid:       t.AmountMid,
			AmountRange:     t.AmountRange,
			TransactionDate: t.TransactionDate,
			DisclosureDate:  t.DisclosureDate,
			Ref:             t.Ref,
		})
	}
	return out, nil
}

// --- TradeCopyProvider ---

func buildTradeCopyProvider(c *Container) aicore.TradeCopyProvider {
	if c.CopyTradingService == nil {
		return nil
	}
	return &tradeCopyAdapter{ct: c.CopyTradingService, public: c.PublicTradesClient}
}

type tradeCopyAdapter struct {
	ct     *copytrading.Service
	public *publictrades.Client
}

// disclosureLagNote keeps Miriam honest about what copying a public figure
// means: filings lag the actual trade by up to 45 days.
const disclosureLagNote = "trades come from public disclosure filings, which by law can lag the actual trade by up to 45 days; prices will differ from what the figure paid"

func (a *tradeCopyAdapter) ListConductors(ctx context.Context) ([]map[string]interface{}, error) {
	if a.public == nil {
		return nil, fmt.Errorf("public trade data source not configured")
	}
	figures, err := a.public.ListPopularFigures(ctx, 15)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]interface{}, 0, len(figures))
	for _, f := range figures {
		out = append(out, map[string]interface{}{
			"name":            f.Name,
			"chamber":         f.Chamber,
			"trades_12mo":     f.TradeCount,
			"last_traded":     f.LastTraded,
			"top_tickers":     f.TopTickers,
			"disclosure_note": disclosureLagNote,
		})
	}
	return out, nil
}

// resolveFigure finds the public figure best matching a user-supplied name.
func (a *tradeCopyAdapter) resolveFigure(ctx context.Context, name string) (*publictrades.Figure, error) {
	if a.public == nil {
		return nil, fmt.Errorf("public trade data source not configured")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("trader name is required")
	}
	figures, err := a.public.SearchFigures(ctx, name)
	if err != nil {
		return nil, err
	}
	switch {
	case len(figures) == 0:
		return nil, fmt.Errorf("no public figure named %q found in trade disclosures — ask the user for the full name", name)
	case len(figures) == 1:
		return &figures[0], nil
	default:
		// Exact full-name match wins; otherwise the most active match, but
		// only when it is clearly ahead — ambiguity goes back to the user.
		lower := strings.ToLower(name)
		for i := range figures {
			if strings.ToLower(figures[i].Name) == lower {
				return &figures[i], nil
			}
		}
		if figures[0].TradeCount >= figures[1].TradeCount*3 {
			return &figures[0], nil
		}
		names := make([]string, 0, 5)
		for i := 0; i < len(figures) && i < 5; i++ {
			names = append(names, figures[i].Name)
		}
		return nil, fmt.Errorf("multiple public figures match %q: %s — ask the user which one", name, strings.Join(names, ", "))
	}
}

func (a *tradeCopyAdapter) ResearchTrader(ctx context.Context, userID uuid.UUID, trader string) (map[string]interface{}, error) {
	figure, err := a.resolveFigure(ctx, trader)
	if err != nil {
		return nil, err
	}
	trades, err := a.public.GetFigureTrades(ctx, figure.Key, time.Now().AddDate(0, -6, 0), 15)
	if err != nil {
		return nil, err
	}
	recent := make([]map[string]interface{}, 0, len(trades))
	buysLast60Days := 0
	cutoff := time.Now().AddDate(0, 0, -60)
	for _, t := range trades {
		recent = append(recent, map[string]interface{}{
			"ticker":       t.Ticker,
			"asset":        t.AssetName,
			"side":         t.Side,
			"amount_range": t.AmountRange,
			"traded":       t.TransactionDate.Format("2006-01-02"),
			"disclosed":    t.DisclosureDate.Format("2006-01-02"),
		})
		if t.Side == "buy" && t.DisclosureDate.After(cutoff) {
			buysLast60Days++
		}
	}
	research := map[string]interface{}{
		"name":              figure.Name,
		"chamber":           figure.Chamber,
		"trades_12mo":       figure.TradeCount,
		"last_traded":       figure.LastTraded,
		"top_tickers":       figure.TopTickers,
		"recent_trades":     recent,
		"copyable_buys_now": buysLast60Days,
		"disclosure_note":   disclosureLagNote,
		"how_copying_works": "copy_trader immediately splits the user's amount across this figure's recent disclosed buys (real orders), then keeps copying new disclosures automatically",
	}
	if drafts, err := a.ct.GetUserDrafts(ctx, userID); err == nil {
		for _, d := range drafts {
			if d != nil && strings.EqualFold(d.ConductorName, figure.Name) {
				research["already_copying"] = map[string]interface{}{
					"draft_id": d.ID.String(),
					"status":   string(d.Status),
				}
				break
			}
		}
	}
	return research, nil
}

func (a *tradeCopyAdapter) GetCopyStatus(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
	drafts, err := a.ct.GetUserDrafts(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]interface{}, 0, len(drafts))
	for _, d := range drafts {
		if d == nil {
			continue
		}
		out = append(out, map[string]interface{}{
			"draft_id":          d.ID.String(),
			"conductor":         d.ConductorName,
			"status":            string(d.Status),
			"allocated_capital": d.AllocatedCapital.StringFixed(2),
			"current_value":     d.CurrentAUM.StringFixed(2),
			"profit_loss":       d.TotalProfitLoss.StringFixed(2),
			"return_pct":        d.ReturnPct.StringFixed(2),
		})
	}
	return out, nil
}

func (a *tradeCopyAdapter) StartCopying(ctx context.Context, userID uuid.UUID, trader, amount string) (map[string]interface{}, error) {
	figure, err := a.resolveFigure(ctx, trader)
	if err != nil {
		return nil, err
	}
	capital, err := decimal.NewFromString(amount)
	if err != nil || !capital.IsPositive() {
		return nil, fmt.Errorf("invalid amount: %s", amount)
	}
	result, err := a.ct.CopyRecentTrades(ctx, userID, figure.Key, figure.Name, capital)
	if err != nil {
		return nil, err
	}
	executed := make([]map[string]interface{}, 0, len(result.Trades))
	for _, t := range result.Trades {
		item := map[string]interface{}{
			"ticker":    t.Ticker,
			"side":      t.Side,
			"requested": t.RequestedUSD.StringFixed(2),
			"status":    string(t.Status),
			"disclosed": t.DisclosedAt,
		}
		if t.ExecutedUSD.IsPositive() {
			item["executed"] = t.ExecutedUSD.StringFixed(2)
			item["price"] = t.ExecutedPrice.StringFixed(2)
		}
		if t.Error != "" {
			item["error"] = t.Error
		}
		executed = append(executed, item)
	}
	return map[string]interface{}{
		"status":            "copied",
		"trader":            result.Conductor,
		"draft_id":          result.DraftID.String(),
		"allocated_capital": result.Allocated.StringFixed(2),
		"trades":            executed,
		"ongoing":           "new disclosed trades from this figure will keep copying automatically until stopped",
		"disclosure_note":   disclosureLagNote,
	}, nil
}

func (a *tradeCopyAdapter) PauseCopying(ctx context.Context, userID uuid.UUID, draftID string) (map[string]interface{}, error) {
	return a.manageDraft(ctx, userID, draftID, "paused", a.ct.PauseDraft)
}

func (a *tradeCopyAdapter) ResumeCopying(ctx context.Context, userID uuid.UUID, draftID string) (map[string]interface{}, error) {
	return a.manageDraft(ctx, userID, draftID, "resumed", a.ct.ResumeDraft)
}

func (a *tradeCopyAdapter) StopCopying(ctx context.Context, userID uuid.UUID, draftID string) (map[string]interface{}, error) {
	return a.manageDraft(ctx, userID, draftID, "stopped", a.ct.UnlinkDraft)
}

func (a *tradeCopyAdapter) manageDraft(ctx context.Context, userID uuid.UUID, draftID, status string, fn func(context.Context, uuid.UUID, uuid.UUID) error) (map[string]interface{}, error) {
	dID, err := uuid.Parse(draftID)
	if err != nil {
		return nil, fmt.Errorf("invalid draft id: %s", draftID)
	}
	if err := fn(ctx, userID, dID); err != nil {
		return nil, err
	}
	return map[string]interface{}{"status": status, "draft_id": draftID}, nil
}

// --- BankTransferProvider (NGN bank transfers via RampHub offramp) ---

func buildBankTransferProvider(c *Container) aicore.BankTransferProvider {
	if c.RampService == nil {
		return nil
	}
	return &bankTransferAdapter{ramp: c.RampService}
}

type bankTransferAdapter struct {
	ramp *rampsvc.Service
}

func (a *bankTransferAdapter) ListBanks(ctx context.Context) ([]map[string]interface{}, error) {
	banks, err := a.ramp.GetBanks(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]interface{}, 0, len(banks))
	for _, b := range banks {
		out = append(out, map[string]interface{}{
			"bank_code": b.BankCode,
			"bank_name": b.BankName,
		})
	}
	return out, nil
}

func (a *bankTransferAdapter) ResolveBankAccount(ctx context.Context, bankCode, accountNumber, bankName string) (map[string]interface{}, error) {
	resolved, err := a.ramp.ResolveBankAccount(ctx, bankCode, accountNumber, bankName)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"account_name":   resolved.AccountName,
		"account_number": resolved.AccountNumber,
		"bank_code":      resolved.BankCode,
	}, nil
}

func (a *bankTransferAdapter) CreateOfframp(ctx context.Context, userID uuid.UUID, bankCode, accountNumber, bankName, amount, currency, accountName string) (map[string]interface{}, error) {
	fiatAmount, err := decimal.NewFromString(amount)
	if err != nil {
		return nil, fmt.Errorf("invalid amount: %s", amount)
	}
	if currency == "" {
		currency = "NGN"
	}
	// expectedRate=0 tells the ramp service to fetch a live quote.
	result, err := a.ramp.CreateOfframp(ctx, userID, bankCode, accountNumber, bankName, fiatAmount.InexactFloat64(), currency, 0)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"status":         result.Status,
		"transaction_id": result.TransactionID,
		"provider":      result.Provider,
		"fiat_amount":   result.FiatAmount,
		"rate":          result.Rate,
		"rail_fee":      result.RailFee,
	}, nil
}

// --- CryptoSendProvider (USDC to external wallet via withdrawal service) ---

func buildCryptoSendProvider(c *Container) aicore.CryptoSendProvider {
	if c.WithdrawalService == nil || c.WalletRepo == nil {
		return nil
	}
	return &cryptoSendAdapter{
		withdrawal: c.WithdrawalService,
		walletRepo: c.WalletRepo,
	}
}

type cryptoSendAdapter struct {
	withdrawal *withdrawal.WithdrawalService
	walletRepo *repositories.WalletRepository
}

func (a *cryptoSendAdapter) SendCrypto(ctx context.Context, userID uuid.UUID, destinationAddress, amount, chain string) (map[string]interface{}, error) {
	amt, err := decimal.NewFromString(amount)
	if err != nil {
		return nil, fmt.Errorf("invalid amount: %s", amount)
	}
	if amt.LessThan(decimal.NewFromInt(1)) {
		return nil, fmt.Errorf("minimum crypto send is $1.00")
	}

	// Determine destination chain (default to Solana). Accept "solana"/"sol"
	// and "evm"/"eth"/"ethereum" as user-friendly aliases.
	destChain := strings.ToLower(strings.TrimSpace(chain))
	if destChain == "" || destChain == "solana" || destChain == "sol" {
		destChain = string(entities.WalletChainSolana)
	}
	if destChain == "evm" || destChain == "eth" || destChain == "ethereum" {
		destChain = string(entities.WalletChainEthereum)
	}

	// Look up the user's wallet — try the destination chain first, fall back to Solana.
	wallet, err := a.walletRepo.GetByUserAndChain(ctx, userID, entities.WalletChain(destChain))
	if err != nil {
		wallet, err = a.walletRepo.GetByUserAndChain(ctx, userID, entities.WalletChainSolana)
	}
	if err != nil {
		return nil, fmt.Errorf("no wallet found for user: %w", err)
	}
	if wallet.CircleWalletID == "" && strings.TrimSpace(wallet.BridgeWalletID) == "" {
		return nil, fmt.Errorf("withdrawal provider is not configured for this account")
	}

	req := &entities.InitiateCryptoWithdrawalRequest{
		UserID:              userID,
		Amount:              amt,
		Currency:            entities.WithdrawalCurrencyUSDC,
		DestinationAddress:  destinationAddress,
		DestinationChain:    destChain,
		SourceChain:         string(wallet.Chain),
		SourceAccount:       entities.WithdrawalSourceSpendingBalance,
		BridgeWalletID:      wallet.BridgeWalletID,
		CircleWalletID:      wallet.CircleWalletID,
		SourceWalletAddress: wallet.Address,
		Category:            "miriam_send",
		Narration:           "Sent via Miriam",
		IdempotencyKey:      fmt.Sprintf("miriam-crypto-%s-%s-%s", userID.String(), destinationAddress, amount),
	}

	resp, err := a.withdrawal.InitiateCryptoWithdrawal(ctx, req)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"status":        string(resp.Status),
		"withdrawal_id": resp.WithdrawalID.String(),
		"message":       resp.Message,
	}, nil
}
