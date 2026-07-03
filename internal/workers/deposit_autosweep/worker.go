package deposit_autosweep

import (
	"context"
	"errors"
	"fmt"
	"math/big"
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
	circlepkg "github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
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
	MarkInProgress(ctx context.Context, id uuid.UUID, intentAddress string, intentID int, feeAmount, fundingAmount *decimal.Decimal) error
	MarkCompleted(ctx context.Context, id uuid.UUID, txHash string) error
	MarkFailed(ctx context.Context, id uuid.UUID, errMsg string) error
	MarkTerminalFailed(ctx context.Context, id uuid.UUID, errMsg string) error
	GetStale(ctx context.Context, olderThan time.Duration) ([]*entities.DepositSweep, error)
}

// CircleTransferer funds ChainRails intents from the user's source-chain Circle
// wallet. An intent settles only after its address receives the USDC — without
// this transfer the sweep would sit unfunded until the intent expires.
type CircleTransferer interface {
	GetUSDCTokenIDOnchain(ctx context.Context, walletID string) (string, error)
	TransferUSDCWithIdempotency(ctx context.Context, walletID, tokenID, destinationAddress, amount, idempotencyKey string) (*circlepkg.Transaction, error)
}

// Worker polls for pending deposit sweeps and creates ChainRails intents to bridge to Solana.
type Worker struct {
	sweepRepo  SweepRepository
	walletRepo WalletLookup
	crClient   *chainrails.Client
	circle     CircleTransferer
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
	circle CircleTransferer,
	alerter Alerter,
	logger *zap.Logger,
) *Worker {
	return &Worker{
		sweepRepo:  sweepRepo,
		walletRepo: walletRepo,
		crClient:   crClient,
		circle:     circle,
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

	// Resume branch: a prior attempt already created and persisted an intent but
	// funding failed. Re-fund the SAME intent with the persisted amount and the
	// per-intent idempotency key — Circle returns the original transfer if the
	// earlier attempt actually went through, so this can never double-spend.
	if sweep.IntentAddress != nil && *sweep.IntentAddress != "" && sweep.FundingAmount != nil {
		if err := w.fundIntent(ctx, sweep, sourceWallet.CircleWalletID, *sweep.IntentAddress, *sweep.FundingAmount); err != nil {
			return err
		}
		w.logger.Info("Deposit sweep intent re-funded",
			zap.String("sweep_id", sweep.ID.String()),
			zap.String("intent_address", *sweep.IntentAddress))
		return nil
	}

	// ChainRails expects the amount as a micro-unit integer string (e.g.
	// "1000000" for 1 USDC) — a decimal string like "1.000000" is rejected with
	// "amount must be a positive integer string".
	intent, err := w.crClient.CreateIntent(ctx, &chainrails.CreateIntentRequest{
		Sender:           sourceWallet.Address,
		Amount:           sweep.Amount.Truncate(6).Shift(6).BigInt().String(),
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
	fundingAmount, err := sweepFundingAmount(intent, sweep.Amount)
	if err != nil {
		return err
	}

	// Persist the intent (address + exact funding amount) BEFORE moving money,
	// so a crash between funding and persistence can never create an intent we
	// fund twice under different keys.
	if err := w.sweepRepo.MarkInProgress(ctx, sweep.ID, intent.IntentAddress, intent.ID, parseSweepFee(intent), &fundingAmount); err != nil {
		return fmt.Errorf("mark in_progress: %w", err)
	}

	if err := w.fundIntent(ctx, sweep, sourceWallet.CircleWalletID, intent.IntentAddress, fundingAmount); err != nil {
		return err
	}

	w.logger.Info("Deposit sweep intent created and funded",
		zap.String("sweep_id", sweep.ID.String()),
		zap.String("intent_address", intent.IntentAddress),
		zap.Int("intent_id", intent.ID),
		zap.String("funding_amount", fundingAmount.StringFixed(6)))
	return nil
}

// fundIntent transfers the funding USDC from the user's source-chain Circle
// wallet to the ChainRails intent address. The idempotency key is scoped to
// (sweep, intent) so retries dedupe against the original transfer, and a
// replacement intent would get its own key.
func (w *Worker) fundIntent(ctx context.Context, sweep *entities.DepositSweep, circleWalletID, intentAddress string, amount decimal.Decimal) error {
	if w.circle == nil {
		return fmt.Errorf("circle transferer not configured; cannot fund sweep intent")
	}
	if circleWalletID == "" {
		return newTerminalSweepError("source wallet for sweep %s has no circle_wallet_id", sweep.ID)
	}
	tokenID, err := w.circle.GetUSDCTokenIDOnchain(ctx, circleWalletID)
	if err != nil {
		return fmt.Errorf("resolve source USDC token id: %w", err)
	}
	idemKey := uuid.NewSHA1(uuid.NameSpaceOID,
		[]byte("deposit-sweep-fund-"+sweep.ID.String()+"-"+intentAddress)).String()
	tx, err := w.circle.TransferUSDCWithIdempotency(ctx, circleWalletID, tokenID, intentAddress, amount.StringFixed(6), idemKey)
	if err != nil {
		return fmt.Errorf("fund chainrails intent: %w", err)
	}
	if tx.State == "DENIED" || tx.State == "FAILED" || tx.State == "CANCELLED" {
		return fmt.Errorf("funding transfer %s ended in state %s", tx.ID, tx.State)
	}
	return nil
}

// sweepFundingAmount derives the exact USDC to send to the intent address from
// ChainRails' quoted total (amount + bridge fees, in token micro-units).
func sweepFundingAmount(intent *chainrails.CreateIntentResponse, requested decimal.Decimal) (decimal.Decimal, error) {
	if strings.TrimSpace(intent.TotalAmountInAssetToken) == "" || intent.AssetTokenDecimals <= 0 {
		return decimal.Zero, fmt.Errorf("chainrails intent %d did not return total funding amount", intent.ID)
	}
	totalUnits, ok := new(big.Int).SetString(intent.TotalAmountInAssetToken, 10)
	if !ok {
		return decimal.Zero, fmt.Errorf("unparseable chainrails funding amount %q", intent.TotalAmountInAssetToken)
	}
	amount := decimal.NewFromBigInt(totalUnits, -int32(intent.AssetTokenDecimals))
	if !amount.GreaterThan(decimal.Zero) {
		return decimal.Zero, fmt.Errorf("chainrails intent %d returned non-positive funding amount %s", intent.ID, amount.String())
	}
	if amount.LessThan(requested.Truncate(6)) {
		return decimal.Zero, fmt.Errorf("chainrails intent %d funding amount %s is less than requested %s",
			intent.ID, amount.StringFixed(6), requested.Truncate(6).StringFixed(6))
	}
	return amount, nil
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
			if err := w.sweepRepo.MarkTerminalFailed(ctx, sweep.ID, "reconciled: "+status.Status); err != nil {
				w.logger.Error("Failed to mark reconciled sweep as terminal failed",
					zap.String("sweep_id", sweep.ID.String()),
					zap.String("status", status.Status),
					zap.Error(err))
				continue
			}
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
