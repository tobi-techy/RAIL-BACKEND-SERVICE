package app

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/shopspring/decimal"

	"github.com/rail-service/rail_service/internal/api/routes"
	"github.com/rail-service/rail_service/internal/domain/entities"
	aiservice "github.com/rail-service/rail_service/internal/domain/services/ai"
	statement "github.com/rail-service/rail_service/internal/domain/services/statement"
	kycservice "github.com/rail-service/rail_service/internal/domain/services/kyc"
	alpacaadapter "github.com/rail-service/rail_service/internal/infrastructure/adapters/alpaca"
	bridgeadapter "github.com/rail-service/rail_service/internal/infrastructure/adapters/bridge"
	circleadapter "github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
	diditadapter "github.com/rail-service/rail_service/internal/infrastructure/adapters/didit"
	sumsubadapter "github.com/rail-service/rail_service/internal/infrastructure/adapters/sumsub"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/internal/infrastructure/database"
	"github.com/rail-service/rail_service/internal/infrastructure/di"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	supermemoryclient "github.com/rail-service/rail_service/internal/infrastructure/supermemory"
	ai_insights "github.com/rail-service/rail_service/internal/workers/ai_insights"
	automation_worker "github.com/rail-service/rail_service/internal/workers/automation_worker"
	balance_reconciliation "github.com/rail-service/rail_service/internal/workers/balance_reconciliation"
	bridge_govid_repair "github.com/rail-service/rail_service/internal/workers/bridge_govid_repair"
	daily_pulse "github.com/rail-service/rail_service/internal/workers/daily_pulse"
	deposit_allocation_recovery "github.com/rail-service/rail_service/internal/workers/deposit_allocation_recovery"
	deposit_autosweep "github.com/rail-service/rail_service/internal/workers/deposit_autosweep"
	"github.com/rail-service/rail_service/internal/workers/funding_webhook"
	gameplay_workers "github.com/rail-service/rail_service/internal/workers/gameplay"
	growth_engine "github.com/rail-service/rail_service/internal/workers/growth_engine"
	growth_mail "github.com/rail-service/rail_service/internal/workers/growth_mail"
	kyc_autoinvest "github.com/rail-service/rail_service/internal/workers/kyc_autoinvest"
	"github.com/rail-service/rail_service/internal/workers/kyc_sync"
	memory_worker "github.com/rail-service/rail_service/internal/workers/memory_worker"
	miriam_worker "github.com/rail-service/rail_service/internal/workers/miriam_worker"
	opportunity_sync "github.com/rail-service/rail_service/internal/workers/opportunity_sync"
	paj_offramp_recovery "github.com/rail-service/rail_service/internal/workers/paj_offramp_recovery"
	paj_onramp_recovery "github.com/rail-service/rail_service/internal/workers/paj_onramp_recovery"
	portfolio_snapshot_worker "github.com/rail-service/rail_service/internal/workers/portfolio_snapshot_worker"
	rebalancing_worker "github.com/rail-service/rail_service/internal/workers/rebalancing_worker"
	scheduled_investment_worker "github.com/rail-service/rail_service/internal/workers/scheduled_investment_worker"
	scheduled_notifications "github.com/rail-service/rail_service/internal/workers/scheduled_notifications"
	statement_processor "github.com/rail-service/rail_service/internal/workers/statement_processor"
	subscription_billing "github.com/rail-service/rail_service/internal/workers/subscription_billing"
	walletprovisioning "github.com/rail-service/rail_service/internal/workers/wallet_provisioning"
	withdrawal_recovery "github.com/rail-service/rail_service/internal/workers/withdrawal_recovery"
	"github.com/rail-service/rail_service/pkg/alerting"
	"github.com/rail-service/rail_service/pkg/analytics"
	"github.com/rail-service/rail_service/pkg/jobqueue"
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
	scheduler                    *walletprovisioning.Scheduler
	webhookManager               *funding_webhook.Manager
	scheduledInvestmentWorker    *scheduled_investment_worker.Worker
	portfolioSnapshotWorker      *portfolio_snapshot_worker.Worker
	depositAllocationWorker      *deposit_allocation_recovery.Worker
	pajOfframpRecoveryWorker     *paj_offramp_recovery.Worker
	pajOnrampRecoveryWorker      *paj_onramp_recovery.Worker
	withdrawalRecoveryWorker     *withdrawal_recovery.Worker
	kycAutoInvestWorker          *kyc_autoinvest.Worker
	rebalancingWorker            *rebalancing_worker.Worker
	kycSyncWorker                *kyc_sync.Worker
	balanceReconciliationWorker  *balance_reconciliation.Worker
	bridgeGovIDRepairWorker      *bridge_govid_repair.Worker
	bridgeGovIDRepairCancel      context.CancelFunc
	scheduledNotificationsWorker *scheduled_notifications.Worker
	subscriptionBillingWorker    *subscription_billing.Worker
	streakEvaluatorWorker        *gameplay_workers.StreakEvaluator
	challengeRotatorWorker       *gameplay_workers.ChallengeRotator
	achievementCheckerWorker     *gameplay_workers.AchievementChecker
	insightGeneratorWorker       *gameplay_workers.InsightGenerator
	dailyMetricsWorker           *gameplay_workers.DailyMetricsWorker
	aiInsightsWorker             *ai_insights.Worker
	automationWorker             *automation_worker.Worker
	memoryWorker                 *memory_worker.Worker
	miriamWorker                 *miriam_worker.Worker
	miriamWorkerCancel           context.CancelFunc
	dailyPulseWorker             *daily_pulse.Worker
	growthEngineWorker           *growth_engine.Worker
	growthEngineCancel           context.CancelFunc
	workerMu                     sync.Mutex
	growthMailWorker             *growth_mail.Worker
	growthMailCancel             context.CancelFunc
	opportunitySyncWorker        *opportunity_sync.Worker
	depositAutoSweepWorker       *deposit_autosweep.Worker

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
	db, err := database.NewConnection(cfg.Database, cfg.Environment)
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

	// Initialize Mixpanel analytics
	analytics.Init(log.Zap())

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
	collectorURL := getEnvOrDefault("OTEL_COLLECTOR_URL", "")
	tracingEnabled := getBoolEnvOrDefault("OTEL_TRACING_ENABLED", collectorURL != "" && app.cfg.Environment != "test")
	tracingConfig := tracing.Config{
		Enabled:      tracingEnabled,
		CollectorURL: collectorURL,
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

	// Deposit allocation recovery worker
	if app.container.DB != nil && app.container.GetAllocationService() != nil {
		app.depositAllocationWorker = deposit_allocation_recovery.NewWorker(
			app.container.DB,
			app.container.GetAllocationService(),
			app.log.Zap(),
			deposit_allocation_recovery.DefaultConfig(),
		)
		go app.depositAllocationWorker.Start(context.Background())
		app.log.Info("Deposit allocation recovery worker started")
	}

	// Paj offramp recovery worker — auto-reverses stuck NGN withdrawals and
	// reconciles orders whose Circle transfer was initiated but never
	// webhooked, using Circle's own state as the source of truth.
	if app.container.DB != nil && app.container.PajHandlers != nil && app.container.LedgerService != nil {
		app.pajOfframpRecoveryWorker = paj_offramp_recovery.NewWorker(app.container.DB, di.NewWithdrawalLedgerAdapter(app.container.LedgerService), app.log.Zap())
		if app.container.NotificationService != nil {
			app.pajOfframpRecoveryWorker.SetNotifier(&pajOfframpRecoveryNotifierAdapter{svc: app.container.NotificationService})
		}
		if app.container.CircleAdapter != nil {
			app.pajOfframpRecoveryWorker.SetCircleStatusChecker(&pajOfframpCircleStatusAdapter{adapter: app.container.CircleAdapter})
		} else {
			app.log.Warn("Paj offramp recovery: no Circle adapter — reconciliation of post-transfer stuck orders disabled")
		}
		go app.pajOfframpRecoveryWorker.Start(context.Background())
		app.log.Info("Paj offramp recovery worker started")
	}

	// Paj onramp recovery worker — marks stuck NGN deposits as failed for retry
	if app.container.DB != nil && app.container.PajHandlers != nil {
		app.pajOnrampRecoveryWorker = paj_onramp_recovery.NewWorker(app.container.DB, app.log.Zap())
		go app.pajOnrampRecoveryWorker.Start(context.Background())
		app.log.Info("Paj onramp recovery worker started")
	}

	// Withdrawal recovery worker — auto-reverses stuck crypto withdrawals and
	// polls the provider for withdrawals whose webhook never landed.
	if app.container.DB != nil && app.container.LedgerService != nil {
		app.withdrawalRecoveryWorker = withdrawal_recovery.NewWorker(app.container.DB, di.NewWithdrawalLedgerAdapter(app.container.LedgerService), app.log.Zap())
		if app.container.WithdrawalService != nil {
			app.withdrawalRecoveryWorker.SetWithdrawalSyncer(app.container.WithdrawalService)
		}
		go app.withdrawalRecoveryWorker.Start(context.Background())
		app.log.Info("Withdrawal recovery worker started")
	}

	// KYC auto-invest worker
	if app.container.DB != nil && app.container.GetAutoInvestService() != nil {
		app.kycAutoInvestWorker = kyc_autoinvest.NewWorker(
			app.container.DB,
			app.container.GetAutoInvestService(),
			app.log.Zap(),
			kyc_autoinvest.DefaultConfig(),
		)
		go app.kycAutoInvestWorker.Start(context.Background())
		app.log.Info("KYC auto-invest worker started")
	}

	// Rebalancing worker
	rulesRepo, positionRepo, strategyProvider, orderPlacer := app.container.GetRebalancingWorkerDeps()
	if rulesRepo != nil && positionRepo != nil {
		app.rebalancingWorker = rebalancing_worker.NewWorker(
			rulesRepo,
			positionRepo,
			strategyProvider,
			orderPlacer,
			nil, // notifier — optional
			app.log.Zap(),
		)
		go app.rebalancingWorker.Start(context.Background())
		app.log.Info("Rebalancing worker started")
	}

	// KYC Sumsub sync worker
	if err := app.initializeKYCSyncWorker(); err != nil {
		return fmt.Errorf("failed to initialize KYC sync worker: %w", err)
	}

	// Scheduled push notifications (KYC reminders + daily engagement)
	if app.container.ExpoPushService != nil && app.container.UserRepo != nil {
		app.scheduledNotificationsWorker = scheduled_notifications.NewWorker(
			app.container.UserRepo,
			app.container.ExpoPushService,
			app.log.Zap(),
		)
		go app.scheduledNotificationsWorker.Start(context.Background())
		app.log.Info("Scheduled notifications worker started")
	}

	// Subscription billing worker
	if app.container.SubscriptionService != nil {
		app.subscriptionBillingWorker = subscription_billing.NewWorker(
			app.container.SubscriptionService,
			app.log.Zap(),
		)
		go app.subscriptionBillingWorker.Start(context.Background())
		app.log.Info("Subscription billing worker started")
	}

	// Gameplay workers
	if app.container.GameplayStreakService != nil {
		// Resolve push notifier: SNS preferred, Expo fallback
		var pushSender gameplay_workers.PushNotifier
		if app.container.SNSPushService != nil {
			pushSender = app.container.SNSPushService
		} else if app.container.ExpoPushService != nil {
			pushSender = app.container.ExpoPushService
		}

		app.streakEvaluatorWorker = gameplay_workers.NewStreakEvaluator(
			app.container.GameplayStreakService, pushSender, app.log.Zap())
		go app.streakEvaluatorWorker.Start(context.Background())

		app.challengeRotatorWorker = gameplay_workers.NewChallengeRotator(
			app.container.GameplayChallengeService, app.log.Zap())
		go app.challengeRotatorWorker.Start(context.Background())

		app.achievementCheckerWorker = gameplay_workers.NewAchievementChecker(
			app.container.GameplayAchievementService, app.container.GameplayRepo, app.log.Zap())
		go app.achievementCheckerWorker.Start(context.Background())

		app.insightGeneratorWorker = gameplay_workers.NewInsightGenerator(
			app.container.GameplayRepo,
			app.container.LedgerService,
			app.container.GameplayXPService,
			app.container.GameplayStreakService,
			app.container.SubscriptionService,
			pushSender,
			app.log.Zap())
		go app.insightGeneratorWorker.Start(context.Background())

		app.dailyMetricsWorker = gameplay_workers.NewDailyMetricsWorker(
			app.container.GameplayRepo,
			app.container.GameplayRepo,
			app.container.LedgerService,
			app.container.GameplayChallengeService,
			app.container.GameplayStreakService,
			app.log.Zap())
		go app.dailyMetricsWorker.Start(context.Background())

		app.log.Info("Gameplay workers started (streak evaluator, challenge rotator, achievement checker, insight generator, daily metrics)")
	}

	if app.container.UserRepo != nil && app.container.LedgerSpendingRepo != nil && app.container.LedgerService != nil {
		var pushSender ai_insights.PushSender
		if app.container.SNSPushService != nil {
			pushSender = app.container.SNSPushService
		} else if app.container.ExpoPushService != nil {
			pushSender = app.container.ExpoPushService
		}

		if pushSender != nil {
			var cooldowns ai_insights.CooldownStore
			if app.container.RedisClient != nil {
				cooldowns = app.container.RedisClient.Client()
			}
			app.aiInsightsWorker = ai_insights.NewWorker(
				app.container.UserRepo,
				pushSender,
				cooldowns,
				app.container.LedgerSpendingRepo,
				app.container.BudgetRepo,
				app.container.LedgerService,
				app.container.SubscriptionService,
				app.log.Zap(),
			)
			go app.aiInsightsWorker.Start(context.Background())
			app.log.Info("AI insights worker started")
		}
	}

	// Start memory worker (transaction patterns, decay, summarization)
	if app.container.MemoryService != nil && app.container.LedgerSpendingRepo != nil {
		app.memoryWorker = memory_worker.NewWorker(
			app.container.MemoryService,
			app.container.LedgerSpendingRepo,
			app.container.LedgerService,
			app.log.Zap(),
		)
		go app.memoryWorker.Start(context.Background())
		app.log.Info("Memory worker started")
	}

	if app.container.AutomationService != nil {
		app.automationWorker = automation_worker.NewWorker(app.container.AutomationService, app.log.Zap())
		go app.automationWorker.Start(context.Background())
		app.log.Info("Miriam automation worker started")
	}

	if app.cfg.Workers.MiriamIntelligenceLocal && app.container.MiriamIntelligenceService != nil && app.container.UserRepo != nil {
		if app.container.MiriamIntelligenceOrchestrator != nil {
			app.miriamWorker = miriam_worker.NewWorkerWithIntelligence(
				app.container.UserRepo,
				app.container.MiriamIntelligenceService,
				app.container.MiriamIntelligenceOrchestrator,
				app.log.Zap(),
			)
			app.log.Info("Miriam intelligence worker started (unified brain)")
		} else {
			app.miriamWorker = miriam_worker.NewWorker(app.container.UserRepo, app.container.MiriamIntelligenceService, app.log.Zap())
			app.log.Info("Miriam intelligence worker started (classic mode)")
		}
		miriamCtx, miriamCancel := context.WithCancel(context.Background())
		app.miriamWorkerCancel = miriamCancel
		go app.miriamWorker.Start(miriamCtx)
	} else if !app.cfg.Workers.MiriamIntelligenceLocal {
		app.log.Info("Miriam intelligence local worker disabled; expecting external scheduler")
	}

	// Opportunity sync worker — ingests Superteam Earn listings and generates weekly picks
	if app.container.OpportunityService != nil && app.container.UserRepo != nil {
		app.opportunitySyncWorker = opportunity_sync.NewWorker(
			app.container.OpportunityService,
			&opportunityUserListerAdapter{repo: app.container.UserRepo},
			app.log.Zap(),
		)
		go app.opportunitySyncWorker.Start(context.Background())
		app.log.Info("Opportunity sync worker started")
	}

	// Deposit auto-sweep worker: bridges non-Solana Circle deposits to Solana
	if app.container.DepositSweepRepo != nil && app.container.ChainRailsClient != nil && app.container.WalletRepo != nil {
		var sweepAlerter deposit_autosweep.Alerter
		if ta := alerting.NewTelegramAlerter(
			app.cfg.TelegramAlerts.BotToken,
			app.cfg.TelegramAlerts.ChatID,
		); ta != nil {
			sweepAlerter = ta
		} else {
			app.log.Warn("Deposit auto-sweep alerter not configured — exhausted sweeps will not trigger alerts")
		}
		app.depositAutoSweepWorker = deposit_autosweep.NewWorker(
			app.container.DepositSweepRepo,
			app.container.WalletRepo,
			app.container.ChainRailsClient,
			sweepAlerter,
			app.log.Zap(),
		)
		app.depositAutoSweepWorker.Start()
		app.log.Info("Deposit auto-sweep worker started")
	}

	if app.container.UserRepo != nil && app.container.LedgerService != nil && app.container.LedgerSpendingRepo != nil && app.container.BudgetRepo != nil {
		var pushSender daily_pulse.PushSender
		if app.container.SNSPushService != nil {
			pushSender = app.container.SNSPushService
		} else if app.container.ExpoPushService != nil {
			pushSender = app.container.ExpoPushService
		}
		if pushSender != nil {
			app.dailyPulseWorker = daily_pulse.NewWorker(
				&dailyPulseUserRepoAdapter{repo: app.container.UserRepo},
				app.container.LedgerService,
				app.container.LedgerSpendingRepo,
				app.container.BudgetRepo,
				nil,
				pushSender,
				app.log.Zap(),
			)
			if app.container.AIOrchestrator != nil {
				app.dailyPulseWorker.SetBriefProvider(&dailyPulseBriefProvider{orchestrator: app.container.AIOrchestrator})
			}
			if app.container.AIProviderManager != nil {
				app.dailyPulseWorker.SetNudger(daily_pulse.NewAINudger(app.container.AIProviderManager, app.log.Zap()))
			}
			go app.dailyPulseWorker.Start(context.Background())
			app.log.Info("Miriam daily pulse worker started")
		}
	}

	if app.container.GrowthMailService != nil {
		app.growthMailWorker = growth_mail.NewWorker(app.container.GrowthMailService, app.log.Zap())
		ctx, cancel := context.WithCancel(context.Background())
		app.growthMailCancel = cancel
		go app.growthMailWorker.Start(ctx)
		app.log.Info("Growth mail worker started")
	}

	if app.container.GrowthEngineService != nil {
		w := growth_engine.NewWorker(app.container.GrowthEngineService, app.log.Zap())
		app.workerMu.Lock()
		app.growthEngineWorker = w
		app.workerMu.Unlock()
		ctx, cancel := context.WithCancel(context.Background())
		app.growthEngineCancel = cancel
		go func() {
			defer func() {
				if r := recover(); r != nil {
					app.log.Error("growth engine worker panicked", "panic", r)
					app.workerMu.Lock()
					app.growthEngineWorker = nil
					app.workerMu.Unlock()
					// TODO: implement automatic restart with exponential backoff
					// TODO: alert monitoring systems on worker panic
				}
			}()
			app.log.Info("Growth engine worker started")
			app.workerMu.Lock()
			worker := app.growthEngineWorker
			if worker == nil {
				app.workerMu.Unlock()
				return
			}
			app.workerMu.Unlock()
			worker.Start(ctx)
			app.log.Info("Growth engine worker stopped")
		}()
	}


	// Statement processor worker: processes uploaded bank statement PDFs via multi-strategy pipeline
	if app.container.BankStatementRepo != nil && app.container.JobQueueInstance != nil {
		kimiKey := app.container.Config.AI.Kimi.APIKey
		kimiBase := app.container.Config.AI.Kimi.BaseURL
		kimiModel := app.container.Config.AI.Kimi.Model
		if kimiKey != "" {
			parser := statement.NewTransactionParserWithConfig(kimiKey, kimiBase, kimiModel, app.log.Zap())

			// Build V2 pipeline with multi-strategy extraction and LLM failover
			var textractClient statement.TextractClient
			if app.container.Config.Statement.EnableOCR && app.container.Config.Statement.TextractRegion != "" {
				tc, err := statement.NewTextractExtractor(context.Background(), statement.TextractConfig{
					Region: app.container.Config.Statement.TextractRegion,
				}, app.log.Zap())
				if err == nil {
					textractClient = tc
					app.log.Info("Textract OCR enabled for statement processing")
				} else {
					app.log.Warn("Textract init failed, OCR disabled", "error", err)
				}
			}

			var visionClient statement.VisionClient
			if app.container.Config.AI.Kimi.APIKey != "" {
				visionClient = statement.NewOpenAIVisionClientWithConfig(
					app.container.Config.AI.Kimi.APIKey,
					"https://api.moonshot.ai/v1",
					"moonshot-v1-32k-vision-preview",
					app.log.Zap(),
				)
			}

			extractor := statement.NewDocumentExtractor(textractClient, visionClient, app.log.Zap())

			// Primary parser: Kimi (has credit, handles long contexts)
			var primaryParser *statement.TransactionParser
			primaryParser = parser // Kimi primary

			// File store: S3 if configured, nil falls back to DB BLOB
			var fileStore statement.FileStore
			if app.container.Config.Statement.S3Bucket != "" {
				fs, err := statement.NewS3FileStore(context.Background(), statement.S3Config{
					Region: app.container.Config.Statement.S3Region,
					Bucket: app.container.Config.Statement.S3Bucket,
					Prefix: app.container.Config.Statement.S3Prefix,
				}, app.log.Zap())
				if err == nil {
					fileStore = fs
					app.log.Info("S3 file store enabled for statements", "bucket", app.container.Config.Statement.S3Bucket)
				}
			}

			// Progress reporter via Redis
			var reporter statement.ProgressReporter
			if app.container.RedisClient != nil {
				rr := statement.NewRedisProgressReporter(app.container.RedisClient.Client(), app.log.Zap())
				if rr != nil {
					reporter = rr
				}
			}
			if reporter == nil {
				reporter = &statement.NoOpReporter{}
			}

			pipeline := statement.NewPipeline(statement.PipelineConfig{
				Extractor:      extractor,
				PrimaryParser:  primaryParser,
				FallbackParser: nil,
				FileStore:      fileStore,
				Reporter:       reporter,
				Logger:         app.log.Zap(),
			})

			// Build Supermemory writer adapter for statement worker
			var smWriter statement_processor.SupermemoryWriter
			if app.container.SupermemoryClient != nil {
				smWriter = &statementSupermemoryAdapter{client: app.container.SupermemoryClient}
			}

			stmtWorkerV2 := statement_processor.NewWorkerV2(
				app.container.BankStatementRepo,
				pipeline,
				fileStore,
				app.container.MiriamMemoryRepo,
				smWriter,
				app.container.NotificationService,
				&statementConversationAdapter{repo: app.container.ConversationRepo},
				reporter,
				app.log.Zap(),
			)

			// Also keep V1 worker for backward compat with existing queued jobs
			stmtWorkerV1 := statement_processor.NewWorker(
				app.container.BankStatementRepo,
				app.container.MiriamMemoryRepo,
				app.container.NotificationService,
				parser,
				app.log.Zap(),
			)

			// Cache handler functions to avoid re-creating closures on every job
			v2Handler := stmtWorkerV2.HandlerV2()
			v1Handler := stmtWorkerV1.Handler()

			jqWorker := jobqueue.NewWorker(app.container.JobQueueInstance, app.log.Zap(), 5)
			// Register a unified handler that routes based on payload version
			jqWorker.RegisterHandler(statement_processor.JobType, func(ctx context.Context, job *jobqueue.Job) error {
				if v, _ := job.Payload["version"].(string); v == "v2" {
					return v2Handler(ctx, job)
				}
				return v1Handler(ctx, job)
			})
			go jqWorker.Start(context.Background())
			app.log.Info("Statement processor started (V2 pipeline: Kimi + OpenAI fallback + OCR)")

			// Reconcile orphaned pending uploads (stuck from a prior crash)
			go app.reconcileOrphanedStatements(context.Background())
		}
	}

	return nil
}

