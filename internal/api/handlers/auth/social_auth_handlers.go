package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-webauthn/webauthn/protocol"
	webauthnlib "github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/entities"
	passcodesvc "github.com/rail-service/rail_service/internal/domain/services/passcode"
	"github.com/rail-service/rail_service/internal/domain/services/socialauth"
	webauthnsvc "github.com/rail-service/rail_service/internal/domain/services/webauthn"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/rail-service/rail_service/pkg/auth"
	"github.com/rail-service/rail_service/pkg/crypto"
)

const (
	webauthnSessionRegistrationPrefix = "webauthn:registration"
	webauthnSessionLoginPrefix        = "webauthn:login"
	defaultWebAuthnSessionTTL         = 5 * time.Minute
)

type webAuthnRegistrationSession struct {
	SessionData    webauthnlib.SessionData `json:"sessionData"`
	UserID         uuid.UUID               `json:"userId"`
	Email          string                  `json:"email"`
	DisplayName    string                  `json:"displayName"`
	CredentialName string                  `json:"credentialName"`
}

type webAuthnLoginSession struct {
	SessionData webauthnlib.SessionData `json:"sessionData"`
	UserID      uuid.UUID               `json:"userId"`
	Email       string                  `json:"email"`
}

func ensureWebAuthnResponseType(response json.RawMessage) json.RawMessage {
	if len(response) == 0 {
		return response
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(response, &payload); err != nil {
		return response
	}

	if typ, ok := payload["type"]; ok {
		if typedString, ok := typ.(string); ok && strings.TrimSpace(typedString) != "" {
			return response
		}
	}

	payload["type"] = "public-key"
	normalized, err := json.Marshal(payload)
	if err != nil {
		return response
	}

	return normalized
}

type SocialAuthHandlers struct {
	socialAuthService *socialauth.Service
	webauthnService   *webauthnsvc.Service
	sessionService    SessionService
	passcodeService   *passcodesvc.Service
	redisClient       cache.RedisClient
	userRepo          repositories.UserRepository
	cfg               *config.Config
	logger            *zap.Logger
}

func NewSocialAuthHandlers(
	socialAuthService *socialauth.Service,
	webauthnService *webauthnsvc.Service,
	sessionService SessionService,
	passcodeService *passcodesvc.Service,
	userRepo repositories.UserRepository,
	redisClient cache.RedisClient,
	cfg *config.Config,
	logger *zap.Logger,
) *SocialAuthHandlers {
	return &SocialAuthHandlers{
		socialAuthService: socialAuthService,
		webauthnService:   webauthnService,
		sessionService:    sessionService,
		passcodeService:   passcodeService,
		redisClient:       redisClient,
		userRepo:          userRepo,
		cfg:               cfg,
		logger:            logger,
	}
}

// enrichUserInfo sets HasPasscode on a UserInfo based on the user's passcode status.
func (h *SocialAuthHandlers) enrichUserInfo(ctx context.Context, info *entities.UserInfo, userID uuid.UUID) {
	if h.passcodeService == nil {
		return
	}
	status, err := h.passcodeService.GetStatus(ctx, userID)
	if err != nil {
		h.logger.Warn("Failed to get passcode status for user info enrichment", zap.Error(err))
		return
	}
	info.HasPasscode = status.Enabled
}

// GetSocialAuthURL returns OAuth authorization URL
func (h *SocialAuthHandlers) GetSocialAuthURL(c *gin.Context) {
	var req entities.SocialAuthURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_REQUEST", Message: "An unexpected error occurred. Please try again."})
		return
	}

	if h.socialAuthService == nil {
		c.JSON(http.StatusServiceUnavailable, entities.ErrorResponse{Code: "SOCIAL_AUTH_UNAVAILABLE", Message: "Social authentication not configured"})
		return
	}

	// Generate state for CSRF protection
	state, _ := crypto.GenerateRandomString(32)

	if h.redisClient != nil {
		h.redisClient.Set(c.Request.Context(), "oauth:state:"+state, "1", 10*time.Minute)
	}

	url, err := h.socialAuthService.GetAuthURL(req.Provider, req.RedirectURI, state)
	if err != nil {
		h.logger.Error("Failed to generate auth URL", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "AUTH_URL_ERROR", Message: "An unexpected error occurred. Please try again."})
		return
	}

	c.JSON(http.StatusOK, entities.SocialAuthURLResponse{URL: url, State: state})
}

