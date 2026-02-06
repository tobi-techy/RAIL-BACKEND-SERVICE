package auth

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services"
	"github.com/rail-service/rail_service/internal/domain/services/onboarding"
	"github.com/rail-service/rail_service/internal/domain/services/security"
	"github.com/rail-service/rail_service/internal/domain/services/session"
	"github.com/rail-service/rail_service/internal/domain/services/twofa"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/rail-service/rail_service/pkg/auth"
	"github.com/rail-service/rail_service/pkg/crypto"
	"github.com/rail-service/rail_service/pkg/ratelimit"
	"go.uber.org/zap"
)

// AuthHandlers consolidates authentication, signup, and onboarding handlers
type AuthHandlers struct {
	db                  *sql.DB
	cfg                 *config.Config
	logger              *zap.Logger
	userRepo            repositories.UserRepository
	verificationService services.VerificationService
	onboardingService   *onboarding.Service
	emailService        *adapters.EmailService
	sessionService      SessionService
	twoFAService        TwoFAService
	redisClient         RedisClient
	validator           *validator.Validate
}

// RedisClient interface for pending registration storage
type RedisClient interface {
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error
	Get(ctx context.Context, key string, dest interface{}) error
	Del(ctx context.Context, key string) error
}

// SessionService interface for session management
type SessionService interface {
	InvalidateSession(ctx context.Context, token string) error
	InvalidateAllUserSessions(ctx context.Context, userID uuid.UUID) error
	CreateSession(ctx context.Context, userID uuid.UUID, accessToken, refreshToken, ipAddress, userAgent, deviceFingerprint, location string, expiresAt time.Time) (*session.Session, error)
}

// TwoFAService interface for 2FA management
type TwoFAService interface {
	GenerateSecret(ctx context.Context, userID uuid.UUID, userEmail string) (*twofa.TwoFASetup, error)
	VerifyAndEnable(ctx context.Context, userID uuid.UUID, code string) error
	Disable(ctx context.Context, userID uuid.UUID, code string) error
	GetStatus(ctx context.Context, userID uuid.UUID) (*twofa.TwoFAStatus, error)
}

// NewAuthHandlers creates a new instance of AuthHandlers
func NewAuthHandlers(
	db *sql.DB,
	cfg *config.Config,
	logger *zap.Logger,
	userRepo repositories.UserRepository,
	verificationService services.VerificationService,
	onboardingService *onboarding.Service,
	emailService *adapters.EmailService,
	sessionService SessionService,
	twoFAService TwoFAService,
	redisClient RedisClient,
) *AuthHandlers {
	return &AuthHandlers{
		db:                  db,
		cfg:                 cfg,
		logger:              logger,
		userRepo:            userRepo,
		verificationService: verificationService,
		onboardingService:   onboardingService,
		emailService:        emailService,
		sessionService:      sessionService,
		twoFAService:        twoFAService,
		redisClient:         redisClient,
		validator:           validator.New(),
	}
}

// Register handles user registration and queues verification
// @Summary Register a new user and prepare verification
// @Description Create a new user account request and store pending registration data (email or phone only, no password)
// @Tags auth
// @Accept json
// @Produce json
// @Param request body entities.SignUpRequest true "Signup data (email or phone only)"
// @Success 202 {object} entities.SignUpResponse "Registration accepted"
// @Failure 400 {object} entities.ErrorResponse
// @Failure 409 {object} entities.ErrorResponse
// @Failure 500 {object} entities.ErrorResponse
// @Router /api/v1/auth/register [post]
func (h *AuthHandlers) Register(c *gin.Context) {
	ctx := c.Request.Context()

	var req entities.SignUpRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("Invalid signup request", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_REQUEST",
			Message: "Invalid request payload",
			Details: map[string]interface{}{"error": err.Error()},
		})
		return
	}

	if req.Email == nil && req.Phone == nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "VALIDATION_ERROR",
			Message: "Either email or phone is required",
		})
		return
	}
	if req.Email != nil && req.Phone != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "VALIDATION_ERROR",
			Message: "Only one of email or phone can be provided",
		})
		return
	}

	identifier := ""
	identifierType := ""
	var existingUser *entities.UserProfile
	existingUnverified := false

	if req.Email != nil {
		identifier = strings.TrimSpace(*req.Email)
		identifierType = "email"
	} else {
		identifier = strings.TrimSpace(*req.Phone)
		identifierType = "phone"
	}

	if identifier == "" {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "VALIDATION_ERROR",
			Message: fmt.Sprintf("%s cannot be empty", identifierType),
		})
		return
	}

	if identifierType == "email" {
		var err error
		existingUser, err = h.userRepo.GetByEmail(ctx, identifier)
		if err != nil && !common.IsUserNotFoundError(err) {
			h.logger.Error("Failed to check email existence", zap.Error(err))
			c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
				Code:    "INTERNAL_ERROR",
				Message: "Internal server error",
			})
			return
		}
		if existingUser != nil {
			if existingUser.EmailVerified {
				c.JSON(http.StatusConflict, entities.ErrorResponse{
					Code:    "USER_EXISTS",
					Message: "User already exists with this email",
				})
				return
			}
			existingUnverified = true
		}
	} else {
		var err error
		existingUser, err = h.userRepo.GetByPhone(ctx, identifier)
		if err != nil && !common.IsUserNotFoundError(err) {
			h.logger.Error("Failed to check phone existence", zap.Error(err))
			c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
				Code:    "INTERNAL_ERROR",
				Message: "Internal server error",
			})
			return
		}
		if existingUser != nil {
			if existingUser.PhoneVerified {
				c.JSON(http.StatusConflict, entities.ErrorResponse{
					Code:    "USER_EXISTS",
					Message: "User already exists with this phone",
				})
				return
			}
			existingUnverified = true
		}
	}

	var pendingKey string
	if !existingUnverified {
		// Store pending registration in Redis (expires in 10 minutes) - no password hash
		pendingTTL := 10 * time.Minute
		pending := entities.PendingRegistration{
			CreatedAt: time.Now(),
			ExpiresAt: time.Now().Add(pendingTTL),
		}
		if identifierType == "email" {
			pending.Email = identifier
		} else {
			pending.Phone = identifier
		}

		pendingKey = fmt.Sprintf("pending_registration:%s:%s", identifierType, identifier)
		if err := h.redisClient.Set(ctx, pendingKey, pending, pendingTTL); err != nil {
			h.logger.Error("Failed to store pending registration", zap.Error(err))
			c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
				Code:    "INTERNAL_ERROR",
				Message: "Failed to process registration",
			})
			return
		}
		h.logger.Info("Pending registration created", zap.String("identifier", identifier))
	}

	if _, err := h.verificationService.GenerateAndSendCode(ctx, identifierType, identifier); err != nil {
		status := http.StatusInternalServerError
		code := "VERIFICATION_SEND_FAILED"
		message := "Failed to send verification code. Please try again."
		lowered := strings.ToLower(err.Error())
		if strings.Contains(lowered, "too many verification code send attempts") {
			status = http.StatusTooManyRequests
			code = "TOO_MANY_REQUESTS"
			message = "Too many verification code requests. Please wait before retrying."
		}

		if pendingKey != "" {
			_ = h.redisClient.Del(ctx, pendingKey)
		}

		h.logger.Error("Failed to send verification code", zap.Error(err), zap.String("identifier", identifier))
		c.JSON(status, entities.ErrorResponse{
			Code:    code,
			Message: message,
		})
		return
	}

	if existingUnverified {
		h.logger.Info("Verification code sent for existing unverified user", zap.String("identifier", identifier))
		c.JSON(http.StatusAccepted, entities.SignUpResponse{
			Message:    fmt.Sprintf("Verification code sent to %s. If you did not receive it, call /api/v1/auth/resend-code.", identifier),
			Identifier: identifier,
		})
		return
	}

	c.JSON(http.StatusAccepted, entities.SignUpResponse{
		Message:    fmt.Sprintf("Registration received for %s. Verification code sent. If you did not receive it, call /api/v1/auth/resend-code. You will set your password during onboarding.", identifier),
		Identifier: identifier,
	})
}

