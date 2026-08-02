package routes

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/api/handlers"
	admin_handlers "github.com/rail-service/rail_service/internal/api/handlers/admin"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	kychandlers "github.com/rail-service/rail_service/internal/api/handlers/kyc"
	securityHandlersV2 "github.com/rail-service/rail_service/internal/api/handlers/security"
	waitlisthandlers "github.com/rail-service/rail_service/internal/api/handlers/waitlist"
	"github.com/rail-service/rail_service/internal/api/middleware"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services"
	aiservice "github.com/rail-service/rail_service/internal/domain/services/ai"
	kycservice "github.com/rail-service/rail_service/internal/domain/services/kyc"
	"github.com/rail-service/rail_service/internal/domain/services/session"
	statement "github.com/rail-service/rail_service/internal/domain/services/statement"
	alpacaadapter "github.com/rail-service/rail_service/internal/infrastructure/adapters/alpaca"
	diditadapter "github.com/rail-service/rail_service/internal/infrastructure/adapters/didit"
	sumsubadapter "github.com/rail-service/rail_service/internal/infrastructure/adapters/sumsub"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/rail-service/rail_service/internal/infrastructure/di"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	supermemoryclient "github.com/rail-service/rail_service/internal/infrastructure/supermemory"
	"github.com/rail-service/rail_service/pkg/alerting"
	"github.com/rail-service/rail_service/pkg/analytics"
	"github.com/rail-service/rail_service/pkg/ratelimit"
	"github.com/rail-service/rail_service/pkg/tracing"
)

type SessionValidatorAdapter struct {
	svc *session.Service
}

type WithdrawalWalletProviderAdapter struct {
	getWalletByUserAndChain func(context.Context, uuid.UUID, entities.WalletChain) (*entities.ManagedWallet, error)
}

func (a *WithdrawalWalletProviderAdapter) GetUserWalletByChain(ctx context.Context, userID uuid.UUID, chain string) (*entities.ManagedWallet, error) {
	if a == nil || a.getWalletByUserAndChain == nil {
		return nil, fmt.Errorf("wallet provider not configured")
	}

	normalized := entities.WalletChain(strings.ToUpper(strings.TrimSpace(chain)))
	wallet, err := a.getWalletByUserAndChain(ctx, userID, normalized)
	if err == nil {
		return wallet, nil
	}

	return nil, err
}

func NewSessionValidatorAdapter(svc *session.Service) *SessionValidatorAdapter {
	return &SessionValidatorAdapter{svc: svc}
}

func (a *SessionValidatorAdapter) ValidateSession(ctx context.Context, token string) (*middleware.SessionInfo, error) {
	sess, err := a.svc.ValidateSession(ctx, token)
	if err != nil {
		return nil, err
	}
	return &middleware.SessionInfo{
		ID:     sess.ID,
		UserID: sess.UserID,
	}, nil
}

// receiptMemoryAdapter adapts supermemory.Client to the ReceiptMemoryStore interface.
type receiptMemoryAdapter struct {
	client interface {
		CreateMemories(ctx context.Context, containerTag string, memories []supermemoryclient.Memory) error
	}
}

func (a *receiptMemoryAdapter) StoreMemory(ctx context.Context, userID string, content string, eventDate string, metadata map[string]string) error {
	return a.client.CreateMemories(ctx, userID, []supermemoryclient.Memory{{
		Content:  content,
		Metadata: metadata,
	}})
}

