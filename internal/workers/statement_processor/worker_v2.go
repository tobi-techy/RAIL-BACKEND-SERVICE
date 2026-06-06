package statement_processor

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/statement"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/rail-service/rail_service/pkg/jobqueue"
	"github.com/rail-service/rail_service/pkg/metrics"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// ConversationWriter persists messages into the user's Miriam conversation.
type ConversationWriter interface {
	GetOrCreateConversation(ctx context.Context, userID uuid.UUID) (uuid.UUID, error)
	CreateMessage(ctx context.Context, msg *entities.AIMessage) error
}

// WorkerV2 uses the improved multi-strategy pipeline with S3 storage and LLM failover.
type WorkerV2 struct {
	repo         *repositories.BankStatementRepository
	pipeline     *statement.Pipeline
	fileStore    statement.FileStore
	memory       MemoryWriter
	supermemory  SupermemoryWriter
	notifier     Notifier
	conversations ConversationWriter
	reporter     statement.ProgressReporter
	logger       *zap.Logger
}

// SupermemoryWriter ingests structured conversations into Supermemory for long-term recall.
type SupermemoryWriter interface {
	IngestConversation(ctx context.Context, userID string, messages []SupermemoryMsg) error
	CreateMemories(ctx context.Context, containerTag string, memories []SupermemoryMemory) error
}

// SupermemoryMsg is a single conversation turn for Supermemory ingestion.
type SupermemoryMsg struct {
	Role    string
	Content string
}

// SupermemoryMemory is a single fact to store directly.
type SupermemoryMemory struct {
	Content   string
	Metadata  map[string]string
	EventDate string // YYYY-MM-DD
}

func NewWorkerV2(
	repo *repositories.BankStatementRepository,
	pipeline *statement.Pipeline,
	fileStore statement.FileStore,
	memory MemoryWriter,
	supermemory SupermemoryWriter,
	notifier Notifier,
	conversations ConversationWriter,
	reporter statement.ProgressReporter,
	logger *zap.Logger,
) *WorkerV2 {
	return &WorkerV2{
		repo: repo, pipeline: pipeline, fileStore: fileStore,
		memory: memory, supermemory: supermemory, notifier: notifier,
		conversations: conversations, reporter: reporter, logger: logger,
	}
}

// HandlerV2 returns a jobqueue.JobHandler for "process_statement" jobs with version=v2.
func (w *WorkerV2) HandlerV2() jobqueue.JobHandler {
	return func(ctx context.Context, job *jobqueue.Job) error {
		processCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		defer cancel()

		uploadIDStr, _ := job.Payload["upload_id"].(string)
		userIDStr, _ := job.Payload["user_id"].(string)
		bankName, _ := job.Payload["bank_name"].(string)
		contentType, _ := job.Payload["content_type"].(string)
		fileKey, _ := job.Payload["file_key"].(string)

		uploadID, err := uuid.Parse(uploadIDStr)
		if err != nil {
			return fmt.Errorf("invalid upload_id: %w", err)
		}
		userID, err := uuid.Parse(userIDStr)
		if err != nil {
			return fmt.Errorf("invalid user_id: %w", err)
		}

		// Idempotency: atomically claim
		claimed, err := w.repo.AtomicClaim(processCtx, uploadID)
		if err != nil {
			return fmt.Errorf("atomic claim: %w", err)
		}
		if !claimed {
			return nil
		}

		// Load file data from S3 (or DB fallback)
		var data []byte
		if w.fileStore != nil && fileKey != "" {
			data, err = w.fileStore.Download(processCtx, fileKey)
			if err != nil {
				return w.fail(processCtx, uploadID, fmt.Sprintf("Failed to retrieve file: %v", err))
			}
		} else {
			upload, err := w.repo.GetByIDWithData(processCtx, userID, uploadID)
			if err != nil {
				return fmt.Errorf("upload not found: %w", err)
			}
			data = upload.FileData
			if contentType == "" {
				contentType = "application/pdf"
			}
		}

		if len(data) == 0 {
			return w.fail(processCtx, uploadID, "File is empty — please re-upload")
		}
		if contentType == "" {
			contentType = "application/pdf"
		}

		return w.process(processCtx, uploadID, userID, data, contentType, bankName)
	}
}

