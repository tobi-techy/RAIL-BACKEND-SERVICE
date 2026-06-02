package statement_processor

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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

// WorkerV2 uses the improved multi-strategy pipeline with S3 storage and LLM failover.
type WorkerV2 struct {
	repo        *repositories.BankStatementRepository
	pipeline    *statement.Pipeline
	fileStore   statement.FileStore
	memory      MemoryWriter
	supermemory SupermemoryWriter
	notifier    Notifier
	reporter    statement.ProgressReporter
	logger      *zap.Logger
}

// SupermemoryWriter ingests structured conversations into Supermemory for long-term recall.
type SupermemoryWriter interface {
	IngestConversation(ctx context.Context, userID string, messages []SupermemoryMsg) error
}

// SupermemoryMsg is a single conversation turn for Supermemory ingestion.
type SupermemoryMsg struct {
	Role    string
	Content string
}

func NewWorkerV2(
	repo *repositories.BankStatementRepository,
	pipeline *statement.Pipeline,
	fileStore statement.FileStore,
	memory MemoryWriter,
	supermemory SupermemoryWriter,
	notifier Notifier,
	reporter statement.ProgressReporter,
	logger *zap.Logger,
) *WorkerV2 {
	return &WorkerV2{
		repo: repo, pipeline: pipeline, fileStore: fileStore,
		memory: memory, supermemory: supermemory, notifier: notifier, reporter: reporter, logger: logger,
	}
}

// HandlerV2 returns a jobqueue.JobHandler for "process_statement" jobs with version=v2.
func (w *WorkerV2) HandlerV2() jobqueue.JobHandler {
	return func(ctx context.Context, job *jobqueue.Job) error {
		processCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
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
	w.generateFacts(saveCtx, userID, txns, bankName, validation.Confidence)
	w.ingestToSupermemory(saveCtx, userID, txns, bankName, periodStart, periodEnd)
	w.report(ctx, uploadID, statement.StageEnrich, 1.0, "Financial insights updated")

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

	w.sendNotification(saveCtx, userID, txns, bankName, periodStart, periodEnd)
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

// ingestToSupermemory sends structured financial data as a conversation to Supermemory
// so Miriam can recall specific transactions, patterns, and financial behavior.
func (w *WorkerV2) ingestToSupermemory(ctx context.Context, userID uuid.UUID, txns []*entities.BankStatementTransaction, bankName string, periodStart, periodEnd *time.Time) {
	if w.supermemory == nil || len(txns) == 0 {
		return
	}

	// Build a rich structured summary that Supermemory can extract facts from
	var totalCredits, totalDebits decimal.Decimal
	catSpend := make(map[string]decimal.Decimal)
	var topExpenses []string // largest individual transactions

	type bigTxn struct {
		desc   string
		amount decimal.Decimal
		date   string
		cat    string
	}
	var bigDebits []bigTxn

	for _, t := range txns {
		if t.Type == entities.StatementTxnTypeDebit {
			totalDebits = totalDebits.Add(t.Amount)
			catSpend[t.Category] = catSpend[t.Category].Add(t.Amount)
			bigDebits = append(bigDebits, bigTxn{t.Description, t.Amount, t.TransactionDate.Format("2006-01-02"), t.Category})
		} else {
			totalCredits = totalCredits.Add(t.Amount)
		}
	}

	// Sort biggest debits and take top 10
	sort.Slice(bigDebits, func(i, j int) bool { return bigDebits[i].amount.GreaterThan(bigDebits[j].amount) })
	limit := 10
	if len(bigDebits) < limit {
		limit = len(bigDebits)
	}
	for _, d := range bigDebits[:limit] {
		topExpenses = append(topExpenses, fmt.Sprintf("- %s: %s %s on %s (%s)", d.desc, txns[0].Currency, d.amount.StringFixed(0), d.date, d.cat))
	}

	months := computeMonths(txns)
	currency := txns[0].Currency

	// Build spending breakdown
	type ce struct {
		cat   string
		total decimal.Decimal
	}
	sortedCats := make([]ce, 0, len(catSpend))
	for cat, total := range catSpend {
		sortedCats = append(sortedCats, ce{cat, total})
	}
	sort.Slice(sortedCats, func(i, j int) bool { return sortedCats[i].total.GreaterThan(sortedCats[j].total) })

	var catBreakdown string
	for _, c := range sortedCats {
		monthly := c.total.Div(decimal.NewFromInt(int64(months)))
		catBreakdown += fmt.Sprintf("- %s: %s %s total (%s/month)\n", c.cat, currency, c.total.StringFixed(0), monthly.StringFixed(0))
	}

	periodStr := ""
	if periodStart != nil && periodEnd != nil {
		periodStr = fmt.Sprintf("%s to %s", periodStart.Format("Jan 2006"), periodEnd.Format("Jan 2006"))
	}

	// Construct as a conversation so Supermemory extracts rich memories
	userMsg := fmt.Sprintf("Here is my %s bank statement for %s. It covers %d months with %d transactions. Total income: %s %s. Total spending: %s %s.",
		bankName, periodStr, months, len(txns), currency, totalCredits.StringFixed(0), currency, totalDebits.StringFixed(0))

	assistantMsg := fmt.Sprintf(`I've analyzed your %s statement (%s). Here's what I found:

**Income:** %s %s/month average
**Spending:** %s %s/month average

**Spending by category:**
%s
**Largest expenses:**
%s
I've saved all %d transactions to your financial memory. You can ask me about specific spending patterns, compare months, or get insights anytime.`,
		bankName, periodStr,
		currency, totalCredits.Div(decimal.NewFromInt(int64(months))).StringFixed(0),
		currency, totalDebits.Div(decimal.NewFromInt(int64(months))).StringFixed(0),
		catBreakdown,
		strings.Join(topExpenses, "\n"),
		len(txns),
	)

	messages := []SupermemoryMsg{
		{Role: "user", Content: userMsg},
		{Role: "assistant", Content: assistantMsg},
	}

	smCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	if err := w.supermemory.IngestConversation(smCtx, userID.String(), messages); err != nil {
		w.logger.Warn("supermemory ingestion failed", zap.Error(err), zap.String("user_id", userID.String()))
	} else {
		w.logger.Info("statement data ingested to supermemory", zap.String("user_id", userID.String()), zap.Int("transactions", len(txns)))
	}
}
