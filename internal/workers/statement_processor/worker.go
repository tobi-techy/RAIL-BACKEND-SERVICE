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

const JobType = "process_statement"

type MemoryWriter interface {
	SaveFact(ctx context.Context, fact *entities.MiriamUserFact, supersedes *uuid.UUID) error
}

type Notifier interface {
	SendGenericNotification(ctx context.Context, userID uuid.UUID, title, message string) error
}

type Worker struct {
	repo     *repositories.BankStatementRepository
	memory   MemoryWriter
	notifier Notifier
	parser   *statement.TransactionParser
	logger   *zap.Logger
}

func NewWorker(repo *repositories.BankStatementRepository, memory MemoryWriter, notifier Notifier, parser *statement.TransactionParser, logger *zap.Logger) *Worker {
	return &Worker{repo: repo, memory: memory, notifier: notifier, parser: parser, logger: logger}
}

// Handler returns a jobqueue.JobHandler for the "process_statement" job type.
func (w *Worker) Handler() jobqueue.JobHandler {
	return func(ctx context.Context, job *jobqueue.Job) error {
		// Override the default 5min job timeout — statement processing needs up to 10min.
		// Use the job's context as the parent so cancellation propagates on shutdown.
		processCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()

		uploadIDStr, _ := job.Payload["upload_id"].(string)
		userIDStr, _ := job.Payload["user_id"].(string)
		bankName, _ := job.Payload["bank_name"].(string)

		uploadID, err := uuid.Parse(uploadIDStr)
		if err != nil {
			return fmt.Errorf("invalid upload_id: %w", err)
		}
		userID, err := uuid.Parse(userIDStr)
		if err != nil {
			return fmt.Errorf("invalid user_id: %w", err)
		}

		// Fetch upload record from DB (includes file_data, survives restarts)
		upload, err := w.repo.GetByIDWithData(processCtx, userID, uploadID)
		if err != nil {
			return fmt.Errorf("upload not found: %w", err)
		}
		if len(upload.FileData) == 0 {
			return fmt.Errorf("upload %s has no file data — statement may need to be re-uploaded", uploadID)
		}

		return w.process(processCtx, uploadID, userID, upload.FileData, bankName)
	}
}

