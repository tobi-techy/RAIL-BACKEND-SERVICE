package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/crypto"
)

// UserRepository implements the user repository interface using PostgreSQL
type UserRepository struct {
	db     *sql.DB
	logger *zap.Logger
}

// NewUserRepository creates a new user repository
func NewUserRepository(db *sql.DB, logger *zap.Logger) *UserRepository {
	return &UserRepository{
		db:     db,
		logger: logger,
	}
}

// Create creates a new user
func (r *UserRepository) Create(ctx context.Context, user *entities.UserProfile) error {
	query := `
		INSERT INTO users (
			id, email, phone, first_name, last_name, date_of_birth,
			auth_provider_id, email_verified, phone_verified, 
			onboarding_status, kyc_status, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
		)`

	_, err := r.db.ExecContext(ctx, query,
		user.ID,
		user.Email,
		user.Phone,
		user.FirstName,
		user.LastName,
		user.DateOfBirth,
		user.AuthProviderID,
		user.EmailVerified,
		user.PhoneVerified,
		string(user.OnboardingStatus),
		user.KYCStatus,
		user.CreatedAt,
		user.UpdatedAt,
	)

	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			return fmt.Errorf("user with email already exists: %w", err)
		}
		r.logger.Error("Failed to create user", zap.Error(err), zap.String("email", user.Email))
		return fmt.Errorf("failed to create user: %w", err)
	}

	r.logger.Debug("User created successfully", zap.String("user_id", user.ID.String()))
	return nil
}

// GetByID retrieves a user by ID
func (r *UserRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.UserProfile, error) {
	query := `
        SELECT id, email, phone, first_name, last_name, date_of_birth,
               auth_provider_id, email_verified, phone_verified,
               onboarding_status, kyc_status, kyc_approved_at, kyc_rejection_reason,
               is_active, created_at, updated_at
        FROM users 
        WHERE id = $1`

	user := &entities.UserProfile{}
	var kycApprovedAt sql.NullTime
	var kycRejectionReason sql.NullString
	var firstName, lastName sql.NullString
	var dateOfBirth sql.NullTime

	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&user.ID,
		&user.Email,
		&user.Phone,
		&firstName,
		&lastName,
		&dateOfBirth,
		&user.AuthProviderID,
		&user.EmailVerified,
		&user.PhoneVerified,
		&user.OnboardingStatus,
		&user.KYCStatus,
		&kycApprovedAt,
		&kycRejectionReason,
		&user.IsActive,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("user not found")
		}
		r.logger.Error("Failed to get user by ID", zap.Error(err), zap.String("user_id", id.String()))
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	if kycApprovedAt.Valid {
		user.KYCApprovedAt = &kycApprovedAt.Time
	}
	if kycRejectionReason.Valid {
		user.KYCRejectionReason = &kycRejectionReason.String
	}
	// firstName, lastName, dateOfBirth fields not yet implemented in UserProfile entity

	return user, nil
}

// GetByEmail retrieves a user by email
func (r *UserRepository) GetByEmail(ctx context.Context, email string) (*entities.UserProfile, error) {
	query := `
        SELECT id, email, phone, first_name, last_name, date_of_birth,
               auth_provider_id, email_verified, phone_verified,
               onboarding_status, kyc_status, kyc_approved_at, kyc_rejection_reason,
               is_active, created_at, updated_at
        FROM users 
        WHERE email = $1`

	user := &entities.UserProfile{}
	var kycApprovedAt sql.NullTime
	var kycRejectionReason sql.NullString
	var firstName, lastName sql.NullString
	var dateOfBirth sql.NullTime

	err := r.db.QueryRowContext(ctx, query, email).Scan(
		&user.ID,
		&user.Email,
		&user.Phone,
		&firstName,
		&lastName,
		&dateOfBirth,
		&user.AuthProviderID,
		&user.EmailVerified,
		&user.PhoneVerified,
		&user.OnboardingStatus,
		&user.KYCStatus,
		&kycApprovedAt,
		&kycRejectionReason,
		&user.IsActive,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("user not found")
		}
		r.logger.Error("Failed to get user by email", zap.Error(err), zap.String("email", email))
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	if kycApprovedAt.Valid {
		user.KYCApprovedAt = &kycApprovedAt.Time
	}
	if kycRejectionReason.Valid {
		user.KYCRejectionReason = &kycRejectionReason.String
	}

	return user, nil
}

