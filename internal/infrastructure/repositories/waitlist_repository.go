package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

type WaitlistRepository struct {
	db     *sql.DB
	logger *zap.Logger
}

func NewWaitlistRepository(db *sql.DB, logger *zap.Logger) *WaitlistRepository {
	return &WaitlistRepository{db: db, logger: logger}
}

func (r *WaitlistRepository) Create(ctx context.Context, user *entities.WaitlistUser) error {
	if user.ID == uuid.Nil {
		user.ID = uuid.New()
	}
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO waitlist_users (id, email, full_name, referral_code, referred_by, source)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING position, created_at`,
		user.ID, user.Email, user.FullName, user.ReferralCode, user.ReferredBy, user.Source,
	).Scan(&user.Position, &user.CreatedAt)
	if err != nil {
		return fmt.Errorf("create waitlist user: %w", err)
	}
	user.Status = entities.WaitlistStatusWaitlist
	return nil
}

func (r *WaitlistRepository) GetByEmail(ctx context.Context, email string) (*entities.WaitlistUser, error) {
	var u entities.WaitlistUser
	err := r.db.QueryRowContext(ctx, `
		SELECT id, email, full_name, status, referral_code, referred_by, position, source, converted_user_id, invited_at, converted_at, created_at
		FROM waitlist_users WHERE email = $1`, email,
	).Scan(&u.ID, &u.Email, &u.FullName, &u.Status, &u.ReferralCode, &u.ReferredBy, &u.Position, &u.Source, &u.ConvertedUserID, &u.InvitedAt, &u.ConvertedAt, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get waitlist user by email: %w", err)
	}
	return &u, nil
}

func (r *WaitlistRepository) GetByReferralCode(ctx context.Context, code string) (*entities.WaitlistUser, error) {
	var u entities.WaitlistUser
	err := r.db.QueryRowContext(ctx, `
		SELECT id, email, full_name, status, referral_code, referred_by, position, source, converted_user_id, invited_at, converted_at, created_at
		FROM waitlist_users WHERE referral_code = $1`, code,
	).Scan(&u.ID, &u.Email, &u.FullName, &u.Status, &u.ReferralCode, &u.ReferredBy, &u.Position, &u.Source, &u.ConvertedUserID, &u.InvitedAt, &u.ConvertedAt, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get waitlist user by referral code: %w", err)
	}
	return &u, nil
}

func (r *WaitlistRepository) List(ctx context.Context, status *entities.WaitlistStatus, limit, offset int) ([]entities.WaitlistUser, int, error) {
	var total int
	countQuery := "SELECT COUNT(*) FROM waitlist_users"
	listQuery := `SELECT id, email, full_name, status, referral_code, referred_by, position, source, converted_user_id, invited_at, converted_at, created_at FROM waitlist_users`

	if status != nil {
		countQuery += " WHERE status = $1"
		listQuery += " WHERE status = $1 ORDER BY position ASC LIMIT $2 OFFSET $3"
		if err := r.db.QueryRowContext(ctx, countQuery, *status).Scan(&total); err != nil {
			return nil, 0, fmt.Errorf("count waitlist: %w", err)
		}
		rows, err := r.db.QueryContext(ctx, listQuery, *status, limit, offset)
		if err != nil {
			return nil, 0, fmt.Errorf("list waitlist: %w", err)
		}
		return scanWaitlistRows(rows, total)
	}

	listQuery += " ORDER BY position ASC LIMIT $1 OFFSET $2"
	if err := r.db.QueryRowContext(ctx, countQuery).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count waitlist: %w", err)
	}
	rows, err := r.db.QueryContext(ctx, listQuery, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list waitlist: %w", err)
	}
	return scanWaitlistRows(rows, total)
}

func (r *WaitlistRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status entities.WaitlistStatus) error {
	var err error
	if status == entities.WaitlistStatusInvited {
		_, err = r.db.ExecContext(ctx, `UPDATE waitlist_users SET status = $1, invited_at = NOW() WHERE id = $2`, status, id)
	} else {
		_, err = r.db.ExecContext(ctx, `UPDATE waitlist_users SET status = $1 WHERE id = $2`, status, id)
	}
	if err != nil {
		return fmt.Errorf("update waitlist status: %w", err)
	}
	return nil
}

func (r *WaitlistRepository) MarkConverted(ctx context.Context, email string, userID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE waitlist_users SET status = 'converted', converted_user_id = $1, converted_at = $2
		WHERE email = $3 AND status != 'converted'`,
		userID, time.Now(), email,
	)
	if err != nil {
		return fmt.Errorf("mark waitlist converted: %w", err)
	}
	return nil
}

func (r *WaitlistRepository) Count(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM waitlist_users").Scan(&count)
	return count, err
}

func scanWaitlistRows(rows *sql.Rows, total int) ([]entities.WaitlistUser, int, error) {
	defer rows.Close()
	var users []entities.WaitlistUser
	for rows.Next() {
		var u entities.WaitlistUser
		if err := rows.Scan(&u.ID, &u.Email, &u.FullName, &u.Status, &u.ReferralCode, &u.ReferredBy, &u.Position, &u.Source, &u.ConvertedUserID, &u.InvitedAt, &u.ConvertedAt, &u.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan waitlist row: %w", err)
		}
		users = append(users, u)
	}
	return users, total, rows.Err()
}
