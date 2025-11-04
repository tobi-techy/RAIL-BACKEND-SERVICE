package async_processor

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/stack-service/stack_service/pkg/logger"
)

// Task represents an async task
type Task struct {
	ID          string                 `json:"id"`
	Type        string                 `json:"type"`
	Payload     map[string]interface{} `json:"payload"`
	Priority    int                    `json:"priority"` // 1=low, 5=high
	Queue       string                 `json:"queue"`
	MaxRetries  int                    `json:"max_retries"`
	CreatedAt   time.Time              `json:"created_at"`
	ScheduledAt *time.Time             `json:"scheduled_at,omitempty"`
}

// TaskResult represents the result of task execution
type TaskResult struct {
	TaskID      string        `json:"task_id"`
	Success     bool          `json:"success"`
	Result      interface{}   `json:"result,omitempty"`
	Error       string        `json:"error,omitempty"`
	Duration    time.Duration `json:"duration"`
	CompletedAt time.Time     `json:"completed_at"`
}

// TaskHandler defines a function that processes tasks
type TaskHandler func(ctx context.Context, task *Task) (*TaskResult, error)

// AsyncProcessor handles async task processing
type AsyncProcessor struct {
	redis    *redis.Client
	handlers map[string]TaskHandler
	logger   *logger.Logger
	workers  []*Worker
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// Worker represents a task processing worker
type Worker struct {
	id        string
	processor *AsyncProcessor
	queue     string
	stopCh    chan struct{}
}

// NewAsyncProcessor creates a new async processor
func NewAsyncProcessor(redis *redis.Client, logger *logger.Logger) *AsyncProcessor {
	return &AsyncProcessor{
		redis:    redis,
		handlers: make(map[string]TaskHandler),
		logger:   logger,
		stopCh:   make(chan struct{}),
	}
}

// RegisterHandler registers a task handler for a specific task type
func (ap *AsyncProcessor) RegisterHandler(taskType string, handler TaskHandler) {
	ap.handlers[taskType] = handler
	ap.logger.Infow("Registered task handler", "task_type", taskType)
}

// StartWorkers starts worker goroutines for processing tasks
func (ap *AsyncProcessor) StartWorkers(queue string, workerCount int) error {
	for i := 0; i < workerCount; i++ {
		worker := &Worker{
			id:        fmt.Sprintf("%s-worker-%d", queue, i+1),
			processor: ap,
			queue:     queue,
			stopCh:    make(chan struct{}),
		}

		ap.workers = append(ap.workers, worker)
		ap.wg.Add(1)

		go worker.start()
	}

	ap.logger.Infow("Started async workers",
		"queue", queue,
		"worker_count", workerCount,
	)

	return nil
}

// EnqueueTask adds a task to the processing queue
func (ap *AsyncProcessor) EnqueueTask(ctx context.Context, task *Task) error {
	if task.ID == "" {
		task.ID = uuid.New().String()
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now()
	}

	// Serialize task
	taskData, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("failed to serialize task: %w", err)
	}

	// Determine queue key
	queueKey := fmt.Sprintf("queue:%s", task.Queue)

	// Add to queue with priority (using sorted set score)
	score := float64(task.Priority)
	if task.ScheduledAt != nil {
		score = float64(task.ScheduledAt.Unix())
	}

	err = ap.redis.ZAdd(ctx, queueKey, &redis.Z{
		Score:  score,
		Member: taskData,
	}).Err()

	if err != nil {
		return fmt.Errorf("failed to enqueue task: %w", err)
	}

	ap.logger.Debugw("Task enqueued",
		"task_id", task.ID,
		"task_type", task.Type,
		"queue", task.Queue,
		"priority", task.Priority,
	)

	return nil
}

// ScheduleTask schedules a task for future execution
func (ap *AsyncProcessor) ScheduleTask(ctx context.Context, task *Task, executeAt time.Time) error {
	task.ScheduledAt = &executeAt
	return ap.EnqueueTask(ctx, task)
}

// GetTaskStatus gets the status of a task (simplified - in production you'd track in DB)
func (ap *AsyncProcessor) GetTaskStatus(ctx context.Context, taskID string) (string, error) {
	// This is a simplified implementation
	// In production, you'd query a tasks table for status
	return "unknown", nil
}

// Shutdown gracefully shuts down all workers
func (ap *AsyncProcessor) Shutdown(timeout time.Duration) error {
	close(ap.stopCh)

	// Stop all workers
	for _, worker := range ap.workers {
		close(worker.stopCh)
	}

	// Wait for workers to finish or timeout
	done := make(chan struct{})
	go func() {
		ap.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		ap.logger.Info("Async processor shutdown complete")
		return nil
	case <-time.After(timeout):
		ap.logger.Warn("Async processor shutdown timed out")
		return fmt.Errorf("shutdown timed out")
	}
}

