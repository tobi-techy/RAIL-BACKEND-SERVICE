package investing

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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
		c.JSON(http.StatusOK, gin.H{"data": gin.H{
			"document_id": doc.ID.String(),
			"status":      doc.Status,
		}})
		return
	}

	fields, _ := h.repo.GetExtractedFields(c.Request.Context(), id)
	validation, _ := h.repo.GetValidation(c.Request.Context(), id)

	resp := gin.H{
		"document_id": doc.ID.String(),
		"status":      doc.Status,
		"type":        doc.Type,
	}
	if fields != nil {
		resp["fields"] = fields
	}
	if validation != nil {
		resp["validation"] = validation
	}
	c.JSON(http.StatusOK, gin.H{"data": resp})
}
