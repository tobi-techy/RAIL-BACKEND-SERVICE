package investing

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/statement"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/rail-service/rail_service/pkg/jobqueue"
	"github.com/rail-service/rail_service/pkg/metrics"
	"go.uber.org/zap"
)

func computeFileHash(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// Supported file types detected via magic bytes
var imageMagicBytes = []struct {
	prefix []byte
	offset int // byte offset to check at
	mime   string
}{
	{[]byte{0xFF, 0xD8, 0xFF}, 0, "image/jpeg"},
	{[]byte{0x89, 0x50, 0x4E, 0x47}, 0, "image/png"},
	{[]byte("ftyp"), 4, "image/heic"}, // HEIC/HEIF: ftyp box at offset 4
}

// StatementUploadHandlerV2 handles the improved statement upload pipeline.
type StatementUploadHandlerV2 struct {
	repo      *repositories.BankStatementRepository
	queue     *jobqueue.JobQueue
	fileStore statement.FileStore
	reporter  *statement.RedisProgressReporter
	logger    *zap.Logger
}

func NewStatementUploadHandlerV2(
	repo *repositories.BankStatementRepository,
	queue *jobqueue.JobQueue,
	fileStore statement.FileStore,
	reporter *statement.RedisProgressReporter,
	logger *zap.Logger,
) *StatementUploadHandlerV2 {
	return &StatementUploadHandlerV2{repo: repo, queue: queue, fileStore: fileStore, reporter: reporter, logger: logger}
}

// Upload handles POST /v2/ai/statement/upload
// Accepts PDF, JPEG, PNG, HEIC files up to 20MB.
func (h *StatementUploadHandlerV2) Upload(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	bankName := sanitizeBankName(c.PostForm("bank_name"))

	// Rate limits
	if err := h.checkRateLimits(c, userID); err != nil {
		return // response already sent
	}

	file, _, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "File is required. Supported formats: PDF, JPEG, PNG"})
		return
	}
	defer file.Close()

	// Read file data
	data, err := io.ReadAll(io.LimitReader(file, 20*1024*1024+1))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read file"})
		return
	}
	if len(data) > 20*1024*1024 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "File too large (max 20MB)"})
		return
	}

	// Detect content type via magic bytes
	contentType := detectContentType(data)
	if contentType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unsupported file type. Please upload a PDF, JPEG, or PNG file."})
		return
	}

	// Compute file hash for dedup
	hash := computeFileHash(data)
	exists, err := h.repo.ExistsByHash(c.Request.Context(), userID, hash)
	if err == nil && exists {
		c.JSON(http.StatusConflict, gin.H{"error": "This statement has already been uploaded"})
		return
	}

	// Upload to S3 (or local store in dev)
	uploadID := uuid.New()
	ext := extensionForContentType(contentType)
	fileKey := fmt.Sprintf("%s/%s%s", userID.String(), uploadID.String(), ext)

	if h.fileStore != nil {
		if _, err := h.fileStore.Upload(c.Request.Context(), fileKey, data, contentType); err != nil {
			h.logger.Error("failed to upload file to store", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to store file"})
			return
		}
	}

	// Create DB record (no file_data BLOB — just the key)
	upload := &entities.BankStatementUpload{
		UserID:        userID,
		BankName:      bankName,
		FileHash:      hash,
		FileSizeBytes: len(data),
		Status:        entities.StatementStatusPending,
	}
	// If no external file store, fall back to DB storage (existing behavior)
	if h.fileStore == nil {
		upload.FileData = data
	}

	upload.ID = uploadID
	if err := h.repo.Create(c.Request.Context(), upload); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create upload record"})
		return
	}

	// Report initial progress
	if h.reporter != nil {
		h.reporter.Report(c.Request.Context(), &statement.StageProgress{
			UploadID:  uploadID,
			Stage:     statement.StageUpload,
			Progress:  1.0,
			Message:   "File uploaded successfully",
			StartedAt: time.Now(),
		})
	}

	// Enqueue processing job
	job := &jobqueue.Job{
		ID:       uuid.New().String(),
		Type:     "process_statement",
		Priority: jobqueue.PriorityNormal,
		Payload: map[string]interface{}{
			"upload_id":    uploadID.String(),
			"user_id":      userID.String(),
			"bank_name":    bankName,
			"content_type": contentType,
			"file_key":     fileKey,
			"version":      "v2", // signals the worker to use new pipeline
		},
		MaxRetries: 3,
		CreatedAt:  time.Now(),
	}
	if err := h.queue.Enqueue(c.Request.Context(), job); err != nil {
		if h.fileStore != nil {
			h.fileStore.Delete(c.Request.Context(), fileKey)
		}
		h.repo.Delete(c.Request.Context(), userID, uploadID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to queue processing. Please try again."})
		return
	}

	metrics.RecordStatementUploaded()

	c.JSON(http.StatusAccepted, gin.H{"data": gin.H{
		"upload_id":    uploadID.String(),
		"status":       upload.Status,
		"bank_name":    bankName,
		"content_type": contentType,
		"file_size":    len(data),
		"message":      "Your statement is being processed. You'll be notified when ready.",
	}})
}

