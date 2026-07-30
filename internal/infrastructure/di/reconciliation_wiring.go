package di

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/reconciliation"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/alpaca"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/bridge"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	commonmetrics "github.com/rail-service/rail_service/pkg/common/metrics"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

func (c *Container) initializeReconciliationService() error {

	// Create reconciliation service config
	reconciliationConfig := &reconciliation.Config{
		AutoCorrectLowSeverity: true,
		ToleranceCircle:        decimal.NewFromFloat(10.0),
		ToleranceAlpaca:        decimal.NewFromFloat(100.0),
		EnableAlerting:         true,
		AlertWebhookURL:        c.Config.Reconciliation.AlertWebhookURL,
	}

	// Initialize reconciliation service with all dependencies
	metricsService := &reconciliationMetricsService{}
	c.ReconciliationService = reconciliation.NewService(
		c.ReconciliationRepo,
		c.LedgerRepo,
		c.DepositRepo,
		c.WithdrawalRepo,
		c.ConversionRepo,
		c.LedgerService,
		&bridgeBalanceAdapter{
			bridgeAdapter: c.BridgeAdapter,
			walletRepo:    c.WalletRepo,
			userRepo:      c.UserRepo,
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
type bridgeBalanceAdapter struct {
	bridgeAdapter *bridge.Adapter
	walletRepo    *repositories.WalletRepository
	userRepo      *repositories.UserRepository
}

func (a *bridgeBalanceAdapter) GetTotalUSDCBalance(ctx context.Context) (decimal.Decimal, error) {
	filters := repositories.WalletListFilters{
		Status: (*entities.WalletStatus)(ptrOf(entities.WalletStatusLive)),
		Limit:  10000,
		Offset: 0,
	}
	wallets, _, err := a.walletRepo.ListWithFilters(ctx, filters)
	if err != nil {
		return decimal.Zero, fmt.Errorf("failed to list wallets: %w", err)
	}

	total := decimal.Zero
	for _, wallet := range wallets {
		if wallet.BridgeWalletID == "" {
			continue
		}
		user, err := a.userRepo.GetByID(ctx, wallet.UserID)
		if err != nil || user == nil || user.BridgeCustomerID == nil || *user.BridgeCustomerID == "" {
			continue
		}
		wb, err := a.bridgeAdapter.GetWalletBalance(ctx, *user.BridgeCustomerID, wallet.BridgeWalletID)
		if err != nil {
			continue
		}
		if amt, err := decimal.NewFromString(wb.GetUSDCAmount()); err == nil {
			total = total.Add(amt)
		}
	}
	return total, nil
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
		logger.Fatal("bridge.supported_chains not configured - refusing to start with default testnet chain")
		return nil // unreachable; Fatal calls os.Exit
	}

	normalized := make([]entities.WalletChain, 0, len(raw))
	seen := make(map[entities.WalletChain]struct{})

	for _, entry := range raw {
		if strings.TrimSpace(entry) == "" {
			continue
		}

		upper := strings.ToUpper(strings.TrimSpace(entry))
		chain := entities.WalletChain(upper)
		if !chain.IsValid() {
			normalizedKey := strings.NewReplacer("-", "_", " ", "_").Replace(upper)
			switch normalizedKey {
			case "SOLANA", "SOL":
				chain = entities.WalletChainSolana
			case "ETHEREUM", "ETH":
				chain = entities.WalletChainEthereum
			case "POLYGON", "MATIC":
				chain = entities.WalletChainPolygon
			case "CELO":
				chain = entities.WalletChainCelo
			case "BASE":
				chain = entities.WalletChainBase
			case "AVALANCHE", "AVAX":
				chain = entities.WalletChainAvalanche
			case "ARBITRUM", "ARB":
				chain = entities.WalletChainArbitrum
			case "OPTIMISM", "OP":
				chain = entities.WalletChainOptimism
			default:
				logger.Warn("Ignoring unsupported wallet chain from configuration", zap.String("chain", upper))
				continue
			}
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
		logger.Fatal("bridge.supported_chains contained no valid entries - refusing to start with default testnet chain")
		return nil // unreachable; Fatal calls os.Exit
	}

	return normalized
}

// Check if AI is configured
