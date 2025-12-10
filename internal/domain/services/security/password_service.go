package security

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

const (
	// BcryptCost is the cost factor for bcrypt hashing (12-14 recommended for production)
	BcryptCost = 12
	// PasswordHistoryLimit is the number of previous passwords to track
	PasswordHistoryLimit = 5
	// PasswordExpirationDays is the number of days before password expires (0 = disabled)
	PasswordExpirationDays = 90
)

// PasswordService handles password hashing, validation, and history
type PasswordService struct {
	db           *sql.DB
	logger       *zap.Logger
	policyService *PasswordPolicyService
}

// NewPasswordService creates a new password service
func NewPasswordService(db *sql.DB, logger *zap.Logger, checkBreaches bool) *PasswordService {
	return &PasswordService{
		db:            db,
		logger:        logger,
		policyService: NewPasswordPolicyService(checkBreaches),
	}
}

// HashPassword hashes a password using bcrypt with increased cost
func (s *PasswordService) HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), BcryptCost)
	if err != nil {
		return "", fmt.Errorf("failed to hash password: %w", err)
	}
	return string(hash), nil
}

// VerifyPassword verifies a password against a hash
func (s *PasswordService) VerifyPassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// ValidateNewPassword validates a new password against policy and history
func (s *PasswordService) ValidateNewPassword(ctx context.Context, userID uuid.UUID, newPassword string) (*PasswordValidationResult, error) {
	// First validate against policy
	result, err := s.policyService.ValidatePassword(ctx, newPassword)
	if err != nil {
		return nil, fmt.Errorf("failed to validate password policy: %w", err)
	}

	if !result.Valid {
		return result, nil
	}

	// Check password history
	reused, err := s.isPasswordReused(ctx, userID, newPassword)
	if err != nil {
		s.logger.Error("Failed to check password history", zap.Error(err))
		// Continue without history check on error
	} else if reused {
		result.Valid = false
		result.Errors = append(result.Errors, fmt.Sprintf("Cannot reuse any of your last %d passwords", PasswordHistoryLimit))
	}

	return result, nil
}

// SetPassword sets a new password and records it in history
func (s *PasswordService) SetPassword(ctx context.Context, userID uuid.UUID, newPassword string) (string, error) {
	// Hash the new password
	hash, err := s.HashPassword(newPassword)
	if err != nil {
		return "", err
	}

	// Add to password history
	if err := s.addToHistory(ctx, userID, hash); err != nil {
		s.logger.Error("Failed to add password to history", zap.Error(err))
		// Continue even if history fails
	}

	// Update last password change timestamp
	if err := s.updateLastPasswordChange(ctx, userID); err != nil {
		s.logger.Error("Failed to update last password change", zap.Error(err))
	}

	return hash, nil
}

// IsPasswordExpired checks if user's password has expired
func (s *PasswordService) IsPasswordExpired(ctx context.Context, userID uuid.UUID) (bool, error) {
	if PasswordExpirationDays == 0 {
		return false, nil
	}

	var lastChange sql.NullTime
	err := s.db.QueryRowContext(ctx,
		"SELECT last_password_change FROM users WHERE id = $1",
		userID).Scan(&lastChange)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("failed to get last password change: %w", err)
	}

	if !lastChange.Valid {
		return true, nil // No password change recorded, consider expired
	}

	expirationDate := lastChange.Time.AddDate(0, 0, PasswordExpirationDays)
	return time.Now().After(expirationDate), nil
}

// GetPasswordExpirationDate returns when the password will expire
func (s *PasswordService) GetPasswordExpirationDate(ctx context.Context, userID uuid.UUID) (*time.Time, error) {
	if PasswordExpirationDays == 0 {
		return nil, nil
	}

	var lastChange sql.NullTime
	err := s.db.QueryRowContext(ctx,
		"SELECT last_password_change FROM users WHERE id = $1",
		userID).Scan(&lastChange)
	if err != nil {
		return nil, fmt.Errorf("failed to get last password change: %w", err)
	}

	if !lastChange.Valid {
		return nil, nil
	}

	expirationDate := lastChange.Time.AddDate(0, 0, PasswordExpirationDays)
	return &expirationDate, nil
}

// isPasswordReused checks if password matches any in history
func (s *PasswordService) isPasswordReused(ctx context.Context, userID uuid.UUID, password string) (bool, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT password_hash FROM password_history 
		 WHERE user_id = $1 
		 ORDER BY created_at DESC 
		 LIMIT $2`,
		userID, PasswordHistoryLimit)
	if err != nil {
		return false, fmt.Errorf("failed to query password history: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			return false, fmt.Errorf("failed to scan password hash: %w", err)
		}
		if s.VerifyPassword(password, hash) {
			return true, nil
		}
	}

	return false, rows.Err()
}

// addToHistory adds a password hash to the user's history
func (s *PasswordService) addToHistory(ctx context.Context, userID uuid.UUID, hash string) error {
	// Insert new password
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO password_history (id, user_id, password_hash, created_at)
		 VALUES ($1, $2, $3, $4)`,
		uuid.New(), userID, hash, time.Now())
	if err != nil {
		return fmt.Errorf("failed to insert password history: %w", err)
	}

	// Clean up old entries beyond limit
	_, err = s.db.ExecContext(ctx,
		`DELETE FROM password_history 
		 WHERE user_id = $1 
		 AND id NOT IN (
			 SELECT id FROM password_history 
			 WHERE user_id = $1 
			 ORDER BY created_at DESC 
			 LIMIT $2
		 )`,
		userID, PasswordHistoryLimit)
	if err != nil {
		s.logger.Warn("Failed to cleanup old password history", zap.Error(err))
	}

	return nil
}

// updateLastPasswordChange updates the last password change timestamp
func (s *PasswordService) updateLastPasswordChange(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE users SET last_password_change = $1 WHERE id = $2",
		time.Now(), userID)
	return err
}