func (w *WorkerV2) process(ctx context.Context, uploadID, userID uuid.UUID, data []byte, contentType, bankName string) (retErr error) {
	defer func() {
		if r := recover(); r != nil {
			errMsg := fmt.Sprintf("internal error: %v", r)
			w.repo.UpdateStatus(ctx, uploadID, entities.StatementStatusFailed, &errMsg)
			w.logger.Error("worker panic", zap.Any("panic", r), zap.String("upload_id", uploadID.String()))
			retErr = fmt.Errorf("panic: %v", r)
			metrics.RecordStatementProcessed("failed")
		}
	}()

	start := time.Now()

	// Run the multi-strategy pipeline
	result, err := w.pipeline.Process(ctx, uploadID, data, contentType, bankName)
	if err != nil {
		return w.fail(ctx, uploadID, err.Error())
	}

	if len(result.ParseResult.Transactions) == 0 {
		return w.fail(ctx, uploadID, "No transactions found in the document")
	}

	// Cap at 500 transactions
	if len(result.ParseResult.Transactions) > 500 {
		result.ParseResult.Transactions = result.ParseResult.Transactions[:500]
	}

	// Auto-detect bank name
	if bankName == "" || bankName == "unknown" {
		if result.ParseResult.BankName != "" {
			bankName = result.ParseResult.BankName
		} else {
			bankName = "Unknown Bank"
		}
		w.repo.UpdateBankName(ctx, uploadID, bankName)
	}

	// Report: Validating
	w.report(ctx, uploadID, statement.StageValidate, 0.0, "Validating transactions...")

	// Convert and validate
	parser := &statement.TransactionParser{}
	txns, periodStart, periodEnd := parser.ToEntities(result.ParseResult, uploadID, userID)
	validation, validTxns := statement.ValidateTransactions(txns)

	if !validation.Valid {
		return w.fail(ctx, uploadID, fmt.Sprintf(
			"Validation failed: %d/%d transactions valid (confidence: %.0f%%)",
			validation.ValidTransactions, validation.TotalTransactions, validation.Confidence*100,
		))
	}
	txns = validTxns

	w.report(ctx, uploadID, statement.StageValidate, 1.0, fmt.Sprintf(
		"%d transactions validated (%.0f%% confidence)", validation.ValidTransactions, validation.Confidence*100,
	))

	// Report: Storing
	w.report(ctx, uploadID, statement.StageStore, 0.0, "Saving transactions...")

	saveCtx, saveCancel := context.WithTimeout(ctx, 30*time.Second)
	defer saveCancel()
	if err := w.repo.CreateTransactions(saveCtx, txns); err != nil {
		return w.fail(ctx, uploadID, "Failed to store transactions")
	}

	pageCount := result.PageCount
	summaryJSON := buildSummaryJSON(txns, bankName, periodStart, periodEnd, string(result.Strategy), result.ParserUsed)
	w.repo.UpdateCompleted(saveCtx, uploadID, len(txns), periodStart, periodEnd, &pageCount, &summaryJSON)

	w.report(ctx, uploadID, statement.StageStore, 1.0, "Transactions saved")

	// Report: Enriching (Miriam memory facts + Supermemory)
	w.report(ctx, uploadID, statement.StageEnrich, 0.0, "Learning your financial patterns...")
	w.generateFacts(ctx, userID, txns, bankName, validation.Confidence)
	w.ingestToSupermemory(ctx, userID, txns, bankName, periodStart, periodEnd)
	w.detectTrends(ctx, userID, uploadID, txns, periodStart, periodEnd)
	budgetSuggestions := w.generateBudgetSuggestions(ctx, userID, txns)
	w.report(ctx, uploadID, statement.StageEnrich, 1.0, "Financial insights updated")

	// Post instant insights to user's Miriam chat
	w.postInstantInsightsWithBudgets(ctx, userID, txns, bankName, periodStart, periodEnd, budgetSuggestions)

	// Done
	w.report(ctx, uploadID, statement.StageComplete, 1.0, fmt.Sprintf(
		"Done! %d transactions from %s analyzed in %s", len(txns), bankName, time.Since(start).Round(time.Second),
	))

	metrics.RecordStatementProcessed("completed")
	metrics.RecordStatementTransactions(len(txns))
	metrics.RecordStatementParseDuration(time.Since(start).Seconds())

	w.logger.Info("v2 statement processed",
		zap.String("upload_id", uploadID.String()),
		zap.String("bank", bankName),
		zap.Int("transactions", len(txns)),
		zap.String("strategy", string(result.Strategy)),
		zap.String("parser", result.ParserUsed),
		zap.Duration("duration", time.Since(start)),
	)

	w.sendNotification(ctx, userID, txns, bankName, periodStart, periodEnd)
	return nil
}

func (w *WorkerV2) fail(ctx context.Context, uploadID uuid.UUID, msg string) error {
	w.repo.UpdateStatus(ctx, uploadID, entities.StatementStatusFailed, &msg)
	w.report(ctx, uploadID, statement.StageFailed, 0, msg)
	metrics.RecordStatementProcessed("failed")
	return fmt.Errorf("%s", msg)
}

func (w *WorkerV2) report(ctx context.Context, uploadID uuid.UUID, stage statement.ProcessingStage, progress float64, msg string) {
	if w.reporter == nil {
		return
	}
	w.reporter.Report(ctx, &statement.StageProgress{
		UploadID:  uploadID,
		Stage:     stage,
		Progress:  progress,
		Message:   msg,
		StartedAt: time.Now(),
	})
}