// SocialLogin handles OAuth callback and login/registration
func (h *SocialAuthHandlers) SocialLogin(c *gin.Context) {
	ctx := c.Request.Context()

	var req entities.SocialLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_REQUEST", Message: "An unexpected error occurred. Please try again."})
		return
	}

	if h.socialAuthService == nil {
		c.JSON(http.StatusServiceUnavailable, entities.ErrorResponse{Code: "SOCIAL_AUTH_UNAVAILABLE", Message: "Social authentication not configured"})
		return
	}

	var socialInfo *socialauth.SocialUserInfo

	if req.IDToken != "" {
		// Mobile SDK flows may not use redirect state, but only skip state after the ID token is verified.
		var err error
		socialInfo, err = h.socialAuthService.Authenticate(ctx, &req)
		if err != nil {
			h.logger.Error("Social authentication failed", zap.Error(err), zap.String("provider", string(req.Provider)))
			// Distinguish config errors from auth failures so the client can show
			// a actionable message instead of a generic "Authentication failed".
			errMsg := strings.ToLower(err.Error())
			if strings.Contains(errMsg, "not configured") || strings.Contains(errMsg, "missing client id") {
				c.JSON(http.StatusServiceUnavailable, entities.ErrorResponse{
					Code:    "SOCIAL_AUTH_UNAVAILABLE",
					Message: "Social authentication is not configured on the server. Please contact support or use email sign in.",
				})
				return
			}
			c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "AUTH_FAILED", Message: "Authentication failed"})
			return
		}
	} else {
		// Validate OAuth state to prevent CSRF for web code-exchange flows.
		if req.State == "" {
			c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "MISSING_STATE", Message: "OAuth state parameter is required"})
			return
		}
		if h.redisClient != nil {
			stateKey := "oauth:state:" + req.State
			exists, _ := h.redisClient.Exists(ctx, stateKey)
			if !exists {
				c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_STATE", Message: "Invalid or expired OAuth state"})
				return
			}
			h.redisClient.Del(ctx, stateKey)
		}

		var err error
		socialInfo, err = h.socialAuthService.Authenticate(ctx, &req)
		if err != nil {
			h.logger.Error("Social authentication failed", zap.Error(err), zap.String("provider", string(req.Provider)))
			c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "AUTH_FAILED", Message: "Authentication failed"})
			return
		}
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
				c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
					Code:    "LINK_FAILED",
					Message: "Failed to link social account",
				})
				return
			}
			user, _ = h.userRepo.GetUserEntityByID(ctx, userID)
		} else {
			// Create new user
			isNewUser = true

			if strings.TrimSpace(socialInfo.Email) == "" {
				h.logger.Warn("Social provider did not return email for new account",
					zap.String("provider", string(req.Provider)))
				c.JSON(http.StatusBadRequest, entities.ErrorResponse{
					Code:    "EMAIL_REQUIRED",
					Message: "Email from social provider is required to create account",
				})
				return
			}

			password := uuid.New().String() // Keep existing behavior for non-Apple providers
			if req.Provider == entities.SocialProviderApple {
				password = "" // Apple signup: password is optional at account creation
			}

			newUser, err := h.userRepo.CreateUserFromAuth(ctx, &entities.RegisterRequest{
				Email:    socialInfo.Email,
				Password: password,
			})
			if err != nil {
				h.logger.Error("Failed to create user", zap.Error(err))
				c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "USER_CREATION_FAILED", Message: "Failed to create account"})
				return
			}

			// Mark email as verified (social providers verify email)
			newUser.EmailVerified = true
			// Keep onboarding_status as 'started' so user goes through
			// profile completion → CompleteOnboarding → Bridge/wallet provisioning.
			// Persist name from social provider if available (Apple only sends on first sign-in).
			if givenName := strings.TrimSpace(req.GivenName); givenName != "" {
				newUser.FirstName = &givenName
			}
			if familyName := strings.TrimSpace(req.FamilyName); familyName != "" {
				newUser.LastName = &familyName
			}
			if err := h.userRepo.Update(ctx, newUser.ToUserProfile()); err != nil {
				h.logger.Error("Failed to update social user profile after creation", zap.Error(err), zap.String("user_id", newUser.ID.String()))
				c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "USER_UPDATE_FAILED", Message: "Failed to finalize account setup"})
				return
			}

			userID = newUser.ID
			user = newUser

			// Link social account
			if err := h.socialAuthService.LinkAccount(ctx, userID, socialInfo); err != nil {
				h.logger.Error("Failed to link social account", zap.Error(err), zap.String("user_id", userID.String()))
				c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
					Code:    "LINK_FAILED",
					Message: "Failed to link social account",
				})
				return
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
	if h.sessionService != nil {
		ipAddress, userAgent, fingerprint, location := extractSessionDetails(c)
		sessionExpiresAt := h.sessionExpiryFromRefreshTTL()
		if _, err := h.sessionService.CreateSession(ctx, user.ID, tokens.AccessToken, tokens.RefreshToken, ipAddress, userAgent, fingerprint, location, sessionExpiresAt); err != nil {
			h.logger.Warn("Failed to create session after social login", zap.Error(err), zap.String("user_id", user.ID.String()))
		}
	}

	h.logger.Info("Social login successful",
		zap.String("user_id", user.ID.String()),
		zap.String("provider", string(req.Provider)),
		zap.Bool("is_new_user", isNewUser))

	socialResp := entities.SocialLoginResponse{
		User:             user.ToUserInfo(),
		AccessToken:      tokens.AccessToken,
		RefreshToken:     tokens.RefreshToken,
		ExpiresAt:        tokens.ExpiresAt,
		SessionExpiresAt: h.sessionExpiryFromRefreshTTL(),
		IsNewUser:        isNewUser,
	}
	h.enrichUserInfo(ctx, socialResp.User, user.ID)
	c.JSON(http.StatusOK, socialResp)
}

