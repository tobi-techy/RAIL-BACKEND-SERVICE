package jobqueue

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
)

type JobHandler func(ctx context.Context, job *Job) error

type Worker struct {
	queue      *JobQueue
	logger     *zap.Logger
	handlers   map[string]JobHandler
	priorities []Priority
	workers    int
	stopCh     chan struct{}
	wg         sync.WaitGroup
}

func NewWorker(queue *JobQueue, logger *zap.Logger, workers int) *Worker {
	return &Worker{
		queue:    queue,
		logger:   logger,
		handlers: make(map[string]JobHandler),
		priorities: []Priority{
			PriorityCritical,
			PriorityHigh,
			PriorityNormal,
			PriorityLow,
		},
		workers: workers,
		stopCh:  make(chan struct{}),
	}
}

func (w *Worker) RegisterHandler(jobType string, handler JobHandler) {
	w.handlers[jobType] = handler
}

func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Starting workers", zap.Int("count", w.workers))

	for i := 0; i < w.workers; i++ {
		w.wg.Add(1)
		go w.processJobs(ctx, i)
	}

	w.wg.Add(1)
	go w.processScheduledJobs(ctx)
}

// dequeueBlockTimeout is how long each worker's BRPOP blocks server-side while
// the queues are empty. One billed command per window per worker. A pushed job
// wakes a blocked worker INSTANTLY regardless of this value, so a long block
// has zero latency cost and only cuts idle re-issues: at 30s across 5 workers
// the idle floor is ~14K commands/day (vs ~864K/day with the old 2s polling).
// 30s stays under common serverless blocking-command caps (Upstash allows up
// to ~60s).
const dequeueBlockTimeout = 30 * time.Second

func (w *Worker) processJobs(ctx context.Context, workerID int) {
	defer w.wg.Done()

	var consecutiveErrors int
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		default:
		}

		// Blocking pop: returns instantly when a job arrives, or (nil, nil)
		// after the block timeout — which doubles as the stop-check cadence.
		job, err := w.queue.DequeueBlocking(ctx, w.priorities, dequeueBlockTimeout)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			consecutiveErrors++
			if consecutiveErrors <= 3 {
				w.logger.Error("Failed to dequeue job", zap.Int("worker", workerID), zap.Error(err))
			}
			// Exponential backoff: 1s, 2s, 4s, ... capped at 30s
			backoff := time.Duration(1<<min(consecutiveErrors, 5)) * time.Second
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			time.Sleep(backoff)
			continue
		}
		consecutiveErrors = 0

		if job == nil {
			continue
		}

		w.handleJob(ctx, job, workerID)
	}
}

func (w *Worker) handleJob(ctx context.Context, job *Job, workerID int) {
	handler, exists := w.handlers[job.Type]
	if !exists {
		w.logger.Error("No handler for job type", zap.String("type", job.Type))
		w.queue.MoveToDeadLetter(ctx, job, "no handler found")
		return
	}

	w.logger.Info("Processing job",
		zap.Int("worker", workerID),
		zap.String("job_id", job.ID),
		zap.String("type", job.Type),
		zap.Int("priority", int(job.Priority)),
	)

	jobCtx, cancel := context.WithTimeout(ctx, 35*time.Minute)
	defer cancel()

	if err := handler(jobCtx, job); err != nil {
		w.logger.Error("Job failed",
			zap.String("job_id", job.ID),
			zap.Error(err),
		)

		if err := w.queue.Retry(ctx, job); err != nil {
			w.logger.Error("Failed to retry job", zap.String("job_id", job.ID), zap.Error(err))
		}
		return
	}

	w.logger.Info("Job completed", zap.String("job_id", job.ID))
}

func (w *Worker) processScheduledJobs(ctx context.Context) {
	defer w.wg.Done()

	// 30s: scheduled jobs tolerate sub-minute promotion latency, and this is a
	// billed ZRANGEBYSCORE per tick on pay-per-command Redis.
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		case <-ticker.C:
			if err := w.queue.ProcessScheduled(ctx); err != nil {
				w.logger.Error("Failed to process scheduled jobs", zap.Error(err))
			}
		}
	}
}

func (w *Worker) Stop() {
	close(w.stopCh)
	w.wg.Wait()
	w.logger.Info("Workers stopped")
}
