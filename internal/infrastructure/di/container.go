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
	"github.com/rail-service/rail_service/internal/adapters/alpaca"
	"github.com/rail-service/rail_service/internal/adapters/bridge"
	"github.com/rail-service/rail_service/internal/api/handlers"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services"
	aiservice "github.com/rail-service/rail_service/internal/domain/services/ai"
	alpacaservice "github.com/rail-service/rail_service/internal/domain/services/alpaca"
	"github.com/rail-service/rail_service/internal/domain/services/allocation"
	"github.com/rail-service/rail_service/internal/domain/services/autoinvest"
	analyticsservice "github.com/rail-service/rail_service/internal/domain/services/analytics"
	"github.com/rail-service/rail_service/internal/domain/services/apikey"
	"github.com/rail-service/rail_service/internal/domain/services/audit"
	entitysecret "github.com/rail-service/rail_service/internal/domain/services/entity_secret"
	"github.com/rail-service/rail_service/internal/domain/services/funding"
	"github.com/rail-service/rail_service/internal/domain/services/integration"
	"github.com/rail-service/rail_service/internal/domain/services/investing"
	"github.com/rail-service/rail_service/internal/domain/services/ledger"
	"github.com/rail-service/rail_service/internal/domain/services/limits"
	marketservice "github.com/rail-service/rail_service/internal/domain/services/market"
	newsservice "github.com/rail-service/rail_service/internal/domain/services/news"
	"github.com/rail-service/rail_service/internal/domain/services/onboarding"
	"github.com/rail-service/rail_service/internal/domain/services/passcode"
	"github.com/rail-service/rail_service/internal/domain/services/reconciliation"
	"github.com/rail-service/rail_service/internal/domain/services/roundup"
	"github.com/rail-service/rail_service/internal/domain/services/copytrading"
	"github.com/rail-service/rail_service/internal/domain/services/card"
	"github.com/rail-service/rail_service/internal/domain/services/session"
	"github.com/rail-service/rail_service/internal/domain/services/socialauth"
	"github.com/rail-service/rail_service/internal/domain/services/station"
	"github.com/rail-service/rail_service/internal/domain/services/strategy"
	"github.com/rail-service/rail_service/internal/domain/services/twofa"
	"github.com/rail-service/rail_service/internal/domain/services/wallet"
	"github.com/rail-service/rail_service/internal/domain/services/webauthn"
	"github.com/rail-service/rail_service/internal/domain/services/security"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"github.com/rail-service/rail_service/internal/infrastructure/circle"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/rail-service/rail_service/pkg/auth"
	commonmetrics "github.com/rail-service/rail_service/pkg/common/metrics"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/rail-service/rail_service/pkg/ratelimit"
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

// LedgerIntegrationAdapter adapts integration.LedgerIntegration to funding.LedgerIntegration interface
type LedgerIntegrationAdapter struct {
	integration *integration.LedgerIntegration
}

func (a *LedgerIntegrationAdapter) RecordDeposit(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, depositID uuid.UUID, chain, txHash string) error {
	return a.integration.RecordDeposit(ctx, userID, amount, depositID, chain, txHash)
}

