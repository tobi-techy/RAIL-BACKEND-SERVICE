package investing

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/document"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/rail-service/rail_service/pkg/jobqueue"
	"go.uber.org/zap"
)

// DocumentJobType is the queue job type handled by the document processor worker.
const DocumentJobType = "process_document"

// DocumentHandler exposes the unified document-intelligence upload/status/result API.
type DocumentHandler struct {
	repo      *repositories.DocumentRepository
	queue     *jobqueue.JobQueue
	fileStore document.FileStore
	logger    *zap.Logger
}

// NewDocumentHandler builds the handler.
func NewDocumentHandler(repo *repositories.DocumentRepository, queue *jobqueue.JobQueue, fileStore document.FileStore, logger *zap.Logger) *DocumentHandler {
	return &DocumentHandler{repo: repo, queue: queue, fileStore: fileStore, logger: logger}
}

// docMagicBytes detects supported types (PDF, JPEG, PNG, HEIC).
var docMagicBytes = []struct {
	prefix []byte
	offset int
	mime   string
}{
	{[]byte("%PDF-"), 0, "application/pdf"},
	{[]byte{0xFF, 0xD8, 0xFF}, 0, "image/jpeg"},
	{[]byte{0x89, 0x50, 0x4E, 0x47}, 0, "image/png"},
	{[]byte("ftyp"), 4, "image/heic"},
}

func detectDocContentType(data []byte) string {
	for _, m := range docMagicBytes {
		end := m.offset + len(m.prefix)
		if len(data) < end {
			continue
		}
		match := true
		for i, b := range m.prefix {
			if data[m.offset+i] != b {
				match = false
				break
			}
		}
		if match {
			return m.mime
		}
	}
	return ""
}

func docExtension(ct string) string {
	switch ct {
	case "application/pdf":
		return ".pdf"
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/heic":
		return ".heic"
	default:
		return ""
	}
}

// isUniqueViolation reports a Postgres unique-violation (SQLSTATE 23505),
// the concurrent-dedup race signal on uq_documents_user_hash. It matches the
// pg driver error code and falls back to a message substring so wrapped
// errors are still recognized.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && string(pqErr.Code) == "23505" {
		return true
	}
	return strings.Contains(err.Error(), "23505")
}

// Upload handles POST /api/v1/documents/upload.
// Accepts PDF/JPEG/PNG/HEIC up to 20MB, stores the original, enqueues processing,
// and returns immediately with a document_id.
func (h *DocumentHandler) Upload(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	// Rate limit: 20 documents / 24h per user.
	if count, err := h.repo.CountUploadsSince(c.Request.Context(), userID, 24*time.Hour); err == nil && count >= 20 {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "Daily document limit reached (20/day)."})
		return
	}

	file, _, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "File is required (field name: file)."})
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, 20*1024*1024+1))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read file"})
		return
	}
	if len(data) > 20*1024*1024 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "File too large (max 20MB)"})
		return
	}

	contentType := detectDocContentType(data)
	if contentType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unsupported file type. Upload PDF, JPEG, PNG, or HEIC."})
		return
	}

	hash := fmt.Sprintf("%x", sha256.Sum256(data))

	// Dedup: return the existing document instead of reprocessing.
	if existing, err := h.repo.GetByHash(c.Request.Context(), userID, hash); err == nil && existing != nil {
		c.JSON(http.StatusOK, gin.H{"data": gin.H{
			"document_id": existing.ID.String(),
			"status":      existing.Status,
			"duplicate":   true,
		}})
		return
	}

	documentID := uuid.New()
	fileKey := fmt.Sprintf("documents/%s/%s%s", userID.String(), documentID.String(), docExtension(contentType))

	if h.fileStore == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Document storage is not configured"})
		return
	}
	if err := h.fileStore.Upload(c.Request.Context(), fileKey, data, contentType); err != nil {
		h.logger.Error("document upload to store failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to store file"})
		return
	}

	doc := &entities.Document{
		ID:            documentID,
		UserID:        userID,
		Status:        entities.DocumentStatusProcessing,
		Type:          entities.DocTypeUnknown,
		FileKey:       fileKey,
		MimeType:      contentType,
		FileSizeBytes: len(data),
		FileHash:      hash,
	}
	if err := h.repo.Create(c.Request.Context(), doc); err != nil {
		_ = h.fileStore.Delete(c.Request.Context(), fileKey)
		// Concurrent duplicate upload lost the race on
		// uq_documents_user_hash: return the winner instead of a 500.
		// GetByHash is user-scoped, so this cannot leak another user's doc.
		if isUniqueViolation(err) {
			if existing, gerr := h.repo.GetByHash(c.Request.Context(), userID, hash); gerr == nil && existing != nil {
				c.JSON(http.StatusOK, gin.H{"data": gin.H{
					"document_id": existing.ID.String(),
					"status":      existing.Status,
					"duplicate":   true,
				}})
				return
			}
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create document record"})
		return
	}

	job := &jobqueue.Job{
		ID:       uuid.New().String(),
		Type:     DocumentJobType,
		Priority: jobqueue.PriorityNormal,
		Payload: map[string]interface{}{
			"document_id":  documentID.String(),
			"user_id":      userID.String(),
			"content_type": contentType,
			"file_key":     fileKey,
		},
		MaxRetries: 3,
		CreatedAt:  time.Now(),
	}
	if err := h.queue.Enqueue(c.Request.Context(), job); err != nil {
		_ = h.fileStore.Delete(c.Request.Context(), fileKey)
		_ = h.repo.Delete(c.Request.Context(), userID, documentID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to queue processing. Please try again."})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"data": gin.H{
		"document_id": documentID.String(),
		"status":      entities.DocumentStatusProcessing,
		"message":     "Document uploaded successfully.",
	}})
}

