package di

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"
	"github.com/stack-service/stack_service/internal/adapters/alpaca"
	"github.com/stack-service/stack_service/internal/adapters/due"
	"github.com/stack-service/stack_service/internal/domain/entities"
	"github.com/stack-service/stack_service/internal/domain/services"
	"github.com/stack-service/stack_service/internal/domain/services/allocation"
	"github.com/stack-service/stack_service/internal/domain/services/apikey"
	entitysecret "github.com/stack-service/stack_service/internal/domain/services/entity_secret"
	"github.com/stack-service/stack_service/internal/domain/services/funding"
	"github.com/stack-service/stack_service/internal/domain/services/investing"
	"github.com/stack-service/stack_service/internal/domain/services/ledger"
	"github.com/stack-service/stack_service/internal/domain/services/onboarding"
	"github.com/stack-service/stack_service/internal/domain/services/passcode"
	"github.com/stack-service/stack_service/internal/domain/services/reconciliation"
	"github.com/stack-service/stack_service/internal/domain/services/session"
	"github.com/stack-service/stack_service/internal/domain/services/twofa"
	"github.com/stack-service/stack_service/internal/domain/services/wallet"
	"github.com/stack-service/stack_service/internal/infrastructure/adapters"
	"github.com/stack-service/stack_service/internal/infrastructure/cache"
	"github.com/stack-service/stack_service/internal/infrastructure/circle"
	"github.com/stack-service/stack_service/internal/infrastructure/config"
	"github.com/stack-service/stack_service/internal/infrastructure/repositories"
	commonmetrics "github.com/stack-service/stack_service/pkg/common/metrics"
	"github.com/stack-service/stack_service/pkg/logger"
	"go.uber.org/zap"
)

// CircleAdapter adapts circle.Client to funding.CircleAdapter interface
type CircleAdapter struct {
	client *circle.Client
}

func (a *CircleAdapter) GenerateDepositAddress(ctx context.Context, chain entities.Chain, userID uuid.UUID) (string, error) {
	// Convert entities.Chain to entities.WalletChain
	walletChain := entities.WalletChain(chain)
	return a.client.GenerateDepositAddress(ctx, walletChain, userID)
}

func (a *CircleAdapter) ValidateDeposit(ctx context.Context, txHash string, amount decimal.Decimal) (bool, error) {
	// This method doesn't exist in circle.Client, so we'll need to implement it
	// For now, return a placeholder implementation
	return true, nil
}

func (a *CircleAdapter) ConvertToUSD(ctx context.Context, amount decimal.Decimal, token entities.Stablecoin) (decimal.Decimal, error) {
	// This method doesn't exist in circle.Client, so we'll need to implement it
	// For now, return the same amount as placeholder
	return amount, nil
}

func (a *CircleAdapter) GetWalletBalances(ctx context.Context, walletID string, tokenAddress ...string) (*entities.CircleWalletBalancesResponse, error) {
	return a.client.GetWalletBalances(ctx, walletID, tokenAddress...)
}

// AlpacaFundingAdapter adapts alpaca.FundingAdapter to funding.AlpacaAdapter interface
type AlpacaFundingAdapter struct {
	adapter *alpaca.FundingAdapter
	client  *alpaca.Client
}

func (a *AlpacaFundingAdapter) GetAccount(ctx context.Context, accountID string) (*entities.AlpacaAccountResponse, error) {
	return a.client.GetAccount(ctx, accountID)
}

func (a *AlpacaFundingAdapter) InitiateInstantFunding(ctx context.Context, req *entities.AlpacaInstantFundingRequest) (*entities.AlpacaInstantFundingResponse, error) {
	return a.adapter.InitiateInstantFunding(ctx, req)
}

func (a *AlpacaFundingAdapter) GetInstantFundingStatus(ctx context.Context, transferID string) (*entities.AlpacaInstantFundingResponse, error) {
	return a.adapter.GetInstantFundingStatus(ctx, transferID)
}

func (a *AlpacaFundingAdapter) GetAccountBalance(ctx context.Context, accountID string) (*entities.AlpacaAccountResponse, error) {
	return a.adapter.GetAccountBalance(ctx, accountID)
}