func (a *LedgerIntegrationAdapter) GetUserBalance(ctx context.Context, userID uuid.UUID) (*funding.LedgerBalanceView, error) {
	view, err := a.integration.GetUserBalance(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &funding.LedgerBalanceView{
		USDCBalance:       view.USDCBalance,
		FiatExposure:      view.FiatExposure,
		PendingInvestment: view.PendingInvestment,
		TotalValue:        view.TotalValue,
	}, nil
}

// WithdrawalAlpacaAdapter adapts alpaca.Client to services.AlpacaAdapter interface for withdrawals
type WithdrawalAlpacaAdapter struct {
	client         *alpaca.Client
	fundingAdapter *alpaca.FundingAdapter
}

func (a *WithdrawalAlpacaAdapter) GetAccount(ctx context.Context, accountID string) (*entities.AlpacaAccountResponse, error) {
	return a.client.GetAccount(ctx, accountID)
}

func (a *WithdrawalAlpacaAdapter) CreateJournal(ctx context.Context, req *entities.AlpacaJournalRequest) (*entities.AlpacaJournalResponse, error) {
	return a.fundingAdapter.CreateJournal(ctx, req)
}

// WithdrawalBridgeAdapter adapts bridge.Adapter to services.WithdrawalProviderAdapter interface
type WithdrawalBridgeAdapter struct {
	adapter *bridge.Adapter
}

func (a *WithdrawalBridgeAdapter) ProcessWithdrawal(ctx context.Context, req *entities.InitiateWithdrawalRequest) (*services.ProcessWithdrawalResponse, error) {
	// Create Bridge transfer for withdrawal
	transferReq := &bridge.CreateTransferRequest{
		Amount: req.Amount.String(),
		Source: bridge.TransferSource{
			PaymentRail: bridge.PaymentRailEthereum, // Default source
			Currency:    bridge.CurrencyUSDC,
		},
		Destination: bridge.TransferDestination{
			PaymentRail: mapChainToPaymentRail(req.DestinationChain),
			Currency:    bridge.CurrencyUSDC,
			ToAddress:   req.DestinationAddress,
		},
	}

	transfer, err := a.adapter.TransferFunds(ctx, transferReq)
	if err != nil {
		return nil, err
	}

	return &services.ProcessWithdrawalResponse{
		TransferID:   transfer.ID,
		SourceAmount: transfer.Amount,
		DestAmount:   transfer.Amount,
		Status:       string(transfer.Status),
	}, nil
}

func (a *WithdrawalBridgeAdapter) GetTransferStatus(ctx context.Context, transferID string) (*services.OnRampTransferResponse, error) {
	transfer, err := a.adapter.Client().GetTransfer(ctx, transferID)
	if err != nil {
		return nil, err
	}
	return &services.OnRampTransferResponse{
		ID:     transfer.ID,
		Status: string(transfer.Status),
	}, nil
}

func mapChainToPaymentRail(chain string) bridge.PaymentRail {
	switch chain {
	case "ETH", "ethereum":
		return bridge.PaymentRailEthereum
	case "MATIC", "polygon":
		return bridge.PaymentRailPolygon
	case "SOL", "solana":
		return bridge.PaymentRailSolana
	case "BASE", "base":
		return bridge.PaymentRailBase
	default:
		return bridge.PaymentRailEthereum
	}
}

// BridgeOnboardingAdapter adapts bridge.Adapter to onboarding.BridgeAdapter interface
type BridgeOnboardingAdapter struct {
	adapter *bridge.Adapter
}

func (a *BridgeOnboardingAdapter) CreateCustomer(ctx context.Context, req *entities.CreateAccountRequest) (*entities.CreateAccountResponse, error) {
	bridgeReq := &bridge.CreateCustomerRequest{
		Type:      bridge.CustomerTypeIndividual,
		FirstName: req.FirstName,
		LastName:  req.LastName,
		Email:     req.Email,
	}

	customer, err := a.adapter.CreateCustomerWithWallet(ctx, &bridge.CreateCustomerWithWalletRequest{
		FirstName: req.FirstName,
		LastName:  req.LastName,
		Email:     req.Email,
		Chain:     bridge.PaymentRailEthereum, // Default chain
	})
	if err != nil {
		// Fallback to just creating customer without wallet
		cust, err := a.adapter.Client().CreateCustomer(ctx, bridgeReq)
		if err != nil {
			return nil, err
		}
		return &entities.CreateAccountResponse{
			AccountID: cust.ID,
			Status:    string(cust.Status),
		}, nil
	}

	return &entities.CreateAccountResponse{
		AccountID: customer.Customer.ID,
		Status:    string(customer.Customer.Status),
	}, nil
}

// FundingNotificationAdapter adapts NotificationService to funding.FundingNotificationService
type FundingNotificationAdapter struct {
	svc *services.NotificationService
}

func (a *FundingNotificationAdapter) NotifyDepositConfirmed(ctx context.Context, userID uuid.UUID, amount, chain, txHash string) error {
	return a.svc.NotifyDepositConfirmed(ctx, userID, amount, chain, txHash)
}

func (a *FundingNotificationAdapter) NotifyLargeBalanceChange(ctx context.Context, userID uuid.UUID, changeType string, amount decimal.Decimal, newBalance decimal.Decimal) error {
	return a.svc.NotifyLargeBalanceChange(ctx, userID, changeType, amount, newBalance)
}

// WithdrawalNotificationAdapter adapts NotificationService to services.WithdrawalNotificationService
type WithdrawalNotificationAdapter struct {
	svc *services.NotificationService
}

func (a *WithdrawalNotificationAdapter) NotifyWithdrawalCompleted(ctx context.Context, userID uuid.UUID, amount, destinationAddress string) error {
	return a.svc.NotifyWithdrawalCompleted(ctx, userID, amount, destinationAddress)
}

func (a *WithdrawalNotificationAdapter) NotifyWithdrawalFailed(ctx context.Context, userID uuid.UUID, amount, reason string) error {
	return a.svc.NotifyWithdrawalFailed(ctx, userID, amount, reason)
}

func (a *WithdrawalNotificationAdapter) NotifyLargeBalanceChange(ctx context.Context, userID uuid.UUID, changeType string, amount decimal.Decimal, newBalance decimal.Decimal) error {
	return a.svc.NotifyLargeBalanceChange(ctx, userID, changeType, amount, newBalance)
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
	BridgeClient  *bridge.Client
	BridgeAdapter *bridge.Adapter
	KYCProvider   *adapters.KYCProvider
	EmailService  *adapters.EmailService
	SMSService    *adapters.SMSService
	AuditService  *adapters.AuditService
	RedisClient   cache.RedisClient

	// Bridge Domain Adapters
	BridgeKYCAdapter              *BridgeKYCAdapter
	BridgeFundingAdapter          *BridgeFundingAdapter
	BridgeVirtualAccountService   *funding.BridgeVirtualAccountService
	BridgeWebhookHandler          *handlers.BridgeWebhookHandler

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
	BalanceService          *services.BalanceService
	EntitySecretService     *entitysecret.Service
	LedgerService           *ledger.Service
	ReconciliationService   *reconciliation.Service
	ReconciliationScheduler *reconciliation.Scheduler
	AllocationService       *allocation.Service
	AutoInvestService       *autoinvest.Service
	StrategyEngine          *strategy.Engine
	StationService          *station.Service
	NotificationService     *services.NotificationService
	SocialAuthService       *socialauth.Service
	WebAuthnService         *webauthn.Service
	LimitsService           *limits.Service
	DomainAuditService      *audit.Service
	WithdrawalService       *services.WithdrawalService

	// AI Financial Manager Services
	AIProviderManager     *ai.ProviderManager
	AIOrchestrator        *aiservice.Orchestrator
	AIRecommender         *aiservice.Recommender
	NewsService           *newsservice.Service
	PortfolioDataProvider *aiservice.PortfolioDataProviderImpl
	ActivityDataProvider  *aiservice.ActivityDataProviderImpl

	// Additional Repositories
	OnboardingJobRepo *repositories.OnboardingJobRepository

	// Alpaca Investment Repositories
	AlpacaAccountRepo      *repositories.AlpacaAccountRepository
	InvestmentOrderRepo    *repositories.InvestmentOrderRepository
	InvestmentPositionRepo *repositories.InvestmentPositionRepository
	AlpacaEventRepo        *repositories.AlpacaEventRepository
	AlpacaInstantFundingRepo *repositories.AlpacaInstantFundingRepository

	// Advanced Features Repositories
	PortfolioSnapshotRepo     *repositories.PortfolioSnapshotRepository
	ScheduledInvestmentRepo   *repositories.ScheduledInvestmentRepository
	RebalancingConfigRepo     *repositories.RebalancingConfigRepository
	MarketAlertRepo           *repositories.MarketAlertRepository

	// Alpaca Investment Services
	AlpacaAccountService   *alpacaservice.AccountService
	AlpacaFundingBridge    *alpacaservice.FundingBridge
	AlpacaEventProcessor   *alpacaservice.EventProcessor
	AlpacaPortfolioSync    *alpacaservice.PortfolioSyncService

	// Advanced Features Services
	PortfolioAnalyticsService   *analyticsservice.PortfolioAnalyticsService
	MarketDataService           *marketservice.MarketDataService
	ScheduledInvestmentService  *investing.ScheduledInvestmentService
	RebalancingService          *investing.RebalancingService

	// Brokerage Adapter
	BrokerageAdapter *adapters.BrokerageAdapter

	// Round-up Services
	RoundupRepo    *repositories.RoundupRepository
	RoundupService *roundup.Service

	// Copy Trading Services
	CopyTradingRepo    *repositories.CopyTradingRepository
	CopyTradingService *copytrading.Service

	// Card Services
	CardRepo    *repositories.CardRepository
	CardService *card.Service

	// Workers
	WalletProvisioningScheduler interface{} // Type interface{} to avoid circular dependency, will be set at runtime
	FundingWebhookManager       interface{} // Type interface{} to avoid circular dependency, will be set at runtime

	// Cache & Queue
	CacheInvalidator *cache.CacheInvalidator
	JobQueue         interface{} // Job queue for background processing
	JobScheduler     interface{} // Job scheduler for cron jobs

	// Security Services
	LoginProtectionService    *security.LoginProtectionService
	DeviceTrackingService     *security.DeviceTrackingService
	WithdrawalSecurityService *security.WithdrawalSecurityService
	IPWhitelistService        *security.IPWhitelistService
	PasswordPolicyService     *security.PasswordPolicyService
	SecurityEventLogger       *security.SecurityEventLogger
	PasswordService           *security.PasswordService
	
	// Enhanced Security Services (MFA, Geo, Fraud, Incident Response)
	MFAService              *security.MFAService
	GeoSecurityService      *security.GeoSecurityService
	FraudDetectionService   *security.FraudDetectionService
	IncidentResponseService *security.IncidentResponseService
	
	// Token and Rate Limiting
	TokenBlacklist      *auth.TokenBlacklist
	JWTService          *auth.JWTService
	TieredRateLimiter   *ratelimit.TieredLimiter
	LoginAttemptTracker *ratelimit.LoginAttemptTracker
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

	// Initialize Bridge service
	bridgeConfig := bridge.Config{
		APIKey:      cfg.Bridge.APIKey,
		BaseURL:     cfg.Bridge.BaseURL,
		Environment: cfg.Bridge.Environment,
		Timeout:     time.Duration(cfg.Bridge.Timeout) * time.Second,
		MaxRetries:  cfg.Bridge.MaxRetries,
	}
	bridgeClient := bridge.NewClient(bridgeConfig, zapLog)
	bridgeAdapter := bridge.NewAdapter(bridgeClient, zapLog)

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
		Provider:     cfg.Email.Provider,
		APIKey:       cfg.Email.APIKey,
		FromEmail:    cfg.Email.FromEmail,
		FromName:     cfg.Email.FromName,
		Environment:  cfg.Email.Environment,
		BaseURL:      cfg.Email.BaseURL,
		ReplyTo:      cfg.Email.ReplyTo,
		SMTPHost:     cfg.Email.SMTPHost,
		SMTPPort:     cfg.Email.SMTPPort,
		SMTPUsername: cfg.Email.SMTPUsername,
		SMTPPassword: cfg.Email.SMTPPassword,
		SMTPUseTLS:   cfg.Email.SMTPUseTLS,
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
		BridgeClient:  bridgeClient,
		BridgeAdapter: bridgeAdapter,
		KYCProvider:   kycProvider,
		EmailService:  emailService,
		SMSService:    smsService,
		AuditService:  auditService,
		RedisClient:   redisClient,

		// Bridge Domain Adapters
		BridgeKYCAdapter:     NewBridgeKYCAdapter(bridgeAdapter, userRepo),
		BridgeFundingAdapter: NewBridgeFundingAdapter(bridgeAdapter),

		// Entity Secret Service
		EntitySecretService: entitySecretService,

		// Cache & Queue
		CacheInvalidator: cacheInvalidator,
	}

	// Initialize Bridge virtual account service and webhook handler
	container.initializeBridgeServices()

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
		c.OnboardingService, // onboardingService - will be set after onboarding service is created
		c.ZapLog,
		walletServiceConfig,
	)

	// Initialize Alpaca adapter
	alpacaAdapter := alpaca.NewAdapter(c.AlpacaClient, c.Logger)

	// Initialize Bridge onboarding adapter
	bridgeOnboardingAdapter := &BridgeOnboardingAdapter{adapter: c.BridgeAdapter}

	// Initialize onboarding service (depends on wallet service)
	// Note: AllocationService will be injected after it's initialized
	c.OnboardingService = onboarding.NewService(
		c.UserRepo,
		c.OnboardingFlowRepo,
		c.KYCSubmissionRepo,
		c.WalletService, // Domain service dependency
		c.KYCProvider,
		c.EmailService,
		c.AuditService,
		bridgeOnboardingAdapter,
		alpacaAdapter,
		nil, // AllocationService - will be set after initialization
		c.ZapLog,
		append([]entities.WalletChain(nil), walletServiceConfig.SupportedChains...),
		c.Config.KYC.Provider, // KYC provider name
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

	// Initialize social auth service
	socialAuthConfig := socialauth.Config{
		Google: socialauth.OAuthConfig{
			ClientID:     c.Config.SocialAuth.Google.ClientID,
			ClientSecret: c.Config.SocialAuth.Google.ClientSecret,
			RedirectURI:  c.Config.SocialAuth.Google.RedirectURI,
		},
		Apple: socialauth.AppleOAuthConfig{
			ClientID:    c.Config.SocialAuth.Apple.ClientID,
			TeamID:      c.Config.SocialAuth.Apple.TeamID,
			KeyID:       c.Config.SocialAuth.Apple.KeyID,
			PrivateKey:  c.Config.SocialAuth.Apple.PrivateKey,
			RedirectURI: c.Config.SocialAuth.Apple.RedirectURI,
		},
	}
	c.SocialAuthService = socialauth.NewService(c.DB, c.ZapLog, socialAuthConfig)

	// Initialize WebAuthn service
	if c.Config.WebAuthn.RPID != "" {
		webauthnConfig := webauthn.Config{
			RPDisplayName: c.Config.WebAuthn.RPDisplayName,
			RPID:          c.Config.WebAuthn.RPID,
			RPOrigins:     c.Config.WebAuthn.RPOrigins,
		}
		webauthnSvc, err := webauthn.NewService(c.DB, c.ZapLog, webauthnConfig)
		if err != nil {
			c.Logger.Warn("Failed to initialize WebAuthn service", zap.Error(err))
		} else {
			c.WebAuthnService = webauthnSvc
		}
	}

	// Initialize simple wallet repository for funding service
	simpleWalletRepo := repositories.NewSimpleWalletRepository(c.DB, c.Logger)

	// Initialize virtual account repository
	sqlxDB := sqlx.NewDb(c.DB, "postgres")
	virtualAccountRepo := repositories.NewVirtualAccountRepository(sqlxDB)

	// Initialize Alpaca funding adapter
	alpacaFundingAdapter := alpaca.NewFundingAdapter(c.AlpacaClient, c.ZapLog)

	// Initialize ledger service
	c.LedgerService = ledger.NewService(c.LedgerRepo, sqlxDB, c.Logger)

	// Initialize ledger integration (bridges legacy and new ledger system)
	ledgerIntegration := integration.NewLedgerIntegration(
		c.LedgerService,
		c.BalanceRepo,
		c.Logger,
		false, // shadowMode disabled - fully migrated to ledger
		false, // strictMode
	)

	// Initialize standalone Balance service with Alpaca adapter
	alpacaBalanceAdapter := &AlpacaFundingAdapter{adapter: alpacaFundingAdapter, client: c.AlpacaClient}
	c.BalanceService = services.NewBalanceService(c.BalanceRepo, alpacaBalanceAdapter, c.Logger)

	// Initialize funding service with ledger integration (Bridge replaces Due)
	circleAdapter := &CircleAdapter{client: c.CircleClient}
	ledgerAdapter := &LedgerIntegrationAdapter{integration: ledgerIntegration}
	c.FundingService = funding.NewService(
		c.DepositRepo,
		simpleWalletRepo,
		c.WalletRepo,
		virtualAccountRepo,
		circleAdapter,
		&AlpacaFundingAdapter{adapter: alpacaFundingAdapter, client: c.AlpacaClient},
		ledgerAdapter,
		c.Logger,
	)

	// Initialize allocation service
	allocationRepo := repositories.NewAllocationRepository(sqlxDB, c.Logger)
	c.AllocationService = allocation.NewService(
		allocationRepo,
		c.LedgerService,
		c.Logger,
	)

	// Initialize auto-invest service (OrderPlacer will be set after InvestingService is created)
	_ = repositories.NewAutoInvestRepository(sqlxDB) // Keep for future use
	autoInvestConfig := autoinvest.Config{}
	c.AutoInvestService = autoinvest.NewService(
		c.LedgerService,
		nil, // OrderPlacer - will be set after InvestingService initialization
		autoInvestConfig,
		c.Logger,
	)

	// Wire auto-invest service to allocation service for automatic triggering
	c.AllocationService.SetAutoInvestService(c.AutoInvestService)

	// Inject allocation service into onboarding service (for auto-enabling 70/30 mode)
	c.OnboardingService.SetAllocationService(c.AllocationService)

	// Initialize station service (for home screen / Station endpoint)
	c.StationService = station.NewService(
		c.LedgerService,
		allocationRepo,
		c.DepositRepo,
		c.ZapLog,
	)

	// Initialize investing service with repositories
	basketRepo := repositories.NewBasketRepository(c.DB, c.ZapLog)
	orderRepo := repositories.NewOrderRepository(c.DB, c.ZapLog)
	positionRepo := repositories.NewPositionRepository(c.DB, c.ZapLog)

	// Initialize brokerage adapter with Alpaca service and required repositories
	brokerageAdapter := adapters.NewBrokerageAdapter(
		c.AlpacaClient,
		basketRepo,
		c.AlpacaAccountRepo,
		c.ZapLog,
	)
	c.BrokerageAdapter = brokerageAdapter

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

	// Wire auto-invest service with OrderPlacer now that InvestingService is available
	autoInvestOrderPlacer := &autoInvestOrderPlacerAdapter{
		accountService: c.AlpacaAccountService,
		alpacaClient:   c.AlpacaClient,
		orderRepo:      c.InvestmentOrderRepo,
		logger:         c.ZapLog,
	}
	c.AutoInvestService.SetOrderPlacer(autoInvestOrderPlacer)

	// Initialize strategy engine and wire to auto-invest service
	c.StrategyEngine = strategy.NewEngine(&strategyUserProfileAdapter{userRepo: c.UserRepo}, c.Logger)
	c.AutoInvestService.SetStrategyEngine(c.StrategyEngine)

	// Initialize reconciliation service
	if err := c.initializeReconciliationService(); err != nil {
		return fmt.Errorf("failed to initialize reconciliation service: %w", err)
	}

	// Initialize limits service for deposit/withdrawal limits
	usageRepo := repositories.NewUsageRepository(c.DB, c.ZapLog)
	c.LimitsService = limits.NewService(c.UserRepo, usageRepo, c.Logger)

	// Initialize domain audit service for compliance logging
	auditRepo := repositories.NewAuditRepository(sqlxDB)
	c.DomainAuditService = audit.NewService(auditRepo, c.ZapLog)

	// Initialize security services
	c.LoginProtectionService = security.NewLoginProtectionService(c.RedisClient.Client(), c.ZapLog)
	c.DeviceTrackingService = security.NewDeviceTrackingService(c.DB, c.ZapLog)
	c.WithdrawalSecurityService = security.NewWithdrawalSecurityService(c.DB, c.RedisClient.Client(), c.ZapLog)
	c.IPWhitelistService = security.NewIPWhitelistService(c.DB, c.RedisClient.Client(), c.ZapLog)
	c.PasswordPolicyService = security.NewPasswordPolicyService(c.Config.Security.CheckPasswordBreaches)
	c.SecurityEventLogger = security.NewSecurityEventLogger(c.DB, c.ZapLog)
	c.PasswordService = security.NewPasswordService(c.DB, c.ZapLog, c.Config.Security.CheckPasswordBreaches)

	// Initialize enhanced security services (MFA, Geo, Fraud, Incident Response)
	c.MFAService = security.NewMFAService(c.DB, c.RedisClient.Client(), c.ZapLog, c.Config.Security.EncryptionKey, nil) // SMS provider can be injected later
	c.GeoSecurityService = security.NewGeoSecurityService(c.DB, c.RedisClient.Client(), c.ZapLog, "") // IP API key can be configured
	c.FraudDetectionService = security.NewFraudDetectionService(c.DB, c.RedisClient.Client(), c.ZapLog)
	c.IncidentResponseService = security.NewIncidentResponseService(c.DB, c.RedisClient.Client(), c.ZapLog, nil, c.SecurityEventLogger)

	// Initialize token blacklist and JWT service
	if c.Config.Security.EnableTokenBlacklist {
		c.TokenBlacklist = auth.NewTokenBlacklist(c.RedisClient.Client())
		c.JWTService = auth.NewJWTService(
			c.Config.JWT.Secret,
			c.Config.Security.AccessTokenTTL,
			c.Config.Security.RefreshTokenTTL,
			c.TokenBlacklist,
		)
	}

	// Initialize tiered rate limiter
	tieredConfig := ratelimit.TieredConfig{
		GlobalLimit:  1000,
		GlobalWindow: time.Minute,
		IPLimit:      int64(c.Config.Server.RateLimitPerMin),
		IPWindow:     time.Minute,
		UserLimit:    200,
		UserWindow:   time.Minute,
		EndpointLimits: map[string]ratelimit.EndpointLimit{
			"POST /api/v1/auth/login": {Limit: 5, Window: 15 * time.Minute},
			"POST /api/v1/auth/register": {Limit: 3, Window: time.Hour},
			"POST /api/v1/funding/withdraw": {Limit: 10, Window: time.Hour},
		},
	}
	c.TieredRateLimiter = ratelimit.NewTieredLimiter(c.RedisClient.Client(), tieredConfig, c.ZapLog)
	c.LoginAttemptTracker = ratelimit.NewLoginAttemptTracker(c.RedisClient.Client(), c.ZapLog)

	// Wire limits and audit services to funding service
	c.FundingService.SetLimitsService(c.LimitsService)
	c.FundingService.SetAuditService(c.DomainAuditService)
	c.FundingService.SetNotificationService(&FundingNotificationAdapter{svc: c.NotificationService})
	c.FundingService.SetAllocationService(c.AllocationService) // Enable automatic 70/30 deposit split

	// Initialize withdrawal service with adapters (Bridge replaces Due)
	withdrawalAlpacaAdapter := &WithdrawalAlpacaAdapter{
		client:         c.AlpacaClient,
		fundingAdapter: alpacaFundingAdapter,
	}
	withdrawalBridgeAdapter := &WithdrawalBridgeAdapter{adapter: c.BridgeAdapter}
	c.WithdrawalService = services.NewWithdrawalService(
		c.WithdrawalRepo,
		withdrawalAlpacaAdapter,
		withdrawalBridgeAdapter,
		c.AllocationService,
		nil, // AllocationNotificationManager - optional
		c.Logger,
		nil, // QueuePublisher - will use mock
	)
	// Wire limits, audit, and notification services to withdrawal service
	c.WithdrawalService.SetLimitsService(c.LimitsService)
	c.WithdrawalService.SetAuditService(c.DomainAuditService)
	c.WithdrawalService.SetNotificationService(&WithdrawalNotificationAdapter{svc: c.NotificationService})

	// Initialize AI Financial Manager services
	if err := c.initializeAIServices(sqlxDB, positionRepo, allocationRepo, basketRepo); err != nil {
		c.ZapLog.Warn("AI services initialization failed, AI features disabled", zap.Error(err))
	}

	// Initialize Alpaca investment infrastructure
	if err := c.initializeAlpacaInvestmentServices(sqlxDB); err != nil {
		c.ZapLog.Warn("Alpaca investment services initialization failed", zap.Error(err))
	}

	// Initialize advanced features (analytics, market data, scheduled investments, rebalancing)
	if err := c.initializeAdvancedFeatures(sqlxDB); err != nil {
		c.ZapLog.Warn("Advanced features initialization failed", zap.Error(err))
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

// GetSocialAuthService returns the social auth service
func (c *Container) GetSocialAuthService() *socialauth.Service {
	return c.SocialAuthService
}

// GetWebAuthnService returns the WebAuthn service
func (c *Container) GetWebAuthnService() *webauthn.Service {
	return c.WebAuthnService
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

// GetWithdrawalService returns the withdrawal service
func (c *Container) GetWithdrawalService() *services.WithdrawalService {
	return c.WithdrawalService
}

// GetInvestingService returns the investing service
func (c *Container) GetInvestingService() *investing.Service {
	return c.InvestingService
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

// GetAutoInvestService returns the auto-invest service
func (c *Container) GetAutoInvestService() *autoinvest.Service {
	return c.AutoInvestService
}

// GetLimitsService returns the limits service
func (c *Container) GetLimitsService() *limits.Service {
	return c.LimitsService
}

// GetLimitsHandler returns a new limits handler
func (c *Container) GetLimitsHandler() *handlers.LimitsHandler {
	if c.LimitsService == nil {
		return nil
	}
	return handlers.NewLimitsHandler(c.LimitsService, c.Logger)
}

// GetLoginProtectionService returns the login protection service
func (c *Container) GetLoginProtectionService() *security.LoginProtectionService {
	return c.LoginProtectionService
}

// GetDeviceTrackingService returns the device tracking service
func (c *Container) GetDeviceTrackingService() *security.DeviceTrackingService {
	return c.DeviceTrackingService
}

// GetWithdrawalSecurityService returns the withdrawal security service
func (c *Container) GetWithdrawalSecurityService() *security.WithdrawalSecurityService {
	return c.WithdrawalSecurityService
}

// GetIPWhitelistService returns the IP whitelist service
func (c *Container) GetIPWhitelistService() *security.IPWhitelistService {
	return c.IPWhitelistService
}

// GetPasswordPolicyService returns the password policy service
func (c *Container) GetPasswordPolicyService() *security.PasswordPolicyService {
	return c.PasswordPolicyService
}

// GetSecurityEventLogger returns the security event logger
func (c *Container) GetSecurityEventLogger() *security.SecurityEventLogger {
	return c.SecurityEventLogger
}

// GetPasswordService returns the enhanced password service
func (c *Container) GetPasswordService() *security.PasswordService {
	return c.PasswordService
}

// GetMFAService returns the unified MFA service
func (c *Container) GetMFAService() *security.MFAService {
	return c.MFAService
}

// GetGeoSecurityService returns the geo security service
func (c *Container) GetGeoSecurityService() *security.GeoSecurityService {
	return c.GeoSecurityService
}

// GetFraudDetectionService returns the fraud detection service
func (c *Container) GetFraudDetectionService() *security.FraudDetectionService {
	return c.FraudDetectionService
}

// GetIncidentResponseService returns the incident response service
func (c *Container) GetIncidentResponseService() *security.IncidentResponseService {
	return c.IncidentResponseService
}

// GetTokenBlacklist returns the token blacklist service
func (c *Container) GetTokenBlacklist() *auth.TokenBlacklist {
	return c.TokenBlacklist
}

// GetJWTService returns the enhanced JWT service
func (c *Container) GetJWTService() *auth.JWTService {
	return c.JWTService
}

// GetTieredRateLimiter returns the tiered rate limiter
func (c *Container) GetTieredRateLimiter() *ratelimit.TieredLimiter {
	return c.TieredRateLimiter
}

// GetLoginAttemptTracker returns the login attempt tracker
func (c *Container) GetLoginAttemptTracker() *ratelimit.LoginAttemptTracker {
	return c.LoginAttemptTracker
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
			entities.WalletChainSOLDevnet,
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
			entities.WalletChainSOLDevnet,
		}
	}

	return normalized
}

// initializeAIServices initializes AI Financial Manager services
func (c *Container) initializeAIServices(sqlxDB *sqlx.DB, positionRepo *repositories.PositionRepository, allocationRepo *repositories.AllocationRepository, basketRepo *repositories.BasketRepository) error {
	// Check if AI is configured
	if c.Config.AI.OpenAI.APIKey == "" && c.Config.AI.Gemini.APIKey == "" {
		return fmt.Errorf("no AI provider configured")
	}

	// Initialize AI providers
	var providers []ai.AIProvider

	if c.Config.AI.OpenAI.APIKey != "" {
		openaiConfig := &ai.ProviderConfig{
			APIKey:      c.Config.AI.OpenAI.APIKey,
			Model:       c.Config.AI.OpenAI.Model,
			MaxTokens:   c.Config.AI.OpenAI.MaxTokens,
			Temperature: c.Config.AI.OpenAI.Temperature,
			Timeout:     30 * time.Second,
		}
		openaiProvider := ai.NewOpenAIProvider(openaiConfig, c.ZapLog)
		providers = append(providers, openaiProvider)
	}

	if c.Config.AI.Gemini.APIKey != "" {
		geminiConfig := &ai.ProviderConfig{
			APIKey:      c.Config.AI.Gemini.APIKey,
			Model:       c.Config.AI.Gemini.Model,
			MaxTokens:   c.Config.AI.Gemini.MaxTokens,
			Temperature: c.Config.AI.Gemini.Temperature,
			Timeout:     30 * time.Second,
		}
		geminiProvider := ai.NewGeminiProvider(geminiConfig, c.ZapLog)
		providers = append(providers, geminiProvider)
	}

	if len(providers) == 0 {
		return fmt.Errorf("no AI providers available")
	}

	// Set primary and fallbacks based on config
	var primary ai.AIProvider
	var fallbacks []ai.AIProvider

	if c.Config.AI.Primary == "gemini" && len(providers) > 1 {
		primary = providers[1]
		fallbacks = []ai.AIProvider{providers[0]}
	} else {
		primary = providers[0]
		if len(providers) > 1 {
			fallbacks = providers[1:]
		}
	}

	c.AIProviderManager = ai.NewProviderManager(primary, fallbacks, nil, c.ZapLog)

	// Initialize repositories for AI services
	userNewsRepo := repositories.NewUserNewsRepository(c.DB, c.ZapLog)
	streakRepo := repositories.NewInvestmentStreakRepository(c.DB, c.ZapLog)
	contributionsRepo := repositories.NewUserContributionsRepository(c.DB, c.ZapLog)
	portfolioRepo := repositories.NewPortfolioRepository(c.DB, c.ZapLog)

	// Initialize data providers
	c.PortfolioDataProvider = aiservice.NewPortfolioDataProvider(
		&portfolioValueAdapter{repo: portfolioRepo},
		positionRepo,
		c.ZapLog,
	)

	c.ActivityDataProvider = aiservice.NewActivityDataProvider(
		&contributionRepoAdapter{repo: contributionsRepo},
		&streakRepoAdapter{repo: streakRepo},
		c.ZapLog,
	)

	// Initialize news service
	c.NewsService = newsservice.NewService(
		&alpacaNewsAdapter{client: c.AlpacaClient},
		userNewsRepo,
		positionRepo,
		c.ZapLog,
	)

	// Initialize AI orchestrator (use primary provider directly)
	c.AIOrchestrator = aiservice.NewOrchestrator(
		primary,
		c.PortfolioDataProvider,
		c.ActivityDataProvider,
		&newsProviderAdapter{svc: c.NewsService},
		c.ZapLog,
	)

	// Initialize basket recommender
	c.AIRecommender = aiservice.NewRecommender(
		primary,
		&basketRepoAdapter{repo: basketRepo},
		c.PortfolioDataProvider,
		c.ZapLog,
	)

	c.ZapLog.Info("AI Financial Manager services initialized",
		zap.String("primary_provider", primary.Name()),
		zap.Int("fallback_count", len(fallbacks)),
	)

	return nil
}

// AI service adapters

type portfolioValueAdapter struct {
	repo *repositories.PortfolioRepository
}

func (a *portfolioValueAdapter) GetPortfolioValue(ctx context.Context, userID uuid.UUID, date time.Time) (decimal.Decimal, error) {
	return a.repo.GetPortfolioValue(ctx, userID, date)
}

type contributionRepoAdapter struct {
	repo *repositories.UserContributionsRepository
}

func (a *contributionRepoAdapter) GetByUserID(ctx context.Context, userID uuid.UUID, contributionType *entities.ContributionType, startDate, endDate *time.Time, limit, offset int) ([]*entities.UserContribution, error) {
	return a.repo.GetByUserID(ctx, userID, contributionType, startDate, endDate, limit, offset)
}

func (a *contributionRepoAdapter) GetTotalByType(ctx context.Context, userID uuid.UUID, startDate, endDate time.Time) (map[entities.ContributionType]string, error) {
	return a.repo.GetTotalByType(ctx, userID, startDate, endDate)
}

type streakRepoAdapter struct {
	repo *repositories.InvestmentStreakRepository
}

func (a *streakRepoAdapter) GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.InvestmentStreak, error) {
	return a.repo.GetByUserID(ctx, userID)
}

type newsProviderAdapter struct {
	svc *newsservice.Service
}

func (a *newsProviderAdapter) GetWeeklyNews(ctx context.Context, userID uuid.UUID) ([]*entities.UserNews, error) {
	return a.svc.GetWeeklyNews(ctx, userID)
}

type basketRepoAdapter struct {
	repo *repositories.BasketRepository
}

func (a *basketRepoAdapter) GetCuratedBaskets(ctx context.Context) ([]*entities.Basket, error) {
	return a.repo.GetAll(ctx)
}

func (a *basketRepoAdapter) GetByID(ctx context.Context, id uuid.UUID) (*entities.Basket, error) {
	return a.repo.GetByID(ctx, id)
}

type alpacaNewsAdapter struct {
	client *alpaca.Client
}

func (a *alpacaNewsAdapter) GetNews(ctx context.Context, req *entities.AlpacaNewsRequest) (*entities.AlpacaNewsResponse, error) {
	return a.client.GetNews(ctx, req)
}

// GetAIOrchestrator returns the AI orchestrator
func (c *Container) GetAIOrchestrator() *aiservice.Orchestrator {
	return c.AIOrchestrator
}

// GetAIRecommender returns the AI recommender
func (c *Container) GetAIRecommender() *aiservice.Recommender {
	return c.AIRecommender
}

// GetNewsService returns the news service
func (c *Container) GetNewsService() *newsservice.Service {
	return c.NewsService
}

// GetPortfolioDataProvider returns the portfolio data provider
func (c *Container) GetPortfolioDataProvider() *aiservice.PortfolioDataProviderImpl {
	return c.PortfolioDataProvider
}

// GetActivityDataProvider returns the activity data provider
func (c *Container) GetActivityDataProvider() *aiservice.ActivityDataProviderImpl {
	return c.ActivityDataProvider
}

// GetStreakRepository returns the investment streak repository adapter
func (c *Container) GetStreakRepository() handlers.InvestmentStreakRepository {
	if c.ActivityDataProvider == nil {
		return nil
	}
	return &streakRepoAdapter{repo: repositories.NewInvestmentStreakRepository(c.DB, c.ZapLog)}
}

// GetContributionsRepository returns the user contributions repository adapter
func (c *Container) GetContributionsRepository() handlers.UserContributionsRepository {
	if c.ActivityDataProvider == nil {
		return nil
	}
	return &contributionRepoAdapter{repo: repositories.NewUserContributionsRepository(c.DB, c.ZapLog)}
}


// initializeAlpacaInvestmentServices initializes Alpaca investment infrastructure
func (c *Container) initializeAlpacaInvestmentServices(sqlxDB *sqlx.DB) error {
	// Initialize repositories
	c.AlpacaAccountRepo = repositories.NewAlpacaAccountRepository(sqlxDB)
	c.InvestmentOrderRepo = repositories.NewInvestmentOrderRepository(sqlxDB)
	c.InvestmentPositionRepo = repositories.NewInvestmentPositionRepository(sqlxDB)
	c.AlpacaEventRepo = repositories.NewAlpacaEventRepository(sqlxDB)
	c.AlpacaInstantFundingRepo = repositories.NewAlpacaInstantFundingRepository(sqlxDB)

	// User profile adapter for account service
	userProfileAdapter := repositories.NewUserProfileAdapter(c.UserRepo)

	// Initialize Account Service
	c.AlpacaAccountService = alpacaservice.NewAccountService(
		c.AlpacaClient,
		c.AlpacaAccountRepo,
		userProfileAdapter,
		c.ZapLog,
	)

	// Initialize Funding Bridge
	c.AlpacaFundingBridge = alpacaservice.NewFundingBridge(
		c.AlpacaClient,
		c.AlpacaAccountRepo,
		c.AlpacaInstantFundingRepo,
		c.BalanceRepo,
		c.Config.Alpaca.FirmAccountNo,
		c.ZapLog,
	)

	// Initialize Event Processor
	c.AlpacaEventProcessor = alpacaservice.NewEventProcessor(
		c.AlpacaAccountRepo,
		c.InvestmentOrderRepo,
		c.InvestmentPositionRepo,
		c.AlpacaEventRepo,
		c.BalanceRepo,
		c.ZapLog,
	)

	// Initialize Portfolio Sync Service
	c.AlpacaPortfolioSync = alpacaservice.NewPortfolioSyncService(
		c.AlpacaClient,
		c.AlpacaAccountRepo,
		c.InvestmentPositionRepo,
		c.BalanceRepo,
		c.ZapLog,
	)

	c.ZapLog.Info("Alpaca investment services initialized")
	return nil
}

// initializeAdvancedFeatures initializes analytics, market data, and automation services
func (c *Container) initializeAdvancedFeatures(sqlxDB *sqlx.DB) error {
	// Initialize repositories
	c.PortfolioSnapshotRepo = repositories.NewPortfolioSnapshotRepository(sqlxDB)
	c.ScheduledInvestmentRepo = repositories.NewScheduledInvestmentRepository(sqlxDB)
	c.RebalancingConfigRepo = repositories.NewRebalancingConfigRepository(sqlxDB)
	c.MarketAlertRepo = repositories.NewMarketAlertRepository(sqlxDB)

	// Initialize Portfolio Analytics Service
	c.PortfolioAnalyticsService = analyticsservice.NewPortfolioAnalyticsService(
		c.PortfolioSnapshotRepo,
		c.InvestmentPositionRepo,
		c.AlpacaAccountRepo,
		c.ZapLog,
	)

	// Initialize Market Data Service
	c.MarketDataService = marketservice.NewMarketDataService(
		c.AlpacaClient,
		c.MarketAlertRepo,
		&marketNotificationAdapter{svc: c.NotificationService},
		c.ZapLog,
	)

	// Initialize Order Placer adapter for scheduled investments
	orderPlacer := &orderPlacerAdapter{
		investingService: c.InvestingService,
		accountService:   c.AlpacaAccountService,
		alpacaClient:     c.AlpacaClient,
		orderRepo:        c.InvestmentOrderRepo,
		logger:           c.ZapLog,
	}

	// Initialize Scheduled Investment Service
	c.ScheduledInvestmentService = investing.NewScheduledInvestmentService(
		c.ScheduledInvestmentRepo,
		orderPlacer,
		c.BrokerageAdapter, // BasketOrderPlacer
		c.ZapLog,
	)

	// Initialize Rebalancing Service
	c.RebalancingService = investing.NewRebalancingService(
		c.RebalancingConfigRepo,
		c.InvestmentPositionRepo,
		c.MarketDataService,
		orderPlacer,
		c.ZapLog,
	)

	// Initialize Round-up Service
	c.RoundupRepo = repositories.NewRoundupRepository(sqlxDB)
	c.RoundupService = roundup.NewService(
		c.RoundupRepo,
		c.AllocationService,
		orderPlacer,
		nil, // ContributionRecorder - can be added later
		c.ZapLog,
	)

	// Initialize Copy Trading Service
	c.CopyTradingRepo = repositories.NewCopyTradingRepository(sqlxDB)
	c.CopyTradingService = copytrading.NewService(
		c.CopyTradingRepo,
		&copyTradingBalanceAdapter{ledgerService: c.LedgerService, userID: uuid.Nil},
		&copyTradingTradingAdapter{alpacaClient: c.AlpacaClient, accountRepo: c.AlpacaAccountRepo},
		c.ZapLog,
	)

	// Initialize Card Service
	c.CardRepo = repositories.NewCardRepository(sqlxDB)
	c.CardService = card.NewService(
		c.CardRepo,
		c.BridgeAdapter,
		&cardUserProfileAdapter{userRepo: c.UserRepo},
		&cardWalletAdapter{walletService: c.WalletService},
		&cardBalanceAdapter{ledgerService: c.LedgerService},
		c.ZapLog,
	)
	// Wire ledger service to card service for transaction ledger entries
	c.CardService.SetLedgerService(c.LedgerService)

	c.ZapLog.Info("Advanced features initialized")
	return nil
}

// marketNotificationAdapter adapts NotificationService for market alerts
type marketNotificationAdapter struct {
	svc *services.NotificationService
}

func (a *marketNotificationAdapter) SendPushNotification(ctx context.Context, userID uuid.UUID, title, message string) error {
	if a.svc == nil {
		return nil
	}
	// Use existing notification service method
	return a.svc.SendGenericNotification(ctx, userID, title, message)
}

// copyTradingBalanceAdapter adapts LedgerService for copy trading balance operations
type copyTradingBalanceAdapter struct {
	ledgerService *ledger.Service
	userID        uuid.UUID
}

func (a *copyTradingBalanceAdapter) GetAvailableBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	if a.ledgerService == nil {
		return decimal.Zero, fmt.Errorf("ledger service not available")
	}
	balances, err := a.ledgerService.GetUserBalances(ctx, userID)
	if err != nil {
		return decimal.Zero, err
	}
	return balances.USDCBalance, nil
}