func (w *WorkerV2) generateFacts(ctx context.Context, userID uuid.UUID, txns []*entities.BankStatementTransaction, bankName string, confidence float64) {
	if w.memory == nil || len(txns) == 0 {
		return
	}

	var totalIncome, totalSpend decimal.Decimal
	catSpend := make(map[string]decimal.Decimal)
	for _, t := range txns {
		if t.Type == entities.StatementTxnTypeDebit {
			catSpend[t.Category] = catSpend[t.Category].Add(t.Amount)
			totalSpend = totalSpend.Add(t.Amount)
		} else {
			totalIncome = totalIncome.Add(t.Amount)
		}
	}

	months := computeMonths(txns)
	factConfidence := decimal.NewFromFloat(0.5 + (confidence * 0.45))
	now := time.Now().UTC()
	currency := txns[0].Currency

	if totalSpend.IsPositive() {
		monthly := totalSpend.Div(decimal.NewFromInt(int64(months)))
		w.memory.SaveFact(ctx, &entities.MiriamUserFact{
			UserID: userID, Category: entities.FactCategoryExternalSpending,
			Fact: fmt.Sprintf("From %s statement: average monthly spending is %s %s across %d months", bankName, currency, monthly.StringFixed(0), months),
			Source: entities.FactSourceBankStatement, Confidence: factConfidence, FirstObservedAt: now, LastConfirmedAt: now,
		}, nil)
	}
	if totalIncome.IsPositive() {
		monthly := totalIncome.Div(decimal.NewFromInt(int64(months)))
		w.memory.SaveFact(ctx, &entities.MiriamUserFact{
			UserID: userID, Category: entities.FactCategoryExternalIncome,
			Fact: fmt.Sprintf("From %s statement: average monthly income is %s %s", bankName, currency, monthly.StringFixed(0)),
			Source: entities.FactSourceBankStatement, Confidence: factConfidence, FirstObservedAt: now, LastConfirmedAt: now,
		}, nil)
	}

	// Top 3 categories
	type ce struct {
		cat   string
		total decimal.Decimal
	}
	sorted := make([]ce, 0, len(catSpend))
	for cat, total := range catSpend {
		sorted = append(sorted, ce{cat, total})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].total.GreaterThan(sorted[j].total) })
	limit := 3
	if len(sorted) < limit {
		limit = len(sorted)
	}
	for _, e := range sorted[:limit] {
		monthly := e.total.Div(decimal.NewFromInt(int64(months)))
		w.memory.SaveFact(ctx, &entities.MiriamUserFact{
			UserID: userID, Category: entities.FactCategoryExternalSpending,
			Fact: fmt.Sprintf("From %s: spends ~%s %s/month on %s", bankName, currency, monthly.StringFixed(0), e.cat),
			Source: entities.FactSourceBankStatement, Confidence: factConfidence, FirstObservedAt: now, LastConfirmedAt: now,
		}, nil)
	}
}

func (w *WorkerV2) sendNotification(ctx context.Context, userID uuid.UUID, txns []*entities.BankStatementTransaction, bankName string, periodStart, periodEnd *time.Time) {
	if w.notifier == nil || len(txns) == 0 {
		return
	}
	var totalCredits, totalDebits decimal.Decimal
	for _, t := range txns {
		if t.Type == entities.StatementTxnTypeCredit {
			totalCredits = totalCredits.Add(t.Amount)
		} else {
			totalDebits = totalDebits.Add(t.Amount)
		}
	}
	currency := txns[0].Currency
	periodStr := ""
	if periodStart != nil && periodEnd != nil {
		periodStr = fmt.Sprintf(" (%s – %s)", periodStart.Format("Jan 2"), periodEnd.Format("Jan 2, 2006"))
	}

	title := fmt.Sprintf("%s statement analyzed", bankName)
	msg := fmt.Sprintf("Found %d transactions%s. Income: %s%s, Spending: %s%s. Your financial insights have been updated.",
		len(txns), periodStr, currency, totalCredits.StringFixed(0), currency, totalDebits.StringFixed(0))

	w.notifier.SendGenericNotification(ctx, userID, title, msg)
}

func computeMonths(txns []*entities.BankStatementTransaction) int {
	if len(txns) < 2 {
		return 1
	}
	earliest, latest := txns[0].TransactionDate, txns[0].TransactionDate
	for _, t := range txns[1:] {
		if t.TransactionDate.Before(earliest) {
			earliest = t.TransactionDate
		}
		if t.TransactionDate.After(latest) {
			latest = t.TransactionDate
		}
	}
	months := int(latest.Sub(earliest).Hours()/24/30) + 1
	if months < 1 {
		return 1
	}
	return months
}

func buildSummaryJSON(txns []*entities.BankStatementTransaction, bankName string, periodStart, periodEnd *time.Time, strategy, parserUsed string) string {
	if len(txns) == 0 {
		return "{}"
	}
	var totalCredits, totalDebits decimal.Decimal
	catSpend := make(map[string]decimal.Decimal)
	for _, t := range txns {
		if t.Type == entities.StatementTxnTypeCredit {
			totalCredits = totalCredits.Add(t.Amount)
		} else {
			totalDebits = totalDebits.Add(t.Amount)
			catSpend[t.Category] = catSpend[t.Category].Add(t.Amount)
		}
	}

	months := computeMonths(txns)

	type catEntry struct {
		Category string `json:"category"`
		Total    string `json:"total"`
	}
	sorted := make([]catEntry, 0, len(catSpend))
	for cat, total := range catSpend {
		sorted = append(sorted, catEntry{cat, total.StringFixed(2)})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Total > sorted[j].Total })
	if len(sorted) > 5 {
		sorted = sorted[:5]
	}

	summary := map[string]interface{}{
		"bank_name":          bankName,
		"total_transactions": len(txns),
		"total_income":       totalCredits.StringFixed(2),
		"total_spending":     totalDebits.StringFixed(2),
		"currency":           txns[0].Currency,
		"months_covered":     months,
		"top_categories":     sorted,
		"extraction_strategy": strategy,
		"parser_used":        parserUsed,
	}
	if periodStart != nil {
		summary["period_start"] = periodStart.Format("2006-01-02")
	}
	if periodEnd != nil {
		summary["period_end"] = periodEnd.Format("2006-01-02")
	}

	b, _ := json.Marshal(summary)
	return string(b)
}