func (a *AlpacaFundingAdapter) CreateJournal(ctx context.Context, req *entities.AlpacaJournalRequest) (*entities.AlpacaJournalResponse, error) {
	return a.adapter.CreateJournal(ctx, req)
}

// Container holds all application dependencies
type Container struct {
	Config *config.Config
	DB     *sql.DB
	Logger *logger.Logger
	ZapLog *zap.Logger

	// Repositories
	UserRepo                  *repositories.UserRepository
	OnboardingFlowRepo        *repositories.OnboardingFlowRepository
	KYCSubmissionRepo         *repositories.KYCSubmissionRepository
	WalletRepo                *repositories.WalletRepository
	WalletSetRepo             *repositories.WalletSetRepository
	WalletProvisioningJobRepo *repositories.WalletProvisioningJobRepository
	DepositRepo               *repositories.DepositRepository
	WithdrawalRepo            *repositories.WithdrawalRepository
	ConversionRepo            *repositories.ConversionRepository
	BalanceRepo               *repositories.BalanceRepository
	FundingEventJobRepo       *repositories.FundingEventJobRepository
	LedgerRepo                *repositories.LedgerRepository
	ReconciliationRepo        repositories.ReconciliationRepository

	// External Services
	CircleClient  *circle.Client
	AlpacaClient  *alpaca.Client
	AlpacaService *alpaca.Service
	KYCProvider   *adapters.KYCProvider
	EmailService  *adapters.EmailService
	SMSService    *adapters.SMSService
	AuditService  *adapters.AuditService
	RedisClient   cache.RedisClient

	// Domain Services
	OnboardingService       *onboarding.Service
	OnboardingJobService    *services.OnboardingJobService
	VerificationService     services.VerificationService
	PasscodeService         *passcode.Service
	SessionService          *session.Service
	TwoFAService            *twofa.Service
	APIKeyService           *apikey.Service
	WalletService           *wallet.Service
	FundingService          *funding.Service
	InvestingService        *investing.Service
	DueService              *services.DueService
	BalanceService          *services.BalanceService
	EntitySecretService     *entitysecret.Service
	LedgerService           *ledger.Service
	ReconciliationService   *reconciliation.Service
	ReconciliationScheduler *reconciliation.Scheduler
	AllocationService       *allocation.Service
	NotificationService     *services.NotificationService

	// Additional Repositories
	OnboardingJobRepo *repositories.OnboardingJobRepository

	// Workers
	WalletProvisioningScheduler interface{} // Type interface{} to avoid circular dependency, will be set at runtime
	FundingWebhookManager       interface{} // Type interface{} to avoid circular dependency, will be set at runtime

	// Cache & Queue
	CacheInvalidator *cache.CacheInvalidator
	JobQueue         interface{} // Job queue for background processing
	JobScheduler     interface{} // Job scheduler for cron jobs
}