func (app *Application) initializeKYCSyncWorker() error {
	if app.container.KYCSyncJobRepo == nil || app.container.KYCSubmissionRepo == nil {
		return nil
	}

	var sumsubClient kycservice.SumsubAdapter
	if strings.EqualFold(strings.TrimSpace(app.cfg.KYC.Provider), "sumsub") &&
		app.cfg.KYC.APIKey != "" && app.cfg.KYC.APISecret != "" {
		sumsubClient = sumsubadapter.NewClient(sumsubadapter.Config{
			BaseURL:       app.cfg.KYC.BaseURL,
			AppToken:      app.cfg.KYC.APIKey,
			SecretKey:     app.cfg.KYC.APISecret,
			WebhookSecret: app.cfg.KYC.WebhookSecret,
			LevelName:     app.cfg.KYC.LevelName,
			UserAgent:     app.cfg.KYC.UserAgent,
			Timeout:       30 * time.Second,
		}, app.log.Zap())
	}

	var diditClient kycservice.DiditAdapter
	if app.cfg.KYC.DiditAPIKey != "" && app.cfg.KYC.DiditWorkflowID != "" {
		diditClient = diditadapter.NewClient(diditadapter.Config{
			APIKey:        app.cfg.KYC.DiditAPIKey,
			WebhookSecret: app.cfg.KYC.DiditWebhookSecret,
			WorkflowID:    app.cfg.KYC.DiditWorkflowID,
		}, app.log.Zap())
	}

	kycSvc := kycservice.NewService(
		repositories.NewKYCUserRepositoryAdapter(app.container.UserRepo),
		app.container.KYCSubmissionRepo,
		app.container.BridgeAdapter,
		alpacaadapter.NewAdapter(app.container.AlpacaClient, app.container.Logger),
		sumsubClient,
		app.container.SumsubWebhookEventRepo,
		app.container.KYCSyncJobRepo,
		app.cfg.KYC.LevelName,
		app.cfg.Security.EncryptionKey,
		app.log.Zap(),
		diditClient,
	)
	if app.container.NotificationService != nil {
		kycSvc.SetNotifier(app.container.NotificationService)
	}

	app.kycSyncWorker = kyc_sync.NewWorkerWithRetry(
		app.container.KYCSyncJobRepo,
		kycSvc,
		kycSvc,
		app.log.Zap(),
		kyc_sync.DefaultConfig(),
		kycSvc,
	)
	go app.kycSyncWorker.Start(context.Background())
	app.log.Info("KYC sync worker started")

	// Initialize balance reconciliation worker
	app.balanceReconciliationWorker = balance_reconciliation.NewWorker(
		&bridgeWalletBalanceAdapter{adapter: app.container.BridgeAdapter},
		app.container.LedgerService,
		app.container.WalletRepo,
		app.container.LedgerRepo,
		6*time.Hour,
		decimal.NewFromFloat(0.01),
		app.log.Zap(),
	)
	go app.balanceReconciliationWorker.Start(context.Background())
	app.log.Info("Balance reconciliation worker started")

	app.bridgeGovIDRepairWorker = bridge_govid_repair.NewWorker(
		app.container.UserRepo,
		kycSvc,
		app.log.Zap(),
	)
	if diditClient != nil {
		repairCtx, repairCancel := context.WithCancel(context.Background())
		app.bridgeGovIDRepairCancel = repairCancel
		go app.bridgeGovIDRepairWorker.Start(repairCtx)
		app.log.Info("Bridge gov ID repair worker started")
	}

	return nil
}

