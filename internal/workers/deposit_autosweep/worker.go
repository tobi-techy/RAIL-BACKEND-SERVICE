package deposit_autosweep

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/chainrouting"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/chainrails"
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

type SweepRepository interface {
	GetPending(ctx context.Context, maxAttempts int) ([]*entities.DepositSweep, error)
	MarkInProgress(ctx context.Context, id uuid.UUID, intentAddress string, intentID int, feeAmount *decimal.Decimal) error
	MarkCompleted(ctx context.Context, id uuid.UUID, txHash string) error
	MarkFailed(ctx context.Context, id uuid.UUID, errMsg string) error
	MarkTerminalFailed(ctx context.Context, id uuid.UUID, errMsg string) error
	GetStale(ctx context.Context, olderThan time.Duration) ([]*entities.DepositSweep, error)
}

// Worker polls for pending deposit sweeps and creates ChainRails intents to bridge to Solana.
type Worker struct {
	sweepRepo  SweepRepository
	walletRepo WalletLookup
	crClient   *chainrails.Client
	alerter    Alerter
	logger     *zap.Logger
	stopCh     chan struct{}
	stopOnce   sync.Once
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	running    int32
}

func NewWorker(
	sweepRepo SweepRepository,
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
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				w.logger.Error("Deposit auto-sweep worker panicked",
					zap.Any("panic", r), zap.Stack("stack"))
			}
		}()
		w.poll(ctx)
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-w.stopCh:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				w.poll(ctx)
			}
		}
	}()
}

func (w *Worker) Stop() {
	w.stopOnce.Do(func() {
		w.logger.Info("Deposit auto-sweep worker stopping")
		if w.cancel != nil {
			w.cancel()
		}
		close(w.stopCh)
		w.wg.Wait()
	})
}

func (w *Worker) poll(parent context.Context) {
	if !atomic.CompareAndSwapInt32(&w.running, 0, 1) {
		return
	}
	defer atomic.StoreInt32(&w.running, 0)

	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
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
				if ctx.Err() != nil {
					return
				}
				w.logger.Error("Failed to process sweep",
					zap.String("sweep_id", sweep.ID.String()),
					zap.String("deposit_id", sweep.DepositID.String()),
					zap.Error(err))
				exhausted := sweep.Attempts+1 >= maxAttempts
				terminal := isTerminalSweepError(err)
				var markErr error
				if terminal || exhausted {
					markErr = w.sweepRepo.MarkTerminalFailed(ctx, sweep.ID, err.Error())
				} else {
					markErr = w.sweepRepo.MarkFailed(ctx, sweep.ID, err.Error())
				}
				if markErr != nil {
					w.logger.Error("Failed to mark sweep as failed",
						zap.String("sweep_id", sweep.ID.String()), zap.Error(markErr))
				}
				sweepsTotal.WithLabelValues("failed").Inc()

				// Alert exactly once when this attempt exhausts retries
				if exhausted && w.alerter != nil {
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

func (w *Worker) processSweep(ctx context.Context, sweep *entities.DepositSweep) error {
	// Resolve source wallet (where the deposit landed)
	sourceChain := chainrouting.WalletChainFromCircleChain(sweep.SourceChain)
	if sourceChain == "" {
		return newTerminalSweepError("unsupported source chain: %s", sweep.SourceChain)
	}
	sourceWallet, err := w.walletRepo.GetByUserAndChain(ctx, sweep.UserID, sourceChain)
	if err != nil {
		return fmt.Errorf("resolve source wallet for chain %s: %w", sweep.SourceChain, err)
	}
	if sourceWallet == nil {
		return newTerminalSweepError("no wallet found for user %s on chain %s", sweep.UserID, sweep.SourceChain)
	}

	// Resolve destination (user's Solana wallet)
	solWallet, err := w.walletRepo.GetByUserAndChain(ctx, sweep.UserID, entities.WalletChainSolana)
	if err != nil {
		return fmt.Errorf("resolve solana wallet: %w", err)
	}
	if solWallet == nil {
		return newTerminalSweepError("no solana wallet found for user %s", sweep.UserID)
	}

	crSourceChain := chainrouting.CircleChainToChainRailsChain(sweep.SourceChain)
	if crSourceChain == "" {
		return newTerminalSweepError("unsupported source chain for sweep: %s", sweep.SourceChain)
	}

	tokenIn := chainrouting.USDCTokenForChainRailsChain(crSourceChain)
	if tokenIn == "" {
		return newTerminalSweepError("no USDC token address for chain: %s", crSourceChain)
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

type terminalSweepError struct {
	err error
}

func newTerminalSweepError(format string, args ...interface{}) error {
	return &terminalSweepError{err: fmt.Errorf(format, args...)}
}

func (e *terminalSweepError) Error() string {
	return e.err.Error()
}

func (e *terminalSweepError) Unwrap() error {
	return e.err
}

func isTerminalSweepError(err error) bool {
	var terminal *terminalSweepError
	if errors.As(err, &terminal) {
		return true
	}
	var apiErr *chainrails.APIError
	return errors.As(err, &apiErr) && !apiErr.Retryable()
}
