package auth

import (
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services"
	"github.com/rail-service/rail_service/internal/domain/services/onboarding"
	"github.com/rail-service/rail_service/internal/domain/services/twofa"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/rail-service/rail_service/pkg/auth"
	"github.com/rail-service/rail_service/pkg/crypto"
	"go.uber.org/zap"
)


// AuthHandlers consolidates authentication, signup, and onboarding handlers
type AuthHandlers struct {
	db                   *sql.DB
	cfg                  *config.Config
	logger               *zap.Logger
	userRepo             repositories.UserRepository
	verificationService  services.VerificationService
	onboardingJobService services.OnboardingJobService
	onboardingService    *onboarding.Service
	emailService         *adapters.EmailService
	kycProvider          *adapters.KYCProvider
	sessionService       SessionService
	twoFAService         TwoFAService
	redisClient          RedisClient
	validator            *validator.Validate
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
	onboardingJobService services.OnboardingJobService,
	onboardingService *onboarding.Service,
	emailService *adapters.EmailService,
	kycProvider *adapters.KYCProvider,
	sessionService SessionService,
	twoFAService TwoFAService,
	redisClient RedisClient,
) *AuthHandlers {
	return &AuthHandlers{
		db:                   db,
		cfg:                  cfg,
		logger:               logger,
		userRepo:             userRepo,
		verificationService:  verificationService,
		onboardingJobService: onboardingJobService,
		onboardingService:    onboardingService,
		emailService:         emailService,
		kycProvider:          kycProvider,
		sessionService:       sessionService,
		twoFAService:         twoFAService,
		redisClient:          redisClient,
		validator:            validator.New(),
	}
}

// SignUp handles user registration and sends a verification code
// @Summary Register a new user and send verification code
// @Description Create a new user account and initiate email/phone verification
// @Tags auth
// @Accept json
// @Produce json
// @Param request body entities.SignUpRequest true "Signup data (email or phone, and password)"
// @Success 202 {object} entities.SignUpResponse "Verification code sent"
// @Failure 400 {object} entities.ErrorResponse
// @Failure 409 {object} entities.ErrorResponse
// @Failure 500 {object} entities.ErrorResponse
// @Router /api/v1/auth/signup [post]
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

	if req.Email != nil {
		identifier = strings.TrimSpace(*req.Email)
		identifierType = "email"

		// Check if verified user already exists
		existingUser, err := h.userRepo.GetByEmail(ctx, identifier)
		if err == nil && existingUser != nil && existingUser.EmailVerified {
			c.JSON(http.StatusConflict, entities.ErrorResponse{
				Code:    "USER_EXISTS",
				Message: "User already exists with this email",
			})
			return
		}
	} else {
		identifier = strings.TrimSpace(*req.Phone)
		identifierType = "phone"

		// Check if verified user already exists
		exists, err := h.userRepo.PhoneExists(ctx, identifier)
		if err != nil {
			h.logger.Error("Failed to check phone existence", zap.Error(err))
			c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
				Code:    "INTERNAL_ERROR",
				Message: "Internal server error",
			})
			return
		}
		if exists {
			c.JSON(http.StatusConflict, entities.ErrorResponse{
				Code:    "USER_EXISTS",
				Message: "User already exists with this phone",
			})
			return
		}
	}

	if identifier == "" {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "VALIDATION_ERROR",
			Message: fmt.Sprintf("%s cannot be empty", identifierType),
		})
		return
	}

	// Hash password for pending registration
	passwordHash, err := crypto.HashPassword(req.Password)
	if err != nil {
		h.logger.Error("Failed to hash password", zap.Error(err))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "PASSWORD_HASH_FAILED",
			Message: "Failed to process password",
		})
		return
	}

	// Store pending registration in Redis (expires in 10 minutes)
	pendingTTL := 10 * time.Minute
	pending := entities.PendingRegistration{
		PasswordHash: passwordHash,
		CreatedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(pendingTTL),
	}
	if identifierType == "email" {
		pending.Email = identifier
	} else {
		pending.Phone = identifier
	}

	pendingKey := fmt.Sprintf("pending_registration:%s:%s", identifierType, identifier)
	if err := h.redisClient.Set(ctx, pendingKey, pending, pendingTTL); err != nil {
		h.logger.Error("Failed to store pending registration", zap.Error(err))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "INTERNAL_ERROR",
			Message: "Failed to process registration",
		})
		return
	}

	// Send verification code (in development, log it instead of sending)
	code, err := h.verificationService.GenerateAndSendCode(ctx, identifierType, identifier)
	if err != nil {
		h.logger.Error("Failed to send verification code", zap.Error(err), zap.String("identifier", identifier))
		// In development, continue even if email fails
		if h.cfg.Environment != "development" {
			c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
				Code:    "VERIFICATION_SEND_FAILED",
				Message: "Failed to send verification code. Please try again.",
			})
			return
		}
		h.logger.Info("Development mode: verification code generated", zap.String("code", code), zap.String("identifier", identifier))
	}

	h.logger.Info("Pending registration created, verification code sent", zap.String("identifier", identifier))
	c.JSON(http.StatusAccepted, entities.SignUpResponse{
		Message:    fmt.Sprintf("Verification code sent to %s. Please verify to complete registration.", identifier),
		Identifier: identifier,
	})
}