// SetupRoutes configures all application routes
func SetupRoutes(container *di.Container) *gin.Engine {
	router := gin.New()

	// Configure trusted proxies for secure IP detection in rate limiting
	// This prevents IP spoofing via X-Forwarded-For headers
	// In production, set this to your actual proxy/load balancer IPs
	trustedProxies := container.Config.Server.TrustedProxies
	if len(trustedProxies) == 0 {
		// Default: trust only localhost (for local development with nginx/proxy)
		trustedProxies = []string{"127.0.0.1", "::1"}
	}
	if err := router.SetTrustedProxies(trustedProxies); err != nil {
		// Log warning but continue - ClientIP will fall back to RemoteAddr
		container.Logger.Warn("Failed to set trusted proxies: %v", err)
	}

	// Lightweight ping — no middleware, no DB, for uptime monitoring
	router.GET("/ping", func(c *gin.Context) {
		c.String(http.StatusOK, "pong")
	})

	// Global middleware - order matters for security
	router.Use(tracing.HTTPMiddleware()) // Tracing should be early in the chain
	router.Use(middleware.RequestID())
	router.Use(middleware.MetricsMiddleware())
	router.Use(middleware.RequestSizeLimit())
	router.Use(middleware.InputValidation())
	router.Use(middleware.Recovery(container.Logger))
	router.Use(middleware.ScannerBlock(container.ZapLog))
	router.Use(middleware.Logger(container.Logger))
	router.Use(middleware.ErrorAlerter(alerting.NewTelegramAlerter(
		container.Config.TelegramAlerts.BotToken,
		container.Config.TelegramAlerts.ChatID,
	)))
	router.Use(middleware.CORS(container.Config.Server.AllowedOrigins, container.Config.Environment))
	router.Use(createRateLimitMiddleware(container))
	router.Use(middleware.SecurityHeaders())
	router.Use(analytics.Middleware())
	router.Use(middleware.DeviceFingerprintExtractor())
	router.Use(middleware.APIVersionMiddleware(container.Config.Server.SupportedVersions))
	router.Use(middleware.PaginationMiddleware())

	// CSRF protection
	csrfStore := middleware.NewCSRFStore()
	router.Use(middleware.CSRFToken(csrfStore))

	// Initialize handlers with services from DI container
	coreHandlers := handlers.NewCoreHandlers(container.DB, container.Logger)
	allocationHandlers := handlers.NewAllocationHandlers(
		container.GetAllocationService(),
		container.Logger,
	)

	// Health checks (no auth required)
	router.GET("/", coreHandlers.Health)
	router.GET("/health", coreHandlers.Health)
	router.GET("/ready", coreHandlers.Ready)
	router.GET("/live", coreHandlers.Live)
	router.GET("/version", coreHandlers.Version)

	// /metrics requires internal API key — exposes internal system state
	router.GET("/metrics", middleware.InternalAPIKeyAuth(container.Config.Security.InternalAPIKey), coreHandlers.Metrics)

	// Internal ops endpoints — protected by group-level INTERNAL_API_KEY middleware
	// Rate limited: 5 requests/minute to prevent abuse
	internalHandlers := handlers.NewInternalHandlers(container.DB, container.Config.Security.InternalAPIKey, container.ZapLog)
	internal := router.Group("/internal")
	internal.Use(middleware.RateLimit(5))
	internal.Use(middleware.InternalAPIKeyAuth(container.Config.Security.InternalAPIKey))
	// Defense-in-depth: when an internal signing secret is configured, require
	// a fresh HMAC-SHA256 request signature so a leaked static key alone cannot
	// drive money-moving internal routes (TM-001). No-op until configured.
	internal.Use(middleware.InternalRequestSignature(container.Config.Security.InternalRequestSigningSecret, container.ZapLog))
	{
		internal.GET("/users/lookup", internalHandlers.LookupUser)
		internal.DELETE("/users/:id", internalHandlers.DeleteUser)
		internal.POST("/paj-orders/complete-stuck", internalHandlers.CompleteStuckPajOrders)
	}

	// Internal statement management.
	//
	// Order matters: delete child rows (bank_statement_transactions) BEFORE
	// the parent upload so a partial failure can't leave orphaned transactions
	// pointing at a deleted upload. Both deletes share a single transaction so
	// the operation is all-or-nothing.
	internal.DELETE("/statement/:id", func(c *gin.Context) {
		id := c.Param("id")
		tx, err := container.DB.BeginTx(c.Request.Context(), nil)
		if err != nil {
			container.ZapLog.Error("internal statement delete: begin tx failed",
				zap.String("upload_id", id), zap.Error(err))
			c.JSON(500, gin.H{"error": "failed to start transaction"})
			return
		}
		rollback := func() {
			if rbErr := tx.Rollback(); rbErr != nil && rbErr != sql.ErrTxDone {
				container.ZapLog.Warn("internal statement delete: rollback failed",
					zap.String("upload_id", id), zap.Error(rbErr))
			}
		}
		if _, err := tx.ExecContext(c.Request.Context(),
			"DELETE FROM bank_statement_transactions WHERE upload_id = $1", id); err != nil {
			rollback()
			container.ZapLog.Error("internal statement delete: transactions delete failed",
				zap.String("upload_id", id), zap.Error(err))
			c.JSON(500, gin.H{"error": "failed to delete statement transactions"})
			return
		}
		if _, err := tx.ExecContext(c.Request.Context(),
			"DELETE FROM bank_statement_uploads WHERE id = $1", id); err != nil {
			rollback()
			container.ZapLog.Error("internal statement delete: upload delete failed",
				zap.String("upload_id", id), zap.Error(err))
			c.JSON(500, gin.H{"error": "failed to delete statement upload"})
			return
		}
		if err := tx.Commit(); err != nil {
			container.ZapLog.Error("internal statement delete: commit failed",
				zap.String("upload_id", id), zap.Error(err))
			c.JSON(500, gin.H{"error": "failed to commit statement delete"})
			return
		}
		c.JSON(200, gin.H{"deleted": true})
	})

	// Purge all statements for a user (resets dedup)
	internal.DELETE("/statement/user/:user_id", func(c *gin.Context) {
		uid := c.Param("user_id")
		_, err := container.DB.ExecContext(c.Request.Context(),
			"DELETE FROM bank_statement_transactions WHERE upload_id IN (SELECT id FROM bank_statement_uploads WHERE user_id = $1)", uid)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		res, err := container.DB.ExecContext(c.Request.Context(),
			"DELETE FROM bank_statement_uploads WHERE user_id = $1", uid)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		n, _ := res.RowsAffected()
		c.JSON(200, gin.H{"deleted": true, "count": n})
	})

	if container.GrowthEngineService != nil {
		growthHandlers := handlers.NewGrowthEngineHandlers(container.GrowthEngineService, container.ZapLog)
		internal.POST("/growth/events", growthHandlers.TrackEvent)
		internal.POST("/growth/run", growthHandlers.RunSegmentation)
		internal.GET("/growth/whatsapp-export", growthHandlers.ManualWhatsAppExport)
	}

	// Internal knowledge ingestion — auth handled by group middleware
	if container.GetKnowledgeService() != nil {
		knowledgeHandlers := handlers.NewKnowledgeHandlers(container.GetKnowledgeService(), container.ZapLog)
		internal.POST("/knowledge/ingest", knowledgeHandlers.Ingest)
	}

	// Manual deposit credit — auth handled by group middleware
	if container.FundingService != nil {
		internal.POST("/deposit/credit", func(c *gin.Context) {
			var req struct {
				Address string `json:"address" binding:"required"`
				Amount  string `json:"amount" binding:"required"`
				TxHash  string `json:"tx_hash" binding:"required"`
				Chain   string `json:"chain"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(400, gin.H{"error": err.Error()})
				return
			}

			// Security: cap single deposit credits at $10,000 USD to limit blast radius of compromised keys
			amt, err := decimal.NewFromString(req.Amount)
			if err != nil || amt.LessThanOrEqual(decimal.Zero) {
				c.JSON(400, gin.H{"error": "invalid amount"})
				return
			}
			maxDeposit := decimal.NewFromInt(10000)
			if amt.GreaterThan(maxDeposit) {
				c.JSON(400, gin.H{"error": "amount exceeds maximum allowed deposit of 10000 USD"})
				return
			}

			// Audit trail for internal deposit credits
			container.Logger.Info("internal deposit credit requested",
				zap.String("client_ip", c.ClientIP()),
				zap.String("amount", req.Amount),
				zap.String("address", req.Address),
				zap.String("tx_hash", req.TxHash),
			)
			chain := strings.ToUpper(req.Chain)
			if chain == "" {
				chain = "BASE"
			}
			deposit := &entities.ChainDepositWebhook{
				Chain:     entities.Chain(chain),
				Address:   req.Address,
				Token:     entities.StablecoinUSDC,
				Amount:    req.Amount,
				TxHash:    req.TxHash,
				BlockTime: time.Now(),
			}
			if err := container.FundingService.ProcessChainDeposit(c.Request.Context(), deposit); err != nil {
				c.JSON(500, gin.H{"error": err.Error()})
				return
			}
			c.JSON(200, gin.H{"status": "credited"})
		})
	}

	// Internal stash-to-spend transfer — bypasses stash lock, auth handled by group middleware
	// Security: amount capped at $500, full audit trail with caller IP
	internal.POST("/stash/transfer-to-spend", func(c *gin.Context) {
		var req struct {
			UserID string `json:"user_id" binding:"required"`
			Amount string `json:"amount" binding:"required"`
			Reason string `json:"reason" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		userID, err := uuid.Parse(req.UserID)
		if err != nil {
			c.JSON(400, gin.H{"error": "invalid user_id"})
			return
		}
		amt, err := decimal.NewFromString(req.Amount)
		if err != nil || amt.LessThanOrEqual(decimal.Zero) {
			c.JSON(400, gin.H{"error": "amount must be positive"})
			return
		}
		maxTransfer := decimal.NewFromInt(500)
		if amt.GreaterThan(maxTransfer) {
			c.JSON(400, gin.H{"error": "amount exceeds maximum of $500"})
			return
		}

		// Audit: log before execution with caller IP for forensics
		container.ZapLog.Warn("internal stash-to-spend transfer REQUESTED",
			zap.String("user_id", req.UserID),
			zap.String("amount", req.Amount),
			zap.String("reason", req.Reason),
			zap.String("caller_ip", c.ClientIP()),
			zap.String("user_agent", c.Request.UserAgent()),
		)

		idempotencyKey := fmt.Sprintf("admin_stash_xfer_%s_%d", userID.String(), time.Now().UnixNano())
		if err := container.LedgerService.AdminTransferStashToSpending(c.Request.Context(), userID, amt, idempotencyKey, req.Reason); err != nil {
			container.ZapLog.Error("internal stash-to-spend transfer FAILED", zap.String("user_id", req.UserID), zap.String("amount", req.Amount), zap.Error(err))
			c.JSON(500, gin.H{"error": "transfer failed, check server logs"})
			return
		}
		container.ZapLog.Warn("internal stash-to-spend transfer COMPLETED",
			zap.String("user_id", req.UserID),
			zap.String("amount", req.Amount),
			zap.String("caller_ip", c.ClientIP()),
		)
		c.JSON(200, gin.H{"status": "completed", "user_id": req.UserID, "amount": req.Amount})
	})

	// Internal Miriam evaluation trigger. Cloudflare Cron calls this endpoint;
	// Rail keeps the financial execution, DB state, and audit trail in the backend.
	if container.MiriamIntelligenceService != nil && container.UserRepo != nil {
		internal.POST("/miriam/evaluate", func(c *gin.Context) {
			reqCtx, reqCancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
			defer reqCancel()

			var req struct {
				UserID    string `json:"user_id"`
				EventType string `json:"event_type"`
				Limit     int    `json:"limit"`
			}
			if err := c.ShouldBindJSON(&req); err != nil && err != io.EOF {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			eventType := normalizeMiriamInternalEvent(req.EventType)
			limit := req.Limit
			if limit <= 0 {
				limit = container.Config.Workers.MiriamIntelligenceBatchSize
			}
			if limit <= 0 || limit > 500 {
				limit = 500
			}

			var userIDs []uuid.UUID
			if strings.TrimSpace(req.UserID) != "" {
				userID, err := uuid.Parse(req.UserID)
				if err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user_id"})
					return
				}
				userIDs = []uuid.UUID{userID}
			} else {
				ids, err := container.UserRepo.ListMiriamWorkerUserIDs(reqCtx, limit)
				if err != nil {
					container.ZapLog.Error("internal miriam evaluation: list users failed", zap.Error(err))
					c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list Miriam users"})
					return
				}
				userIDs = ids
			}

			var (
				mu          sync.Mutex
				evaluated   int
				failed      int
				failedUsers = make([]string, 0)
				wg          sync.WaitGroup
				sem         = make(chan struct{}, 10)
			)

			for _, uid := range userIDs {
				if reqCtx.Err() != nil {
					break
				}
				wg.Add(1)
				sem <- struct{}{}
				go func(userID uuid.UUID) {
					defer wg.Done()
					defer func() { <-sem }()
					if reqCtx.Err() != nil {
						return
					}
					evalCtx, cancel := context.WithTimeout(reqCtx, 10*time.Second)
					var evalErr error
					if container.MiriamIntelligenceOrchestrator != nil {
						_, evalErr = container.MiriamIntelligenceOrchestrator.Evaluate(evalCtx, userID, eventType)
					} else {
						evalErr = container.MiriamIntelligenceService.EvaluateUser(evalCtx, userID, eventType)
					}
					cancel()
					mu.Lock()
					if evalErr != nil {
						failed++
						if len(failedUsers) < 20 {
							failedUsers = append(failedUsers, userID.String())
						}
						container.ZapLog.Warn("internal miriam evaluation: user failed", zap.String("user_id", userID.String()), zap.Error(evalErr))
					} else {
						evaluated++
					}
					mu.Unlock()
				}(uid)
			}
			wg.Wait()

			c.JSON(http.StatusOK, gin.H{
				"status":       "completed",
				"event_type":   eventType,
				"requested":    len(userIDs),
				"evaluated":    evaluated,
				"failed":       failed,
				"failed_users": failedUsers,
			})
		})
	}

	// Apple App Site Association — required for passkey Associated Domains
	router.GET("/.well-known/apple-app-site-association", func(c *gin.Context) {
		c.Header("Content-Type", "application/json")
		c.File("static/.well-known/apple-app-site-association")
	})
	router.GET("/apple-app-site-association", func(c *gin.Context) {
		c.Header("Content-Type", "application/json")
		c.File("static/.well-known/apple-app-site-association")
	})

	// Swagger documentation (development only, password-protected)
	if container.Config.Environment == "development" {
		swaggerPassword := strings.TrimSpace(os.Getenv("SWAGGER_PASSWORD"))
		if swaggerPassword != "" {
			router.GET("/swagger/*any", func(c *gin.Context) {
				if c.Query("key") != swaggerPassword {
					c.Header("WWW-Authenticate", `Basic realm="Swagger"`)
					c.AbortWithStatus(http.StatusUnauthorized)
					return
				}
				ginSwagger.WrapHandler(swaggerFiles.Handler)(c)
			})
		} else {
			router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
		}
	}
	walletFundingHandlers := handlers.NewWalletFundingHandlers(
		container.GetWalletService(),
		container.GetFundingService(),
		container.GetWithdrawalService(),
		container.GetInvestingService(),
		container.Logger,
	)
	// Configure webhook secret - SECURITY: only skip verification in explicit development mode
	skipWebhookVerify := container.Config.Environment == "development" && container.Config.Payment.WebhookSecret == ""
	if skipWebhookVerify && (container.Config.Environment == "production" || container.Config.Environment == "staging") {
		skipWebhookVerify = false // fail-closed: never skip in production/staging
	}
	walletFundingHandlers.SetWebhookSecret(container.Config.Payment.WebhookSecret, skipWebhookVerify)

	// Wire user profile provider for withdrawal AlpacaAccountID lookup
	walletFundingHandlers.SetUserProfileProvider(container.UserRepo)

	// Wire ledger service for reconciliation
	walletFundingHandlers.SetLedgerService(container.LedgerService)

	// Wire allocation service for unified balance queries
	if allocationSvc := container.GetAllocationService(); allocationSvc != nil {
		walletFundingHandlers.SetAllocationBalanceProvider(allocationSvc)
	}
	var walletLookup func(context.Context, uuid.UUID, entities.WalletChain) (*entities.ManagedWallet, error)
	if ws := container.GetWalletService(); ws != nil {
		walletLookup = ws.GetWalletByUserAndChain
	}

	withdrawalHandlers := handlers.NewWithdrawalHandlers(
		container.GetWithdrawalService(),
		&WithdrawalWalletProviderAdapter{
			getWalletByUserAndChain: walletLookup,
		},
		container.Logger,
	)

	authHandlers := handlers.NewAuthHandlers(
		container.DB,
		container.Config,
		container.ZapLog,
		*container.UserRepo,
		container.GetVerificationService(),
		container.GetOnboardingService(),
		container.EmailService,
		container.GetSessionService(),
		container.GetTwoFAService(),
		container.GetPasscodeService(),
		container.RedisClient,
		container.GetAccountDeletionService(),
		container.Config.KYC.WebhookSecret,
		container.GetSocialAuthService(),
	)
	securityHandlers := handlers.NewSecurityHandlers(
		container.GetPasscodeService(),
		container.GetOnboardingService(),
		container.UserRepo,
		container.GetSessionService(),
		container.Config,
		container.ZapLog,
	)

	// Initialize social auth handlers
	socialAuthHandlers := handlers.NewSocialAuthHandlers(
		container.GetSocialAuthService(),
		container.GetWebAuthnService(),
		container.GetSessionService(),
		container.GetPasscodeService(),
		*container.UserRepo,
		container.RedisClient,
		container.Config,
		container.ZapLog,
	)

	// Initialize integration handlers (Alpaca only)
	integrationHandlers := handlers.NewIntegrationHandlers(
		container.AlpacaClient,
		services.NewNotificationService(container.ZapLog),
		container.Logger,
	)

	// Initialize Bridge KYC handlers for optimized KYC flow
	bridgeKYCHandlers := handlers.NewBridgeKYCHandlers(
		container.BridgeClient,
		*container.UserRepo,
		container.ZapLog,
	)
	var sumsubClient kycservice.SumsubAdapter
	if strings.EqualFold(strings.TrimSpace(container.Config.KYC.Provider), "sumsub") &&
		container.Config.KYC.APIKey != "" &&
		container.Config.KYC.APISecret != "" {
		sumsubClient = sumsubadapter.NewClient(sumsubadapter.Config{
			BaseURL:       container.Config.KYC.BaseURL,
			AppToken:      container.Config.KYC.APIKey,
			SecretKey:     container.Config.KYC.APISecret,
			WebhookSecret: container.Config.KYC.WebhookSecret,
			LevelName:     container.Config.KYC.LevelName,
			UserAgent:     container.Config.KYC.UserAgent,
			Timeout:       30 * time.Second,
		}, container.ZapLog)
	}

	var diditClient *diditadapter.Client
	if container.Config.KYC.DiditAPIKey != "" && container.Config.KYC.DiditWorkflowID != "" {
		diditClient = diditadapter.NewClient(diditadapter.Config{
			APIKey:        container.Config.KYC.DiditAPIKey,
			WebhookSecret: container.Config.KYC.DiditWebhookSecret,
			WorkflowID:    container.Config.KYC.DiditWorkflowID,
		}, container.ZapLog)
	}
	kycUserRepoAdapter := repositories.NewKYCUserRepositoryAdapter(container.UserRepo)
	var kycService *kycservice.Service
	if diditClient != nil {
		kycService = kycservice.NewService(
			kycUserRepoAdapter,
			container.KYCSubmissionRepo,
			container.BridgeAdapter,
			alpacaadapter.NewAdapter(container.AlpacaClient, container.Logger),
			sumsubClient,
			container.SumsubWebhookEventRepo,
			container.KYCSyncJobRepo,
			container.Config.KYC.LevelName,
			container.Config.Security.EncryptionKey,
			container.ZapLog,
			diditClient,
		)
	} else {
		kycService = kycservice.NewService(
			kycUserRepoAdapter,
			container.KYCSubmissionRepo,
			container.BridgeAdapter,
			alpacaadapter.NewAdapter(container.AlpacaClient, container.Logger),
			sumsubClient,
			container.SumsubWebhookEventRepo,
			container.KYCSyncJobRepo,
			container.Config.KYC.LevelName,
			container.Config.Security.EncryptionKey,
			container.ZapLog,
		)
	}
	kycHTTPHandlers := kychandlers.NewHandler(kycService, container.Logger)
	if container.NotificationService != nil {
		kycService.SetNotifier(container.NotificationService)
	}
	if container.ComplianceService != nil {
		kycService.SetAMLScreener(container.ComplianceService)
	}
	if container.GraphVirtualAccountService != nil {
		kycService.SetGraphVAService(container.GraphVirtualAccountService)
	}
	kycEligibilityMiddleware := middleware.NewKYCMiddleware(container.UserRepo, container.Logger)

	// Create session validator adapter
	sessionValidator := NewSessionValidatorAdapter(container.GetSessionService())

	// API v1 routes
	v1 := router.Group("/api/v1")
	{
		// Authentication routes (no auth required)
		auth := v1.Group("/auth")
		auth.Use(middleware.AuthCSRFProtection())
		{
			auth.POST("/register", middleware.AuthRateLimit(5), authHandlers.Register)
			auth.POST("/verify", middleware.AuthRateLimit(5), authHandlers.Verify)
			auth.POST("/refresh", middleware.AuthRateLimit(10), authHandlers.RefreshToken)
			auth.POST("/resend-code", authHandlers.ResendCode)

			// Sensitive auth endpoints with stricter rate limiting
			authRateLimited := auth.Group("/")
			authRateLimited.Use(middleware.AuthRateLimit(5))
			if container.LoginAttemptTracker != nil {
				authRateLimited.Use(middleware.LoginRateLimiting(container.LoginAttemptTracker, container.GetCaptchaVerifier(), container.Logger))
			}
			if lp := container.GetLoginProtectionService(); lp != nil {
				authRateLimited.Use(middleware.LoginProtection(lp, container.ZapLog))
			}
			if anomalySvc := container.GetSessionAnomalyService(); anomalySvc != nil {
				authRateLimited.Use(middleware.SessionAnomalyDetection(anomalySvc, container.ZapLog))
			}
			{
				authRateLimited.POST("/login", authHandlers.Login)
				authRateLimited.POST("/passcode-login", authHandlers.PasscodeLogin)

				// Passwordless email login (OTP-only)
				authRateLimited.POST("/email/start", authHandlers.EmailOTPStart)
				authRateLimited.POST("/email/login", authHandlers.EmailOTPLogin)

				// Deprecated password endpoints — removed (OTP-only auth)
				// authRateLimited.POST("/forgot-password", authHandlers.ForgotPassword)
				// authRateLimited.POST("/verify-reset-code", authHandlers.VerifyResetCode)
				// authRateLimited.POST("/reset-password", authHandlers.ResetPassword)
			}

			// Social auth routes
			authRateLimited.POST("/social/url", socialAuthHandlers.GetSocialAuthURL)
			authRateLimited.POST("/social/login", socialAuthHandlers.SocialLogin)
			authRateLimited.POST("/webauthn/login/begin", socialAuthHandlers.BeginWebAuthnLogin)
			authRateLimited.POST("/webauthn/login/finish", socialAuthHandlers.FinishWebAuthnLogin)
		}

		// Waitlist routes (public, no auth required)
		if container.WaitlistService != nil {
			wlHandlers := waitlisthandlers.NewHandlers(container.WaitlistService, container.ZapLog)
			waitlist := v1.Group("/waitlist")
			{
				waitlist.POST("", middleware.RateLimit(10), wlHandlers.Signup)
				waitlist.GET("/count", wlHandlers.Count)
			}
		}

		// Dashboard auth (email-only for super_admins)
		v1.POST("/dashboard/auth", middleware.RateLimit(5), func(c *gin.Context) {
			var req struct {
				Email string `json:"email" binding:"required"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(400, gin.H{"error": "email required"})
				return
			}
			email := strings.ToLower(strings.TrimSpace(req.Email))
			var userID, role string
			err := container.DB.QueryRowContext(c.Request.Context(),
				"SELECT id, role FROM users WHERE LOWER(email) = $1", email).Scan(&userID, &role)
			if err != nil || (role != "admin" && role != "super_admin") {
				c.JSON(401, gin.H{"error": "unauthorized"})
				return
			}
			uid, _ := uuid.Parse(userID)
			tokenPair, err := container.JWTService.GenerateTokenPairEnhanced(uid, email, role)
			if err != nil {
				c.JSON(500, gin.H{"error": "token generation failed"})
				return
			}
			c.JSON(200, gin.H{"token": tokenPair.AccessToken, "role": role, "email": email})
		})

		// Dashboard analytics (JWT-only auth, no session required)
		if container.AdminAnalyticsService != nil {
			dashAnalytics := v1.Group("/dashboard/analytics")
			dashAnalytics.Use(func(c *gin.Context) {
				authHeader := c.GetHeader("Authorization")
				if len(authHeader) < 8 || authHeader[:7] != "Bearer " {
					c.JSON(401, gin.H{"error": "unauthorized"})
					c.Abort()
					return
				}
				claims, err := container.JWTService.ValidateEnhancedToken(authHeader[7:])
				if err != nil {
					c.JSON(401, gin.H{"error": "invalid token"})
					c.Abort()
					return
				}
				c.Set("user_id", claims.UserID.String())
				c.Set("user_role", claims.Role)
				c.Next()
			})
			dashAnalytics.Use(middleware.AdminAuth(container.DB, container.Logger))
			{
				ah := admin_handlers.NewAnalyticsHandlers(container.AdminAnalyticsService, container.ZapLog)
				dashAnalytics.GET("/overview", ah.Overview)
				dashAnalytics.GET("/users", ah.Users)
				dashAnalytics.GET("/waitlist", ah.Waitlist)
				dashAnalytics.GET("/miriam", ah.Miriam)
				dashAnalytics.GET("/money-movement", ah.MoneyMovement)
				dashAnalytics.GET("/retention", ah.Retention)
				dashAnalytics.GET("/trust", ah.Trust)
				dashAnalytics.GET("/chains", ah.Chains)
			}
		}

		// Onboarding routes
		onboarding := v1.Group("/onboarding")
		{
			authenticatedOnboarding := onboarding.Group("/")
			authenticatedOnboarding.Use(middleware.Authentication(container.Config, container.Logger, sessionValidator, container.TokenBlacklist))
			{
				authenticatedOnboarding.POST("/basic-complete", authHandlers.BasicCompleteOnboarding)
				authenticatedOnboarding.POST("/source-of-funds", authHandlers.SaveSourceOfFunds)
				authenticatedOnboarding.GET("/kyc/missing-fields", authHandlers.GetMissingKycFields)
				// Fraud detection: correlate device fingerprint across accounts at onboarding completion.
				// Catches fraud rings using purchased KYC identities from the same device.
				if fraudSvc := container.GetOnboardingFraudService(); fraudSvc != nil {
					authenticatedOnboarding.POST("/complete", middleware.OnboardingFraudMiddleware(fraudSvc, container.ZapLog), authHandlers.CompleteOnboarding)
				} else {
					authenticatedOnboarding.POST("/complete", authHandlers.CompleteOnboarding)
				}
			}
		}

		// KYC provider webhooks (no auth required for external callbacks)
		// NOTE: Signature verification is handled inside each handler:
		// - ProcessKYCCallback: verifies via h.verifyKYCCallbackSignature() when secret is configured
		// - HandleSumsubWebhook: verifies via kycService.VerifySumsubWebhookSignature()
		// - HandleDiditWebhook: verifies via kycService.VerifyDiditWebhookSignature()
		kyc := v1.Group("/kyc")
		{
			kyc.POST("/callback/:provider_ref", authHandlers.ProcessKYCCallback)
			if sumsubClient != nil {
				kyc.POST("/sumsub/webhook", kycHTTPHandlers.HandleSumsubWebhook)
			}
			if diditClient != nil {
				kyc.POST("/didit/webhook", kycHTTPHandlers.HandleDiditWebhook)
				// Didit transaction monitoring webhook
				if container.ComplianceService != nil {
					kyc.POST("/didit/transaction-webhook", func(c *gin.Context) {
						body, err := io.ReadAll(c.Request.Body)
						if err != nil {
							c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
							return
						}
						sig := c.GetHeader("X-Signature-V2")
						ts := c.GetHeader("X-Timestamp")
						if err := diditClient.VerifyWebhookSignature(body, sig, ts); err != nil {
							container.ZapLog.Warn("Invalid Didit transaction webhook signature", zap.Error(err))
							c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
							return
						}
						var payload diditadapter.TransactionWebhookPayload
						if err := json.Unmarshal(body, &payload); err != nil {
							c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
							return
						}
						if err := container.ComplianceService.HandleTransactionWebhook(c.Request.Context(), &payload); err != nil {
							container.ZapLog.Error("Failed to handle transaction webhook", zap.Error(err))
							c.JSON(http.StatusInternalServerError, gin.H{"error": "processing failed"})
							return
						}
						if strings.EqualFold(payload.Status, "APPROVED") && container.WithdrawalService != nil {
							if err := container.WithdrawalService.ResumeComplianceApprovedWithdrawal(c.Request.Context(), payload.TransactionID); err != nil {
								container.ZapLog.Error("Failed to resume compliance-approved withdrawal",
									zap.Error(err),
									zap.String("didit_uuid", payload.UUID),
									zap.String("transaction_id", payload.TransactionID))
								c.JSON(http.StatusInternalServerError, gin.H{"error": "withdrawal resume failed"})
								return
							}
						}
						c.JSON(http.StatusOK, gin.H{"status": "ok"})
					})
				}
			}
		}

		// Protected routes (auth required)
		protected := v1.Group("/")
		protected.Use(middleware.Authentication(container.Config, container.Logger, sessionValidator, container.TokenBlacklist))
		protected.Use(middleware.CSRFProtection(csrfStore))
		{
			// Logout (requires valid session)
			protected.POST("/auth/logout", authHandlers.Logout)

			// User management
			users := protected.Group("/users")
			{
				users.GET("/me", authHandlers.GetProfile)
				users.PUT("/me", authHandlers.UpdateProfile)
				// Deprecated: password-based change-password removed (OTP-only auth)
				// users.POST("/me/change-password", authHandlers.ChangePassword)
				users.DELETE("/me", middleware.AuthRateLimit(3), authHandlers.DeleteAccount)
				users.POST("/me/enable-2fa", authHandlers.Enable2FA)
				users.POST("/me/disable-2fa", authHandlers.Disable2FA)
				users.GET("/me/tos", authHandlers.GetTOSAcceptance)
				users.POST("/me/tos", authHandlers.AcceptTOS)
			}

			// Messaging platform linking (iMessage/WhatsApp/Telegram). Issues the
			// one-time handshake token users text to the bridge to bind their sender id.
			if container.PlatformHandler != nil {
				platformGroup := protected.Group("/platform")
				{
					platformGroup.POST("/link", middleware.AuthRateLimit(5), container.PlatformHandler.InitiateLink)
					platformGroup.GET("/linked", container.PlatformHandler.ListLinked)
					platformGroup.DELETE("/:platform", middleware.AuthRateLimit(5), container.PlatformHandler.Unlink)
				}
			}

			// KYC status utilities (auth required but no KYC gate)
			kycProtected := protected.Group("/kyc")
			{
				kycProtected.POST("/sumsub/session", middleware.AuthRateLimit(3), kycEligibilityMiddleware.RequireKYCEligibility(), kycHTTPHandlers.CreateSumsubSession)
				kycProtected.GET("/sumsub/token", middleware.AuthRateLimit(10), kycHTTPHandlers.RefreshSumsubToken)
				kycProtected.POST("/didit/session", middleware.AuthRateLimit(3), kycEligibilityMiddleware.RequireKYCEligibility(), kycHTTPHandlers.CreateDiditSession)
				kycProtected.POST("/submit", middleware.AuthRateLimit(3), kycEligibilityMiddleware.RequireKYCEligibility(), kycHTTPHandlers.SubmitKYC)
				kycProtected.GET("/status", kycHTTPHandlers.GetKYCStatus)
				// Bridge KYC - optimized for sub-2-minute verification
				kycProtected.GET("/bridge/link", bridgeKYCHandlers.GetBridgeKYCLink)
				kycProtected.GET("/bridge/status", bridgeKYCHandlers.GetBridgeKYCStatus)

				// Tier upgrade endpoints
				kycProtected.POST("/sprout/upgrade", middleware.AuthRateLimit(3), kycHTTPHandlers.SproutUpgrade)
				kycProtected.POST("/bloom/upgrade", middleware.AuthRateLimit(3), kycHTTPHandlers.BloomUpgrade)
			}

			// Security routes for passcode management
			security := protected.Group("/security")
			{
				security.GET("/passcode", securityHandlers.GetPasscodeStatus)
				security.POST("/passcode", securityHandlers.CreatePasscode)
				security.PUT("/passcode", securityHandlers.UpdatePasscode)
				security.POST("/passcode/verify", securityHandlers.VerifyPasscode)
				security.DELETE("/passcode", securityHandlers.RemovePasscode)

				// Social account management
				security.GET("/social-accounts", socialAuthHandlers.GetLinkedAccounts)
				security.POST("/social-accounts/link", socialAuthHandlers.LinkSocialAccount)
				security.DELETE("/social-accounts/:provider", socialAuthHandlers.UnlinkSocialAccount)

				// WebAuthn/Passkey management
				security.GET("/passkeys", socialAuthHandlers.GetWebAuthnCredentials)
				security.POST("/passkeys/register", middleware.AuthRateLimit(5), socialAuthHandlers.BeginWebAuthnRegistration)
				security.POST("/passkeys/register/finish", middleware.AuthRateLimit(5), socialAuthHandlers.FinishWebAuthnRegistration)
				security.DELETE("/passkeys/:id", socialAuthHandlers.DeleteWebAuthnCredential)

				// Device management
				securityEnhancedHandlers := handlers.NewSecurityEnhancedHandlers(
					container.GetDeviceTrackingService(),
					container.GetIPWhitelistService(),
					container.GetWithdrawalSecurityService(),
					container.GetSecurityEventLogger(),
					container.ZapLog,
				)
				security.GET("/devices", securityEnhancedHandlers.GetDevices)

				// IP whitelist management
				security.GET("/ip-whitelist", securityEnhancedHandlers.GetIPWhitelist)
				security.POST("/ip-whitelist/:id/verify", securityEnhancedHandlers.VerifyWhitelistedIP)

				// Security events
				security.GET("/events", securityEnhancedHandlers.GetSecurityEvents)
				security.GET("/current-ip", securityEnhancedHandlers.GetCurrentIP)

				// MFA management
				mfaHandlers := handlers.NewMFAHandlers(
					container.GetMFAService(),
					container.GetGeoSecurityService(),
					container.GetIncidentResponseService(),
					container.ZapLog,
				)
				security.GET("/mfa", mfaHandlers.GetMFASettings)
				security.POST("/mfa/sms", mfaHandlers.SetupSMSMFA)
				security.POST("/mfa/send-code", mfaHandlers.SendMFACode)
				security.POST("/mfa/verify", mfaHandlers.VerifyMFACode)
				security.GET("/geo-info", mfaHandlers.GetGeoInfo)

				// Sensitive operations require a short-lived passcode session token.
				securitySensitive := security.Group("/")
				securitySensitive.Use(middleware.RequirePasscodeSession(container.GetPasscodeService(), true, container.ZapLog))
				{
					securitySensitive.POST("/devices/:id/trust", securityEnhancedHandlers.TrustDevice)
					securitySensitive.DELETE("/devices/:id", securityEnhancedHandlers.RevokeDevice)
					securitySensitive.POST("/ip-whitelist", securityEnhancedHandlers.AddIPToWhitelist)
					securitySensitive.DELETE("/ip-whitelist/:id", securityEnhancedHandlers.RemoveIPFromWhitelist)
					securitySensitive.POST("/withdrawals/confirm", securityEnhancedHandlers.ConfirmWithdrawal)
				}

				// Security features v2: address whitelist + adaptive MFA
				securityFeaturesHandler := securityHandlersV2.NewSecurityFeaturesHandler(
					container.GetAddressWhitelistService(),
					container.GetAdaptiveMFAService(),
					container.ZapLog,
				)
				RegisterSecurityFeatureRoutes(security, securityFeaturesHandler)
			}

			// Mobile-optimized API endpoints for better app performance
			mobile := protected.Group("/mobile")
			mobile.Use(middleware.TimeoutMiddleware(10 * time.Second))
			{
				mobileHandlers := handlers.NewMobileHandlers(
					container.StationService,
					container.GetAllocationService(),
					container.GetInvestingService(),
					*container.UserRepo,
					container.CardRepo,
					container.ZapLog,
				)
				mobile.GET("/home", mobileHandlers.GetMobileHome)
				mobile.POST("/batch", mobileHandlers.BatchExecute)
				mobile.POST("/sync", mobileHandlers.Sync)
			}

			// Funding routes (legacy - kept for backward compat, prefer /deposits)
			funding := protected.Group("/funding")
			funding.Use(middleware.TimeoutMiddleware(30*time.Second), middleware.SystemPaused())
			{
				funding.GET("/transactions", walletFundingHandlers.GetTransactionHistory)
				// Pre-KYC: TOS link needed during onboarding, read-only Paj lookups
				funding.GET("/tos-link", walletFundingHandlers.GetBridgeTOSLink)
				if container.PajHandlers != nil {
					paj := funding.Group("/paj")
					paj.Use(middleware.TimeoutMiddleware(30*time.Second), middleware.SystemPaused())
					paj.GET("/rates", container.PajHandlers.GetRates)
					paj.GET("/banks", container.PajHandlers.GetBanks)
					paj.GET("/orders", container.PajHandlers.GetOrders)
					paj.GET("/orders/:id/status", container.PajHandlers.GetOrderStatus)
				}

				// RampHub: read-only best-rate lookups (no KYC required)
				if container.RampHandlers != nil {
					rampRO := funding.Group("/ramp")
					rampRO.Use(middleware.TimeoutMiddleware(30*time.Second), middleware.SystemPaused())
					rampRO.GET("/quote", container.RampHandlers.GetQuote)
					rampRO.GET("/banks", container.RampHandlers.GetBanks)
					rampRO.GET("/orders", container.RampHandlers.GetOrders)
					rampRO.GET("/orders/:id/status", container.RampHandlers.GetOrderStatus)
				}

				// Graph NGN named virtual account (no Bridge KYC — uses own Graph verification)
				if container.NGNHandlers != nil {
					funding.GET("/ngn/virtual-account", container.NGNHandlers.GetNGNAccount)
					funding.POST("/ngn/virtual-account",
						middleware.AuthRateLimit(5),
						container.NGNHandlers.ProvisionNGNAccount)
					funding.POST("/ngn/auto-provision",
						middleware.AuthRateLimit(10),
						container.NGNHandlers.AutoProvisionNGN)
				}

				// KYC-gated funding operations
				// Deposit address — available to all users with Circle wallets (no KYC required)
				funding.POST("/deposit/address", walletFundingHandlers.CreateDepositAddress)

				// Bridge KYC required: fiat virtual accounts and instant funding
				fundingBridgeGated := funding.Group("/")
				fundingBridgeGated.Use(middleware.RequireBridgeCapability(container.UserRepo, container.ZapLog))
				{
					fundingBridgeGated.POST("/virtual-account", walletFundingHandlers.CreateVirtualAccount)
					fundingBridgeGated.GET("/virtual-accounts", walletFundingHandlers.GetVirtualAccounts)

					if instantFundingHandlers := container.GetInstantFundingHandlers(); instantFundingHandlers != nil {
						fundingBridgeGated.POST("/instant", instantFundingHandlers.RequestInstantFunding)
						fundingBridgeGated.GET("/instant/status", instantFundingHandlers.GetInstantFundingStatus)
					}
				}

				// Crypto-capable: ChainRails and PAJ (no KYC required, backend enforces limits)
				if container.ChainRailsHandlers != nil {
					chainrails := funding.Group("/chainrails")
					chainrails.Use(middleware.TimeoutMiddleware(30*time.Second), middleware.SystemPaused())
					chainrails.POST("/session",
						middleware.AuthRateLimit(10),
						container.ChainRailsHandlers.CreateSession)
				}

				if container.PajHandlers != nil {
					paj := funding.Group("/paj")
					paj.Use(middleware.TimeoutMiddleware(30*time.Second), middleware.SystemPaused())
					paj.Use(middleware.RequireCryptoCapability(container.UserRepo, container.ZapLog))
					paj.POST("/initiate", middleware.AuthRateLimit(5), container.PajHandlers.Initiate)
					paj.POST("/verify", middleware.AuthRateLimit(10), container.PajHandlers.Verify)
					// Rate-limited: bank resolution is an account-name oracle (PII) and
					// each call costs a provider lookup.
					paj.POST("/banks/resolve", middleware.AuthRateLimit(10), container.PajHandlers.ResolveBankAccount)
					paj.POST("/banks/add", container.PajHandlers.AddBankAccount)
					paj.GET("/banks/saved", container.PajHandlers.GetBankAccounts)
					paj.POST("/onramp", middleware.AuthRateLimit(10), container.PajHandlers.CreateOnramp)
					// Withdrawals require a passcode-verified session, matching
					// /v1/withdrawals — the bearer token alone must not be able to move
					// funds out. The app already sends X-Passcode-Session here.
					paj.POST("/offramp",
						middleware.AuthRateLimit(10),
						middleware.RequirePasscodeSession(container.GetPasscodeService(), true, container.ZapLog),
						container.PajHandlers.CreateOfframp)
				}

				// RampHub: crypto-capable on/off ramp operations (backend enforces limits)
				if container.RampHandlers != nil {
					ramp := funding.Group("/ramp")
					ramp.Use(middleware.TimeoutMiddleware(30*time.Second), middleware.SystemPaused())
					ramp.Use(middleware.RequireCryptoCapability(container.UserRepo, container.ZapLog))
					ramp.POST("/banks/resolve", middleware.AuthRateLimit(10), container.RampHandlers.ResolveBankAccount)
					ramp.POST("/onramp", middleware.AuthRateLimit(10), container.RampHandlers.CreateOnramp)
					ramp.POST("/offramp",
						middleware.AuthRateLimit(10),
						middleware.RequirePasscodeSession(container.GetPasscodeService(), true, container.ZapLog),
						container.RampHandlers.CreateOfframp)
				}
			}

			// Airbills bill-pay management endpoints (read-only + mandate/beneficiary management)
			if container.BillPayHandlers != nil {
				billpay := protected.Group("/billpay")
				billpay.Use(middleware.TimeoutMiddleware(30*time.Second), middleware.SystemPaused())
				{
					billpay.GET("/orders/:id", container.BillPayHandlers.GetOrder)
					billpay.GET("/beneficiaries", container.BillPayHandlers.ListBeneficiaries)
					billpay.POST("/beneficiaries", container.BillPayHandlers.SaveBeneficiary)
					billpay.GET("/mandates/:category", container.BillPayHandlers.GetMandate)
					billpay.PUT("/mandates/:category", container.BillPayHandlers.SetMandate)
				}
			}

			// Unified Balance route
			protected.GET("/balances", middleware.TimeoutMiddleware(10*time.Second), walletFundingHandlers.GetUnifiedBalances)

			// Gas balance route — SOL balance for Solana gas fees
			protected.GET("/gas-balance", middleware.TimeoutMiddleware(10*time.Second), walletFundingHandlers.GetGasBalance)

			// Unified Activity feed — single endpoint for all transaction history
			if container.ActivityHandlers != nil {
				protected.GET("/activity", middleware.TimeoutMiddleware(10*time.Second), container.ActivityHandlers.GetActivityFeed)
			}

			// Unified Deposit routes
			deposits := protected.Group("/deposits")
			deposits.Use(middleware.TimeoutMiddleware(30*time.Second), middleware.SystemPaused())
			{
				// Read-only: no KYC gate — all authenticated users can view their deposits
				deposits.GET("", walletFundingHandlers.ListDeposits)
				deposits.GET("/:id", walletFundingHandlers.GetDeposit)
			}
			// Write operations require Bridge KYC
			depositsGated := deposits.Group("/")
			depositsGated.Use(middleware.RequireCryptoCapability(container.UserRepo, container.ZapLog))
			// Fraud detection on deposit creation: catches suspicious first deposits from fraud ring accounts.
			if fraudSvc := container.GetOnboardingFraudService(); fraudSvc != nil {
				depositsGated.Use(middleware.DepositFraudMiddleware(fraudSvc, container.ZapLog))
			}
			{
				depositsGated.POST("", walletFundingHandlers.CreateDeposit)
			}

			// Unified Withdrawal routes with security middleware
			withdrawals := protected.Group("/withdrawals")
			withdrawals.Use(middleware.TimeoutMiddleware(30*time.Second), middleware.SystemPaused())
			withdrawals.Use(middleware.RequireCryptoCapability(container.UserRepo, container.ZapLog))
			// Apply withdrawal security: rate limits (3/day) and daily max ($10k new, $100k established)
			if withdrawalSecurityStore := container.GetWithdrawalSecurityStore(); withdrawalSecurityStore != nil {
				withdrawals.Use(middleware.WithdrawalSecurityMiddleware(
					withdrawalSecurityStore,
					middleware.DefaultWithdrawalSecurityConfig(),
				))
			}
			// Enforce passcode-verified session for new withdrawal initiation requests.
			withdrawalsSensitive := withdrawals.Group("/")
			withdrawalsSensitive.Use(middleware.RequirePasscodeSession(container.GetPasscodeService(), true, container.ZapLog))
			{
				// Keep POST /withdrawals for backward compatibility and treat it as crypto withdrawal.
				withdrawalsSensitive.POST("", withdrawalHandlers.InitiateCryptoWithdrawal)
				withdrawalsSensitive.POST("/crypto", withdrawalHandlers.InitiateCryptoWithdrawal)
				// Fiat withdrawals require full Bridge KYC
				fiatWithdrawals := withdrawalsSensitive.Group("/")
				fiatWithdrawals.Use(middleware.RequireBridgeCapability(container.UserRepo, container.ZapLog))
				fiatWithdrawals.POST("/fiat", withdrawalHandlers.InitiateFiatWithdrawal)
				withdrawals.GET("/fees", withdrawalHandlers.GetWithdrawalFees)
				withdrawals.GET("/emergency/preview", withdrawalHandlers.EmergencyWithdrawalPreview)
				withdrawalsSensitive.POST("/emergency/to-spending", withdrawalHandlers.EmergencyStashToSpending)
				withdrawals.GET("", withdrawalHandlers.GetUserWithdrawals)
				withdrawals.GET("/:withdrawalId", withdrawalHandlers.GetWithdrawal)
				withdrawals.DELETE("/:withdrawalId", withdrawalHandlers.CancelWithdrawal)
			}

			// Funding routes - fund stash from spending balance
			fundingRoutes := protected.Group("/funding")
			fundingRoutes.Use(middleware.RequirePasscodeSession(container.GetPasscodeService(), true, container.ZapLog))
			{
				fundingRoutes.POST("/stash", withdrawalHandlers.FundStash)
			}

			// Account routes - Station (home screen) endpoint
			account := protected.Group("/account")
			account.Use(middleware.TimeoutMiddleware(10 * time.Second))
			{
				stationHandlers := container.GetStationHandlers()
				if stationHandlers != nil {
					// Station endpoint - returns home screen data per PRD
					// "Total balance, Spend balance, Invest balance, System status"
					account.GET("/station", stationHandlers.GetStation)
				}

				// Spending Stash endpoint - comprehensive spending data
				spendingStashHandlers := container.GetSpendingStashHandlers()
				if spendingStashHandlers != nil {
					account.GET("/spending-stash", spendingStashHandlers.GetSpendingStash)
				}

				// Investment Stash endpoint - comprehensive investment data
				investmentStashHandlers := container.GetInvestmentStashHandlers()
				if investmentStashHandlers != nil {
					account.GET("/investment-stash", investmentStashHandlers.GetInvestmentStash)
					account.GET("/investment-stash/positions", investmentStashHandlers.GetInvestmentPositions)
					account.GET("/investment-stash/distribution", investmentStashHandlers.GetInvestmentDistribution)
					account.GET("/investment-stash/transactions", investmentStashHandlers.GetInvestmentTransactions)
					account.GET("/investment-stash/performance", investmentStashHandlers.GetInvestmentPerformance)
				}

				// Yield estimate endpoint
				if container.YieldService != nil {
					yieldHandlers := handlers.NewYieldHandlers(container.YieldService, container.AllocationService, container.ZapLog)
					account.GET("/yield/estimate", yieldHandlers.GetDailyYieldEstimate)
				}

				// Blend yield read endpoints (frontend)
				if container.BlendDepositRouter != nil {
					blendHandlers := handlers.NewBlendHandlers(container.BlendDepositRouter, container.ZapLog)
					account.GET("/yield/blend/overview", blendHandlers.GetYieldOverview)
				}
			}

			// Limits routes - deposit/withdrawal limits based on KYC tier
			limits := protected.Group("/limits")

			// Gameplay routes - streaks, XP, challenges, achievements, subscription
			SetupGameplayRoutes(protected, container)

			{
				limitsHandler := container.GetLimitsHandler()
				if limitsHandler != nil {
					limits.GET("", limitsHandler.GetUserLimits())
					limits.POST("/validate/deposit", limitsHandler.ValidateDeposit())
					limits.POST("/validate/withdrawal", limitsHandler.ValidateWithdrawal())
				}
			}

			// P2P Transfer routes - Cash App style money transfers
			// Rate limited: 20 send/lookup per minute, 10 cancel per minute
			p2pHandlers := container.GetP2PHandlers()
			if p2pHandlers != nil {
				p2p := protected.Group("/p2p")
				p2p.Use(middleware.AuthRateLimit(20))
				p2p.Use(middleware.RequireCryptoCapability(container.UserRepo, container.ZapLog))
				{
					p2p.POST("/lookup", middleware.AuthRateLimit(10), p2pHandlers.Lookup)
					p2p.POST("/send", p2pHandlers.Send)
					p2p.GET("/transfers", p2pHandlers.GetTransfers)
					p2p.GET("/recent", p2pHandlers.GetRecentRecipients)
					p2p.DELETE("/transfers/:id", middleware.AuthRateLimit(10), p2pHandlers.Cancel)
					p2p.POST("/claim/:token", p2pHandlers.ClaimByToken)
					p2p.POST("/railtag", p2pHandlers.SetRailTag)
					p2p.POST("/railtag/check", p2pHandlers.CheckRailTag)

					// Tap-to-pay secure handshake
					p2p.POST("/tap/intent", p2pHandlers.TapIntent)
					tapSensitive := p2p.Group("/tap")
					tapSensitive.Use(middleware.RequirePasscodeSession(container.GetPasscodeService(), true, container.ZapLog))
					tapSensitive.POST("/confirm", p2pHandlers.TapConfirm)
				}
			}

			// Public P2P claim routes (no auth required — for web claim page)
			// SECURITY: Rate limited to 3/min per IP to prevent claim token enumeration and bank detail brute-force
			if p2pHandlers != nil {
				publicP2P := router.Group("/api/v1/p2p")
				publicP2P.Use(middleware.AuthRateLimit(3))
				{
					publicP2P.GET("/claim/:token", p2pHandlers.GetClaimInfo)
					publicP2P.POST("/claim/:token/bank", p2pHandlers.ClaimToBank)
				}
			}

			// Household expense tracking
			if container.P2PService != nil {
				householdHandler := handlers.NewHouseholdHandler(sqlx.NewDb(container.DB, "postgres"), container.P2PService, container.ZapLog)
				household := protected.Group("/household/groups")
				{
					household.POST("", householdHandler.CreateGroup)
					household.POST("/:id/receipts", householdHandler.ShareReceipt)
					household.GET("/:id/summary", householdHandler.GetSummary)
				}
			}

			if container.FinancialObligationService != nil {
				obligationHandler := handlers.NewFinancialObligationHandler(container.FinancialObligationService, container.ZapLog)
				obligations := protected.Group("/financial-obligations")
				{
					obligations.POST("", obligationHandler.Create)
					obligations.GET("", obligationHandler.List)
					obligations.GET("/:id", obligationHandler.Get)
					obligations.PATCH("/:id", obligationHandler.Update)
					obligations.DELETE("/:id", obligationHandler.Delete)
				}
			}

			if container.MoneyGuardService != nil {
				moneyGuardHandler := handlers.NewMoneyGuardHandler(container.MoneyGuardService, container.ZapLog)
				moneyGuard := protected.Group("/money-guard")
				{
					moneyGuard.GET("/summary", moneyGuardHandler.Summary)
					moneyGuard.PATCH("/settings", moneyGuardHandler.UpdateSettings)
					moneyGuard.POST("/caps", moneyGuardHandler.CreateCap)
					moneyGuard.GET("/caps", moneyGuardHandler.ListCaps)
					moneyGuard.DELETE("/caps/:id", moneyGuardHandler.DeleteCap)
					moneyGuard.GET("/weekly-autopsy", moneyGuardHandler.WeeklyAutopsy)
					moneyGuard.GET("/recovery-plan", moneyGuardHandler.RecoveryPlan)
				}
			}

			// Notification routes - push tokens and in-app notifications
			notificationHandlers := handlers.NewNotificationHandlers(
				container.DeviceTokenRepo,
				container.NotificationRepo,
				container.ZapLog,
			)
			devices := protected.Group("/devices")
			{
				devices.POST("/token", notificationHandlers.RegisterDeviceToken)
				devices.DELETE("/token", notificationHandlers.UnregisterDeviceToken)
			}
			notifications := protected.Group("/notifications")
			{
				notifications.GET("", notificationHandlers.GetNotifications)
				notifications.GET("/unread-count", notificationHandlers.GetUnreadCount)
				notifications.POST("/:id/read", notificationHandlers.MarkAsRead)
				notifications.POST("/read-all", notificationHandlers.MarkAllAsRead)
			}

			// Investment routes
			basketExecutor := container.InitializeBasketExecutor()
			investingService := container.GetInvestingService()
			if basketExecutor != nil && investingService != nil {
				// Curated baskets endpoints
				baskets := protected.Group("/baskets")
				{
					baskets.GET("", func(c *gin.Context) {
						// Get curated baskets
						ctx := c.Request.Context()
						basketList, err := investingService.ListBaskets(ctx)
						if err != nil {
							common.SendInternalError(c, common.ErrCodeInternalError, "Failed to get baskets")
							return
						}
						c.JSON(200, gin.H{"baskets": basketList})
					})
					baskets.GET("/:id", func(c *gin.Context) {
						// Get basket by ID
						ctx := c.Request.Context()
						basketID, err := uuid.Parse(c.Param("id"))
						if err != nil {
							common.SendBadRequest(c, common.ErrCodeInvalidID, "Invalid basket ID")
							return
						}
						basket, err := investingService.GetBasket(ctx, basketID)
						if err != nil {
							common.SendInternalError(c, common.ErrCodeInternalError, "Failed to get basket")
							return
						}
						if basket == nil {
							common.SendNotFound(c, common.ErrCodeBasketNotFound, "Basket not found")
							return
						}
						c.JSON(200, basket)
					})
					baskets.POST("/:id/invest", func(c *gin.Context) {
						// Invest in basket
						ctx := c.Request.Context()
						userID, _ := uuid.Parse(c.GetString("user_id"))
						basketID, err := uuid.Parse(c.Param("id"))
						if err != nil {
							common.SendBadRequest(c, common.ErrCodeInvalidID, "Invalid basket ID")
							return
						}
						var req struct {
							Amount string `json:"amount" binding:"required"`
						}
						if err := c.ShouldBindJSON(&req); err != nil {
							common.SendBadRequest(c, common.ErrCodeInvalidRequest, err.Error())
							return
						}
						amount, err := decimal.NewFromString(req.Amount)
						if err != nil {
							common.SendBadRequest(c, common.ErrCodeInvalidAmount, "Invalid amount format")
							return
						}
						// Create order request
						orderReq := &entities.OrderCreateRequest{
							BasketID: basketID,
							Side:     entities.OrderSideBuy,
							Amount:   amount.String(),
						}
						order, err := investingService.CreateOrder(ctx, userID, orderReq)
						if err != nil {
							common.SendInternalError(c, common.ErrCodeOperationFailed, err.Error())
							return
						}
						c.JSON(201, gin.H{"order": order})
					})
				}
			}

			// Wallet routes (OpenAPI spec compliant)
			wallet := protected.Group("/wallet")
			{
				wallet.GET("/addresses", walletFundingHandlers.GetWalletAddresses)
				wallet.GET("/status", walletFundingHandlers.GetWalletStatus)
			}

			// Enhanced wallet endpoints
			wallets := protected.Group("/wallets")
			{
				wallets.POST("/initiate", walletFundingHandlers.InitiateWalletCreation)
				wallets.POST("/provision", walletFundingHandlers.ProvisionWallets)
				wallets.GET("/:chain/address", walletFundingHandlers.GetWalletByChain)
			}

			// Portfolio endpoints (STACK MVP spec compliant)
			portfolio := protected.Group("/portfolio")
			{
				portfolio.GET("/overview", walletFundingHandlers.GetPortfolio)

				// AI Financial Manager - Portfolio endpoints
				if container.GetPortfolioDataProvider() != nil {
					portfolioActivityHandlers := handlers.NewPortfolioActivityHandlers(
						container.GetPortfolioDataProvider(),
						container.GetActivityDataProvider(),
						container.GetStreakRepository(),
						container.GetContributionsRepository(),
						container.Logger,
					)
					portfolio.GET("/weekly-stats", portfolioActivityHandlers.GetWeeklyStats)
					portfolio.GET("/top-movers", portfolioActivityHandlers.GetTopMovers)
					portfolio.GET("/performance", portfolioActivityHandlers.GetPerformance)
				}
			}

			// Activity endpoints (AI Financial Manager)
			if container.GetActivityDataProvider() != nil {
				activity := protected.Group("/activity")
				{
					portfolioActivityHandlers := handlers.NewPortfolioActivityHandlers(
						container.GetPortfolioDataProvider(),
						container.GetActivityDataProvider(),
						container.GetStreakRepository(),
						container.GetContributionsRepository(),
						container.Logger,
					)
					activity.GET("/contributions", portfolioActivityHandlers.GetContributions)
					activity.GET("/streak", portfolioActivityHandlers.GetStreak)
					activity.GET("/timeline", portfolioActivityHandlers.GetTimeline)
				}
			}

			// AI Chat endpoints (AI Financial Manager)
			if container.GetAIOrchestrator() != nil {
				aiChatHandlers := handlers.NewAIChatHandlers(container.GetAIOrchestrator(), container.GetConversationService(), container.Logger)
				var voiceHandler interface {
					HandleSession(*gin.Context)
					IssueSessionToken(*gin.Context)
					CheckELHealth(*gin.Context)
					IssueSignedURL(*gin.Context)
					HandleToolExecution(*gin.Context)
					PrepareVoiceAction(*gin.Context)
					GetProactiveInsight(*gin.Context)
					HandleServerTool(*gin.Context)
				}
				if container.Config.AI.ElevenLabs.APIKey != "" && container.Config.AI.ElevenLabs.AgentID != "" {
					el := container.Config.AI.ElevenLabs
					ttsCfg := &infraai.ELTTSConfig{
						Stability:       el.Stability,
						SimilarityBoost: el.SimilarityBoost,
						Style:           el.Style,
						UseSpeakerBoost: el.UseSpeakerBoost,
					}
					if el.VoiceID != "" {
						ttsCfg.VoiceID = el.VoiceID
					}
					// Redis-backed voice session rate limiter (holds across replicas).
					// 10 sessions/user/hour. Nil-safe: if Redis is unavailable the
					// handler skips the check (fail-open).
					var voiceSessionLimiter *aiservice.VoiceSessionRateLimiter
					if container.RedisClient != nil {
						voiceSessionLimiter = aiservice.NewVoiceSessionRateLimiter(container.RedisClient, 10, container.ZapLog)
					}
					voiceHandler = handlers.NewVoiceHandler(
						el.APIKey,
						el.AgentID,
						container.Config.JWT.Secret,
						el.WebhookSecret,
						el.PidginVoiceID,
						container.GetAIOrchestrator(),
						container.GetUsageService(),
						container.GetConversationService(),
						voiceSessionLimiter,
						container.Config.Server.AllowedOrigins,
						container.ZapLog,
						ttsCfg,
					)
				}
				aiGroup := protected.Group("/ai")
				{
					aiGroup.POST("/chat", middleware.AuthRateLimit(20), middleware.PerUserRateLimit(20), aiChatHandlers.Chat)
					aiGroup.POST("/chat/stream", middleware.AuthRateLimit(20), middleware.PerUserRateLimit(20), aiChatHandlers.ChatStream)
					aiGroup.GET("/wrapped", middleware.AuthRateLimit(10), aiChatHandlers.GetWrapped)
					aiGroup.GET("/quick-insight", middleware.AuthRateLimit(20), aiChatHandlers.QuickInsight)
					aiGroup.GET("/financial-health", middleware.AuthRateLimit(20), aiChatHandlers.FinancialHealth)
					aiGroup.GET("/financial-audit", middleware.AuthRateLimit(20), aiChatHandlers.FinancialAudit)
					aiGroup.GET("/cash-flow-forecast", middleware.AuthRateLimit(20), aiChatHandlers.CashFlowForecast)
					aiGroup.GET("/financial-plan", middleware.AuthRateLimit(20), aiChatHandlers.FinancialPlan)
					aiGroup.GET("/action-receipts", middleware.AuthRateLimit(20), aiChatHandlers.ActionReceipts)
					aiGroup.GET("/financial-advice", middleware.AuthRateLimit(20), aiChatHandlers.FinancialAdvice)
					aiGroup.GET("/financial-timeline", middleware.AuthRateLimit(20), aiChatHandlers.FinancialTimeline)
					aiGroup.GET("/miriam-brief", middleware.AuthRateLimit(20), aiChatHandlers.MiriamBrief)
					if container.MiriamIntelligenceService != nil {
						miriamHandler := handlers.NewMiriamIntelligenceHandler(
							container.MiriamIntelligenceService,
							container.MiriamHealthScoreTracker,
							container.MiriamPredictiveEngine,
							container.MiriamMandateSuggestionEngine,
							container.ZapLog,
						)
						miriam := aiGroup.Group("/miriam")
						{
							miriam.GET("/state", middleware.AuthRateLimit(20), miriamHandler.GetState)
							miriam.POST("/state/refresh", middleware.AuthRateLimit(10), miriamHandler.RefreshState)
							miriam.GET("/mandates", middleware.AuthRateLimit(20), miriamHandler.ListMandates)
							miriam.POST("/mandates", middleware.AuthRateLimit(10), miriamHandler.CreateMandate)
							miriam.PATCH("/mandates/:id/status", middleware.AuthRateLimit(10), miriamHandler.UpdateMandateStatus)
							miriam.GET("/receipts", middleware.AuthRateLimit(20), miriamHandler.ListReceipts)
							miriam.POST("/receipts/:id/feedback", middleware.AuthRateLimit(20), miriamHandler.RecordFeedback)
							miriam.GET("/health-score", middleware.AuthRateLimit(20), miriamHandler.GetHealthScore)
							miriam.GET("/health-score/trend", middleware.AuthRateLimit(20), miriamHandler.GetHealthScoreTrend)
							miriam.GET("/predictions", middleware.AuthRateLimit(20), miriamHandler.GetPredictions)
							miriam.GET("/suggestions", middleware.AuthRateLimit(20), miriamHandler.ListSuggestions)
							miriam.POST("/suggestions/:id/accept", middleware.AuthRateLimit(10), miriamHandler.AcceptSuggestion)
							miriam.POST("/suggestions/:id/dismiss", middleware.AuthRateLimit(10), miriamHandler.DismissSuggestion)
						}
					}
					if container.MiriamPreferencesService != nil {
						prefsHandler := handlers.NewMiriamPreferencesHandler(container.MiriamPreferencesService, container.ZapLog)
						aiGroup.GET("/miriam/preferences", middleware.AuthRateLimit(20), prefsHandler.Get)
						aiGroup.PUT("/miriam/preferences", middleware.AuthRateLimit(10), prefsHandler.Put)
					}
					aiGroup.GET("/suggestions", aiChatHandlers.GetSuggestedQuestions)
					aiGroup.GET("/starters", middleware.AuthRateLimit(10), aiChatHandlers.GetConversationStarters)
					aiGroup.GET("/proactive-opener", middleware.AuthRateLimit(10), aiChatHandlers.GetProactiveOpener)
					aiGroup.POST("/nudge", middleware.AuthRateLimit(10), middleware.PerUserRateLimit(10), aiChatHandlers.Nudge)
					enhancedNudgeHandler := handlers.NewEnhancedNudgeHandler(container.GetAIOrchestrator(), container.ZapLog)
					aiGroup.POST("/nudge/enhanced", middleware.AuthRateLimit(10), middleware.PerUserRateLimit(10), enhancedNudgeHandler.HandleEnhancedNudge)

					if container.AutomationService != nil {
						automationHandler := handlers.NewAutomationHandler(container.AutomationService, container.ZapLog, container.GetPasscodeService())
						automations := aiGroup.Group("/automations")
						{
							automations.POST("", automationHandler.CreateAutomation)
							automations.GET("", automationHandler.ListAutomations)
							automations.GET("/logs", automationHandler.GetAutomationLogs)
							automations.GET("/:id", automationHandler.GetAutomation)
							automations.PATCH("/:id", automationHandler.UpdateAutomation)
							automations.DELETE("/:id", automationHandler.DeleteAutomation)
						}
					}

					// Image analysis (receipt scanning) — uses Cencori gateway (OpenAI-compatible)
					var imageHandler *handlers.ImageAnalysisHandler
					if container.Config.AI.Cencori.APIKey != "" {
						cencoriBase := "https://api.cencori.com/v1"
						imageHandler = handlers.NewImageAnalysisHandlerWithVision(
							container.Config.AI.Cencori.APIKey,
							cencoriBase,
							"gpt-4o",
							container.GetAIOrchestrator(),
							container.ReceiptRepo,
							container.ZapLog,
						)
					}
					if imageHandler != nil {
						imageHandler.SetBudgetRepo(container.BudgetRepo)
						imageHandler.SetSpendingRepo(container.LedgerSpendingRepo)
						imageHandler.SetBankStatementRepo(container.BankStatementRepo)
						if container.SupermemoryClient != nil {
							imageHandler.SetMemoryStore(&receiptMemoryAdapter{client: container.SupermemoryClient})
						}
						if container.GetConversationService() != nil {
							imageHandler.SetConversationPersister(container.GetConversationService())
						}
						aiGroup.POST("/chat/image", middleware.LargeBodyLimit(25*1024*1024), middleware.AuthRateLimit(10), imageHandler.AnalyzeImage)
						aiGroup.POST("/chat/images", middleware.LargeBodyLimit(25*1024*1024), middleware.AuthRateLimit(3), imageHandler.BatchAnalyzeImages)
						aiGroup.GET("/receipts", imageHandler.GetReceipts)
						aiGroup.GET("/receipts/gallery", imageHandler.GetReceiptGallery)
						aiGroup.PUT("/receipts/:id", imageHandler.UpdateReceipt)
						aiGroup.DELETE("/receipts/:id", imageHandler.DeleteReceipt)

						// Receipt split with friends
						if container.P2PService != nil {
							splitHandler := handlers.NewReceiptSplitHandler(container.ReceiptRepo, container.ReceiptSplitRepo, container.P2PService, container.ZapLog)
							aiGroup.POST("/receipts/:id/split", splitHandler.SplitReceipt)
						}

						// Receipt split tracking (list, detail, reminders, mark paid)
						if container.ReceiptSplitRepo != nil {
							splitTrackingHandler := handlers.NewReceiptSplitTrackingHandler(container.ReceiptSplitRepo, container.ZapLog)
							aiGroup.GET("/receipts/splits", splitTrackingHandler.ListSplits)
							aiGroup.GET("/receipts/splits/:id", splitTrackingHandler.GetSplit)
							aiGroup.POST("/receipts/splits/:id/remind", splitTrackingHandler.SendReminder)
							aiGroup.POST("/receipts/splits/:id/participants/:pid/paid", splitTrackingHandler.MarkPaid)
						}
					}

					// Miriam AI endpoints (available to all users)
					{
						premiumHandlers := handlers.NewPremiumAIHandlers(
							container.GetAIOrchestrator(),
							container.ZapLog,
							container.GetConversationService(),
						)
						premiumHandlers.SetPasscodeValidator(container.GetPasscodeService())
						aiGroup.GET("/report/weekly", middleware.AuthRateLimit(5), premiumHandlers.WeeklyReport)
						aiGroup.POST("/simulate", middleware.AuthRateLimit(10), premiumHandlers.Simulate)
						aiGroup.GET("/tax-summary", middleware.AuthRateLimit(5), premiumHandlers.TaxSummary)
						aiGroup.GET("/operating-plan", middleware.AuthRateLimit(10), premiumHandlers.OperatingPlan)
						aiGroup.POST("/operating-plan/actions", middleware.AuthRateLimit(10), premiumHandlers.StageOperatingPlanAction)
						aiGroup.GET("/money-across-borders-report", middleware.AuthRateLimit(5), premiumHandlers.MoneyAcrossBordersReport)
						aiGroup.POST("/challenge/generate", middleware.AuthRateLimit(10), premiumHandlers.GenerateChallenge)
						aiGroup.GET("/goals/progress", middleware.AuthRateLimit(10), premiumHandlers.GoalProgress)
					}

					// Bank statement upload & processing
					if container.BankStatementRepo != nil {
						var progressReporter *statement.RedisProgressReporter
						if container.RedisClient != nil {
							progressReporter = statement.NewRedisProgressReporter(container.RedisClient.Client(), container.ZapLog)
						}

						stmtHandler := handlers.NewStatementUploadHandler(container.BankStatementRepo, container.JobQueueInstance, progressReporter, container.ZapLog)
						aiGroup.POST("/statement/upload", middleware.LargeBodyLimit(25*1024*1024), middleware.AuthRateLimit(5), stmtHandler.Upload)
						aiGroup.GET("/statement/:id/status", middleware.AuthRateLimit(30), stmtHandler.GetStatus)
						aiGroup.GET("/statements", middleware.AuthRateLimit(20), stmtHandler.List)
						aiGroup.DELETE("/statement/:id", middleware.AuthRateLimit(10), stmtHandler.Delete)

						// V2 statement pipeline: supports images, OCR, real-time progress
						stmtHandlerV2 := handlers.NewStatementUploadHandlerV2(container.BankStatementRepo, container.JobQueueInstance, nil, progressReporter, container.ZapLog)
						aiGroup.POST("/v2/statement/upload", middleware.LargeBodyLimit(25*1024*1024), middleware.AuthRateLimit(5), stmtHandlerV2.Upload)
						aiGroup.GET("/v2/statement/:id/progress", middleware.AuthRateLimit(60), stmtHandlerV2.StreamProgress)
						// V2 reuses V1 status/list/delete/transactions endpoints (same DB)
						aiGroup.GET("/statement/:id/transactions", middleware.AuthRateLimit(20), stmtHandler.GetTransactions)
					}

					// Voice session ticket issuance (protected by standard auth).
					// WebSocket endpoint uses its own voice session token auth (no Bearer/CSRF).
					if voiceHandler != nil {
						aiGroup.POST("/voice/session-token", middleware.AuthRateLimit(60), middleware.PerUserRateLimit(60), voiceHandler.IssueSessionToken)
						aiGroup.POST("/voice/signed-url", middleware.AuthRateLimit(60), middleware.PerUserRateLimit(60), voiceHandler.IssueSignedURL)
						aiGroup.POST("/voice/execute-tool", middleware.AuthRateLimit(30), middleware.PerUserRateLimit(30), voiceHandler.HandleToolExecution)
						aiGroup.POST("/voice/prepare-action", middleware.AuthRateLimit(30), middleware.PerUserRateLimit(30), voiceHandler.PrepareVoiceAction)
						aiGroup.GET("/voice/proactive-insight", middleware.PerUserRateLimit(30), voiceHandler.GetProactiveInsight)
						v1.GET("/ai/voice/health", voiceHandler.CheckELHealth)
						v1.GET("/ai/voice/session", voiceHandler.HandleSession)
						// ElevenLabs server tool webhook (public, authenticated by webhook secret)
						v1.POST("/ai/voice/server-tool/:tool_name", voiceHandler.HandleServerTool)
					}

					// Support agent (ElevenLabs)
					if container.Config.AI.ElevenLabs.SupportAgentID != "" {
						supportHandler := handlers.NewSupportHandler(
							container.Config.AI.ElevenLabs.APIKey,
							container.Config.AI.ElevenLabs.SupportAgentID,
							container.ZapLog,
						)
						aiGroup.POST("/support/signed-url", middleware.AuthRateLimit(30), middleware.PerUserRateLimit(30), supportHandler.IssueSignedURL)
					}
				}

				// Conversation endpoints. The "gate-on + nil-passcode" invariant
				// is validated at application startup (Application.validateSecurityConfig),
				// so the gate is guaranteed wired by the time we reach this route
				// setup — no mid-route Fatal here.
				if container.GetConversationService() != nil {
					convHandlers := handlers.NewConversationHandlers(
						container.GetAIOrchestrator(),
						container.GetConversationService(),
						container.ZapLog,
						container.GetPasscodeService(),
						container.Config.Security.AIFundActionsRequirePasscode,
					)
					convGroup := protected.Group("/ai/conversations")
					{
						convGroup.POST("", convHandlers.CreateConversation)
						convGroup.GET("", convHandlers.ListConversations)
						convGroup.GET("/:id", convHandlers.GetConversation)
						convGroup.DELETE("/:id", convHandlers.DeleteConversation)
						convGroup.POST("/:id/chat", middleware.AuthRateLimit(20), middleware.PerUserRateLimit(20), convHandlers.ChatInConversation)
						convGroup.POST("/:id/confirm", convHandlers.ConfirmAction)
						convGroup.POST("/:id/cancel", convHandlers.CancelAction)
					}
				}

				// Usage tracking endpoint
				if container.GetUsageService() != nil {
					usageHandlers := handlers.NewUsageHandlers(container.GetUsageService(), container.ZapLog)
					aiGroup.GET("/usage", usageHandlers.GetUsage)
				}
			}

			// News endpoints (AI Financial Manager)
			if container.GetNewsService() != nil {
				newsHandlers := handlers.NewNewsHandlers(container.GetNewsService(), container.Logger)
				news := protected.Group("/news")
				{
					news.GET("/feed", newsHandlers.GetFeed)
					news.GET("/weekly", newsHandlers.GetWeeklyNews)
					news.POST("/read", newsHandlers.MarkAsRead)
					news.GET("/unread-count", newsHandlers.GetUnreadCount)
					news.POST("/refresh", newsHandlers.RefreshNews)
				}
			}

			// Alpaca Assets - Tradable stocks and ETFs (cached 5min — asset list rarely changes)
			assets := protected.Group("/assets")
			{
				assets.GET("/", middleware.PublicCache(300), integrationHandlers.GetAssets)
				assets.GET("/:symbol_or_id", middleware.PublicCache(300), integrationHandlers.GetAsset)
			}

			// Allocation routes - 70/30 Smart Allocation Mode (ON/OFF)
			allocation := protected.Group("/allocation")
			allocation.Use(middleware.RequireCryptoCapability(container.UserRepo, container.ZapLog))
			{
				allocation.POST("/enable", allocationHandlers.EnableAllocationMode)
				allocation.POST("/disable", allocationHandlers.DisableAllocationMode)
				allocation.GET("/balances", allocationHandlers.GetAllocationBalances)
			}
		}

		// Admin bootstrap route (enforces super admin token after initial creation)
		// v1.POST("/admin/users", adminHandlers.CreateAdmin)
		// v1.POST("/admin/promote", adminHandlers.PromoteUser)

		// Admin routes (admin auth required)
		admin := v1.Group("/admin")
		admin.Use(middleware.Authentication(container.Config, container.Logger, sessionValidator, container.TokenBlacklist))
		admin.Use(middleware.AdminAuth(container.DB, container.Logger))
		admin.Use(middleware.CSRFProtection(csrfStore))
		{
			// Complete stuck PAJ orders (internal key auth)
			admin.POST("/paj/complete/:order_id", func(c *gin.Context) {
				key := c.GetHeader("X-Internal-Key")
				if key == "" || key != container.Config.JWT.Secret {
					c.JSON(401, gin.H{"error": "unauthorized"})
					return
				}
				orderID := c.Param("order_id")
				if orderID == "" {
					c.JSON(400, gin.H{"error": "order_id required"})
					return
				}
				result, err := container.DB.ExecContext(c.Request.Context(),
					`UPDATE paj_orders SET status = 'completed', updated_at = NOW() WHERE paj_order_id = $1 AND status NOT IN ('completed', 'failed')`, orderID)
				if err != nil {
					c.JSON(500, gin.H{"error": err.Error()})
					return
				}
				rows, _ := result.RowsAffected()
				c.JSON(200, gin.H{"updated": rows, "order_id": orderID})
			})

			// User lookup
			admin.GET("/users/lookup", handlers.AdminLookupUser(container.DB))

			// Wallet admin routes
			admin.POST("/wallet/create", walletFundingHandlers.CreateWalletsForUser)
			admin.POST("/wallet/retry-provisioning", walletFundingHandlers.RetryWalletProvisioning)
			admin.GET("/wallet/health", walletFundingHandlers.HealthCheck)
			admin.POST("/reconcile/:user_id", walletFundingHandlers.ReconcileUserBalance)

			// Stash reconciliation — manually trigger a check of ledger vs yield-provider balance
			if container.StashReconciliation != nil {
				admin.POST("/stash/reconcile", handlers.TriggerStashReconciliation(container.StashReconciliation, container.ZapLog))
			}

			// Blend stash backfill — one-time: route every user's current stash balance into Blend
			if container.BlendDepositRouter != nil {
				blendAdminHandlers := handlers.NewBlendHandlers(container.BlendDepositRouter, container.ZapLog)
				admin.POST("/blend/backfill-stash", blendAdminHandlers.TriggerStashBackfill)
			}

			// KYC admin routes
			admin.POST("/kyc/resync-bridge", kycHTTPHandlers.ResyncBridge)
			admin.POST("/kyc/repair-bridge-govid", kycHTTPHandlers.RepairBridgeGovID)

			// Knowledge base admin routes
			if container.GetKnowledgeService() != nil {
				knowledgeHandlers := handlers.NewKnowledgeHandlers(container.GetKnowledgeService(), container.ZapLog)
				admin.POST("/knowledge/ingest", knowledgeHandlers.Ingest)
			}

			// Waitlist admin routes
			if container.WaitlistService != nil {
				wlHandlers := waitlisthandlers.NewHandlers(container.WaitlistService, container.ZapLog)
				admin.GET("/waitlist", wlHandlers.List)
			}

			// Analytics admin routes
			// Analytics routes moved to /api/v1/dashboard/analytics (JWT-only auth)
			mixpanelBackfill := admin_handlers.NewMixpanelBackfillHandler(container.DB, container.ZapLog)
			admin.POST("/analytics/backfill", mixpanelBackfill.TriggerBackfill)

			// Security admin routes
			adminMFAHandlers := handlers.NewMFAHandlers(
				container.GetMFAService(),
				container.GetGeoSecurityService(),
				container.GetIncidentResponseService(),
				container.ZapLog,
			)
			adminSecurity := admin.Group("/security")
			{
				// Security dashboard
				adminSecurity.GET("/dashboard", adminMFAHandlers.GetSecurityDashboard)

				// Incident management
				adminSecurity.GET("/incidents", adminMFAHandlers.GetOpenIncidents)
				adminSecurity.GET("/incidents/:id", adminMFAHandlers.GetIncident)
				adminSecurity.PUT("/incidents/:id/status", adminMFAHandlers.UpdateIncidentStatus)
				adminSecurity.POST("/incidents/:id/playbook", adminMFAHandlers.ExecutePlaybook)

				// Geo-blocking management
				adminSecurity.GET("/blocked-countries", adminMFAHandlers.GetBlockedCountries)
				adminSecurity.POST("/blocked-countries", adminMFAHandlers.BlockCountry)
				adminSecurity.DELETE("/blocked-countries/:country_code", adminMFAHandlers.UnblockCountry)
			}
		}

		// Internal-only endpoints (X-Internal-Key auth, no session required)
		if container.CircleAdapter != nil {
			v1.POST("/ops/reverse-sol", func(c *gin.Context) {
				key := c.GetHeader("X-Internal-Key")
				if key == "" || key != container.Config.JWT.Secret {
					c.JSON(401, gin.H{"error": "unauthorized"})
					return
				}
				var req struct {
					WalletID    string `json:"wallet_id" binding:"required"`
					Destination string `json:"destination" binding:"required"`
					Lamports    uint64 `json:"lamports" binding:"required"`
				}
				if err := c.ShouldBindJSON(&req); err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"error": "wallet_id, destination, and lamports required"})
					return
				}
				if req.Lamports == 0 {
					c.JSON(http.StatusBadRequest, gin.H{"error": "lamports must be > 0"})
					return
				}
				if req.Lamports > 1_000_000_000 { // >1 SOL safety cap
					c.JSON(http.StatusBadRequest, gin.H{"error": "safety cap: max 1 SOL (1e9 lamports) per reversal"})
					return
				}
				container.ZapLog.Info("SOL reversal requested",
					zap.String("wallet_id", req.WalletID),
					zap.String("destination", req.Destination),
					zap.Uint64("lamports", req.Lamports))

				sig, err := container.CircleAdapter.ReverseNativeSOL(
					c.Request.Context(), req.WalletID, req.Destination, req.Lamports)
				if err != nil {
					container.ZapLog.Error("SOL reversal failed", zap.Error(err))
					c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
					return
				}
				c.JSON(http.StatusOK, gin.H{"signature": sig})
			})
		}

		// Webhooks (external systems) - OpenAPI spec compliant
		// Apply webhook security middleware (rate limiting, IP whitelisting, replay protection)
		webhookConfig := middleware.DefaultWebhookSecurityConfig()
		webhookConfig.Environment = container.Config.Environment
		webhookConfig.Secrets = map[string]string{
			"bridge": container.Config.Bridge.WebhookSecret,
			"alpaca": container.Config.Alpaca.WebhookSecret,
		}

		webhooks := v1.Group("/webhooks")
		if redisNative := container.RedisClient.Client(); redisNative != nil {
			webhooks.Use(middleware.WebhookSecurityWithRedisV8(
				redisNative,
				webhookConfig,
				container.ZapLog,
			))
		}
		// Hardened per-provider signature + timestamp verification
		webhookSigSecrets := container.Config.Security.WebhookSignatureSecrets
		if webhookSigSecrets.Bridge != "" || webhookSigSecrets.Alpaca != "" {
			webhooks.Use(middleware.HardenedWebhookVerification(
				middleware.WebhookProviderConfig{
					BridgeSecret: webhookSigSecrets.Bridge,
					AlpacaSecret: webhookSigSecrets.Alpaca,
				},
				container.ZapLog,
			))
		}
		{
			webhooks.POST("/chain-deposit", walletFundingHandlers.ChainDepositWebhook)
			webhooks.POST("/brokerage-fill", walletFundingHandlers.BrokerageFillWebhook)

			// Unified funding webhook - routes based on source header/payload
			// POST /webhooks/funding - handles Bridge and Alpaca webhooks
			if unifiedWebhookHandler := container.GetUnifiedFundingWebhookHandler(); unifiedWebhookHandler != nil {
				webhooks.POST("/funding", unifiedWebhookHandler.HandleFundingWebhook)
			}

			// Bridge webhooks for fiat deposits and transfers
			if bridgeWebhookHandler := container.GetBridgeWebhookHandler(); bridgeWebhookHandler != nil {
				webhooks.POST("/bridge", bridgeWebhookHandler.HandleWebhook)
				// Synchronous real-time card authorization webhook (Bridge calls this during transactions)
				webhooks.POST("/bridge/card-auth", bridgeWebhookHandler.HandleRealTimeAuth)
			}

			// ChainRails webhooks for cross-chain deposits
			if container.ChainRailsHandlers != nil {
				chainrailsWebhooks := webhooks.Group("/chainrails")
				chainrailsWebhooks.Use(middleware.RateLimit(100)) // 100 req/min per IP
				chainrailsWebhooks.POST("", container.ChainRailsHandlers.HandleWebhook)
			}

			// Paj Cash webhooks (per-order, no signature verification — verified by polling)
			if container.PajHandlers != nil {
				pajWebhooks := webhooks.Group("/paj")
				pajWebhooks.Use(middleware.RateLimit(100))
				pajWebhooks.POST("", container.PajHandlers.HandleWebhook)
			}

			// RampHub webhooks (HMAC-SHA256 signed; verified in the handler)
			if container.RampHandlers != nil {
				ramphubPath := container.Config.RampHub.WebhookPath
				if ramphubPath == "" {
					ramphubPath = "/ramphub"
				}
				ramphubWebhooks := webhooks.Group(ramphubPath)
				ramphubWebhooks.Use(middleware.RateLimit(100))
				ramphubWebhooks.POST("", container.RampHandlers.HandleWebhook)
			}

			// Airbills webhooks (HMAC-SHA256 signed; verified in the handler)
			if container.BillPayHandlers != nil {
				airbillsPath := container.Config.Airbills.WebhookPath
				if airbillsPath == "" {
					airbillsPath = "/airbills"
				}
				airbillsWebhooks := webhooks.Group(airbillsPath)
				airbillsWebhooks.Use(middleware.RateLimit(100))
				airbillsWebhooks.POST("", container.BillPayHandlers.HandleWebhook)
			}

			// Circle Programmable Wallets webhooks for inbound deposits
			if circleWebhookHandler := container.GetCircleWebhookHandler(); circleWebhookHandler != nil {
				circleWebhooks := webhooks.Group("/circle")
				circleWebhooks.Use(middleware.RateLimit(100))
				circleWebhooks.Use(middleware.CircleIPAllowlist(container.Config.Environment, container.ZapLog))
				circleWebhooks.POST("", circleWebhookHandler.HandleWebhook)
			}

			// Graph (useoval.com) webhooks for NGN named virtual account deposits
			if container.GraphWebhookHandler != nil {
				graphPath := container.Config.Graph.WebhookPath
				if graphPath == "" {
					graphPath = "/graph"
				}
				graphWebhooks := webhooks.Group(graphPath)
				graphWebhooks.Use(middleware.RateLimit(100))
				graphWebhooks.POST("", container.GraphWebhookHandler.HandleWebhook)
			}
		}

		// Register Alpaca investment routes
		if container.GetInvestmentHandlers() != nil {
			RegisterAlpacaRoutes(
				v1,
				container.GetInvestmentHandlers(),
				container.GetAlpacaWebhookHandlers(),
				container.Config,
				container.Logger,
				sessionValidator,
				container.UserRepo,
				container.TokenBlacklist,
			)
		}

		// Register advanced features routes (analytics, market, scheduled investments, rebalancing)
		if container.GetAnalyticsHandlers() != nil {
			RegisterAdvancedFeaturesRoutes(
				v1,
				container.GetAnalyticsHandlers(),
				container.GetMarketHandlers(),
				container.GetScheduledInvestmentHandlers(),
				container.GetRebalancingHandlers(),
				container.Config,
				container.Logger,
				sessionValidator,
				container.TokenBlacklist,
			)
		}

		// Register round-up routes
		RegisterRoundupRoutes(
			v1,
			container.GetRoundupHandlers(),
			container.Config,
			container.Logger,
			sessionValidator,
			container.TokenBlacklist,
		)

		// Register copy trading routes
		if container.GetCopyTradingHandlers() != nil {
			copyTradingHandlers := container.GetCopyTradingHandlers()
			authMiddleware := middleware.Authentication(container.Config, container.Logger, sessionValidator, container.TokenBlacklist)
			SetupCopyTradingRoutes(v1, copyTradingHandlers, authMiddleware)
		}

		// Register card routes
		RegisterCardRoutes(
			v1,
			container.GetCardHandlers(),
			container.Config,
			container.Logger,
			sessionValidator,
			container.UserRepo,
			container.TokenBlacklist,
		)

		// Register collaborative savings goal routes (also created via Miriam)
		if container.SharedGoalService != nil {
			RegisterSharedGoalRoutes(protected, container.SharedGoalService, container.ZapLog)
		}

		// Register opportunity routes
		RegisterOpportunityRoutes(
			v1,
			internal,
			container.GetOpportunityHandlers(),
			container.Config,
			container.Logger,
			sessionValidator,
			container.TokenBlacklist,
		)

		// Register premium feature routes
		if premiumHandlers := container.GetPremiumHandlers(); premiumHandlers != nil {
			premium := protected.Group("/premium")
			{
				// Tier 1: Naira Shield
				premium.GET("/naira-shield", premiumHandlers.GetNairaShield)
				// Tier 1: Black Tax
				premium.GET("/black-tax", premiumHandlers.GetBlackTaxSummary)
				premium.POST("/black-tax/budget", premiumHandlers.SetBlackTaxBudget)
				premium.POST("/black-tax/sync-recipients", premiumHandlers.SyncBlackTaxRecipients)
				// Tier 1: Receipt Split
				premium.POST("/receipts/split", premiumHandlers.SplitReceipt)
				// Tier 2: Scam Intelligence
				premium.POST("/scam/check-merchant", premiumHandlers.CheckMerchant)
				premium.GET("/scam/alerts", premiumHandlers.GetScamAlerts)
				premium.POST("/scam/alerts/:alertId/dismiss", premiumHandlers.DismissScamAlert)
				// Tier 2: Tax Residency
				premium.POST("/tax/location", premiumHandlers.LogLocation)
				premium.GET("/tax/residency", premiumHandlers.GetTaxResidency)
				premium.POST("/tax/profile", premiumHandlers.SetTaxProfile)
				// Tier 2: Income Smoothing
				premium.GET("/income/forecast", premiumHandlers.GetIncomeForecast)
				// Tier 3: Financial Trauma
				premium.GET("/wellness/score", premiumHandlers.GetWellnessScore)
				// Tier 3: Visa Proof
				premium.POST("/visa-proof", premiumHandlers.GenerateVisaProof)
				premium.GET("/visa-proof", premiumHandlers.GetVisaProofs)
				// Tier 3: Panic Button
				premium.GET("/emergency/contacts", premiumHandlers.GetEmergencyContacts)
				premium.POST("/emergency/contacts", premiumHandlers.AddEmergencyContact)
				premium.DELETE("/emergency/contacts/:contactId", premiumHandlers.RemoveEmergencyContact)
				premium.POST("/emergency/lock", premiumHandlers.TriggerEmergencyLock)
				premium.GET("/emergency/lock", premiumHandlers.GetEmergencyLock)
			}
		}
	}

	// ZeroG and dedicated AI-CFO HTTP routes have been removed.

	// Catch-all: unmatched routes get a clean 404 (not 503)
	router.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{
			"error":      "NOT_FOUND",
			"message":    "Route not found",
			"request_id": c.GetString("request_id"),
		})
	})

	return router
}

func createDistributedRateLimiter(container *di.Container) *ratelimit.DistributedRateLimiter {
	rateLimitConfig := container.GetRateLimitConfig()

	limiter := container.GetTieredRateLimiter()

	return ratelimit.NewDistributedRateLimiter(limiter, *rateLimitConfig, container.Logger.Zap())
}

func createRateLimitMiddleware(container *di.Container) gin.HandlerFunc {
	distributedRL := createDistributedRateLimiter(container)
	return distributedRL.Middleware()
}

func normalizeMiriamInternalEvent(eventType string) string {
	switch strings.TrimSpace(eventType) {
	case "idle_spend", "spending_spike", "bill_pressure", "income_lower_than_usual":
		return strings.TrimSpace(eventType)
	default:
		return "worker_sweep"
	}
}