// VerifyCode handles verification code submission
// @Summary Verify user account with code
// @Description Verify the email or phone number using a 6-digit code
// @Tags auth
// @Accept json
// @Produce json
// @Param request body entities.VerifyCodeRequest true "Verification data (email or phone, and code)"
// @Success 200 {object} entities.VerifyCodeResponse "Account verified, returns JWT tokens"
// @Failure 400 {object} entities.ErrorResponse
// @Failure 401 {object} entities.ErrorResponse
// @Failure 404 {object} entities.ErrorResponse
// @Failure 500 {object} entities.ErrorResponse
// @Router /api/v1/auth/verify [post]
// Verify handles unified verification for both new registrations and existing users
func (h *AuthHandlers) Verify(c *gin.Context) {
	ctx := c.Request.Context()

	var req entities.VerifyCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_REQUEST", Message: "Invalid request payload"})
		return
	}

	if req.Email == nil && req.Phone == nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "VALIDATION_ERROR", Message: "Either email or phone is required"})
		return
	}

	var identifier, identifierType string
	if req.Email != nil {
		identifier, identifierType = strings.TrimSpace(*req.Email), "email"
	} else {
		identifier, identifierType = strings.TrimSpace(*req.Phone), "phone"
	}

	isValid, err := h.verificationService.VerifyCode(ctx, identifierType, identifier, req.Code)
	if err != nil || !isValid {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "INVALID_CODE", Message: "Invalid or expired verification code"})
		return
	}

	// Check for pending registration (new user flow)
	pendingKey := fmt.Sprintf("pending_registration:%s:%s", identifierType, identifier)
	var pending entities.PendingRegistration
	if err := h.redisClient.Get(ctx, pendingKey, &pending); err == nil {
		h.completeNewUserVerification(c, ctx, identifier, identifierType, pending, pendingKey)
		return
	}

	// Existing user verification
	h.completeExistingUserVerification(c, ctx, identifier, identifierType)
}

func (h *AuthHandlers) completeNewUserVerification(c *gin.Context, ctx context.Context, identifier, identifierType string, pending entities.PendingRegistration, pendingKey string) {
	var phone *string
	var email string
	if identifierType == "email" {
		email = identifier
	} else {
		phone = &identifier
	}

	// Create user without password hash - password will be set during onboarding
	user, err := h.userRepo.CreateUserWithHash(ctx, email, phone, "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "USER_CREATION_FAILED", Message: "Failed to create user account"})
		return
	}

	if identifierType == "email" {
		user.EmailVerified = true
	} else {
		user.PhoneVerified = true
	}
	user.OnboardingStatus = entities.OnboardingStatusStarted

	_ = h.userRepo.Update(ctx, &entities.UserProfile{
		ID: user.ID, Email: user.Email, Phone: user.Phone,
		EmailVerified: user.EmailVerified, PhoneVerified: user.PhoneVerified,
		OnboardingStatus: user.OnboardingStatus, KYCStatus: user.KYCStatus,
	})
	_ = h.redisClient.Del(ctx, pendingKey)

	// Auto-start onboarding flow
	var onboardingResp *entities.OnboardingStatusResponse
	if h.onboardingService != nil {
		_ = h.onboardingService.CompleteEmailVerification(ctx, user.ID)
		onboardingResp, _ = h.onboardingService.GetOnboardingStatus(ctx, user.ID)
	}

	tokens, err := auth.GenerateTokenPair(user.ID, user.Email, "user", h.cfg.JWT.Secret, h.cfg.JWT.AccessTTL, h.cfg.JWT.RefreshTTL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "TOKEN_GENERATION_FAILED", Message: "Failed to generate tokens"})
		return
	}

	if h.sessionService != nil {
		ipAddress, userAgent, fingerprint, location := extractSessionDetails(c)
		if _, err := h.sessionService.CreateSession(ctx, user.ID, tokens.AccessToken, tokens.RefreshToken, ipAddress, userAgent, fingerprint, location, tokens.ExpiresAt); err != nil {
			h.logger.Warn("Failed to create session", zap.Error(err), zap.String("user_id", user.ID.String()))
		}
	}

	h.logger.Info("User created and verified", zap.String("user_id", user.ID.String()))

	response := gin.H{
		"user":              &entities.UserInfo{ID: user.ID, Email: user.Email, Phone: user.Phone, EmailVerified: user.EmailVerified, PhoneVerified: user.PhoneVerified, OnboardingStatus: user.OnboardingStatus, KYCStatus: user.KYCStatus, CreatedAt: user.CreatedAt},
		"accessToken":       tokens.AccessToken,
		"refreshToken":      tokens.RefreshToken,
		"expiresAt":         tokens.ExpiresAt,
		"onboarding_status": user.OnboardingStatus,
		"next_step":         "complete_onboarding",
	}
	if onboardingResp != nil {
		response["onboarding"] = onboardingResp
	}
	c.JSON(http.StatusOK, response)
}