// StatementChartData is the chart payload the frontend renders for statement summaries.
type StatementChartData struct {
	Type     string   `json:"type"`
	Labels   []string `json:"labels"`
	Values   []string `json:"values"`
	Currency string   `json:"currency"`
}

func (w *WorkerV2) postInstantInsights(ctx context.Context, userID uuid.UUID, txns []*entities.BankStatementTransaction, bankName string, periodStart, periodEnd *time.Time) {
	if w.conversations == nil || len(txns) == 0 {
		return
	}

	currency := txns[0].Currency
	var totalIncome, totalSpend decimal.Decimal
	catSpend := make(map[string]decimal.Decimal)
	for _, t := range txns {
		if t.Type == entities.StatementTxnTypeDebit {
			totalSpend = totalSpend.Add(t.Amount)
			catSpend[t.Category] = catSpend[t.Category].Add(t.Amount)
		} else {
			totalIncome = totalIncome.Add(t.Amount)
		}
	}

	// Sort categories by spend descending
	type catEntry struct {
		name  string
		total decimal.Decimal
	}
	sorted := make([]catEntry, 0, len(catSpend))
	for cat, total := range catSpend {
		sorted = append(sorted, catEntry{cat, total})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].total.GreaterThan(sorted[j].total) })

	// Build text summary
	periodStr := ""
	if periodStart != nil && periodEnd != nil {
		periodStr = fmt.Sprintf("%s – %s", periodStart.Format("Jan 2"), periodEnd.Format("Jan 2, 2006"))
	}

	summary := fmt.Sprintf("📊 **%s Statement Analyzed**\n\n", bankName)
	if periodStr != "" {
		summary += fmt.Sprintf("Period: %s\n", periodStr)
	}
	summary += fmt.Sprintf("Transactions: %d\n", len(txns))
	summary += fmt.Sprintf("Total Income: %s %s\n", currency, totalIncome.StringFixed(0))
	summary += fmt.Sprintf("Total Spending: %s %s\n\n", currency, totalSpend.StringFixed(0))

	top := 5
	if len(sorted) < top {
		top = len(sorted)
	}
	if top > 0 {
		summary += "**Top categories:**\n"
		for _, e := range sorted[:top] {
			summary += fmt.Sprintf("• %s: %s %s\n", e.name, currency, e.total.StringFixed(0))
		}
	}

	// Build chart data (top 8 categories)
	chartLimit := 8
	if len(sorted) < chartLimit {
		chartLimit = len(sorted)
	}
	labels := make([]string, chartLimit)
	values := make([]string, chartLimit)
	for i, e := range sorted[:chartLimit] {
		labels[i] = e.name
		values[i] = e.total.StringFixed(0)
	}

	chartData := StatementChartData{
		Type:     "bar",
		Labels:   labels,
		Values:   values,
		Currency: currency,
	}

	card := entities.InsightCard{
		Type:     "statement_summary",
		Title:    fmt.Sprintf("%s Statement Summary", bankName),
		Subtitle: periodStr,
		Data:     chartData,
	}

	// Persist as assistant message in user's conversation
	convID, err := w.conversations.GetOrCreateConversation(ctx, userID)
	if err != nil {
		w.logger.Warn("failed to get/create conversation for instant insights", zap.Error(err), zap.String("user_id", userID.String()))
		return
	}

	metadata := map[string]interface{}{
		"cards": []entities.InsightCard{card},
	}

	if err := w.conversations.CreateMessage(ctx, &entities.AIMessage{
		ConversationID: convID,
		Role:           "assistant",
		Content:        summary,
		Model:          "system",
		Metadata:       metadata,
	}); err != nil {
		w.logger.Warn("failed to persist instant insights message", zap.Error(err), zap.String("user_id", userID.String()))
	}
}

// generateBudgetSuggestions computes suggested budgets per category from the current
// statement's transactions and stores them as Supermemory memories.
// Returns formatted suggestion lines for inclusion in instant insights.
func (w *WorkerV2) generateBudgetSuggestions(ctx context.Context, userID uuid.UUID, txns []*entities.BankStatementTransaction) []string {
	if w.supermemory == nil || len(txns) == 0 {
		return nil
	}

	currency := txns[0].Currency
	months := computeMonths(txns)
	monthsDec := decimal.NewFromInt(int64(months))
	threshold := decimal.NewFromInt(5000) // NGN 5,000/month minimum

	// Aggregate spending per category
	catSpend := make(map[string]decimal.Decimal)
	for _, t := range txns {
		if t.Type == entities.StatementTxnTypeDebit {
			catSpend[t.Category] = catSpend[t.Category].Add(t.Amount)
		}
	}

	var suggestions []string
	var memories []SupermemoryMemory
	buffer := decimal.NewFromFloat(1.10) // 10% buffer

	for cat, total := range catSpend {
		monthly := total.Div(monthsDec)
		if monthly.LessThan(threshold) {
			continue
		}
		ceiling := monthly.Mul(buffer).Round(0)
		content := fmt.Sprintf("Budget suggestion: %s spending averages %s %s/month. Suggested ceiling: %s %s/month.",
			cat, currency, monthly.Round(0).StringFixed(0), currency, ceiling.StringFixed(0))
		suggestions = append(suggestions, content)
		memories = append(memories, SupermemoryMemory{
			Content:   content,
			EventDate: time.Now().Format("2006-01-02"),
			Metadata:  map[string]string{"type": "budget_suggestion", "category": cat},
		})
	}

	if len(memories) > 0 {
		if err := w.supermemory.CreateMemories(ctx, userID.String(), memories); err != nil {
			w.logger.Warn("failed to store budget suggestions", zap.Error(err), zap.String("user_id", userID.String()))
		}
	}

	return suggestions
}

