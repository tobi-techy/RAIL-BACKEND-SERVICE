package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/api/routes"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/internal/infrastructure/database"
	"github.com/rail-service/rail_service/internal/infrastructure/di"
	"github.com/rail-service/rail_service/internal/workers/funding_webhook"
	portfolio_snapshot_worker "github.com/rail-service/rail_service/internal/workers/portfolio_snapshot_worker"
	scheduled_investment_worker "github.com/rail-service/rail_service/internal/workers/scheduled_investment_worker"
	walletprovisioning "github.com/rail-service/rail_service/internal/workers/wallet_provisioning"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/rail-service/rail_service/pkg/metrics"
	"github.com/rail-service/rail_service/pkg/tracing"
)

// Application represents the main application
type Application struct {
	cfg       *config.Config
	log       *logger.Logger
	server    *http.Server
	container *di.Container

	// Workers
	scheduler                 *walletprovisioning.Scheduler
	webhookManager            *funding_webhook.Manager
	scheduledInvestmentWorker *scheduled_investment_worker.Worker
	portfolioSnapshotWorker   *portfolio_snapshot_worker.Worker

	// Tracing
	tracingShutdown func(context.Context) error
}

// NewApplication creates a new application instance
func NewApplication() *Application {
	return &Application{}
}

// Initialize initializes the application
func (app *Application) Initialize() error {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	app.cfg = cfg

	// Initialize logger
	log := logger.New(cfg.LogLevel, cfg.Environment)
	app.log = log

	// Initialize database
	db, err := database.NewConnection(cfg.Database)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	// Run migrations
	if err := database.RunMigrations(cfg.Database.URL); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	// Initialize tracing
	if err := app.initializeTracing(); err != nil {
		return fmt.Errorf("failed to initialize tracing: %w", err)
	}

	// Build dependency injection container
	container, err := di.NewContainer(cfg, db, log)
	if err != nil {
		return fmt.Errorf("failed to create DI container: %w", err)
	}
	app.container = container

	// Initialize workers
	if err := app.initializeWorkers(); err != nil {
		return fmt.Errorf("failed to initialize workers: %w", err)
	}

	// Initialize server
	if err := app.initializeServer(); err != nil {
		return fmt.Errorf("failed to initialize server: %w", err)
	}

	return nil
}

// initializeTracing initializes OpenTelemetry tracing
func (app *Application) initializeTracing() error {
	tracingConfig := tracing.Config{
		Enabled:      app.cfg.Environment != "test",
		CollectorURL: getEnvOrDefault("OTEL_COLLECTOR_URL", "localhost:4317"),
		Environment:  app.cfg.Environment,
		SampleRate:   getSampleRate(app.cfg.Environment),
	}

	tracingShutdown, err := tracing.InitTracer(context.Background(), tracingConfig, app.log.Zap())
	if err != nil {
		return fmt.Errorf("failed to initialize tracing: %w", err)
	}

	app.tracingShutdown = tracingShutdown
	app.log.Info("OpenTelemetry tracing initialized", "collector_url", tracingConfig.CollectorURL)
	return nil
}

// initializeWorkers initializes all background workers
func (app *Application) initializeWorkers() error {
	// Wallet provisioning scheduler
	if err := app.initializeWalletProvisioning(); err != nil {
		return fmt.Errorf("failed to initialize wallet provisioning: %w", err)
	}

	// Funding webhook workers
	if err := app.initializeFundingWebhooks(); err != nil {
		return fmt.Errorf("failed to initialize funding webhooks: %w", err)
	}

	// Reconciliation scheduler
	if app.cfg.Reconciliation.Enabled {
		if err := app.container.ReconciliationScheduler.Start(context.Background()); err != nil {
			return fmt.Errorf("failed to start reconciliation scheduler: %w", err)
		}
		app.log.Info("Reconciliation scheduler started")
	}

	// Scheduled investment worker
	if app.container.GetScheduledInvestmentService() != nil {
		app.scheduledInvestmentWorker = scheduled_investment_worker.NewWorker(
			app.container.GetScheduledInvestmentService(),
			app.container.GetMarketDataService(),
			app.log.Zap(),
		)
		go app.scheduledInvestmentWorker.Start(context.Background())
		app.log.Info("Scheduled investment worker started")
	}

	// Portfolio snapshot worker
	if app.container.GetPortfolioAnalyticsService() != nil {
		app.portfolioSnapshotWorker = portfolio_snapshot_worker.NewWorker(
			app.container.GetPortfolioAnalyticsService(),
			app.container,
			app.log.Zap(),
		)
		go app.portfolioSnapshotWorker.Start(context.Background())
		app.log.Info("Portfolio snapshot worker started")
	}

	return nil
}