func (a *copyTradingBalanceAdapter) DeductBalance(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, description string) error {
	if a.ledgerService == nil {
		return fmt.Errorf("ledger service not available")
	}
	// Reserve funds for copy trading allocation
	return a.ledgerService.ReserveForInvestment(ctx, userID, amount)
}

func (a *copyTradingBalanceAdapter) AddBalance(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, description string) error {
	if a.ledgerService == nil {
		return fmt.Errorf("ledger service not available")
	}
	// Release reserved funds back to user
	return a.ledgerService.ReleaseReservation(ctx, userID, amount)
}

// copyTradingTradingAdapter adapts Alpaca client for copy trading order execution
type copyTradingTradingAdapter struct {
	alpacaClient *alpaca.Client
	accountRepo  *repositories.AlpacaAccountRepository
}

func (a *copyTradingTradingAdapter) PlaceOrder(ctx context.Context, userID uuid.UUID, symbol string, side string, quantity decimal.Decimal) (string, decimal.Decimal, error) {
	if a.alpacaClient == nil || a.accountRepo == nil {
		return "", decimal.Zero, fmt.Errorf("trading adapter not configured")
	}

	// Get user's Alpaca account
	account, err := a.accountRepo.GetByUserID(ctx, userID)
	if err != nil || account == nil {
		return "", decimal.Zero, fmt.Errorf("user has no brokerage account")
	}

	// Place order via Alpaca
	orderSide := entities.AlpacaOrderSideBuy
	if side == "sell" {
		orderSide = entities.AlpacaOrderSideSell
	}

	orderReq := &entities.AlpacaCreateOrderRequest{
		Symbol:      symbol,
		Qty:         &quantity,
		Side:        orderSide,
		Type:        entities.AlpacaOrderTypeMarket,
		TimeInForce: entities.AlpacaTimeInForceDay,
	}

	resp, err := a.alpacaClient.CreateOrder(ctx, account.AlpacaAccountID, orderReq)
	if err != nil {
		return "", decimal.Zero, fmt.Errorf("failed to place order: %w", err)
	}

	// Get executed price (for market orders, use filled_avg_price or current price)
	executedPrice := decimal.Zero
	if resp.FilledAvgPrice != nil && !resp.FilledAvgPrice.IsZero() {
		executedPrice = *resp.FilledAvgPrice
	}

	return resp.ID, executedPrice, nil
}