func (w *Worker) process(ctx context.Context, uploadID, userID uuid.UUID, data []byte, bankName string) (retErr error) {
	// Recover from panics (e.g. pdfcpu on malformed PDFs)
	defer func() {
		if r := recover(); r != nil {
			errMsg := fmt.Sprintf("internal error processing statement: %v", r)
			w.repo.UpdateStatus(ctx, uploadID, entities.StatementStatusFailed, &errMsg)
			w.logger.Error("statement processor panic", zap.Any("panic", r), zap.String("upload_id", uploadID.String()))
			retErr = fmt.Errorf("panic: %v", r)
			metrics.RecordStatementProcessed("failed")
		}
	}()

	// Idempotency guard: atomically claim this upload as processing.
	// If another worker already claimed it or it's already completed, skip.
	claimed, err := w.repo.AtomicClaim(ctx, uploadID)
	if err != nil {
		return fmt.Errorf("atomic claim failed: %w", err)
	}
	if !claimed {
		w.logger.Info("upload already claimed or completed, skipping",
			zap.String("upload_id", uploadID.String()))
		return nil
	}

	// Validate file size
	if len(data) > 20*1024*1024 {
		errMsg := "File too large for processing"
		w.repo.UpdateStatus(ctx, uploadID, entities.StatementStatusFailed, &errMsg)
		metrics.RecordStatementProcessed("failed")
		return fmt.Errorf("file exceeds 20MB")
	}

	// Extract text
	result, err := statement.ExtractTextFromBytes(data)
	if err != nil {
		var errMsg string
		if strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), "encrypt") {
			errMsg = "This PDF is password-protected. Please upload a non-password-protected version."
		} else {
			errMsg = fmt.Sprintf("PDF extraction failed: %v", err)
		}
		w.repo.UpdateStatus(ctx, uploadID, entities.StatementStatusFailed, &errMsg)
		metrics.RecordStatementProcessed("failed")
		return err
	}

	if result.IsScanned {
		errMsg := "Scanned PDF detected — image-based statements are not yet supported. Please upload a digital/text-based statement."
		w.repo.UpdateStatus(ctx, uploadID, entities.StatementStatusFailed, &errMsg)
		metrics.RecordStatementProcessed("failed")
		return fmt.Errorf("scanned PDF not supported")
	}

	// Check for garbled text (bad encoding, custom fonts)
	if statement.IsGarbageText(result.Text) {
		errMsg := "Could not read this PDF — it may use a custom font or encoding. Try exporting the statement as a different PDF or contact your bank for a digital copy."
		w.repo.UpdateStatus(ctx, uploadID, entities.StatementStatusFailed, &errMsg)
		metrics.RecordStatementProcessed("failed")
		return fmt.Errorf("garbage text detected in PDF")
	}

	// Parse transactions via LLM (with timing for cost tracking)
	// For large statements (>10 pages), parse in chunks to avoid context window limits
	parseStart := time.Now()
	var parsed *statement.ParseResult
	if result.PageCount > 10 && len(result.Pages) > 1 {
		parsed, err = w.parseChunked(ctx, result.Pages, bankName)
	} else {
		parsed, err = w.parser.Parse(ctx, result.Text, bankName)
	}
	parseDuration := time.Since(parseStart).Seconds()
	metrics.RecordStatementParseDuration(parseDuration)
	w.logger.Info("statement LLM parse completed",
		zap.Float64("duration_seconds", parseDuration),
		zap.Int("pages", result.PageCount),
	)
	if err != nil {
		// If context was cancelled but we have partial results from chunked parsing, save them
		if ctx.Err() != nil && parsed != nil && len(parsed.Transactions) > 0 {
			w.logger.Warn("context cancelled with partial results, saving what we have",
				zap.Int("partial_txns", len(parsed.Transactions)),
			)
			// Fall through to save partial results
		} else {
			errMsg := fmt.Sprintf("Transaction parsing failed: %v", err)
			w.repo.UpdateStatus(ctx, uploadID, entities.StatementStatusFailed, &errMsg)
			metrics.RecordStatementProcessed("failed")
			return err
		}
	}

	if len(parsed.Transactions) == 0 {
		errMsg := "No transactions found in the statement"
		w.repo.UpdateStatus(ctx, uploadID, entities.StatementStatusFailed, &errMsg)
		metrics.RecordStatementProcessed("failed")
		return fmt.Errorf("no transactions parsed")
	}

	// Cost guard: cap at 500 transactions to limit LLM token usage per statement
	if len(parsed.Transactions) > 500 {
		w.logger.Warn("truncating large statement to 500 transactions",
			zap.Int("original_count", len(parsed.Transactions)),
		)
		parsed.Transactions = parsed.Transactions[:500]
	}

	// Auto-detect bank name from LLM response if not provided
	if bankName == "" || bankName == "auto" || bankName == "unknown" {
		if parsed.BankName != "" {
			bankName = parsed.BankName
		} else {
			bankName = "Unknown Bank"
		}
		// Update the upload record with detected bank name
		w.repo.UpdateBankName(ctx, uploadID, bankName)
	}

	// Convert to entities
	txns, periodStart, periodEnd := w.parser.ToEntities(parsed, uploadID, userID)

	// Validate parsed transactions (dedup, bounds check, balance continuity)
	validation, validTxns := statement.ValidateTransactions(txns)
	if !validation.Valid {
		// Retry once if confidence is between 0.1 and 0.3 inclusive (borderline — Kimi randomness may have caused issues)
		if validation.Confidence >= 0.1 && validation.Confidence <= 0.3 {
			w.logger.Info("low confidence, retrying parse", zap.Float64("confidence", validation.Confidence))
			var retryParsed *statement.ParseResult
			if result.PageCount > 10 && len(result.Pages) > 1 {
				retryParsed, err = w.parseChunked(ctx, result.Pages, bankName)
			} else {
				retryParsed, err = w.parser.Parse(ctx, result.Text, bankName)
			}
			if err == nil && len(retryParsed.Transactions) > 0 {
				txns, periodStart, periodEnd = w.parser.ToEntities(retryParsed, uploadID, userID)
				validation, validTxns = statement.ValidateTransactions(txns)
			}
		}
		if !validation.Valid {
			var errMsg string
			if validation.ValidTransactions == 0 && validation.DroppedCount > 0 {
				errMsg = fmt.Sprintf("Found %d transactions but all had invalid amounts or dates. This bank's format may not be supported yet.", validation.TotalTransactions)
			} else if validation.BalanceMismatches > validation.ValidTransactions/2 {
				errMsg = "Too many balance inconsistencies detected — the statement may be corrupted or in an unsupported format."
			} else {
				errMsg = "Statement parsed but transactions failed validation — the PDF format may not be supported"
			}
			w.repo.UpdateStatus(ctx, uploadID, entities.StatementStatusFailed, &errMsg)
			metrics.RecordStatementProcessed("failed")
			return fmt.Errorf("validation failed: confidence=%.2f, valid=%d/%d", validation.Confidence, validation.ValidTransactions, validation.TotalTransactions)
		}
	}
	txns = validTxns
	passRate := float64(validation.ValidTransactions) / float64(validation.TotalTransactions)
	if validation.TotalTransactions > 0 {
		metrics.RecordStatementValidationPassRate(passRate)
	}
	w.logger.Info("statement validation passed",
		zap.Float64("confidence", validation.Confidence),
		zap.Int("valid", validation.ValidTransactions),
		zap.Int("dropped", validation.DroppedCount),
		zap.Int("balance_mismatches", validation.BalanceMismatches),
		zap.Float64("pass_rate", passRate),
	)

	// Store transactions (use ctx as parent so shutdown still propagates)
	saveCtx, saveCancel := context.WithTimeout(ctx, 30*time.Second)
	defer saveCancel()
	if err := w.repo.CreateTransactions(saveCtx, txns); err != nil {
		errMsg := "Failed to store transactions"
		w.repo.UpdateStatus(saveCtx, uploadID, entities.StatementStatusFailed, &errMsg)
		metrics.RecordStatementProcessed("failed")
		return err
	}

	// Build and store structured summary, then mark upload as completed
	pageCount := result.PageCount
	summaryJSON := w.buildStructuredSummary(txns, bankName, periodStart, periodEnd)
	if err := w.repo.UpdateCompleted(saveCtx, uploadID, len(txns), periodStart, periodEnd, &pageCount, &summaryJSON); err != nil {
		w.logger.Error("failed to update upload status", zap.Error(err))
	}

	// Generate Miriam memory facts with derived confidence
	w.generateFacts(saveCtx, userID, txns, bankName, validation.Confidence)

	metrics.RecordStatementProcessed("completed")
	metrics.RecordStatementTransactions(len(txns))

	w.logger.Info("statement processed",
		zap.String("upload_id", uploadID.String()),
		zap.String("bank", bankName),
		zap.Int("transactions", len(txns)),
	)

	// Send notification to user with structured summary
	w.sendCompletionNotification(saveCtx, userID, txns, bankName, periodStart, periodEnd)

	return nil
}