// GetLinkedAccounts returns user's linked social accounts
func (h *SocialAuthHandlers) GetLinkedAccounts(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := common.GetUserIDFromContext(c)
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

	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "UNAUTHORIZED", Message: "Not authenticated"})
		return
	}

	var req entities.SocialLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_REQUEST", Message: "An unexpected error occurred. Please try again."})
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

	userID, err := common.GetUserIDFromContext(c)
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
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "UNLINK_FAILED", Message: "An unexpected error occurred. Please try again."})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Account unlinked successfully"})
}

// WebAuthn Handlers

// BeginWebAuthnRegistration starts passkey registration
func (h *SocialAuthHandlers) BeginWebAuthnRegistration(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "UNAUTHORIZED", Message: "Not authenticated"})
		return
	}

	var req entities.WebAuthnRegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_REQUEST", Message: "An unexpected error occurred. Please try again."})
		return
	}

	if h.webauthnService == nil {
		c.JSON(http.StatusServiceUnavailable, entities.ErrorResponse{Code: "WEBAUTHN_UNAVAILABLE", Message: "WebAuthn not configured"})
		return
	}
	if h.redisClient == nil {
		c.JSON(http.StatusServiceUnavailable, entities.ErrorResponse{Code: "WEBAUTHN_SESSION_UNAVAILABLE", Message: "WebAuthn session store unavailable"})
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

	options, sessionData, err := h.webauthnService.BeginRegistration(ctx, userID, user.Email, displayName)
	if err != nil {
		h.logger.Error("Failed to begin WebAuthn registration", zap.Error(err))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "REGISTRATION_ERROR", Message: "An unexpected error occurred. Please try again."})
		return
	}
	h.logger.Info("WebAuthn registration begin options",
		zap.String("user_id", userID.String()),
		zap.String("rp_id", options.Response.RelyingParty.ID),
		zap.String("resident_key", string(options.Response.AuthenticatorSelection.ResidentKey)),
		zap.String("user_verification", string(options.Response.AuthenticatorSelection.UserVerification)),
	)

	sessionID, err := crypto.GenerateRandomString(32)
	if err != nil {
		h.logger.Error("Failed to generate WebAuthn registration session ID", zap.Error(err))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "SESSION_ERROR", Message: "Failed to create registration session"})
		return
	}
	if err := h.storeWebAuthnRegistrationSession(ctx, sessionID, &webAuthnRegistrationSession{
		SessionData:    *sessionData,
		UserID:         userID,
		Email:          user.Email,
		DisplayName:    displayName,
		CredentialName: strings.TrimSpace(req.Name),
	}); err != nil {
		h.logger.Error("Failed to persist WebAuthn registration session", zap.Error(err))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "SESSION_ERROR", Message: "Failed to create registration session"})
		return
	}

	c.JSON(http.StatusOK, entities.WebAuthnRegisterResponse{Options: options, SessionID: sessionID})
}

