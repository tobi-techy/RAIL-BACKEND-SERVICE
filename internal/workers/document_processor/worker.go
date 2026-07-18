// Package document_processor runs the document-intelligence pipeline for queued
// uploads: download -> OCR -> classify -> extract -> validate -> persist.
package document_processor

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/document"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/rail-service/rail_service/pkg/jobqueue"
	"go.uber.org/zap"
)

// JobType is the queue job type this worker handles.
const JobType = "process_document"

// Worker processes documents through the pipeline and persists results.
type Worker struct {
	repo      *repositories.DocumentRepository
	pipeline  *document.Pipeline
	fileStore document.FileStore
	logger    *zap.Logger
}

// NewWorker builds a document processor worker.
func NewWorker(repo *repositories.DocumentRepository, pipeline *document.Pipeline, fileStore document.FileStore, logger *zap.Logger) *Worker {
	return &Worker{repo: repo, pipeline: pipeline, fileStore: fileStore, logger: logger}
}

// Handler returns the jobqueue handler for process_document jobs.
func (w *Worker) Handler() jobqueue.JobHandler {
	return func(ctx context.Context, job *jobqueue.Job) error {
		procCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()

		documentIDStr, _ := job.Payload["document_id"].(string)
		contentType, _ := job.Payload["content_type"].(string)
		fileKey, _ := job.Payload["file_key"].(string)

		documentID, err := uuid.Parse(documentIDStr)
		if err != nil {
			return fmt.Errorf("invalid document_id: %w", err)
		}

		claimed, err := w.repo.AtomicClaim(procCtx, documentID)
		if err != nil {
			return fmt.Errorf("claim document: %w", err)
		}
		if !claimed {
			return nil // already completed
		}

		if w.fileStore == nil {
			return w.fail(procCtx, documentID, "document storage not configured")
		}
		data, err := w.fileStore.Download(procCtx, fileKey)
		if err != nil {
			return w.fail(procCtx, documentID, fmt.Sprintf("failed to retrieve file: %v", err))
		}
		if len(data) == 0 {
			return w.fail(procCtx, documentID, "file is empty")
		}

		return w.process(procCtx, documentID, data, contentType)
	}
}

func (w *Worker) process(ctx context.Context, documentID uuid.UUID, data []byte, contentType string) (retErr error) {
	defer func() {
		if r := recover(); r != nil {
			msg := fmt.Sprintf("internal error: %v", r)
			_ = w.repo.UpdateStatus(ctx, documentID, entities.DocumentStatusFailed, &msg)
			w.logger.Error("document worker panic", zap.Any("panic", r), zap.String("document_id", documentID.String()))
			retErr = fmt.Errorf("panic: %v", r)
		}
	}()

	result, err := w.pipeline.Process(ctx, data, contentType)
	if err != nil {
		return w.fail(ctx, documentID, err.Error())
	}

	// Persist OCR output.
	linesJSON, _ := json.Marshal(result.OCR.Lines)
	if err := w.repo.SaveOCRResult(ctx, &entities.OCRResultRecord{
		ID:             uuid.New(),
		DocumentID:     documentID,
		RawText:        result.OCR.Text,
		MeanConfidence: result.OCR.MeanConfidence,
		Engine:         result.OCR.Engine,
		PageCount:      result.OCR.PageCount,
		Lines:          linesJSON,
	}); err != nil {
		w.logger.Warn("failed to save ocr result", zap.Error(err))
	}

	// Persist document type.
	_ = w.repo.UpdateType(ctx, documentID, string(result.Type))

	// Persist extracted fields.
	if result.Fields != nil {
		if err := w.repo.SaveExtractedFields(ctx, buildFieldsRecord(documentID, result)); err != nil {
			w.logger.Warn("failed to save extracted fields", zap.Error(err))
		}
	}

	// Persist validation.
	if result.Validation != nil {
		errsJSON, _ := json.Marshal(result.Validation.Errors)
		if err := w.repo.SaveValidation(ctx, &entities.DocumentValidationRecord{
			ID:         uuid.New(),
			DocumentID: documentID,
			Passed:     result.Validation.Passed,
			Errors:     errsJSON,
			Confidence: result.Validation.Confidence,
		}); err != nil {
			w.logger.Warn("failed to save validation", zap.Error(err))
		}
	}

	if err := w.repo.UpdateStatus(ctx, documentID, entities.DocumentStatusCompleted, nil); err != nil {
		return fmt.Errorf("mark completed: %w", err)
	}

	w.logger.Info("document processed",
		zap.String("document_id", documentID.String()),
		zap.String("type", string(result.Type)),
		zap.Float64("ocr_confidence", result.OCR.MeanConfidence),
		zap.Bool("valid", result.Validation != nil && result.Validation.Passed),
	)
	return nil
}

func (w *Worker) fail(ctx context.Context, documentID uuid.UUID, msg string) error {
	_ = w.repo.UpdateStatus(ctx, documentID, entities.DocumentStatusFailed, &msg)
	return fmt.Errorf("%s", msg)
}

// buildFieldsRecord flattens pipeline Fields into the storable record. The full
// structured payload (items/transactions) is preserved in the json column.
func buildFieldsRecord(documentID uuid.UUID, result *document.Result) *entities.ExtractedFieldsRecord {
	f := result.Fields
	rec := &entities.ExtractedFieldsRecord{
		ID:             uuid.New(),
		DocumentID:     documentID,
		OpeningBalance: f.OpeningBalance,
		ClosingBalance: f.ClosingBalance,
		Amount:         f.Total,
	}
	// Prefer the LLM-normalized merchant when available.
	merchant := f.Merchant
	if result.Enrichment != nil && result.Enrichment.Merchant != "" {
		merchant = result.Enrichment.Merchant
	}
	if merchant != "" {
		rec.Merchant = &merchant
	}
	if f.Currency != "" {
		cur := f.Currency
		rec.Currency = &cur
	}
	if f.Date != nil {
		rec.DocDate = f.Date
	} else if f.PeriodEnd != nil {
		rec.DocDate = f.PeriodEnd
	}
	if f.AccountName != "" {
		an := f.AccountName
		rec.AccountName = &an
	}

	payload := map[string]interface{}{"fields": f}
	if result.Enrichment != nil {
		payload["enrichment"] = result.Enrichment
	}
	jsonBytes, _ := json.Marshal(payload)
	rec.JSON = jsonBytes
	return rec
}