// sendCompletionNotification sends a structured summary to the user's notification feed.
func (w *Worker) sendCompletionNotification(ctx context.Context, userID uuid.UUID, txns []*entities.BankStatementTransaction, bankName string, periodStart, periodEnd *time.Time) {
	if w.notifier == nil || len(txns) == 0 {
		return
	}

	sum := computeTxnSummary(txns)

	// Build top categories string
	type sc struct {
		cat   string
		total decimal.Decimal
	}
	sorted := make([]sc, 0, len(sum.catSpend))
	for cat, total := range sum.catSpend {
		sorted = append(sorted, sc{cat, total})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].total.GreaterThan(sorted[j].total)
	})
	topCats := ""
	limit := 3
	if len(sorted) < limit {
		limit = len(sorted)
	}
	for i, entry := range sorted[:limit] {
		if i > 0 {
			topCats += ", "
		}
		topCats += fmt.Sprintf("%s: %s%s", entry.cat, sum.currency, entry.total.StringFixed(0))
	}

	// Build period string
	periodStr := ""
	if periodStart != nil && periodEnd != nil {
		periodStr = fmt.Sprintf(" from %s to %s", periodStart.Format("Jan 2, 2006"), periodEnd.Format("Jan 2, 2006"))
	}

	title := fmt.Sprintf("%s statement analyzed", bankName)
	message := fmt.Sprintf(
		"I reviewed your %s statement%s and found %d transactions. ",
		bankName, periodStr, len(txns),
	)
	if sum.totalCredits.IsPositive() {
		message += fmt.Sprintf("Income: %s%s. ", sum.currency, sum.totalCredits.StringFixed(0))
	}
	message += fmt.Sprintf("Spending: %s%s", sum.currency, sum.totalDebits.StringFixed(0))
	if topCats != "" {
		message += fmt.Sprintf(". Top categories: %s", topCats)
	}
	message += ". I've saved this data to give you better financial insights."

	if len(message) > 500 {
		message = message[:497] + "..."
	}

	if err := w.notifier.SendGenericNotification(ctx, userID, title, message); err != nil {
		w.logger.Warn("failed to send statement completion notification",
			zap.Error(err),
			zap.String("upload_id", txns[0].UploadID.String()),
		)
	}
}

