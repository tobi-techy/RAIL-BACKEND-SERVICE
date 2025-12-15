package security

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"math/big"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/pkg/crypto"
)

type MFAMethod string

const (
	MFAMethodTOTP    MFAMethod = "totp"
	MFAMethodSMS     MFAMethod = "sms"
	MFAMethodWebAuthn MFAMethod = "webauthn"
)

type MFAService struct {
	db            *sql.DB
	redis         *redis.Client
	logger        *zap.Logger
	encryptionKey string
	smsProvider   SMSProvider
	highValueThreshold decimal.Decimal
}

type SMSProvider interface {
	SendSMS(ctx context.Context, phoneNumber, message string) error
}

type MFASettings struct {
	UserID          uuid.UUID
	PrimaryMethod   MFAMethod
	FallbackMethod  *MFAMethod
	PhoneNumber     string
	MFARequired     bool
	MFAEnforcedAt   *time.Time
	GracePeriodEnds *time.Time
	TOTPEnabled     bool
	WebAuthnEnabled bool
	BackupCodesLeft int
}

type MFAVerifyResult struct {
	Valid           bool
	Method          MFAMethod
	RequiresMFA     bool
	AvailableMethods []MFAMethod
	GracePeriod     bool
}

func NewMFAService(db *sql.DB, redis *redis.Client, logger *zap.Logger, encryptionKey string, smsProvider SMSProvider) *MFAService {
	return &MFAService{
		db:                 db,
		redis:              redis,
		logger:             logger,
		encryptionKey:      encryptionKey,
		smsProvider:        smsProvider,
		highValueThreshold: decimal.NewFromInt(50000),
	}
}

// GetMFASettings returns user's MFA configuration
func (s *MFAService) GetMFASettings(ctx context.Context, userID uuid.UUID) (*MFASettings, error) {
	settings := &MFASettings{UserID: userID}

	// Get MFA settings
	var phoneEncrypted sql.NullString
	var fallbackMethod sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT primary_method, fallback_method, phone_number_encrypted, mfa_required, mfa_enforced_at, grace_period_ends
		FROM user_mfa_settings WHERE user_id = $1`, userID).Scan(
		&settings.PrimaryMethod, &fallbackMethod, &phoneEncrypted,
		&settings.MFARequired, &settings.MFAEnforcedAt, &settings.GracePeriodEnds)
	
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("failed to get MFA settings: %w", err)
	}

	if fallbackMethod.Valid {
		method := MFAMethod(fallbackMethod.String)
		settings.FallbackMethod = &method
	}

	if phoneEncrypted.Valid {
		phone, _ := crypto.Decrypt(phoneEncrypted.String, s.encryptionKey)
		settings.PhoneNumber = phone
	}

	// Check TOTP status
	var totpEnabled bool
	s.db.QueryRowContext(ctx, "SELECT is_enabled FROM user_2fa WHERE user_id = $1", userID).Scan(&totpEnabled)
	settings.TOTPEnabled = totpEnabled

	// Check WebAuthn credentials
	var webauthnCount int
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM webauthn_credentials WHERE user_id = $1", userID).Scan(&webauthnCount)
	settings.WebAuthnEnabled = webauthnCount > 0

	// Count backup codes
	var backupCodes []string
	s.db.QueryRowContext(ctx, "SELECT backup_codes_encrypted FROM user_2fa WHERE user_id = $1", userID).Scan(&backupCodes)
	for _, code := range backupCodes {
		if code != "" {
			settings.BackupCodesLeft++
		}
	}

	return settings, nil
}

// RequiresMFA checks if user must complete MFA
func (s *MFAService) RequiresMFA(ctx context.Context, userID uuid.UUID) (*MFAVerifyResult, error) {
	result := &MFAVerifyResult{AvailableMethods: []MFAMethod{}}

	settings, err := s.GetMFASettings(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Check if in grace period
	if settings.GracePeriodEnds != nil && time.Now().Before(*settings.GracePeriodEnds) {
		result.GracePeriod = true
		return result, nil
	}

	// Check if MFA is required
	if !settings.MFARequired && !s.isHighValueAccount(ctx, userID) {
		return result, nil
	}

	result.RequiresMFA = true

	// Determine available methods
	if settings.TOTPEnabled {
		result.AvailableMethods = append(result.AvailableMethods, MFAMethodTOTP)
	}
	if settings.PhoneNumber != "" {
		result.AvailableMethods = append(result.AvailableMethods, MFAMethodSMS)
	}
	if settings.WebAuthnEnabled {
		result.AvailableMethods = append(result.AvailableMethods, MFAMethodWebAuthn)
	}

	return result, nil
}

// SendSMSCode sends an SMS verification code
func (s *MFAService) SendSMSCode(ctx context.Context, userID uuid.UUID) error {
	settings, err := s.GetMFASettings(ctx, userID)
	if err != nil {
		return err
	}

	if settings.PhoneNumber == "" {
		return fmt.Errorf("no phone number configured")
	}

	// Generate 6-digit code
	code, err := s.generateCode(6)
	if err != nil {
		return fmt.Errorf("failed to generate code: %w", err)
	}

	// Hash and store code
	codeHash := s.hashCode(code)
	expiresAt := time.Now().Add(5 * time.Minute)

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO sms_mfa_codes (user_id, phone_number, code_hash, expires_at)
		VALUES ($1, $2, $3, $4)`,
		userID, settings.PhoneNumber, codeHash, expiresAt)
	if err != nil {
		return fmt.Errorf("failed to store SMS code: %w", err)
	}

	// Send SMS
	if s.smsProvider != nil {
		message := fmt.Sprintf("Your RAIL verification code is: %s. Valid for 5 minutes.", code)
		if err := s.smsProvider.SendSMS(ctx, settings.PhoneNumber, message); err != nil {
			s.logger.Error("Failed to send SMS", zap.Error(err))
			return fmt.Errorf("failed to send SMS: %w", err)
		}
	}

	s.logger.Info("SMS MFA code sent", zap.String("user_id", userID.String()))
	return nil
}