// initializeWalletProvisioning initializes wallet provisioning workers.
// Wallet creation is now handled by Bridge during onboarding; the worker is a no-op.
func (app *Application) initializeWalletProvisioning() error {
	workerConfig := walletprovisioning.DefaultConfig()
	workerConfig.ChainsToProvision = app.container.WalletService.SupportedChains()

	worker := walletprovisioning.NewWorker(
		app.container.WalletProvisioningJobRepo,
		app.container.AuditService,
		workerConfig,
		app.log.Zap(),
		app.container.WalletService,
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
		app.container.RedisClient.Client(),
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

	// Add gzip compression if enabled
	if app.cfg.Server.EnableGzip {
		router.Use(GzipMiddleware())
	}

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
	metrics.InitBusinessMetrics()
	go app.startMetricsCollection()

	// One-time backfill: populate missing virtual account details from Bridge
	go app.backfillVirtualAccountDetails()

	// Drop legacy constraints on virtual_accounts that block multi-currency VAs
	go app.dropLegacyVirtualAccountConstraints()

	// Hotfix: ensure basic_complete is in onboarding_status CHECK constraints.
	// Migration 140 was a no-op (ALTER TYPE on a TEXT column) and 142 may not
	// run if schema_migrations is dirty. This is idempotent — safe on every boot.
	go app.fixOnboardingStatusConstraint()

	return nil
}

// startMetricsCollection starts background metrics collection
// backfillVirtualAccountDetails fetches missing bank details from Bridge for existing virtual accounts.
// Safe to run on every boot — only touches rows with empty bank_address.
func (app *Application) backfillVirtualAccountDetails() {
	ctx := context.Background()
	if app.container.BridgeClient == nil {
		return
	}

	rows, err := app.container.DB.QueryContext(ctx, `
		SELECT id, bridge_customer_id, bridge_account_id
		FROM virtual_accounts
		WHERE bridge_account_id IS NOT NULL
		  AND (bank_address = '' OR bank_address IS NULL)
	`)
	if err != nil {
		app.log.Error("backfill VA: query failed", "error", err)
		return
	}
	defer rows.Close()

	var updated int
	for rows.Next() {
		var id, customerID, bridgeAccountID string
		if err := rows.Scan(&id, &customerID, &bridgeAccountID); err != nil {
			continue
		}
		va, err := app.container.BridgeClient.GetVirtualAccount(ctx, customerID, bridgeAccountID)
		if err != nil {
			app.log.Warn("backfill VA: bridge fetch failed", "id", id, "error", err)
			continue
		}
		sdi := va.SourceDepositInstructions
		_, err = app.container.DB.ExecContext(ctx, `
			UPDATE virtual_accounts
			SET bank_address = $1, beneficiary_address = $2, payment_rails = $3,
			    bank_name = COALESCE(NULLIF(bank_name, ''), $4),
			    beneficiary_name = COALESCE(NULLIF(beneficiary_name, ''), $5),
			    account_number = COALESCE(NULLIF(account_number, ''), $6),
			    routing_number = COALESCE(NULLIF(routing_number, ''), $7)
			WHERE id = $8
		`, sdi.BankAddress, sdi.BankBeneficiaryAddress, pq.Array(sdi.PaymentRails),
			sdi.BankName, sdi.BankBeneficiaryName, sdi.BankAccountNumber, sdi.BankRoutingNumber, id)
		if err != nil {
			app.log.Warn("backfill VA: update failed", "id", id, "error", err)
			continue
		}
		updated++
	}
	if updated > 0 {
		app.log.Info("backfill VA: completed", "updated", updated)
	}
}

// dropLegacyVirtualAccountConstraints removes old unique constraints that prevent multi-currency virtual accounts.
func (app *Application) dropLegacyVirtualAccountConstraints() {
	stmts := []string{
		`ALTER TABLE virtual_accounts DROP CONSTRAINT IF EXISTS virtual_accounts_due_account_id_key`,
		`ALTER TABLE virtual_accounts DROP CONSTRAINT IF EXISTS virtual_accounts_user_id_alpaca_account_id_key`,
		`ALTER TABLE virtual_accounts DROP CONSTRAINT IF EXISTS virtual_accounts_account_number_key`,
		`ALTER TABLE virtual_accounts ALTER COLUMN alpaca_account_id DROP NOT NULL`,
		`ALTER TABLE virtual_accounts ALTER COLUMN account_number DROP NOT NULL`,
	}
	for _, stmt := range stmts {
		if _, err := app.container.DB.Exec(stmt); err != nil {
			app.log.Warn("Failed to drop legacy VA constraint (may already be gone)", "error", err)
		}
	}
	app.log.Info("Legacy virtual_accounts constraints dropped")
}

// fixOnboardingStatusConstraint ensures basic_complete is allowed in the onboarding_status CHECK constraints.
func (app *Application) fixOnboardingStatusConstraint() {
	stmts := []string{
		`ALTER TABLE users DROP CONSTRAINT IF EXISTS chk_onboarding_status`,
		`ALTER TABLE users ADD CONSTRAINT chk_onboarding_status CHECK (onboarding_status IN ('started', 'basic_complete', 'kyc_pending', 'kyc_approved', 'kyc_rejected', 'wallets_pending', 'completed'))`,
		`ALTER TABLE users DROP CONSTRAINT IF EXISTS users_onboarding_status_check`,
		`ALTER TABLE users ADD CONSTRAINT users_onboarding_status_check CHECK (onboarding_status IN ('started', 'basic_complete', 'kyc_pending', 'kyc_approved', 'kyc_rejected', 'wallets_pending', 'completed'))`,
	}
	for _, stmt := range stmts {
		if _, err := app.container.DB.Exec(stmt); err != nil {
			app.log.Warn("fixOnboardingStatusConstraint failed", "error", err, "stmt", stmt)
		}
	}
	app.log.Info("Onboarding status constraint fixed")
}

func (app *Application) startMetricsCollection() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		ctx := context.Background()

		// Update database connection metrics
		stats := app.container.DB.Stats()
		metrics.DatabaseConnectionsGauge.WithLabelValues("open").Set(float64(stats.OpenConnections))
		metrics.DatabaseConnectionsGauge.WithLabelValues("idle").Set(float64(stats.Idle))
		metrics.DatabaseConnectionsGauge.WithLabelValues("in_use").Set(float64(stats.InUse))

		// Update business gauges from DB
		if metrics.Business != nil {
			var count int
			if err := app.container.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count); err == nil {
				metrics.Business.UsersRegistered.Add(0) // ensure metric exists
				metrics.ActiveUsersGauge.Set(float64(count))
				metrics.Business.ActiveUsers.Set(float64(count))
			}

			var totalBalance float64
			if err := app.container.DB.QueryRowContext(ctx, "SELECT COALESCE(SUM(balance),0) FROM ledger_accounts WHERE account_type IN ('spending_balance','stash_balance')").Scan(&totalBalance); err == nil {
				metrics.Business.TotalBalance.Set(totalBalance)
			}

			var stashBal float64
			if err := app.container.DB.QueryRowContext(ctx, "SELECT COALESCE(SUM(balance),0) FROM ledger_accounts WHERE account_type = 'stash_balance'").Scan(&stashBal); err == nil {
				metrics.Business.StashBalanceTotal.Set(stashBal)
			}

			var spendBal float64
			if err := app.container.DB.QueryRowContext(ctx, "SELECT COALESCE(SUM(balance),0) FROM ledger_accounts WHERE account_type = 'spending_balance'").Scan(&spendBal); err == nil {
				metrics.Business.SpendBalanceTotal.Set(spendBal)
			}

			var avgBal float64
			if err := app.container.DB.QueryRowContext(ctx, "SELECT COALESCE(AVG(balance),0) FROM ledger_accounts WHERE account_type IN ('spending_balance','stash_balance') AND balance > 0").Scan(&avgBal); err == nil {
				metrics.Business.AverageBalance.Set(avgBal)
			}
		}
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

	// Stop deposit allocation recovery worker
	if app.depositAllocationWorker != nil {
		app.log.Info("Stopping deposit allocation recovery worker...")
		app.depositAllocationWorker.Stop()
	}
	if app.container != nil && app.container.BlendDepositRouter != nil {
		app.log.Info("Stopping Blend deposit router...")
		if err := app.container.BlendDepositRouter.Stop(); err != nil {
			app.log.Error("Error stopping Blend deposit router", "error", err)
		}
	}
	if app.pajOfframpRecoveryWorker != nil {
		app.pajOfframpRecoveryWorker.Stop()
	}

	// Stop KYC auto-invest worker
	if app.kycAutoInvestWorker != nil {
		app.log.Info("Stopping KYC auto-invest worker...")
		app.kycAutoInvestWorker.Stop()
	}

	// Stop KYC sync worker
	if app.kycSyncWorker != nil {
		app.log.Info("Stopping KYC sync worker...")
		app.kycSyncWorker.Stop()
	}

	// Stop balance reconciliation worker
	if app.balanceReconciliationWorker != nil {
		app.log.Info("Stopping balance reconciliation worker...")
		app.balanceReconciliationWorker.Stop()
	}

	// Stop bridge gov ID repair worker
	if app.bridgeGovIDRepairCancel != nil {
		app.log.Info("Stopping bridge gov ID repair worker...")
		app.bridgeGovIDRepairCancel()
		app.bridgeGovIDRepairCancel = nil
	}

	// Stop subscription billing worker
	if app.subscriptionBillingWorker != nil {
		app.log.Info("Stopping subscription billing worker...")
		app.subscriptionBillingWorker.Stop()
	}

	// Stop gameplay workers
	if app.streakEvaluatorWorker != nil {
		app.streakEvaluatorWorker.Stop()
	}
	if app.challengeRotatorWorker != nil {
		app.challengeRotatorWorker.Stop()
	}
	if app.achievementCheckerWorker != nil {
		app.achievementCheckerWorker.Stop()
	}
	if app.insightGeneratorWorker != nil {
		app.insightGeneratorWorker.Stop()
	}
	if app.dailyMetricsWorker != nil {
		app.dailyMetricsWorker.Stop()
	}
	if app.depositAutoSweepWorker != nil {
		app.depositAutoSweepWorker.Stop()
	}
	if app.growthMailCancel != nil {
		app.growthMailCancel()
	}
	if app.miriamWorkerCancel != nil {
		app.miriamWorkerCancel()
	}
	if app.growthEngineCancel != nil {
		app.growthEngineCancel()
	}
}