// GetWebAuthnCredentials returns user's passkeys
func (h *SocialAuthHandlers) GetWebAuthnCredentials(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := common.GetUserIDFromContext(c)
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

	userID, err := common.GetUserIDFromContext(c)
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
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "DELETE_FAILED", Message: "An unexpected error occurred. Please try again."})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Credential deleted successfully"})
}

// BeginWebAuthnLogin starts passkey login
func (h *SocialAuthHandlers) BeginWebAuthnLogin(c *gin.Context) {
	ctx := c.Request.Context()

	var req entities.WebAuthnLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_REQUEST", Message: "An unexpected error occurred. Please try again."})
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if req.Email == "" {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_REQUEST", Message: "email is required"})
		return
	}

	if h.webauthnService == nil {
		c.JSON(http.StatusServiceUnavailable, entities.ErrorResponse{Code: "WEBAUTHN_UNAVAILABLE", Message: "WebAuthn not configured"})
		return
	}
	if h.redisClient == nil {
		c.JSON(http.StatusServiceUnavailable, entities.ErrorResponse{Code: "WEBAUTHN_SESSION_UNAVAILABLE", Message: "WebAuthn session store unavailable"})
		return
	}

	// Get user by email
	user, err := h.userRepo.GetByEmail(ctx, req.Email)
	if err != nil {
		c.JSON(http.StatusNotFound, entities.ErrorResponse{Code: "USER_NOT_FOUND", Message: "User not found"})
		return
	}

	options, sessionData, err := h.webauthnService.BeginLogin(ctx, user.ID, user.Email)
	if err != nil {
		h.logger.Error("Failed to begin WebAuthn login", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "LOGIN_ERROR", Message: "An unexpected error occurred. Please try again."})
		return
	}
	h.logger.Info("WebAuthn login begin session",
		zap.String("user_id", user.ID.String()),
		zap.String("primary_rp_id", h.webauthnService.PrimaryRPID()),
		zap.Strings("supported_rp_ids", h.webauthnService.SupportedRPIDs()),
		zap.Time("session_expires", sessionData.Expires),
		zap.Int("allowed_credentials", len(sessionData.AllowedCredentialIDs)),
		zap.String("user_verification", string(sessionData.UserVerification)))

	sessionID, err := crypto.GenerateRandomString(32)
	if err != nil {
		h.logger.Error("Failed to generate WebAuthn login session ID", zap.Error(err))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "SESSION_ERROR", Message: "Failed to create login session"})
		return
	}
	if err := h.storeWebAuthnLoginSession(ctx, sessionID, &webAuthnLoginSession{
		SessionData: *sessionData,
		UserID:      user.ID,
		Email:       user.Email,
	}); err != nil {
		h.logger.Error("Failed to persist WebAuthn login session", zap.Error(err))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "SESSION_ERROR", Message: "Failed to create login session"})
		return
	}

	c.JSON(http.StatusOK, entities.WebAuthnLoginResponse{Options: options, SessionID: sessionID})
}

// FinishWebAuthnRegistration completes passkey registration.
func (h *SocialAuthHandlers) FinishWebAuthnRegistration(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "UNAUTHORIZED", Message: "Not authenticated"})
		return
	}

	var req entities.WebAuthnRegisterFinishRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_REQUEST", Message: "An unexpected error occurred. Please try again."})
		return
	}
	if strings.TrimSpace(req.SessionID) == "" || len(req.Response) == 0 {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_REQUEST", Message: "sessionId and response are required"})
		return
	}

	if h.webauthnService == nil {
		c.JSON(http.StatusServiceUnavailable, entities.ErrorResponse{Code: "WEBAUTHN_UNAVAILABLE", Message: "WebAuthn not configured"})
		return
	}
	if h.redisClient == nil {
		c.JSON(http.StatusServiceUnavailable, entities.ErrorResponse{Code: "WEBAUTHN_SESSION_UNAVAILABLE", Message: "WebAuthn session store unavailable"})
		return
	}

	session, err := h.getWebAuthnRegistrationSession(ctx, req.SessionID)
	if err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_SESSION", Message: "Registration session is invalid or expired"})
		return
	}
	if session.UserID != userID {
		c.JSON(http.StatusForbidden, entities.ErrorResponse{Code: "SESSION_USER_MISMATCH", Message: "Registration session does not belong to this user"})
		return
	}

	normalizedResponse := ensureWebAuthnResponseType(req.Response)
	parsedResponse, err := protocol.ParseCredentialCreationResponseBytes(normalizedResponse)
	if err != nil {
		h.logger.Warn("Failed to parse WebAuthn registration response", zap.Error(err), zap.String("user_id", userID.String()))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_WEBAUTHN_RESPONSE", Message: "Failed to parse registration response"})
		return
	}

	credentialName := strings.TrimSpace(req.Name)
	if credentialName == "" {
		credentialName = strings.TrimSpace(session.CredentialName)
	}
	if credentialName == "" {
		credentialName = "Passkey"
	}

	if err := h.webauthnService.FinishRegistration(
		ctx,
		userID,
		session.Email,
		session.DisplayName,
		credentialName,
		&session.SessionData,
		parsedResponse,
	); err != nil {
		h.logger.Error("Failed to finish WebAuthn registration", zap.Error(err), zap.String("user_id", userID.String()))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "REGISTRATION_FAILED", Message: "Passkey registration failed"})
		return
	}

	h.deleteWebAuthnSession(ctx, webauthnSessionRegistrationPrefix, req.SessionID)

	c.JSON(http.StatusCreated, gin.H{
		"message": "Passkey registered successfully",
		"name":    credentialName,
	})
}

