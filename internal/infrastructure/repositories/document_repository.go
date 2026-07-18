package repositories

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// DocumentRepository persists documents and their OCR/extraction/validation output.
type DocumentRepository struct {
	db *sqlx.DB
}

// NewDocumentRepository builds a DocumentRepository.
func NewDocumentRepository(db *sqlx.DB) *DocumentRepository {
	return &DocumentRepository{db: db}
}

// Create inserts a new document record.
func (r *DocumentRepository) Create(ctx context.Context, d *entities.Document) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO documents (id, user_id, status, type, file_key, mime_type, file_size_bytes, file_hash, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW(), NOW())`,
		d.ID, d.UserID, d.Status, d.Type, d.FileKey, d.MimeType, d.FileSizeBytes, d.FileHash)
	if err != nil {
		return fmt.Errorf("create document: %w", err)
	}
	return nil
}

// GetByID fetches a document scoped to its owner.
func (r *DocumentRepository) GetByID(ctx context.Context, userID, id uuid.UUID) (*entities.Document, error) {
	var d entities.Document
	err := r.db.GetContext(ctx, &d, `
		SELECT id, user_id, status, type, file_key, mime_type, file_size_bytes, file_hash, error_message, created_at, updated_at
		FROM documents WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return nil, fmt.Errorf("get document: %w", err)
	}
	return &d, nil
}

// ExistsByHash reports whether the user already uploaded a file with this hash.
func (r *DocumentRepository) ExistsByHash(ctx context.Context, userID uuid.UUID, hash string) (bool, error) {
	var exists bool
	err := r.db.GetContext(ctx, &exists,
		`SELECT EXISTS(SELECT 1 FROM documents WHERE user_id = $1 AND file_hash = $2)`, userID, hash)
	if err != nil {
		return false, fmt.Errorf("check document hash: %w", err)
	}
	return exists, nil
}

// GetByHash returns an existing document by (user, hash) or nil.
func (r *DocumentRepository) GetByHash(ctx context.Context, userID uuid.UUID, hash string) (*entities.Document, error) {
	var d entities.Document
	err := r.db.GetContext(ctx, &d, `
		SELECT id, user_id, status, type, file_key, mime_type, file_size_bytes, file_hash, error_message, created_at, updated_at
		FROM documents WHERE user_id = $1 AND file_hash = $2`, userID, hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get document by hash: %w", err)
	}
	return &d, nil
}

// UpdateStatus sets status + optional error message.
func (r *DocumentRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status string, errMsg *string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE documents SET status = $2, error_message = $3, updated_at = NOW() WHERE id = $1`,
		id, status, errMsg)
	if err != nil {
		return fmt.Errorf("update document status: %w", err)
	}
	return nil
}

// UpdateType sets the classified document type.
func (r *DocumentRepository) UpdateType(ctx context.Context, id uuid.UUID, docType string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE documents SET type = $2, updated_at = NOW() WHERE id = $1`, id, docType)
	if err != nil {
		return fmt.Errorf("update document type: %w", err)
	}
	return nil
}

// Delete removes a document (cascades to ocr/fields/validation).
func (r *DocumentRepository) Delete(ctx context.Context, userID, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM documents WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("delete document: %w", err)
	}
	return nil
}