// GetByAuthProviderID retrieves a user by auth provider ID
func (r *UserRepository) GetByAuthProviderID(ctx context.Context, authProviderID string) (*entities.UserProfile, error) {
	query := `
        SELECT id, email, phone, first_name, last_name, date_of_birth,
               auth_provider_id, email_verified, phone_verified,
               onboarding_status, kyc_status, kyc_approved_at, kyc_rejection_reason,
               is_active, created_at, updated_at
        FROM users 
        WHERE auth_provider_id = $1`

	user := &entities.UserProfile{}
	var kycApprovedAt sql.NullTime
	var kycRejectionReason sql.NullString
	var firstName, lastName sql.NullString
	var dateOfBirth sql.NullTime

	err := r.db.QueryRowContext(ctx, query, authProviderID).Scan(
		&user.ID,
		&user.Email,
		&user.Phone,
		&firstName,
		&lastName,
		&dateOfBirth,
		&user.AuthProviderID,
		&user.EmailVerified,
		&user.PhoneVerified,
		&user.OnboardingStatus,
		&user.KYCStatus,
		&kycApprovedAt,
		&kycRejectionReason,
		&user.IsActive,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("user not found")
		}
		r.logger.Error("Failed to get user by auth provider ID", zap.Error(err), zap.String("auth_provider_id", authProviderID))
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	if kycApprovedAt.Valid {
		user.KYCApprovedAt = &kycApprovedAt.Time
	}
	if kycRejectionReason.Valid {
		user.KYCRejectionReason = &kycRejectionReason.String
	}

	return user, nil
}

// Update updates a user
func (r *UserRepository) Update(ctx context.Context, user *entities.UserProfile) error {
	query := `
		UPDATE users SET 
			email = $2, phone = $3, first_name = $4, last_name = $5, 
			date_of_birth = $6, auth_provider_id = $7, email_verified = $8, 
			phone_verified = $9, onboarding_status = $10, kyc_status = $11, 
			kyc_approved_at = $12, kyc_rejection_reason = $13, updated_at = $14
		WHERE id = $1`

	_, err := r.db.ExecContext(ctx, query,
		user.ID,
		user.Email,
		user.Phone,
		"",  // first_name - not yet implemented in UserProfile
		"",  // last_name - not yet implemented in UserProfile
		nil, // date_of_birth - not yet implemented in UserProfile
		user.AuthProviderID,
		user.EmailVerified,
		user.PhoneVerified,
		string(user.OnboardingStatus),
		user.KYCStatus,
		user.KYCApprovedAt,
		user.KYCRejectionReason,
		time.Now(),
	)

	if err != nil {
		r.logger.Error("Failed to update user", zap.Error(err), zap.String("user_id", user.ID.String()))
		return fmt.Errorf("failed to update user: %w", err)
	}

	r.logger.Debug("User updated successfully", zap.String("user_id", user.ID.String()))
	return nil
}

// UpdateOnboardingStatus updates the onboarding status
func (r *UserRepository) UpdateOnboardingStatus(ctx context.Context, userID uuid.UUID, status entities.OnboardingStatus) error {
	query := `UPDATE users SET onboarding_status = $2, updated_at = $3 WHERE id = $1`

	_, err := r.db.ExecContext(ctx, query, userID, string(status), time.Now())
	if err != nil {
		r.logger.Error("Failed to update onboarding status", zap.Error(err), zap.String("user_id", userID.String()))
		return fmt.Errorf("failed to update onboarding status: %w", err)
	}

	r.logger.Debug("Onboarding status updated", zap.String("user_id", userID.String()), zap.String("status", string(status)))
	return nil
}

