package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/stack-service/stack_service/internal/domain/entities"
	"github.com/stack-service/stack_service/internal/domain/services"
	"github.com/stack-service/stack_service/internal/domain/services/onboarding"
	"github.com/stack-service/stack_service/internal/infrastructure/adapters"
	"github.com/stack-service/stack_service/internal/infrastructure/config"
	"github.com/stack-service/stack_service/internal/infrastructure/repositories"
	"github.com/stack-service/stack_service/pkg/auth"
	"github.com/stack-service/stack_service/pkg/crypto"
	"github.com/stack-service/stack_service/pkg/logger"

	"github.com/gin-gonic/gin"
)

// AuthSignupHandlers holds dependencies for authentication-related handlers
type AuthSignupHandlers struct {
	db                   *sql.DB
	cfg                  *config.Config
	logger               *zap.Logger
	userRepo             repositories.UserRepository
	verificationService  services.VerificationService
	onboardingJobService services.OnboardingJobService
	onboardingService    *onboarding.Service
	emailService         *adapters.EmailService
	kycProvider          *adapters.KYCProvider
}

// NewAuthSignupHandlers creates a new instance of AuthSignupHandlers
func NewAuthSignupHandlers(
	db *sql.DB,
	cfg *config.Config,
	logger *zap.Logger,
	userRepo repositories.UserRepository,
	verificationService services.VerificationService,
	onboardingJobService services.OnboardingJobService,
	onboardingService *onboarding.Service,
	emailService *adapters.EmailService,
	kycProvider *adapters.KYCProvider,
) *AuthSignupHandlers {
	return &AuthSignupHandlers{
		db:                   db,
		cfg:                  cfg,
		logger:               logger,
		userRepo:             userRepo,
		verificationService:  verificationService,
		onboardingJobService: onboardingJobService,
		onboardingService:    onboardingService,
		emailService:         emailService,
		kycProvider:          kycProvider,
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
func (h *AuthSignupHandlers) Register(c *gin.Context) {
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

	if req.Email != nil {
		identifier = strings.TrimSpace(*req.Email)
		identifierType = "email"

		var err error
		existingUser, err = h.userRepo.GetByEmail(ctx, identifier)
		if err != nil {
			if !isUserNotFoundError(err) {
				h.logger.Error("Failed to check existing user by email", zap.Error(err), zap.String("email", identifier))
				c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
					Code:    "INTERNAL_ERROR",
					Message: "Internal server error",
				})
				return
			}
			existingUser = nil
		}
	} else {
		identifier = strings.TrimSpace(*req.Phone)
		identifierType = "phone"

		exists, err := h.userRepo.PhoneExists(ctx, identifier)
		if err != nil {
			h.logger.Error("Failed to check phone existence", zap.Error(err), zap.String("phone", identifier))
			c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
				Code:    "INTERNAL_ERROR",
				Message: "Internal server error",
			})
			return
		}

		if exists {
			c.JSON(http.StatusConflict, entities.ErrorResponse{
				Code:    "USER_EXISTS",
				Message: fmt.Sprintf("User already exists with this %s", identifierType),
				Details: map[string]interface{}{identifierType: identifier},
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

	if existingUser != nil {
		if existingUser.EmailVerified {
			c.JSON(http.StatusConflict, entities.ErrorResponse{
				Code:    "USER_EXISTS",
				Message: fmt.Sprintf("User already exists with this %s", identifierType),
				Details: map[string]interface{}{identifierType: identifier},
			})
			return
		}

		passwordHash, err := crypto.HashPassword(req.Password)
		if err != nil {
			h.logger.Error("Failed to hash password for existing unverified user", zap.Error(err), zap.String("identifier", identifier))
			c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
				Code:    "PASSWORD_HASH_FAILED",
				Message: "Failed to process password",
			})
			return
		}

		if err := h.userRepo.UpdatePassword(ctx, existingUser.ID, passwordHash); err != nil {
			h.logger.Error("Failed to refresh password for existing unverified user", zap.Error(err), zap.String("user_id", existingUser.ID.String()))
			c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
				Code:    "USER_UPDATE_FAILED",
				Message: "Failed to update existing account",
			})
			return
		}

		if existingUser.OnboardingStatus != entities.OnboardingStatusStarted {
			if err := h.userRepo.UpdateOnboardingStatus(ctx, existingUser.ID, entities.OnboardingStatusStarted); err != nil {
				h.logger.Warn("Failed to reset onboarding status for existing unverified user",
					zap.Error(err),
					zap.String("user_id", existingUser.ID.String()))
			}
		}

		if _, err := h.verificationService.GenerateAndSendCode(ctx, identifierType, identifier); err != nil {
			h.logger.Error("Failed to send verification code for existing unverified user", zap.Error(err), zap.String("identifier", identifier))
			c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
				Code:    "VERIFICATION_SEND_FAILED",
				Message: "Failed to send verification code. Please try again.",
			})
			return
		}

		h.logger.Info("Resent verification code for existing unverified user", zap.String("user_id", existingUser.ID.String()), zap.String("identifier", identifier))
		c.JSON(http.StatusAccepted, entities.SignUpResponse{
			Message:    fmt.Sprintf("Verification code sent to %s. Please verify your account.", identifier),
			Identifier: identifier,
		})
		return
	}

	email := ""
	if identifierType == "email" {
		email = identifier
	}

	// Store user in DB (unverified)
	registeredUser, err := h.userRepo.CreateUserFromAuth(ctx, &entities.RegisterRequest{
		Email:    email,
		Phone:    req.Phone,
		Password: req.Password,
	})
	if err != nil {
		h.logger.Error("Failed to create user in DB", zap.Error(err), zap.String("identifier", identifier))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "USER_CREATION_FAILED",
			Message: "Failed to create user account",
		})
		return
	}

	h.bootstrapKYCApplicant(ctx, registeredUser)

	// Send verification code
	_, err = h.verificationService.GenerateAndSendCode(ctx, identifierType, identifier)
	if err != nil {
		h.logger.Error("Failed to send verification code", zap.Error(err), zap.String("identifier", identifier))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "VERIFICATION_SEND_FAILED",
			Message: "Failed to send verification code. Please try again.",
		})
		return
	}

	if registeredUser.Email != "" && h.emailService != nil {
		if err := h.emailService.SendWelcomeEmail(ctx, registeredUser.Email); err != nil {
			h.logger.Warn("Failed to send welcome email", zap.Error(err), zap.String("email", registeredUser.Email))
		}
	}

	h.logger.Info("User signed up and verification code sent", zap.String("user_id", registeredUser.ID.String()), zap.String("identifier", identifier))
	c.JSON(http.StatusAccepted, entities.SignUpResponse{
		Message:    fmt.Sprintf("Verification code sent to %s. Please verify your account.", identifier),
		Identifier: identifier,
	})
}

