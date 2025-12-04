package routes

import (
	"context"
	"database/sql"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/api/handlers"
	"github.com/rail-service/rail_service/internal/api/middleware"
	"github.com/rail-service/rail_service/internal/domain/services/apikey"
	"github.com/rail-service/rail_service/internal/domain/services/session"
	"github.com/rail-service/rail_service/internal/domain/services/twofa"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/pkg/logger"
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

func SetupSecurityRoutes(
	router *gin.Engine,
	cfg *config.Config,
	db *sql.DB,
	zapLog *zap.Logger,
) {
	// Initialize services
	sessionService := session.NewService(db, zapLog)
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
		// Apply global rate limiting
		v1.Use(middleware.RateLimit(100)) // 100 requests per minute globally

		// Authentication required routes
		auth := v1.Group("")
		auth.Use(middleware.Authentication(cfg, log, sessionValidator))
		auth.Use(userRateLimiter.UserRateLimit(60)) // 60 requests per minute per user
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

		// Admin routes
		admin := v1.Group("/admin")
		admin.Use(middleware.Authentication(cfg, log, sessionValidator))
		admin.Use(middleware.AdminAuth(db, log))
		admin.Use(userRateLimiter.UserRateLimit(120)) // Higher limit for admins
		{
			// Admin API key management
			admin.GET("/api-keys", securityHandlers.AdminListAPIKeys)
			admin.DELETE("/api-keys/:id", securityHandlers.AdminRevokeAPIKey)
		}

		// API key authenticated routes
		api := v1.Group("/external")
		api.Use(middleware.ValidateAPIKey(apikeyValidator))
		{
			// Webhook endpoints
			webhooks := api.Group("/webhooks")
			{
				webhooks.POST("/funding", func(c *gin.Context) {
					c.JSON(200, gin.H{"message": "Funding webhook"})
				})
			}
		}
	}
}