// UpdateKYCStatus updates the KYC status and related fields
func (r *UserRepository) UpdateKYCStatus(ctx context.Context, userID uuid.UUID, status string, approvedAt *time.Time, rejectionReason *string) error {
	query := `
		UPDATE users SET 
			kyc_status = $2, 
			kyc_approved_at = $3, 
			kyc_rejection_reason = $4, 
			updated_at = $5 
		WHERE id = $1`

	_, err := r.db.ExecContext(ctx, query, userID, status, approvedAt, rejectionReason, time.Now())
	if err != nil {
		r.logger.Error("Failed to update KYC status", zap.Error(err), zap.String("user_id", userID.String()))
		return fmt.Errorf("failed to update KYC status: %w", err)
	}

	r.logger.Debug("KYC status updated", zap.String("user_id", userID.String()), zap.String("status", status))
	return nil
}

// UpdateKYCProvider sets the KYC provider reference and status
func (r *UserRepository) UpdateKYCProvider(ctx context.Context, userID uuid.UUID, providerRef string, status entities.KYCStatus) error {
	query := `
		UPDATE users SET 
			kyc_provider_ref = $2,
			kyc_status = $3,
			updated_at = $4
		WHERE id = $1`

	_, err := r.db.ExecContext(ctx, query, userID, providerRef, string(status), time.Now())
	if err != nil {
		r.logger.Error("Failed to update KYC provider reference", zap.Error(err), zap.String("user_id", userID.String()))
		return fmt.Errorf("failed to update KYC provider reference: %w", err)
	}

	r.logger.Info("Updated KYC provider reference",
		zap.String("user_id", userID.String()),
		zap.String("provider_ref", providerRef),
		zap.String("status", string(status)))

	return nil
}

// === Authentication-related methods ===

// CreateUserFromAuth creates a new user with authentication data
func (r *UserRepository) CreateUserFromAuth(ctx context.Context, req *entities.RegisterRequest) (*entities.User, error) {
	// Hash password
	passwordHash, err := crypto.HashPassword(req.Password)
	if err != nil {
		r.logger.Error("Failed to hash password", zap.Error(err))
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}

	// Create user entity
	user := &entities.User{
		ID:               uuid.New(),
		Email:            req.Email,
		Phone:            req.Phone,
		PasswordHash:     passwordHash,
		EmailVerified:    false, // Will be set to true after email verification
		PhoneVerified:    false, // Will be set to true after phone verification (if phone provided)
		OnboardingStatus: entities.OnboardingStatusStarted,
		KYCStatus:        string(entities.KYCStatusPending),
		Role:             "user",
		IsActive:         true,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}

	// Insert into database
	query := `
		INSERT INTO users (
			id, email, phone, password_hash, auth_provider_id, 
			email_verified, phone_verified, onboarding_status, kyc_status,
			role, is_active, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
		)`

	_, err = r.db.ExecContext(ctx, query,
		user.ID,
		user.Email,
		user.Phone,
		user.PasswordHash,
		user.AuthProviderID,
		user.EmailVerified,
		user.PhoneVerified,
		string(user.OnboardingStatus),
		user.KYCStatus,
		user.Role,
		user.IsActive,
		user.CreatedAt,
		user.UpdatedAt,
	)

	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			r.logger.Warn("User registration failed - email already exists", zap.String("email", req.Email))
			return nil, fmt.Errorf("user with email already exists")
		}
		r.logger.Error("Failed to create user", zap.Error(err), zap.String("email", req.Email))
		return nil, fmt.Errorf("failed to create user: %w", err)
	}

	r.logger.Info("User created successfully",
		zap.String("user_id", user.ID.String()),
		zap.String("email", user.Email))

	return user, nil
}

