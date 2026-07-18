// Package airbills_recovery periodically reconciles stuck Airbills bill-pay
// orders: reversing abandoned holds whose USDC never left the wallet, and
// finalizing or reversing 'sent' orders based on Circle's terminal state. All
// logic lives in billpay.Service.RunRecovery; this worker only schedules it.
package airbills_recovery

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// Reconciler is satisfied by *billpay.Service.
type Reconciler interface {
	RunRecovery(ctx context.Context)
}

type Worker struct {
	svc           Reconciler
	logger        *zap.Logger
	checkInterval time.Duration
	stopCh        chan struct{}
}

func NewWorker(svc Reconciler, logger *zap.Logger) *Worker {
	return &Worker{
		svc:           svc,
		logger:        logger,
		checkInterval: 2 * time.Minute,
		stopCh:        make(chan struct{}),
	}
}

func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Starting Airbills recovery worker", zap.Duration("check_interval", w.checkInterval))
	ticker := time.NewTicker(w.checkInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		case <-ticker.C:
			w.svc.RunRecovery(ctx)
		}
	}
}

func (w *Worker) Stop() { close(w.stopCh) }
