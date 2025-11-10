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
		queue:   queue,
		logger:  logger,
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

func (w *Worker) processJobs(ctx context.Context, workerID int) {
	defer w.wg.Done()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		case <-ticker.C:
			job, err := w.queue.Dequeue(ctx, w.priorities)
			if err != nil {
				w.logger.Error("Failed to dequeue job", zap.Int("worker", workerID), zap.Error(err))
				continue
			}

			if job == nil {
				continue
			}

			w.handleJob(ctx, job, workerID)
		}
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

	jobCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
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

	ticker := time.NewTicker(10 * time.Second)
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