// buildStructuredSummary computes stats and returns a JSON summary string
// that gets stored in the DB and returned by the status endpoint.
func (w *Worker) buildStructuredSummary(txns []*entities.BankStatementTransaction, bankName string, periodStart, periodEnd *time.Time) string {
	if len(txns) == 0 {
		return "{}"
	}

	sum := computeTxnSummary(txns)

	// Months covered
	months := 1
	if len(txns) > 1 {
		earliest := txns[0].TransactionDate
		latest := txns[0].TransactionDate
		for _, t := range txns[1:] {
			if t.TransactionDate.Before(earliest) {
				earliest = t.TransactionDate
			}
			if t.TransactionDate.After(latest) {
				latest = t.TransactionDate
			}
		}
		diff := latest.Sub(earliest)
		months = int(diff.Hours()/24/30) + 1
		if months < 1 {
			months = 1
		}
	}

	// Top categories
	type catEntry struct {
		Category string          `json:"category"`
		Total    decimal.Decimal `json:"total"`
	}
	sorted := make([]catEntry, 0, len(sum.catSpend))
	for cat, total := range sum.catSpend {
		sorted = append(sorted, catEntry{cat, total})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Total.GreaterThan(sorted[j].Total)
	})

	totalSpendFloat, _ := sum.totalDebits.Float64()
	topCats := make([]map[string]interface{}, 0, 3)
	limit := 3
	if len(sorted) < limit {
		limit = len(sorted)
	}
	for _, entry := range sorted[:limit] {
		pct := 0.0
		if totalSpendFloat > 0 {
			f, _ := entry.Total.Float64()
			pct = (f / totalSpendFloat) * 100
		}
		topCats = append(topCats, map[string]interface{}{
			"category":   entry.Category,
			"total":      entry.Total.StringFixed(2),
			"percentage": fmt.Sprintf("%.1f", pct),
		})
	}

	summary := map[string]interface{}{
		"bank_name":          bankName,
		"total_transactions": len(txns),
		"total_income":       sum.totalCredits.StringFixed(2),
		"total_spending":     sum.totalDebits.StringFixed(2),
		"currency":           sum.currency,
		"months_covered":     months,
		"top_categories":     topCats,
	}

	if sum.totalCredits.IsPositive() && months > 0 {
		monthlyIncome := sum.totalCredits.Div(decimal.NewFromInt(int64(months)))
		summary["monthly_avg_income"] = monthlyIncome.StringFixed(2)
	}
	if sum.totalDebits.IsPositive() && months > 0 {
		monthlySpend := sum.totalDebits.Div(decimal.NewFromInt(int64(months)))
		summary["monthly_avg_spending"] = monthlySpend.StringFixed(2)
	}
	if periodStart != nil {
		summary["period_start"] = periodStart.Format("2006-01-02")
	}
	if periodEnd != nil {
		summary["period_end"] = periodEnd.Format("2006-01-02")
	}

	b, err := json.Marshal(summary)
	if err != nil {
		w.logger.Warn("failed to marshal summary JSON", zap.Error(err))
		return "{}"
	}
	return string(b)
}