type dailyPulseUserRepoAdapter struct {
	repo *repositories.UserRepository
}

type dailyPulseBriefProvider struct {
	orchestrator *aiservice.Orchestrator
}

func (p *dailyPulseBriefProvider) GetMiriamBrief(ctx context.Context, userID uuid.UUID, country string) (map[string]interface{}, error) {
	result, err := p.orchestrator.ExecuteToolPublic(ctx, userID, infraai.ToolCall{
		ID:   "daily-pulse-miriam-brief",
		Name: aiservice.ToolGetMiriamBrief,
		Arguments: map[string]interface{}{
			"country": country,
		},
	})
	if err != nil {
		return nil, err
	}

	// Normalize typed internal slices into JSON-like maps for the worker package.
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	var normalized map[string]interface{}
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

func (a *dailyPulseUserRepoAdapter) GetAllActiveUsers(ctx context.Context) ([]struct {
	ID      uuid.UUID
	Country string
}, error) {
	users, err := a.repo.GetAllActiveUsers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]struct {
		ID      uuid.UUID
		Country string
	}, 0, len(users))
	for _, user := range users {
		out = append(out, struct {
			ID      uuid.UUID
			Country string
		}{ID: user.ID, Country: user.Country})
	}
	return out, nil
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

func getBoolEnvOrDefault(key string, defaultValue bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return defaultValue
	}

	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return defaultValue
	}
	return parsed
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