// NewContainer creates a new dependency injection container
func NewContainer(cfg *config.Config, db *sql.DB, log *logger.Logger) (*Container, error) {
	zapLog := log.Zap()

	// Wrap sql.DB with sqlx for repositories that need it
	sqlxDB := sqlx.NewDb(db, "postgres")

	// Initialize repositories
	userRepo := repositories.NewUserRepository(db, zapLog)
	onboardingFlowRepo := repositories.NewOnboardingFlowRepository(db, zapLog)
	kycSubmissionRepo := repositories.NewKYCSubmissionRepository(db, zapLog)
	walletRepo := repositories.NewWalletRepository(db, zapLog)
	walletSetRepo := repositories.NewWalletSetRepository(db, zapLog)
	walletProvisioningJobRepo := repositories.NewWalletProvisioningJobRepository(db, zapLog)
	depositRepo := repositories.NewDepositRepository(sqlxDB)
	withdrawalRepo := repositories.NewWithdrawalRepository(sqlxDB)
	conversionRepo := repositories.NewConversionRepository(sqlxDB)
	balanceRepo := repositories.NewBalanceRepository(db, zapLog)
	fundingEventJobRepo := repositories.NewFundingEventJobRepository(db, log)
	ledgerRepo := repositories.NewLedgerRepository(sqlxDB)
	reconciliationRepo := repositories.NewPostgresReconciliationRepository(db)
	onboardingJobRepo := repositories.NewOnboardingJobRepository(db, zapLog)

	// Initialize external services
	circleConfig := circle.Config{
		APIKey:                 cfg.Circle.APIKey,
		Environment:            cfg.Circle.Environment,
		BaseURL:                cfg.Circle.BaseURL,
		EntitySecretCiphertext: cfg.Circle.EntitySecretCiphertext,
	}
	circleClient := circle.NewClient(circleConfig, zapLog)

	// Initialize Alpaca service
	alpacaConfig := alpaca.Config{
		ClientID:    cfg.Alpaca.ClientID,
		SecretKey:   cfg.Alpaca.SecretKey,
		BaseURL:     cfg.Alpaca.BaseURL,
		DataBaseURL: cfg.Alpaca.DataBaseURL,
		Environment: cfg.Alpaca.Environment,
		Timeout:     time.Duration(cfg.Alpaca.Timeout) * time.Second,
	}
	alpacaClient := alpaca.NewClient(alpacaConfig, zapLog)
	alpacaService := alpaca.NewService(alpacaClient, zapLog)

	// Initialize KYC provider with full configuration
	kycProviderConfig := adapters.KYCProviderConfig{
		Provider:    cfg.KYC.Provider,
		APIKey:      cfg.KYC.APIKey,
		APISecret:   cfg.KYC.APISecret,
		BaseURL:     cfg.KYC.BaseURL,
		Environment: cfg.KYC.Environment,
		CallbackURL: cfg.KYC.CallbackURL,
		UserAgent:   cfg.KYC.UserAgent,
		LevelName:   cfg.KYC.LevelName,
	}
	var kycProvider *adapters.KYCProvider
	var err error
	if strings.TrimSpace(cfg.KYC.Provider) != "" {
		kycProvider, err = adapters.NewKYCProvider(zapLog, kycProviderConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize KYC provider: %w", err)
		}
	} else {
		zapLog.Warn("KYC provider not configured; KYC features disabled")
	}

	// Initialize email service with full configuration
	emailServiceConfig := adapters.EmailServiceConfig{
		Provider:    cfg.Email.Provider,
		APIKey:      cfg.Email.APIKey,
		FromEmail:   cfg.Email.FromEmail,
		FromName:    cfg.Email.FromName,
		Environment: cfg.Email.Environment,
		BaseURL:     cfg.Email.BaseURL,
		ReplyTo:     cfg.Email.ReplyTo,
	}
	var emailService *adapters.EmailService
	if strings.TrimSpace(cfg.Email.Provider) != "" {
		emailService, err = adapters.NewEmailService(zapLog, emailServiceConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize email service: %w", err)
		}
	} else {
		zapLog.Warn("Email provider not configured; email notifications disabled")
	}

	// Initialize SMS service
	var smsService *adapters.SMSService
	if strings.TrimSpace(cfg.SMS.Provider) != "" {
		smsService, err = adapters.NewSMSService(zapLog, adapters.SMSConfig{
			Provider:    cfg.SMS.Provider,
			APIKey:      cfg.SMS.APIKey,
			APISecret:   cfg.SMS.APISecret,
			FromNumber:  cfg.SMS.FromNumber,
			Environment: cfg.SMS.Environment,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to initialize SMS service: %w", err)
		}
	} else {
		zapLog.Warn("SMS provider not configured; SMS notifications disabled")
	}

	// Initialize Redis client
	redisClient, err := cache.NewRedisClient(&cfg.Redis, zapLog)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Redis client: %w", err)
	}

	auditService := adapters.NewAuditService(db, zapLog)

	// Initialize cache invalidator
	cacheInvalidator := cache.NewCacheInvalidator(redisClient, zapLog, cache.InvalidateImmediate)

	// Initialize entity secret service
	entitySecretService := entitysecret.NewService(zapLog)

	container := &Container{
		Config: cfg,
		DB:     db,
		Logger: log,
		ZapLog: zapLog,

		// Repositories
		UserRepo:                  userRepo,
		OnboardingFlowRepo:        onboardingFlowRepo,
		KYCSubmissionRepo:         kycSubmissionRepo,
		WalletRepo:                walletRepo,
		WalletSetRepo:             walletSetRepo,
		WalletProvisioningJobRepo: walletProvisioningJobRepo,
		DepositRepo:               depositRepo,
		WithdrawalRepo:            withdrawalRepo,
		ConversionRepo:            conversionRepo,
		BalanceRepo:               balanceRepo,
		FundingEventJobRepo:       fundingEventJobRepo,
		LedgerRepo:                ledgerRepo,
		ReconciliationRepo:        reconciliationRepo,
		OnboardingJobRepo:         onboardingJobRepo,

		// External Services
		CircleClient:  circleClient,
		AlpacaClient:  alpacaClient,
		AlpacaService: alpacaService,
		KYCProvider:   kycProvider,
		EmailService:  emailService,
		SMSService:    smsService,
		AuditService:  auditService,
		RedisClient:   redisClient,

		// Entity Secret Service
		EntitySecretService: entitySecretService,

		// Cache & Queue
		CacheInvalidator: cacheInvalidator,
	}

	// Initialize domain services with their dependencies
	if err := container.initializeDomainServices(); err != nil {
		return nil, fmt.Errorf("failed to initialize domain services: %w", err)
	}

	// Initialize verification and onboarding job services
	container.VerificationService = services.NewVerificationService(
		container.RedisClient,
		container.EmailService,
		container.SMSService,
		container.ZapLog,
		container.Config,
	)

	container.OnboardingJobService = services.NewOnboardingJobService(container.OnboardingJobRepo, container.ZapLog)

	return container, nil
}

