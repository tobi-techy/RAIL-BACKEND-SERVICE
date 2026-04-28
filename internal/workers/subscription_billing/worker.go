package subscription_billing

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// SubscriptionRenewer renews due subscriptions
type SubscriptionRenewer interface {
	RenewDueSubscriptions(ctx context.Context) (charged, failed int)
}

// Worker runs daily subscription billing
type Worker struct {
	renewer SubscriptionRenewer
	logger  *zap.Logger
	stop    chan struct{}
}

func NewWorker(renewer SubscriptionRenewer, logger *zap.Logger) *Worker {
	return &Worker{renewer: renewer, logger: logger, stop: make(chan struct{})}
}

func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Subscription billing worker started")
	// Run immediately on startup
	w.run(ctx)

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Subscription billing worker stopped (context)")
			return
		case <-w.stop:
			w.logger.Info("Subscription billing worker stopped")
			return
		case <-ticker.C:
			// Only run at midnight UTC
			if time.Now().UTC().Hour() == 0 {
				w.run(ctx)
			}
		}
	}
}

func (w *Worker) run(ctx context.Context) {
	charged, failed := w.renewer.RenewDueSubscriptions(ctx)
	if charged > 0 || failed > 0 {
		w.logger.Info("Subscription billing cycle complete",
			zap.Int("charged", charged), zap.Int("failed", failed))
	}
}

func (w *Worker) Stop() {
	close(w.stop)
}
