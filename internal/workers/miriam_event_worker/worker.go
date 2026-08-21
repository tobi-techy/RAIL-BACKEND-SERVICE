package miriam_event_worker

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/miriam"
	"go.uber.org/zap"
)

// Brain is the subset of the Miriam intelligence orchestrator the event worker
// needs. It evaluates a single user in response to a trigger.
type Brain interface {
	Evaluate(ctx context.Context, userID uuid.UUID, eventType string) (*miriam.IntelligenceResult, error)
}

// Wakeup signals a consumer of the adaptive loop that a user just had a money
// event and should be re-evaluated soon.
type Wakeup interface {
	Notify(ctx context.Context, userID uuid.UUID) error
}

// Worker consumes MoneyEvents from a stream and evaluates the affected user.
// It is the real-time trigger for Miriam's always-on brain. All evaluations
// from this worker use miriam.EventMoneyEvent, which is read-only on money.
type Worker struct {
	consumer miriam.MoneyEventConsumer
	brain    Brain
	wakeup   Wakeup
	logger   *zap.Logger
}

// NewWorker creates an event-driven Miriam worker.
func NewWorker(consumer miriam.MoneyEventConsumer, brain Brain, logger *zap.Logger) *Worker {
	return &Worker{
		consumer: consumer,
		brain:    brain,
		logger:   logger,
	}
}

// SetWakeup installs an optional callback that pings the adaptive think-loop
// when a user has a fresh money event.
func (w *Worker) SetWakeup(u Wakeup) {
	w.wakeup = u
}

// Start blocks until ctx is cancelled, consuming events and evaluating users.
func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Miriam event worker started")
	if w.consumer == nil {
		w.logger.Warn("Miriam event worker has no consumer; stopping")
		return
	}

	err := w.consumer.Consume(ctx, func(evt miriam.MoneyEvent) error {
		return w.handleEvent(ctx, evt)
	})

	if err != nil && err != context.Canceled {
		w.logger.Error("Miriam event worker stopped with error", zap.Error(err))
	} else {
		w.logger.Info("Miriam event worker stopped")
	}
}

func (w *Worker) handleEvent(ctx context.Context, evt miriam.MoneyEvent) error {
	evalCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	start := time.Now()
	_, err := w.brain.Evaluate(evalCtx, evt.UserID, miriam.EventMoneyEvent)
	duration := time.Since(start)

	if err != nil {
		w.logger.Warn("Miriam event evaluation failed",
			zap.String("user_id", evt.UserID.String()),
			zap.String("event_type", evt.EventType),
			zap.Duration("duration", duration),
			zap.Error(err))
		return fmt.Errorf("evaluate %s: %w", evt.EventType, err)
	}

	w.logger.Info("Miriam event evaluation succeeded",
		zap.String("user_id", evt.UserID.String()),
		zap.String("event_type", evt.EventType),
		zap.Duration("duration", duration))

	if w.wakeup != nil {
		if wakeErr := w.wakeup.Notify(ctx, evt.UserID); wakeErr != nil {
			w.logger.Debug("adaptive wakeup notify failed",
				zap.String("user_id", evt.UserID.String()),
				zap.Error(wakeErr))
		}
	}

	return nil
}