func (a *copyTradingTradingAdapter) GetCurrentPrice(ctx context.Context, symbol string) (decimal.Decimal, error) {
	if a.alpacaClient == nil {
		return decimal.Zero, fmt.Errorf("trading adapter not configured")
	}

	quote, err := a.alpacaClient.GetLatestQuote(ctx, symbol)
	if err != nil {
		return decimal.Zero, fmt.Errorf("failed to get quote: %w", err)
	}

	return quote.Ask, nil
}

// autoInvestOrderPlacerAdapter implements autoinvest.OrderPlacer interface
type autoInvestOrderPlacerAdapter struct {
	accountService *alpacaservice.AccountService
	alpacaClient   *alpaca.Client
	orderRepo      *repositories.InvestmentOrderRepository
	logger         *zap.Logger
}

func (a *autoInvestOrderPlacerAdapter) PlaceMarketOrder(ctx context.Context, userID uuid.UUID, symbol string, amount decimal.Decimal) (*entities.AlpacaOrderResponse, error) {
	// Get user's Alpaca account
	account, err := a.accountService.GetUserAccount(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get account: %w", err)
	}
	if account == nil {
		return nil, fmt.Errorf("user has no Alpaca account")
	}

	// Create market order via Alpaca
	orderReq := &entities.AlpacaCreateOrderRequest{
		Symbol:      symbol,
		Notional:    &amount,
		Side:        entities.AlpacaOrderSideBuy,
		Type:        entities.AlpacaOrderTypeMarket,
		TimeInForce: entities.AlpacaTimeInForceDay,
	}

	alpacaOrder, err := a.alpacaClient.CreateOrder(ctx, account.AlpacaAccountID, orderReq)
	if err != nil {
		return nil, fmt.Errorf("create order: %w", err)
	}

	// Store order in database for tracking
	now := time.Now()
	order := &entities.InvestmentOrder{
		ID:              uuid.New(),
		UserID:          userID,
		AlpacaAccountID: &account.ID,
		AlpacaOrderID:   &alpacaOrder.ID,
		ClientOrderID:   alpacaOrder.ClientOrderID,
		Symbol:          symbol,
		Side:            entities.AlpacaOrderSideBuy,
		OrderType:       entities.AlpacaOrderTypeMarket,
		TimeInForce:     entities.AlpacaTimeInForceDay,
		Notional:        &amount,
		Status:          alpacaOrder.Status,
		SubmittedAt:     &now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	if err := a.orderRepo.Create(ctx, order); err != nil {
		a.logger.Error("Failed to store auto-invest order", zap.Error(err))
	}

	return alpacaOrder, nil
}

// strategyUserProfileAdapter adapts UserRepository for strategy engine
type strategyUserProfileAdapter struct {
	userRepo *repositories.UserRepository
}

func (a *strategyUserProfileAdapter) GetByID(ctx context.Context, id uuid.UUID) (*entities.UserProfile, error) {
	if a.userRepo == nil {
		return nil, fmt.Errorf("user repository not available")
	}
	return a.userRepo.GetByID(ctx, id)
}

// orderPlacerAdapter implements OrderPlacer interface for scheduled investments
type orderPlacerAdapter struct {
	investingService *investing.Service
	accountService   *alpacaservice.AccountService
	alpacaClient     *alpaca.Client
	orderRepo        *repositories.InvestmentOrderRepository
	logger           *zap.Logger
}

func (a *orderPlacerAdapter) PlaceMarketOrder(ctx context.Context, userID uuid.UUID, symbol string, notional decimal.Decimal) (*entities.InvestmentOrder, error) {
	// Get user's Alpaca account
	account, err := a.accountService.GetUserAccount(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get account: %w", err)
	}
	if account == nil {
		return nil, fmt.Errorf("user has no Alpaca account")
	}

	// Determine side based on notional sign
	side := entities.AlpacaOrderSideBuy
	if notional.LessThan(decimal.Zero) {
		side = entities.AlpacaOrderSideSell
		notional = notional.Abs()
	}

	// Create order via Alpaca
	orderReq := &entities.AlpacaCreateOrderRequest{
		Symbol:      symbol,
		Notional:    &notional,
		Side:        side,
		Type:        entities.AlpacaOrderTypeMarket,
		TimeInForce: entities.AlpacaTimeInForceDay,
	}

	alpacaOrder, err := a.alpacaClient.CreateOrder(ctx, account.AlpacaAccountID, orderReq)
	if err != nil {
		return nil, fmt.Errorf("create order: %w", err)
	}

	// Store order in database
	now := time.Now()
	order := &entities.InvestmentOrder{
		ID:              uuid.New(),
		UserID:          userID,
		AlpacaAccountID: &account.ID,
		AlpacaOrderID:   &alpacaOrder.ID,
		ClientOrderID:   alpacaOrder.ClientOrderID,
		Symbol:          symbol,
		Side:            side,
		OrderType:       entities.AlpacaOrderTypeMarket,
		TimeInForce:     entities.AlpacaTimeInForceDay,
		Notional:        &notional,
		Status:          alpacaOrder.Status,
		SubmittedAt:     &now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	if err := a.orderRepo.Create(ctx, order); err != nil {
		a.logger.Error("Failed to store order", zap.Error(err))
	}

	return order, nil
}

// Card service adapters

// cardUserProfileAdapter adapts UserRepository for card service
type cardUserProfileAdapter struct {
	userRepo *repositories.UserRepository
}

func (a *cardUserProfileAdapter) GetByID(ctx context.Context, id uuid.UUID) (*entities.UserProfile, error) {
	if a.userRepo == nil {
		return nil, fmt.Errorf("user repository not available")
	}
	return a.userRepo.GetByID(ctx, id)
}

// cardWalletAdapter adapts WalletService for card service
type cardWalletAdapter struct {
	walletService *wallet.Service
}

func (a *cardWalletAdapter) GetUserWalletByChain(ctx context.Context, userID uuid.UUID, chain string) (*entities.ManagedWallet, error) {
	if a.walletService == nil {
		return nil, fmt.Errorf("wallet service not available")
	}
	walletChain := entities.WalletChain(strings.ToUpper(chain))
	return a.walletService.GetWalletByUserAndChain(ctx, userID, walletChain)
}

// cardBalanceAdapter adapts LedgerService for card balance operations
type cardBalanceAdapter struct {
	ledgerService *ledger.Service
}

func (a *cardBalanceAdapter) GetSpendBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	if a.ledgerService == nil {
		return decimal.Zero, fmt.Errorf("ledger service not available")
	}
	// Get spending balance account directly
	account, err := a.ledgerService.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return decimal.Zero, err
	}
	return account.Balance, nil
}

