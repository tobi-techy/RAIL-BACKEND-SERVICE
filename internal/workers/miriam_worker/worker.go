package miriam_worker

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	miriamsvc "github.com/rail-service/rail_service/internal/domain/services/miriam"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

type UserLister interface {
	ListMiriamWorkerUserIDs(ctx context.Context, limit int) ([]uuid.UUID, error)
}

// runner is the subset of the Miriam brain the worker needs to evaluate a user.
// Both *miriamsvc.Service (classic) and *miriamsvc.IntelligenceOrchestrator
// (unified brain) satisfy this via the small adapters below.
type runner interface {
	Evaluate(ctx context.Context, userID uuid.UUID, eventType string) (*miriamsvc.IntelligenceResult, error)
	RunSelfReview(ctx context.Context, userID uuid.UUID) error
}

type serviceRunner struct{ s *miriamsvc.Service }

func (r *serviceRunner) Evaluate(ctx context.Context, userID uuid.UUID, eventType string) (*miriamsvc.IntelligenceResult, error) {
	return nil, r.s.EvaluateUser(ctx, userID, eventType)
}

func (r *serviceRunner) RunSelfReview(_ context.Context, _ uuid.UUID) error { return nil }

type brainRunner struct {
	b *miriamsvc.IntelligenceOrchestrator
}

func (r *brainRunner) Evaluate(ctx context.Context, userID uuid.UUID, eventType string) (*miriamsvc.IntelligenceResult, error) {
	return r.b.Evaluate(ctx, userID, eventType)
}

func (r *brainRunner) RunSelfReview(ctx context.Context, userID uuid.UUID) error {
	return r.b.RunSelfReview(ctx, userID)
}

type Worker struct {
	users       UserLister
	runner      runner
	interval    time.Duration
	limit       int
	concurrency int
	lastCleanup time.Time
	logger      *zap.Logger

	// adaptive fields
	adaptive     bool
	redis        cache.RedisClient
	fastInterval time.Duration
	slowInterval time.Duration
	hotMu        sync.Mutex
	hotUsers     map[uuid.UUID]time.Time
	hotTTL       time.Duration
}

// NewWorker creates a Miriam worker with the classic service for backward compatibility.
func NewWorker(users UserLister, service *miriamsvc.Service, logger *zap.Logger) *Worker {
	if logger != nil {
		logger.Warn("miriam worker running without unified intelligence; self-review is unavailable")
	}
	return &Worker{
		users: users, runner: &serviceRunner{s: service}, interval: 15 * time.Minute,
		limit: 500, concurrency: 10, logger: logger,
		hotUsers: make(map[uuid.UUID]time.Time),
		hotTTL:   5 * time.Minute,
	}
}

// NewWorkerWithIntelligence creates a Miriam worker with the unified intelligence orchestrator.
func NewWorkerWithIntelligence(users UserLister, service *miriamsvc.Service, brain *miriamsvc.IntelligenceOrchestrator, logger *zap.Logger) *Worker {
	var r runner = &serviceRunner{s: service}
	if brain != nil {
		r = &brainRunner{b: brain}
	} else if logger != nil {
		logger.Warn("miriam worker running without unified intelligence; self-review is unavailable")
	}
	return &Worker{
		users: users, runner: r, interval: 15 * time.Minute,
		limit: 500, concurrency: 10, logger: logger,
		hotUsers: make(map[uuid.UUID]time.Time),
		hotTTL:   5 * time.Minute,
	}
}

// SetAdaptive enables the event-woken + backoff loop. fastInterval controls how
// often hot users are re-evaluated; slowInterval controls the full sweep.
// redis is optional; when provided, hot-user state is mirrored for visibility
// across failovers. When nil, hot users are tracked in-memory (fine on the
// leader replica). Setting adaptive without intervals defaults to 1m fast / 15m slow.
func (w *Worker) SetAdaptive(redis cache.RedisClient, fastInterval, slowInterval time.Duration) {
	w.adaptive = true
	w.redis = redis
	if fastInterval <= 0 {
		fastInterval = time.Minute
	}
	if slowInterval <= 0 {
		slowInterval = 15 * time.Minute
	}
	w.fastInterval = fastInterval
	w.slowInterval = slowInterval
}

// Notify flags a user as "hot" so the adaptive loop re-evaluates them on the
// next fast tick. It satisfies miriam_event_worker.Wakeup.
func (w *Worker) Notify(ctx context.Context, userID uuid.UUID) error {
	if !w.adaptive {
		return nil
	}
	w.hotMu.Lock()
	w.hotUsers[userID] = time.Now().Add(w.hotTTL)
	w.hotMu.Unlock()

	if w.redis != nil {
		key := "miriam:hot_user:" + userID.String()
		if err := w.redis.Set(ctx, key, time.Now().UTC().Format(time.RFC3339Nano), w.hotTTL); err != nil {
			// Redis is best-effort here: the in-memory hot-user entry stays
			// active regardless, so the fast ticker still re-evaluates this
			// user on the leader replica. Log for visibility.
			w.logger.Warn("miriam worker: failed to mirror hot user to Redis",
				zap.String("user_id", userID.String()), zap.Error(err))
		}
	}
	return nil
}