// CreateUserWithHash creates a new user with a pre-hashed password (used after verification)
func (r *UserRepository) CreateUserWithHash(ctx context.Context, email string, phone *string, passwordHash string) (*entities.User, error) {
	user := &entities.User{
		ID:               uuid.New(),
		Email:            email,
		Phone:            phone,
		PasswordHash:     passwordHash,
		EmailVerified:    false,
		PhoneVerified:    false,
		OnboardingStatus: entities.OnboardingStatusStarted,
		KYCStatus:        string(entities.KYCStatusPending),
		Role:             "user",
		IsActive:         true,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}

	query := `
		INSERT INTO users (
			id, email, phone, password_hash, auth_provider_id, 
			email_verified, phone_verified, onboarding_status, kyc_status,
			role, is_active, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
		)`

	_, err := r.db.ExecContext(ctx, query,
		user.ID,
		user.Email,
		user.Phone,
		user.PasswordHash,
		user.AuthProviderID,
		user.EmailVerified,
		user.PhoneVerified,
		string(user.OnboardingStatus),
		user.KYCStatus,
		user.Role,
		user.IsActive,
		user.CreatedAt,
		user.UpdatedAt,
	)

	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			return nil, fmt.Errorf("user already exists")
		}
		r.logger.Error("Failed to create user", zap.Error(err))
		return nil, fmt.Errorf("failed to create user: %w", err)
	}

	r.logger.Info("User created successfully", zap.String("user_id", user.ID.String()))
	return user, nil
}

// GetUserByEmailForLogin retrieves a user by email for login purposes (includes password hash)
func (r *UserRepository) GetUserByEmailForLogin(ctx context.Context, email string) (*entities.User, error) {
	query := `
		SELECT id, email, phone, password_hash, auth_provider_id,
		       email_verified, phone_verified, onboarding_status, kyc_status,
		       kyc_provider_ref, kyc_submitted_at, kyc_approved_at, kyc_rejection_reason,
		       role, is_active, last_login_at, created_at, updated_at
		FROM users 
		WHERE email = $1 AND is_active = true`

	user := &entities.User{}
	var kycSubmittedAt, kycApprovedAt, lastLoginAt sql.NullTime
	var kycRejectionReason, kycProviderRef sql.NullString

	err := r.db.QueryRowContext(ctx, query, email).Scan(
		&user.ID,
		&user.Email,
		&user.Phone,
		&user.PasswordHash,
		&user.AuthProviderID,
		&user.EmailVerified,
		&user.PhoneVerified,
		&user.OnboardingStatus,
		&user.KYCStatus,
		&kycProviderRef,
		&kycSubmittedAt,
		&kycApprovedAt,
		&kycRejectionReason,
		&user.Role,
		&user.IsActive,
		&lastLoginAt,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("user not found")
		}
		r.logger.Error("Failed to get user by email for login", zap.Error(err), zap.String("email", email))
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	// Handle nullable fields
	if kycProviderRef.Valid {
		user.KYCProviderRef = &kycProviderRef.String
	}
	if kycSubmittedAt.Valid {
		user.KYCSubmittedAt = &kycSubmittedAt.Time
	}
	if kycApprovedAt.Valid {
		user.KYCApprovedAt = &kycApprovedAt.Time
	}
	if kycRejectionReason.Valid {
		user.KYCRejectionReason = &kycRejectionReason.String
	}
	if lastLoginAt.Valid {
		user.LastLoginAt = &lastLoginAt.Time
	}

	return user, nil
}

// PhoneExists checks if a phone number already exists
func (r *UserRepository) PhoneExists(ctx context.Context, phone string) (bool, error) {
	query := `SELECT EXISTS(SELECT 1 FROM users WHERE phone = $1 AND is_active = true)`

	var exists bool
	err := r.db.QueryRowContext(ctx, query, phone).Scan(&exists)
	if err != nil {
		r.logger.Error("Failed to check phone existence", zap.Error(err), zap.String("phone", phone))
		return false, fmt.Errorf("failed to check phone existence: %w", err)
	}

	r.logger.Debug("Checked phone existence", zap.String("phone", phone), zap.Bool("exists", exists))
	return exists, nil
}