// initializeWalletProvisioning initializes wallet provisioning workers
func (app *Application) initializeWalletProvisioning() error {
	workerConfig := walletprovisioning.DefaultConfig()
	workerConfig.WalletSetNamePrefix = app.cfg.Circle.DefaultWalletSetName
	workerConfig.ChainsToProvision = app.container.WalletService.SupportedChains()
	workerConfig.DefaultWalletSetID = app.cfg.Circle.DefaultWalletSetID

	// Create user repository adapter
	userRepoAdapter := &userRepositoryAdapter{repo: app.container.UserRepo}

	worker := walletprovisioning.NewWorker(
		app.container.WalletRepo,
		app.container.WalletSetRepo,
		app.container.WalletProvisioningJobRepo,
		app.container.CircleClient,
		app.container.AuditService,
		userRepoAdapter,
		workerConfig,
		app.log.Zap(),
	)

	schedulerConfig := walletprovisioning.DefaultSchedulerConfig()
	scheduler := walletprovisioning.NewScheduler(
		worker,
		app.container.WalletProvisioningJobRepo,
		schedulerConfig,
		app.log.Zap(),
	)

	if err := scheduler.Start(); err != nil {
		return fmt.Errorf("failed to start wallet provisioning scheduler: %w", err)
	}

	app.scheduler = scheduler
	app.container.WalletProvisioningScheduler = scheduler
	app.log.Info("Wallet provisioning scheduler started")

	return nil
}

// initializeFundingWebhooks initializes funding webhook workers
func (app *Application) initializeFundingWebhooks() error {
	processorConfig := funding_webhook.DefaultProcessorConfig()
	reconciliationConfig := funding_webhook.DefaultReconciliationConfig()

	webhookManager, err := funding_webhook.NewManager(
		processorConfig,
		reconciliationConfig,
		app.container.FundingEventJobRepo,
		app.container.DepositRepo,
		app.container.FundingService,
		app.container.AuditService,
		app.log,
	)
	if err != nil {
		return fmt.Errorf("failed to create webhook manager: %w", err)
	}

	if err := webhookManager.Start(context.Background()); err != nil {
		return fmt.Errorf("failed to start webhook manager: %w", err)
	}

	app.webhookManager = webhookManager
	app.container.FundingWebhookManager = webhookManager
	app.log.Info("Funding webhook workers started")

	return nil
}

// initializeServer initializes the HTTP server
func (app *Application) initializeServer() error {
	// Set Gin mode
	if app.cfg.Environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	// Initialize router
	router := routes.SetupRoutes(app.container)

	// Setup security routes
	routes.SetupSecurityRoutesEnhanced(
		router,
		app.cfg,
		app.container.DB,
		app.log.Zap(),
		app.container.GetTokenBlacklist(),
		app.container.GetTieredRateLimiter(),
		app.container.GetLoginAttemptTracker(),
		app.container.GetIPWhitelistService(),
		app.container.GetDeviceTrackingService(),
		app.container.GetLoginProtectionService(),
	)

	// Create server
	app.server = &http.Server{
		Addr:           fmt.Sprintf(":%d", app.cfg.Server.Port),
		Handler:        router,
		ReadTimeout:    time.Duration(app.cfg.Server.ReadTimeout) * time.Second,
		WriteTimeout:   time.Duration(app.cfg.Server.WriteTimeout) * time.Second,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 1 << 20, // 1MB
	}

	return nil
}

// Start starts the application
func (app *Application) Start() error {
	// Start server in goroutine
	go func() {
		app.log.Info("Starting server",
			"port", app.cfg.Server.Port,
			"environment", app.cfg.Environment,
			"read_timeout", app.cfg.Server.ReadTimeout,
			"write_timeout", app.cfg.Server.WriteTimeout,
		)

		if err := app.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			app.log.Fatal("Failed to start server", "error", err)
		}
	}()

	// Start metrics collection
	go app.startMetricsCollection()

	return nil
}