// initializeDomainServices initializes all domain services with their dependencies
func (c *Container) initializeDomainServices() error {
	defaultWalletChains := convertWalletChains(c.Config.Circle.SupportedChains, c.ZapLog)
	walletServiceConfig := wallet.Config{
		WalletSetNamePrefix: c.Config.Circle.DefaultWalletSetName,
		SupportedChains:     defaultWalletChains,
		DefaultWalletSetID:  c.Config.Circle.DefaultWalletSetID,
	}

	// Initialize wallet service first (no dependencies on other domain services)
	c.WalletService = wallet.NewService(
		c.WalletRepo,
		c.WalletSetRepo,
		c.WalletProvisioningJobRepo,
		c.CircleClient,
		c.AuditService,
		c.EntitySecretService,
		nil, // onboardingService - will be set after onboarding service is created
		c.ZapLog,
		walletServiceConfig,
	)

	// Initialize Due client and adapter
	dueClient := due.NewClient(due.Config{
		APIKey:    c.Config.Due.APIKey,
		AccountID: c.Config.Due.AccountID,
		BaseURL:   c.Config.Due.BaseURL,
		Timeout:   30 * time.Second,
	}, c.Logger)
	dueAdapter := due.NewAdapter(dueClient, c.Logger)

	// Initialize Alpaca adapter
	alpacaAdapter := alpaca.NewAdapter(c.AlpacaClient, c.Logger)

	// Initialize onboarding service (depends on wallet service)
	c.OnboardingService = onboarding.NewService(
		c.UserRepo,
		c.OnboardingFlowRepo,
		c.KYCSubmissionRepo,
		c.WalletService, // Domain service dependency
		c.KYCProvider,
		c.EmailService,
		c.AuditService,
		dueAdapter,
		alpacaAdapter,
		c.ZapLog,
		append([]entities.WalletChain(nil), walletServiceConfig.SupportedChains...),
	)

	// Inject onboarding service back into wallet service to complete circular dependency
	c.WalletService.SetOnboardingService(c.OnboardingService)

	// Initialize passcode service for transaction security
	c.PasscodeService = passcode.NewService(
		c.UserRepo,
		c.RedisClient,
		c.ZapLog,
	)

	// Initialize security services
	c.SessionService = session.NewService(c.DB, c.ZapLog)
	c.TwoFAService = twofa.NewService(c.DB, c.ZapLog, c.Config.Security.EncryptionKey)
	c.APIKeyService = apikey.NewService(c.DB, c.ZapLog)

	// Initialize simple wallet repository for funding service
	simpleWalletRepo := repositories.NewSimpleWalletRepository(c.DB, c.Logger)

	// Initialize virtual account repository
	sqlxDB := sqlx.NewDb(c.DB, "postgres")
	virtualAccountRepo := repositories.NewVirtualAccountRepository(sqlxDB)

	// Initialize Due service with deposit and balance repositories
	c.DueService = services.NewDueService(dueClient, c.DepositRepo, c.BalanceRepo, c.Logger)

	// Initialize Alpaca funding adapter
	alpacaFundingAdapter := alpaca.NewFundingAdapter(c.AlpacaClient, c.ZapLog)

	// Initialize ledger service
	c.LedgerService = ledger.NewService(c.LedgerRepo, sqlxDB, c.Logger)

	// Initialize standalone Balance service with Alpaca adapter
	alpacaBalanceAdapter := &AlpacaFundingAdapter{adapter: alpacaFundingAdapter, client: c.AlpacaClient}
	c.BalanceService = services.NewBalanceService(c.BalanceRepo, alpacaBalanceAdapter, c.Logger)

	// Initialize funding service with dependencies
	circleAdapter := &CircleAdapter{client: c.CircleClient}
	c.FundingService = funding.NewService(
		c.DepositRepo,
		c.BalanceRepo,
		simpleWalletRepo,
		c.WalletRepo,
		virtualAccountRepo,
		circleAdapter,
		dueAdapter,
		&AlpacaFundingAdapter{adapter: alpacaFundingAdapter, client: c.AlpacaClient},
		c.Logger,
	)

	// Initialize allocation service
	allocationRepo := repositories.NewAllocationRepository(sqlxDB, c.Logger)
	c.AllocationService = allocation.NewService(
		allocationRepo,
		c.LedgerService,
		c.Logger,
	)

	// Initialize investing service with repositories
	basketRepo := repositories.NewBasketRepository(c.DB, c.ZapLog)
	orderRepo := repositories.NewOrderRepository(c.DB, c.ZapLog)
	positionRepo := repositories.NewPositionRepository(c.DB, c.ZapLog)

	// Initialize brokerage adapter with Alpaca service
	brokerageAdapter := adapters.NewBrokerageAdapter(
		c.AlpacaClient,
		c.ZapLog,
	)

	// Initialize notification service
	c.NotificationService = services.NewNotificationService(c.ZapLog)

	c.InvestingService = investing.NewService(
		basketRepo,
		orderRepo,
		positionRepo,
		c.BalanceRepo,
		brokerageAdapter,
		c.WalletRepo,
		c.CircleClient,
		c.AllocationService,
		c.NotificationService,
		c.Logger,
	)

	// Initialize reconciliation service
	if err := c.initializeReconciliationService(); err != nil {
		return fmt.Errorf("failed to initialize reconciliation service: %w", err)
	}

	return nil
}