// GetUserByPhoneForLogin retrieves a user by phone for login purposes (includes password hash)
func (r *UserRepository) GetUserByPhoneForLogin(ctx context.Context, phone string) (*entities.User, error) {
	query := `
		SELECT id, email, phone, password_hash, auth_provider_id,
		       email_verified, phone_verified, onboarding_status, kyc_status,
		       kyc_provider_ref, kyc_submitted_at, kyc_approved_at, kyc_rejection_reason,
		       role, is_active, last_login_at, created_at, updated_at
		FROM users 
		WHERE phone = $1 AND is_active = true`

	user := &entities.User{}
	var kycSubmittedAt, kycApprovedAt, lastLoginAt sql.NullTime
	var kycRejectionReason, kycProviderRef sql.NullString

	err := r.db.QueryRowContext(ctx, query, phone).Scan(
		&user.ID,
		&user.Email,
		&user.Phone,
		&user.PasswordHash,
		&user.AuthProviderID,
		&user.EmailVerified,
		&user.PhoneVerified,
		&user.OnboardingStatus,
		&user.KYCStatus,
		&kycProviderRef,
		&kycSubmittedAt,
		&kycApprovedAt,
		&kycRejectionReason,
		&user.Role,
		&user.IsActive,
		&lastLoginAt,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("user not found")
		}
		r.logger.Error("Failed to get user by phone for login", zap.Error(err), zap.String("phone", phone))
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	// Handle nullable fields
	if kycProviderRef.Valid {
		user.KYCProviderRef = &kycProviderRef.String
	}
	if kycSubmittedAt.Valid {
		user.KYCSubmittedAt = &kycSubmittedAt.Time
	}
	if kycApprovedAt.Valid {
		user.KYCApprovedAt = &kycApprovedAt.Time
	}
	if kycRejectionReason.Valid {
		user.KYCRejectionReason = &kycRejectionReason.String
	}
	if lastLoginAt.Valid {
		user.LastLoginAt = &lastLoginAt.Time
	}

	return user, nil
}

// GetUserEntityByID retrieves a user as User entity by ID (excludes sensitive fields like password)
func (r *UserRepository) GetUserEntityByID(ctx context.Context, id uuid.UUID) (*entities.User, error) {
	query := `
		SELECT id, email, phone, auth_provider_id,
		       email_verified, phone_verified, onboarding_status, kyc_status,
		       kyc_provider_ref, kyc_submitted_at, kyc_approved_at, kyc_rejection_reason,
		       role, is_active, last_login_at, created_at, updated_at
		FROM users 
		WHERE id = $1 AND is_active = true`

	user := &entities.User{}
	var kycSubmittedAt, kycApprovedAt, lastLoginAt sql.NullTime
	var kycRejectionReason, kycProviderRef sql.NullString

	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&user.ID,
		&user.Email,
		&user.Phone,
		&user.AuthProviderID,
		&user.EmailVerified,
		&user.PhoneVerified,
		&user.OnboardingStatus,
		&user.KYCStatus,
		&kycProviderRef,
		&kycSubmittedAt,
		&kycApprovedAt,
		&kycRejectionReason,
		&user.Role,
		&user.IsActive,
		&lastLoginAt,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("user not found")
		}
		r.logger.Error("Failed to get user by ID", zap.Error(err), zap.String("user_id", id.String()))
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	// Handle nullable fields
	if kycProviderRef.Valid {
		user.KYCProviderRef = &kycProviderRef.String
	}
	if kycSubmittedAt.Valid {
		user.KYCSubmittedAt = &kycSubmittedAt.Time
	}
	if kycApprovedAt.Valid {
		user.KYCApprovedAt = &kycApprovedAt.Time
	}
	if kycRejectionReason.Valid {
		user.KYCRejectionReason = &kycRejectionReason.String
	}
	if lastLoginAt.Valid {
		user.LastLoginAt = &lastLoginAt.Time
	}

	return user, nil
}

// UpdateLastLogin updates the user's last login timestamp
func (r *UserRepository) UpdateLastLogin(ctx context.Context, userID uuid.UUID) error {
	query := `UPDATE users SET last_login_at = $2, updated_at = $2 WHERE id = $1`

	now := time.Now()
	_, err := r.db.ExecContext(ctx, query, userID, now)
	if err != nil {
		r.logger.Error("Failed to update last login", zap.Error(err), zap.String("user_id", userID.String()))
		return fmt.Errorf("failed to update last login: %w", err)
	}

	return nil
}