// GetStatus handles GET /api/v1/documents/:id.
func (h *DocumentHandler) GetStatus(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid document id"})
		return
	}
	doc, err := h.repo.GetByID(c.Request.Context(), userID, id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "document not found"})
		return
	}
	resp := gin.H{
		"document_id": doc.ID.String(),
		"status":      doc.Status,
		"type":        doc.Type,
	}
	if doc.ErrorMessage != nil {
		resp["error"] = *doc.ErrorMessage
	}
	c.JSON(http.StatusOK, gin.H{"data": resp})
}

// GetResult handles GET /api/v1/documents/:id/result.
//
// Serves the versioned document-intelligence contract (contract v1):
// {schema_version, document_id, document_type, status, confidence, data,
// validation, evidence}. Non-completed documents return identity + status
// only — no invented financial data. Ownership is enforced via GetByID's
// (id, user_id) scoping; cross-user access returns 404, never 403, so one
// user cannot probe another user's document existence.
func (h *DocumentHandler) GetResult(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid document id"})
		return
	}
	doc, err := h.repo.GetByID(c.Request.Context(), userID, id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "document not found"})
		return
	}

	if doc.Status != entities.DocumentStatusCompleted {
		res := document.NonCompleted(doc.ID.String(), doc.Type, doc.Status)
		c.JSON(http.StatusOK, gin.H{"data": res})
		return
	}

	res := h.buildResult(c.Request.Context(), doc)
	if err := res.Validate(); err != nil {
		h.logger.Error("document contract invalid", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "result unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": res})
}

// buildResult assembles contract v1 from persisted pipeline output. Missing
// pieces stay explicit (nil fields, empty evidence) — never synthesized.
func (h *DocumentHandler) buildResult(ctx context.Context, doc *entities.Document) document.APIResult {
	res := document.APIResult{
		SchemaVersion: document.SchemaVersion,
		DocumentID:    doc.ID.String(),
		DocumentType:  doc.Type,
		Status:        doc.Status,
		Evidence:      []document.APIEvidence{},
	}
	res.Validation = document.APIValidation{
		Status:     doc.Status,
		Difference: "0.00",
		Checks:     []document.APICheck{},
	}

	fields, _ := h.repo.GetExtractedFields(ctx, doc.ID)
	if fields != nil {
		res.Data.Merchant = fields.Merchant
		res.Data.Amount = fields.Amount
		res.Data.Currency = fields.Currency
		if fields.DocDate != nil {
			s := fields.DocDate.Format("2006-01-02")
			res.Data.DocumentDate = &s
		}
		res.Data.AccountName = fields.AccountName
		res.Data.OpeningBalance = fields.OpeningBalance
		res.Data.ClosingBalance = fields.ClosingBalance
		if len(fields.JSON) > 0 {
			res.Data.Raw = fields.JSON
		}
	}

	validation, _ := h.repo.GetValidation(ctx, doc.ID)
	if validation != nil {
		res.Confidence = validation.Confidence
		if validation.Passed {
			res.Validation.Status = "valid"
			res.Validation.Reconciled = true
		} else {
			res.Validation.Status = "invalid"
		}
		var errs []string
		if len(validation.Errors) > 0 {
			_ = json.Unmarshal(validation.Errors, &errs)
		}
		res.Validation.Errors = errs
		for _, e := range errs {
			res.Validation.Checks = append(res.Validation.Checks, document.APICheck{
				Name: "deterministic", Passed: false, Message: e,
			})
		}
	}

	ocr, _ := h.repo.GetOCRResult(ctx, doc.ID)
	if ocr != nil {
		var lines []document.Line
		if len(ocr.Lines) > 0 {
			_ = json.Unmarshal(ocr.Lines, &lines)
		}
		for i, l := range lines {
			if l.Text == "" || i >= 20 {
				break
			}
			pg := l.Page
			res.Evidence = append(res.Evidence, document.APIEvidence{
				Field:      l.Text,
				Page:       &pg,
				Region:     flattenBBox(l.BBox),
				Engine:     ocr.Engine,
				Confidence: l.Confidence,
			})
		}
	}
	return res
}

func flattenBBox(bbox [][]float64) []float64 {
	var out []float64
	for _, row := range bbox {
		out = append(out, row...)
	}
	return out
}

// Delete handles DELETE /api/v1/documents/:id. Ownership-scoped: deleting
// another user's document is a 404, and the original bytes are removed from
// object storage alongside the DB row (cascades to ocr/fields/validation).
func (h *DocumentHandler) Delete(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid document id"})
		return
	}
	doc, err := h.repo.GetByID(c.Request.Context(), userID, id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "document not found"})
		return
	}
	if h.fileStore != nil && doc.FileKey != "" {
		if err := h.fileStore.Delete(c.Request.Context(), doc.FileKey); err != nil {
			h.logger.Warn("document object delete failed", zap.Error(err))
		}
	}
	if err := h.repo.Delete(c.Request.Context(), userID, id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete document"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"document_id": id.String(), "deleted": true}})
}
