package recovery

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

type BackupManager struct {
	db     *sql.DB
	logger *zap.Logger
}

type StorageBackup struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	Namespace   string
	StorageID   string
	Checksum    string
	Size        int64
	BackedUpAt  time.Time
	VerifiedAt  *time.Time
	Status      string
}

func NewBackupManager(db *sql.DB, logger *zap.Logger) *BackupManager {
	return &BackupManager{
		db:     db,
		logger: logger,
	}
}

func (m *BackupManager) RecordBackup(ctx context.Context, backup *StorageBackup) error {
	query := `
		INSERT INTO zerog_storage_backups 
		(id, user_id, namespace, storage_id, checksum, size, backed_up_at, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`

	_, err := m.db.ExecContext(ctx, query,
		backup.ID,
		backup.UserID,
		backup.Namespace,
		backup.StorageID,
		backup.Checksum,
		backup.Size,
		backup.BackedUpAt,
		backup.Status,
	)

	if err != nil {
		return fmt.Errorf("record backup: %w", err)
	}

	m.logger.Info("backup recorded",
		zap.String("storage_id", backup.StorageID),
		zap.String("namespace", backup.Namespace),
	)

	return nil
}

func (m *BackupManager) VerifyBackup(ctx context.Context, storageID string) error {
	query := `
		UPDATE zerog_storage_backups 
		SET verified_at = $1, status = 'verified'
		WHERE storage_id = $2
	`

	now := time.Now()
	_, err := m.db.ExecContext(ctx, query, now, storageID)
	if err != nil {
		return fmt.Errorf("verify backup: %w", err)
	}

	m.logger.Info("backup verified", zap.String("storage_id", storageID))
	return nil
}

func (m *BackupManager) GetBackup(ctx context.Context, storageID string) (*StorageBackup, error) {
	query := `
		SELECT id, user_id, namespace, storage_id, checksum, size, backed_up_at, verified_at, status
		FROM zerog_storage_backups
		WHERE storage_id = $1
	`

	backup := &StorageBackup{}
	err := m.db.QueryRowContext(ctx, query, storageID).Scan(
		&backup.ID,
		&backup.UserID,
		&backup.Namespace,
		&backup.StorageID,
		&backup.Checksum,
		&backup.Size,
		&backup.BackedUpAt,
		&backup.VerifiedAt,
		&backup.Status,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("backup not found")
	}
	if err != nil {
		return nil, fmt.Errorf("get backup: %w", err)
	}

	return backup, nil
}

func (m *BackupManager) ListUnverifiedBackups(ctx context.Context, olderThan time.Duration) ([]*StorageBackup, error) {
	query := `
		SELECT id, user_id, namespace, storage_id, checksum, size, backed_up_at, verified_at, status
		FROM zerog_storage_backups
		WHERE verified_at IS NULL AND backed_up_at < $1
		ORDER BY backed_up_at ASC
		LIMIT 100
	`

	threshold := time.Now().Add(-olderThan)
	rows, err := m.db.QueryContext(ctx, query, threshold)
	if err != nil {
		return nil, fmt.Errorf("list unverified: %w", err)
	}
	defer rows.Close()

	var backups []*StorageBackup
	for rows.Next() {
		backup := &StorageBackup{}
		err := rows.Scan(
			&backup.ID,
			&backup.UserID,
			&backup.Namespace,
			&backup.StorageID,
			&backup.Checksum,
			&backup.Size,
			&backup.BackedUpAt,
			&backup.VerifiedAt,
			&backup.Status,
		)
		if err != nil {
			return nil, fmt.Errorf("scan backup: %w", err)
		}
		backups = append(backups, backup)
	}

	return backups, nil
}

func (m *BackupManager) DeleteOldBackups(ctx context.Context, olderThan time.Duration) (int64, error) {
	query := `
		DELETE FROM zerog_storage_backups
		WHERE backed_up_at < $1 AND status = 'verified'
	`

	threshold := time.Now().Add(-olderThan)
	result, err := m.db.ExecContext(ctx, query, threshold)
	if err != nil {
		return 0, fmt.Errorf("delete old backups: %w", err)
	}

	count, _ := result.RowsAffected()
	m.logger.Info("old backups deleted", zap.Int64("count", count))
	return count, nil
}