func (h *AuthHandlers) completeExistingUserVerification(c *gin.Context, ctx context.Context, identifier, identifierType string) {
	if identifierType != "email" {
		c.JSON(http.StatusNotFound, entities.ErrorResponse{Code: "USER_NOT_FOUND", Message: "User not found"})
		return
	}

	user, err := h.userRepo.GetByEmail(ctx, identifier)
	if err != nil || user == nil {
		c.JSON(http.StatusNotFound, entities.ErrorResponse{Code: "USER_NOT_FOUND", Message: "User not found"})
		return
	}

	user.EmailVerified = true
	if err := h.userRepo.Update(ctx, user); err != nil {
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "VERIFY_FAILED", Message: "Failed to verify email"})
		return
	}

	if h.onboardingService != nil {
		_ = h.onboardingService.CompleteEmailVerification(ctx, user.ID)
	}

	c.JSON(http.StatusOK, gin.H{"message": "Email verified successfully", "verified": true})
}

// ResendCode handles requests to resend a verification code
// @Summary Resend verification code
// @Description Request a new verification code to be sent to email or phone
// @Tags auth
// @Accept json
// @Produce json
// @Param request body entities.ResendCodeRequest true "Resend code data (email or phone)"
// @Success 202 {object} entities.SignUpResponse "New verification code sent"
// @Failure 400 {object} entities.ErrorResponse
// @Failure 404 {object} entities.ErrorResponse
// @Failure 429 {object} entities.ErrorResponse "Too many requests"
// @Failure 500 {object} entities.ErrorResponse
// @Router /api/v1/auth/resend-code [post]
func (h *AuthHandlers) ResendCode(c *gin.Context) {
	ctx := c.Request.Context()

	var req entities.ResendCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("Invalid resend code request", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_REQUEST",
			Message: "Invalid request payload",
			Details: map[string]interface{}{"error": err.Error()},
		})
		return
	}

	if req.Email == nil && req.Phone == nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "VALIDATION_ERROR",
			Message: "Either email or phone is required",
		})
		return
	}
	if req.Email != nil && req.Phone != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "VALIDATION_ERROR",
			Message: "Only one of email or phone can be provided",
		})
		return
	}

	var identifier string
	var identifierType string

	if req.Email != nil {
		identifier = strings.TrimSpace(*req.Email)
		identifierType = "email"
	} else if req.Phone != nil {
		identifier = strings.TrimSpace(*req.Phone)
		identifierType = "phone"
	}

	if identifier == "" {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "VALIDATION_ERROR",
			Message: fmt.Sprintf("%s cannot be empty", identifierType),
		})
		return
	}

	var (
		userProfile   *entities.UserProfile
		pendingExists bool
	)

	// Attempt to load pending registration for first-time verification
	pendingKey := fmt.Sprintf("pending_registration:%s:%s", identifierType, identifier)
	var pending entities.PendingRegistration
	if err := h.redisClient.Get(ctx, pendingKey, &pending); err == nil {
		pendingExists = true
	} else if err != nil && !isRedisNilError(err) {
		h.logger.Error("Failed to check pending registration", zap.Error(err), zap.String("key", pendingKey))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "INTERNAL_ERROR",
			Message: "Failed to process verification",
		})
		return
	}

	// Fetch existing user if available
	var err error
	if identifierType == "email" {
		userProfile, err = h.userRepo.GetByEmail(ctx, identifier)
	} else {
		userProfile, err = h.userRepo.GetByPhone(ctx, identifier)
	}
	if err != nil {
		if !common.IsUserNotFoundError(err) {
			h.logger.Error("Failed to get user", zap.Error(err), zap.String("identifier", identifier), zap.String("identifierType", identifierType))
			c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
				Code:    "INTERNAL_ERROR",
				Message: "Internal server error",
			})
			return
		}
		userProfile = nil
	}

	if userProfile == nil && !pendingExists {
		c.JSON(http.StatusNotFound, entities.ErrorResponse{
			Code:    "USER_NOT_FOUND",
			Message: "User not found",
		})
		return
	}

	if userProfile != nil {
		if (identifierType == "email" && userProfile.EmailVerified) || (identifierType == "phone" && userProfile.PhoneVerified) {
			c.JSON(http.StatusOK, entities.ErrorResponse{
				Code:    "ALREADY_VERIFIED",
				Message: fmt.Sprintf("%s is already verified", identifierType),
			})
			return
		}
	}

	// Check if resending is allowed (rate limit)
	canResend, err := h.verificationService.CanResendCode(ctx, identifierType, identifier)
	if err != nil {
		h.logger.Error("Failed to check resend eligibility", zap.Error(err), zap.String("identifier", identifier))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "INTERNAL_ERROR",
			Message: "Failed to check resend eligibility",
		})
		return
	}
	if !canResend {
		c.JSON(http.StatusTooManyRequests, entities.ErrorResponse{
			Code:    "TOO_MANY_REQUESTS",
			Message: "Too many resend attempts. Please wait before requesting a new code.",
		})
		return
	}

	// Generate and send new code for either pending registration or existing user
	_, err = h.verificationService.GenerateAndSendCode(ctx, identifierType, identifier)
	if err != nil {
		status := http.StatusInternalServerError
		code := "VERIFICATION_SEND_FAILED"
		message := "Failed to send verification code. Please try again."
		lowered := strings.ToLower(err.Error())
		if strings.Contains(lowered, "too many verification code send attempts") {
			status = http.StatusTooManyRequests
			code = "TOO_MANY_REQUESTS"
			message = "Too many verification code requests. Please wait before retrying."
		}

		h.logger.Error("Failed to send verification code", zap.Error(err), zap.String("identifier", identifier))
		c.JSON(status, entities.ErrorResponse{
			Code:    code,
			Message: message,
		})
		return
	}

	if userProfile != nil {
		h.logger.Info("Verification code re-sent", zap.String("user_id", userProfile.ID.String()), zap.String("identifier", identifier))
	} else {
		h.logger.Info("Verification code sent for pending registration", zap.String("identifier", identifier))
	}
	c.JSON(http.StatusAccepted, entities.SignUpResponse{
		Message:    fmt.Sprintf("Verification code sent to %s.", identifier),
		Identifier: identifier,
	})
}

