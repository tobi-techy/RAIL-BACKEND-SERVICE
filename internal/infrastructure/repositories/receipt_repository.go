package repositories

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// ReceiptRepository handles receipt scan persistence.
type ReceiptRepository struct {
	db *sqlx.DB
}

func NewReceiptRepository(db *sqlx.DB) *ReceiptRepository {
	return &ReceiptRepository{db: db}
}

func (r *ReceiptRepository) Create(ctx context.Context, scan *entities.ReceiptScan) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO receipt_scans (id, user_id, merchant, amount, currency, receipt_date, category, items, raw_text, image_hash, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		scan.ID, scan.UserID, scan.Merchant, scan.Amount, scan.Currency,
		scan.ReceiptDate, scan.Category, scan.Items, scan.RawText, scan.ImageHash, scan.CreatedAt)
	if err != nil {
		return fmt.Errorf("create receipt scan: %w", err)
	}
	return nil
}

func (r *ReceiptRepository) GetByUserID(ctx context.Context, userID uuid.UUID, limit int) ([]*entities.ReceiptScan, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	var scans []*entities.ReceiptScan
	err := r.db.SelectContext(ctx, &scans, `
		SELECT id, user_id, merchant, amount, currency, receipt_date, category, items, raw_text, image_hash, created_at
		FROM receipt_scans WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("get receipt scans: %w", err)
	}
	return scans, nil
}

func (r *ReceiptRepository) GetByUserIDInRange(ctx context.Context, userID uuid.UUID, start, end time.Time, limit int) ([]*entities.ReceiptScan, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	var scans []*entities.ReceiptScan
	err := r.db.SelectContext(ctx, &scans, `
		SELECT id, user_id, merchant, amount, currency, receipt_date, category, items, raw_text, image_hash, created_at
		FROM receipt_scans WHERE user_id = $1 AND created_at >= $2 AND created_at < $3
		ORDER BY created_at DESC LIMIT $4`, userID, start, end, limit)
	if err != nil {
		return nil, fmt.Errorf("get receipt scans in range: %w", err)
	}
	return scans, nil
}

func (r *ReceiptRepository) GetTotalByCategory(ctx context.Context, userID uuid.UUID, start, end time.Time) ([]entities.SpendingByCategory, error) {
	var results []entities.SpendingByCategory
	err := r.db.SelectContext(ctx, &results, `
		SELECT category AS merchant_category, SUM(amount) AS total, COUNT(*) AS count
		FROM receipt_scans WHERE user_id = $1 AND created_at >= $2 AND created_at < $3
		GROUP BY category ORDER BY total DESC`, userID, start, end)
	if err != nil {
		return nil, fmt.Errorf("receipt category totals: %w", err)
	}
	return results, nil
}