// start starts the worker processing loop
func (w *Worker) start() {
	defer w.processor.wg.Done()

	w.processor.logger.Infow("Worker started", "worker_id", w.id, "queue", w.queue)

	for {
		select {
		case <-w.stopCh:
			w.processor.logger.Infow("Worker stopping", "worker_id", w.id)
			return
		default:
			w.processNextTask()
		}
	}
}

// processNextTask processes the next task in the queue
func (w *Worker) processNextTask() {
	ctx := context.Background()
	queueKey := fmt.Sprintf("queue:%s", w.queue)

	// Get the highest priority task (lowest score)
	result := w.processor.redis.ZPopMin(ctx, queueKey, 1)
	if result.Err() != nil {
		if result.Err() != redis.Nil {
			w.processor.logger.Errorw("Failed to get task from queue",
				"worker_id", w.id,
				"queue", w.queue,
				"error", result.Err(),
			)
		}
		time.Sleep(1 * time.Second) // Back off if no tasks
		return
	}

	if len(result.Val()) == 0 {
		time.Sleep(1 * time.Second) // No tasks available
		return
	}

	// Parse task
	taskData := result.Val()[0].Member.(string)
	var task Task
	if err := json.Unmarshal([]byte(taskData), &task); err != nil {
		w.processor.logger.Errorw("Failed to parse task",
			"worker_id", w.id,
			"task_data", taskData,
			"error", err,
		)
		return
	}

	// Check if task is scheduled for future
	if task.ScheduledAt != nil && task.ScheduledAt.After(time.Now()) {
		// Re-queue for later
		w.processor.redis.ZAdd(ctx, queueKey, &redis.Z{
			Score:  float64(task.ScheduledAt.Unix()),
			Member: taskData,
		})
		return
	}

	// Process task
	w.processTask(ctx, &task)
}

// processTask processes a single task
func (w *Worker) processTask(ctx context.Context, task *Task) {
	startTime := time.Now()

	w.processor.logger.Debugw("Processing task",
		"worker_id", w.id,
		"task_id", task.ID,
		"task_type", task.Type,
	)

	// Get handler
	handler, exists := w.processor.handlers[task.Type]
	if !exists {
		w.processor.logger.Errorw("No handler registered for task type",
			"worker_id", w.id,
			"task_id", task.ID,
			"task_type", task.Type,
		)
		w.recordTaskResult(task.ID, false, nil, "no handler registered", time.Since(startTime))
		return
	}

	// Execute handler
	result, err := handler(ctx, task)
	duration := time.Since(startTime)

	if err != nil {
		w.processor.logger.Errorw("Task execution failed",
			"worker_id", w.id,
			"task_id", task.ID,
			"task_type", task.Type,
			"error", err,
			"duration", duration,
		)

		// Handle retries
		if task.MaxRetries > 0 {
			task.MaxRetries--
			// Re-queue with backoff
			time.Sleep(time.Duration(task.Priority) * time.Second)
			w.processor.EnqueueTask(ctx, task)
			return
		}

		w.recordTaskResult(task.ID, false, nil, err.Error(), duration)
		return
	}

	w.processor.logger.Debugw("Task completed successfully",
		"worker_id", w.id,
		"task_id", task.ID,
		"task_type", task.Type,
		"duration", duration,
	)

	w.recordTaskResult(task.ID, true, result.Result, "", duration)
}

// recordTaskResult records the task execution result
func (w *Worker) recordTaskResult(taskID string, success bool, result interface{}, errorMsg string, duration time.Duration) {
	taskResult := &TaskResult{
		TaskID:      taskID,
		Success:     success,
		Result:      result,
		Error:       errorMsg,
		Duration:    duration,
		CompletedAt: time.Now(),
	}

	// In production, you'd store this in a database
	w.processor.logger.Debugw("Task result recorded",
		"task_result", taskResult,
	)
}

// EnqueueTask is a convenience method for workers to re-queue tasks
func (w *Worker) EnqueueTask(ctx context.Context, task *Task) error {
	return w.processor.EnqueueTask(ctx, task)
}

// Common task types
const (
	TaskTypeSendEmail        = "send_email"
	TaskTypeProcessWebhook   = "process_webhook"
	TaskTypeGenerateReport   = "generate_report"
	TaskTypeSyncPortfolio    = "sync_portfolio"
	TaskTypeCalculateFees    = "calculate_fees"
	TaskTypeSendNotification = "send_notification"
	TaskTypeBackupData       = "backup_data"
)

// Common queues
const (
	QueueDefault      = "default"
	QueueEmail        = "email"
	QueueWebhook      = "webhook"
	QueueReport       = "report"
	QueueNotification = "notification"
	QueueMaintenance  = "maintenance"
)