// ValidatePassword validates a user's password
func (r *UserRepository) ValidatePassword(plainPassword, hashedPassword string) bool {
	return crypto.ValidatePassword(plainPassword, hashedPassword)
}

// EmailExists checks if an email is already registered
func (r *UserRepository) EmailExists(ctx context.Context, email string) (bool, error) {
	var count int
	query := `SELECT COUNT(*) FROM users WHERE email = $1 AND is_active = true`

	err := r.db.QueryRowContext(ctx, query, email).Scan(&count)
	if err != nil {
		r.logger.Error("Failed to check email existence", zap.Error(err), zap.String("email", email))
		return false, fmt.Errorf("failed to check email: %w", err)
	}

	return count > 0, nil
}

// UpdatePassword updates the user's password hash
func (r *UserRepository) UpdatePassword(ctx context.Context, userID uuid.UUID, newHash string) error {
	query := `UPDATE users SET password_hash = $2, updated_at = $3 WHERE id = $1`
	now := time.Now()
	_, err := r.db.ExecContext(ctx, query, userID, newHash, now)
	if err != nil {
		r.logger.Error("Failed to update password", zap.Error(err), zap.String("user_id", userID.String()))
		return fmt.Errorf("failed to update password: %w", err)
	}
	r.logger.Debug("Password updated", zap.String("user_id", userID.String()))
	return nil
}

// DeactivateUser sets is_active to false for the given user
func (r *UserRepository) DeactivateUser(ctx context.Context, userID uuid.UUID) error {
	query := `UPDATE users SET is_active = false, updated_at = $2 WHERE id = $1`
	now := time.Now()
	_, err := r.db.ExecContext(ctx, query, userID, now)
	if err != nil {
		r.logger.Error("Failed to deactivate user", zap.Error(err), zap.String("user_id", userID.String()))
		return fmt.Errorf("failed to deactivate user: %w", err)
	}
	r.logger.Info("User deactivated", zap.String("user_id", userID.String()))
	return nil
}

// GetPasscodeMetadata retrieves persisted passcode metadata for a user
func (r *UserRepository) GetPasscodeMetadata(ctx context.Context, userID uuid.UUID) (*entities.PasscodeMetadata, error) {
	query := `
		SELECT passcode_hash, passcode_failed_attempts, passcode_locked_until, passcode_updated_at
		FROM users
		WHERE id = $1`

	var passcodeHash sql.NullString
	var failedAttempts sql.NullInt64
	var lockedUntil sql.NullTime
	var updatedAt sql.NullTime

	err := r.db.QueryRowContext(ctx, query, userID).Scan(
		&passcodeHash,
		&failedAttempts,
		&lockedUntil,
		&updatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("user not found")
		}
		r.logger.Error("Failed to load passcode metadata", zap.Error(err), zap.String("user_id", userID.String()))
		return nil, fmt.Errorf("failed to load passcode metadata: %w", err)
	}

	meta := &entities.PasscodeMetadata{}

	if passcodeHash.Valid {
		hash := passcodeHash.String
		meta.HashedPasscode = &hash
	}
	if failedAttempts.Valid {
		meta.FailedAttempts = int(failedAttempts.Int64)
	}
	if lockedUntil.Valid {
		meta.LockedUntil = &lockedUntil.Time
	}
	if updatedAt.Valid {
		meta.UpdatedAt = &updatedAt.Time
	}

	return meta, nil
}