// FinishWebAuthnLogin completes passkey authentication and returns JWT tokens.
func (h *SocialAuthHandlers) FinishWebAuthnLogin(c *gin.Context) {
	ctx := c.Request.Context()

	var req entities.WebAuthnLoginFinishRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_REQUEST", Message: "An unexpected error occurred. Please try again."})
		return
	}
	if strings.TrimSpace(req.SessionID) == "" || len(req.Response) == 0 {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_REQUEST", Message: "sessionId and response are required"})
		return
	}

	if h.webauthnService == nil {
		c.JSON(http.StatusServiceUnavailable, entities.ErrorResponse{Code: "WEBAUTHN_UNAVAILABLE", Message: "WebAuthn not configured"})
		return
	}
	if h.redisClient == nil {
		c.JSON(http.StatusServiceUnavailable, entities.ErrorResponse{Code: "WEBAUTHN_SESSION_UNAVAILABLE", Message: "WebAuthn session store unavailable"})
		return
	}

	session, err := h.getWebAuthnLoginSession(ctx, req.SessionID)
	if err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_SESSION", Message: "Login session is invalid or expired"})
		return
	}

	normalizedResponse := ensureWebAuthnResponseType(req.Response)
	parsedResponse, err := protocol.ParseCredentialRequestResponseBytes(normalizedResponse)
	if err != nil {
		h.logger.Warn("Failed to parse WebAuthn login response", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_WEBAUTHN_RESPONSE", Message: "Failed to parse login response"})
		return
	}
	h.logger.Info("WebAuthn login finish attempt",
		zap.String("user_id", session.UserID.String()),
		zap.String("session_id", req.SessionID),
		zap.Int("allowed_credentials", len(session.SessionData.AllowedCredentialIDs)),
		zap.Int("credential_id_len", len(parsedResponse.RawID)))

	if err := h.webauthnService.FinishLogin(ctx, session.UserID, session.Email, &session.SessionData, parsedResponse); err != nil {
		h.logger.Warn("Failed to finish WebAuthn login",
			zap.Error(err),
			zap.String("user_id", session.UserID.String()),
			zap.String("session_id", req.SessionID),
			zap.Int("allowed_credentials", len(session.SessionData.AllowedCredentialIDs)))
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "LOGIN_FAILED", Message: "Passkey authentication failed"})
		return
	}

	user, err := h.userRepo.GetUserEntityByID(ctx, session.UserID)
	if err != nil || user == nil {
		c.JSON(http.StatusNotFound, entities.ErrorResponse{Code: "USER_NOT_FOUND", Message: "User not found"})
		return
	}
	if !user.IsActive {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "ACCOUNT_INACTIVE", Message: "Account is inactive"})
		return
	}

	tokens, err := auth.GenerateTokenPair(user.ID, user.Email, user.Role, h.cfg.JWT.Secret, h.cfg.JWT.AccessTTL, h.cfg.JWT.RefreshTTL)
	if err != nil {
		h.logger.Error("Failed to generate tokens after WebAuthn login", zap.Error(err), zap.String("user_id", user.ID.String()))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "TOKEN_ERROR", Message: "Failed to generate tokens"})
		return
	}
	if h.sessionService != nil {
		ipAddress, userAgent, fingerprint, location := extractSessionDetails(c)
		sessionExpiresAt := h.sessionExpiryFromRefreshTTL()
		if _, err := h.sessionService.CreateSession(ctx, user.ID, tokens.AccessToken, tokens.RefreshToken, ipAddress, userAgent, fingerprint, location, sessionExpiresAt); err != nil {
			h.logger.Warn("Failed to create session after WebAuthn login", zap.Error(err), zap.String("user_id", user.ID.String()))
		}
	}

	h.deleteWebAuthnSession(ctx, webauthnSessionLoginPrefix, req.SessionID)

	var passcodeSessionToken string
	var passcodeSessionExpiresAt *time.Time
	if h.passcodeService != nil {
		token, expiresAt, passcodeErr := h.passcodeService.IssueSession(ctx, user.ID)
		if passcodeErr != nil {
			h.logger.Warn("Failed to issue passcode session after WebAuthn login",
				zap.Error(passcodeErr),
				zap.String("user_id", user.ID.String()))
		} else {
			passcodeSessionToken = token
			expiresCopy := expiresAt
			passcodeSessionExpiresAt = &expiresCopy
		}
	}

	webauthnResp := entities.AuthResponse{
		User:                     user.ToUserInfo(),
		AccessToken:              tokens.AccessToken,
		RefreshToken:             tokens.RefreshToken,
		ExpiresAt:                tokens.ExpiresAt,
		SessionExpiresAt:         h.sessionExpiryFromRefreshTTL(),
		PasscodeSessionToken:     passcodeSessionToken,
		PasscodeSessionExpiresAt: passcodeSessionExpiresAt,
	}
	h.enrichUserInfo(ctx, webauthnResp.User, user.ID)
	c.JSON(http.StatusOK, webauthnResp)
}