func isRedisNilError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "redis: nil")
}

func (h *AuthHandlers) recordLoginFailure(c *gin.Context, providedIdentifier string) {
	identifier := providedIdentifier
	if identifier == "" {
		identifier = getLoginIdentifierFromContext(c)
	}
	if identifier == "" {
		return
	}

	if tracker := getLoginAttemptTracker(c); tracker != nil {
		if _, err := tracker.RecordFailedAttempt(c.Request.Context(), identifier); err != nil {
			h.logger.Warn("Failed to record login attempt", zap.Error(err), zap.String("identifier", identifier))
		}
	}

	if svc := getLoginProtectionService(c); svc != nil {
		if _, err := svc.RecordFailedAttempt(c.Request.Context(), identifier, c.ClientIP(), c.Request.UserAgent()); err != nil {
			h.logger.Warn("Failed to record login protection attempt", zap.Error(err), zap.String("identifier", identifier))
		}
	}
}

func (h *AuthHandlers) recordLoginSuccess(c *gin.Context, providedIdentifier string) {
	identifier := providedIdentifier
	if identifier == "" {
		identifier = getLoginIdentifierFromContext(c)
	}
	if identifier == "" {
		return
	}

	if tracker := getLoginAttemptTracker(c); tracker != nil {
		if err := tracker.RecordSuccessfulLogin(c.Request.Context(), identifier); err != nil {
			h.logger.Warn("Failed to clear login attempts", zap.Error(err), zap.String("identifier", identifier))
		}
	}

	if svc := getLoginProtectionService(c); svc != nil {
		if err := svc.ClearFailedAttempts(c.Request.Context(), identifier); err != nil {
			h.logger.Warn("Failed to clear login protection attempts", zap.Error(err), zap.String("identifier", identifier))
		}
	}
}

func getLoginIdentifierFromContext(c *gin.Context) string {
	if v, ok := c.Get("login_identifier"); ok {
		if identifier, ok := v.(string); ok {
			return identifier
		}
	}
	if v, ok := c.Get("login_email"); ok {
		if identifier, ok := v.(string); ok {
			return identifier
		}
	}
	return ""
}

func getLoginAttemptTracker(c *gin.Context) *ratelimit.LoginAttemptTracker {
	if v, ok := c.Get("login_tracker"); ok {
		if tracker, ok := v.(*ratelimit.LoginAttemptTracker); ok {
			return tracker
		}
	}
	return nil
}

func getLoginProtectionService(c *gin.Context) *security.LoginProtectionService {
	if v, ok := c.Get("login_protection"); ok {
		if svc, ok := v.(*security.LoginProtectionService); ok {
			return svc
		}
	}
	return nil
}

// Login handles user authentication
// @Summary Login user
// @Description Authenticate user and return JWT tokens
// @Tags auth
// @Accept json
// @Produce json
// @Param request body entities.LoginRequest true "Login credentials"
// @Success 200 {object} entities.AuthResponse
// @Failure 400 {object} entities.ErrorResponse
// @Failure 401 {object} entities.ErrorResponse
// @Router /api/v1/auth/login [post]
func (h *AuthHandlers) Login(c *gin.Context) {
	ctx := c.Request.Context()

	// Parse request
	var req entities.LoginRequest
	if err := c.ShouldBindBodyWith(&req, binding.JSON); err != nil {
		h.logger.Warn("Invalid login request", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_REQUEST",
			Message: "Invalid request payload",
			Details: map[string]interface{}{"error": err.Error()},
		})
		return
	}

	identifier := ""
	identifierType := ""
	if req.Email != nil && strings.TrimSpace(*req.Email) != "" {
		identifier = strings.TrimSpace(*req.Email)
		identifierType = "email"
	} else if req.Phone != nil && strings.TrimSpace(*req.Phone) != "" {
		identifier = strings.TrimSpace(*req.Phone)
		identifierType = "phone"
	}

	// Basic validation
	if identifier == "" || strings.TrimSpace(req.Password) == "" {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "VALIDATION_ERROR",
			Message: "Email or phone and password are required",
		})
		return
	}

	// Get user by identifier
	var (
		user *entities.User
		err  error
	)
	if identifierType == "email" {
		user, err = h.userRepo.GetUserByEmailForLogin(ctx, identifier)
	} else {
		user, err = h.userRepo.GetUserByPhoneForLogin(ctx, identifier)
	}
	if err != nil {
		h.logger.Warn("Login attempt failed - user not found", zap.String("identifier", identifier), zap.Error(err))
		h.recordLoginFailure(c, identifier)
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{
			Code:    "INVALID_CREDENTIALS",
			Message: "Invalid email or password",
		})
		return
	}

	// Validate password
	if !h.userRepo.ValidatePassword(req.Password, user.PasswordHash) {
		h.logger.Warn("Login attempt failed - invalid password", zap.String("identifier", identifier))
		h.recordLoginFailure(c, identifier)
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{
			Code:    "INVALID_CREDENTIALS",
			Message: "Invalid email or password",
		})
		return
	}

	// Check if user is active
	if !user.IsActive {
		h.logger.Warn("Login attempt failed - user account inactive", zap.String("identifier", identifier))
		h.recordLoginFailure(c, identifier)
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{
			Code:    "ACCOUNT_INACTIVE",
			Message: "Account is inactive. Please contact support.",
		})
		return
	}

	// Generate JWT tokens
	tokens, err := auth.GenerateTokenPair(
		user.ID,
		user.Email,
		user.Role,
		h.cfg.JWT.Secret,
		h.cfg.JWT.AccessTTL,
		h.cfg.JWT.RefreshTTL,
	)
	if err != nil {
		h.logger.Error("Failed to generate tokens", zap.Error(err))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "TOKEN_GENERATION_FAILED",
			Message: "Failed to generate authentication tokens",
		})
		return
	}

	if h.sessionService != nil {
		ipAddress, userAgent, fingerprint, location := extractSessionDetails(c)
		if _, err := h.sessionService.CreateSession(ctx, user.ID, tokens.AccessToken, tokens.RefreshToken, ipAddress, userAgent, fingerprint, location, tokens.ExpiresAt); err != nil {
			h.logger.Warn("Failed to create session", zap.Error(err), zap.String("user_id", user.ID.String()))
		}
	}

	// Update last login timestamp
	if err := h.userRepo.UpdateLastLogin(ctx, user.ID); err != nil {
		h.logger.Warn("Failed to update last login", zap.Error(err), zap.String("user_id", user.ID.String()))
		// Don't fail login for this
	}

	h.recordLoginSuccess(c, identifier)

	if h.emailService != nil && user.Email != "" {
		alertDetails := adapters.LoginAlertDetails{
			IP:        c.ClientIP(),
			UserAgent: c.Request.UserAgent(),
			LoginAt:   time.Now().UTC(),
		}

		if forwarded := strings.TrimSpace(c.GetHeader("X-Forwarded-For")); forwarded != "" && forwarded != alertDetails.IP {
			alertDetails.ForwardedFor = forwarded
		}

		location := strings.TrimSpace(c.GetHeader("X-Geo-City"))
		if location == "" {
			location = strings.TrimSpace(c.GetHeader("X-Geo-Country"))
		}
		if location == "" {
			location = strings.TrimSpace(c.GetHeader("CF-IPCountry"))
		}
		alertDetails.Location = location

		if err := h.emailService.SendLoginAlertEmail(ctx, user.Email, alertDetails); err != nil {
			h.logger.Warn("Failed to send login alert email", zap.Error(err), zap.String("user_id", user.ID.String()))
		}
	}

	// Return success response
	response := entities.AuthResponse{
		User:         user.ToUserInfo(),
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		ExpiresAt:    tokens.ExpiresAt,
	}

	h.logger.Info("User logged in successfully", zap.String("user_id", user.ID.String()), zap.String("email", user.Email))
	c.JSON(http.StatusOK, response)
}