func (h *AuthSignupHandlers) bootstrapKYCApplicant(ctx context.Context, user *entities.User) {
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
func (h *AuthSignupHandlers) VerifyCode(c *gin.Context) {
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
	var userProfile *entities.UserProfile
	var err error

	if req.Email != nil {
		identifier = *req.Email
		identifierType = "email"
		userProfile, err = h.userRepo.GetByEmail(ctx, identifier)
	} else {
		identifier = *req.Phone
		identifierType = "phone"
		// For now, we'll need to implement GetByPhone or use a different approach
		h.logger.Warn("Phone verification not yet implemented", zap.String("identifier", identifier))
		c.JSON(http.StatusNotImplemented, entities.ErrorResponse{
			Code:    "NOT_IMPLEMENTED",
			Message: "Phone verification not yet implemented",
		})
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

	// Verify the code
	isValid, err := h.verificationService.VerifyCode(ctx, identifierType, identifier, req.Code)
	if err != nil || !isValid {
		h.logger.Warn("Verification code invalid or expired", zap.Error(err), zap.String("identifier", identifier))
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{
			Code:    "INVALID_CODE",
			Message: err.Error(),
		})
		return
	}

	// Mark user as verified
	if identifierType == "email" {
		userProfile.EmailVerified = true
	} else {
		userProfile.PhoneVerified = true
	}
	userProfile.OnboardingStatus = entities.OnboardingStatusWalletsPending
	if err := h.userRepo.Update(ctx, userProfile); err != nil {
		h.logger.Error("Failed to update user verification status", zap.Error(err), zap.String("user_id", userProfile.ID.String()))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "UPDATE_FAILED",
			Message: "Failed to update user verification status",
		})
		return
	}

	if err := h.onboardingService.CompleteEmailVerification(ctx, userProfile.ID); err != nil {
		h.logger.Warn("Failed to advance onboarding after email verification",
			zap.Error(err),
			zap.String("user_id", userProfile.ID.String()))
	} else {
		userProfile.OnboardingStatus = entities.OnboardingStatusWalletsPending
	}

	// Trigger async onboarding jobs (KYC, Wallet, Welcome Email)
	userPhone := ""
	if userProfile.Phone != nil {
		userPhone = *userProfile.Phone
	}

	_, err = h.onboardingJobService.CreateOnboardingJob(ctx, userProfile.ID, userProfile.Email, userPhone)
	if err != nil {
		h.logger.Error("Failed to create onboarding job", zap.Error(err), zap.String("user_id", userProfile.ID.String()))
	}

	// Generate JWT tokens
	tokens, err := auth.GenerateTokenPair(
		userProfile.ID,
		userProfile.Email,
		"user", // Default role
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

	h.logger.Info("Account verified and tokens issued", zap.String("user_id", userProfile.ID.String()), zap.String("identifier", identifier))
	c.JSON(http.StatusOK, entities.VerifyCodeResponse{
		User: &entities.UserInfo{
			ID:               userProfile.ID,
			Email:            userProfile.Email,
			Phone:            userProfile.Phone,
			EmailVerified:    userProfile.EmailVerified,
			PhoneVerified:    userProfile.PhoneVerified,
			OnboardingStatus: userProfile.OnboardingStatus,
			KYCStatus:        userProfile.KYCStatus,
			CreatedAt:        userProfile.CreatedAt,
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
func (h *AuthSignupHandlers) ResendCode(c *gin.Context) {
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
		// For now, we'll need to implement GetByPhone or use a different approach
		h.logger.Warn("Phone verification not yet implemented", zap.String("identifier", identifier))
		c.JSON(http.StatusNotImplemented, entities.ErrorResponse{
			Code:    "NOT_IMPLEMENTED",
			Message: "Phone verification not yet implemented",
		})
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
// @Param request body LoginRequest true "Login credentials"
// @Success 200 {object} AuthResponse
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Router /api/v1/auth/login [post]
func Login(db *sql.DB, cfg *config.Config, log *logger.Logger, emailService *adapters.EmailService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()

		// Parse request
		var req entities.LoginRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			log.Warnw("Invalid login request", "error", err)
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

		// Create user repository
		userRepo := repositories.NewUserRepository(db, log.Zap())

		// Get user by email
		user, err := userRepo.GetUserByEmailForLogin(ctx, req.Email)
		if err != nil {
			log.Warnw("Login attempt failed - user not found", "email", req.Email, "error", err)
			c.JSON(http.StatusUnauthorized, entities.ErrorResponse{
				Code:    "INVALID_CREDENTIALS",
				Message: "Invalid email or password",
			})
			return
		}

		// Validate password
		if !userRepo.ValidatePassword(req.Password, user.PasswordHash) {
			log.Warnw("Login attempt failed - invalid password", "email", req.Email)
			c.JSON(http.StatusUnauthorized, entities.ErrorResponse{
				Code:    "INVALID_CREDENTIALS",
				Message: "Invalid email or password",
			})
			return
		}

		// Check if user is active
		if !user.IsActive {
			log.Warnw("Login attempt failed - user account inactive", "email", req.Email)
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
			string(user.Role),
			cfg.JWT.Secret,
			cfg.JWT.AccessTTL,
			cfg.JWT.RefreshTTL,
		)
		if err != nil {
			log.Errorw("Failed to generate tokens", "error", err)
			c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
				Code:    "TOKEN_GENERATION_FAILED",
				Message: "Failed to generate authentication tokens",
			})
			return
		}

		// Update last login timestamp
		if err := userRepo.UpdateLastLogin(ctx, user.ID); err != nil {
			log.Warnw("Failed to update last login", "error", err, "user_id", user.ID.String())
			// Don't fail login for this
		}

		if emailService != nil && user.Email != "" {
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

			if err := emailService.SendLoginAlertEmail(ctx, user.Email, alertDetails); err != nil {
				log.Warnw("Failed to send login alert email", "error", err, "user_id", user.ID.String())
			}
		}

		// Return success response
		response := entities.AuthResponse{
			User:         user.ToUserInfo(),
			AccessToken:  tokens.AccessToken,
			RefreshToken: tokens.RefreshToken,
			ExpiresAt:    tokens.ExpiresAt,
		}

		log.Infow("User logged in successfully", "user_id", user.ID.String(), "email", user.Email)
		c.JSON(http.StatusOK, response)
	}
}

// RefreshToken handles JWT token refresh
func RefreshToken(db *sql.DB, cfg *config.Config, log *logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req entities.RefreshTokenRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_REQUEST", Message: "Invalid request payload"})
			return
		}

		// Validate refresh token format before processing
		refreshToken := strings.TrimSpace(req.RefreshToken)
		if refreshToken == "" {
			log.Warnw("Empty refresh token provided")
			c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "INVALID_TOKEN", Message: "Invalid refresh token"})
			return
		}

		// Basic JWT format validation (should have 3 segments separated by dots)
		segments := strings.Split(refreshToken, ".")
		if len(segments) != 3 {
			log.Warnw("Malformed token", "segments_count", len(segments))
			c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "INVALID_TOKEN", Message: "Invalid refresh token format"})
			return
		}

		// Refresh access token using pkg/auth
		pair, err := auth.RefreshAccessToken(refreshToken, cfg.JWT.Secret, cfg.JWT.AccessTTL)
		if err != nil {
			log.Warnw("Failed to refresh token", "error", err)
			c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "INVALID_TOKEN", Message: "Invalid refresh token"})
			return
		}
		c.JSON(http.StatusOK, pair)
	}
}

