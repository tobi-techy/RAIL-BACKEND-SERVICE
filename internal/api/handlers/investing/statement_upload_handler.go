package investing

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/rail-service/rail_service/pkg/jobqueue"
	"go.uber.org/zap"
)

type StatementUploadHandler struct {
	repo     *repositories.BankStatementRepository
	queue    *jobqueue.JobQueue
	uploadDir string
	logger   *zap.Logger
}

func NewStatementUploadHandler(repo *repositories.BankStatementRepository, queue *jobqueue.JobQueue, logger *zap.Logger) *StatementUploadHandler {
	dir := os.TempDir()
	uploadDir := filepath.Join(dir, "rail-statements")
	os.MkdirAll(uploadDir, 0755)

	// Clean up stale temp files on startup (older than 1 hour)
	go cleanStaleTempFiles(uploadDir, logger)

	return &StatementUploadHandler{repo: repo, queue: queue, uploadDir: uploadDir, logger: logger}
}

// cleanStaleTempFiles removes PDF files older than 1 hour from the upload dir.
func cleanStaleTempFiles(dir string, logger *zap.Logger) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-1 * time.Hour)
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			os.Remove(filepath.Join(dir, entry.Name()))
			removed++
		}
	}
	if removed > 0 {
		logger.Info("cleaned stale statement temp files", zap.Int("removed", removed))
	}
}

// Upload handles POST /v1/ai/statement/upload
func (h *StatementUploadHandler) Upload(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	bankName := c.PostForm("bank_name")
	if bankName == "" {
		bankName = "unknown"
	}

	// Per-user throttle: max 3 pending/processing uploads at a time
	existing, _ := h.repo.GetByUserID(c.Request.Context(), userID, 10)
	pendingCount := 0
	for _, u := range existing {
		if u.Status == entities.StatementStatusPending || u.Status == entities.StatementStatusProcessing {
			pendingCount++
		}
	}
	if pendingCount >= 3 {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "You have too many statements being processed. Please wait for them to complete."})
		return
	}

	file, _, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "PDF file is required"})
		return
	}
	defer file.Close()

	// Validate content type via magic bytes (multipart content-type headers are unreliable)
	buf := make([]byte, 5)
	n, _ := file.Read(buf)
	if n < 5 || string(buf[:5]) != "%PDF-" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only PDF files are accepted"})
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to process file"})
		return
	}

	// Read file and compute hash
	data, err := io.ReadAll(io.LimitReader(file, 20*1024*1024+1))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read file"})
		return
	}
	if len(data) > 20*1024*1024 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "File too large (max 20MB)"})
		return
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(data))

	// Check for duplicate
	exists, err := h.repo.ExistsByHash(c.Request.Context(), userID, hash)
	if err == nil && exists {
		c.JSON(http.StatusConflict, gin.H{"error": "This statement has already been uploaded"})
		return
	}

	// Save to temp file
	tmpPath := filepath.Join(h.uploadDir, fmt.Sprintf("%s_%s.pdf", userID.String(), uuid.New().String()[:8]))
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save file"})
		return
	}

	// Create upload record
	upload := &entities.BankStatementUpload{
		UserID:        userID,
		BankName:      bankName,
		FileHash:      hash,
		FileSizeBytes: len(data),
		Status:        entities.StatementStatusPending,
	}
	if err := h.repo.Create(c.Request.Context(), upload); err != nil {
		os.Remove(tmpPath)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create upload record"})
		return
	}

	// Enqueue processing job
	job := &jobqueue.Job{
		ID:       uuid.New().String(),
		Type:     "process_statement",
		Priority: jobqueue.PriorityNormal,
		Payload: map[string]interface{}{
			"upload_id": upload.ID.String(),
			"user_id":   userID.String(),
			"file_path": tmpPath,
			"bank_name": bankName,
		},
		MaxRetries: 2,
		CreatedAt:  time.Now(),
	}
	if err := h.queue.Enqueue(c.Request.Context(), job); err != nil {
		h.logger.Error("failed to enqueue statement job", zap.Error(err))
		// Still return success — we can retry later
	}

	c.JSON(http.StatusAccepted, gin.H{"data": gin.H{
		"upload_id": upload.ID.String(),
		"status":    upload.Status,
		"bank_name": bankName,
		"message":   "Your statement is being processed. Miriam will notify you when it's ready.",
	}})
}

// GetStatus handles GET /v1/ai/statement/:id/status
func (h *StatementUploadHandler) GetStatus(c *gin.Context) {
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

	upload, err := h.repo.GetByID(c.Request.Context(), userID, uploadID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "upload not found"})
		return
	}

	resp := gin.H{
		"upload_id":         upload.ID.String(),
		"status":            upload.Status,
		"bank_name":         upload.BankName,
		"transaction_count": upload.TransactionCount,
		"created_at":        upload.CreatedAt.Format(time.RFC3339),
	}
	if upload.ErrorMessage != nil {
		resp["error_message"] = *upload.ErrorMessage
	}
	if upload.PeriodStart != nil {
		resp["period_start"] = upload.PeriodStart.Format("2006-01-02")
	}
	if upload.PeriodEnd != nil {
		resp["period_end"] = upload.PeriodEnd.Format("2006-01-02")
	}

	c.JSON(http.StatusOK, gin.H{"data": resp})
}

// List handles GET /v1/ai/statements
func (h *StatementUploadHandler) List(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	uploads, err := h.repo.GetByUserID(c.Request.Context(), userID, 20)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list statements"})
		return
	}

	items := make([]gin.H, 0, len(uploads))
	for _, u := range uploads {
		item := gin.H{
			"id":                u.ID.String(),
			"bank_name":         u.BankName,
			"status":            u.Status,
			"transaction_count": u.TransactionCount,
			"created_at":        u.CreatedAt.Format(time.RFC3339),
		}
		if u.PeriodStart != nil {
			item["period_start"] = u.PeriodStart.Format("2006-01-02")
		}
		if u.PeriodEnd != nil {
			item["period_end"] = u.PeriodEnd.Format("2006-01-02")
		}
		items = append(items, item)
	}

	c.JSON(http.StatusOK, gin.H{"data": gin.H{"statements": items}})
}

// Delete handles DELETE /v1/ai/statement/:id
func (h *StatementUploadHandler) Delete(c *gin.Context) {
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

	if _, err := h.repo.GetByID(c.Request.Context(), userID, uploadID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "upload not found"})
		return
	}

	if err := h.repo.Delete(c.Request.Context(), userID, uploadID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": gin.H{"deleted": true}})
}
