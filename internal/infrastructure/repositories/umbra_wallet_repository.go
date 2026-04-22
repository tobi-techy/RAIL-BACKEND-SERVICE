package repositories

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// UmbraWallet represents a per-user Umbra privacy wallet.
type UmbraWallet struct {
	ID             uuid.UUID  `db:"id"`
	UserID         uuid.UUID  `db:"user_id"`
	SolanaAddress  string     `db:"solana_address"`
	KeyCiphertext  string     `db:"key_ciphertext"` // AES-256-GCM encrypted
	KeyVersion     int        `db:"key_version"`
	Network        string     `db:"network"`
	Registered     bool       `db:"registered"`
	RegisteredAt   *time.Time `db:"registered_at"`
	CreatedAt      time.Time  `db:"created_at"`
	UpdatedAt      time.Time  `db:"updated_at"`
}

type UmbraWalletRepository struct {
	db *sqlx.DB
}

func NewUmbraWalletRepository(db *sqlx.DB) *UmbraWalletRepository {
	return &UmbraWalletRepository{db: db}
}

func (r *UmbraWalletRepository) Create(ctx context.Context, w *UmbraWallet) error {
	query := `INSERT INTO umbra_wallets (id, user_id, solana_address, key_ciphertext, key_version, network, registered, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
	_, err := r.db.ExecContext(ctx, query,
		w.ID, w.UserID, w.SolanaAddress, w.KeyCiphertext, w.KeyVersion, w.Network, w.Registered, w.CreatedAt, w.UpdatedAt)
	return err
}

func (r *UmbraWalletRepository) GetByUserID(ctx context.Context, userID uuid.UUID) (*UmbraWallet, error) {
	var w UmbraWallet
	err := r.db.GetContext(ctx, &w, `SELECT * FROM umbra_wallets WHERE user_id = $1`, userID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &w, err
}

func (r *UmbraWalletRepository) MarkRegistered(ctx context.Context, userID uuid.UUID) error {
	now := time.Now()
	_, err := r.db.ExecContext(ctx,
		`UPDATE umbra_wallets SET registered = TRUE, registered_at = $1, updated_at = $1 WHERE user_id = $2`,
		now, userID)
	return err
}

func (r *UmbraWalletRepository) UpdateKeyCiphertext(ctx context.Context, userID uuid.UUID, ciphertext string, version int) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE umbra_wallets SET key_ciphertext = $1, key_version = $2, updated_at = CURRENT_TIMESTAMP WHERE user_id = $3`,
		ciphertext, version, userID)
	return err
}