// StreamProgress handles GET /v2/ai/statement/:id/progress (SSE)
func (h *StatementUploadHandlerV2) StreamProgress(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	uploadID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid upload id"})
		return
	}

	// Verify ownership
	if _, err := h.repo.GetByID(c.Request.Context(), userID, uploadID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "upload not found"})
		return
	}

	if h.reporter == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "progress tracking unavailable"})
		return
	}

	// SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()

	// Send current state immediately
	if current, err := h.reporter.GetProgress(ctx, uploadID); err == nil {
		data, _ := json.Marshal(current)
		fmt.Fprintf(c.Writer, "data: %s\n\n", data)
		c.Writer.Flush()
		if current.Stage == statement.StageComplete || current.Stage == statement.StageFailed {
			return
		}
	}

	// Subscribe to updates
	ch := h.reporter.Subscribe(ctx, uploadID)
	for progress := range ch {
		data, _ := json.Marshal(progress)
		fmt.Fprintf(c.Writer, "data: %s\n\n", data)
		c.Writer.Flush()
		if progress.Stage == statement.StageComplete || progress.Stage == statement.StageFailed {
			return
		}
	}
}

// --- Helpers ---

func (h *StatementUploadHandlerV2) checkRateLimits(c *gin.Context, userID uuid.UUID) error {
	dailyCount, err := h.repo.CountUploadsSince(c.Request.Context(), userID, 24*time.Hour)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check upload limit"})
		return err
	}
	if dailyCount >= 10 {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "Daily upload limit reached (10/day). Try again tomorrow."})
		return fmt.Errorf("rate limited")
	}

	existing, _ := h.repo.GetByUserID(c.Request.Context(), userID, 10, 0)
	pending := 0
	for _, u := range existing {
		if u.Status == entities.StatementStatusPending || u.Status == entities.StatementStatusProcessing {
			pending++
		}
	}
	if pending >= 3 {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "3 statements are already processing. Please wait."})
		return fmt.Errorf("concurrent limit")
	}
	return nil
}

func detectContentType(data []byte) string {
	if len(data) >= 5 && string(data[:5]) == "%PDF-" {
		return "application/pdf"
	}
	for _, m := range imageMagicBytes {
		end := m.offset + len(m.prefix)
		if len(data) >= end {
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
	}
	return ""
}

func extensionForContentType(ct string) string {
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

func sanitizeBankName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "unknown"
	}
	var clean strings.Builder
	for _, r := range name {
		if r >= 32 && r <= 126 {
			clean.WriteRune(r)
		}
	}
	result := clean.String()
	if len(result) > 100 {
		result = result[:100]
	}
	if result == "" {
		return "unknown"
	}
	return result
}
