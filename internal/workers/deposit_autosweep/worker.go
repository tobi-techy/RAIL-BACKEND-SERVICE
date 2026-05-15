package deposit_autosweep

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/chainrails"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

const (
	pollInterval = 30 * time.Second
	maxAttempts  = 5
)

var (
	sweepsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "rail_deposit_sweeps_total",
		Help: "Total deposit sweeps by outcome",
	}, []string{"status"})
	sweepDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "rail_deposit_sweep_duration_seconds",
		Help:    "Time from sweep creation to completion",
		Buckets: []float64{10, 30, 60, 120, 300, 600, 1800},
	})
	sweepAttempts = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "rail_deposit_sweep_attempts",
		Help:    "Number of attempts before sweep resolution",
		Buckets: []float64{1, 2, 3, 4, 5},
	})
)

// Alerter sends alerts when sweeps exhaust retries.
type Alerter interface {
	SendSweepExhausted(sweepID, depositID, sourceChain string, amount string, attempts int)
}

// WalletLookup resolves a user's managed wallet by chain.
type WalletLookup interface {
	GetByUserAndChain(ctx context.Context, userID uuid.UUID, chain entities.WalletChain) (*entities.ManagedWallet, error)
}

// Worker polls for pending deposit sweeps and creates ChainRails intents to bridge to Solana.
type Worker struct {
	sweepRepo *repositories.DepositSweepRepository
	walletRepo WalletLookup
	crClient   *chainrails.Client
	alerter    Alerter
	logger     *zap.Logger
	stopCh     chan struct{}
	stopOnce   sync.Once
	running    int32
}

func NewWorker(
	sweepRepo *repositories.DepositSweepRepository,
	walletRepo WalletLookup,
	crClient *chainrails.Client,
	alerter Alerter,
	logger *zap.Logger,
) *Worker {
	return &Worker{
		sweepRepo:  sweepRepo,
		walletRepo: walletRepo,
		crClient:   crClient,
		alerter:    alerter,
		logger:     logger,
		stopCh:     make(chan struct{}),
	}
}

func (w *Worker) Start() {
	w.logger.Info("Deposit auto-sweep worker started")
	go func() {
		defer func() {
			if r := recover(); r != nil {
				w.logger.Error("Deposit auto-sweep worker panicked",
					zap.Any("panic", r), zap.Stack("stack"))
			}
		}()
		w.poll()
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-w.stopCh:
				return
			case <-ticker.C:
				w.poll()
			}
		}
	}()
}

func (w *Worker) Stop() {
	w.stopOnce.Do(func() {
		w.logger.Info("Deposit auto-sweep worker stopping")
		close(w.stopCh)
	})
}

