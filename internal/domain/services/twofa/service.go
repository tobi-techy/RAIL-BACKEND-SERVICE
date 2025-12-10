package twofa

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/pkg/crypto"
)

type Service struct {
	db            *sql.DB
	logger        *zap.Logger
	encryptionKey string
}

type TwoFASetup struct {
	Secret    string   `json:"secret"`
	QRCodeURL string   `json:"qr_code_url"`
	BackupCodes []string `json:"backup_codes"`
}

type TwoFAStatus struct {
	IsEnabled   bool      `json:"is_enabled"`
	VerifiedAt  *time.Time `json:"verified_at,omitempty"`
	BackupCodesCount int  `json:"backup_codes_count"`
}

func NewService(db *sql.DB, logger *zap.Logger, encryptionKey string) *Service {
	return &Service{
		db:            db,
		logger:        logger,
		encryptionKey: encryptionKey,
	}
}

// GenerateSecret generates a new 2FA secret for a user
func (s *Service) GenerateSecret(ctx context.Context, userID uuid.UUID, userEmail string) (*TwoFASetup, error) {
	// Check if 2FA is already enabled
	var exists bool
	err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM user_2fa WHERE user_id = $1 AND is_enabled = true)", userID).Scan(&exists)
	if err != nil {
		return nil, fmt.Errorf("failed to check existing 2FA: %w", err)
	}
	if exists {
		return nil, fmt.Errorf("2FA is already enabled for this user")
	}

	// Generate secret
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "STACK",
		AccountName: userEmail,
		SecretSize:  32,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to generate TOTP key: %w", err)
	}

	// Generate backup codes
	backupCodes := s.generateBackupCodes(8)

	// Encrypt secret and backup codes
	encryptedSecret, err := crypto.Encrypt(key.Secret(), s.encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt secret: %w", err)
	}

	encryptedBackupCodes := make([]string, len(backupCodes))
	for i, code := range backupCodes {
		encrypted, err := crypto.Encrypt(code, s.encryptionKey)
		if err != nil {
			return nil, fmt.Errorf("failed to encrypt backup code: %w", err)
		}
		encryptedBackupCodes[i] = encrypted
	}

	// Store in database (not enabled yet)
	query := `
		INSERT INTO user_2fa (user_id, secret_encrypted, backup_codes_encrypted, is_enabled)
		VALUES ($1, $2, $3, false)
		ON CONFLICT (user_id) DO UPDATE SET
			secret_encrypted = EXCLUDED.secret_encrypted,
			backup_codes_encrypted = EXCLUDED.backup_codes_encrypted,
			is_enabled = false,
			verified_at = NULL,
			updated_at = NOW()`

	_, err = s.db.ExecContext(ctx, query, userID, encryptedSecret, encryptedBackupCodes)
	if err != nil {
		return nil, fmt.Errorf("failed to store 2FA setup: %w", err)
	}

	return &TwoFASetup{
		Secret:      key.Secret(),
		QRCodeURL:   key.URL(),
		BackupCodes: backupCodes,
	}, nil
}

// VerifyAndEnable verifies a TOTP code and enables 2FA
func (s *Service) VerifyAndEnable(ctx context.Context, userID uuid.UUID, code string) error {
	// Get encrypted secret
	var encryptedSecret string
	var isEnabled bool
	err := s.db.QueryRowContext(ctx, 
		"SELECT secret_encrypted, is_enabled FROM user_2fa WHERE user_id = $1", 
		userID).Scan(&encryptedSecret, &isEnabled)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("2FA not set up for this user")
		}
		return fmt.Errorf("failed to get 2FA secret: %w", err)
	}

	if isEnabled {
		return fmt.Errorf("2FA is already enabled")
	}

	// Decrypt secret
	secret, err := crypto.Decrypt(encryptedSecret, s.encryptionKey)
	if err != nil {
		return fmt.Errorf("failed to decrypt secret: %w", err)
	}

	// Verify TOTP code
	valid := totp.Validate(code, secret)
	if !valid {
		return fmt.Errorf("invalid TOTP code")
	}

	// Enable 2FA
	_, err = s.db.ExecContext(ctx, 
		"UPDATE user_2fa SET is_enabled = true, verified_at = NOW(), updated_at = NOW() WHERE user_id = $1", 
		userID)
	if err != nil {
		return fmt.Errorf("failed to enable 2FA: %w", err)
	}

	s.logger.Info("2FA enabled for user", zap.String("user_id", userID.String()))
	return nil
}