// postInstantInsightsWithBudgets posts the statement analysis insights plus budget suggestions.
func (w *WorkerV2) postInstantInsightsWithBudgets(ctx context.Context, userID uuid.UUID, txns []*entities.BankStatementTransaction, bankName string, periodStart, periodEnd *time.Time, budgetSuggestions []string) {
	if w.conversations == nil || len(txns) == 0 {
		return
	}

	currency := txns[0].Currency
	var totalIncome, totalSpend decimal.Decimal
	catSpend := make(map[string]decimal.Decimal)
	for _, t := range txns {
		if t.Type == entities.StatementTxnTypeDebit {
			totalSpend = totalSpend.Add(t.Amount)
			catSpend[t.Category] = catSpend[t.Category].Add(t.Amount)
		} else {
			totalIncome = totalIncome.Add(t.Amount)
		}
	}

	type catEntry struct {
		name  string
		total decimal.Decimal
	}
	sorted := make([]catEntry, 0, len(catSpend))
	for cat, total := range catSpend {
		sorted = append(sorted, catEntry{cat, total})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].total.GreaterThan(sorted[j].total) })

	periodStr := ""
	if periodStart != nil && periodEnd != nil {
		periodStr = fmt.Sprintf("%s – %s", periodStart.Format("Jan 2"), periodEnd.Format("Jan 2, 2006"))
	}

	summary := fmt.Sprintf("📊 **%s Statement Analyzed**\n\n", bankName)
	if periodStr != "" {
		summary += fmt.Sprintf("Period: %s\n", periodStr)
	}
	summary += fmt.Sprintf("Transactions: %d\n", len(txns))
	summary += fmt.Sprintf("Total Income: %s %s\n", currency, totalIncome.StringFixed(0))
	summary += fmt.Sprintf("Total Spending: %s %s\n\n", currency, totalSpend.StringFixed(0))

	top := 5
	if len(sorted) < top {
		top = len(sorted)
	}
	if top > 0 {
		summary += "**Top categories:**\n"
		for _, e := range sorted[:top] {
			summary += fmt.Sprintf("• %s: %s %s\n", e.name, currency, e.total.StringFixed(0))
		}
	}

	// Append budget suggestions
	if len(budgetSuggestions) > 0 {
		summary += "\n💡 **Suggested budgets based on your spending:**\n"
		for _, s := range budgetSuggestions {
			summary += fmt.Sprintf("• %s\n", s)
		}
		summary += "\nWant me to set any of these budgets for you?"
	}

	// Build chart data
	chartLimit := 8
	if len(sorted) < chartLimit {
		chartLimit = len(sorted)
	}
	labels := make([]string, chartLimit)
	values := make([]string, chartLimit)
	for i, e := range sorted[:chartLimit] {
		labels[i] = e.name
		values[i] = e.total.StringFixed(0)
	}

	chartData := StatementChartData{
		Type:     "bar",
		Labels:   labels,
		Values:   values,
		Currency: currency,
	}

	card := entities.InsightCard{
		Type:     "statement_summary",
		Title:    fmt.Sprintf("%s Statement Summary", bankName),
		Subtitle: periodStr,
		Data:     chartData,
	}

	convID, err := w.conversations.GetOrCreateConversation(ctx, userID)
	if err != nil {
		w.logger.Warn("failed to get/create conversation for instant insights", zap.Error(err), zap.String("user_id", userID.String()))
		return
	}

	metadata := map[string]interface{}{
		"cards": []entities.InsightCard{card},
	}

	if err := w.conversations.CreateMessage(ctx, &entities.AIMessage{
		ConversationID: convID,
		Role:           "assistant",
		Content:        summary,
		Model:          "system",
		Metadata:       metadata,
	}); err != nil {
		w.logger.Warn("failed to persist instant insights message", zap.Error(err), zap.String("user_id", userID.String()))
	} else {
		w.logger.Info("instant insights posted to conversation", zap.String("user_id", userID.String()), zap.String("conversation_id", convID.String()))
	}
}

