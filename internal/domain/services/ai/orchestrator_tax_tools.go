package ai

import (
	"context"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
)

// Tool names for tax, email, and goals.
const (
	ToolGetTaxSummary   = "get_tax_summary"
	ToolGetTaxCalendar  = "get_tax_calendar"
	ToolSendReport      = "send_report"
	ToolGetSavingsGoals = "get_savings_goals"
)

// UserProfileProvider returns user profile data (country, email).
type UserProfileProvider interface {
	GetCountry(ctx context.Context, userID uuid.UUID) (string, error)
	GetEmail(ctx context.Context, userID uuid.UUID) (string, error)
}

// ReportEmailSender sends HTML emails.
type ReportEmailSender interface {
	SendReportEmail(ctx context.Context, to, subject, htmlBody string) error
}

// SetUserProfile sets the user profile provider.
// Deprecated: Use NewOrchestratorWithDeps instead.
func (o *Orchestrator) SetUserProfile(p UserProfileProvider) { o.userProfile = p }

// SetReportEmailSender sets the email sender for reports.
// Deprecated: Use NewOrchestratorWithDeps instead.
func (o *Orchestrator) SetReportEmailSender(s ReportEmailSender) { o.reportEmail = s }

// TaxAndReportTools returns tool definitions for tax, email, and goals.
func TaxAndReportTools(hasProfile, hasEmail bool) []infraai.Tool {
	var tools []infraai.Tool

	tools = append(tools, infraai.Tool{
		Name:        ToolGetTaxSummary,
		Description: "Get a tax-relevant financial summary for the user's tax year. Shows total deposits, yield earned, card spending, and transfers — formatted for the user's country. Use when user asks about taxes, tax reporting, or year-end summary.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"year": map[string]interface{}{"type": "integer", "description": "Tax year (e.g. 2025). Defaults to current year."},
			},
		},
	})

	tools = append(tools, infraai.Tool{
		Name:        ToolGetTaxCalendar,
		Description: "Get upcoming tax filing deadlines for the user's country. Use when user asks about tax deadlines, when to file, or tax calendar.",
		Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []string{}, "additionalProperties": false},
	})

	if hasEmail {
		tools = append(tools, infraai.Tool{
			Name:        ToolSendReport,
			Description: "Send a financial report to the user's email. Requires user confirmation. Use when user says 'email me my report', 'send my spending summary', or 'email my tax summary'.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"report_type": map[string]interface{}{
						"type": "string",
						"enum": []string{"spending", "tax_summary", "deposit_history"},
						"description": "Type of report to send",
					},
				},
				"required": []string{"report_type"},
			},
		})
	}

	tools = append(tools, infraai.Tool{
		Name:        ToolGetSavingsGoals,
		Description: "Get the user's savings goals and progress. Use when user asks about their goals, savings targets, or how close they are to a goal.",
		Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []string{}, "additionalProperties": false},
	})

	return tools
}

// executeTaxSummary builds a tax-relevant financial summary.
func (o *Orchestrator) executeTaxSummary(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	year := time.Now().Year()
	if y, ok := args["year"].(float64); ok && y > 2020 && y <= float64(year) {
		year = int(y)
	}

	// Determine user's country for tax year boundaries
	country := ""
	if o.userProfile != nil {
		country, _ = o.userProfile.GetCountry(ctx, userID)
	}

	// Tax year boundaries differ by country
	var start, end time.Time
	if country == "GB" {
		// UK tax year: April 6 – April 5
		start = time.Date(year, 4, 6, 0, 0, 0, 0, time.UTC)
		end = time.Date(year+1, 4, 5, 23, 59, 59, 0, time.UTC)
	} else {
		// Calendar year (US, Nigeria, most countries)
		start = time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
		end = time.Date(year, 12, 31, 23, 59, 59, 0, time.UTC)
	}

	result := map[string]interface{}{
		"tax_year": year,
		"country":  country,
		"period":   fmt.Sprintf("%s to %s", start.Format("Jan 2, 2006"), end.Format("Jan 2, 2006")),
	}

	// Yield earned (taxable)
	if o.yieldProvider != nil {
		snapshots, err := o.yieldProvider.GetSnapshotsInWindow(ctx, userID, start, end)
		if err == nil && len(snapshots) >= 2 {
			first := snapshots[0].Balance
			last := snapshots[len(snapshots)-1].Balance
			yieldEarned := last.Sub(first)
			if yieldEarned.LessThan(decimal.Zero) {
				yieldEarned = decimal.Zero
			}
			result["yield_earned"] = yieldEarned.StringFixed(2)
			result["yield_note"] = "USDB yield is likely taxable as interest/savings income"
		}
	}

	// Total spending
	if o.spending != nil {
		summary, err := o.spending.GetSummary(ctx, userID, start, end)
		if err == nil {
			result["total_spending"] = summary.Total.StringFixed(2)
			result["transaction_count"] = summary.TxCount
		}
	}

	// Balances
	if o.aggregateStats != nil {
		spend, _ := o.aggregateStats.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
		stash, _ := o.aggregateStats.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
		result["current_spend_balance"] = spend.StringFixed(2)
		result["current_stash_balance"] = stash.StringFixed(2)
	}

	// Deposits
	if o.depositHistory != nil {
		deposits, err := o.depositHistory.GetByUserID(ctx, userID, 100, 0)
		if err == nil {
			total := decimal.Zero
			count := 0
			for _, d := range deposits {
				if d.CreatedAt.After(start) && d.CreatedAt.Before(end) && d.Status == "confirmed" {
					total = total.Add(d.Amount)
					count++
				}
			}
			result["total_deposits"] = total.StringFixed(2)
			result["deposit_count"] = count
		}
	}

	// Potential deductions from receipts
	if o.receiptHistory != nil {
		receipts, err := o.receiptHistory.GetByUserIDInRange(ctx, userID, start, end, 50)
		if err == nil && len(receipts) > 0 {
			result["potential_deductions"] = categorizeDeductions(receipts)
		}
	}

	return result, nil
}

