package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/socialauth"
	"github.com/rail-service/rail_service/internal/domain/services/webauthn"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/rail-service/rail_service/pkg/auth"
	"github.com/rail-service/rail_service/pkg/crypto"
)

type SocialAuthHandlers struct {
	socialAuthService *socialauth.Service
	webauthnService   *webauthn.Service
	userRepo          repositories.UserRepository
	cfg               *config.Config
	logger            *zap.Logger
}

func NewSocialAuthHandlers(
	socialAuthService *socialauth.Service,
	webauthnService *webauthn.Service,
	userRepo repositories.UserRepository,
	cfg *config.Config,
	logger *zap.Logger,
) *SocialAuthHandlers {
	return &SocialAuthHandlers{
		socialAuthService: socialAuthService,
		webauthnService:   webauthnService,
		userRepo:          userRepo,
		cfg:               cfg,
		logger:            logger,
	}
}

// GetSocialAuthURL returns OAuth authorization URL
func (h *SocialAuthHandlers) GetSocialAuthURL(c *gin.Context) {
	var req entities.SocialAuthURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_REQUEST", Message: err.Error()})
		return
	}

	if h.socialAuthService == nil {
		c.JSON(http.StatusServiceUnavailable, entities.ErrorResponse{Code: "SOCIAL_AUTH_UNAVAILABLE", Message: "Social authentication not configured"})
		return
	}

	// Generate state for CSRF protection
	state, _ := crypto.GenerateRandomString(32)

	url, err := h.socialAuthService.GetAuthURL(req.Provider, req.RedirectURI, state)
	if err != nil {
		h.logger.Error("Failed to generate auth URL", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "AUTH_URL_ERROR", Message: err.Error()})
		return
	}

	c.JSON(http.StatusOK, entities.SocialAuthURLResponse{URL: url, State: state})
}

// SocialLogin handles OAuth callback and login/registration
func (h *SocialAuthHandlers) SocialLogin(c *gin.Context) {
	ctx := c.Request.Context()

	var req entities.SocialLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_REQUEST", Message: err.Error()})
		return
	}

	if h.socialAuthService == nil {
		c.JSON(http.StatusServiceUnavailable, entities.ErrorResponse{Code: "SOCIAL_AUTH_UNAVAILABLE", Message: "Social authentication not configured"})
		return
	}

	// Authenticate with provider
	socialInfo, err := h.socialAuthService.Authenticate(ctx, &req)
	if err != nil {
		h.logger.Error("Social authentication failed", zap.Error(err), zap.String("provider", string(req.Provider)))
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "AUTH_FAILED", Message: "Authentication failed"})
		return
	}

	// Check if user exists with this social account
	userID, err := h.socialAuthService.FindUserByProvider(ctx, req.Provider, socialInfo.ProviderID)
	if err != nil {
		h.logger.Error("Failed to find user", zap.Error(err))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "INTERNAL_ERROR", Message: "Internal error"})
		return
	}

	isNewUser := false
	var user *entities.User

	if userID == uuid.Nil {
		// Check if user exists with this email
		existingUser, err := h.userRepo.GetByEmail(ctx, socialInfo.Email)
		if err == nil && existingUser != nil {
			// Link social account to existing user
			userID = existingUser.ID
			if err := h.socialAuthService.LinkAccount(ctx, userID, socialInfo); err != nil {
				h.logger.Error("Failed to link social account", zap.Error(err))
			}
			user, _ = h.userRepo.GetUserEntityByID(ctx, userID)
		} else {
			// Create new user
			isNewUser = true
			newUser, err := h.userRepo.CreateUserFromAuth(ctx, &entities.RegisterRequest{
				Email:    socialInfo.Email,
				Password: uuid.New().String(), // Random password for social users
			})
			if err != nil {
				h.logger.Error("Failed to create user", zap.Error(err))
				c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "USER_CREATION_FAILED", Message: "Failed to create account"})
				return
			}

			// Mark email as verified (social providers verify email)
			newUser.EmailVerified = true
			newUser.OnboardingStatus = entities.OnboardingStatusWalletsPending
			h.userRepo.Update(ctx, newUser.ToUserProfile())

			userID = newUser.ID
			user = newUser

			// Link social account
			if err := h.socialAuthService.LinkAccount(ctx, userID, socialInfo); err != nil {
				h.logger.Warn("Failed to link social account", zap.Error(err))
			}
		}
	} else {
		user, _ = h.userRepo.GetUserEntityByID(ctx, userID)
	}

	if user == nil {
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "USER_NOT_FOUND", Message: "User not found"})
		return
	}

	// Generate tokens
	tokens, err := auth.GenerateTokenPair(user.ID, user.Email, user.Role, h.cfg.JWT.Secret, h.cfg.JWT.AccessTTL, h.cfg.JWT.RefreshTTL)
	if err != nil {
		h.logger.Error("Failed to generate tokens", zap.Error(err))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "TOKEN_ERROR", Message: "Failed to generate tokens"})
		return
	}

	h.logger.Info("Social login successful",
		zap.String("user_id", user.ID.String()),
		zap.String("provider", string(req.Provider)),
		zap.Bool("is_new_user", isNewUser))

	c.JSON(http.StatusOK, entities.SocialLoginResponse{
		User:         user.ToUserInfo(),
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		ExpiresAt:    tokens.ExpiresAt,
		IsNewUser:    isNewUser,
	})
}