// VerifySMSCode verifies an SMS code
func (s *MFAService) VerifySMSCode(ctx context.Context, userID uuid.UUID, code string) (bool, error) {
	codeHash := s.hashCode(code)

	var id uuid.UUID
	var attempts int
	err := s.db.QueryRowContext(ctx, `
		SELECT id, attempts FROM sms_mfa_codes 
		WHERE user_id = $1 AND code_hash = $2 AND expires_at > NOW() AND verified = false
		ORDER BY created_at DESC LIMIT 1`,
		userID, codeHash).Scan(&id, &attempts)

	if err == sql.ErrNoRows {
		// Increment attempts on latest code
		s.db.ExecContext(ctx, `
			UPDATE sms_mfa_codes SET attempts = attempts + 1 
			WHERE user_id = $1 AND expires_at > NOW() AND verified = false`,
			userID)
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to verify code: %w", err)
	}

	// Mark as verified
	s.db.ExecContext(ctx, "UPDATE sms_mfa_codes SET verified = true WHERE id = $1", id)

	return true, nil
}

// VerifyTOTP verifies a TOTP code
func (s *MFAService) VerifyTOTP(ctx context.Context, userID uuid.UUID, code string) (bool, error) {
	var encryptedSecret string
	var isEnabled bool
	err := s.db.QueryRowContext(ctx,
		"SELECT secret_encrypted, is_enabled FROM user_2fa WHERE user_id = $1",
		userID).Scan(&encryptedSecret, &isEnabled)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("failed to get TOTP secret: %w", err)
	}

	if !isEnabled {
		return false, nil
	}

	secret, err := crypto.Decrypt(encryptedSecret, s.encryptionKey)
	if err != nil {
		return false, fmt.Errorf("failed to decrypt secret: %w", err)
	}

	return totp.Validate(code, secret), nil
}

// VerifyBackupCode verifies a backup code
func (s *MFAService) VerifyBackupCode(ctx context.Context, userID uuid.UUID, code string) (bool, error) {
	var encryptedBackupCodes []string
	err := s.db.QueryRowContext(ctx,
		"SELECT backup_codes_encrypted FROM user_2fa WHERE user_id = $1",
		userID).Scan(&encryptedBackupCodes)
	if err != nil {
		return false, nil
	}

	for i, encryptedCode := range encryptedBackupCodes {
		if encryptedCode == "" {
			continue
		}
		backupCode, err := crypto.Decrypt(encryptedCode, s.encryptionKey)
		if err != nil {
			continue
		}
		if backupCode == code {
			// Mark as used
			encryptedBackupCodes[i] = ""
			s.db.ExecContext(ctx,
				"UPDATE user_2fa SET backup_codes_encrypted = $1 WHERE user_id = $2",
				encryptedBackupCodes, userID)
			return true, nil
		}
	}

	return false, nil
}