func (a *cardBalanceAdapter) DeductSpendBalance(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, reference string) error {
	if a.ledgerService == nil {
		return fmt.Errorf("ledger service not available")
	}
	// Create a debit entry for card transaction
	return a.ledgerService.RecordCardTransaction(ctx, userID, amount, reference)
}

// Getters for new services

// GetAlpacaAccountService returns the Alpaca account service
func (c *Container) GetAlpacaAccountService() *alpacaservice.AccountService {
	return c.AlpacaAccountService
}

// GetAlpacaFundingBridge returns the Alpaca funding bridge
func (c *Container) GetAlpacaFundingBridge() *alpacaservice.FundingBridge {
	return c.AlpacaFundingBridge
}

// GetAlpacaEventProcessor returns the Alpaca event processor
func (c *Container) GetAlpacaEventProcessor() *alpacaservice.EventProcessor {
	return c.AlpacaEventProcessor
}

// GetAlpacaPortfolioSync returns the Alpaca portfolio sync service
func (c *Container) GetAlpacaPortfolioSync() *alpacaservice.PortfolioSyncService {
	return c.AlpacaPortfolioSync
}

// GetPortfolioAnalyticsService returns the portfolio analytics service
func (c *Container) GetPortfolioAnalyticsService() *analyticsservice.PortfolioAnalyticsService {
	return c.PortfolioAnalyticsService
}

