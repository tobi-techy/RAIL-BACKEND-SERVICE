package ledger_outbox_publisher

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/miriam"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	// MaxOutboxRetries is the retry ceiling: events reaching it are dead-lettered
	// and never claimed again. The ledger maintenance worker purges them after
	// the retention window.
	MaxOutboxRetries = 10
	outboxBatchSize  = 100

	// outboxWriteTimeout bounds detached dispatch-outcome writes so an
	// exhausted or cancelled parent context never strands an event.
	outboxWriteTimeout = 5 * time.Second
)

type OutboxReader interface {
	BeginTx(ctx context.Context) (context.Context, error)
	CommitTx(ctx context.Context) error
	RollbackTx(ctx context.Context) error
	ClaimUnpublishedOutbox(ctx context.Context, batchSize int, maxRetries int) ([]repositories.OutboxRecord, error)
	IncrementOutboxRetry(ctx context.Context, id uuid.UUID, lastErr string) error
	MarkOutboxPublished(ctx context.Context, ids []uuid.UUID) error
}

type Worker struct {
	store     OutboxReader
	publisher miriam.MoneyEventPublisher
	logger    *zap.Logger
}

func NewWorker(store OutboxReader, logger *zap.Logger) *Worker {
	return &Worker{store: store, logger: logger}
}

// SetMiriamPublisher wires a publisher that sends money events to Miriam's
// always-on event stream. Optional; when nil the worker logs events only.
func (w *Worker) SetMiriamPublisher(p miriam.MoneyEventPublisher) {
	w.publisher = p
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

	// Claiming sets a lease (claimed_at), not published_at: events are only
	// marked published once dispatch succeeds, so an interrupted or failed
	// dispatch is reclaimed on a later tick instead of being silently lost.
	var published []uuid.UUID
	var failed int
	for _, evt := range events {
		if err := w.dispatch(evt); err != nil {
			failed++
			w.logger.Error("Failed to dispatch outbox event",
				zap.String("event_id", evt.ID.String()),
				zap.String("event_type", evt.EventType),
				zap.Error(err))
			w.recordDispatchFailure(ctx, evt, err)
			continue
		}
		published = append(published, evt.ID)
	}

	if len(published) > 0 {
		// Dispatch-outcome writes must survive shutdown or an exhausted caller
		// context; if this write fails the lease expires and the batch is
		// re-dispatched (at-least-once) on a later tick.
		writeCtx, cancel := detachedWriteCtx(ctx)
		defer cancel()
		if err := w.store.MarkOutboxPublished(writeCtx, published); err != nil {
			w.logger.Error("Failed to mark outbox events published",
				zap.Int("count", len(published)),
				zap.Error(err))
		}
	}

	w.logger.Info("Outbox batch published",
		zap.Int("claimed", len(events)),
		zap.Int("dispatched", len(published)),
		zap.Int("failed", failed))
}

// recordDispatchFailure un-claims a failed event so it is retried on a later
// tick, and warns once the event hits the retry ceiling (dead-lettering).
func (w *Worker) recordDispatchFailure(ctx context.Context, evt repositories.OutboxRecord, dispatchErr error) {
	writeCtx, cancel := detachedWriteCtx(ctx)
	defer cancel()
	if err := w.store.IncrementOutboxRetry(writeCtx, evt.ID, dispatchErr.Error()); err != nil {
		w.logger.Error("Failed to increment outbox retry",
			zap.String("event_id", evt.ID.String()),
			zap.Error(err))
		// The claim lease expires and the event is reclaimed on a later tick,
		// so the failed attempt is never silently dropped.
		return
	}

	if evt.RetryCount+1 >= MaxOutboxRetries {
		w.logger.Warn("Outbox event reached max retries, dead-lettering",
			zap.String("event_id", evt.ID.String()),
			zap.String("event_type", evt.EventType),
			zap.Int("retry_count", evt.RetryCount+1))
	}
}

// claim atomically reserves a batch of outbox events in its own transaction so
// concurrent publishers never dispatch the same event.
func (w *Worker) claim(ctx context.Context) ([]repositories.OutboxRecord, error) {
	txCtx, err := w.store.BeginTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin outbox claim tx: %w", err)
	}

	events, err := w.store.ClaimUnpublishedOutbox(txCtx, outboxBatchSize, MaxOutboxRetries)
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

// detachedWriteCtx returns a context detached from the caller's cancellation so
// dispatch-outcome writes survive an exhausted or cancelled parent context
// (per-user budgets, worker shutdown).
func detachedWriteCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), outboxWriteTimeout)
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

	if w.publisher != nil {
		var payload map[string]interface{}
		if len(evt.Payload) > 0 {
			if err := json.Unmarshal(evt.Payload, &payload); err != nil {
				return fmt.Errorf("decode outbox payload for miriam event: %w", err)
			}
		}
		publishErr := w.publisher.PublishMoneyEvent(context.Background(), miriam.MoneyEvent{
			ID:         evt.ID.String(),
			UserID:     evt.AggregateID,
			EventType:  evt.EventType,
			Payload:    payload,
			OccurredAt: evt.CreatedAt,
		})
		if publishErr != nil {
			return fmt.Errorf("publish miriam event: %w", publishErr)
		}
	}

	return nil
}
