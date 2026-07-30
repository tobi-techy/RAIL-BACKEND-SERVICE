package ledger_outbox_publisher

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"go.uber.org/zap"
)

const maxOutboxRetries = 10

type OutboxReader interface {
	GetUnpublishedOutboxEvents(ctx context.Context, limit int) ([]repositories.OutboxRecord, error)
	MarkOutboxPublished(ctx context.Context, ids []uuid.UUID) error
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
	events, err := w.store.GetUnpublishedOutboxEvents(ctx, 100)
	if err != nil {
		w.logger.Error("Failed to fetch unpublished outbox events", zap.Error(err))
		return
	}

	if len(events) == 0 {
		return
	}

	var succeeded []uuid.UUID
	for _, evt := range events {
		if err := w.dispatch(ctx, evt); err != nil {
			w.logger.Error("Failed to dispatch outbox event",
				zap.String("event_id", evt.ID.String()),
				zap.String("event_type", evt.EventType),
				zap.Error(err))

			if err := w.store.IncrementOutboxRetry(ctx, evt.ID, err.Error()); err != nil {
				w.logger.Error("Failed to increment outbox retry",
					zap.String("event_id", evt.ID.String()),
					zap.Error(err))
			}

			if evt.RetryCount+1 >= maxOutboxRetries {
				w.logger.Warn("Outbox event exceeded max retries, dead-lettering",
					zap.String("event_id", evt.ID.String()),
					zap.Int("retry_count", evt.RetryCount+1))
			}
		} else {
			succeeded = append(succeeded, evt.ID)
		}
	}

	if len(succeeded) > 0 {
		if err := w.store.MarkOutboxPublished(ctx, succeeded); err != nil {
			w.logger.Error("Failed to mark outbox events published", zap.Error(err))
		}
	}
}

func (w *Worker) dispatch(ctx context.Context, evt repositories.OutboxRecord) error {
	payload, _ := json.Marshal(evt)
	w.logger.Info("Dispatching outbox event",
		zap.String("event_id", evt.ID.String()),
		zap.String("event_type", evt.EventType),
		zap.String("aggregate_type", evt.AggregateType),
		zap.String("aggregate_id", evt.AggregateID.String()),
		zap.ByteString("payload", payload),
	)

	// TODO: Route to message broker (RabbitMQ, Kafka, etc.) based on event_type.
	// For now, just log the event. Integration with the message broker is a
	// future enhancement.
	switch evt.EventType {
	case "transaction.created", "transaction.completed", "transaction.reversed", "transaction.failed":
		w.logger.Info("Ledger transaction event", zap.ByteString("payload", payload))
	case "balance.updated":
		w.logger.Info("Ledger balance event", zap.ByteString("payload", payload))
	default:
		return fmt.Errorf("unknown event type: %s", evt.EventType)
	}

	return nil
}