// GzipMiddleware returns a gin middleware for gzip compression
func GzipMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !strings.Contains(c.Request.Header.Get("Accept-Encoding"), "gzip") {
			c.Next()
			return
		}

		c.Header("Content-Encoding", "gzip")

		gz := gzip.NewWriter(c.Writer)
		defer gz.Close()

		c.Writer = &gzipWriter{Writer: gz, ResponseWriter: c.Writer}
		c.Next()
	}
}

type gzipWriter struct {
	gin.ResponseWriter
	Writer *gzip.Writer
}

func (g *gzipWriter) Write(data []byte) (int, error) {
	return g.Writer.Write(data)
}

func (g *gzipWriter) WriteString(s string) (int, error) {
	return g.Writer.Write([]byte(s))
}

func (g *gzipWriter) Flush() {
	g.Writer.Flush()
}

// pajOfframpRecoveryNotifierAdapter adapts NotificationService.NotifyWithdrawalCompleted
// (which takes amount + destination as separate args) to the worker's Notifier interface.
type pajOfframpRecoveryNotifierAdapter struct {
	svc interface {
		NotifyWithdrawalCompleted(ctx context.Context, userID uuid.UUID, amount, destinationAddress string) error
	}
}

func (a *pajOfframpRecoveryNotifierAdapter) NotifyWithdrawalCompleted(ctx context.Context, userID uuid.UUID, amount, destination string) error {
	if a.svc == nil {
		return nil
	}
	return a.svc.NotifyWithdrawalCompleted(ctx, userID, amount, destination)
}