// RefreshToken handles JWT token refresh
func (h *AuthHandlers) RefreshToken(c *gin.Context) {
	ctx := c.Request.Context()

	var req entities.RefreshTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.RespondBadRequest(c, "Invalid request payload", nil)
		return
	}

	refreshToken := strings.TrimSpace(req.RefreshToken)
	if refreshToken == "" {
		h.logger.Warn("Empty refresh token provided")
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "INVALID_TOKEN", Message: "Invalid refresh token"})
		return
	}

	// Basic JWT format validation
	segments := strings.Split(refreshToken, ".")
	if len(segments) != 3 {
		h.logger.Warn("Malformed token", zap.Int("segments_count", len(segments)))
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "INVALID_TOKEN", Message: "Invalid refresh token format"})
		return
	}

	// Validate refresh token and extract user ID
	userID, err := auth.ValidateRefreshToken(refreshToken, h.cfg.JWT.Secret)
	if err != nil {
		h.logger.Warn("Failed to validate refresh token", zap.Error(err))
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "INVALID_TOKEN", Message: "Invalid refresh token"})
		return
	}

	// Fetch current user data from database
	user, err := h.userRepo.GetUserEntityByID(ctx, userID)
	if err != nil {
		h.logger.Warn("User not found during token refresh", zap.Error(err), zap.String("user_id", userID.String()))
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "USER_NOT_FOUND", Message: "User not found"})
		return
	}

	// Validate user is still active
	if !user.IsActive {
		h.logger.Warn("Inactive user attempted token refresh", zap.String("user_id", userID.String()))
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "ACCOUNT_INACTIVE", Message: "Account is inactive"})
		return
	}

	// Generate new access token with current user data
	accessToken, expiresAt, err := auth.GenerateAccessToken(user.ID, user.Email, user.Role, h.cfg.JWT.Secret, h.cfg.JWT.AccessTTL)
	if err != nil {
		h.logger.Error("Failed to generate access token", zap.Error(err))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "TOKEN_GENERATION_FAILED", Message: "Failed to generate access token"})
		return
	}

	c.JSON(http.StatusOK, auth.TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    expiresAt,
	})
}

// Logout handles user logout
func (h *AuthHandlers) Logout(c *gin.Context) {
	ctx := c.Request.Context()

	// Get the token from Authorization header
	authHeader := c.GetHeader("Authorization")
	if authHeader != "" && len(authHeader) > 7 && authHeader[:7] == "Bearer " {
		token := authHeader[7:]

		if h.sessionService != nil {
			if err := h.sessionService.InvalidateSession(ctx, token); err != nil {
				h.logger.Warn("Failed to invalidate session", zap.Error(err))
			} else {
				h.logger.Info("Session invalidated successfully")
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "Logged out"})
}

// ForgotPassword handles password reset requests
func (h *AuthHandlers) ForgotPassword(c *gin.Context) {
	var req entities.ForgotPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.RespondBadRequest(c, "Invalid request payload", nil)
		return
	}
	ctx := c.Request.Context()
	user, err := h.userRepo.GetByEmail(ctx, req.Email)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "If an account exists, password reset instructions will be sent"})
		return
	}
	// Generate selector-verifier token for secure, fast validation
	svToken, err := crypto.GenerateSelectorVerifierToken()
	if err != nil {
		h.logger.Error("Failed to generate password reset token", zap.Error(err))
		c.JSON(http.StatusOK, gin.H{"message": "If an account exists, password reset instructions will be sent"})
		return
	}
	expiresAt := time.Now().Add(1 * time.Hour)
	if err := h.userRepo.CreatePasswordResetToken(ctx, user.ID, svToken.Selector, svToken.VerifierHash, expiresAt); err != nil {
		h.logger.Error("Failed to store password reset token", zap.Error(err))
		c.JSON(http.StatusOK, gin.H{"message": "If an account exists, password reset instructions will be sent"})
		return
	}
	if h.emailService != nil {
		if err := h.emailService.SendVerificationEmail(ctx, user.Email, svToken.FullToken); err != nil {
			h.logger.Error("Failed to send password reset email", zap.Error(err))
		}
	}
	c.JSON(http.StatusOK, gin.H{"message": "If an account exists, password reset instructions will be sent"})
}