// Logout handles user logout
func Logout(db *sql.DB, cfg *config.Config, log *logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// For now, client can simply drop tokens. Optionally implement session invalidation.
		c.JSON(http.StatusOK, gin.H{"message": "Logged out"})
	}
}

// ForgotPassword handles password reset requests
func ForgotPassword(db *sql.DB, cfg *config.Config, log *logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req entities.ForgotPasswordRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_REQUEST", Message: "Invalid request payload"})
			return
		}
		// Check user exists
		userRepo := repositories.NewUserRepository(db, log.Zap())
		ctx := c.Request.Context()
		user, err := userRepo.GetByEmail(ctx, req.Email)
		if err != nil {
			// Do not reveal whether user exists
			c.JSON(http.StatusOK, gin.H{"message": "If an account exists, password reset instructions will be sent"})
			return
		}
		// Generate a reset token (use crypto.GenerateSecureToken)
		token, _ := crypto.GenerateSecureToken()
		// In a real app we'd store a hashed token and expiry in sessions or password_resets table
		// Send email via adapter if configured
		emailer, err := adapters.NewEmailService(log.Zap(), adapters.EmailServiceConfig{
			Provider:    cfg.Email.Provider,
			APIKey:      cfg.Email.APIKey,
			FromEmail:   cfg.Email.FromEmail,
			FromName:    cfg.Email.FromName,
			Environment: cfg.Email.Environment,
			BaseURL:     cfg.Email.BaseURL,
			ReplyTo:     cfg.Email.ReplyTo,
		})
		if err != nil {
			log.Errorw("Failed to initialize email service", "error", err)
			c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "EMAIL_SERVICE_ERROR", Message: "Unable to send password reset email"})
			return
		}
		if err := emailer.SendVerificationEmail(ctx, user.Email, token); err != nil {
			log.Errorw("Failed to send password reset email", "error", err)
			c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "EMAIL_SEND_FAILED", Message: "Unable to send password reset email"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "If an account exists, password reset instructions will be sent"})
	}
}