// GetOnboardingService returns the onboarding service
func (c *Container) GetOnboardingService() *onboarding.Service {
	return c.OnboardingService
}

// GetPasscodeService returns the passcode service
func (c *Container) GetPasscodeService() *passcode.Service {
	return c.PasscodeService
}

// GetSessionService returns the session service
func (c *Container) GetSessionService() *session.Service {
	return c.SessionService
}

// GetTwoFAService returns the 2FA service
func (c *Container) GetTwoFAService() *twofa.Service {
	return c.TwoFAService
}

// GetAPIKeyService returns the API key service
func (c *Container) GetAPIKeyService() *apikey.Service {
	return c.APIKeyService
}

// GetWalletService returns the wallet service
func (c *Container) GetWalletService() *wallet.Service {
	return c.WalletService
}

// GetFundingService returns the funding service
func (c *Container) GetFundingService() *funding.Service {
	return c.FundingService
}

// GetInvestingService returns the investing service
func (c *Container) GetInvestingService() *investing.Service {
	return c.InvestingService
}

// GetDueService returns the Due service
func (c *Container) GetDueService() *services.DueService {
	return c.DueService
}

// GetBalanceService returns the Balance service
func (c *Container) GetBalanceService() *services.BalanceService {
	return c.BalanceService
}

// GetLedgerService returns the Ledger service
func (c *Container) GetLedgerService() *ledger.Service {
	return c.LedgerService
}

