package routes

import (
	"context"
	"database/sql"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/api/handlers"
	"github.com/rail-service/rail_service/internal/api/middleware"
	"github.com/rail-service/rail_service/internal/domain/services/apikey"
	"github.com/rail-service/rail_service/internal/domain/services/security"
	"github.com/rail-service/rail_service/internal/domain/services/session"
	"github.com/rail-service/rail_service/internal/domain/services/twofa"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/pkg/auth"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/rail-service/rail_service/pkg/ratelimit"
)

type APIKeyValidatorAdapter struct {
	svc *apikey.Service
}

func NewAPIKeyValidatorAdapter(svc *apikey.Service) *APIKeyValidatorAdapter {
	return &APIKeyValidatorAdapter{svc: svc}
}

func (a *APIKeyValidatorAdapter) ValidateAPIKey(ctx context.Context, key string) (*middleware.APIKeyInfo, error) {
	keyInfo, err := a.svc.ValidateAPIKey(ctx, key)
	if err != nil {
		return nil, err
	}
	return &middleware.APIKeyInfo{
		ID:     keyInfo.ID,
		UserID: keyInfo.UserID,
		Scopes: keyInfo.Scopes,
	}, nil
}

// SetupSecurityRoutesEnhanced sets up security routes with enhanced security features
func SetupSecurityRoutesEnhanced(
	router *gin.Engine,
	cfg *config.Config,
	db *sql.DB,
	zapLog *zap.Logger,
	tokenBlacklist *auth.TokenBlacklist,
	tieredLimiter *ratelimit.TieredLimiter,
	loginTracker *ratelimit.LoginAttemptTracker,
	ipWhitelistService *security.IPWhitelistService,
	deviceTrackingService *security.DeviceTrackingService,
	loginProtectionService *security.LoginProtectionService,
) {
	// Initialize services
	sessionService := session.NewService(db, nil, zapLog)
	twofaService := twofa.NewService(db, zapLog, cfg.Security.EncryptionKey)
	apikeyService := apikey.NewService(db, zapLog)

	// Create adapters
	sessionValidator := NewSessionValidatorAdapter(sessionService)
	apikeyValidator := NewAPIKeyValidatorAdapter(apikeyService)

	// Wrap zap logger to logger.Logger
	log := logger.NewLogger(zapLog)

	// Initialize handlers
	securityHandlers := handlers.NewEnhancedSecurityHandlers(
		sessionService,
		twofaService,
		apikeyService,
		zapLog,
	)

	// Initialize rate limiter
	userRateLimiter := middleware.NewUserRateLimiter(db, zapLog)

	// API v1 routes
	v1 := router.Group("/api/v1")
	{
		// Apply tiered rate limiting if available
		if tieredLimiter != nil {
			v1.Use(middleware.TieredRateLimiting(tieredLimiter, log))
		} else {
			v1.Use(middleware.RateLimit(100))
		}

		// Apply login protection middleware to auth routes
		authRoutes := v1.Group("/auth")
		if loginTracker != nil {
			authRoutes.Use(middleware.LoginRateLimiting(loginTracker, log))
		}
		if loginProtectionService != nil {
			authRoutes.Use(middleware.LoginProtection(loginProtectionService, zapLog))
		}

		// Authentication required routes with enhanced auth
		auth := v1.Group("")
		if tokenBlacklist != nil {
			auth.Use(middleware.EnhancedAuthentication(cfg, tokenBlacklist, log, sessionValidator))
		} else {
			auth.Use(middleware.Authentication(cfg, log, sessionValidator))
		}
		auth.Use(userRateLimiter.UserRateLimit(60))

		// Apply device tracking if available
		if deviceTrackingService != nil {
			auth.Use(middleware.DeviceVerification(deviceTrackingService, zapLog))
		}
		{
			// 2FA Management
			twofa := auth.Group("/2fa")
			{
				twofa.GET("/status", securityHandlers.Get2FAStatus)
				twofa.POST("/setup", securityHandlers.Setup2FA)
				twofa.POST("/enable", securityHandlers.Enable2FA)
				twofa.POST("/verify", securityHandlers.Verify2FA)
				twofa.POST("/disable", securityHandlers.Disable2FA)
				twofa.POST("/backup-codes/regenerate", securityHandlers.RegenerateBackupCodes)
			}

			// Session Management
			sessions := auth.Group("/sessions")
			{
				sessions.GET("", securityHandlers.GetSessions)
				sessions.DELETE("/current", securityHandlers.InvalidateSession)
				sessions.DELETE("/all", securityHandlers.InvalidateAllSessions)
			}

			// API Key Management
			apikeys := auth.Group("/api-keys")
			{
				apikeys.GET("", securityHandlers.ListAPIKeys)
				apikeys.POST("", securityHandlers.CreateAPIKey)
				apikeys.PUT("/:id", securityHandlers.UpdateAPIKey)
				apikeys.DELETE("/:id", securityHandlers.RevokeAPIKey)
			}
		}

		// Sensitive operations requiring IP whitelist
		sensitive := v1.Group("/sensitive")
		if tokenBlacklist != nil {
			sensitive.Use(middleware.EnhancedAuthentication(cfg, tokenBlacklist, log, sessionValidator))
		} else {
			sensitive.Use(middleware.Authentication(cfg, log, sessionValidator))
		}
		if ipWhitelistService != nil {
			sensitive.Use(middleware.RequireIPWhitelist(ipWhitelistService, zapLog))
		}

		// Admin routes
		admin := v1.Group("/admin")
		if tokenBlacklist != nil {
			admin.Use(middleware.EnhancedAuthentication(cfg, tokenBlacklist, log, sessionValidator))
		} else {
			admin.Use(middleware.Authentication(cfg, log, sessionValidator))
		}
		admin.Use(middleware.AdminAuth(db, log))
		admin.Use(userRateLimiter.UserRateLimit(120))
		{
			admin.GET("/api-keys", securityHandlers.AdminListAPIKeys)
			admin.DELETE("/api-keys/:id", securityHandlers.AdminRevokeAPIKey)
		}

		// API key authenticated routes
		api := v1.Group("/external")
		api.Use(middleware.ValidateAPIKey(apikeyValidator))
		{
			webhooks := api.Group("/webhooks")
			{
				webhooks.POST("/funding", func(c *gin.Context) {
					c.JSON(200, gin.H{"message": "Funding webhook"})
				})
			}
		}
	}
}

// SetupSecurityRoutes is the legacy function for backward compatibility
func SetupSecurityRoutes(
	router *gin.Engine,
	cfg *config.Config,
	db *sql.DB,
	zapLog *zap.Logger,
) {
	SetupSecurityRoutesEnhanced(router, cfg, db, zapLog, nil, nil, nil, nil, nil, nil)
}