// GetLinkedAccounts returns user's linked social accounts
func (h *SocialAuthHandlers) GetLinkedAccounts(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := getUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "UNAUTHORIZED", Message: "Not authenticated"})
		return
	}

	if h.socialAuthService == nil {
		c.JSON(http.StatusOK, entities.LinkedAccountsResponse{Accounts: []entities.LinkedAccount{}})
		return
	}

	accounts, err := h.socialAuthService.GetLinkedAccounts(ctx, userID)
	if err != nil {
		h.logger.Error("Failed to get linked accounts", zap.Error(err))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "INTERNAL_ERROR", Message: "Failed to get accounts"})
		return
	}

	c.JSON(http.StatusOK, entities.LinkedAccountsResponse{Accounts: accounts})
}

// LinkSocialAccount links a social account to current user
func (h *SocialAuthHandlers) LinkSocialAccount(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := getUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "UNAUTHORIZED", Message: "Not authenticated"})
		return
	}

	var req entities.SocialLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_REQUEST", Message: err.Error()})
		return
	}

	if h.socialAuthService == nil {
		c.JSON(http.StatusServiceUnavailable, entities.ErrorResponse{Code: "SOCIAL_AUTH_UNAVAILABLE", Message: "Social authentication not configured"})
		return
	}

	socialInfo, err := h.socialAuthService.Authenticate(ctx, &req)
	if err != nil {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "AUTH_FAILED", Message: "Authentication failed"})
		return
	}

	if err := h.socialAuthService.LinkAccount(ctx, userID, socialInfo); err != nil {
		h.logger.Error("Failed to link account", zap.Error(err))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "LINK_FAILED", Message: "Failed to link account"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Account linked successfully"})
}

// UnlinkSocialAccount removes a linked social account
func (h *SocialAuthHandlers) UnlinkSocialAccount(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := getUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "UNAUTHORIZED", Message: "Not authenticated"})
		return
	}

	provider := entities.SocialProvider(c.Param("provider"))
	if provider == "" {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_PROVIDER", Message: "Provider is required"})
		return
	}

	if h.socialAuthService == nil {
		c.JSON(http.StatusServiceUnavailable, entities.ErrorResponse{Code: "SOCIAL_AUTH_UNAVAILABLE", Message: "Social authentication not configured"})
		return
	}

	if err := h.socialAuthService.UnlinkAccount(ctx, userID, provider); err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "UNLINK_FAILED", Message: err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Account unlinked successfully"})
}

// WebAuthn Handlers

