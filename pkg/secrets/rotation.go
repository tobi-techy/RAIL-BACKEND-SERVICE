package secrets

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	// RotationCycleDays is the default rotation cycle (90 days)
	RotationCycleDays = 90
)

// RotationService handles secret rotation
type RotationService struct {
	provider Provider
	db       *sql.DB
	logger   *zap.Logger
}

// RotationRecord represents a secret rotation audit record
type RotationRecord struct {
	ID           uuid.UUID
	SecretName   string
	RotationType string // "scheduled", "manual", "emergency"
	RotatedBy    string
	OldVersion   string
	NewVersion   string
	Status       string // "pending", "completed", "failed", "rolled_back"
	ErrorMessage string
	CreatedAt    time.Time
	CompletedAt  *time.Time
}

// NewRotationService creates a new rotation service
func NewRotationService(provider Provider, db *sql.DB, logger *zap.Logger) *RotationService {
	return &RotationService{
		provider: provider,
		db:       db,
		logger:   logger,
	}
}

// RotateSecret rotates a secret and records the rotation
func (s *RotationService) RotateSecret(ctx context.Context, secretName, rotationType, rotatedBy string) error {
	recordID := uuid.New()
	
	// Create rotation record
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO secret_rotations (id, secret_name, rotation_type, rotated_by, status, created_at)
		 VALUES ($1, $2, $3, $4, 'pending', $5)`,
		recordID, secretName, rotationType, rotatedBy, time.Now())
	if err != nil {
		return fmt.Errorf("failed to create rotation record: %w", err)
	}

	// Get old secret for audit (hash only)
	oldSecret, err := s.provider.GetSecret(ctx, secretName)
	oldVersion := ""
	if err == nil {
		oldVersion = hashSecret(oldSecret)
	}

	// Generate new secret
	newSecret, err := generateSecureSecret(32)
	if err != nil {
		s.updateRotationStatus(ctx, recordID, "failed", err.Error())
		return fmt.Errorf("failed to generate new secret: %w", err)
	}
	newVersion := hashSecret(newSecret)

	// Update rotation record with versions
	_, err = s.db.ExecContext(ctx,
		`UPDATE secret_rotations SET old_version = $1, new_version = $2 WHERE id = $3`,
		oldVersion, newVersion, recordID)
	if err != nil {
		s.logger.Warn("Failed to update rotation versions", zap.Error(err))
	}

	// Set new secret
	if err := s.provider.SetSecret(ctx, secretName, newSecret); err != nil {
		s.updateRotationStatus(ctx, recordID, "failed", err.Error())
		return fmt.Errorf("failed to set new secret: %w", err)
	}

	// Mark rotation as completed
	s.updateRotationStatus(ctx, recordID, "completed", "")

	s.logger.Info("Secret rotated successfully",
		zap.String("secret_name", secretName),
		zap.String("rotation_type", rotationType),
		zap.String("rotated_by", rotatedBy))

	return nil
}

// GetRotationHistory returns rotation history for a secret
func (s *RotationService) GetRotationHistory(ctx context.Context, secretName string, limit int) ([]*RotationRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, secret_name, rotation_type, rotated_by, old_version, new_version, 
		        status, error_message, created_at, completed_at
		 FROM secret_rotations
		 WHERE secret_name = $1
		 ORDER BY created_at DESC
		 LIMIT $2`,
		secretName, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query rotation history: %w", err)
	}
	defer rows.Close()

	var records []*RotationRecord
	for rows.Next() {
		r := &RotationRecord{}
		var oldVersion, newVersion, errorMsg sql.NullString
		var completedAt sql.NullTime
		
		err := rows.Scan(&r.ID, &r.SecretName, &r.RotationType, &r.RotatedBy,
			&oldVersion, &newVersion, &r.Status, &errorMsg, &r.CreatedAt, &completedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan rotation record: %w", err)
		}
		
		r.OldVersion = oldVersion.String
		r.NewVersion = newVersion.String
		r.ErrorMessage = errorMsg.String
		if completedAt.Valid {
			r.CompletedAt = &completedAt.Time
		}
		
		records = append(records, r)
	}

	return records, rows.Err()
}

// CheckRotationNeeded checks if a secret needs rotation
func (s *RotationService) CheckRotationNeeded(ctx context.Context, secretName string) (bool, *time.Time, error) {
	var lastRotation sql.NullTime
	err := s.db.QueryRowContext(ctx,
		`SELECT MAX(completed_at) FROM secret_rotations 
		 WHERE secret_name = $1 AND status = 'completed'`,
		secretName).Scan(&lastRotation)
	if err != nil && err != sql.ErrNoRows {
		return false, nil, fmt.Errorf("failed to check last rotation: %w", err)
	}

	if !lastRotation.Valid {
		return true, nil, nil // Never rotated
	}

	nextRotation := lastRotation.Time.AddDate(0, 0, RotationCycleDays)
	needsRotation := time.Now().After(nextRotation)
	
	return needsRotation, &nextRotation, nil
}

// RollbackRotation attempts to rollback a failed rotation
func (s *RotationService) RollbackRotation(ctx context.Context, recordID uuid.UUID, oldSecret string) error {
	var secretName string
	err := s.db.QueryRowContext(ctx,
		"SELECT secret_name FROM secret_rotations WHERE id = $1",
		recordID).Scan(&secretName)
	if err != nil {
		return fmt.Errorf("failed to get rotation record: %w", err)
	}

	if err := s.provider.SetSecret(ctx, secretName, oldSecret); err != nil {
		return fmt.Errorf("failed to rollback secret: %w", err)
	}

	s.updateRotationStatus(ctx, recordID, "rolled_back", "")
	
	s.logger.Info("Secret rotation rolled back",
		zap.String("record_id", recordID.String()),
		zap.String("secret_name", secretName))

	return nil
}

func (s *RotationService) updateRotationStatus(ctx context.Context, recordID uuid.UUID, status, errorMsg string) {
	var completedAt interface{}
	if status == "completed" || status == "failed" || status == "rolled_back" {
		completedAt = time.Now()
	}

	_, err := s.db.ExecContext(ctx,
		`UPDATE secret_rotations 
		 SET status = $1, error_message = $2, completed_at = $3 
		 WHERE id = $4`,
		status, errorMsg, completedAt, recordID)
	if err != nil {
		s.logger.Error("Failed to update rotation status", zap.Error(err))
	}
}

func generateSecureSecret(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(bytes), nil
}

func hashSecret(secret string) string {
	// Use first 8 chars of base64 encoded hash for audit (not reversible)
	bytes := []byte(secret)
	hash := make([]byte, 8)
	for i, b := range bytes {
		hash[i%8] ^= b
	}
	return base64.URLEncoding.EncodeToString(hash)[:8]
}