// UpdatePasscodeHash persists a new passcode hash and resets security counters
func (r *UserRepository) UpdatePasscodeHash(ctx context.Context, userID uuid.UUID, hash string, updatedAt time.Time) error {
	query := `
		UPDATE users
		SET passcode_hash = $2,
			passcode_failed_attempts = 0,
			passcode_locked_until = NULL,
			passcode_updated_at = $3,
			updated_at = NOW()
		WHERE id = $1`

	res, err := r.db.ExecContext(ctx, query, userID, hash, updatedAt)
	if err != nil {
		r.logger.Error("Failed to update passcode hash", zap.Error(err), zap.String("user_id", userID.String()))
		return fmt.Errorf("failed to update passcode hash: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to verify passcode hash update: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("user not found")
	}

	return nil
}

// ResetPasscodeFailures clears passcode failure counters and lock state
func (r *UserRepository) ResetPasscodeFailures(ctx context.Context, userID uuid.UUID) error {
	query := `
		UPDATE users
		SET passcode_failed_attempts = 0,
			passcode_locked_until = NULL,
			updated_at = NOW()
		WHERE id = $1`

	_, err := r.db.ExecContext(ctx, query, userID)
	if err != nil {
		r.logger.Error("Failed to reset passcode failures", zap.Error(err), zap.String("user_id", userID.String()))
		return fmt.Errorf("failed to reset passcode failures: %w", err)
	}

	return nil
}

// IncrementPasscodeFailures increases the failure counter and optionally locks the passcode
func (r *UserRepository) IncrementPasscodeFailures(ctx context.Context, userID uuid.UUID, failureCountThreshold int, lockUntil *time.Time) (*entities.PasscodeMetadata, error) {
	query := `
		UPDATE users
		SET passcode_failed_attempts = passcode_failed_attempts + 1,
			passcode_locked_until = CASE
				WHEN passcode_failed_attempts + 1 >= $2 THEN COALESCE($3, passcode_locked_until)
				ELSE passcode_locked_until
			END,
			updated_at = NOW()
		WHERE id = $1
		RETURNING passcode_hash, passcode_failed_attempts, passcode_locked_until, passcode_updated_at`

	var passcodeHash sql.NullString
	var failedAttempts int
	var lockedUntil sql.NullTime
	var updatedAt sql.NullTime

	lockValue := interface{}(nil)
	if lockUntil != nil {
		lockValue = *lockUntil
	}

	err := r.db.QueryRowContext(ctx, query, userID, failureCountThreshold, lockValue).Scan(
		&passcodeHash,
		&failedAttempts,
		&lockedUntil,
		&updatedAt,
	)

	if err != nil {
		r.logger.Error("Failed to increment passcode failures", zap.Error(err), zap.String("user_id", userID.String()))
		return nil, fmt.Errorf("failed to increment passcode failures: %w", err)
	}

	meta := &entities.PasscodeMetadata{
		FailedAttempts: failedAttempts,
	}
	if passcodeHash.Valid {
		hash := passcodeHash.String
		meta.HashedPasscode = &hash
	}
	if lockedUntil.Valid {
		meta.LockedUntil = &lockedUntil.Time
	}
	if updatedAt.Valid {
		meta.UpdatedAt = &updatedAt.Time
	}

	return meta, nil
}

// ClearPasscode removes the stored passcode and resets counters
func (r *UserRepository) ClearPasscode(ctx context.Context, userID uuid.UUID) error {
	query := `
		UPDATE users
		SET passcode_hash = NULL,
			passcode_failed_attempts = 0,
			passcode_locked_until = NULL,
			passcode_updated_at = NULL,
			updated_at = NOW()
		WHERE id = $1`

	res, err := r.db.ExecContext(ctx, query, userID)
	if err != nil {
		r.logger.Error("Failed to clear passcode", zap.Error(err), zap.String("user_id", userID.String()))
		return fmt.Errorf("failed to clear passcode: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to verify passcode clear rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("user not found")
	}

	return nil
}

// CreatePasswordResetToken stores a password reset token using selector-verifier pattern
func (r *UserRepository) CreatePasswordResetToken(ctx context.Context, userID uuid.UUID, selector, verifierHash string, expiresAt time.Time) error {
	query := `INSERT INTO password_reset_tokens (user_id, selector, token_hash, expires_at) VALUES ($1, $2, $3, $4)`
	_, err := r.db.ExecContext(ctx, query, userID, selector, verifierHash, expiresAt)
	if err != nil {
		r.logger.Error("Failed to create password reset token", zap.Error(err), zap.String("user_id", userID.String()))
		return fmt.Errorf("failed to create password reset token: %w", err)
	}
	return nil
}

// ValidatePasswordResetToken validates a token using selector-verifier pattern and marks as used
func (r *UserRepository) ValidatePasswordResetToken(ctx context.Context, rawToken string) (uuid.UUID, error) {
	// Parse selector and verifier from the token
	selector, verifier, err := crypto.ParseSelectorVerifierToken(rawToken)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid or expired token")
	}

	// Use transaction with row lock to prevent race conditions
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		r.logger.Error("Failed to begin transaction", zap.Error(err))
		return uuid.Nil, fmt.Errorf("failed to validate token: %w", err)
	}
	defer tx.Rollback()

	// Fast lookup by selector with row lock (FOR UPDATE)
	query := `
		SELECT id, user_id, token_hash 
		FROM password_reset_tokens 
		WHERE selector = $1 AND expires_at > NOW() AND used_at IS NULL
		FOR UPDATE`

	var tokenID uuid.UUID
	var userID uuid.UUID
	var storedHash string

	err = tx.QueryRowContext(ctx, query, selector).Scan(&tokenID, &userID, &storedHash)
	if err != nil {
		if err == sql.ErrNoRows {
			return uuid.Nil, fmt.Errorf("invalid or expired token")
		}
		r.logger.Error("Failed to query password reset token", zap.Error(err))
		return uuid.Nil, fmt.Errorf("failed to validate token: %w", err)
	}

	// Single bcrypt comparison of verifier
	if bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(verifier)) != nil {
		return uuid.Nil, fmt.Errorf("invalid or expired token")
	}

	// Token matches - mark as used
	updateQuery := `UPDATE password_reset_tokens SET used_at = NOW() WHERE id = $1`
	if _, err := tx.ExecContext(ctx, updateQuery, tokenID); err != nil {
		r.logger.Error("Failed to mark token as used", zap.Error(err))
		return uuid.Nil, fmt.Errorf("failed to validate token: %w", err)
	}

	// Commit the transaction
	if err := tx.Commit(); err != nil {
		r.logger.Error("Failed to commit transaction", zap.Error(err))
		return uuid.Nil, fmt.Errorf("failed to validate token: %w", err)
	}

	return userID, nil
}