func (w *Worker) Start(ctx context.Context) {
	if w.adaptive {
		w.startAdaptive(ctx)
		return
	}

	w.logger.Info("Miriam intelligence worker started", zap.Duration("interval", w.interval), zap.Int("concurrency", w.concurrency))
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	w.run(ctx, miriamsvc.EventWorkerSweep)
	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Miriam intelligence worker stopped")
			return
		case <-ticker.C:
			w.run(ctx, miriamsvc.EventWorkerSweep)
		}
	}
}

func (w *Worker) startAdaptive(ctx context.Context) {
	w.logger.Info("Miriam intelligence worker started (adaptive)", zap.Duration("fast", w.fastInterval), zap.Duration("slow", w.slowInterval), zap.Int("concurrency", w.concurrency))
	fastTicker := time.NewTicker(w.fastInterval)
	slowTicker := time.NewTicker(w.slowInterval)
	defer fastTicker.Stop()
	defer slowTicker.Stop()

	// Initial full sweep so the brain has a baseline.
	w.run(ctx, miriamsvc.EventAutonomousTick)
	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Miriam intelligence worker stopped")
			return
		case <-fastTicker.C:
			users := w.popHotUsers()
			if len(users) > 0 {
				w.runUsers(ctx, users, miriamsvc.EventAutonomousTick)
			}
		case <-slowTicker.C:
			w.run(ctx, miriamsvc.EventAutonomousTick)
		}
	}
}

func (w *Worker) run(ctx context.Context, eventType string) {
	users, err := w.users.ListMiriamWorkerUserIDs(ctx, w.limit)
	if err != nil {
		w.logger.Error("miriam worker: list users failed", zap.Error(err))
		return
	}
	w.runUsers(ctx, users, eventType)

	// Run data retention cleanup once per day (unified brain only).
	if br, ok := w.runner.(*brainRunner); ok && time.Since(w.lastCleanup) > 24*time.Hour {
		if hs := br.b.HealthScoreTracker(); hs != nil {
			if deleted, err := hs.CleanupOldScores(ctx, 30); err == nil && deleted > 0 {
				w.logger.Info("miriam worker: cleaned old health scores", zap.Int64("deleted", deleted))
			}
		}
		if ot := br.b.OutcomeTracker(); ot != nil {
			if deleted, err := ot.CleanupOldPredictions(ctx, 30); err == nil && deleted > 0 {
				w.logger.Info("miriam worker: cleaned old predictions", zap.Int64("deleted", deleted))
			}
			if deleted, err := ot.CleanupEvaluatedOutcomes(ctx, 30); err == nil && deleted > 0 {
				w.logger.Info("miriam worker: cleaned old evaluated outcomes", zap.Int64("deleted", deleted))
			}
		}
		w.lastCleanup = time.Now()
	}
}

func (w *Worker) runUsers(ctx context.Context, users []uuid.UUID, eventType string) {
	var evaluated, failed int64
	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(w.concurrency)

	for _, uid := range users {
		userID := uid
		g.Go(func() error {
			evalCtx, cancel := context.WithTimeout(gCtx, 30*time.Second)
			defer cancel()

			_, evalErr := w.runner.Evaluate(evalCtx, userID, eventType)
			// Self-review self-gates to once per day; safe to call every tick.
			if srErr := w.runner.RunSelfReview(evalCtx, userID); srErr != nil {
				w.logger.Debug("miriam worker: self-review failed", zap.String("user_id", userID.String()), zap.Error(srErr))
			}
			if evalErr != nil {
				atomic.AddInt64(&failed, 1)
				w.logger.Warn("miriam worker: user evaluation failed", zap.String("user_id", userID.String()), zap.Error(evalErr))
			} else {
				atomic.AddInt64(&evaluated, 1)
			}
			return nil // don't abort other goroutines on individual failure
		})
	}
	_ = g.Wait()
	w.logger.Info("miriam worker: run complete", zap.Int64("evaluated", evaluated), zap.Int64("failed", failed), zap.String("event_type", eventType))
}

func (w *Worker) popHotUsers() []uuid.UUID {
	w.hotMu.Lock()
	defer w.hotMu.Unlock()

	now := time.Now()
	var users []uuid.UUID
	for uid, expiresAt := range w.hotUsers {
		if now.Before(expiresAt) {
			users = append(users, uid)
		}
		delete(w.hotUsers, uid)
	}
	return users
}
