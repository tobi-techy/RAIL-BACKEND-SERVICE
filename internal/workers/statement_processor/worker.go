package statement_processor

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/statement"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/rail-service/rail_service/pkg/jobqueue"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

const JobType = "process_statement"

type MemoryWriter interface {
	SaveFact(ctx context.Context, fact *entities.MiriamUserFact, supersedes *uuid.UUID) error
}

type Worker struct {
	repo     *repositories.BankStatementRepository
	memory   MemoryWriter
	parser   *statement.TransactionParser
	logger   *zap.Logger
}

func NewWorker(repo *repositories.BankStatementRepository, memory MemoryWriter, parser *statement.TransactionParser, logger *zap.Logger) *Worker {
	return &Worker{repo: repo, memory: memory, parser: parser, logger: logger}
}

// Handler returns a jobqueue.JobHandler for the "process_statement" job type.
func (w *Worker) Handler() jobqueue.JobHandler {
	return func(ctx context.Context, job *jobqueue.Job) error {
		// Override the default 5min job timeout — statement processing needs up to 10min
		processCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		uploadIDStr, _ := job.Payload["upload_id"].(string)
		userIDStr, _ := job.Payload["user_id"].(string)
		filePath, _ := job.Payload["file_path"].(string)
		bankName, _ := job.Payload["bank_name"].(string)

		uploadID, err := uuid.Parse(uploadIDStr)
		if err != nil {
			return fmt.Errorf("invalid upload_id: %w", err)
		}
		userID, err := uuid.Parse(userIDStr)
		if err != nil {
			return fmt.Errorf("invalid user_id: %w", err)
		}
		if filePath == "" {
			return fmt.Errorf("missing file_path in job payload")
		}

		return w.process(processCtx, uploadID, userID, filePath, bankName)
	}
}

func (w *Worker) process(ctx context.Context, uploadID, userID uuid.UUID, filePath, bankName string) (retErr error) {
	// Recover from panics (e.g. pdfcpu on malformed PDFs)
	defer func() {
		if r := recover(); r != nil {
			errMsg := fmt.Sprintf("internal error processing statement: %v", r)
			w.repo.UpdateStatus(ctx, uploadID, entities.StatementStatusFailed, &errMsg)
			w.logger.Error("statement processor panic", zap.Any("panic", r), zap.String("upload_id", uploadID.String()))
			retErr = fmt.Errorf("panic: %v", r)
		}
	}()

	// Mark as processing
	w.repo.UpdateStatus(ctx, uploadID, entities.StatementStatusProcessing, nil)

	// Read PDF file
	data, err := os.ReadFile(filePath)
	if err != nil {
		errMsg := "failed to read PDF file"
		w.repo.UpdateStatus(ctx, uploadID, entities.StatementStatusFailed, &errMsg)
		return fmt.Errorf("read file: %w", err)
	}
	if len(data) > 20*1024*1024 {
		errMsg := "File too large for processing"
		w.repo.UpdateStatus(ctx, uploadID, entities.StatementStatusFailed, &errMsg)
		os.Remove(filePath)
		return fmt.Errorf("file exceeds 20MB")
	}
	// Clean up temp file after processing
	defer os.Remove(filePath)

	// Extract text
	result, err := statement.ExtractTextFromBytes(data)
	if err != nil {
		errMsg := fmt.Sprintf("PDF extraction failed: %v", err)
		w.repo.UpdateStatus(ctx, uploadID, entities.StatementStatusFailed, &errMsg)
		return err
	}

	if result.IsScanned {
		errMsg := "Scanned PDF detected — image-based statements are not yet supported. Please upload a digital/text-based statement."
		w.repo.UpdateStatus(ctx, uploadID, entities.StatementStatusFailed, &errMsg)
		return fmt.Errorf("scanned PDF not supported")
	}

	// Check for garbled text (bad encoding, custom fonts)
	if statement.IsGarbageText(result.Text) {
		errMsg := "Could not read this PDF — it may use a custom font or encoding. Try exporting the statement as a different PDF or contact your bank for a digital copy."
		w.repo.UpdateStatus(ctx, uploadID, entities.StatementStatusFailed, &errMsg)
		return fmt.Errorf("garbage text detected in PDF")
	}

	// Parse transactions via LLM
	// For large statements (>10 pages), parse in chunks to avoid context window limits
	var parsed *statement.ParseResult
	if result.PageCount > 10 && len(result.Pages) > 1 {
		parsed, err = w.parseChunked(ctx, result.Pages, bankName)
	} else {
		parsed, err = w.parser.Parse(ctx, result.Text, bankName)
	}
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
			return err
		}
	}

	if len(parsed.Transactions) == 0 {
		errMsg := "No transactions found in the statement"
		w.repo.UpdateStatus(ctx, uploadID, entities.StatementStatusFailed, &errMsg)
		return fmt.Errorf("no transactions parsed")
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
		// Retry once if confidence is between 0.1 and 0.3 (borderline — Kimi randomness may have caused issues)
		if validation.Confidence >= 0.1 && validation.Confidence < 0.3 {
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
			return fmt.Errorf("validation failed: confidence=%.2f, valid=%d/%d", validation.Confidence, validation.ValidTransactions, validation.TotalTransactions)
		}
	}
	txns = validTxns
	w.logger.Info("statement validation passed",
		zap.Float64("confidence", validation.Confidence),
		zap.Int("valid", validation.ValidTransactions),
		zap.Int("dropped", validation.DroppedCount),
		zap.Int("balance_mismatches", validation.BalanceMismatches),
	)

	// Store transactions (use background context in case original was cancelled)
	saveCtx, saveCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer saveCancel()
	if err := w.repo.CreateTransactions(saveCtx, txns); err != nil {
		errMsg := "Failed to store transactions"
		w.repo.UpdateStatus(saveCtx, uploadID, entities.StatementStatusFailed, &errMsg)
		return err
	}

	// Update upload as completed
	pageCount := result.PageCount
	if err := w.repo.UpdateCompleted(saveCtx, uploadID, len(txns), periodStart, periodEnd, &pageCount); err != nil {
		w.logger.Error("failed to update upload status", zap.Error(err))
	}

	// Generate Miriam memory facts
	w.generateFacts(saveCtx, userID, txns, bankName)

	w.logger.Info("statement processed",
		zap.String("upload_id", uploadID.String()),
		zap.String("bank", bankName),
		zap.Int("transactions", len(txns)),
	)
	return nil
}