// pajOfframpCircleStatusAdapter translates Circle's transaction state into
// the worker's CircleTransferStatus enum without exposing the adapter package
// to the worker (which would pull in the full Circle SDK at the worker layer).
type pajOfframpCircleStatusAdapter struct {
	adapter *circleadapter.Adapter
}

func (a *pajOfframpCircleStatusAdapter) GetCircleTransferStatus(ctx context.Context, circleTxID string) (paj_offramp_recovery.CircleTransferStatus, error) {
	if a.adapter == nil {
		return paj_offramp_recovery.CircleTransferUnknown, nil
	}
	tx, err := a.adapter.GetTransaction(ctx, circleTxID)
	if err != nil {
		return paj_offramp_recovery.CircleTransferUnknown, err
	}
	if tx == nil {
		return paj_offramp_recovery.CircleTransferUnknown, nil
	}
	switch tx.State {
	case circleadapter.TransactionStateComplete:
		return paj_offramp_recovery.CircleTransferComplete, nil
	case circleadapter.TransactionStateFailed,
		circleadapter.TransactionStateCancelled,
		circleadapter.TransactionStateDenied:
		return paj_offramp_recovery.CircleTransferFailed, nil
	default:
		return paj_offramp_recovery.CircleTransferPending, nil
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

// bridgeWalletBalanceAdapter adapts *bridge.Adapter to balance_reconciliation.WalletBalanceClient
type bridgeWalletBalanceAdapter struct {
	adapter *bridgeadapter.Adapter
}

func (a *bridgeWalletBalanceAdapter) GetWalletBalance(ctx context.Context, customerID, walletID string) (string, error) {
	bal, err := a.adapter.GetWalletBalance(ctx, customerID, walletID)
	if err != nil {
		return "0", err
	}
	return bal.GetUSDCAmount(), nil
}

// opportunityUserListerAdapter adapts UserRepository to opportunity_sync.UserLister.
type opportunityUserListerAdapter struct {
	repo *repositories.UserRepository
}

func (a *opportunityUserListerAdapter) GetAllActiveUserIDs(ctx context.Context) ([]uuid.UUID, error) {
	users, err := a.repo.GetAllActiveUsers(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(users))
	for _, u := range users {
		ids = append(ids, u.ID)
	}
	return ids, nil
}

// reconcileOrphanedStatements re-enqueues bank statement uploads that got stuck in "pending"
// statementSupermemoryAdapter bridges the statement_processor.SupermemoryWriter interface
// with the infrastructure Supermemory client.
type statementSupermemoryAdapter struct {
	client *supermemoryclient.Client
}

func (a *statementSupermemoryAdapter) IngestConversation(ctx context.Context, userID string, messages []statement_processor.SupermemoryMsg) error {
	if a == nil || a.client == nil {
		return nil
	}
	msgs := make([]supermemoryclient.Message, len(messages))
	for i, m := range messages {
		msgs[i] = supermemoryclient.Message{Role: m.Role, Content: m.Content}
	}
	return a.client.IngestConversation(ctx, userID, msgs)
}

func (a *statementSupermemoryAdapter) CreateMemories(ctx context.Context, containerTag string, memories []statement_processor.SupermemoryMemory) error {
	if a == nil || a.client == nil {
		return nil
	}
	mems := make([]supermemoryclient.Memory, len(memories))
	for i, m := range memories {
		mems[i] = supermemoryclient.Memory{
			Content:  m.Content,
			Metadata: m.Metadata,
		}
		if m.EventDate != "" {
			mems[i].TemporalContext = &supermemoryclient.TemporalContext{
				EventDate: []string{m.EventDate},
			}
		}
	}
	return a.client.CreateMemories(ctx, containerTag, mems)
}

// statementConversationAdapter bridges the statement_processor.ConversationWriter interface
// with the conversation repository.
type statementConversationAdapter struct {
	repo *repositories.ConversationRepository
}

func (a *statementConversationAdapter) GetOrCreateConversation(ctx context.Context, userID uuid.UUID) (uuid.UUID, error) {
	convs, err := a.repo.ListByUserID(ctx, userID, 1, 0)
	if err != nil {
		return uuid.Nil, err
	}
	if len(convs) > 0 {
		return convs[0].ID, nil
	}
	conv := &entities.AIConversation{
		UserID: userID,
		Title:  "Miriam",
	}
	if err := a.repo.CreateConversation(ctx, conv); err != nil {
		return uuid.Nil, err
	}
	return conv.ID, nil
}

func (a *statementConversationAdapter) CreateMessage(ctx context.Context, msg *entities.AIMessage) error {
	return a.repo.CreateMessage(ctx, msg)
}

// or "processing" status (e.g. after a server crash). Runs once at startup and exits.
func (app *Application) reconcileOrphanedStatements(ctx context.Context) {
	const minAge = 20 * time.Minute
	// Run periodically, not just once at startup
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	// Run immediately on first call, then every 10 minutes
	for {
		orphans, err := app.container.BankStatementRepo.GetPendingOlderThan(ctx, minAge)
		if err != nil {
			app.log.Warnw("failed to reconcile orphaned statements", "error", err)
		} else if len(orphans) > 0 {
			app.log.Infow("reconciling orphaned statement uploads", "count", len(orphans))
			for _, u := range orphans {
		// Atomically reset stuck processing uploads back to pending so AtomicClaim can pick them up.
		// The SQL WHERE status = 'processing' guard ensures we don't race with a worker that already
		// claimed or completed the upload between the fetch and this update.
		reset, err := app.container.BankStatementRepo.ResetToPending(ctx, u.ID)
		if err != nil {
			app.log.Warnw("failed to reset stuck processing upload",
				"upload_id", u.ID.String(),
				"error", err,
			)
			continue
		}
		if u.Status == entities.StatementStatusProcessing && !reset {
			// Upload was processing but ResetToPending didn't match — another worker already
			// completed it. Skip enqueuing a duplicate job.
			continue
		}

		job := &jobqueue.Job{
			ID:       uuid.New().String(),
			Type:     statement_processor.JobType,
			Priority: jobqueue.PriorityNormal,
			Payload: map[string]interface{}{
				"upload_id": u.ID.String(),
				"user_id":   u.UserID.String(),
				"bank_name": u.BankName,
			},
		}
		if err := app.container.JobQueueInstance.Enqueue(ctx, job); err != nil {
			app.log.Warnw("failed to re-enqueue orphaned statement",
				"upload_id", u.ID.String(),
				"error", err,
			)
		}
	}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