// SaveOCRResult upserts the OCR output for a document.
func (r *DocumentRepository) SaveOCRResult(ctx context.Context, rec *entities.OCRResultRecord) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO ocr_results (id, document_id, raw_text, mean_confidence, engine, page_count, lines, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
		ON CONFLICT (document_id) DO UPDATE SET
			raw_text = EXCLUDED.raw_text, mean_confidence = EXCLUDED.mean_confidence,
			engine = EXCLUDED.engine, page_count = EXCLUDED.page_count, lines = EXCLUDED.lines`,
		rec.ID, rec.DocumentID, rec.RawText, rec.MeanConfidence, rec.Engine, rec.PageCount, rec.Lines)
	if err != nil {
		return fmt.Errorf("save ocr result: %w", err)
	}
	return nil
}

// SaveExtractedFields upserts extracted fields for a document.
func (r *DocumentRepository) SaveExtractedFields(ctx context.Context, rec *entities.ExtractedFieldsRecord) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO extracted_fields (id, document_id, merchant, amount, currency, doc_date, account_name, opening_balance, closing_balance, json, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW())
		ON CONFLICT (document_id) DO UPDATE SET
			merchant = EXCLUDED.merchant, amount = EXCLUDED.amount, currency = EXCLUDED.currency,
			doc_date = EXCLUDED.doc_date, account_name = EXCLUDED.account_name,
			opening_balance = EXCLUDED.opening_balance, closing_balance = EXCLUDED.closing_balance, json = EXCLUDED.json`,
		rec.ID, rec.DocumentID, rec.Merchant, rec.Amount, rec.Currency, rec.DocDate,
		rec.AccountName, rec.OpeningBalance, rec.ClosingBalance, rec.JSON)
	if err != nil {
		return fmt.Errorf("save extracted fields: %w", err)
	}
	return nil
}

// SaveValidation upserts the validation outcome for a document.
func (r *DocumentRepository) SaveValidation(ctx context.Context, rec *entities.DocumentValidationRecord) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO document_validation (id, document_id, passed, errors, confidence, created_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (document_id) DO UPDATE SET
			passed = EXCLUDED.passed, errors = EXCLUDED.errors, confidence = EXCLUDED.confidence`,
		rec.ID, rec.DocumentID, rec.Passed, rec.Errors, rec.Confidence)
	if err != nil {
		return fmt.Errorf("save validation: %w", err)
	}
	return nil
}

// GetOCRResult returns the OCR record for a document or nil.
func (r *DocumentRepository) GetOCRResult(ctx context.Context, documentID uuid.UUID) (*entities.OCRResultRecord, error) {
	var rec entities.OCRResultRecord
	err := r.db.GetContext(ctx, &rec, `
		SELECT id, document_id, raw_text, mean_confidence, engine, page_count, lines, created_at
		FROM ocr_results WHERE document_id = $1`, documentID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get ocr result: %w", err)
	}
	return &rec, nil
}

// GetExtractedFields returns the extracted-fields record for a document or nil.
func (r *DocumentRepository) GetExtractedFields(ctx context.Context, documentID uuid.UUID) (*entities.ExtractedFieldsRecord, error) {
	var rec entities.ExtractedFieldsRecord
	err := r.db.GetContext(ctx, &rec, `
		SELECT id, document_id, merchant, amount, currency, doc_date, account_name, opening_balance, closing_balance, json, created_at
		FROM extracted_fields WHERE document_id = $1`, documentID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get extracted fields: %w", err)
	}
	return &rec, nil
}

// GetValidation returns the validation record for a document or nil.
func (r *DocumentRepository) GetValidation(ctx context.Context, documentID uuid.UUID) (*entities.DocumentValidationRecord, error) {
	var rec entities.DocumentValidationRecord
	err := r.db.GetContext(ctx, &rec, `
		SELECT id, document_id, passed, errors, confidence, created_at
		FROM document_validation WHERE document_id = $1`, documentID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get validation: %w", err)
	}
	return &rec, nil
}

// CountUploadsSince counts a user's documents created within the window.
func (r *DocumentRepository) CountUploadsSince(ctx context.Context, userID uuid.UUID, since time.Duration) (int, error) {
	var count int
	err := r.db.GetContext(ctx, &count,
		`SELECT COUNT(*) FROM documents WHERE user_id = $1 AND created_at >= $2`,
		userID, time.Now().Add(-since))
	if err != nil {
		return 0, fmt.Errorf("count documents: %w", err)
	}
	return count, nil
}

// AtomicClaim returns true if the document is claimable (not already completed).
// Since all downstream writes are upserts, a retried job re-runs safely; this
// only prevents wasted work on documents that already finished successfully.
func (r *DocumentRepository) AtomicClaim(ctx context.Context, id uuid.UUID) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE documents SET status = 'processing', updated_at = NOW()
		 WHERE id = $1 AND status <> 'completed'`, id)
	if err != nil {
		return false, fmt.Errorf("atomic claim document: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
