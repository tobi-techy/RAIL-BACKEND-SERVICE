package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/crypto"
)

// AuthRepository handles authentication-related database operations
type AuthRepository struct {
	db     *sql.DB
	logger *zap.Logger
}

// NewAuthRepository creates a new auth repository
func NewAuthRepository(db *sql.DB, logger *zap.Logger) *AuthRepository {
	return &AuthRepository{
		db:     db,
		logger: logger,
	}
}

// CreateUser creates a new user with hashed password
func (r *AuthRepository) CreateUser(ctx context.Context, req *entities.RegisterRequest) (*entities.User, error) {
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

// GetUserByEmailForLogin retrieves a user by email for login purposes (includes password hash)
func (r *AuthRepository) GetUserByEmailForLogin(ctx context.Context, email string) (*entities.User, error) {
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

// GetUserByID retrieves a user by ID (excludes sensitive fields like password)
func (r *AuthRepository) GetUserByID(ctx context.Context, id uuid.UUID) (*entities.User, error) {
	query := `
		SELECT id, email, phone, auth_provider_id,
		       email_verified, phone_verified, onboarding_status, kyc_status,
		       kyc_provider_ref, kyc_submitted_at, kyc_approved_at, kyc_rejection_reason,
		       role, is_active, last_login_at, created_at, updated_at
		FROM users 
		WHERE id = $1`

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
func (r *AuthRepository) UpdateLastLogin(ctx context.Context, userID uuid.UUID) error {
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
func (r *AuthRepository) ValidatePassword(plainPassword, hashedPassword string) bool {
	return crypto.ValidatePassword(plainPassword, hashedPassword)
}

// EmailExists checks if an email is already registered
func (r *AuthRepository) EmailExists(ctx context.Context, email string) (bool, error) {
	var count int
	query := `SELECT COUNT(*) FROM users WHERE email = $1 AND is_active = true`

	err := r.db.QueryRowContext(ctx, query, email).Scan(&count)
	if err != nil {
		r.logger.Error("Failed to check email existence", zap.Error(err), zap.String("email", email))
		return false, fmt.Errorf("failed to check email: %w", err)
	}

	return count > 0, nil
}