// ingestToSupermemory sends granular per-transaction memories so Miriam can answer
// specific questions like "how much did I spend on airtime in March?"
func (w *WorkerV2) ingestToSupermemory(ctx context.Context, userID uuid.UUID, txns []*entities.BankStatementTransaction, bankName string, periodStart, periodEnd *time.Time) {
	if w.supermemory == nil || len(txns) == 0 {
		return
	}

	currency := txns[0].Currency
	containerTag := userID.String()

	// 1. Build per-transaction memories
	var memories []SupermemoryMemory
	for _, t := range txns {
		dir := "Received"
		if t.Type == entities.StatementTxnTypeDebit {
			dir = "Spent"
		}
		content := fmt.Sprintf("%s %s %s on %s — %s [%s]",
			dir, currency, t.Amount.StringFixed(0),
			t.TransactionDate.Format("2006-01-02"),
			t.Description, t.Category)
		memories = append(memories, SupermemoryMemory{
			Content:   content,
			EventDate: t.TransactionDate.Format("2006-01-02"),
			Metadata:  map[string]string{"type": "transaction", "category": t.Category, "bank": bankName},
		})
	}

	// 2. Build monthly summaries
	type monthData struct {
		income  decimal.Decimal
		spend   decimal.Decimal
		catMap  map[string]decimal.Decimal
		count   int
	}
	byMonth := make(map[string]*monthData)
	for _, t := range txns {
		key := t.TransactionDate.Format("2006-01")
		md, ok := byMonth[key]
		if !ok {
			md = &monthData{catMap: make(map[string]decimal.Decimal)}
			byMonth[key] = md
		}
		md.count++
		if t.Type == entities.StatementTxnTypeDebit {
			md.spend = md.spend.Add(t.Amount)
			md.catMap[t.Category] = md.catMap[t.Category].Add(t.Amount)
		} else {
			md.income = md.income.Add(t.Amount)
		}
	}

	for month, md := range byMonth {
		// Monthly overview
		content := fmt.Sprintf("Monthly summary %s: income %s %s, spending %s %s, %d transactions.",
			month, currency, md.income.StringFixed(0), currency, md.spend.StringFixed(0), md.count)
		memories = append(memories, SupermemoryMemory{
			Content:   content,
			EventDate: month + "-01",
			Metadata:  map[string]string{"type": "monthly_summary", "bank": bankName},
		})

		// Per-category monthly
		for cat, total := range md.catMap {
			catContent := fmt.Sprintf("Spent %s %s on %s in %s.", currency, total.StringFixed(0), cat, month)
			memories = append(memories, SupermemoryMemory{
				Content:   catContent,
				EventDate: month + "-01",
				Metadata:  map[string]string{"type": "category_monthly", "category": cat, "bank": bankName},
			})
		}
	}

	// 3. Overall statement summary
	var totalCredits, totalDebits decimal.Decimal
	for _, t := range txns {
		if t.Type == entities.StatementTxnTypeDebit {
			totalDebits = totalDebits.Add(t.Amount)
		} else {
			totalCredits = totalCredits.Add(t.Amount)
		}
	}
	periodStr := ""
	if periodStart != nil && periodEnd != nil {
		periodStr = fmt.Sprintf("%s to %s", periodStart.Format("Jan 2006"), periodEnd.Format("Jan 2006"))
	}
	memories = append(memories, SupermemoryMemory{
		Content:  fmt.Sprintf("Bank statement from %s (%s): %d transactions, total income %s %s, total spending %s %s.", bankName, periodStr, len(txns), currency, totalCredits.StringFixed(0), currency, totalDebits.StringFixed(0)),
		Metadata: map[string]string{"type": "statement_overview", "bank": bankName},
	})

	// 4. Top recipients (debit transactions grouped by description)
	type recipientData struct {
		total       decimal.Decimal
		count       int
		firstMonth  time.Time
		lastMonth   time.Time
	}
	recipients := make(map[string]*recipientData)
	for _, t := range txns {
		if t.Type != entities.StatementTxnTypeDebit {
			continue
		}
		rd, ok := recipients[t.Description]
		if !ok {
			rd = &recipientData{firstMonth: t.TransactionDate, lastMonth: t.TransactionDate}
			recipients[t.Description] = rd
		}
		rd.total = rd.total.Add(t.Amount)
		rd.count++
		if t.TransactionDate.Before(rd.firstMonth) {
			rd.firstMonth = t.TransactionDate
		}
		if t.TransactionDate.After(rd.lastMonth) {
			rd.lastMonth = t.TransactionDate
		}
	}
	type recipientEntry struct {
		name string
		data *recipientData
	}
	recipientList := make([]recipientEntry, 0, len(recipients))
	for name, data := range recipients {
		recipientList = append(recipientList, recipientEntry{name, data})
	}
	sort.Slice(recipientList, func(i, j int) bool {
		return recipientList[i].data.total.GreaterThan(recipientList[j].data.total)
	})
	topN := 10
	if len(recipientList) < topN {
		topN = len(recipientList)
	}
	for _, r := range recipientList[:topN] {
		dateRange := r.data.firstMonth.Format("Jan 2006")
		if r.data.firstMonth.Format("2006-01") != r.data.lastMonth.Format("2006-01") {
			dateRange = fmt.Sprintf("%s-%s", r.data.firstMonth.Format("Jan"), r.data.lastMonth.Format("Jan 2006"))
		}
		content := fmt.Sprintf("Top recipient: %s — sent %s %s across %d transactions (%s)",
			r.name, currency, r.data.total.StringFixed(0), r.data.count, dateRange)
		memories = append(memories, SupermemoryMemory{
			Content:  content,
			Metadata: map[string]string{"type": "top_recipient", "bank": bankName},
		})
	}

	// 5. Large transactions (over 5000 in local currency)
	largeThreshold := decimal.NewFromInt(5000)
	for _, t := range txns {
		if t.Type == entities.StatementTxnTypeDebit && t.Amount.GreaterThan(largeThreshold) {
			content := fmt.Sprintf("Large expense: %s %s to %s on %s [%s]",
				currency, t.Amount.StringFixed(0), t.Description, t.TransactionDate.Format("2006-01-02"), t.Category)
			memories = append(memories, SupermemoryMemory{
				Content:   content,
				EventDate: t.TransactionDate.Format("2006-01-02"),
				Metadata:  map[string]string{"type": "large_transaction", "category": t.Category, "bank": bankName},
			})
		}
	}

	// 6. Month-over-month comparisons
	sortedMonths := make([]string, 0, len(byMonth))
	for m := range byMonth {
		sortedMonths = append(sortedMonths, m)
	}
	sort.Strings(sortedMonths)
	for i := 1; i < len(sortedMonths); i++ {
		prev := byMonth[sortedMonths[i-1]]
		curr := byMonth[sortedMonths[i]]
		if prev.spend.IsZero() {
			continue
		}
		diff := curr.spend.Sub(prev.spend)
		pct := diff.Div(prev.spend).Mul(decimal.NewFromInt(100))
		direction := "increased"
		if diff.IsNegative() {
			direction = "decreased"
			diff = diff.Abs()
			pct = pct.Abs()
		}
		prevT, _ := time.Parse("2006-01", sortedMonths[i-1])
		currT, _ := time.Parse("2006-01", sortedMonths[i])
		// Category changes
		var catChanges []string
		for cat, currTotal := range curr.catMap {
			if prevTotal, ok := prev.catMap[cat]; ok && !prevTotal.IsZero() {
				catPct := currTotal.Sub(prevTotal).Div(prevTotal).Mul(decimal.NewFromInt(100))
				if catPct.Abs().GreaterThan(decimal.NewFromInt(10)) {
					dir := "up"
					if catPct.IsNegative() {
						dir = "down"
					}
					catChanges = append(catChanges, fmt.Sprintf("%s %s %s%%", cat, dir, catPct.Abs().StringFixed(0)))
				}
			}
		}
		content := fmt.Sprintf("%s vs %s: spending %s by %s %s (%s%%).",
			currT.Format("January 2006"), prevT.Format("January 2006"), direction, currency, diff.StringFixed(0), pct.StringFixed(0))
		if len(catChanges) > 0 {
			limit := 3
			if len(catChanges) < limit {
				limit = len(catChanges)
			}
			content += " " + fmt.Sprintf("Notable: %s.", joinStrings(catChanges[:limit]))
		}
		memories = append(memories, SupermemoryMemory{
			Content:   content,
			EventDate: sortedMonths[i] + "-01",
			Metadata:  map[string]string{"type": "month_comparison", "bank": bankName},
		})
	}

	// 7. Income sources (credit transactions grouped by description)
	type incomeSourceData struct {
		total      decimal.Decimal
		count      int
		days       []int
		months     []string
	}
	incomeSources := make(map[string]*incomeSourceData)
	for _, t := range txns {
		if t.Type != entities.StatementTxnTypeCredit {
			continue
		}
		isd, ok := incomeSources[t.Description]
		if !ok {
			isd = &incomeSourceData{}
			incomeSources[t.Description] = isd
		}
		isd.total = isd.total.Add(t.Amount)
		isd.count++
		isd.days = append(isd.days, t.TransactionDate.Day())
		isd.months = append(isd.months, t.TransactionDate.Format("2006-01"))
	}
	for name, isd := range incomeSources {
		uniqueMonths := make(map[string]bool)
		for _, m := range isd.months {
			uniqueMonths[m] = true
		}
		monthCount := len(uniqueMonths)
		avgPerMonth := isd.total.Div(decimal.NewFromInt(int64(monthCount)))
		content := fmt.Sprintf("Income source: %s — %s %s/month, received %d times",
			name, currency, avgPerMonth.StringFixed(0), isd.count)
		if isd.count >= 2 && monthCount >= 2 {
			// Detect recurring pattern by checking if day-of-month is consistent
			avgDay := 0
			for _, d := range isd.days {
				avgDay += d
			}
			avgDay /= len(isd.days)
			content += fmt.Sprintf(", typically around the %s", ordinal(avgDay))
		}
		memories = append(memories, SupermemoryMemory{
			Content:  content,
			Metadata: map[string]string{"type": "income_source", "bank": bankName},
		})
	}

	// 8. Spending frequency patterns (debit transactions grouped by category)
	type freqData struct {
		total    decimal.Decimal
		count    int
		weekdays int
		weekends int
	}
	catFreq := make(map[string]*freqData)
	for _, t := range txns {
		if t.Type != entities.StatementTxnTypeDebit {
			continue
		}
		fd, ok := catFreq[t.Category]
		if !ok {
			fd = &freqData{}
			catFreq[t.Category] = fd
		}
		fd.total = fd.total.Add(t.Amount)
		fd.count++
		if t.TransactionDate.Weekday() == time.Saturday || t.TransactionDate.Weekday() == time.Sunday {
			fd.weekends++
		} else {
			fd.weekdays++
		}
	}
	for cat, fd := range catFreq {
		if fd.count < 3 {
			continue
		}
		avg := fd.total.Div(decimal.NewFromInt(int64(fd.count)))
		timing := "evenly spread"
		if fd.weekdays > 0 && fd.weekends > 0 {
			wdPct := float64(fd.weekdays) / float64(fd.count)
			if wdPct > 0.75 {
				timing = "mostly on weekdays"
			} else if wdPct < 0.35 {
				timing = "mostly on weekends"
			}
		}
		content := fmt.Sprintf("%s: %d transactions averaging %s %s each, %s",
			cat, fd.count, currency, avg.StringFixed(0), timing)
		memories = append(memories, SupermemoryMemory{
			Content:  content,
			Metadata: map[string]string{"type": "spending_frequency", "category": cat, "bank": bankName},
		})
	}

	// 9. Send in batches (API limit: 100 per call)
	smCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	if err := w.supermemory.CreateMemories(smCtx, containerTag, memories); err != nil {
		w.logger.Warn("supermemory ingestion failed", zap.Error(err), zap.String("user_id", userID.String()))
	} else {
		w.logger.Info("statement data ingested to supermemory",
			zap.String("user_id", userID.String()),
			zap.Int("transactions", len(txns)),
			zap.Int("memories_created", len(memories)))
	}
}