// Verify verifies a TOTP code or backup code
func (s *Service) Verify(ctx context.Context, userID uuid.UUID, code string) (bool, error) {
	// Get 2FA data
	var encryptedSecret string
	var encryptedBackupCodes []string
	var isEnabled bool
	err := s.db.QueryRowContext(ctx, 
		"SELECT secret_encrypted, backup_codes_encrypted, is_enabled FROM user_2fa WHERE user_id = $1", 
		userID).Scan(&encryptedSecret, &encryptedBackupCodes, &isEnabled)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, fmt.Errorf("2FA not enabled for this user")
		}
		return false, fmt.Errorf("failed to get 2FA data: %w", err)
	}

	if !isEnabled {
		return false, fmt.Errorf("2FA is not enabled")
	}

	// Try TOTP first
	secret, err := crypto.Decrypt(encryptedSecret, s.encryptionKey)
	if err != nil {
		return false, fmt.Errorf("failed to decrypt secret: %w", err)
	}

	if totp.Validate(code, secret) {
		return true, nil
	}

	// Try backup codes
	for i, encryptedCode := range encryptedBackupCodes {
		if encryptedCode == "" {
			continue // Already used
		}

		backupCode, err := crypto.Decrypt(encryptedCode, s.encryptionKey)
		if err != nil {
			continue
		}

		if backupCode == code {
			// Mark backup code as used
			encryptedBackupCodes[i] = ""
			_, err = s.db.ExecContext(ctx, 
				"UPDATE user_2fa SET backup_codes_encrypted = $1, updated_at = NOW() WHERE user_id = $2", 
				encryptedBackupCodes, userID)
			if err != nil {
				s.logger.Error("Failed to mark backup code as used", zap.Error(err))
			}
			return true, nil
		}
	}

	return false, nil
}

// Disable disables 2FA for a user
func (s *Service) Disable(ctx context.Context, userID uuid.UUID, code string) error {
	// Verify code first
	valid, err := s.Verify(ctx, userID, code)
	if err != nil {
		return err
	}
	if !valid {
		return fmt.Errorf("invalid verification code")
	}

	// Disable 2FA
	_, err = s.db.ExecContext(ctx, "DELETE FROM user_2fa WHERE user_id = $1", userID)
	if err != nil {
		return fmt.Errorf("failed to disable 2FA: %w", err)
	}

	s.logger.Info("2FA disabled for user", zap.String("user_id", userID.String()))
	return nil
}

// GetStatus returns 2FA status for a user
func (s *Service) GetStatus(ctx context.Context, userID uuid.UUID) (*TwoFAStatus, error) {
	var isEnabled bool
	var verifiedAt *time.Time
	var encryptedBackupCodes []string

	err := s.db.QueryRowContext(ctx, 
		"SELECT is_enabled, verified_at, backup_codes_encrypted FROM user_2fa WHERE user_id = $1", 
		userID).Scan(&isEnabled, &verifiedAt, &encryptedBackupCodes)
	if err != nil {
		if err == sql.ErrNoRows {
			return &TwoFAStatus{IsEnabled: false}, nil
		}
		return nil, fmt.Errorf("failed to get 2FA status: %w", err)
	}

	// Count unused backup codes
	backupCodesCount := 0
	for _, code := range encryptedBackupCodes {
		if code != "" {
			backupCodesCount++
		}
	}

	return &TwoFAStatus{
		IsEnabled:        isEnabled,
		VerifiedAt:       verifiedAt,
		BackupCodesCount: backupCodesCount,
	}, nil
}

// RegenerateBackupCodes generates new backup codes
func (s *Service) RegenerateBackupCodes(ctx context.Context, userID uuid.UUID, code string) ([]string, error) {
	// Verify current code
	valid, err := s.Verify(ctx, userID, code)
	if err != nil {
		return nil, err
	}
	if !valid {
		return nil, fmt.Errorf("invalid verification code")
	}

	// Generate new backup codes
	backupCodes := s.generateBackupCodes(8)
	encryptedBackupCodes := make([]string, len(backupCodes))
	for i, code := range backupCodes {
		encrypted, err := crypto.Encrypt(code, s.encryptionKey)
		if err != nil {
			return nil, fmt.Errorf("failed to encrypt backup code: %w", err)
		}
		encryptedBackupCodes[i] = encrypted
	}

	// Update database
	_, err = s.db.ExecContext(ctx, 
		"UPDATE user_2fa SET backup_codes_encrypted = $1, updated_at = NOW() WHERE user_id = $2", 
		encryptedBackupCodes, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to update backup codes: %w", err)
	}

	return backupCodes, nil
}

func (s *Service) generateBackupCodes(count int) []string {
	codes := make([]string, count)
	for i := 0; i < count; i++ {
		// Generate 8-character backup code
		bytes := make([]byte, 5)
		rand.Read(bytes)
		code := base32.StdEncoding.EncodeToString(bytes)
		code = strings.TrimRight(code, "=")
		codes[i] = code[:8]
	}
	return codes
}