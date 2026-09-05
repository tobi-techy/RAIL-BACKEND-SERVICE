package di

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/statement"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	platform "github.com/rail-service/rail_service/internal/infrastructure/platform"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/rail-service/rail_service/pkg/jobqueue"
	"go.uber.org/zap"
)

const pendingGuestStatementTTL = 24 * time.Hour

type pendingGuestStatement struct {
	Name     string `json:"name"`
	MIMEType string `json:"mime_type"`
	Data     []byte `json:"data"`
}

// platformStatementAttachmentHandler scans guest PDFs immediately, while
// keeping the original bytes in a short-lived Redis record until signup.
// Linked PDFs go straight into the existing durable statement job.
type platformStatementAttachmentHandler struct {
	repo     *repositories.BankStatementRepository
	queue    *jobqueue.JobQueue
	pipeline *statement.Pipeline
	store    cache.RedisClient
	logger   *zap.Logger
}

// NewPlatformStatementAttachmentHandler creates the bridge-to-statement
// adapter. It returns nil when the durable statement dependencies are absent.
func NewPlatformStatementAttachmentHandler(
	repo *repositories.BankStatementRepository,
	queue *jobqueue.JobQueue,
	pipeline *statement.Pipeline,
	store cache.RedisClient,
	logger *zap.Logger,
) platform.StatementAttachmentHandler {
	if repo == nil || queue == nil || pipeline == nil || store == nil {
		return nil
	}
	return &platformStatementAttachmentHandler{
		repo: repo, queue: queue, pipeline: pipeline, store: store, logger: logger,
	}
}

func (h *platformStatementAttachmentHandler) ScanGuest(ctx context.Context, senderID string, attachment platform.StatementAttachment) (*platform.StatementScan, error) {
	if len(attachment.Data) == 0 {
		return nil, fmt.Errorf("empty statement")
	}
	scanCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	result, err := h.pipeline.Process(scanCtx, uuid.New(), attachment.Data, attachment.MIMEType, "")
	if err != nil {
		return nil, fmt.Errorf("scan statement: %w", err)
	}
	if result == nil || result.ParseResult == nil || len(result.ParseResult.Transactions) == 0 {
		return nil, fmt.Errorf("no transactions found")
	}

	pendingID := uuid.NewString()
	pendingKey := pendingStatementKey(senderID, pendingID)
	pending := pendingGuestStatement{
		Name: attachment.Name, MIMEType: "application/pdf", Data: attachment.Data,
	}
	if err := h.store.Set(ctx, pendingKey, pending, pendingGuestStatementTTL); err != nil {
		return nil, fmt.Errorf("save pending statement: %w", err)
	}

	return &platform.StatementScan{
		PendingID: pendingKey,
		Summary:   summarizeGuestStatement(result.ParseResult),
	}, nil
}

func (h *platformStatementAttachmentHandler) EnqueueLinked(ctx context.Context, userID uuid.UUID, attachment platform.StatementAttachment) (*platform.PlatformReply, error) {
	uploadID, err := h.createAndEnqueue(ctx, userID, attachment)
	if err != nil {
		return nil, err
	}
	h.logger.Info("platform statement queued", zap.String("upload_id", uploadID.String()), zap.String("user_id", userID.String()))
	return &platform.PlatformReply{Text: "Got it. I'm scanning that statement now. I'll bring you the useful patterns when it's ready."}, nil
}

func (h *platformStatementAttachmentHandler) CompletePending(ctx context.Context, userID uuid.UUID, pendingID string) error {
	pendingKey := strings.TrimSpace(pendingID)
	if pendingKey == "" {
		return nil
	}
	if !strings.HasPrefix(pendingKey, "onboarding:statement:") {
		return fmt.Errorf("invalid pending statement reference")
	}
	var pending pendingGuestStatement
	if err := h.store.Get(ctx, pendingKey, &pending); err != nil {
		return fmt.Errorf("load pending statement: %w", err)
	}
	if _, err := h.createAndEnqueue(ctx, userID, platform.StatementAttachment{
		Name: pending.Name, MIMEType: pending.MIMEType, Data: pending.Data,
	}); err != nil {
		return err
	}
	if err := h.store.Del(ctx, pendingKey); err != nil {
		return fmt.Errorf("delete pending statement: %w", err)
	}
	return nil
}

func (h *platformStatementAttachmentHandler) createAndEnqueue(ctx context.Context, userID uuid.UUID, attachment platform.StatementAttachment) (uuid.UUID, error) {
	uploadID := uuid.New()
	hash := sha256.Sum256(attachment.Data)
	upload := &entities.BankStatementUpload{
		ID:            uploadID,
		UserID:        userID,
		BankName:      "unknown",
		FileHash:      hex.EncodeToString(hash[:]),
		FileSizeBytes: len(attachment.Data),
		FileData:      attachment.Data,
		Status:        entities.StatementStatusPending,
	}
	if err := h.repo.Create(ctx, upload); err != nil {
		return uuid.Nil, fmt.Errorf("create statement upload: %w", err)
	}
	job := &jobqueue.Job{
		ID: uuid.NewString(), Type: "process_statement", Priority: jobqueue.PriorityNormal,
		Payload: map[string]interface{}{
			"upload_id": uploadID.String(), "user_id": userID.String(),
			"bank_name": "unknown", "content_type": "application/pdf", "version": "v2",
		},
		MaxRetries: 3, CreatedAt: time.Now(),
	}
	if err := h.queue.Enqueue(ctx, job); err != nil {
		_ = h.repo.Delete(ctx, userID, uploadID)
		return uuid.Nil, fmt.Errorf("enqueue statement: %w", err)
	}
	return uploadID, nil
}

func pendingStatementKey(senderID, pendingID string) string {
	if senderID == "" {
		return "onboarding:statement:" + pendingID
	}
	sum := sha256.Sum256([]byte(senderID))
	return fmt.Sprintf("onboarding:statement:%s:%s", hex.EncodeToString(sum[:8]), pendingID)
}

func summarizeGuestStatement(result *statement.ParseResult) string {
	var income, spending float64
	categories := make(map[string]float64)
	for _, txn := range result.Transactions {
		if strings.EqualFold(txn.Type, "credit") {
			income += txn.Amount
			continue
		}
		spending += txn.Amount
		category := strings.TrimSpace(txn.Category)
		if category == "" {
			category = "other"
		}
		categories[category] += txn.Amount
	}
	type categoryTotal struct {
		name  string
		total float64
	}
	sorted := make([]categoryTotal, 0, len(categories))
	for name, total := range categories {
		sorted = append(sorted, categoryTotal{name: name, total: total})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].total > sorted[j].total })
	top := make([]string, 0, 3)
	for i := 0; i < len(sorted) && i < 3; i++ {
		top = append(top, fmt.Sprintf("%s %.0f", sorted[i].name, sorted[i].total))
	}
	currency := result.Currency
	if currency == "" {
		currency = "the statement currency"
	}
	summary := fmt.Sprintf("I found %d transactions. Income was about %s %.0f and spending about %s %.0f", len(result.Transactions), currency, income, currency, spending)
	if len(top) > 0 {
		summary += ". Biggest spending areas: " + strings.Join(top, ", ")
	}
	return summary + "."
}