// ResetPassword handles password reset
func (h *AuthHandlers) ResetPassword(c *gin.Context) {
	var req entities.ResetPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.RespondBadRequest(c, "Invalid request payload", nil)
		return
	}
	ctx := c.Request.Context()
	// Pass raw token to repository - bcrypt comparison happens there
	userID, err := h.userRepo.ValidatePasswordResetToken(ctx, req.Token)
	if err != nil {
		h.logger.Warn("Invalid password reset token", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_TOKEN", Message: "Invalid or expired reset token"})
		return
	}
	newHash, err := crypto.HashPassword(req.Password)
	if err != nil {
		h.logger.Error("Failed to hash new password", zap.Error(err))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "HASH_FAILED", Message: "Failed to hash password"})
		return
	}
	if err := h.userRepo.UpdatePassword(ctx, userID, newHash); err != nil {
		h.logger.Error("Failed to update password", zap.Error(err))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "UPDATE_FAILED", Message: "Failed to update password"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Password has been reset"})
}

// GetProfile returns user profile with optional includes
// @Summary Get current user profile
// @Description Get user profile. Use ?include=onboarding,kyc for additional data
// @Tags users
// @Produce json
// @Param include query string false "Comma-separated includes: onboarding,kyc"
// @Success 200 {object} entities.UserInfo
// @Router /api/v1/users/me [get]
func (h *AuthHandlers) GetProfile(c *gin.Context) {
	ctx := c.Request.Context()
	userIDVal, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "UNAUTHORIZED", Message: "User not authenticated"})
		return
	}
	userID, ok := userIDVal.(uuid.UUID)
	if !ok {
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "INTERNAL_ERROR", Message: "Invalid user id in context"})
		return
	}
	user, err := h.userRepo.GetUserEntityByID(ctx, userID)
	if err != nil {
		c.JSON(http.StatusNotFound, entities.ErrorResponse{Code: "USER_NOT_FOUND", Message: "User not found"})
		return
	}

	// Check for include param
	includeParam := c.Query("include")
	if includeParam == "" {
		c.JSON(http.StatusOK, user.ToUserInfo())
		return
	}

	includes := strings.Split(includeParam, ",")
	response := gin.H{"user": user.ToUserInfo()}

	for _, inc := range includes {
		switch strings.TrimSpace(inc) {
		case "onboarding":
			if h.onboardingService != nil {
				if status, err := h.onboardingService.GetOnboardingStatus(ctx, userID); err == nil {
					response["onboarding"] = status
				}
			}
		case "kyc":
			if h.onboardingService != nil {
				if kycStatus, err := h.onboardingService.GetKYCStatus(ctx, userID); err == nil {
					response["kyc"] = kycStatus
				}
			}
		}
	}

	c.JSON(http.StatusOK, response)
}

func (h *AuthHandlers) UpdateProfile(c *gin.Context) {
	ctx := c.Request.Context()
	userIDVal, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "UNAUTHORIZED", Message: "User not authenticated"})
		return
	}
	userID, ok := userIDVal.(uuid.UUID)
	if !ok {
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "INTERNAL_ERROR", Message: "Invalid user id in context"})
		return
	}
	var payload entities.UserProfile
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_REQUEST", Message: "Invalid payload", Details: map[string]interface{}{"error": err.Error()}})
		return
	}
	user, err := h.userRepo.GetByID(ctx, userID)
	if err != nil {
		c.JSON(http.StatusNotFound, entities.ErrorResponse{Code: "USER_NOT_FOUND", Message: "User not found"})
		return
	}
	if payload.Phone != nil {
		user.Phone = payload.Phone
	}
	if payload.FirstName != nil {
		user.FirstName = payload.FirstName
	}
	if payload.LastName != nil {
		user.LastName = payload.LastName
	}
	if err := h.userRepo.Update(ctx, user); err != nil {
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "UPDATE_FAILED", Message: "Failed to update profile"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Profile updated"})
}

func (h *AuthHandlers) ChangePassword(c *gin.Context) {
	ctx := c.Request.Context()
	userIDVal, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "UNAUTHORIZED", Message: "User not authenticated"})
		return
	}
	userID, ok := userIDVal.(uuid.UUID)
	if !ok {
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "INTERNAL_ERROR", Message: "Invalid user id in context"})
		return
	}
	var req entities.ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.RespondBadRequest(c, "Invalid request payload", nil)
		return
	}
	user, err := h.userRepo.GetUserEntityByID(ctx, userID)
	if err != nil {
		c.JSON(http.StatusNotFound, entities.ErrorResponse{Code: "USER_NOT_FOUND", Message: "User not found"})
		return
	}
	if !h.userRepo.ValidatePassword(req.CurrentPassword, user.PasswordHash) {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "INVALID_CREDENTIALS", Message: "Current password is incorrect"})
		return
	}
	newHash, err := crypto.HashPassword(req.NewPassword)
	if err != nil {
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "HASH_FAILED", Message: "Failed to hash new password"})
		return
	}
	if err := h.userRepo.UpdatePassword(ctx, userID, newHash); err != nil {
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "UPDATE_FAILED", Message: "Failed to update password"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Password changed"})
}

func (h *AuthHandlers) DeleteAccount(c *gin.Context) {
	ctx := c.Request.Context()
	userIDVal, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "UNAUTHORIZED", Message: "User not authenticated"})
		return
	}
	userID, ok := userIDVal.(uuid.UUID)
	if !ok {
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "INTERNAL_ERROR", Message: "Invalid user id in context"})
		return
	}
	if err := h.userRepo.DeactivateUser(ctx, userID); err != nil {
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "DELETE_FAILED", Message: "Failed to delete account"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Account deactivated"})
}

