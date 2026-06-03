package statement

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ProcessingStage represents a named stage in the pipeline.
type ProcessingStage string

const (
	StageUpload     ProcessingStage = "upload"
	StageExtract    ProcessingStage = "extract"
	StageParse      ProcessingStage = "parse"
	StageValidate   ProcessingStage = "validate"
	StageStore      ProcessingStage = "store"
	StageEnrich     ProcessingStage = "enrich"
	StageComplete   ProcessingStage = "complete"
	StageFailed     ProcessingStage = "failed"
)

// StageProgress holds real-time progress information for the pipeline.
type StageProgress struct {
	UploadID    uuid.UUID       `json:"upload_id"`
	Stage       ProcessingStage `json:"stage"`
	Progress    float64         `json:"progress"` // 0.0-1.0
	Message     string          `json:"message"`
	Strategy    string          `json:"strategy,omitempty"`
	StartedAt   time.Time       `json:"started_at"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
	Error       string          `json:"error,omitempty"`
}

// ProgressReporter sends stage updates to subscribers (SSE, WebSocket, polling).
type ProgressReporter interface {
	Report(ctx context.Context, progress *StageProgress)
}

// Pipeline orchestrates the full document processing flow with stage tracking.
type Pipeline struct {
	extractor *DocumentExtractor
	parser    *TransactionParser
	fallback  *TransactionParser // fallback LLM parser
	reporter  ProgressReporter
	logger    *zap.Logger
}

type PipelineConfig struct {
	Extractor      *DocumentExtractor
	PrimaryParser  *TransactionParser
	FallbackParser *TransactionParser
	FileStore      FileStore // unused by Pipeline — stored for DI convenience
	Reporter       ProgressReporter
	Logger         *zap.Logger
}

func NewPipeline(cfg PipelineConfig) *Pipeline {
	return &Pipeline{
		extractor: cfg.Extractor,
		parser:    cfg.PrimaryParser,
		fallback:  cfg.FallbackParser,
		reporter:  cfg.Reporter,
		logger:    cfg.Logger,
	}
}

// PipelineResult holds the full output of processing.
type PipelineResult struct {
	Transactions []ParsedTxn
	ParseResult  *ParseResult
	PeriodStart  *time.Time
	PeriodEnd    *time.Time
	PageCount    int
	Strategy     ExtractionStrategy
	ParserUsed   string // "primary" or "fallback"
}

// Process runs the full pipeline: extract → parse → validate.
func (p *Pipeline) Process(ctx context.Context, uploadID uuid.UUID, data []byte, contentType, bankHint string) (*PipelineResult, error) {
	start := time.Now()

	// Stage: Extract text from document
	p.report(ctx, uploadID, StageExtract, 0.0, "Extracting text from document...")

	extractResult, err := p.extractor.Extract(ctx, data, contentType)
	if err != nil {
		p.report(ctx, uploadID, StageFailed, 0.0, fmt.Sprintf("Extraction failed: %v", err))
		return nil, fmt.Errorf("extraction: %w", err)
	}

	p.report(ctx, uploadID, StageExtract, 1.0, fmt.Sprintf("Extracted %d pages using %s", extractResult.PageCount, extractResult.Strategy))
	p.logger.Info("extraction complete",
		zap.String("strategy", string(extractResult.Strategy)),
		zap.Int("pages", extractResult.PageCount),
		zap.Int("text_length", len(extractResult.Text)),
		zap.Duration("elapsed", time.Since(start)),
	)

	// Stage: Parse transactions via LLM
	p.report(ctx, uploadID, StageParse, 0.0, "Analyzing transactions with AI...")

	parsed, parserUsed, err := p.parseWithFallback(ctx, extractResult, bankHint)
	if err != nil {
		p.report(ctx, uploadID, StageFailed, 0.0, fmt.Sprintf("Parse failed: %v", err))
		return nil, fmt.Errorf("parse: %w", err)
	}

	p.report(ctx, uploadID, StageParse, 1.0, fmt.Sprintf("Found %d transactions (%s)", len(parsed.Transactions), parserUsed))

	return &PipelineResult{
		Transactions: parsed.Transactions,
		ParseResult:  parsed,
		PageCount:    extractResult.PageCount,
		Strategy:     extractResult.Strategy,
		ParserUsed:   parserUsed,
	}, nil
}

func (p *Pipeline) parseWithFallback(ctx context.Context, result *ExtractionResult, bankHint string) (*ParseResult, string, error) {
	// Try chunked parsing for large docs
	if result.PageCount > 10 && len(result.Pages) > 1 {
		parsed, err := p.parseChunkedWithParser(ctx, result.Pages, bankHint, p.parser)
		if err == nil && len(parsed.Transactions) > 0 {
			return parsed, "primary", nil
		}
		if p.fallback != nil {
			p.logger.Info("primary parser failed on chunked doc, trying fallback")
			parsed, err = p.parseChunkedWithParser(ctx, result.Pages, bankHint, p.fallback)
			if err == nil && len(parsed.Transactions) > 0 {
				return parsed, "fallback", nil
			}
			// Last resort: try full text (truncated to ~50 pages) with fallback parser
			p.logger.Info("chunked parsing failed on both parsers, trying full text with fallback")
			truncated := result.Text
			if len(truncated) > 200000 {
				truncated = truncated[:200000]
			}
			parsed, err = p.fallback.Parse(ctx, truncated, bankHint)
			if err == nil && len(parsed.Transactions) > 0 {
				return parsed, "fallback-fulltext", nil
			}
		}
		if err != nil {
			return nil, "", err
		}
		return nil, "", fmt.Errorf("no transactions found after chunked parsing")
	}

	// Standard single-pass parsing
	parsed, err := p.parser.Parse(ctx, result.Text, bankHint)
	if err == nil && len(parsed.Transactions) > 0 {
		return parsed, "primary", nil
	}

	// Fallback to secondary parser
	if p.fallback != nil {
		p.logger.Info("primary parser failed, trying fallback", zap.Error(err))
		parsed, err = p.fallback.Parse(ctx, result.Text, bankHint)
		if err == nil && len(parsed.Transactions) > 0 {
			return parsed, "fallback", nil
		}
	}

	if err != nil {
		return nil, "", err
	}
	return nil, "", fmt.Errorf("no transactions extracted")
}

func (p *Pipeline) parseChunkedWithParser(ctx context.Context, pages []string, bankHint string, parser *TransactionParser) (*ParseResult, error) {
	const chunkSize = 10
	const maxChunks = 5
	merged := &ParseResult{Currency: "NGN"}
	seen := make(map[string]bool)
	chunks := 0

	for i := 0; i < len(pages) && chunks < maxChunks; i += chunkSize {
		end := i + chunkSize
		if end > len(pages) {
			end = len(pages)
		}
		chunkText := ""
		for j := i; j < end; j++ {
			if j > i {
				chunkText += "\n---PAGE BREAK---\n"
			}
			chunkText += pages[j]
		}

		parsed, err := parser.Parse(ctx, chunkText, bankHint)
		if err != nil {
			p.logger.Warn("chunk parse failed", zap.Int("chunk_start", i), zap.Error(err))
			continue
		}
		chunks++

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
			fp := fmt.Sprintf("%s|%.4f|%s|%s", txn.Date, txn.Amount, txn.Type, txn.Description)
			if !seen[fp] {
				seen[fp] = true
				merged.Transactions = append(merged.Transactions, txn)
			}
		}
	}

	if len(merged.Transactions) == 0 {
		return nil, fmt.Errorf("no transactions parsed from any chunk")
	}
	return merged, nil
}

func (p *Pipeline) report(ctx context.Context, uploadID uuid.UUID, stage ProcessingStage, progress float64, msg string) {
	if p.reporter == nil {
		return
	}
	p.reporter.Report(ctx, &StageProgress{
		UploadID:  uploadID,
		Stage:     stage,
		Progress:  progress,
		Message:   msg,
		StartedAt: time.Now(),
	})
}
