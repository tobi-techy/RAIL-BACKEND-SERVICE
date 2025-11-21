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
	"github.com/stack-service/stack_service/internal/domain/entities"
	domainrepos "github.com/stack-service/stack_service/internal/domain/repositories"
	"github.com/stack-service/stack_service/internal/domain/services"
	entitysecret "github.com/stack-service/stack_service/internal/domain/services/entity_secret"
	"github.com/stack-service/stack_service/internal/domain/services/funding"
	"github.com/stack-service/stack_service/internal/domain/services/investing"
	"github.com/stack-service/stack_service/internal/domain/services/onboarding"
	"github.com/stack-service/stack_service/internal/domain/services/passcode"
	"github.com/stack-service/stack_service/internal/domain/services/session"
	"github.com/stack-service/stack_service/internal/domain/services/twofa"
	"github.com/stack-service/stack_service/internal/domain/services/apikey"
	"github.com/stack-service/stack_service/internal/domain/services/wallet"
	"github.com/stack-service/stack_service/internal/adapters/alpaca"
	"github.com/stack-service/stack_service/internal/adapters/due"
	"github.com/stack-service/stack_service/internal/infrastructure/adapters"
	"github.com/stack-service/stack_service/internal/infrastructure/cache"
	"github.com/stack-service/stack_service/internal/infrastructure/circle"
	"github.com/stack-service/stack_service/internal/infrastructure/config"
	"github.com/stack-service/stack_service/internal/infrastructure/repositories"
	"github.com/stack-service/stack_service/internal/infrastructure/zerog"
	"github.com/stack-service/stack_service/pkg/logger"
	"go.uber.org/zap"
)

// AISummariesRepositoryAdapter adapts the domain repository to the service interface
type AISummariesRepositoryAdapter struct {
	repo domainrepos.AISummaryRepository
}

// toRepoAISummary converts service AISummary to repository AISummary
func toRepoAISummary(s *services.AISummary) *domainrepos.AISummary {
	return &domainrepos.AISummary{
		ID:          s.ID,
		UserID:      s.UserID,
		WeekStart:   s.WeekStart,
		SummaryMD:   s.SummaryMD,
		ArtifactURI: s.ArtifactURI,
		CreatedAt:   s.CreatedAt,
	}
}

// toServiceAISummary converts repository AISummary to service AISummary
func toServiceAISummary(r *domainrepos.AISummary) *services.AISummary {
	return &services.AISummary{
		ID:          r.ID,
		UserID:      r.UserID,
		WeekStart:   r.WeekStart,
		SummaryMD:   r.SummaryMD,
		ArtifactURI: r.ArtifactURI,
		CreatedAt:   r.CreatedAt,
	}
}

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

func (a *AISummariesRepositoryAdapter) Create(ctx context.Context, summary *services.AISummary) error {
	return a.repo.Create(ctx, toRepoAISummary(summary))
}

func (a *AISummariesRepositoryAdapter) GetLatestByUserID(ctx context.Context, userID uuid.UUID) (*services.AISummary, error) {
	repoSummary, err := a.repo.GetLatestByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	return toServiceAISummary(repoSummary), nil
}

func (a *AISummariesRepositoryAdapter) GetByUserAndWeek(ctx context.Context, userID uuid.UUID, weekStart time.Time) (*services.AISummary, error) {
	repoSummary, err := a.repo.GetByUserAndWeek(ctx, userID, weekStart)
	if err != nil {
		return nil, err
	}
	return toServiceAISummary(repoSummary), nil
}