// parseChunked splits pages into chunks of ~10 pages, parses each, and merges results.
// Limited to 5 chunks (50 pages) to stay within the 5-minute job timeout.
func (w *Worker) parseChunked(ctx context.Context, pages []string, bankName string) (*statement.ParseResult, error) {
	const chunkSize = 10
	const maxChunks = 5
	merged := &statement.ParseResult{Currency: "NGN"}

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

		// Merge results
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
		merged.Transactions = append(merged.Transactions, parsed.Transactions...)
	}

	if len(merged.Transactions) == 0 {
		return nil, fmt.Errorf("no transactions parsed from any chunk")
	}
	return merged, nil
}

func (w *Worker) generateFacts(ctx context.Context, userID uuid.UUID, txns []*entities.BankStatementTransaction, bankName string) {
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

	// Store top spending categories as facts
	confidence := decimal.NewFromFloat(0.85)
	now := time.Now().UTC()

	// Overall spending fact
	if totalSpend.IsPositive() {
		monthlySpend := totalSpend.Div(decimal.NewFromInt(int64(months)))
		fact := &entities.MiriamUserFact{
			UserID:     userID,
			Category:   entities.FactCategoryExternalSpending,
			Fact:       fmt.Sprintf("From %s statement: average monthly spending is %s %s across %d months", bankName, txns[0].Currency, monthlySpend.StringFixed(0), months),
			Source:     entities.FactSourceBankStatement,
			Confidence: confidence,
			FirstObservedAt: now,
			LastConfirmedAt: now,
		}
		w.memory.SaveFact(ctx, fact, nil)
	}

	// Income fact
	if totalIncome.IsPositive() {
		monthlyIncome := totalIncome.Div(decimal.NewFromInt(int64(months)))
		fact := &entities.MiriamUserFact{
			UserID:     userID,
			Category:   entities.FactCategoryExternalIncome,
			Fact:       fmt.Sprintf("From %s statement: average monthly income is %s %s", bankName, txns[0].Currency, monthlyIncome.StringFixed(0)),
			Source:     entities.FactSourceBankStatement,
			Confidence: confidence,
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
	var sorted []catEntry
	for cat, total := range categorySpend {
		sorted = append(sorted, catEntry{cat, total})
	}
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].total.GreaterThan(sorted[i].total) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	limit := 3
	if len(sorted) < limit {
		limit = len(sorted)
	}
	for _, entry := range sorted[:limit] {
		monthly := entry.total.Div(decimal.NewFromInt(int64(months)))
		fact := &entities.MiriamUserFact{
			UserID:     userID,
			Category:   entities.FactCategoryExternalSpending,
			Fact:       fmt.Sprintf("From %s: spends ~%s %s/month on %s", bankName, txns[0].Currency, monthly.StringFixed(0), entry.cat),
			Source:     entities.FactSourceBankStatement,
			Confidence: confidence,
			FirstObservedAt: now,
			LastConfirmedAt: now,
		}
		w.memory.SaveFact(ctx, fact, nil)
	}
}
