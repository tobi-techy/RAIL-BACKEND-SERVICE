package ledger_maintenance

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rail-service/rail_service/internal/domain/services/ledger"
	ledger_outbox_publisher "github.com/rail-service/rail_service/internal/workers/ledger_outbox_publisher"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

type MaintenanceService interface {
	RecordDailySnapshots(ctx context.Context, snapshotDate time.Time) (int, error)
	CheckIntegrity(ctx context.Context, deficitThreshold ...decimal.Decimal) *ledger.IntegrityReport
}

type OutboxCleaner interface {
	DeletePublishedOutboxBefore(ctx context.Context, cutoff time.Time) (int64, error)
	DeleteDeadLetteredOutboxBefore(ctx context.Context, cutoff time.Time, minRetries int) (int64, error)
}

type Worker struct {
	svc     MaintenanceService
	cleaner OutboxCleaner
	logger  *zap.Logger
}

func NewWorker(svc MaintenanceService, cleaner OutboxCleaner, logger *zap.Logger) *Worker {
	return &Worker{svc: svc, cleaner: cleaner, logger: logger}
}

func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Ledger maintenance worker started")
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	w.runOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Ledger maintenance worker stopped")
			return
		case <-ticker.C:
			w.runOnce(ctx)
		}
	}
}

func (w *Worker) runOnce(ctx context.Context) {
	w.logger.Info("Running ledger maintenance")

	count, err := w.svc.RecordDailySnapshots(ctx, time.Now())
	if err != nil {
		w.logger.Error("Failed to record daily snapshots", zap.Error(err))
	} else {
		w.logger.Info("Daily snapshots recorded", zap.Int("count", count))
	}

	report := w.svc.CheckIntegrity(ctx)
	if len(report.Errors) > 0 {
		payload, _ := json.Marshal(report)
		w.logger.Error("Ledger integrity check failed",
			zap.Int("error_count", len(report.Errors)),
			zap.ByteString("report", payload),
		)
	} else {
		w.logger.Info("Ledger integrity check passed",
			zap.String("total_debits", report.TotalDebits.String()),
			zap.String("total_credits", report.TotalCredits.String()),
		)
	}

	if w.cleaner != nil {
		const retentionDays = 7
		cutoff := time.Now().Add(-retentionDays * 24 * time.Hour)
		deleted, err := w.cleaner.DeletePublishedOutboxBefore(ctx, cutoff)
		if err != nil {
			w.logger.Error("Failed to clean up published outbox records", zap.Error(err))
		} else if deleted > 0 {
			w.logger.Info("Cleaned up published outbox records",
				zap.Int64("deleted", deleted),
				zap.String("cutoff", cutoff.Format("2006-01-02")))
		}

		// Events at the retry ceiling are never claimed again; purge them after
		// retention so dead letters don't accumulate or skew backlog counts.
		dead, err := w.cleaner.DeleteDeadLetteredOutboxBefore(ctx, cutoff, ledger_outbox_publisher.MaxOutboxRetries)
		if err != nil {
			w.logger.Error("Failed to clean up dead-lettered outbox records", zap.Error(err))
		} else if dead > 0 {
			w.logger.Warn("Purged dead-lettered outbox records",
				zap.Int64("deleted", dead),
				zap.String("cutoff", cutoff.Format("2006-01-02")),
				zap.Int("retry_ceiling", ledger_outbox_publisher.MaxOutboxRetries))
		}
	}
}