func (a *AISummariesRepositoryAdapter) Update(ctx context.Context, summary *services.AISummary) error {
	return a.repo.Update(ctx, toRepoAISummary(summary))
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
	BalanceRepo               *repositories.BalanceRepository
	FundingEventJobRepo       *repositories.FundingEventJobRepository

	// External Services
	CircleClient  *circle.Client
	AlpacaClient  *alpaca.Client
	AlpacaService *alpaca.Service
	KYCProvider   *adapters.KYCProvider
	EmailService *adapters.EmailService
	SMSService   *adapters.SMSService
	AuditService *adapters.AuditService
	RedisClient  cache.RedisClient

	// Domain Services
	OnboardingService    *onboarding.Service
	OnboardingJobService *services.OnboardingJobService
	VerificationService  services.VerificationService
	PasscodeService      *passcode.Service
	SessionService       *session.Service
	TwoFAService         *twofa.Service
	APIKeyService        *apikey.Service
	WalletService        *wallet.Service
	FundingService       *funding.Service
	InvestingService     *investing.Service
	AICfoService         *services.AICfoService
	DueService           *services.DueService
	BalanceService       *services.BalanceService
	EntitySecretService  *entitysecret.Service

	// ZeroG Services
	InferenceGateway *zerog.InferenceGateway
	StorageClient    *zerog.StorageClient
	NamespaceManager *zerog.NamespaceManager

	// Additional Repositories for AI-CFO
	AISummariesRepo   domainrepos.AISummaryRepository
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
	balanceRepo := repositories.NewBalanceRepository(db, zapLog)
	fundingEventJobRepo := repositories.NewFundingEventJobRepository(db, log)
	aiSummariesRepo := repositories.NewAISummaryRepository(db, zapLog)
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

	// Initialize ZeroG services
	storageClient, err := zerog.NewStorageClient(&cfg.ZeroG.Storage, zapLog)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize ZeroG storage client: %w", err)
	}

	inferenceGateway, err := zerog.NewInferenceGateway(&cfg.ZeroG.Compute, storageClient, zapLog)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize ZeroG inference gateway: %w", err)
	}

	namespaceManager := zerog.NewNamespaceManager(storageClient, &cfg.ZeroG.Storage.Namespaces, zapLog)

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
		BalanceRepo:               balanceRepo,
		FundingEventJobRepo:       fundingEventJobRepo,
		AISummariesRepo:           aiSummariesRepo,
		OnboardingJobRepo:         onboardingJobRepo,

		// External Services
		CircleClient:  circleClient,
		AlpacaClient:  alpacaClient,
		AlpacaService: alpacaService,
		KYCProvider:   kycProvider,
		EmailService: emailService,
		SMSService:   smsService,
		AuditService: auditService,
		RedisClient:  redisClient,

		// ZeroG Services
		InferenceGateway: inferenceGateway,
		StorageClient:    storageClient,
		NamespaceManager: namespaceManager,

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

	// Initialize investing service with repositories
	basketRepo := repositories.NewBasketRepository(c.DB, c.ZapLog)
	orderRepo := repositories.NewOrderRepository(c.DB, c.ZapLog)
	positionRepo := repositories.NewPositionRepository(c.DB, c.ZapLog)

	// Initialize brokerage adapter with Alpaca service
	brokerageAdapter := adapters.NewBrokerageAdapter(
		c.AlpacaClient,
		c.ZapLog,
	)

	c.InvestingService = investing.NewService(
		basketRepo,
		orderRepo,
		positionRepo,
		c.BalanceRepo,
		brokerageAdapter,
		c.WalletRepo,
		c.CircleClient,
		c.Logger,
	)

	// Initialize notification service for AI-CFO
	notificationService := services.NewNotificationService(c.ZapLog)

	// Create repository adapter for AI-CFO service
	aiSummariesAdapter := &AISummariesRepositoryAdapter{repo: c.AISummariesRepo}

	// Initialize AI-CFO service
	aicfoService, err := services.NewAICfoService(
		c.InferenceGateway,
		c.StorageClient,
		c.NamespaceManager,
		notificationService,
		nil, // portfolioRepo - TODO: implement when available
		nil, // positionsRepo - TODO: implement when available
		nil, // balanceRepo - TODO: implement GetUserBalance method
		aiSummariesAdapter,
		nil, // userRepo - TODO: implement GetUserPreferences method
		c.ZapLog,
	)
	if err != nil {
		return fmt.Errorf("failed to initialize AI-CFO service: %w", err)
	}
	c.AICfoService = aicfoService

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

// GetAICfoService returns the AI-CFO service
func (c *Container) GetAICfoService() *services.AICfoService {
	return c.AICfoService
}

// GetDueService returns the Due service
func (c *Container) GetDueService() *services.DueService {
	return c.DueService
}

// GetBalanceService returns the Balance service
func (c *Container) GetBalanceService() *services.BalanceService {
	return c.BalanceService
}

// GetInferenceGateway returns the ZeroG inference gateway
func (c *Container) GetInferenceGateway() *zerog.InferenceGateway {
	return c.InferenceGateway
}

// GetStorageClient returns the ZeroG storage client
func (c *Container) GetStorageClient() *zerog.StorageClient {
	return c.StorageClient
}

// GetNamespaceManager returns the ZeroG namespace manager
func (c *Container) GetNamespaceManager() *zerog.NamespaceManager {
	return c.NamespaceManager
}

// GetVerificationService returns the verification service
func (c *Container) GetVerificationService() services.VerificationService {
	return c.VerificationService
}

// GetOnboardingJobService returns the onboarding job service
func (c *Container) GetOnboardingJobService() *services.OnboardingJobService {
	return c.OnboardingJobService
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