// startMetricsCollection starts background metrics collection
func (app *Application) startMetricsCollection() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		// Update database connection metrics
		stats := app.container.DB.Stats()
		metrics.DatabaseConnectionsGauge.WithLabelValues("open").Set(float64(stats.OpenConnections))
		metrics.DatabaseConnectionsGauge.WithLabelValues("idle").Set(float64(stats.Idle))
		metrics.DatabaseConnectionsGauge.WithLabelValues("in_use").Set(float64(stats.InUse))
	}
}

// Shutdown gracefully shuts down the application
func (app *Application) Shutdown() error {
	app.log.Info("Shutting down server...")

	// Stop workers
	app.stopWorkers()

	// Shutdown server
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := app.server.Shutdown(ctx); err != nil {
		app.log.Fatal("Server forced to shutdown", "error", err)
	}

	// Shutdown tracing
	if app.tracingShutdown != nil {
		app.tracingShutdown(context.Background())
	}

	app.log.Info("Server exited gracefully")
	return nil
}

// stopWorkers stops all background workers
func (app *Application) stopWorkers() {
	// Stop wallet provisioning scheduler
	if app.scheduler != nil {
		app.log.Info("Stopping wallet provisioning scheduler...")
		if err := app.scheduler.Stop(); err != nil {
			app.log.Warn("Error stopping scheduler", "error", err)
		}
	}

	// Stop funding webhook manager
	if app.webhookManager != nil {
		app.log.Info("Stopping funding webhook manager...")
		if err := app.webhookManager.Shutdown(30 * time.Second); err != nil {
			app.log.Warn("Error stopping webhook manager", "error", err)
		}
	}

	// Stop reconciliation scheduler
	if app.cfg.Reconciliation.Enabled && app.container.ReconciliationScheduler != nil {
		app.log.Info("Stopping reconciliation scheduler...")
		if err := app.container.ReconciliationScheduler.Stop(); err != nil {
			app.log.Warn("Error stopping reconciliation scheduler", "error", err)
		}
	}

	// Stop scheduled investment worker
	if app.scheduledInvestmentWorker != nil {
		app.log.Info("Stopping scheduled investment worker...")
		app.scheduledInvestmentWorker.Stop()
	}

	// Stop portfolio snapshot worker
	if app.portfolioSnapshotWorker != nil {
		app.log.Info("Stopping portfolio snapshot worker...")
		app.portfolioSnapshotWorker.Stop()
	}
}

// WaitForShutdown waits for interrupt signal
func (app *Application) WaitForShutdown() {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
}

// getEnvOrDefault returns environment variable value or default
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getSampleRate returns appropriate sampling rate based on environment
func getSampleRate(env string) float64 {
	switch env {
	case "production":
		return 0.1 // 10% sampling in production
	case "staging":
		return 0.5 // 50% sampling in staging
	default:
		return 1.0 // 100% sampling in development/test
	}
}

// userRepositoryAdapter adapts infrastructure UserRepository to wallet provisioning UserRepository
type userRepositoryAdapter struct {
	repo interface {
		GetByID(context.Context, uuid.UUID) (*entities.UserProfile, error)
	}
}

func (a *userRepositoryAdapter) GetByID(ctx context.Context, id uuid.UUID) (*entities.User, error) {
	profile, err := a.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return &entities.User{
		ID:                 profile.ID,
		Email:              profile.Email,
		Phone:              profile.Phone,
		EmailVerified:      profile.EmailVerified,
		PhoneVerified:      profile.PhoneVerified,
		OnboardingStatus:   profile.OnboardingStatus,
		KYCStatus:          profile.KYCStatus,
		KYCProviderRef:     profile.KYCProviderRef,
		KYCSubmittedAt:     profile.KYCSubmittedAt,
		KYCApprovedAt:      profile.KYCApprovedAt,
		KYCRejectionReason: profile.KYCRejectionReason,
		BridgeCustomerID:   profile.BridgeCustomerID,
		IsActive:           profile.IsActive,
		CreatedAt:          profile.CreatedAt,
		UpdatedAt:          profile.UpdatedAt,
	}, nil
}