func (h *SocialAuthHandlers) storeWebAuthnRegistrationSession(ctx context.Context, sessionID string, session *webAuthnRegistrationSession) error {
	return h.storeWebAuthnSession(ctx, webauthnSessionRegistrationPrefix, sessionID, session, session.SessionData.Expires)
}

func (h *SocialAuthHandlers) storeWebAuthnLoginSession(ctx context.Context, sessionID string, session *webAuthnLoginSession) error {
	return h.storeWebAuthnSession(ctx, webauthnSessionLoginPrefix, sessionID, session, session.SessionData.Expires)
}

func (h *SocialAuthHandlers) storeWebAuthnSession(ctx context.Context, prefix, sessionID string, payload interface{}, expiresAt time.Time) error {
	if h.redisClient == nil {
		return fmt.Errorf("redis client not configured")
	}

	ttl := time.Until(expiresAt)
	if ttl <= 0 {
		ttl = defaultWebAuthnSessionTTL
	}

	return h.redisClient.Set(ctx, h.webAuthnSessionKey(prefix, sessionID), payload, ttl)
}

func (h *SocialAuthHandlers) getWebAuthnRegistrationSession(ctx context.Context, sessionID string) (*webAuthnRegistrationSession, error) {
	session := &webAuthnRegistrationSession{}
	if err := h.redisClient.Get(ctx, h.webAuthnSessionKey(webauthnSessionRegistrationPrefix, sessionID), session); err != nil {
		return nil, err
	}
	return session, nil
}

func (h *SocialAuthHandlers) getWebAuthnLoginSession(ctx context.Context, sessionID string) (*webAuthnLoginSession, error) {
	session := &webAuthnLoginSession{}
	if err := h.redisClient.Get(ctx, h.webAuthnSessionKey(webauthnSessionLoginPrefix, sessionID), session); err != nil {
		return nil, err
	}
	return session, nil
}

func (h *SocialAuthHandlers) deleteWebAuthnSession(ctx context.Context, prefix, sessionID string) {
	if h.redisClient == nil {
		return
	}
	if err := h.redisClient.Del(ctx, h.webAuthnSessionKey(prefix, sessionID)); err != nil {
		h.logger.Warn("Failed to delete WebAuthn session",
			zap.Error(err),
			zap.String("prefix", prefix))
	}
}

func (h *SocialAuthHandlers) webAuthnSessionKey(prefix, sessionID string) string {
	return fmt.Sprintf("%s:%s", prefix, sessionID)
}

func (h *SocialAuthHandlers) sessionExpiryFromRefreshTTL() time.Time {
	ttl := time.Duration(h.cfg.JWT.RefreshTTL) * time.Second
	if ttl <= 0 {
		ttl = time.Duration(h.cfg.JWT.AccessTTL) * time.Second
	}
	return time.Now().Add(ttl)
}
