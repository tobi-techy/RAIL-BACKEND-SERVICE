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
		INSERT INTO receipt_scans (id, user_id, merchant, amount, currency, receipt_date, category, items, raw_text, image_hash, thumbnail, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		scan.ID, scan.UserID, scan.Merchant, scan.Amount, scan.Currency,
		scan.ReceiptDate, scan.Category, scan.Items, scan.RawText, scan.ImageHash, scan.Thumbnail, scan.CreatedAt)
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
		SELECT id, user_id, merchant, amount, currency, receipt_date, category, items, raw_text, image_hash, thumbnail, created_at
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
		SELECT id, user_id, merchant, amount, currency, receipt_date, category, items, raw_text, image_hash, thumbnail, created_at
		FROM receipt_scans WHERE user_id = $1 AND created_at >= $2 AND created_at < $3
		ORDER BY created_at DESC LIMIT $4`, userID, start, end, limit)
	if err != nil {
		return nil, fmt.Errorf("get receipt scans in range: %w", err)
	}
	return scans, nil
}

func (r *ReceiptRepository) ExistsByImageHash(ctx context.Context, userID uuid.UUID, hash string) (bool, error) {
	var exists bool
	err := r.db.GetContext(ctx, &exists, `SELECT EXISTS(SELECT 1 FROM receipt_scans WHERE user_id = $1 AND image_hash = $2)`, userID, hash)
	if err != nil {
		return false, fmt.Errorf("check receipt hash: %w", err)
	}
	return exists, nil
}

func (r *ReceiptRepository) GetByImageHash(ctx context.Context, userID uuid.UUID, hash string) (*entities.ReceiptScan, error) {
	var scan entities.ReceiptScan
	err := r.db.GetContext(ctx, &scan, `
		SELECT id, user_id, merchant, amount, currency, receipt_date, category, items, raw_text, image_hash, thumbnail, created_at
		FROM receipt_scans WHERE user_id = $1 AND image_hash = $2 LIMIT 1`, userID, hash)
	if err != nil {
		return nil, fmt.Errorf("get receipt by hash: %w", err)
	}
	return &scan, nil
}

func (r *ReceiptRepository) GetByID(ctx context.Context, userID, receiptID uuid.UUID) (*entities.ReceiptScan, error) {
	var scan entities.ReceiptScan
	err := r.db.GetContext(ctx, &scan, `
		SELECT id, user_id, merchant, amount, currency, receipt_date, category, items, raw_text, image_hash, thumbnail, created_at
		FROM receipt_scans WHERE id = $1 AND user_id = $2`, receiptID, userID)
	if err != nil {
		return nil, fmt.Errorf("get receipt scan: %w", err)
	}
	return &scan, nil
}

func (r *ReceiptRepository) Update(ctx context.Context, scan *entities.ReceiptScan) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE receipt_scans SET merchant=$1, amount=$2, currency=$3, receipt_date=$4, category=$5, items=$6
		WHERE id=$7 AND user_id=$8`,
		scan.Merchant, scan.Amount, scan.Currency, scan.ReceiptDate, scan.Category, scan.Items, scan.ID, scan.UserID)
	if err != nil {
		return fmt.Errorf("update receipt scan: %w", err)
	}
	return nil
}

func (r *ReceiptRepository) Delete(ctx context.Context, userID, receiptID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM receipt_scans WHERE id=$1 AND user_id=$2`, receiptID, userID)
	if err != nil {
		return fmt.Errorf("delete receipt scan: %w", err)
	}
	return nil
}

func (r *ReceiptRepository) GetByUserIDPaginated(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.ReceiptScan, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	if offset < 0 {
		offset = 0
	}
	var scans []*entities.ReceiptScan
	err := r.db.SelectContext(ctx, &scans, `
		SELECT id, user_id, merchant, amount, currency, receipt_date, category, items, raw_text, image_hash, thumbnail, created_at
		FROM receipt_scans WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("get receipt scans paginated: %w", err)
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

func (r *ReceiptRepository) GetGallery(ctx context.Context, userID uuid.UUID, category string, limit, offset int) ([]*entities.ReceiptScan, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	var scans []*entities.ReceiptScan
	if category != "" {
		err := r.db.SelectContext(ctx, &scans, `
			SELECT id, user_id, merchant, amount, currency, receipt_date, category, thumbnail, created_at
			FROM receipt_scans WHERE user_id = $1 AND thumbnail IS NOT NULL AND category = $2
			ORDER BY created_at DESC LIMIT $3 OFFSET $4`, userID, category, limit, offset)
		if err != nil {
			return nil, fmt.Errorf("get receipt gallery: %w", err)
		}
	} else {
		err := r.db.SelectContext(ctx, &scans, `
			SELECT id, user_id, merchant, amount, currency, receipt_date, category, thumbnail, created_at
			FROM receipt_scans WHERE user_id = $1 AND thumbnail IS NOT NULL
			ORDER BY created_at DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
		if err != nil {
			return nil, fmt.Errorf("get receipt gallery: %w", err)
		}
	}
	return scans, nil
}