// Enable2FA handles 2FA setup initiation
// @Summary Enable 2FA
// @Description Generates a 2FA secret and QR code for the user
// @Tags auth
// @Produce json
// @Success 200 {object} TwoFASetup
// @Failure 400 {object} entities.ErrorResponse
// @Failure 401 {object} entities.ErrorResponse
// @Failure 500 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/users/me/enable-2fa [post]
func (h *AuthHandlers) Enable2FA(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "UNAUTHORIZED", Message: "User not authenticated"})
		return
	}

	// Check if request has a code (verification step)
	var req struct {
		Code string `json:"code"`
	}
	if err := c.ShouldBindJSON(&req); err == nil && req.Code != "" {
		// Verify and enable
		if h.twoFAService == nil {
			c.JSON(http.StatusServiceUnavailable, entities.ErrorResponse{Code: "2FA_UNAVAILABLE", Message: "2FA service not available"})
			return
		}

		if err := h.twoFAService.VerifyAndEnable(ctx, userID, req.Code); err != nil {
			h.logger.Warn("Failed to verify 2FA code", zap.Error(err), zap.String("user_id", userID.String()))
			c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_CODE", Message: err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "2FA enabled successfully"})
		return
	}

	// Generate new secret
	user, err := h.userRepo.GetUserEntityByID(ctx, userID)
	if err != nil {
		c.JSON(http.StatusNotFound, entities.ErrorResponse{Code: "USER_NOT_FOUND", Message: "User not found"})
		return
	}

	if h.twoFAService == nil {
		c.JSON(http.StatusServiceUnavailable, entities.ErrorResponse{Code: "2FA_UNAVAILABLE", Message: "2FA service not available"})
		return
	}

	setup, err := h.twoFAService.GenerateSecret(ctx, userID, user.Email)
	if err != nil {
		h.logger.Error("Failed to generate 2FA secret", zap.Error(err), zap.String("user_id", userID.String()))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "2FA_SETUP_FAILED", Message: err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"secret":      setup.Secret,
		"qrCodeUrl":   setup.QRCodeURL,
		"backupCodes": setup.BackupCodes,
	})
}

// Disable2FA handles 2FA disabling
// @Summary Disable 2FA
// @Description Disables 2FA for the user after verification
// @Tags auth
// @Accept json
// @Produce json
// @Param request body object{code=string} true "Verification code"
// @Success 200 {object} map[string]string
// @Failure 400 {object} entities.ErrorResponse
// @Failure 401 {object} entities.ErrorResponse
// @Failure 500 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/users/me/disable-2fa [post]
func (h *AuthHandlers) Disable2FA(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "UNAUTHORIZED", Message: "User not authenticated"})
		return
	}

	var req struct {
		Code string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_REQUEST", Message: "Verification code is required"})
		return
	}

	if h.twoFAService == nil {
		c.JSON(http.StatusServiceUnavailable, entities.ErrorResponse{Code: "2FA_UNAVAILABLE", Message: "2FA service not available"})
		return
	}

	if err := h.twoFAService.Disable(ctx, userID, req.Code); err != nil {
		h.logger.Warn("Failed to disable 2FA", zap.Error(err), zap.String("user_id", userID.String()))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "DISABLE_FAILED", Message: err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "2FA disabled successfully"})
}

// GetKYCStatus handles GET /kyc/status
func (h *AuthHandlers) GetKYCStatus(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_USER_ID", Message: "Invalid or missing user ID"})
		return
	}

	status, err := h.onboardingService.GetKYCStatus(ctx, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "KYC_STATUS_ERROR", Message: "Failed to retrieve KYC status"})
		return
	}

	c.JSON(http.StatusOK, status)
}

// ProcessKYCCallback handles KYC provider callbacks
// @Summary Process KYC callback
// @Description Handles callbacks from KYC providers with verification results
// @Tags onboarding
// @Accept json
// @Produce json
// @Param provider_ref path string true "KYC provider reference"
// @Param request body map[string]interface{} true "KYC callback data"
// @Success 200 {object} map[string]interface{} "Callback processed"
// @Failure 400 {object} entities.ErrorResponse
// @Failure 500 {object} entities.ErrorResponse
// @Router /api/v1/onboarding/kyc/callback/{provider_ref} [post]
func (h *AuthHandlers) ProcessKYCCallback(c *gin.Context) {
	ctx := c.Request.Context()

	providerRef := c.Param("provider_ref")
	if providerRef == "" {
		h.logger.Warn("Missing provider reference in KYC callback")
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "MISSING_PROVIDER_REF",
			Message: "Provider reference is required",
		})
		return
	}

	h.logger.Info("Processing KYC callback",
		zap.String("provider_ref", providerRef),
		zap.String("request_id", common.GetRequestID(c)))

	var callbackData map[string]interface{}
	if err := c.ShouldBindJSON(&callbackData); err != nil {
		h.logger.Warn("Invalid KYC callback payload", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_CALLBACK",
			Message: "Invalid callback payload",
			Details: map[string]interface{}{"error": err.Error()},
		})
		return
	}

	// Extract status and rejection reasons from callback
	// This would depend on the specific KYC provider's callback format
	status := entities.KYCStatusProcessing
	var rejectionReasons []string

	var reviewResult map[string]interface{}
	if raw, ok := callbackData["reviewResult"]; ok {
		if rr, ok := raw.(map[string]interface{}); ok {
			reviewResult = rr
		}
	}
	if reviewResult == nil {
		if payloadRaw, ok := callbackData["payload"].(map[string]interface{}); ok {
			if rr, ok := payloadRaw["reviewResult"].(map[string]interface{}); ok {
				reviewResult = rr
			}
		}
	}

	if reviewResult != nil {
		if answer, ok := reviewResult["reviewAnswer"].(string); ok {
			switch strings.ToUpper(strings.TrimSpace(answer)) {
			case "GREEN":
				status = entities.KYCStatusApproved
			case "RED":
				status = entities.KYCStatusRejected
			}
		}
		if labels, ok := reviewResult["rejectLabels"].([]interface{}); ok {
			for _, label := range labels {
				switch v := label.(type) {
				case map[string]interface{}:
					if desc, ok := v["description"].(string); ok && desc != "" {
						rejectionReasons = append(rejectionReasons, desc)
					} else if code, ok := v["code"].(string); ok && code != "" {
						rejectionReasons = append(rejectionReasons, code)
					}
				case string:
					if strings.TrimSpace(v) != "" {
						rejectionReasons = append(rejectionReasons, strings.TrimSpace(v))
					}
				}
			}
		}
	}

	if status == entities.KYCStatusProcessing {
		if statusStr, ok := callbackData["status"].(string); ok {
			switch strings.ToLower(statusStr) {
			case "approved", "passed":
				status = entities.KYCStatusApproved
			case "rejected", "failed":
				status = entities.KYCStatusRejected
				if reasons, ok := callbackData["rejection_reasons"].([]interface{}); ok {
					for _, reason := range reasons {
						if reasonStr, ok := reason.(string); ok {
							rejectionReasons = append(rejectionReasons, reasonStr)
						}
					}
				}
			case "processing", "pending":
				status = entities.KYCStatusProcessing
			}
		}
	}

	// Process the callback
	err := h.onboardingService.ProcessKYCCallback(ctx, providerRef, status, rejectionReasons)
	if err != nil {
		h.logger.Error("Failed to process KYC callback",
			zap.Error(err),
			zap.String("provider_ref", providerRef),
			zap.String("status", string(status)))

		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "CALLBACK_PROCESSING_FAILED",
			Message: "Failed to process KYC callback",
			Details: map[string]interface{}{"error": "Internal server error"},
		})
		return
	}

	h.logger.Info("KYC callback processed successfully",
		zap.String("provider_ref", providerRef),
		zap.String("status", string(status)))

	c.JSON(http.StatusOK, gin.H{
		"message":      "Callback processed successfully",
		"provider_ref": providerRef,
		"status":       string(status),
	})
}

