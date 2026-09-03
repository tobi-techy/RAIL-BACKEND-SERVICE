package di

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/api/handlers"
	activityhandlers "github.com/rail-service/rail_service/internal/api/handlers/activity"
	evalhandlers "github.com/rail-service/rail_service/internal/api/handlers/eval"
	fundinghandlers "github.com/rail-service/rail_service/internal/api/handlers/funding"
	opportunityhandlers "github.com/rail-service/rail_service/internal/api/handlers/opportunities"
	p2phandlers "github.com/rail-service/rail_service/internal/api/handlers/p2p"
	platformhandlers "github.com/rail-service/rail_service/internal/api/handlers/platform"
	premiumhandlers "github.com/rail-service/rail_service/internal/api/handlers/premium"
	"github.com/rail-service/rail_service/internal/api/handlers/webhooks"
	"github.com/rail-service/rail_service/internal/domain/services"
	"github.com/rail-service/rail_service/internal/domain/services/account"
	aiservice "github.com/rail-service/rail_service/internal/domain/services/ai"
	aicore "github.com/rail-service/rail_service/internal/domain/services/ai/core"
	aimemory "github.com/rail-service/rail_service/internal/domain/services/ai/memory"
	aitools "github.com/rail-service/rail_service/internal/domain/services/ai/tools"
	"github.com/rail-service/rail_service/internal/domain/services/allocation"
	alpacaservice "github.com/rail-service/rail_service/internal/domain/services/alpaca"
	analyticsservice "github.com/rail-service/rail_service/internal/domain/services/analytics"
	"github.com/rail-service/rail_service/internal/domain/services/apikey"
	"github.com/rail-service/rail_service/internal/domain/services/audit"
	"github.com/rail-service/rail_service/internal/domain/services/autoinvest"
	"github.com/rail-service/rail_service/internal/domain/services/automation"
	"github.com/rail-service/rail_service/internal/domain/services/billpay"
	"github.com/rail-service/rail_service/internal/domain/services/card"
	compliancesvc "github.com/rail-service/rail_service/internal/domain/services/compliance"
	"github.com/rail-service/rail_service/internal/domain/services/consciousspending"
	conversationsvc "github.com/rail-service/rail_service/internal/domain/services/conversation"
	"github.com/rail-service/rail_service/internal/domain/services/copytrading"
	"github.com/rail-service/rail_service/internal/domain/services/document"
	"github.com/rail-service/rail_service/internal/domain/services/funding"
	"github.com/rail-service/rail_service/internal/domain/services/gameplay"
	"github.com/rail-service/rail_service/internal/domain/services/goals"
	"github.com/rail-service/rail_service/internal/domain/services/growthengine"
	"github.com/rail-service/rail_service/internal/domain/services/growthmail"
	"github.com/rail-service/rail_service/internal/domain/services/investing"
	knowledgesvc "github.com/rail-service/rail_service/internal/domain/services/knowledge"
	"github.com/rail-service/rail_service/internal/domain/services/ledger"
	"github.com/rail-service/rail_service/internal/domain/services/limits"
	marketservice "github.com/rail-service/rail_service/internal/domain/services/market"
	miriamservice "github.com/rail-service/rail_service/internal/domain/services/miriam"
	moneyguardservice "github.com/rail-service/rail_service/internal/domain/services/moneyguard"
	rampsvc "github.com/rail-service/rail_service/internal/domain/services/ramp"
	monosvc "github.com/rail-service/rail_service/internal/domain/services/mono"
	newsservice "github.com/rail-service/rail_service/internal/domain/services/news"
	obligationservice "github.com/rail-service/rail_service/internal/domain/services/obligation"
	"github.com/rail-service/rail_service/internal/domain/services/onboarding"
	opportunitysvc "github.com/rail-service/rail_service/internal/domain/services/opportunity"
	"github.com/rail-service/rail_service/internal/domain/services/p2p"
	"github.com/rail-service/rail_service/internal/domain/services/passcode"
	"github.com/rail-service/rail_service/internal/domain/services/premium"
	"github.com/rail-service/rail_service/internal/domain/services/reconciliation"
	"github.com/rail-service/rail_service/internal/domain/services/roundup"
	"github.com/rail-service/rail_service/internal/domain/services/security"
	"github.com/rail-service/rail_service/internal/domain/services/session"
	"github.com/rail-service/rail_service/internal/domain/services/sharedgoal"
	"github.com/rail-service/rail_service/internal/domain/services/socialauth"
	spendingcommitmentservice "github.com/rail-service/rail_service/internal/domain/services/spendingcommitment"
	"github.com/rail-service/rail_service/internal/domain/services/stashlock"
	"github.com/rail-service/rail_service/internal/domain/services/station"
	"github.com/rail-service/rail_service/internal/domain/services/strategy"
	subscriptionsvc "github.com/rail-service/rail_service/internal/domain/services/subscription"
	"github.com/rail-service/rail_service/internal/domain/services/travel"
	"github.com/rail-service/rail_service/internal/domain/services/twofa"
	"github.com/rail-service/rail_service/internal/domain/services/umbrawallet"
	usagesvc "github.com/rail-service/rail_service/internal/domain/services/usage"
	waitlistsvc "github.com/rail-service/rail_service/internal/domain/services/waitlist"
	"github.com/rail-service/rail_service/internal/domain/services/wallet"
	"github.com/rail-service/rail_service/internal/domain/services/webauthn"
	yieldsvc "github.com/rail-service/rail_service/internal/domain/services/yield"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/alpaca"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/blend"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/bridge"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/chainrails"
	circleadapter "github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/didit"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/publictrades"
	superteamadapter "github.com/rail-service/rail_service/internal/infrastructure/adapters/superteam"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/umbra"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	platform "github.com/rail-service/rail_service/internal/infrastructure/platform"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	supermemoryclient "github.com/rail-service/rail_service/internal/infrastructure/supermemory"
	"github.com/rail-service/rail_service/internal/infrastructure/vector"
	recon "github.com/rail-service/rail_service/internal/workers/reconciliation"
	revenue_sweep "github.com/rail-service/rail_service/internal/workers/revenue_sweep"
	"github.com/rail-service/rail_service/pkg/auth"
	"github.com/rail-service/rail_service/pkg/captcha"
	"github.com/rail-service/rail_service/pkg/jobqueue"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/rail-service/rail_service/pkg/ratelimit"
	"go.uber.org/zap"
)

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
	ReceiptRepo               *repositories.ReceiptRepository
	BankStatementRepo         *repositories.BankStatementRepository
	DocumentRepo              *repositories.DocumentRepository
	DocumentFileStore         document.FileStore
	MiriamMemoryRepo          *repositories.MiriamMemoryRepository
	BudgetRepo                *repositories.BudgetRepository
	BankAccountRepo           *repositories.BankAccountRepository
	FinancialProfileRepo      *repositories.FinancialProfileRepository
	FinancialObligationRepo   *repositories.FinancialObligationRepository
	AutomationRepo            *repositories.AutomationRepository
	SharedGoalRepo            *repositories.SharedGoalRepository
	ContextSignalRepo         *repositories.ContextSignalRepository
	LedgerSpendingRepo        *repositories.LedgerSpendingRepository
	ConversionRepo            *repositories.ConversionRepository
	BalanceRepo               *repositories.BalanceRepository
	FundingEventJobRepo       *repositories.FundingEventJobRepository
	SumsubWebhookEventRepo    *repositories.SumsubWebhookEventRepository
	KYCSyncJobRepo            *repositories.KYCSyncJobRepository
	LedgerRepo                *repositories.LedgerRepository
	ReconciliationRepo        repositories.ReconciliationRepository
	GrowthMailRepo            *repositories.GrowthMailRepository
	GrowthEngineRepo          *repositories.GrowthEngineRepository
	MonoRepo                  *repositories.MonoRepository

	// External Services
	AlpacaClient       *alpaca.Client
	AlpacaService      *alpaca.Service
	BridgeClient       *bridge.Client
	BridgeAdapter      *bridge.Adapter
	CircleAdapter      *circleadapter.Adapter
	UmbraClient        *umbra.Client
	UmbraWalletService *umbrawallet.Service
	EmailService       *adapters.EmailService
	SMSService         *adapters.SMSService
	AuditService       *adapters.AuditService
	RedisClient        cache.RedisClient
	SupermemoryClient  *supermemoryclient.Client
	QdrantStore        *vector.QdrantStore
	VectorizeStore     *vector.VectorizeStore

	// Bridge Domain Adapters
	BridgeKYCAdapter              *BridgeKYCAdapter
	BridgeVirtualAccountService   *funding.BridgeVirtualAccountService
	BridgeWebhookHandler          *handlers.BridgeWebhookHandler
	CircleWebhookHandler          *webhooks.CircleWebhookHandler
	BridgeCustomerStatusProcessor *webhooks.BridgeCustomerStatusProcessor

	// Domain Services
	OnboardingService              *onboarding.Service
	OnboardingJobService           *services.OnboardingJobService
	VerificationService            services.VerificationService
	PasscodeService                *passcode.Service
	SessionService                 *session.Service
	TwoFAService                   *twofa.Service
	APIKeyService                  *apikey.Service
	WalletService                  *wallet.Service
	FundingService                 *funding.Service
	InvestingService               *investing.Service
	BalanceService                 *services.BalanceService
	LedgerService                  *ledger.Service
	YieldService                   *yieldsvc.Service
	yieldRepo                      *repositories.YieldRepository
	ReconciliationService          *reconciliation.Service
	ReconciliationScheduler        *reconciliation.Scheduler
	StashReconciliation            *recon.Worker
	RevenueSweepWorker             *revenue_sweep.Worker
	BlendClient                    *blend.Client
	BlendDepositRouter             *blend.DepositRouter
	AllocationService              *allocation.Service
	AutoInvestService              *autoinvest.Service
	StrategyEngine                 *strategy.Engine
	StationService                 *station.Service
	GameplayXPService              *gameplay.XPService
	GameplayStreakService          *gameplay.StreakService
	GameplayChallengeService       *gameplay.ChallengeService
	GameplayAchievementService     *gameplay.AchievementService
	GameplayRepo                   *repositories.GameplayRepository
	GameplayHooks                  *gameplay.Hooks
	GameplayRingsService           *gameplay.RingsService
	GameplayBoostService           *gameplay.BoostService
	GameplayPointsService          *gameplay.PointsService
	GameplayGraceDayService        *gameplay.GraceDayService
	GameplayRecapService           *gameplay.RecapService
	SubscriptionService            *subscriptionsvc.Service
	FinancialObligationService     *obligationservice.Service
	MoneyGuardService              *moneyguardservice.Service
	MiriamIntelligenceService      *miriamservice.Service
	MiriamIntelligenceOrchestrator *miriamservice.IntelligenceOrchestrator
	MiriamSignalDetector           *miriamservice.SignalDetector
	MiriamPredictiveEngine         *miriamservice.PredictiveEngine
	MiriamDecisionEngine           *miriamservice.DecisionEngine
	MiriamProactiveNudgeEngine     *miriamservice.ProactiveNudgeEngine
	MiriamMandateSuggestionEngine  *miriamservice.MandateSuggestionEngine
	MiriamHealthScoreTracker       *miriamservice.HealthScoreTracker
	MiriamOutcomeTracker           *miriamservice.OutcomeTracker
	MiriamSelfReviewEngine         *miriamservice.SelfReviewEngine
	MiriamNotificationDispatcher   *miriamservice.NotificationDispatcher
	MiriamObligationDetector       *miriamservice.ObligationAutoDetector
	MiriamProactiveChatSender      miriamservice.ProactiveChatSender
	MiriamBridgeDispatcher         *platform.BridgeDispatcher
	AutomationService              *automation.Service
	BillPayService                 *billpay.Service
	TravelService                  *travel.Service
	SharedGoalService              *sharedgoal.Service
	GrowthMailService              *growthmail.Service
	GrowthEngineService            *growthengine.Service
	NotificationService            *services.NotificationService
	// GoalsService is the new Postgres-backed multi-goal service that powers
	// the v2 savings-goal tools + the goal_progress + spending_coach workers.
	GoalsService *goals.Service
	// UserGoalRepo is the persistence layer behind GoalsService.
	UserGoalRepo                 *repositories.UserGoalRepository
	ConsciousSpendingPlanService *consciousspending.Service
	ConsciousSpendingPlanRepo    *repositories.ConsciousSpendingPlanRepository
	// BabyStepsSeeder seeds the 7-step ladder for first-time users.
	BabyStepsSeeder *goals.BabyStepsSeed
	// GoalProgressHooks is the optional deposit-allocated callback wired
	// into automation.Service.
	GoalProgressHooks *GoalProgressHooks
	// ProactiveCoordinator is the unified per-user daily-cap enforcer for
	// all proactive workers (autopilot, ai_insights, daily_pulse,
	// scheduled_notifications, goal_progress, spending_coach).
	ProactiveCoordinator *platform.ProactiveCoordinator
	// AICostGuard is the fast Redis-backed per-user daily/monthly AI cost
	// ceiling. Injected into both the Cencori provider (provider-level check)
	// and core.Agent.Dependencies (agent-level pre-check). nil disables the
	// guard; Cencori will continue without ceiling enforcement.
	AICostGuard               *ai.Guard
	SocialAuthService         *socialauth.Service
	WebAuthnService           *webauthn.Service
	LimitsService             *limits.Service
	SpendingCommitmentService *spendingcommitmentservice.Service
	DomainAuditService        *audit.Service
	WithdrawalService         *services.WithdrawalService
	StashLockService          *stashlock.Service

	// AI Financial Manager Services
	AIProvider               ai.AIProvider
	AIOrchestrator           *aiservice.AgentAdapter
	NewAgent                 *aicore.Agent
	AgentDeps                *aicore.Dependencies
	NewChatEngine            aiservice.ChatEngine
	NewToolRegistry          *aitools.Registry
	DiditClient              *didit.Client
	ComplianceService        *compliancesvc.Service
	AIRecommender            *aiservice.Recommender
	NewsService              *newsservice.Service
	PortfolioDataProvider    *aiservice.PortfolioDataProviderImpl
	ActivityDataProvider     *aiservice.ActivityDataProviderImpl
	ConversationRepo         *repositories.ConversationRepository
	ConversationService      *conversationsvc.Service
	UsageRepo                *repositories.AIUsageRepository
	UsageService             *usagesvc.Service
	EmbeddingsClient         Embedder
	KnowledgeRepo            *repositories.KnowledgeRepository
	KnowledgeService         *knowledgesvc.Service
	MemoryService            *aiservice.MemoryService
	WorkingMemoryStore       *aimemory.WorkingMemoryStore
	EventStore               *aimemory.EventStore
	MemoryMetrics            *aimemory.Metrics
	MiriamIntelligenceRepo   *repositories.MiriamIntelligenceRepository
	MiriamPreferencesRepo    *repositories.MiriamPreferencesRepository
	MiriamPreferencesService *miriamservice.PreferencesService
	AnomalyStore             aiservice.AnomalyStore
	AnomalyEngine            *aiservice.AnomalyEngine
	proactiveGuard           *platform.ProactiveGuard // set during platform init; prefs wired later

	// Additional Repositories
	OnboardingJobRepo *repositories.OnboardingJobRepository

	// Alpaca Investment Repositories
	AlpacaAccountRepo        *repositories.AlpacaAccountRepository
	InvestmentOrderRepo      *repositories.InvestmentOrderRepository
	InvestmentPositionRepo   *repositories.InvestmentPositionRepository
	AlpacaEventRepo          *repositories.AlpacaEventRepository
	AlpacaInstantFundingRepo *repositories.AlpacaInstantFundingRepository

	// Advanced Features Repositories
	PortfolioSnapshotRepo   *repositories.PortfolioSnapshotRepository
	ScheduledInvestmentRepo *repositories.ScheduledInvestmentRepository
	RebalancingConfigRepo   *repositories.RebalancingConfigRepository
	InvestmentRulesRepo     *repositories.InvestmentRulesRepository
	MarketAlertRepo         *repositories.MarketAlertRepository

	// Alpaca Investment Services
	AlpacaAccountService *alpacaservice.AccountService
	AlpacaFundingBridge  *alpacaservice.FundingBridge
	AlpacaEventProcessor *alpacaservice.EventProcessor
	AlpacaPortfolioSync  *alpacaservice.PortfolioSyncService

	// Advanced Features Services
	PortfolioAnalyticsService  *analyticsservice.PortfolioAnalyticsService
	MarketDataService          *marketservice.MarketDataService
	ScheduledInvestmentService *investing.ScheduledInvestmentService
	RebalancingService         *investing.RebalancingService

	// Brokerage Adapter
	BrokerageAdapter *adapters.BrokerageAdapter

	// Round-up Services
	RoundupRepo    *repositories.RoundupRepository
	RoundupService *roundup.Service

	// Copy Trading Services
	CopyTradingRepo    *repositories.CopyTradingRepository
	CopyTradingService *copytrading.Service
	PublicTradesClient *publictrades.Client

	// Card Services
	CardRepo    *repositories.CardRepository
	CardService *card.Service

	// Workers
	WalletProvisioningScheduler interface{} // Type interface{} to avoid circular dependency, will be set at runtime
	FundingWebhookManager       interface{} // Type interface{} to avoid circular dependency, will be set at runtime

	// Cache & Queue
	CacheInvalidator *cache.CacheInvalidator
	JobQueue         interface{} // Job queue for background processing
	JobQueueInstance *jobqueue.JobQueue
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
	OnboardingFraudService  *security.OnboardingFraudService

	// Token and Rate Limiting
	TokenBlacklist      *auth.TokenBlacklist
	JWTService          *auth.JWTService
	TieredRateLimiter   *ratelimit.TieredLimiter
	LoginAttemptTracker *ratelimit.LoginAttemptTracker
	CaptchaVerifier     *captcha.Verifier

	// Device-Bound JWT (Priority 1)
	DeviceSessionRepo      *repositories.DeviceSessionRepository
	DeviceBindingAuditRepo *repositories.DeviceBindingAuditRepository
	DeviceBoundJWTService  *auth.DeviceBoundJWTService

	// Adaptive Rate Limiting (Priority 3)
	RiskScoringEngine   *ratelimit.RiskScoringEngine
	AdaptiveRateLimiter *ratelimit.AdaptiveRateLimiter

	// Instant Funding Services
	InstantFundingRepo         *repositories.InstantFundingRepository
	UserAccountRepo            *repositories.UserAccountRepository
	InstantFundingService      *funding.InstantFundingService
	InstantFundingHandlers     *fundinghandlers.InstantFundingHandlers
	ChainRailsHandlers         *fundinghandlers.ChainRailsHandlers
	ChainRailsClient           *chainrails.Client
	DepositSweepRepo           *repositories.DepositSweepRepository
	PajHandlers                *fundinghandlers.PajHandlers
	RampHandlers               *fundinghandlers.RampHandlers
	RampService                *rampsvc.Service
	NGNHandlers                *fundinghandlers.NGNHandlers
	GraphVirtualAccountService *funding.GraphVirtualAccountService
	GraphWebhookHandler        *webhooks.GraphWebhookHandler
	BillPayHandlers            *fundinghandlers.BillPayHandlers
	ActivityHandlers           *activityhandlers.Handlers

	// Security Stores
	WithdrawalSecurityStore *repositories.WithdrawalSecurityStore
	DepositSecurityStore    *repositories.DepositSecurityStore

	// Account Management
	AccountDeletionService *account.DeletionService

	// P2P Transfer Services
	P2PRepo               *repositories.P2PRepository
	P2PService            *p2p.Service
	P2PNotificationSender *adapters.P2PNotificationSender
	P2PHandlers           *p2phandlers.Handlers

	// Notification Services
	DeviceTokenRepo      *repositories.DeviceTokenRepository
	NotificationRepo     *repositories.NotificationRepository
	ExpoPushService      *adapters.ExpoPushService
	OneSignalPushService *adapters.OneSignalPushService
	SNSPushService       *adapters.SNSPushService

	// Unified Webhook Handler
	UnifiedFundingWebhookHandler *webhooks.UnifiedFundingWebhookHandler

	// Security Features (v2) - Risk Scoring, Whitelist, Anomaly, Limits, Adaptive MFA
	SecurityFeaturesRepo    *repositories.SecurityFeaturesRepository
	RiskScoringService      *security.RiskScoringService
	AddressWhitelistService *security.AddressWhitelistService
	SessionAnomalyService   *security.SessionAnomalyService
	WithdrawalLimitsService *security.WithdrawalLimitsService
	AdaptiveMFAService      *security.AdaptiveMFAService
	DeviceSecurityService   *security.DeviceSecurityService

	// Premium Feature Repositories
	FamilySupportRepo *repositories.FamilySupportRepository
	ScamRepo          *repositories.ScamRepository
	TaxResidencyRepo  *repositories.TaxResidencyRepository
	WellnessRepo      *repositories.WellnessRepository
	EmergencyRepo     *repositories.EmergencyRepository
	ExchangeRateRepo  *repositories.ExchangeRateRepository
	VisaProofRepo     *repositories.VisaProofRepository
	ReceiptSplitRepo  *repositories.ReceiptSplitRepository

	// Premium Feature Services
	NairaShieldService      *premium.NairaShieldService
	BlackTaxService         *premium.BlackTaxService
	ReceiptSplitService     *premium.ReceiptSplitService
	ScamIntelligenceService *premium.ScamIntelligenceService
	TaxResidencyService     *premium.TaxResidencyService
	IncomeSmoothingService  *premium.IncomeSmoothingService
	FinancialTraumaService  *premium.FinancialTraumaService
	VisaProofService        *premium.VisaProofService
	PanicButtonService      *premium.PanicButtonService
	PremiumHandlers         *premiumhandlers.Handlers

	// Opportunity Intelligence
	OpportunityRepo    *repositories.OpportunityRepository
	OpportunityService *opportunitysvc.Service

	// Waitlist
	WaitlistRepo    *repositories.WaitlistRepository
	WaitlistService *waitlistsvc.Service

	// Admin Analytics
	AdminAnalyticsRepo    *repositories.AnalyticsRepository
	AdminAnalyticsService *analyticsservice.Service

	// Platform Messaging (iMessage, WhatsApp, Telegram)
	PlatformIdentityRepo *repositories.PlatformIdentityRepository
	PlatformHandler      *platformhandlers.PlatformHandler
	platformProcessor    *platform.Processor
	platformLinking      *platform.LinkingService
	ConfirmHandler       *platform.ConfirmHandler
	ConfirmTokenStore    *platform.ConfirmTokenStore
	EvalHandler          *evalhandlers.Handler

	// Mono (open-banking data + DirectPay)
	MonoService        *monosvc.Service
	MonoWebhookHandler *webhooks.MonoWebhookHandler
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
	receiptRepo := repositories.NewReceiptRepository(sqlxDB)
	bankStatementRepo := repositories.NewBankStatementRepository(sqlxDB)
	documentRepo := repositories.NewDocumentRepository(sqlxDB)
	budgetRepo := repositories.NewBudgetRepository(sqlxDB)
	financialProfileRepo := repositories.NewFinancialProfileRepository(sqlxDB)
	financialObligationRepo := repositories.NewFinancialObligationRepository(sqlxDB)
	automationRepo := repositories.NewAutomationRepository(sqlxDB)
	sharedGoalRepo := repositories.NewSharedGoalRepository(sqlxDB)
	ledgerSpendingRepo := repositories.NewLedgerSpendingRepository(sqlxDB)
	conversionRepo := repositories.NewConversionRepository(sqlxDB)
	balanceRepo := repositories.NewBalanceRepository(db, zapLog)
	fundingEventJobRepo := repositories.NewFundingEventJobRepository(db, log)
	sumsubWebhookEventRepo := repositories.NewSumsubWebhookEventRepository(db, zapLog)
	kycSyncJobRepo := repositories.NewKYCSyncJobRepository(db, zapLog)
	ledgerRepo := repositories.NewLedgerRepository(sqlxDB)
	reconciliationRepo := repositories.NewPostgresReconciliationRepository(db)
	onboardingJobRepo := repositories.NewOnboardingJobRepository(db, zapLog)
	growthMailRepo := repositories.NewGrowthMailRepository(db)
	growthEngineRepo := repositories.NewGrowthEngineRepository(db)

	// Initialize premium feature repositories
	familySupportRepo := repositories.NewFamilySupportRepository(sqlxDB)
	scamRepo := repositories.NewScamRepository(sqlxDB)
	taxResidencyRepo := repositories.NewTaxResidencyRepository(sqlxDB)
	wellnessRepo := repositories.NewWellnessRepository(sqlxDB)
	emergencyRepo := repositories.NewEmergencyRepository(sqlxDB)
	exchangeRateRepo := repositories.NewExchangeRateRepository(sqlxDB)
	visaProofRepo := repositories.NewVisaProofRepository(sqlxDB)
	receiptSplitRepo := repositories.NewReceiptSplitRepository(sqlxDB)

	// Initialize external services
	// Initialize Alpaca service
	alpacaConfig := alpaca.Config{
		ClientID:      cfg.Alpaca.ClientID,
		SecretKey:     cfg.Alpaca.SecretKey,
		BaseURL:       cfg.Alpaca.BaseURL,
		DataBaseURL:   cfg.Alpaca.DataBaseURL,
		DataAPIKey:    cfg.Alpaca.DataAPIKey,
		DataAPISecret: cfg.Alpaca.DataAPISecret,
		DataFeed:      cfg.Alpaca.DataFeed,
		Environment:   cfg.Alpaca.Environment,
		Timeout:       time.Duration(cfg.Alpaca.Timeout) * time.Second,
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

	// Initialize Circle Programmable Wallets client
	var circleAdapter *circleadapter.Adapter
	if cfg.Circle.APIKey != "" && cfg.Circle.EntitySecret != "" {
		circleClient, circleErr := circleadapter.NewHTTPClient(circleadapter.Config{
			APIKey:             cfg.Circle.APIKey,
			EntitySecret:       cfg.Circle.EntitySecret,
			PublicKeyPEM:       cfg.Circle.PublicKeyPEM,
			BaseURL:            cfg.Circle.BaseURL,
			Timeout:            time.Duration(cfg.Circle.Timeout) * time.Second,
			MaxRetries:         cfg.Circle.MaxRetries,
			DefaultWalletSetID: cfg.Circle.DefaultWalletSetID,
		}, zapLog)
		if circleErr != nil {
			zapLog.Warn("Circle client init failed, wallet features degraded", zap.Error(circleErr))
		} else {
			isSandbox := cfg.Circle.Environment == "sandbox"
			if isSandbox {
				circleAdapter = circleadapter.NewSandboxAdapter(circleClient, zapLog)
			} else {
				circleAdapter = circleadapter.NewAdapter(circleClient, zapLog)
			}
			zapLog.Info("Circle Programmable Wallets initialized", zap.Bool("sandbox", isSandbox))
		}
	} else {
		zapLog.Info("Circle Programmable Wallets not configured (missing API key or entity secret)")
	}

	// Initialize Umbra privacy sidecar client
	var umbraClient *umbra.Client
	if cfg.Umbra.Enabled && cfg.Umbra.SidecarURL != "" {
		umbraClient = umbra.NewClient(cfg.Umbra.SidecarURL, cfg.Umbra.AuthToken)
		zapLog.Info("Umbra privacy sidecar enabled", zap.String("url", cfg.Umbra.SidecarURL), zap.String("network", cfg.Umbra.Network))
	} else {
		zapLog.Info("Umbra privacy sidecar disabled")
	}

	// Initialize email service with Unosend configuration
	var err error
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
		ReceiptRepo:               receiptRepo,
		BankStatementRepo:         bankStatementRepo,
		DocumentRepo:              documentRepo,
		BudgetRepo:                budgetRepo,
		FinancialProfileRepo:      financialProfileRepo,
		FinancialObligationRepo:   financialObligationRepo,
		AutomationRepo:            automationRepo,
		SharedGoalRepo:            sharedGoalRepo,
		LedgerSpendingRepo:        ledgerSpendingRepo,
		ConversionRepo:            conversionRepo,
		BalanceRepo:               balanceRepo,
		FundingEventJobRepo:       fundingEventJobRepo,
		SumsubWebhookEventRepo:    sumsubWebhookEventRepo,
		KYCSyncJobRepo:            kycSyncJobRepo,
		LedgerRepo:                ledgerRepo,
		ReconciliationRepo:        reconciliationRepo,
		OnboardingJobRepo:         onboardingJobRepo,
		GrowthMailRepo:            growthMailRepo,
		GrowthEngineRepo:          growthEngineRepo,
		FamilySupportRepo:         familySupportRepo,
		ScamRepo:                  scamRepo,
		TaxResidencyRepo:          taxResidencyRepo,
		WellnessRepo:              wellnessRepo,
		EmergencyRepo:             emergencyRepo,
		ExchangeRateRepo:          exchangeRateRepo,
		VisaProofRepo:             visaProofRepo,
		ReceiptSplitRepo:          receiptSplitRepo,
		yieldRepo:                 repositories.NewYieldRepository(sqlxDB),
		DeviceTokenRepo:           repositories.NewDeviceTokenRepository(db),
		NotificationRepo:          repositories.NewNotificationRepository(db),

		// External Services
		AlpacaClient:  alpacaClient,
		AlpacaService: alpacaService,
		BridgeClient:  bridgeClient,
		BridgeAdapter: bridgeAdapter,
		CircleAdapter: circleAdapter,
		UmbraClient:   umbraClient,
		AuditService:  auditService,
		RedisClient:   redisClient,

		// Bridge Domain Adapters
		BridgeKYCAdapter: NewBridgeKYCAdapter(bridgeAdapter, userRepo),

		// Cache & Queue
		CacheInvalidator: cacheInvalidator,
		JobQueueInstance: jobqueue.NewJobQueue(redisClient.Client(), zapLog),
	}

	// Store the optional email/SMS services only when actually constructed.
	// Assigning a nil *adapters.X pointer directly would create a typed-nil
	// interface downstream (x != nil is true, calling a method panics on the
	// nil receiver) — exactly the failure that crashed chat onboarding when
	// Twilio was unconfigured.
	if emailService != nil {
		container.EmailService = emailService
	}
	if smsService != nil {
		container.SMSService = smsService
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

	container.OnboardingJobService = services.NewOnboardingJobService(container.OnboardingJobRepo, container.ZapLog, convertWalletChains(cfg.Bridge.SupportedChains, container.ZapLog))

	if container.EmailService != nil {
		container.GrowthMailService = growthmail.NewService(
			container.GrowthMailRepo,
			container.EmailService,
			growthmail.Config{BaseURL: cfg.Email.BaseURL, Limit: 500},
			container.ZapLog,
		)
	}

	// Initialize opportunity intelligence
	container.initializeOpportunityService(sqlxDB)

	// Initialize waitlist
	container.WaitlistRepo = repositories.NewWaitlistRepository(db, zapLog)
	container.WaitlistService = waitlistsvc.NewService(container.WaitlistRepo, zapLog)

	// Initialize admin analytics
	container.AdminAnalyticsRepo = repositories.NewAnalyticsRepository(db, zapLog)
	container.AdminAnalyticsService = analyticsservice.NewService(container.AdminAnalyticsRepo, container.RedisClient, zapLog)

	// Initialize platform messaging (iMessage, WhatsApp, Telegram)
	container.initializePlatformMessaging()

	// Chat-first onboarding for unlinked senders — must run after
	// initializePlatformMessaging (processor/linking) and after
	// VerificationService + OnboardingService exist.
	container.wireChatOnboarding()

	// Initialize Mono open-banking adapter + service (gated on API key)
	if err := container.initializeMono(); err != nil {
		zapLog.Warn("Mono initialization failed, open-banking features degraded", zap.Error(err))
	}

	// Miriam evaluation endpoint (terminal test harness). Gated + token-guarded.
	if cfg.Eval.Enabled && container.AIOrchestrator != nil && cfg.Eval.Token != "" {
		container.EvalHandler = evalhandlers.NewHandler(
			container.AIOrchestrator, container.ConversationService, cfg.Eval.Token, zapLog,
		)
		zapLog.Warn("Miriam eval endpoint ENABLED — do not enable in production")
	}

	return container, nil
}
func (c *Container) initializeOpportunityService(sqlxDB *sqlx.DB) {
	superteamAPIKey := os.Getenv("SUPERTEAM_EARN_API_KEY")
	if superteamAPIKey == "" {
		c.ZapLog.Warn("SUPERTEAM_EARN_API_KEY not set, opportunity service disabled")
		return
	}

	superteamClient := superteamadapter.NewClient(superteamadapter.Config{
		APIKey: superteamAPIKey,
	}, c.ZapLog)

	c.OpportunityRepo = repositories.NewOpportunityRepository(sqlxDB)
	c.OpportunityService = opportunitysvc.NewService(c.OpportunityRepo, superteamClient, c.ZapLog)
}

// GetOpportunityHandlers returns opportunity HTTP handlers.
func (c *Container) GetOpportunityHandlers() *opportunityhandlers.Handlers {
	if c.OpportunityService == nil {
		return nil
	}
	return opportunityhandlers.NewHandlers(c.OpportunityService, c.ZapLog)
}

// GetPlatformProcessor returns the platform message processor, or nil if platform messaging is disabled.
func (c *Container) GetPlatformProcessor() *platform.Processor {
	return c.platformProcessor
}

// GetConfirmHandler returns the email confirmation handler, or nil if platform messaging is disabled.
func (c *Container) GetConfirmHandler() *platform.ConfirmHandler {
	return c.ConfirmHandler
}

// GetConfirmTokenStore returns the confirm token store, or nil if platform messaging is disabled.
func (c *Container) GetConfirmTokenStore() *platform.ConfirmTokenStore {
	return c.ConfirmTokenStore
}

// GetEmailService returns the email service.
func (c *Container) GetEmailService() *adapters.EmailService {
	return c.EmailService
}

// GetUserRepo returns the user repository.
func (c *Container) GetUserRepo() *repositories.UserRepository {
	return c.UserRepo
}