// GetVerificationService returns the verification service
func (c *Container) GetVerificationService() services.VerificationService {
	return c.VerificationService
}

// GetOnboardingJobService returns the onboarding job service
func (c *Container) GetOnboardingJobService() *services.OnboardingJobService {
	return c.OnboardingJobService
}

// GetAllocationService returns the allocation service
func (c *Container) GetAllocationService() *allocation.Service {
	return c.AllocationService
}

// initializeReconciliationService initializes the reconciliation service and scheduler
func (c *Container) initializeReconciliationService() error {
	// Initialize metrics service (placeholder - extend pkg/metrics/reconciliation_metrics.go)
	metricsService := &reconciliationMetricsService{}

	// Create reconciliation service config
	reconciliationConfig := &reconciliation.Config{
		AutoCorrectLowSeverity: true,
		ToleranceCircle:        decimal.NewFromFloat(10.0),
		ToleranceAlpaca:        decimal.NewFromFloat(100.0),
		EnableAlerting:         true,
		AlertWebhookURL:        c.Config.Reconciliation.AlertWebhookURL,
	}

	// Initialize reconciliation service with all dependencies
	c.ReconciliationService = reconciliation.NewService(
		c.ReconciliationRepo,
		c.LedgerRepo,
		c.DepositRepo,
		c.WithdrawalRepo,
		c.ConversionRepo,
		c.LedgerService,
		&circleClientAdapter{
			client:     c.CircleClient,
			walletRepo: c.WalletRepo,
		},
		&alpacaClientAdapter{
			client:  c.AlpacaClient,
			service: c.AlpacaService,
			db:      c.DB,
		},
		c.Logger,
		metricsService,
		reconciliationConfig,
	)

	// Initialize reconciliation scheduler
	schedulerConfig := &reconciliation.SchedulerConfig{
		HourlyInterval: 1 * time.Hour,
		DailyInterval:  24 * time.Hour,
	}

	c.ReconciliationScheduler = reconciliation.NewScheduler(
		c.ReconciliationService,
		c.Logger,
		schedulerConfig,
	)

	return nil
}

// Adapters for reconciliation service
type circleClientAdapter struct {
	client     *circle.Client
	walletRepo *repositories.WalletRepository
}

func (a *circleClientAdapter) GetTotalUSDCBalance(ctx context.Context) (decimal.Decimal, error) {
	// Query all active wallets from the database
	filters := repositories.WalletListFilters{
		Status: (*entities.WalletStatus)(ptrOf(entities.WalletStatusLive)),
		Limit:  10000, // High limit to get all wallets
		Offset: 0,
	}

	wallets, _, err := a.walletRepo.ListWithFilters(ctx, filters)
	if err != nil {
		return decimal.Zero, fmt.Errorf("failed to list wallets: %w", err)
	}

	// Aggregate USDC balances from all wallets
	totalBalance := decimal.Zero
	for _, wallet := range wallets {
		if wallet.CircleWalletID == "" {
			continue // Skip wallets without Circle wallet ID
		}

		// Get balance for this wallet
		balanceResp, err := a.client.GetWalletBalances(ctx, wallet.CircleWalletID)
		if err != nil {
			// Log error but continue with other wallets
			continue
		}

		// Parse USDC balance
		usdcBalanceStr := balanceResp.GetUSDCBalance()
		if usdcBalanceStr != "0" {
			usdcBalance, err := decimal.NewFromString(usdcBalanceStr)
			if err == nil {
				totalBalance = totalBalance.Add(usdcBalance)
			}
		}
	}

	return totalBalance, nil
}

type alpacaClientAdapter struct {
	client  *alpaca.Client
	service *alpaca.Service
	db      *sql.DB
}

