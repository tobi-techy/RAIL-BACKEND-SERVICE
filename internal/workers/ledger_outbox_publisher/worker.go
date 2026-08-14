package ledger_outbox_publisher

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	maxOutboxRetries = 10
	outboxBatchSize  = 100
)

type OutboxReader interface {
	BeginTx(ctx context.Context) (context.Context, error)
	CommitTx(ctx context.Context) error
	RollbackTx(ctx context.Context) error
	ClaimUnpublishedOutbox(ctx context.Context, batchSize int, maxRetries int) ([]repositories.OutboxRecord, error)
	IncrementOutboxRetry(ctx context.Context, id uuid.UUID, lastErr string) error
}

type Worker struct {
	store  OutboxReader
	logger *zap.Logger
}

func NewWorker(store OutboxReader, logger *zap.Logger) *Worker {
	return &Worker{store: store, logger: logger}
}

func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Ledger outbox publisher started")
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	w.publishOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Ledger outbox publisher stopped")
			return
		case <-ticker.C:
			w.publishOnce(ctx)
		}
	}
}

func (w *Worker) publishOnce(ctx context.Context) {
	events, err := w.claim(ctx)
	if err != nil {
		// A serialization failure means a sibling worker claimed concurrently;
		// the next tick picks the batch up.
		if repositories.IsSerializationFailure(err) {
			w.logger.Debug("Outbox claim conflicted, retrying next tick", zap.Error(err))
			return
		}
		w.logger.Error("Failed to claim unpublished outbox events", zap.Error(err))
		return
	}

	if len(events) == 0 {
		return
	}

	// Claiming already set published_at, so success needs no further write.
	// Failures reset published_at via IncrementOutboxRetry.
	var failed int
	for _, evt := range events {
		err := w.dispatch(evt)
		if err == nil {
			continue
		}

		failed++
		w.logger.Error("Failed to dispatch outbox event",
			zap.String("event_id", evt.ID.String()),
			zap.String("event_type", evt.EventType),
			zap.Error(err))

		if retryErr := w.store.IncrementOutboxRetry(ctx, evt.ID, err.Error()); retryErr != nil {
			w.logger.Error("Failed to increment outbox retry",
				zap.String("event_id", evt.ID.String()),
				zap.Error(retryErr))
		}

		if evt.RetryCount+1 >= maxOutboxRetries {
			w.logger.Warn("Outbox event exceeded max retries, dead-lettering",
				zap.String("event_id", evt.ID.String()),
				zap.Int("retry_count", evt.RetryCount+1))
		}
	}

	w.logger.Info("Outbox batch published",
		zap.Int("claimed", len(events)),
		zap.Int("dispatched", len(events)-failed),
		zap.Int("failed", failed))
}

// claim atomically reserves a batch of outbox events in its own transaction so
// concurrent publishers never dispatch the same event.
func (w *Worker) claim(ctx context.Context) ([]repositories.OutboxRecord, error) {
	txCtx, err := w.store.BeginTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin outbox claim tx: %w", err)
	}

	events, err := w.store.ClaimUnpublishedOutbox(txCtx, outboxBatchSize, maxOutboxRetries)
	if err != nil {
		if rbErr := w.store.RollbackTx(txCtx); rbErr != nil {
			w.logger.Error("Failed to roll back outbox claim tx", zap.Error(rbErr))
		}
		return nil, err
	}

	if err := w.store.CommitTx(txCtx); err != nil {
		return nil, fmt.Errorf("commit outbox claim tx: %w", err)
	}
	return events, nil
}

func (w *Worker) dispatch(evt repositories.OutboxRecord) error {
	// TODO: Route to message broker (RabbitMQ, Kafka, etc.) based on event_type.
	// For now, just log the event. Integration with the message broker is a
	// future enhancement.
	switch evt.EventType {
	case "transaction.created", "transaction.completed", "transaction.reversed", "transaction.failed",
		"balance.updated":
	default:
		return fmt.Errorf("unknown event type: %s", evt.EventType)
	}

	w.logger.Info("Dispatched outbox event",
		zap.String("event_id", evt.ID.String()),
		zap.String("event_type", evt.EventType),
		zap.String("aggregate_type", evt.AggregateType),
		zap.String("aggregate_id", evt.AggregateID.String()),
	)

	if w.logger.Core().Enabled(zapcore.DebugLevel) {
		payload, err := json.Marshal(evt)
		if err == nil {
			w.logger.Debug("Outbox event payload",
				zap.String("event_id", evt.ID.String()),
				zap.ByteString("payload", payload))
		}
	}

	return nil
}