// detectTrends compares current upload spending against the previous upload and
// stores trend insights as Supermemory memories.
func (w *WorkerV2) detectTrends(ctx context.Context, userID, uploadID uuid.UUID, txns []*entities.BankStatementTransaction, periodStart, periodEnd *time.Time) {
	if w.supermemory == nil || len(txns) == 0 {
		return
	}

	// Get spending-by-category from the previous completed upload
	prevSpend, err := w.repo.GetPreviousUploadSummary(ctx, userID, time.Now().UTC())
	if err != nil || len(prevSpend) == 0 {
		return // No previous data to compare against
	}

	// Build current spending-by-category
	currSpend := make(map[string]decimal.Decimal)
	var currTotal decimal.Decimal
	for _, t := range txns {
		if t.Type == entities.StatementTxnTypeDebit {
			currSpend[t.Category] = currSpend[t.Category].Add(t.Amount)
			currTotal = currTotal.Add(t.Amount)
		}
	}

	var prevTotal decimal.Decimal
	for _, v := range prevSpend {
		prevTotal = prevTotal.Add(v)
	}

	currency := txns[0].Currency
	currPeriod := formatPeriodLabel(periodStart, periodEnd)

	containerTag := userID.String()
	var memories []SupermemoryMemory

	// Detect per-category changes
	for cat, curr := range currSpend {
		prev, existed := prevSpend[cat]
		if !existed {
			// New category
			memories = append(memories, SupermemoryMemory{
				Content:  fmt.Sprintf("New spending category detected: %s (%s %s) first appeared in %s", cat, currency, curr.StringFixed(0), currPeriod),
				Metadata: map[string]string{"type": "trend", "category": cat},
			})
			continue
		}
		if prev.IsZero() {
			continue
		}
		change := curr.Sub(prev).Div(prev).Mul(decimal.NewFromInt(100))
		absChange := change.Abs()
		if absChange.LessThan(decimal.NewFromInt(10)) {
			continue // Skip insignificant changes
		}
		dir := "increased"
		if change.IsNegative() {
			dir = "decreased"
		}
		memories = append(memories, SupermemoryMemory{
			Content: fmt.Sprintf("Spending trend: %s %s %s%% from %s %s to %s %s in %s",
				cat, dir, absChange.StringFixed(0), currency, prev.StringFixed(0), currency, curr.StringFixed(0), currPeriod),
			Metadata: map[string]string{"type": "trend", "category": cat},
		})
	}

	// Total spending change
	if prevTotal.IsPositive() {
		totalChange := currTotal.Sub(prevTotal).Div(prevTotal).Mul(decimal.NewFromInt(100))
		dir := "increased"
		if totalChange.IsNegative() {
			dir = "decreased"
		}
		memories = append(memories, SupermemoryMemory{
			Content: fmt.Sprintf("Overall spending %s %s%% from %s %s to %s %s in %s",
				dir, totalChange.Abs().StringFixed(0), currency, prevTotal.StringFixed(0), currency, currTotal.StringFixed(0), currPeriod),
			Metadata: map[string]string{"type": "trend", "category": "total"},
		})
	}

	if len(memories) == 0 {
		return
	}

	if err := w.supermemory.CreateMemories(ctx, containerTag, memories); err != nil {
		w.logger.Warn("trend memory ingestion failed", zap.Error(err), zap.String("user_id", userID.String()))
	} else {
		w.logger.Info("trend insights stored", zap.String("user_id", userID.String()), zap.Int("trends", len(memories)))
	}
}

func formatPeriodLabel(start, end *time.Time) string {
	if start != nil && end != nil {
		return fmt.Sprintf("%s–%s", start.Format("Jan 2006"), end.Format("Jan 2006"))
	}
	return time.Now().Format("Jan 2006")
}

func joinStrings(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	result := ss[0]
	for i := 1; i < len(ss); i++ {
		if i == len(ss)-1 {
			result += " and " + ss[i]
		} else {
			result += ", " + ss[i]
		}
	}
	return result
}

func ordinal(n int) string {
	suffix := "th"
	switch {
	case n%100 == 11 || n%100 == 12 || n%100 == 13:
		// keep "th"
	case n%10 == 1:
		suffix = "st"
	case n%10 == 2:
		suffix = "nd"
	case n%10 == 3:
		suffix = "rd"
	}
	return fmt.Sprintf("%d%s", n, suffix)
}