// ResetPassword handles password reset
func ResetPassword(db *sql.DB, cfg *config.Config, log *logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req entities.ResetPasswordRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_REQUEST", Message: "Invalid request payload"})
			return
		}
		// In this simplified implementation we don't track tokens server-side.
		// Expect a user_id query param for test flows or skip validation in dev.
		userIDStr := c.Query("user_id")
		if userIDStr == "" {
			c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "MISSING_USER_ID", Message: "user_id query param required in this test implementation"})
			return
		}
		userID, err := uuid.Parse(userIDStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_USER_ID", Message: "Invalid user_id"})
			return
		}
		// Hash new password and update
		newHash, err := crypto.HashPassword(req.Password)
		if err != nil {
			log.Errorw("Failed to hash new password", "error", err)
			c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "HASH_FAILED", Message: "Failed to hash password"})
			return
		}
		userRepo := repositories.NewUserRepository(db, log.Zap())
		if err := userRepo.UpdatePassword(c.Request.Context(), userID, newHash); err != nil {
			log.Errorw("Failed to update password", "error", err)
			c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "UPDATE_FAILED", Message: "Failed to update password"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "Password has been reset"})
	}
}

// VerifyEmail handles email verification
func VerifyEmail(db *sql.DB, cfg *config.Config, log *logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Simplified: expect user_id query param to verify
		userIDStr := c.Query("user_id")
		if userIDStr == "" {
			c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "MISSING_USER_ID", Message: "user_id query param required"})
			return
		}
		userID, err := uuid.Parse(userIDStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_USER_ID", Message: "Invalid user_id"})
			return
		}
		userRepo := repositories.NewUserRepository(db, log.Zap())
		ctx := c.Request.Context()
		user, err := userRepo.GetUserEntityByID(ctx, userID)
		if err != nil {
			c.JSON(http.StatusNotFound, entities.ErrorResponse{Code: "USER_NOT_FOUND", Message: "User not found"})
			return
		}
		// Mark email verified
		user.EmailVerified = true
		if err := userRepo.Update(ctx, user.ToUserProfile()); err != nil {
			c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "VERIFY_FAILED", Message: "Failed to verify email"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "Email verified"})
	}
}