// parseChunked splits pages into chunks of ~10 pages, parses each, and merges results.
// Limited to 5 chunks (50 pages) to stay within the 5-minute job timeout.
// Deduplicates transactions across chunk boundaries using a date|amount|type|desc fingerprint.
func (w *Worker) parseChunked(ctx context.Context, pages []string, bankName string) (*statement.ParseResult, error) {
	const chunkSize = 10
	const maxChunks = 5
	merged := &statement.ParseResult{Currency: "NGN"}
	seen := make(map[string]bool)

	chunks := 0
	for i := 0; i < len(pages) && chunks < maxChunks; i += chunkSize {
		end := i + chunkSize
		if end > len(pages) {
			end = len(pages)
		}
		chunkText := strings.Join(pages[i:end], "\n---PAGE BREAK---\n")

		parsed, err := w.parser.Parse(ctx, chunkText, bankName)
		if err != nil {
			w.logger.Warn("chunk parse failed, skipping",
				zap.Int("chunk_start", i),
				zap.Error(err),
			)
			continue
		}
		chunks++

		// Merge results with dedup across chunks
		if parsed.BankName != "" && merged.BankName == "" {
			merged.BankName = parsed.BankName
		}
		if parsed.Currency != "" {
			merged.Currency = parsed.Currency
		}
		if parsed.PeriodStart != "" && (merged.PeriodStart == "" || parsed.PeriodStart < merged.PeriodStart) {
			merged.PeriodStart = parsed.PeriodStart
		}
		if parsed.PeriodEnd != "" && (merged.PeriodEnd == "" || parsed.PeriodEnd > merged.PeriodEnd) {
			merged.PeriodEnd = parsed.PeriodEnd
		}
		for _, txn := range parsed.Transactions {
			fingerprint := txn.Date + "|" + fmt.Sprintf("%.2f", txn.Amount) + "|" + txn.Type + "|" + txn.Description
			if seen[fingerprint] {
				continue
			}
			seen[fingerprint] = true
			merged.Transactions = append(merged.Transactions, txn)
		}
	}

	if len(merged.Transactions) == 0 {
		return nil, fmt.Errorf("no transactions parsed from any chunk")
	}
	return merged, nil
}

// txnSummary holds aggregated stats shared between notifications and the stored summary JSON.
type txnSummary struct {
	totalCredits decimal.Decimal
	totalDebits  decimal.Decimal
	catSpend     map[string]decimal.Decimal
	currency     string
}

func computeTxnSummary(txns []*entities.BankStatementTransaction) txnSummary {
	var s txnSummary
	s.catSpend = make(map[string]decimal.Decimal)
	for _, t := range txns {
		if t.Type == entities.StatementTxnTypeCredit {
			s.totalCredits = s.totalCredits.Add(t.Amount)
		} else {
			s.totalDebits = s.totalDebits.Add(t.Amount)
			s.catSpend[t.Category] = s.catSpend[t.Category].Add(t.Amount)
		}
	}
	if len(txns) > 0 {
		s.currency = txns[0].Currency
	}
	return s
}