// GetMarketDataService returns the market data service
func (c *Container) GetMarketDataService() *marketservice.MarketDataService {
	return c.MarketDataService
}

// GetScheduledInvestmentService returns the scheduled investment service
func (c *Container) GetScheduledInvestmentService() *investing.ScheduledInvestmentService {
	return c.ScheduledInvestmentService
}

// GetRebalancingService returns the rebalancing service
func (c *Container) GetRebalancingService() *investing.RebalancingService {
	return c.RebalancingService
}

// GetInvestmentHandlers returns investment handlers
func (c *Container) GetInvestmentHandlers() *handlers.InvestmentHandlers {
	if c.AlpacaAccountService == nil {
		return nil
	}
	return handlers.NewInvestmentHandlers(
		c.AlpacaAccountService,
		c.AlpacaFundingBridge,
		c.AlpacaPortfolioSync,
		c.Logger,
	)
}

// GetAlpacaWebhookHandlers returns Alpaca webhook handlers
func (c *Container) GetAlpacaWebhookHandlers() *handlers.AlpacaWebhookHandlers {
	if c.AlpacaEventProcessor == nil {
		return nil
	}
	return handlers.NewAlpacaWebhookHandlers(c.AlpacaEventProcessor, c.Logger)
}

// GetAnalyticsHandlers returns analytics handlers
func (c *Container) GetAnalyticsHandlers() *handlers.AnalyticsHandlers {
	if c.PortfolioAnalyticsService == nil {
		return nil
	}
	return handlers.NewAnalyticsHandlers(c.PortfolioAnalyticsService, c.Logger)
}