func (h *AuthHandlers) bootstrapKYCApplicant(ctx context.Context, user *entities.User) {
	if h.kycProvider == nil || user == nil {
		return
	}

	if strings.ToLower(h.cfg.KYC.Provider) != "sumsub" {
		return
	}

	applicantID, err := h.kycProvider.EnsureApplicant(ctx, user.ID, nil)
	if err != nil {
		h.logger.Warn("Failed to initialize KYC applicant", zap.Error(err), zap.String("user_id", user.ID.String()))
		return
	}
	if applicantID == "" {
		h.logger.Debug("No KYC applicant created for provider",
			zap.String("user_id", user.ID.String()),
			zap.String("provider", strings.ToLower(h.cfg.KYC.Provider)))
		return
	}

	if err := h.userRepo.UpdateKYCProvider(ctx, user.ID, applicantID, entities.KYCStatusPending); err != nil {
		h.logger.Warn("Failed to update user with KYC provider reference", zap.Error(err), zap.String("user_id", user.ID.String()))
		return
	}

	h.logger.Info("Initialized KYC applicant with Sumsub",
		zap.String("user_id", user.ID.String()),
		zap.String("provider_ref", applicantID))
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
// @Router /api/v1/auth/verify-code [post]
func (h *AuthHandlers) VerifyCode(c *gin.Context) {
	ctx := c.Request.Context()

	var req entities.VerifyCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("Invalid verify code request", zap.Error(err))
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
	} else {
		identifier = strings.TrimSpace(*req.Phone)
		identifierType = "phone"
	}

	// Verify the code first
	isValid, err := h.verificationService.VerifyCode(ctx, identifierType, identifier, req.Code)
	if err != nil || !isValid {
		h.logger.Warn("Verification code invalid or expired", zap.Error(err), zap.String("identifier", identifier))
		errMsg := "Invalid or expired verification code"
		if err != nil {
			errMsg = err.Error()
		}
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{
			Code:    "INVALID_CODE",
			Message: errMsg,
		})
		return
	}

	// Check for pending registration in Redis
	pendingKey := fmt.Sprintf("pending_registration:%s:%s", identifierType, identifier)
	var pending entities.PendingRegistration
	err = h.redisClient.Get(ctx, pendingKey, &pending)
	if err != nil {
		h.logger.Warn("No pending registration found", zap.Error(err), zap.String("identifier", identifier))
		c.JSON(http.StatusNotFound, entities.ErrorResponse{
			Code:    "REGISTRATION_NOT_FOUND",
			Message: "No pending registration found. Please register first.",
		})
		return
	}

	// Create the user now that verification is complete
	var phone *string
	email := ""
	if identifierType == "email" {
		email = identifier
	} else {
		phone = &identifier
	}

	user, err := h.userRepo.CreateUserWithHash(ctx, email, phone, pending.PasswordHash)
	if err != nil {
		h.logger.Error("Failed to create user after verification", zap.Error(err), zap.String("identifier", identifier))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "USER_CREATION_FAILED",
			Message: "Failed to create user account",
		})
		return
	}

	// Mark as verified
	if identifierType == "email" {
		user.EmailVerified = true
	} else {
		user.PhoneVerified = true
	}
	user.OnboardingStatus = entities.OnboardingStatusWalletsPending

	userProfile := &entities.UserProfile{
		ID:               user.ID,
		Email:            user.Email,
		Phone:            user.Phone,
		EmailVerified:    user.EmailVerified,
		PhoneVerified:    user.PhoneVerified,
		OnboardingStatus: user.OnboardingStatus,
		KYCStatus:        user.KYCStatus,
	}
	if err := h.userRepo.Update(ctx, userProfile); err != nil {
		h.logger.Error("Failed to update user verification status", zap.Error(err), zap.String("user_id", user.ID.String()))
	}

	// Clean up pending registration
	_ = h.redisClient.Del(ctx, pendingKey)

	// Bootstrap KYC (non-blocking)
	go h.bootstrapKYCApplicant(context.Background(), user)

	// Trigger async onboarding jobs
	userPhone := ""
	if user.Phone != nil {
		userPhone = *user.Phone
	}
	_, _ = h.onboardingJobService.CreateOnboardingJob(ctx, user.ID, user.Email, userPhone)

	// Generate JWT tokens
	tokens, err := auth.GenerateTokenPair(
		user.ID,
		user.Email,
		"user",
		h.cfg.JWT.Secret,
		h.cfg.JWT.AccessTTL,
		h.cfg.JWT.RefreshTTL,
	)
	if err != nil {
		h.logger.Error("Failed to generate tokens after verification", zap.Error(err))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "TOKEN_GENERATION_FAILED",
			Message: "Failed to generate authentication tokens",
		})
		return
	}

	h.logger.Info("User created and verified", zap.String("user_id", user.ID.String()), zap.String("identifier", identifier))
	c.JSON(http.StatusOK, entities.VerifyCodeResponse{
		User: &entities.UserInfo{
			ID:               user.ID,
			Email:            user.Email,
			Phone:            user.Phone,
			EmailVerified:    user.EmailVerified,
			PhoneVerified:    user.PhoneVerified,
			OnboardingStatus: user.OnboardingStatus,
			KYCStatus:        user.KYCStatus,
			CreatedAt:        user.CreatedAt,
		},
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		ExpiresAt:    tokens.ExpiresAt,
	})
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
	var userProfile *entities.UserProfile
	var err error

	if req.Email != nil {
		identifier = *req.Email
		identifierType = "email"
		userProfile, err = h.userRepo.GetByEmail(ctx, identifier)
	} else {
		identifier = *req.Phone
		identifierType = "phone"
		userProfile, err = h.userRepo.GetByPhone(ctx, identifier)
	}

	if err != nil {
		h.logger.Error("Failed to get user", zap.Error(err), zap.String("identifier", identifier), zap.String("identifierType", identifierType))
		if common.IsUserNotFoundError(err) {
			c.JSON(http.StatusNotFound, entities.ErrorResponse{
				Code:    "USER_NOT_FOUND",
				Message: "User not found",
			})
		} else {
			c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
				Code:    "INTERNAL_ERROR",
				Message: "Internal server error",
			})
		}
		return
	}

	// Check if already verified
	if (identifierType == "email" && userProfile.EmailVerified) || (identifierType == "phone" && userProfile.PhoneVerified) {
		c.JSON(http.StatusOK, entities.ErrorResponse{
			Code:    "ALREADY_VERIFIED",
			Message: fmt.Sprintf("%s is already verified", identifierType),
		})
		return
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

	// Generate and send new code
	_, err = h.verificationService.GenerateAndSendCode(ctx, identifierType, identifier)
	if err != nil {
		h.logger.Error("Failed to resend verification code", zap.Error(err), zap.String("identifier", identifier))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "VERIFICATION_SEND_FAILED",
			Message: "Failed to resend verification code. Please try again.",
		})
		return
	}

	h.logger.Info("Verification code re-sent", zap.String("user_id", userProfile.ID.String()), zap.String("identifier", identifier))
	c.JSON(http.StatusAccepted, entities.SignUpResponse{
		Message:    fmt.Sprintf("New verification code sent to %s.", identifier),
		Identifier: identifier,
	})
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
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("Invalid login request", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_REQUEST",
			Message: "Invalid request payload",
			Details: map[string]interface{}{"error": err.Error()},
		})
		return
	}

	// Basic validation
	if req.Email == "" || req.Password == "" {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "VALIDATION_ERROR",
			Message: "Email and password are required",
		})
		return
	}

	// Get user by email
	user, err := h.userRepo.GetUserByEmailForLogin(ctx, req.Email)
	if err != nil {
		h.logger.Warn("Login attempt failed - user not found", zap.String("email", req.Email), zap.Error(err))
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{
			Code:    "INVALID_CREDENTIALS",
			Message: "Invalid email or password",
		})
		return
	}

	// Validate password
	if !h.userRepo.ValidatePassword(req.Password, user.PasswordHash) {
		h.logger.Warn("Login attempt failed - invalid password", zap.String("email", req.Email))
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{
			Code:    "INVALID_CREDENTIALS",
			Message: "Invalid email or password",
		})
		return
	}

	// Check if user is active
	if !user.IsActive {
		h.logger.Warn("Login attempt failed - user account inactive", zap.String("email", req.Email))
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

	// Update last login timestamp
	if err := h.userRepo.UpdateLastLogin(ctx, user.ID); err != nil {
		h.logger.Warn("Failed to update last login", zap.Error(err), zap.String("user_id", user.ID.String()))
		// Don't fail login for this
	}

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

// VerifyEmail handles email verification via verification code
// This endpoint requires a valid verification code sent to the user's email
// @Summary Verify email address
// @Description Verify user's email using the verification code sent to their email
// @Tags auth
// @Accept json
// @Produce json
// @Param request body entities.VerifyEmailRequest true "Email verification request with code"
// @Success 200 {object} map[string]string
// @Failure 400 {object} entities.ErrorResponse
// @Failure 401 {object} entities.ErrorResponse
// @Failure 404 {object} entities.ErrorResponse
// @Failure 500 {object} entities.ErrorResponse
// @Router /api/v1/auth/verify-email [post]
func (h *AuthHandlers) VerifyEmail(c *gin.Context) {
	ctx := c.Request.Context()

	var req struct {
		Email string `json:"email" binding:"required,email"`
		Code  string `json:"code" binding:"required,len=6"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_REQUEST",
			Message: "Email and 6-digit verification code are required",
			Details: map[string]interface{}{"error": err.Error()},
		})
		return
	}

	// Verify the code using the verification service
	isValid, err := h.verificationService.VerifyCode(ctx, "email", req.Email, req.Code)
	if err != nil || !isValid {
		h.logger.Warn("Email verification code invalid or expired",
			zap.Error(err),
			zap.String("email", req.Email))
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{
			Code:    "INVALID_CODE",
			Message: "Invalid or expired verification code",
		})
		return
	}

	// Find user by email
	user, err := h.userRepo.GetByEmail(ctx, req.Email)
	if err != nil {
		c.JSON(http.StatusNotFound, entities.ErrorResponse{
			Code:    "USER_NOT_FOUND",
			Message: "User not found",
		})
		return
	}

	// Mark email as verified
	user.EmailVerified = true
	if err := h.userRepo.Update(ctx, user); err != nil {
		h.logger.Error("Failed to update user email verification status",
			zap.Error(err),
			zap.String("email", req.Email))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "VERIFY_FAILED",
			Message: "Failed to verify email",
		})
		return
	}

	h.logger.Info("Email verified successfully", zap.String("email", req.Email))
	c.JSON(http.StatusOK, gin.H{"message": "Email verified successfully"})
}

// GetProfile returns user profile
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
	c.JSON(http.StatusOK, user.ToUserInfo())
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



// StartOnboarding handles POST /onboarding/start
// @Summary Start user onboarding
// @Description Initiates the onboarding process for a new user with email/phone verification
// @Tags onboarding
// @Accept json
// @Produce json
// @Param request body entities.OnboardingStartRequest true "Onboarding start data"
// @Success 201 {object} entities.OnboardingStartResponse
// @Failure 400 {object} entities.ErrorResponse
// @Failure 409 {object} entities.ErrorResponse "User already exists"
// @Failure 500 {object} entities.ErrorResponse
// @Router /api/v1/onboarding/start [post]
func (h *AuthHandlers) StartOnboarding(c *gin.Context) {
	ctx := c.Request.Context()

	h.logger.Info("Starting onboarding process",
		zap.String("request_id", common.GetRequestID(c)),
		zap.String("ip", c.ClientIP()))

	var req entities.OnboardingStartRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("Invalid request payload", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_REQUEST",
			Message: "Invalid request payload",
			Details: map[string]interface{}{"error": err.Error()},
		})
		return
	}

	// Validate request
	if err := h.validator.Struct(req); err != nil {
		h.logger.Warn("Request validation failed", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "VALIDATION_ERROR",
			Message: "Request validation failed",
			Details: map[string]interface{}{"validation_errors": err.Error()},
		})
		return
	}

	// Process onboarding start
	response, err := h.onboardingService.StartOnboarding(ctx, &req)
	if err != nil {
		h.logger.Error("Failed to start onboarding",
			zap.Error(err),
			zap.String("email", req.Email))

		// Check for specific error types
		if isUserAlreadyExistsError(err) {
			c.JSON(http.StatusConflict, entities.ErrorResponse{
				Code:    "USER_EXISTS",
				Message: "User already exists with this email",
				Details: map[string]interface{}{"email": req.Email},
			})
			return
		}

		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "ONBOARDING_FAILED",
			Message: "Failed to start onboarding process",
			Details: map[string]interface{}{"error": "Internal server error"},
		})
		return
	}

	h.logger.Info("Onboarding started successfully",
		zap.String("user_id", response.UserID.String()),
		zap.String("email", req.Email))

	c.JSON(http.StatusCreated, response)
}

// GetOnboardingStatus handles GET /onboarding/status
// @Summary Get onboarding status
// @Description Returns the current onboarding status for the authenticated user
// @Tags onboarding
// @Produce json
// @Param user_id query string false "User ID (for admin use)"
// @Success 200 {object} entities.OnboardingStatusResponse
// @Failure 400 {object} entities.ErrorResponse
// @Failure 404 {object} entities.ErrorResponse "User not found"
// @Failure 500 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/onboarding/status [get]
func (h *AuthHandlers) GetOnboardingStatus(c *gin.Context) {
	ctx := c.Request.Context()

	// Get user ID from authenticated context or query parameter
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		h.logger.Warn("Invalid or missing user ID", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_USER_ID",
			Message: "Invalid or missing user ID",
			Details: map[string]interface{}{"error": err.Error()},
		})
		return
	}

	h.logger.Debug("Getting onboarding status",
		zap.String("user_id", userID.String()),
		zap.String("request_id", common.GetRequestID(c)))

	// Get onboarding status
	response, err := h.onboardingService.GetOnboardingStatus(ctx, userID)
	if err != nil {
		h.logger.Error("Failed to get onboarding status",
			zap.Error(err),
			zap.String("user_id", userID.String()))

		// Handle inactive account explicitly
		if strings.Contains(strings.ToLower(err.Error()), "inactive") {
			c.JSON(http.StatusForbidden, entities.ErrorResponse{
				Code:    "USER_INACTIVE",
				Message: "User account is inactive",
				Details: map[string]interface{}{"user_id": userID.String()},
			})
			return
		}

		if common.IsUserNotFoundError(err) {
			c.JSON(http.StatusNotFound, entities.ErrorResponse{
				Code:    "USER_NOT_FOUND",
				Message: "User not found",
				Details: map[string]interface{}{"user_id": userID.String()},
			})
			return
		}

		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "STATUS_RETRIEVAL_FAILED",
			Message: "Failed to retrieve onboarding status",
			Details: map[string]interface{}{"error": "Internal server error"},
		})
		return
	}

	h.logger.Debug("Retrieved onboarding status successfully",
		zap.String("user_id", userID.String()),
		zap.String("status", string(response.OnboardingStatus)))

	c.JSON(http.StatusOK, response)
}

// GetOnboardingProgress handles GET /onboarding/progress
// @Summary Get onboarding progress
// @Description Returns detailed progress information with checklist and completion percentage
// @Tags onboarding
// @Produce json
// @Success 200 {object} entities.OnboardingProgressResponse
// @Failure 400 {object} entities.ErrorResponse
// @Failure 401 {object} entities.ErrorResponse
// @Failure 500 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/onboarding/progress [get]
func (h *AuthHandlers) GetOnboardingProgress(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		h.logger.Warn("Invalid or missing user ID for progress", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_USER_ID",
			Message: "Invalid or missing user ID",
		})
		return
	}

	progress, err := h.onboardingService.GetOnboardingProgress(ctx, userID)
	if err != nil {
		h.logger.Error("Failed to get onboarding progress",
			zap.Error(err),
			zap.String("user_id", userID.String()))

		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "PROGRESS_ERROR",
			Message: "Failed to retrieve onboarding progress",
		})
		return
	}

	c.JSON(http.StatusOK, progress)
}

// GetKYCStatus handles GET /kyc/status
// @Summary Get KYC status
// @Description Returns the user's current KYC verification status and guidance
// @Tags onboarding
// @Produce json
// @Success 200 {object} entities.KYCStatusResponse
// @Failure 400 {object} entities.ErrorResponse
// @Failure 401 {object} entities.ErrorResponse
// @Failure 500 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/kyc/status [get]
func (h *AuthHandlers) GetKYCStatus(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		h.logger.Warn("Invalid or missing user ID for KYC status", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_USER_ID",
			Message: "Invalid or missing user ID",
			Details: map[string]interface{}{"error": err.Error()},
		})
		return
	}

	status, err := h.onboardingService.GetKYCStatus(ctx, userID)
	if err != nil {
		h.logger.Error("Failed to get KYC status",
			zap.Error(err),
			zap.String("user_id", userID.String()))

		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "KYC_STATUS_ERROR",
			Message: "Failed to retrieve KYC status",
		})
		return
	}

	c.JSON(http.StatusOK, status)
}

// GetKYCVerificationURL handles GET /kyc/verification-url
// @Summary Get KYC verification URL
// @Description Generates a URL for the user to complete KYC verification with the provider
// @Tags onboarding
// @Produce json
// @Success 200 {object} map[string]interface{} "KYC verification URL"
// @Failure 400 {object} entities.ErrorResponse
// @Failure 401 {object} entities.ErrorResponse
// @Failure 500 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/kyc/verification-url [get]
func (h *AuthHandlers) GetKYCVerificationURL(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		h.logger.Warn("Invalid or missing user ID for KYC URL", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_USER_ID",
			Message: "Invalid or missing user ID",
		})
		return
	}

	if h.kycProvider == nil {
		h.logger.Error("KYC provider not configured")
		c.JSON(http.StatusServiceUnavailable, entities.ErrorResponse{
			Code:    "KYC_UNAVAILABLE",
			Message: "KYC verification is not available",
		})
		return
	}

	url, err := h.kycProvider.GenerateKYCURL(ctx, userID)
	if err != nil {
		h.logger.Error("Failed to generate KYC URL",
			zap.Error(err),
			zap.String("user_id", userID.String()))

		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "KYC_URL_ERROR",
			Message: "Failed to generate KYC verification URL",
		})
		return
	}

	h.logger.Info("Generated KYC verification URL",
		zap.String("user_id", userID.String()))

	c.JSON(http.StatusOK, gin.H{
		"url":     url,
		"expires": "30m",
		"message": "Complete your identity verification using this link",
	})
}

// SubmitKYC handles POST /onboarding/kyc/submit
// @Summary Submit KYC documents
// @Description Submits KYC documents for verification
// @Tags onboarding
// @Accept json
// @Produce json
// @Param request body entities.KYCSubmitRequest true "KYC submission data"
// @Success 202 {object} map[string]interface{} "KYC submission accepted"
// @Failure 400 {object} entities.ErrorResponse
// @Failure 403 {object} entities.ErrorResponse "User not eligible for KYC"
// @Failure 500 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/onboarding/kyc/submit [post]
func (h *AuthHandlers) SubmitKYC(c *gin.Context) {
	ctx := c.Request.Context()

	// Get user ID from authenticated context
	userID, err := common.GetUserID(c)
	if err != nil {
		h.logger.Warn("Invalid or missing user ID", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_USER_ID",
			Message: "Invalid or missing user ID",
			Details: map[string]interface{}{"error": err.Error()},
		})
		return
	}

	h.logger.Info("Submitting KYC documents",
		zap.String("user_id", userID.String()),
		zap.String("request_id", common.GetRequestID(c)))

	var req entities.KYCSubmitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("Invalid KYC request payload", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_REQUEST",
			Message: "Invalid KYC request payload",
			Details: map[string]interface{}{"error": err.Error()},
		})
		return
	}

	// Validate request
	if err := h.validator.Struct(req); err != nil {
		h.logger.Warn("KYC request validation failed", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "VALIDATION_ERROR",
			Message: "KYC request validation failed",
			Details: map[string]interface{}{"validation_errors": err.Error()},
		})
		return
	}

	// Submit KYC
	err = h.onboardingService.SubmitKYC(ctx, userID, &req)
	if err != nil {
		h.logger.Error("Failed to submit KYC",
			zap.Error(err),
			zap.String("user_id", userID.String()))

		if isKYCNotEligibleError(err) {
			c.JSON(http.StatusForbidden, entities.ErrorResponse{
				Code:    "KYC_NOT_ELIGIBLE",
				Message: "User is not eligible for KYC submission",
				Details: map[string]interface{}{"error": err.Error()},
			})
			return
		}

		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "KYC_SUBMISSION_FAILED",
			Message: "Failed to submit KYC documents",
			Details: map[string]interface{}{"error": "Internal server error"},
		})
		return
	}

	h.logger.Info("KYC submitted successfully",
		zap.String("user_id", userID.String()))

	c.JSON(http.StatusAccepted, gin.H{
		"message": "KYC documents submitted successfully",
		"status":  "processing",
		"user_id": userID.String(),
		"next_steps": []string{
			"Wait for KYC review",
			"You can continue using core features while verification completes",
			"KYC unlocks virtual accounts, cards, and fiat withdrawals",
		},
	})
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

	// Get user ID from authenticated context
	userID, err := common.GetUserID(c)
	if err != nil {
		h.logger.Warn("Invalid or missing user ID", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_USER_ID",
			Message: "Invalid or missing user ID",
			Details: map[string]interface{}{"error": err.Error()},
		})
		return
	}

	h.logger.Info("Completing onboarding",
		zap.String("user_id", userID.String()),
		zap.String("request_id", common.GetRequestID(c)))

	var req entities.OnboardingCompleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("Invalid onboarding complete request payload", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_REQUEST",
			Message: "Invalid request payload",
			Details: map[string]interface{}{"error": err.Error()},
		})
		return
	}

	// Set user ID from authenticated context
	req.UserID = userID

	// Validate request
	if err := h.validator.Struct(req); err != nil {
		h.logger.Warn("Onboarding complete request validation failed", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "VALIDATION_ERROR",
			Message: "Request validation failed",
			Details: map[string]interface{}{"validation_errors": err.Error()},
		})
		return
	}

	// Complete onboarding
	response, err := h.onboardingService.CompleteOnboarding(ctx, &req)
	if err != nil {
		h.logger.Error("Failed to complete onboarding",
			zap.Error(err),
			zap.String("user_id", req.UserID.String()),
			zap.String("first_name", req.FirstName),
			zap.String("last_name", req.LastName),
			zap.String("country", req.Country),
			zap.String("request_id", common.GetRequestID(c)))

		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "ONBOARDING_COMPLETION_FAILED",
			Message: "Failed to complete onboarding",
			Details: map[string]interface{}{"error": "Internal server error"},
		})
		return
	}

	h.logger.Info("Onboarding completed successfully",
		zap.String("user_id", response.UserID.String()),
		zap.String("bridge_customer_id", response.BridgeCustomerID),
		zap.String("alpaca_account_id", response.AlpacaAccountID))

	c.JSON(http.StatusOK, response)
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