// VerifyAny verifies MFA using any available method
func (s *MFAService) VerifyAny(ctx context.Context, userID uuid.UUID, code string, method MFAMethod) (*MFAVerifyResult, error) {
	result := &MFAVerifyResult{Method: method}

	var valid bool
	var err error

	switch method {
	case MFAMethodTOTP:
		valid, err = s.VerifyTOTP(ctx, userID, code)
	case MFAMethodSMS:
		valid, err = s.VerifySMSCode(ctx, userID, code)
	default:
		// Try backup code
		valid, err = s.VerifyBackupCode(ctx, userID, code)
	}

	if err != nil {
		return nil, err
	}

	result.Valid = valid
	return result, nil
}

// EnforceMFA enables mandatory MFA for a user
func (s *MFAService) EnforceMFA(ctx context.Context, userID uuid.UUID, gracePeriodDays int) error {
	now := time.Now()
	gracePeriodEnds := now.AddDate(0, 0, gracePeriodDays)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO user_mfa_settings (user_id, mfa_required, mfa_enforced_at, grace_period_ends)
		VALUES ($1, true, $2, $3)
		ON CONFLICT (user_id) DO UPDATE SET
			mfa_required = true,
			mfa_enforced_at = EXCLUDED.mfa_enforced_at,
			grace_period_ends = EXCLUDED.grace_period_ends,
			updated_at = NOW()`,
		userID, now, gracePeriodEnds)
	if err != nil {
		return fmt.Errorf("failed to enforce MFA: %w", err)
	}

	// Update user record
	s.db.ExecContext(ctx, "UPDATE users SET mfa_enforced = true WHERE id = $1", userID)

	s.logger.Info("MFA enforced for user",
		zap.String("user_id", userID.String()),
		zap.Time("grace_period_ends", gracePeriodEnds))

	return nil
}

// SetupSMSMFA configures SMS as MFA method
func (s *MFAService) SetupSMSMFA(ctx context.Context, userID uuid.UUID, phoneNumber string) error {
	encryptedPhone, err := crypto.Encrypt(phoneNumber, s.encryptionKey)
	if err != nil {
		return fmt.Errorf("failed to encrypt phone: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO user_mfa_settings (user_id, phone_number_encrypted, primary_method)
		VALUES ($1, $2, 'sms')
		ON CONFLICT (user_id) DO UPDATE SET
			phone_number_encrypted = EXCLUDED.phone_number_encrypted,
			updated_at = NOW()`,
		userID, encryptedPhone)
	if err != nil {
		return fmt.Errorf("failed to setup SMS MFA: %w", err)
	}

	return nil
}

// EnforceForHighValueAccounts enforces MFA for accounts above threshold
func (s *MFAService) EnforceForHighValueAccounts(ctx context.Context) (int, error) {
	// Find users with high account value who don't have MFA enforced
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.id FROM users u
		LEFT JOIN user_mfa_settings m ON u.id = m.user_id
		WHERE u.account_value_tier = 'high_value'
		AND (m.mfa_required IS NULL OR m.mfa_required = false)`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var userID uuid.UUID
		if err := rows.Scan(&userID); err != nil {
			continue
		}
		if err := s.EnforceMFA(ctx, userID, 7); err != nil {
			s.logger.Error("Failed to enforce MFA", zap.String("user_id", userID.String()), zap.Error(err))
			continue
		}
		count++
	}

	return count, nil
}

func (s *MFAService) isHighValueAccount(ctx context.Context, userID uuid.UUID) bool {
	var tier string
	err := s.db.QueryRowContext(ctx, "SELECT account_value_tier FROM users WHERE id = $1", userID).Scan(&tier)
	if err != nil {
		return false
	}
	return tier == "high_value"
}

func (s *MFAService) generateCode(length int) (string, error) {
	const digits = "0123456789"
	code := make([]byte, length)
	for i := range code {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(digits))))
		if err != nil {
			return "", err
		}
		code[i] = digits[n.Int64()]
	}
	return string(code), nil
}

func (s *MFAService) hashCode(code string) string {
	hash := sha256.Sum256([]byte(code))
	return hex.EncodeToString(hash[:])
}