// User Management Handlers
func GetProfile(db *sql.DB, cfg *config.Config, log *logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
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
		userRepo := repositories.NewUserRepository(db, log.Zap())
		user, err := userRepo.GetUserEntityByID(ctx, userID)
		if err != nil {
			c.JSON(http.StatusNotFound, entities.ErrorResponse{Code: "USER_NOT_FOUND", Message: "User not found"})
			return
		}
		c.JSON(http.StatusOK, user.ToUserInfo())
	}
}

func UpdateProfile(db *sql.DB, cfg *config.Config, log *logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
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
		userRepo := repositories.NewUserRepository(db, log.Zap())
		user, err := userRepo.GetByID(ctx, userID)
		if err != nil {
			c.JSON(http.StatusNotFound, entities.ErrorResponse{Code: "USER_NOT_FOUND", Message: "User not found"})
			return
		}
		// Apply updatable fields
		if payload.Phone != nil {
			user.Phone = payload.Phone
		}
		if payload.FirstName != nil {
			// not stored in DB yet, ignore
		}
		if payload.LastName != nil {
			// not stored in DB yet, ignore
		}
		if err := userRepo.Update(ctx, user); err != nil {
			c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "UPDATE_FAILED", Message: "Failed to update profile"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "Profile updated"})
	}
}

func ChangePassword(db *sql.DB, cfg *config.Config, log *logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
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
			c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_REQUEST", Message: "Invalid request payload"})
			return
		}
		userRepo := repositories.NewUserRepository(db, log.Zap())
		user, err := userRepo.GetUserEntityByID(ctx, userID)
		if err != nil {
			c.JSON(http.StatusNotFound, entities.ErrorResponse{Code: "USER_NOT_FOUND", Message: "User not found"})
			return
		}
		// Validate current password
		if !userRepo.ValidatePassword(req.CurrentPassword, user.PasswordHash) {
			c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "INVALID_CREDENTIALS", Message: "Current password is incorrect"})
			return
		}
		newHash, err := crypto.HashPassword(req.NewPassword)
		if err != nil {
			c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "HASH_FAILED", Message: "Failed to hash new password"})
			return
		}
		if err := userRepo.UpdatePassword(ctx, userID, newHash); err != nil {
			c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "UPDATE_FAILED", Message: "Failed to update password"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "Password changed"})
	}
}

func DeleteAccount(db *sql.DB, cfg *config.Config, log *logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
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
		userRepo := repositories.NewUserRepository(db, log.Zap())
		if err := userRepo.DeactivateUser(ctx, userID); err != nil {
			c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "DELETE_FAILED", Message: "Failed to delete account"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "Account deactivated"})
	}
}

func Enable2FA(db *sql.DB, cfg *config.Config, log *logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Placeholder - set a flag in user's profile or 2FA table in DB in a full impl
		c.JSON(http.StatusOK, gin.H{"message": "2FA enabled (stub)"})
	}
}

func Disable2FA(db *sql.DB, cfg *config.Config, log *logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Placeholder - clear 2FA flag in DB in a full impl
		c.JSON(http.StatusOK, gin.H{"message": "2FA disabled (stub)"})
	}
}