func (w *Worker) poll() {
	if !atomic.CompareAndSwapInt32(&w.running, 0, 1) {
		return
	}
	defer atomic.StoreInt32(&w.running, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Process pending sweeps
	sweeps, err := w.sweepRepo.GetPending(ctx, maxAttempts)
	if err != nil {
		w.logger.Error("Failed to get pending sweeps", zap.Error(err))
		return
	}
	if len(sweeps) > 0 {
		w.logger.Info("Processing pending deposit sweeps", zap.Int("count", len(sweeps)))
		for _, sweep := range sweeps {
			if err := w.processSweep(ctx, sweep); err != nil {
				w.logger.Error("Failed to process sweep",
					zap.String("sweep_id", sweep.ID.String()),
					zap.String("deposit_id", sweep.DepositID.String()),
					zap.Error(err))
				if markErr := w.sweepRepo.MarkFailed(ctx, sweep.ID, err.Error()); markErr != nil {
					w.logger.Error("Failed to mark sweep as failed",
						zap.String("sweep_id", sweep.ID.String()), zap.Error(markErr))
				}
				sweepsTotal.WithLabelValues("failed").Inc()

				// Alert exactly once when this attempt exhausts retries
				if sweep.Attempts+1 == maxAttempts && w.alerter != nil {
					w.alerter.SendSweepExhausted(
						sweep.ID.String(), sweep.DepositID.String(),
						sweep.SourceChain, sweep.Amount.StringFixed(2), sweep.Attempts+1,
					)
				}
			} else {
				sweepsTotal.WithLabelValues("initiated").Inc()
			}
		}
	}

	// Reconcile stale in-progress sweeps (stuck > 10 minutes)
	w.reconcileStale(ctx)
}

func (w *Worker) processSweep(ctx context.Context, sweep *repositories.DepositSweep) error {
	// Resolve source wallet (where the deposit landed)
	sourceChain := walletChainFromCircleChain(sweep.SourceChain)
	if sourceChain == "" {
		return fmt.Errorf("unsupported source chain: %s", sweep.SourceChain)
	}
	sourceWallet, err := w.walletRepo.GetByUserAndChain(ctx, sweep.UserID, sourceChain)
	if err != nil {
		return fmt.Errorf("resolve source wallet for chain %s: %w", sweep.SourceChain, err)
	}
	if sourceWallet == nil {
		return fmt.Errorf("no wallet found for user %s on chain %s", sweep.UserID, sweep.SourceChain)
	}

	// Resolve destination (user's Solana wallet)
	solWallet, err := w.walletRepo.GetByUserAndChain(ctx, sweep.UserID, entities.WalletChainSolana)
	if err != nil {
		return fmt.Errorf("resolve solana wallet: %w", err)
	}
	if solWallet == nil {
		return fmt.Errorf("no solana wallet found for user %s", sweep.UserID)
	}

	crSourceChain := circleChainToChainRailsChain(sweep.SourceChain)
	if crSourceChain == "" {
		return fmt.Errorf("unsupported source chain for sweep: %s", sweep.SourceChain)
	}

	tokenIn := usdcTokenForChain(crSourceChain)
	if tokenIn == "" {
		return fmt.Errorf("no USDC token address for chain: %s", crSourceChain)
	}

	intent, err := w.crClient.CreateIntent(ctx, &chainrails.CreateIntentRequest{
		Sender:           sourceWallet.Address,
		Amount:           sweep.Amount.StringFixed(6),
		AmountSymbol:     "USDC",
		TokenIn:          tokenIn,
		SourceChain:      crSourceChain,
		DestinationChain: "SOLANA_MAINNET",
		Recipient:        solWallet.Address,
		RefundAddress:    sourceWallet.Address,
		Metadata: map[string]interface{}{
			"type":       "deposit_sweep",
			"sweep_id":   sweep.ID.String(),
			"deposit_id": sweep.DepositID.String(),
		},
	})
	if err != nil {
		return fmt.Errorf("create chainrails intent: %w", err)
	}

	if err := w.sweepRepo.MarkInProgress(ctx, sweep.ID, intent.IntentAddress, intent.ID, parseSweepFee(intent)); err != nil {
		return fmt.Errorf("mark in_progress: %w", err)
	}

	w.logger.Info("Deposit sweep intent created",
		zap.String("sweep_id", sweep.ID.String()),
		zap.String("intent_address", intent.IntentAddress),
		zap.Int("intent_id", intent.ID))
	return nil
}

// walletChainFromCircleChain maps Circle blockchain identifiers to WalletChain.
// Returns empty string for unsupported chains.
func walletChainFromCircleChain(chain string) entities.WalletChain {
	switch strings.ToUpper(chain) {
	case "ETH", "ETH-SEPOLIA":
		return entities.WalletChainEthereum
	case "BASE", "BASE-SEPOLIA":
		return entities.WalletChainBase
	case "MATIC", "MATIC-AMOY":
		return entities.WalletChainPolygon
	case "ARB", "ARB-SEPOLIA":
		return entities.WalletChainArbitrum
	case "OP", "OP-SEPOLIA":
		return entities.WalletChainOptimism
	case "AVAX", "AVAX-FUJI":
		return entities.WalletChainAvalanche
	default:
		return ""
	}
}

// circleChainToChainRailsChain maps Circle chain IDs to ChainRails chain IDs.
func circleChainToChainRailsChain(chain string) string {
	switch strings.ToUpper(chain) {
	case "ETH":
		return "ETHEREUM_MAINNET"
	case "ETH-SEPOLIA":
		return "ETHEREUM_TESTNET"
	case "BASE":
		return "BASE_MAINNET"
	case "BASE-SEPOLIA":
		return "BASE_TESTNET"
	case "MATIC":
		return "POLYGON_MAINNET"
	case "MATIC-AMOY":
		return "POLYGON_MAINNET" // ChainRails has no Polygon testnet; route to mainnet
	case "ARB":
		return "ARBITRUM_MAINNET"
	case "ARB-SEPOLIA":
		return "ARBITRUM_TESTNET"
	case "OP":
		return "OPTIMISM_MAINNET"
	case "OP-SEPOLIA":
		return "OPTIMISM_TESTNET"
	case "AVAX":
		return "AVALANCHE_MAINNET"
	case "AVAX-FUJI":
		return "AVALANCHE_TESTNET"
	default:
		return ""
	}
}

// usdcTokenForChain returns the USDC contract/mint address for a ChainRails chain.
// Returns empty string for unsupported/testnet chains.
func usdcTokenForChain(chain string) string {
	switch chain {
	case "ETHEREUM_MAINNET":
		return "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"
	case "ETHEREUM_TESTNET":
		return "0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238"
	case "BASE_MAINNET":
		return "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"
	case "BASE_TESTNET":
		return "0x036CbD53842c5426634e7929541eC2318f3dCF7e"
	case "POLYGON_MAINNET":
		return "0x3c499c542cEF5E3811e1192ce70d8cC03d5c3359"
	case "ARBITRUM_MAINNET":
		return "0xaf88d065e77c8cC2239327C5EDb3A432268e5831"
	case "ARBITRUM_TESTNET":
		return "0x75faf114eafb1BDbe2F0316DF893fd58CE46AA4d"
	case "OPTIMISM_MAINNET":
		return "0x0b2C639c533813f4Aa9D7837CAf62653d097Ff85"
	case "AVALANCHE_MAINNET":
		return "0xB97EF9Ef8734C71904D8002F8b6Bc66Dd9c48a6E"
	default:
		return ""
	}
}

const staleThreshold = 10 * time.Minute

// reconcileStale checks for sweeps stuck in 'in_progress' and polls ChainRails for their status.
func (w *Worker) reconcileStale(ctx context.Context) {
	stale, err := w.sweepRepo.GetStale(ctx, staleThreshold)
	if err != nil {
		w.logger.Error("Failed to get stale sweeps", zap.Error(err))
		return
	}
	for _, sweep := range stale {
		if sweep.IntentAddress == nil || *sweep.IntentAddress == "" {
			_ = w.sweepRepo.MarkFailed(ctx, sweep.ID, "stale: no intent address")
			continue
		}
		status, err := w.crClient.GetIntentStatus(ctx, *sweep.IntentAddress)
		if err != nil {
			w.logger.Warn("Failed to poll stale sweep status",
				zap.String("sweep_id", sweep.ID.String()), zap.Error(err))
			continue
		}
		switch strings.ToLower(status.Status) {
		case "completed", "settled":
			_ = w.sweepRepo.MarkCompleted(ctx, sweep.ID, status.TxHash)
			sweepsTotal.WithLabelValues("completed").Inc()
			sweepDuration.Observe(time.Since(sweep.CreatedAt).Seconds())
			sweepAttempts.Observe(float64(sweep.Attempts))
			w.logger.Info("Reconciled stale sweep as completed",
				zap.String("sweep_id", sweep.ID.String()), zap.String("tx_hash", status.TxHash))
		case "refunded", "failed", "expired":
			_ = w.sweepRepo.MarkFailed(ctx, sweep.ID, "reconciled: "+status.Status)
			sweepsTotal.WithLabelValues("failed").Inc()
			w.logger.Warn("Reconciled stale sweep as failed",
				zap.String("sweep_id", sweep.ID.String()), zap.String("status", status.Status))
		default:
			// Still in progress — leave it alone but log
			w.logger.Debug("Stale sweep still in progress",
				zap.String("sweep_id", sweep.ID.String()), zap.String("status", status.Status))
		}
	}
}

// parseSweepFee extracts the bridging fee from a ChainRails intent response.
func parseSweepFee(intent *chainrails.CreateIntentResponse) *decimal.Decimal {
	if intent.FeesInUSD == "" {
		return nil
	}
	fee, err := decimal.NewFromString(intent.FeesInUSD)
	if err != nil {
		return nil
	}
	return &fee
}