// GetMarketHandlers returns market data handlers
func (c *Container) GetMarketHandlers() *handlers.MarketHandlers {
	if c.MarketDataService == nil {
		return nil
	}
	return handlers.NewMarketHandlers(c.MarketDataService, c.Logger)
}

// GetScheduledInvestmentHandlers returns scheduled investment handlers
func (c *Container) GetScheduledInvestmentHandlers() *handlers.ScheduledInvestmentHandlers {
	if c.ScheduledInvestmentService == nil {
		return nil
	}
	return handlers.NewScheduledInvestmentHandlers(c.ScheduledInvestmentService, c.Logger)
}

// GetRebalancingHandlers returns rebalancing handlers
func (c *Container) GetRebalancingHandlers() *handlers.RebalancingHandlers {
	if c.RebalancingService == nil {
		return nil
	}
	return handlers.NewRebalancingHandlers(c.RebalancingService, c.Logger)
}

// GetRoundupService returns the round-up service
func (c *Container) GetRoundupService() *roundup.Service {
	return c.RoundupService
}

// GetRoundupHandlers returns round-up handlers
func (c *Container) GetRoundupHandlers() *handlers.RoundupHandlers {
	if c.RoundupService == nil {
		return nil
	}
	return handlers.NewRoundupHandlers(c.RoundupService, c.ZapLog)
}

