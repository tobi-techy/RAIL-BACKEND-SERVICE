package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

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

	"github.com/gin-gonic/gin"
)

// @title Stack Service API
// @version 1.0
// @description GenZ Web3 Multi-Chain Investment Platform API
// @termsOfService http://swagger.io/terms/

// @contact.name API Support
// @contact.url http://www.stackservice.com/support
// @contact.email support@stackservice.com

// @license.name Apache 2.0
// @license.url http://www.apache.org/licenses/LICENSE-2.0.html

// @host localhost:8080
// @BasePath /api/v1

// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Type "Bearer" followed by a space and JWT token.

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
		DueAccountID:       profile.DueAccountID,
		IsActive:           profile.IsActive,
		CreatedAt:          profile.CreatedAt,
		UpdatedAt:          profile.UpdatedAt,
	}, nil
}

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		panic(fmt.Sprintf("Failed to load config: %v", err))
	}

	// Initialize logger
	log := logger.New(cfg.LogLevel, cfg.Environment)

	// Initialize OpenTelemetry tracing
	tracingConfig := tracing.Config{
		Enabled:      cfg.Environment != "test",
		CollectorURL: "localhost:4317",
		Environment:  cfg.Environment,
		SampleRate:   1.0, // 100% sampling in dev/staging, reduce in production
	}

	tracingShutdown, err := tracing.InitTracer(context.Background(), tracingConfig, log.Zap())
	if err != nil {
		log.Fatal("Failed to initialize tracing", "error", err)
	}
	defer tracingShutdown(context.Background())
	log.Info("OpenTelemetry tracing initialized", "collector_url", tracingConfig.CollectorURL)

	// Initialize database with enhanced configuration
	db, err := database.NewConnection(cfg.Database)
	if err != nil {
		log.Fatal("Failed to connect to database", "error", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Warn("Failed to close database connection", "error", err)
		}
	}()

	// Run migrations
	if err := database.RunMigrations(cfg.Database.URL); err != nil {
		log.Fatal("Failed to run migrations", "error", err)
	}

	// Set Gin mode
	if cfg.Environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	// Build dependency injection container
	container, err := di.NewContainer(cfg, db, log)
	if err != nil {
		log.Fatal("Failed to create DI container", "error", err)
	}

	// Initialize router with DI container
	router := routes.SetupRoutes(container)

	// Setup security routes with enhanced security features
	routes.SetupSecurityRoutesEnhanced(
		router,
		cfg,
		db,
		log.Zap(),
		container.GetTokenBlacklist(),
		container.GetTieredRateLimiter(),
		container.GetLoginAttemptTracker(),
		container.GetIPWhitelistService(),
		container.GetDeviceTrackingService(),
		container.GetLoginProtectionService(),
	)

	// Initialize wallet provisioning worker and scheduler
	workerConfig := walletprovisioning.DefaultConfig()
	workerConfig.WalletSetNamePrefix = cfg.Circle.DefaultWalletSetName
	workerConfig.ChainsToProvision = container.WalletService.SupportedChains()
	workerConfig.DefaultWalletSetID = cfg.Circle.DefaultWalletSetID

	// Create user repository adapter for wallet provisioning
	userRepoAdapter := &userRepositoryAdapter{repo: container.UserRepo}

	worker := walletprovisioning.NewWorker(
		container.WalletRepo,
		container.WalletSetRepo,
		container.WalletProvisioningJobRepo,
		container.CircleClient,
		container.AuditService,
		userRepoAdapter,
		container.DueService,
		workerConfig,
		log.Zap(),
	)

	schedulerConfig := walletprovisioning.DefaultSchedulerConfig()
	scheduler := walletprovisioning.NewScheduler(
		worker,
		container.WalletProvisioningJobRepo,
		schedulerConfig,
		log.Zap(),
	)

	// Start the scheduler
	if err := scheduler.Start(); err != nil {
		log.Fatal("Failed to start wallet provisioning scheduler", "error", err)
	}
	log.Info("Wallet provisioning scheduler started")

	// Store scheduler in container for access by handlers
	container.WalletProvisioningScheduler = scheduler

	// Initialize funding webhook workers
	processorConfig := funding_webhook.DefaultProcessorConfig()
	reconciliationConfig := funding_webhook.DefaultReconciliationConfig()

	webhookManager, err := funding_webhook.NewManager(
		processorConfig,
		reconciliationConfig,
		container.FundingEventJobRepo,
		container.DepositRepo,
		container.FundingService,
		container.AuditService,
		log,
	)
	if err != nil {
		log.Fatal("Failed to create webhook manager", "error", err)
	}

	// Start the webhook manager
	if err := webhookManager.Start(context.Background()); err != nil {
		log.Fatal("Failed to start webhook manager", "error", err)
	}
	log.Info("Funding webhook workers started")

	// Store webhook manager in container for access by handlers
	container.FundingWebhookManager = webhookManager

	// Initialize and start reconciliation scheduler
	if cfg.Reconciliation.Enabled {
		log.Info("Starting reconciliation scheduler", 
			"auto_correct", cfg.Reconciliation.AutoCorrectLowSeverity,
		)
		if err := container.ReconciliationScheduler.Start(context.Background()); err != nil {
			log.Fatal("Failed to start reconciliation scheduler", "error", err)
		}
		log.Info("Reconciliation scheduler started")
	} else {
		log.Info("Reconciliation scheduler disabled in configuration")
	}

	// Initialize and start scheduled investment worker
	var scheduledInvestmentWorker *scheduled_investment_worker.Worker
	if container.GetScheduledInvestmentService() != nil {
		scheduledInvestmentWorker = scheduled_investment_worker.NewWorker(
			container.GetScheduledInvestmentService(),
			container.GetMarketDataService(),
			log.Zap(),
		)
		go scheduledInvestmentWorker.Start(context.Background())
		log.Info("Scheduled investment worker started")
	}

	// Initialize and start portfolio snapshot worker
	var portfolioSnapshotWorker *portfolio_snapshot_worker.Worker
	if container.GetPortfolioAnalyticsService() != nil {
		portfolioSnapshotWorker = portfolio_snapshot_worker.NewWorker(
			container.GetPortfolioAnalyticsService(),
			container,
			log.Zap(),
		)
		go portfolioSnapshotWorker.Start(context.Background())
		log.Info("Portfolio snapshot worker started")
	}

	// Create server with enhanced configuration
	server := &http.Server{
		Addr:           fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:        router,
		ReadTimeout:    time.Duration(cfg.Server.ReadTimeout) * time.Second,
		WriteTimeout:   time.Duration(cfg.Server.WriteTimeout) * time.Second,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 1 << 20, // 1MB
	}

	// Start server in goroutine
	go func() {
		log.Info("Starting server", 
			"port", cfg.Server.Port, 
			"environment", cfg.Environment,
			"read_timeout", cfg.Server.ReadTimeout,
			"write_timeout", cfg.Server.WriteTimeout,
		)
		
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("Failed to start server", "error", err)
		}
	}()

	// Initialize metrics collection
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		
		for range ticker.C {
			// Update database connection metrics
			stats := db.Stats()
			metrics.DatabaseConnectionsGauge.WithLabelValues("open").Set(float64(stats.OpenConnections))
			metrics.DatabaseConnectionsGauge.WithLabelValues("idle").Set(float64(stats.Idle))
			metrics.DatabaseConnectionsGauge.WithLabelValues("in_use").Set(float64(stats.InUse))
		}
	}()

	// Wait for interrupt signal to gracefully shut down the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("Shutting down server...")

	// Stop the wallet provisioning scheduler
	log.Info("Stopping wallet provisioning scheduler...")
	if err := scheduler.Stop(); err != nil {
		log.Warn("Error stopping scheduler", "error", err)
	}

	// Stop the funding webhook manager
	log.Info("Stopping funding webhook manager...")
	if webhookMgr, ok := container.FundingWebhookManager.(*funding_webhook.Manager); ok {
		if err := webhookMgr.Shutdown(30 * time.Second); err != nil {
			log.Warn("Error stopping webhook manager", "error", err)
		}
	}

	// Stop the reconciliation scheduler
	if cfg.Reconciliation.Enabled && container.ReconciliationScheduler != nil {
		log.Info("Stopping reconciliation scheduler...")
		if err := container.ReconciliationScheduler.Stop(); err != nil {
			log.Warn("Error stopping reconciliation scheduler", "error", err)
		}
	}

	// Stop scheduled investment worker
	if scheduledInvestmentWorker != nil {
		log.Info("Stopping scheduled investment worker...")
		scheduledInvestmentWorker.Stop()
	}

	// Stop portfolio snapshot worker
	if portfolioSnapshotWorker != nil {
		log.Info("Stopping portfolio snapshot worker...")
		portfolioSnapshotWorker.Stop()
	}

	// Give outstanding requests time to complete
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatal("Server forced to shutdown", "error", err)
	}

	log.Info("Server exited gracefully")
}