// GetByPhone retrieves a user profile by phone number
func (r *UserRepository) GetByPhone(ctx context.Context, phone string) (*entities.UserProfile, error) {
	query := `
        SELECT id, email, phone, first_name, last_name, date_of_birth,
               auth_provider_id, email_verified, phone_verified,
               onboarding_status, kyc_status, kyc_provider_ref,
               kyc_submitted_at, kyc_approved_at, kyc_rejection_reason,
               is_active, created_at, updated_at
        FROM users 
        WHERE phone = $1 AND is_active = true`

	user := &entities.UserProfile{}
	var kycApprovedAt, kycSubmittedAt sql.NullTime
	var kycRejectionReason, kycProviderRef sql.NullString
	var firstName, lastName sql.NullString
	var dateOfBirth sql.NullTime

	err := r.db.QueryRowContext(ctx, query, phone).Scan(
		&user.ID,
		&user.Email,
		&user.Phone,
		&firstName,
		&lastName,
		&dateOfBirth,
		&user.AuthProviderID,
		&user.EmailVerified,
		&user.PhoneVerified,
		&user.OnboardingStatus,
		&user.KYCStatus,
		&kycProviderRef,
		&kycSubmittedAt,
		&kycApprovedAt,
		&kycRejectionReason,
		&user.IsActive,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("user not found")
		}
		r.logger.Error("Failed to get user by phone", zap.Error(err), zap.String("phone", phone))
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	if firstName.Valid {
		user.FirstName = &firstName.String
	}
	if lastName.Valid {
		user.LastName = &lastName.String
	}
	if dateOfBirth.Valid {
		user.DateOfBirth = &dateOfBirth.Time
	}
	if kycProviderRef.Valid {
		user.KYCProviderRef = &kycProviderRef.String
	}
	if kycSubmittedAt.Valid {
		user.KYCSubmittedAt = &kycSubmittedAt.Time
	}
	if kycApprovedAt.Valid {
		user.KYCApprovedAt = &kycApprovedAt.Time
	}
	if kycRejectionReason.Valid {
		user.KYCRejectionReason = &kycRejectionReason.String
	}

	return user, nil
}