// GetCopyTradingService returns the copy trading service
func (c *Container) GetCopyTradingService() *copytrading.Service {
	return c.CopyTradingService
}

// GetCopyTradingHandlers returns copy trading handlers
func (c *Container) GetCopyTradingHandlers() *handlers.CopyTradingHandlers {
	if c.CopyTradingService == nil {
		return nil
	}
	return handlers.NewCopyTradingHandlers(c.CopyTradingService, c.Logger)
}

// GetCardService returns the card service
func (c *Container) GetCardService() *card.Service {
	return c.CardService
}

// GetCardHandlers returns card handlers
func (c *Container) GetCardHandlers() *handlers.CardHandlers {
	if c.CardService == nil {
		return nil
	}
	return handlers.NewCardHandlers(c.CardService, c.ZapLog)
}

// GetStationHandlers returns station handlers
func (c *Container) GetStationHandlers() *handlers.StationHandlers {
	if c.StationService == nil {
		return nil
	}
	return handlers.NewStationHandlers(c.StationService, c.ZapLog)
}

// GetCopyTradingRepository returns the copy trading repository
func (c *Container) GetCopyTradingRepository() *repositories.CopyTradingRepository {
	return c.CopyTradingRepo
}

// ListAllActiveUserIDs returns all active user IDs (for portfolio snapshot worker)
func (c *Container) ListAllActiveUserIDs(ctx context.Context) ([]uuid.UUID, error) {
	query := `SELECT id FROM users WHERE is_active = true`
	rows, err := c.DB.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var userIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			continue
		}
		userIDs = append(userIDs, id)
	}
	return userIDs, rows.Err()
}

// GetBridgeWebhookHandler returns the Bridge webhook handler
func (c *Container) GetBridgeWebhookHandler() *handlers.BridgeWebhookHandler {
	return c.BridgeWebhookHandler
}

// GetBridgeVirtualAccountService returns the Bridge virtual account service
func (c *Container) GetBridgeVirtualAccountService() *funding.BridgeVirtualAccountService {
	return c.BridgeVirtualAccountService
}

// initializeBridgeServices initializes Bridge-related services
func (c *Container) initializeBridgeServices() {
	if c.BridgeClient == nil {
		c.ZapLog.Warn("Bridge client not configured, skipping Bridge services initialization")
		return
	}

	// Bridge virtual account service will be initialized after allocation service
	// For now, just set up the webhook handler with a placeholder service
	webhookSecret := c.Config.Bridge.WebhookSecret
	if webhookSecret == "" {
		c.ZapLog.Warn("Bridge webhook secret not configured")
	}

	// Determine if webhook verification should be skipped (only in development)
	skipWebhookVerification := c.Config.Environment == "development" && webhookSecret == ""

	// Create a minimal webhook service for now
	// Full service will be wired after domain services are initialized
	c.BridgeWebhookHandler = handlers.NewBridgeWebhookHandler(
		nil, // Service will be set later
		c.ZapLog,
		webhookSecret,
		skipWebhookVerification,
	)

	c.ZapLog.Info("Bridge webhook handler initialized")
}