func (a *alpacaClientAdapter) GetTotalBuyingPower(ctx context.Context) (decimal.Decimal, error) {
	// Query all users from database who have Alpaca accounts
	query := `
		SELECT alpaca_account_id 
		FROM users 
		WHERE alpaca_account_id IS NOT NULL AND alpaca_account_id != '' AND is_active = true
	`

	rows, err := a.db.QueryContext(ctx, query)
	if err != nil {
		return decimal.Zero, fmt.Errorf("failed to query users with Alpaca accounts: %w", err)
	}
	defer rows.Close()

	var accountIDs []string
	for rows.Next() {
		var accountID string
		if err := rows.Scan(&accountID); err != nil {
			continue
		}
		accountIDs = append(accountIDs, accountID)
	}

	// Aggregate buying power from all accounts
	totalBuyingPower := decimal.Zero
	for _, accountID := range accountIDs {
		account, err := a.service.GetAccount(ctx, accountID)
		if err != nil {
			// Log error but continue with other accounts
			continue
		}

		// Add buying power (already decimal.Decimal)
		if !account.BuyingPower.IsZero() {
			totalBuyingPower = totalBuyingPower.Add(account.BuyingPower)
		}
	}

	return totalBuyingPower, nil
}

// Real metrics service using Prometheus metrics from pkg/common/metrics
type reconciliationMetricsService struct{}

func (m *reconciliationMetricsService) RecordReconciliationRun(runType string) {
	// Increment run counter
	commonmetrics.ReconciliationRunsTotal.WithLabelValues(runType, "started").Inc()
	commonmetrics.ReconciliationRunsInProgress.WithLabelValues(runType).Inc()
}

func (m *reconciliationMetricsService) RecordReconciliationCompleted(runType string, totalChecks, passedChecks, failedChecks, exceptionsCount int) {
	// Decrement in-progress counter
	commonmetrics.ReconciliationRunsInProgress.WithLabelValues(runType).Dec()
	// Increment completed counter
	commonmetrics.ReconciliationRunsTotal.WithLabelValues(runType, "completed").Inc()
}

func (m *reconciliationMetricsService) RecordCheckResult(checkType string, passed bool, duration time.Duration) {
	// Record check execution
	commonmetrics.ReconciliationChecksTotal.WithLabelValues(checkType).Inc()
	commonmetrics.ReconciliationCheckDuration.WithLabelValues(checkType).Observe(duration.Seconds())

	if passed {
		commonmetrics.ReconciliationChecksPassed.WithLabelValues(checkType).Inc()
	} else {
		commonmetrics.ReconciliationChecksFailed.WithLabelValues(checkType).Inc()
	}
}

func (m *reconciliationMetricsService) RecordExceptionAutoCorrected(checkType string) {
	// Record auto-corrected exception
	commonmetrics.ReconciliationExceptionsAutoCorrected.WithLabelValues(checkType).Inc()
}

func (m *reconciliationMetricsService) RecordDiscrepancyAmount(checkType string, amount decimal.Decimal) {
	// Record discrepancy amount
	amountFloat, _ := amount.Float64()
	commonmetrics.ReconciliationDiscrepancyAmount.WithLabelValues(checkType, "USD").Set(amountFloat)
}

func (m *reconciliationMetricsService) RecordReconciliationAlert(checkType, severity string) {
	// Record alert sent
	commonmetrics.ReconciliationAlertsTotal.WithLabelValues(checkType, severity).Inc()
}

// Helper function to create pointer to value
func ptrOf[T any](v T) *T {
	return &v
}

func convertWalletChains(raw []string, logger *zap.Logger) []entities.WalletChain {
	if len(raw) == 0 {
		logger.Warn("circle.supported_chains not configured; defaulting to SOL-DEVNET")
		return []entities.WalletChain{
			entities.ChainSOLDevnet,
		}
	}

	normalized := make([]entities.WalletChain, 0, len(raw))
	seen := make(map[entities.WalletChain]struct{})

	for _, entry := range raw {
		chain := entities.WalletChain(strings.TrimSpace(strings.ToUpper(entry)))
		if chain == "" {
			continue
		}
		if !chain.IsValid() {
			logger.Warn("Ignoring unsupported wallet chain from configuration", zap.String("chain", string(chain)))
			continue
		}
		if _, ok := seen[chain]; ok {
			continue
		}
		seen[chain] = struct{}{}
		normalized = append(normalized, chain)
	}

	if len(normalized) == 0 {
		logger.Warn("circle.supported_chains contained no valid entries; defaulting to SOL-DEVNET")
		return []entities.WalletChain{
			entities.ChainSOLDevnet,
		}
	}

	return normalized
}