// BeginWebAuthnRegistration starts passkey registration
func (h *SocialAuthHandlers) BeginWebAuthnRegistration(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := getUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "UNAUTHORIZED", Message: "Not authenticated"})
		return
	}

	var req entities.WebAuthnRegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_REQUEST", Message: err.Error()})
		return
	}

	if h.webauthnService == nil {
		c.JSON(http.StatusServiceUnavailable, entities.ErrorResponse{Code: "WEBAUTHN_UNAVAILABLE", Message: "WebAuthn not configured"})
		return
	}

	user, err := h.userRepo.GetUserEntityByID(ctx, userID)
	if err != nil {
		c.JSON(http.StatusNotFound, entities.ErrorResponse{Code: "USER_NOT_FOUND", Message: "User not found"})
		return
	}

	displayName := user.Email
	if user.Phone != nil && *user.Phone != "" {
		displayName = *user.Phone
	}

	options, _, err := h.webauthnService.BeginRegistration(ctx, userID, user.Email, displayName)
	if err != nil {
		h.logger.Error("Failed to begin WebAuthn registration", zap.Error(err))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "REGISTRATION_ERROR", Message: err.Error()})
		return
	}

	// Note: In production, store session data in Redis/DB for FinishRegistration
	c.Set("webauthn_cred_name", req.Name)

	c.JSON(http.StatusOK, entities.WebAuthnRegisterResponse{Options: options})
}

// GetWebAuthnCredentials returns user's passkeys
func (h *SocialAuthHandlers) GetWebAuthnCredentials(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := getUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "UNAUTHORIZED", Message: "Not authenticated"})
		return
	}

	if h.webauthnService == nil {
		c.JSON(http.StatusOK, entities.WebAuthnCredentialsResponse{Credentials: []entities.WebAuthnCredentialInfo{}})
		return
	}

	creds, err := h.webauthnService.GetCredentials(ctx, userID)
	if err != nil {
		h.logger.Error("Failed to get credentials", zap.Error(err))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "INTERNAL_ERROR", Message: "Failed to get credentials"})
		return
	}

	c.JSON(http.StatusOK, entities.WebAuthnCredentialsResponse{Credentials: creds})
}

// DeleteWebAuthnCredential removes a passkey
func (h *SocialAuthHandlers) DeleteWebAuthnCredential(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := getUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "UNAUTHORIZED", Message: "Not authenticated"})
		return
	}

	credIDStr := c.Param("id")
	credID, err := uuid.Parse(credIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_ID", Message: "Invalid credential ID"})
		return
	}

	if h.webauthnService == nil {
		c.JSON(http.StatusServiceUnavailable, entities.ErrorResponse{Code: "WEBAUTHN_UNAVAILABLE", Message: "WebAuthn not configured"})
		return
	}

	if err := h.webauthnService.DeleteCredential(ctx, userID, credID); err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "DELETE_FAILED", Message: err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Credential deleted successfully"})
}

// BeginWebAuthnLogin starts passkey login
func (h *SocialAuthHandlers) BeginWebAuthnLogin(c *gin.Context) {
	ctx := c.Request.Context()

	var req entities.WebAuthnLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_REQUEST", Message: err.Error()})
		return
	}

	if h.webauthnService == nil {
		c.JSON(http.StatusServiceUnavailable, entities.ErrorResponse{Code: "WEBAUTHN_UNAVAILABLE", Message: "WebAuthn not configured"})
		return
	}

	// Get user by email
	user, err := h.userRepo.GetByEmail(ctx, req.Email)
	if err != nil {
		c.JSON(http.StatusNotFound, entities.ErrorResponse{Code: "USER_NOT_FOUND", Message: "User not found"})
		return
	}

	options, _, err := h.webauthnService.BeginLogin(ctx, user.ID, user.Email)
	if err != nil {
		h.logger.Error("Failed to begin WebAuthn login", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "LOGIN_ERROR", Message: err.Error()})
		return
	}

	// Note: In production, store session data in Redis/DB for FinishLogin
	c.JSON(http.StatusOK, entities.WebAuthnLoginResponse{Options: options})
}
