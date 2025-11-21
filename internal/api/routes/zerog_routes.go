package routes

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stack-service/stack_service/internal/api/handlers"
	"github.com/stack-service/stack_service/internal/api/middleware"
	"github.com/stack-service/stack_service/pkg/logger"
	"go.uber.org/zap"
)

// SimpleAPIKeyValidator implements APIKeyValidator interface
type SimpleAPIKeyValidator struct {
	validKeys map[string]bool
}

func (v *SimpleAPIKeyValidator) ValidateAPIKey(ctx context.Context, key string) (*middleware.APIKeyInfo, error) {
	if v.validKeys[key] {
		return &middleware.APIKeyInfo{
			ID:     uuid.New(),
			UserID: nil,
			Scopes: []string{"read", "write"},
		}, nil
	}
	return nil, context.Canceled
}

// SetupZeroGRoutes configures the 0G-related API routes
func SetupZeroGRoutes(
	router *gin.Engine,
	integrationHandlers *handlers.IntegrationHandlers,
	log *zap.Logger,
) {
	// Create a logger instance compatible with middleware
	loggerInstance := &logger.Logger{}
	loggerInstance.SugaredLogger = log.Sugar()

	// Internal 0G API routes (protected by internal auth and API key)
	internal := router.Group("/_internal/0g")
	{
		// Apply internal auth middleware (API key validation)
		// TODO: Load API keys from secure configuration
		log.Warn("Using placeholder API key - configure ZEROG_INTERNAL_API_KEYS in production")
		internal.Use(middleware.ValidateAPIKey(&SimpleAPIKeyValidator{validKeys: map[string]bool{"test-api-key": true}}))
		internal.Use(middleware.RequestID())
		internal.Use(middleware.Logger(loggerInstance))

		// Health check endpoints
		health := internal.Group("/health")
		{
			health.GET("/all", integrationHandlers.ZeroGHealthCheck)
		}
	}

	// Public AI-CFO API routes (protected by JWT auth)
	api := router.Group("/api/v1/ai")
	{
		// Apply authentication middleware for public endpoints
		api.Use(middleware.Authentication(nil, loggerInstance, nil))
		api.Use(middleware.RequestID())
		api.Use(middleware.Logger(loggerInstance))
		api.Use(middleware.RateLimit(10)) // Rate limiting for public APIs

		// Weekly summary endpoints
		summary := api.Group("/summary")
		{
			summary.GET("/latest", integrationHandlers.GetLatestSummary)
		}

		// On-demand analysis endpoints
		api.POST("/analyze", integrationHandlers.AnalyzeOnDemand)

		// Health check endpoint (lighter auth requirements)
		api.GET("/health", integrationHandlers.AICfoHealthCheck)
	}
}

// SetupZeroGMiddlewares sets up common middleware for 0G routes
func SetupZeroGMiddlewares(router *gin.Engine, log *zap.Logger) {
	// Create a logger instance compatible with middleware
	loggerInstance := &logger.Logger{}
	loggerInstance.SugaredLogger = log.Sugar()

	// Global middleware for all routes
	router.Use(middleware.CORS([]string{"*"}))
	router.Use(middleware.SecurityHeaders())
	router.Use(middleware.Recovery(loggerInstance))

	// Request size limits for specific routes
	router.Use(func(c *gin.Context) {
		// Limit request body size for storage operations
		if c.Request.URL.Path == "/_internal/0g/storage/store" {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 10<<20) // 10MB limit
		}
		// Limit for analysis requests
		if c.Request.URL.Path == "/api/v1/ai/analyze" {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20) // 1MB limit
		}
		c.Next()
	})
}

// ZeroGRouteConfig holds configuration for 0G routes
type ZeroGRouteConfig struct {
	EnableInternalAPI bool   `yaml:"enable_internal_api" env:"ZEROG_ENABLE_INTERNAL_API" envDefault:"true"`
	EnablePublicAPI   bool   `yaml:"enable_public_api" env:"ZEROG_ENABLE_PUBLIC_API" envDefault:"true"`
	APIKeyHeader      string `yaml:"api_key_header" env:"ZEROG_API_KEY_HEADER" envDefault:"X-Internal-API-Key"`
	RateLimitRPM      int    `yaml:"rate_limit_rpm" env:"ZEROG_RATE_LIMIT_RPM" envDefault:"10"` // Requests per minute
}