// Tax calendar deadlines by country.
var taxDeadlines = map[string][]struct {
	Date string
	Desc string
}{
	"NG": {
		{Date: "03-31", Desc: "Personal income tax filing deadline (FIRS)"},
		{Date: "06-30", Desc: "Company tax filing deadline"},
	},
	"GB": {
		{Date: "10-31", Desc: "Paper self-assessment deadline (HMRC)"},
		{Date: "01-31", Desc: "Online self-assessment + payment deadline"},
		{Date: "07-31", Desc: "Second payment on account due"},
	},
	"US": {
		{Date: "01-15", Desc: "Q4 estimated tax payment due (IRS)"},
		{Date: "04-15", Desc: "Federal tax filing deadline / Q1 estimated payment"},
		{Date: "06-15", Desc: "Q2 estimated tax payment due"},
		{Date: "09-15", Desc: "Q3 estimated tax payment due"},
		{Date: "10-15", Desc: "Extended filing deadline"},
	},
}

func (o *Orchestrator) executeTaxCalendar(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	country := ""
	if o.userProfile != nil {
		country, _ = o.userProfile.GetCountry(ctx, userID)
	}

	deadlines, ok := taxDeadlines[country]
	if !ok {
		return map[string]interface{}{
			"country":  country,
			"message":  "Tax calendar not available for your country yet. Check your local tax authority's website for filing deadlines.",
			"upcoming": []interface{}{},
		}, nil
	}

	now := time.Now()
	year := now.Year()
	upcoming := make([]map[string]interface{}, 0)

	for _, d := range deadlines {
		t, err := time.Parse("01-02", d.Date)
		if err != nil {
			continue
		}
		deadline := time.Date(year, t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		if deadline.Before(now) {
			deadline = deadline.AddDate(1, 0, 0)
		}
		daysUntil := int(deadline.Sub(now).Hours() / 24)
		upcoming = append(upcoming, map[string]interface{}{
			"date":       deadline.Format("January 2, 2006"),
			"days_until": daysUntil,
			"desc":       d.Desc,
			"urgent":     daysUntil <= 30,
		})
	}

	return map[string]interface{}{
		"country":  country,
		"upcoming": upcoming,
	}, nil
}

// executeSendReport creates a pending action to email a report.
func (o *Orchestrator) executeSendReport(ctx context.Context, userID, convID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	reportType, _ := args["report_type"].(string)
	if reportType == "" {
		return map[string]interface{}{"error": "report_type is required"}, nil
	}

	email := ""
	if o.userProfile != nil {
		email, _ = o.userProfile.GetEmail(ctx, userID)
	}
	if email == "" {
		return map[string]interface{}{"error": "No email address on file"}, nil
	}

	desc := fmt.Sprintf("Email %s report to %s", reportType, email)
	action := &entities.PendingAction{
		ID:             uuid.New().String(),
		ConversationID: convID,
		UserID:         userID,
		Action:         ToolSendReport,
		Description:    desc,
		Params:         map[string]interface{}{"report_type": reportType, "email": email},
		ExpiresAt:      time.Now().Add(pendingActionTTL),
		CreatedAt:      time.Now(),
	}
	if err := o.pending.Set(ctx, convID, action); err != nil {
		return nil, fmt.Errorf("store pending report action: %w", err)
	}

	return map[string]interface{}{
		"action_required": true,
		"pending_action":  action,
	}, nil
}

func (o *Orchestrator) executeGetSavingsGoals(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	// Get stash balance as progress toward any goal
	stash := decimal.Zero
	if o.aggregateStats != nil {
		stash, _ = o.aggregateStats.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	}

	result := map[string]interface{}{
		"stash_balance": stash.StringFixed(2),
	}

	if o.savingsGoalStore != nil {
		goal, err := o.savingsGoalStore.Get(ctx, userID)
		if err == nil && goal != nil {
			target, _ := decimal.NewFromString(goal.Target)
			progress := decimal.Zero
			if !target.IsZero() {
				progress = stash.Div(target).Mul(decimal.NewFromInt(100))
			}
			result["goal_name"] = goal.Name
			result["goal_target"] = goal.Target
			result["goal_deadline"] = goal.Deadline
			result["goal_created_at"] = goal.CreatedAt
			result["progress_pct"] = progress.StringFixed(1)
			result["remaining"] = target.Sub(stash).StringFixed(2)
			result["message"] = fmt.Sprintf("Your goal '%s' is %.1f%% complete.", goal.Name, progress.InexactFloat64())
			return result, nil
		}
	}

	result["message"] = "You don't have a savings goal set yet. Want me to help you create one?"
	return result, nil
}

// executeSendReportAction sends the actual email after user confirmation.
func (o *Orchestrator) executeSendReportAction(ctx context.Context, userID uuid.UUID, action *entities.PendingAction) error {
	if o.reportEmail == nil {
		return fmt.Errorf("email service not configured")
	}

	email, _ := action.Params["email"].(string)
	reportType, _ := action.Params["report_type"].(string)
	if email == "" || reportType == "" {
		return fmt.Errorf("missing email or report_type")
	}

	// Build report content
	summary, err := o.executeTaxSummary(ctx, userID, map[string]interface{}{})
	if err != nil {
		return fmt.Errorf("generate report: %w", err)
	}

	subject := "Your Rail Financial Report"
	switch reportType {
	case "tax_summary":
		subject = "Your Rail Tax Summary"
	case "spending":
		subject = "Your Rail Spending Report"
	case "deposit_history":
		subject = "Your Rail Deposit History"
	}

	html := buildReportHTML(subject, summaryToOrderedFields(summary))
	return o.reportEmail.SendReportEmail(ctx, email, subject, html)
}

// summaryToOrderedFields converts a tax summary map to deterministically ordered fields.
func summaryToOrderedFields(data map[string]interface{}) []reportField {
	orderedKeys := []string{
		"tax_year", "country", "period",
		"total_deposits", "deposit_count",
		"yield_earned", "yield_note",
		"total_spending", "transaction_count",
		"current_spend_balance", "current_stash_balance",
	}
	fields := make([]reportField, 0, len(orderedKeys))
	for _, k := range orderedKeys {
		if v, ok := data[k]; ok {
			fields = append(fields, reportField{Key: k, Value: v})
		}
	}
	return fields
}

// reportField is an ordered key-value pair for report HTML rendering.
type reportField struct {
	Key   string
	Value interface{}
}

func buildReportHTML(title string, fields []reportField) string {
	body := fmt.Sprintf(`<h2 style="color:#1A1A1A;font-family:sans-serif;">%s</h2><table style="width:100%%;border-collapse:collapse;font-family:sans-serif;">`, html.EscapeString(title))
	for _, f := range fields {
		body += fmt.Sprintf(`<tr><td style="padding:8px 0;color:#8C8C8C;border-bottom:1px solid #f0f0f0;">%s</td><td style="padding:8px 0;color:#1A1A1A;text-align:right;border-bottom:1px solid #f0f0f0;">%s</td></tr>`, html.EscapeString(f.Key), html.EscapeString(fmt.Sprintf("%v", f.Value)))
	}
	body += `</table><p style="color:#8C8C8C;font-size:12px;margin-top:24px;font-family:sans-serif;">This is an automated report from Rail. Not tax advice — consult a qualified professional.</p>`
	return body
}

// deductionCategory maps keywords to potential tax deduction categories.
var deductionCategories = map[string][]string{
	"Education":  {"tuition", "books", "course", "school", "university", "training", "udemy", "coursera"},
	"Health":     {"medical", "pharmacy", "hospital", "clinic", "doctor", "health", "dental"},
	"Transport":  {"fuel", "uber", "bolt", "taxi", "petrol", "diesel", "transport"},
	"Services":   {"software", "subscription", "professional", "consulting", "saas", "cloud"},
	"Equipment":  {"electronics", "office", "laptop", "computer", "printer", "supplies"},
}

func categorizeDeductions(receipts []*entities.ReceiptScan) []map[string]interface{} {
	type bucket struct {
		total decimal.Decimal
		count int
	}
	buckets := make(map[string]*bucket)

	for _, r := range receipts {
		cat := matchDeductionCategory(r.Merchant, r.Category)
		if cat == "" {
			continue
		}
		b, ok := buckets[cat]
		if !ok {
			b = &bucket{}
			buckets[cat] = b
		}
		b.total = b.total.Add(r.Amount)
		b.count++
	}

	results := make([]map[string]interface{}, 0, len(buckets))
	for cat, b := range buckets {
		results = append(results, map[string]interface{}{
			"category": cat,
			"total":    b.total.StringFixed(2),
			"count":    b.count,
			"note":     "May qualify as tax deduction — consult a tax professional",
		})
	}
	return results
}

func matchDeductionCategory(merchant, category string) string {
	lower := strings.ToLower(merchant + " " + category)
	for cat, keywords := range deductionCategories {
		for _, kw := range keywords {
			if strings.Contains(lower, kw) {
				return cat
			}
		}
	}
	return ""
}