// CompleteOnboarding handles POST /onboarding/complete
// @Summary Complete onboarding with personal info and account creation
// @Description Completes onboarding by creating Due and Alpaca accounts with user's personal information
// @Tags onboarding
// @Accept json
// @Produce json
// @Param request body entities.OnboardingCompleteRequest true "Onboarding completion data"
// @Success 200 {object} entities.OnboardingCompleteResponse
// @Failure 400 {object} entities.ErrorResponse
// @Failure 500 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/onboarding/complete [post]
func (h *AuthHandlers) CompleteOnboarding(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_USER_ID", Message: "Invalid or missing user ID"})
		return
	}

	var req entities.OnboardingCompleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_REQUEST", Message: "Invalid request payload"})
		return
	}
	req.UserID = userID
	req.IPAddress = c.ClientIP()

	if req.Email != nil {
		authEmail := strings.TrimSpace(c.GetString("user_email"))
		if authEmail == "" || !strings.EqualFold(authEmail, strings.TrimSpace(*req.Email)) {
			c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "EMAIL_MISMATCH", Message: "Email does not match authenticated user"})
			return
		}
	}

	if err := h.validator.Struct(req); err != nil {
		h.logger.Error("Validation failed", zap.Error(err), zap.Any("request", req))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "VALIDATION_ERROR", Message: "Request validation failed"})
		return
	}

	response, err := h.onboardingService.CompleteOnboarding(ctx, &req)
	if err != nil {
		h.logger.Error("Failed to complete onboarding", zap.Error(err), zap.String("user_id", req.UserID.String()))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "ONBOARDING_COMPLETION_FAILED", Message: "Failed to complete onboarding"})
		return
	}

	// Return full status so client doesn't need follow-up calls
	fullResponse := gin.H{
		"user_id":            response.UserID,
		"bridge_customer_id": response.BridgeCustomerID,
		"alpaca_account_id":  response.AlpacaAccountID,
		"message":            response.Message,
		"next_steps":         response.NextSteps,
	}

	if onboardingStatus, err := h.onboardingService.GetOnboardingStatus(ctx, userID); err == nil {
		fullResponse["onboarding"] = onboardingStatus
	}
	if kycStatus, err := h.onboardingService.GetKYCStatus(ctx, userID); err == nil {
		fullResponse["kyc"] = kycStatus
	}

	h.logger.Info("Onboarding completed", zap.String("user_id", response.UserID.String()))
	c.JSON(http.StatusOK, fullResponse)
}

// Helper methods

func (h *AuthHandlers) getUserIDFromContext(c *gin.Context) (uuid.UUID, error) {
	// Try to get from authenticated user context first
	if userIDStr, exists := c.Get("user_id"); exists {
		if userID, ok := userIDStr.(uuid.UUID); ok {
			return userID, nil
		}
		if userIDStr, ok := userIDStr.(string); ok {
			return uuid.Parse(userIDStr)
		}
	}

	// Fallback to query parameter for development/admin use
	userIDQuery := c.Query("user_id")
	if userIDQuery != "" {
		return uuid.Parse(userIDQuery)
	}

	return uuid.Nil, fmt.Errorf("user ID not found in context or query parameters")
}

func extractSessionDetails(c *gin.Context) (ipAddress, userAgent, deviceFingerprint, location string) {
	ipAddress = c.ClientIP()
	userAgent = c.Request.UserAgent()

	deviceFingerprint = strings.TrimSpace(c.GetHeader("X-Device-Fingerprint"))
	if deviceFingerprint == "" {
		deviceFingerprint = security.GenerateFingerprint(
			userAgent,
			c.GetHeader("Accept-Language"),
			c.GetHeader("X-Screen-Res"),
			c.GetHeader("X-Timezone"),
		)
	}

	location = strings.TrimSpace(c.GetHeader("X-Geo-City"))
	if location == "" {
		location = strings.TrimSpace(c.GetHeader("X-Geo-Country"))
	}
	if location == "" {
		location = strings.TrimSpace(c.GetHeader("CF-IPCountry"))
	}
	return ipAddress, userAgent, deviceFingerprint, location
}

func (h *AuthHandlers) getUserID(c *gin.Context) (uuid.UUID, error) {
	userIDVal, exists := c.Get("user_id")
	if !exists {
		return uuid.Nil, fmt.Errorf("user ID not found in context")
	}
	if userID, ok := userIDVal.(uuid.UUID); ok {
		return userID, nil
	}
	return uuid.Nil, fmt.Errorf("invalid user ID type in context")
}

// Error type checking functions
func isUserAlreadyExistsError(err error) bool {
	// Implementation would check for specific error types
	// For now, check error message
	return err != nil && (contains(err.Error(), "user already exists") ||
		contains(err.Error(), "duplicate") ||
		contains(err.Error(), "conflict"))
}

func isKYCNotEligibleError(err error) bool {
	return err != nil && (contains(err.Error(), "cannot start KYC") ||
		contains(err.Error(), "not eligible"))
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		(len(s) > len(substr) &&
			(s[:len(substr)] == substr ||
				s[len(s)-len(substr):] == substr ||
				containsSubstring(s, substr))))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