func (w *Worker) generateFacts(ctx context.Context, userID uuid.UUID, txns []*entities.BankStatementTransaction, bankName string, validationConfidence float64) {
	if w.memory == nil || len(txns) == 0 {
		return
	}

	// Calculate spending by category
	categorySpend := make(map[string]decimal.Decimal)
	var totalIncome, totalSpend decimal.Decimal
	for _, t := range txns {
		if t.Type == entities.StatementTxnTypeDebit {
			categorySpend[t.Category] = categorySpend[t.Category].Add(t.Amount)
			totalSpend = totalSpend.Add(t.Amount)
		} else {
			totalIncome = totalIncome.Add(t.Amount)
		}
	}

	// Determine period
	months := 1
	if len(txns) > 1 {
		earliest := txns[0].TransactionDate
		latest := txns[0].TransactionDate
		for _, t := range txns[1:] {
			if t.TransactionDate.Before(earliest) {
				earliest = t.TransactionDate
			}
			if t.TransactionDate.After(latest) {
				latest = t.TransactionDate
			}
		}
		diff := latest.Sub(earliest)
		months = int(diff.Hours()/24/30) + 1
		if months < 1 {
			months = 1
		}
	}

	// Derive fact confidence from validation score, scaled to [0.5, 0.95] range
	factConfidence := 0.5 + (validationConfidence * 0.45)
	if factConfidence < 0.5 {
		factConfidence = 0.5
	}
	if factConfidence > 0.95 {
		factConfidence = 0.95
	}
	confidence := decimal.NewFromFloat(factConfidence)
	now := time.Now().UTC()

	// Overall spending fact
	if totalSpend.IsPositive() {
		monthlySpend := totalSpend.Div(decimal.NewFromInt(int64(months)))
		fact := &entities.MiriamUserFact{
			UserID:          userID,
			Category:        entities.FactCategoryExternalSpending,
			Fact:            fmt.Sprintf("From %s statement: average monthly spending is %s %s across %d months", bankName, txns[0].Currency, monthlySpend.StringFixed(0), months),
			Source:          entities.FactSourceBankStatement,
			Confidence:      confidence,
			FirstObservedAt: now,
			LastConfirmedAt: now,
		}
		w.memory.SaveFact(ctx, fact, nil)
	}

	// Income fact
	if totalIncome.IsPositive() {
		monthlyIncome := totalIncome.Div(decimal.NewFromInt(int64(months)))
		fact := &entities.MiriamUserFact{
			UserID:          userID,
			Category:        entities.FactCategoryExternalIncome,
			Fact:            fmt.Sprintf("From %s statement: average monthly income is %s %s", bankName, txns[0].Currency, monthlyIncome.StringFixed(0)),
			Source:          entities.FactSourceBankStatement,
			Confidence:      confidence,
			FirstObservedAt: now,
			LastConfirmedAt: now,
		}
		w.memory.SaveFact(ctx, fact, nil)
	}

	// Top 3 spending categories
	type catEntry struct {
		cat   string
		total decimal.Decimal
	}
	sorted := make([]catEntry, 0, len(categorySpend))
	for cat, total := range categorySpend {
		sorted = append(sorted, catEntry{cat, total})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].total.GreaterThan(sorted[j].total)
	})
	limit := 3
	if len(sorted) < limit {
		limit = len(sorted)
	}
	for _, entry := range sorted[:limit] {
		monthly := entry.total.Div(decimal.NewFromInt(int64(months)))
		fact := &entities.MiriamUserFact{
			UserID:          userID,
			Category:        entities.FactCategoryExternalSpending,
			Fact:            fmt.Sprintf("From %s: spends ~%s %s/month on %s", bankName, txns[0].Currency, monthly.StringFixed(0), entry.cat),
			Source:          entities.FactSourceBankStatement,
			Confidence:      confidence,
			FirstObservedAt: now,
			LastConfirmedAt: now,
		}
		w.memory.SaveFact(ctx, fact, nil)
	}
}
