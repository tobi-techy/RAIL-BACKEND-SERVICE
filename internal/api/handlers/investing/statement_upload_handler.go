package investing

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/rail-service/rail_service/pkg/jobqueue"
	"github.com/rail-service/rail_service/pkg/metrics"
	"go.uber.org/zap"
)

type StatementUploadHandler struct {
	repo   *repositories.BankStatementRepository
	queue  *jobqueue.JobQueue
	logger *zap.Logger
}

func NewStatementUploadHandler(repo *repositories.BankStatementRepository, queue *jobqueue.JobQueue, logger *zap.Logger) *StatementUploadHandler {
	return &StatementUploadHandler{repo: repo, queue: queue, logger: logger}
}

// Upload handles POST /v1/ai/statement/upload
func (h *StatementUploadHandler) Upload(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	bankName := c.PostForm("bank_name")
	// Sanitize: trim, limit to 100 chars, strip non-printable chars
	bankName = strings.TrimSpace(bankName)
	if bankName == "" {
		bankName = "unknown"
	} else {
		// Strip non-printable characters and truncate
		var clean strings.Builder
		for _, r := range bankName {
			if r >= 32 && r <= 126 {
				clean.WriteRune(r)
			}
		}
		bankName = clean.String()
		if len(bankName) > 100 {
			bankName = bankName[:100]
		}
		if bankName == "" {
			bankName = "unknown"
		}
	}

	// Per-user throttle: max 10 uploads per rolling 24h
	dailyCount, err := h.repo.CountUploadsSince(c.Request.Context(), userID, 24*time.Hour)
	if err != nil {
		h.logger.Error("failed to check daily upload count", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check upload limit. Please try again."})
		return
	}
	if dailyCount >= 10 {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "Daily upload limit reached. Please try again tomorrow."})
		return
	}

	// Per-user throttle: max 3 pending/processing uploads at a time
	existing, _ := h.repo.GetByUserID(c.Request.Context(), userID, 10, 0)
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

	// Create upload record with file data stored in DB (survives restarts)
	upload := &entities.BankStatementUpload{
		UserID:        userID,
		BankName:      bankName,
		FileHash:      hash,
		FileSizeBytes: len(data),
		FileData:      data,
		Status:        entities.StatementStatusPending,
	}
	if err := h.repo.Create(c.Request.Context(), upload); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create upload record"})
		return
	}

	// Enqueue processing job (no file_path — worker reads from DB)
	job := &jobqueue.Job{
		ID:       uuid.New().String(),
		Type:     "process_statement",
		Priority: jobqueue.PriorityNormal,
		Payload: map[string]interface{}{
			"upload_id": upload.ID.String(),
			"user_id":   userID.String(),
			"bank_name": bankName,
		},
		MaxRetries: 2,
		CreatedAt:  time.Now(),
	}
	if err := h.queue.Enqueue(c.Request.Context(), job); err != nil {
		// Rollback: delete DB record
		if cleanupErr := h.repo.Delete(c.Request.Context(), userID, upload.ID); cleanupErr != nil {
			h.logger.Error("failed to clean up after enqueue failure", zap.Error(cleanupErr))
		}
		h.logger.Error("failed to enqueue statement job", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to queue statement for processing. Please try again."})
		return
	}

	metrics.RecordStatementUploaded()

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
	if upload.Summary != nil && *upload.Summary != "" && *upload.Summary != "{}" {
		var summaryObj interface{}
		if err := json.Unmarshal([]byte(*upload.Summary), &summaryObj); err == nil {
			resp["summary"] = summaryObj
		}
	}

	c.JSON(http.StatusOK, gin.H{"data": resp})
}

// GetTransactions handles GET /v1/ai/statement/:id/transactions?limit=50&offset=0
func (h *StatementUploadHandler) GetTransactions(c *gin.Context) {
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

	// Verify upload belongs to user
	upload, err := h.repo.GetByID(c.Request.Context(), userID, uploadID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "upload not found"})
		return
	}
	if upload.Status != entities.StatementStatusCompleted {
		c.JSON(http.StatusBadRequest, gin.H{"error": "statement is not yet completed"})
		return
	}

	limit := 50
	if l, err := parseIntParam(c.Query("limit")); err == nil && l > 0 && l <= 100 {
		limit = l
	}
	offset := 0
	if o, err := parseIntParam(c.Query("offset")); err == nil && o >= 0 {
		offset = o
	}

	// Get total count for pagination
	totalCount, err := h.repo.CountTransactionsByUploadID(c.Request.Context(), uploadID)
	if err != nil {
		totalCount = 0
	}

	txns, err := h.repo.GetTransactionsByUploadIDPaginated(c.Request.Context(), uploadID, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch transactions"})
		return
	}

	items := make([]gin.H, 0, len(txns))
	for _, t := range txns {
		item := gin.H{
			"id":               t.ID.String(),
			"transaction_date": t.TransactionDate.Format("2006-01-02"),
			"description":      t.Description,
			"amount":           t.Amount.StringFixed(2),
			"currency":         t.Currency,
			"type":             t.Type,
			"category":         t.Category,
		}
		if t.BalanceAfter != nil {
			item["balance_after"] = t.BalanceAfter.StringFixed(2)
		}
		items = append(items, item)
	}

	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"transactions": items,
		"total":        totalCount,
		"limit":        limit,
		"offset":       offset,
	}})
}

// List handles GET /v1/ai/statements?limit=20&offset=0
func (h *StatementUploadHandler) List(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	limit := 20
	if l, err := parseIntParam(c.Query("limit")); err == nil && l > 0 && l <= 100 {
		limit = l
	}
	offset := 0
	if o, err := parseIntParam(c.Query("offset")); err == nil && o >= 0 {
		offset = o
	}

	uploads, err := h.repo.GetByUserID(c.Request.Context(), userID, limit, offset)
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

	c.JSON(http.StatusOK, gin.H{"data": gin.H{"statements": items, "limit": limit, "offset": offset}})
}

// parseIntParam parses an integer query parameter, returning 0 and error if invalid.
func parseIntParam(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}
	return n, nil
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

	upload, err := h.repo.GetByID(c.Request.Context(), userID, uploadID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "upload not found"})
		return
	}
	if upload.Status == entities.StatementStatusProcessing {
		c.JSON(http.StatusConflict, gin.H{"error": "Cannot delete a statement that is currently being processed. Please wait and try again."})
		return
	}

	if err := h.repo.Delete(c.Request.Context(), userID, uploadID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": gin.H{"deleted": true}})
}
