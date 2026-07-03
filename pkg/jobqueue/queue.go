package jobqueue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"go.uber.org/zap"
)

type Priority int

const (
	PriorityLow Priority = iota
	PriorityNormal
	PriorityHigh
	PriorityCritical
)

type Job struct {
	ID          string                 `json:"id"`
	Type        string                 `json:"type"`
	Priority    Priority               `json:"priority"`
	Payload     map[string]interface{} `json:"payload"`
	Retries     int                    `json:"retries"`
	MaxRetries  int                    `json:"max_retries"`
	CreatedAt   time.Time              `json:"created_at"`
	ScheduledAt *time.Time             `json:"scheduled_at,omitempty"`
}

type JobQueue struct {
	client *redis.Client
	logger *zap.Logger
	queues map[Priority]string
	dlq    string
}

func NewJobQueue(client *redis.Client, logger *zap.Logger) *JobQueue {
	return &JobQueue{
		client: client,
		logger: logger,
		queues: map[Priority]string{
			PriorityLow:      "queue:low",
			PriorityNormal:   "queue:normal",
			PriorityHigh:     "queue:high",
			PriorityCritical: "queue:critical",
		},
		dlq: "queue:dead_letter",
	}
}

func (jq *JobQueue) Enqueue(ctx context.Context, job *Job) error {
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now()
	}
	if job.MaxRetries == 0 {
		job.MaxRetries = 3
	}

	data, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("failed to marshal job: %w", err)
	}

	queueName := jq.queues[job.Priority]

	if job.ScheduledAt != nil && job.ScheduledAt.After(time.Now()) {
		score := float64(job.ScheduledAt.Unix())
		return jq.client.ZAdd(ctx, "queue:scheduled", &redis.Z{
			Score:  score,
			Member: data,
		}).Err()
	}

	return jq.client.LPush(ctx, queueName, data).Err()
}

func (jq *JobQueue) Dequeue(ctx context.Context, priorities []Priority) (*Job, error) {
	for _, priority := range priorities {
		queueName := jq.queues[priority]
		result, err := jq.client.RPop(ctx, queueName).Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			return nil, err
		}

		var job Job
		if err := json.Unmarshal([]byte(result), &job); err != nil {
			jq.logger.Error("Failed to unmarshal job", zap.Error(err))
			continue
		}

		return &job, nil
	}

	return nil, nil
}

// DequeueBlocking pops the next job with a single BRPOP over all priority
// queues (highest first — BRPOP checks keys in argument order), blocking up to
// timeout when the queues are empty. On a pay-per-command Redis (Upstash) this
// is the difference between 4 billed RPOPs per worker per tick (~864K commands
// per day at 5 workers / 2s ticks — ~$52/month of idle polling) and one billed
// command per timeout window (~43K/day). Returns (nil, nil) on timeout.
func (jq *JobQueue) DequeueBlocking(ctx context.Context, priorities []Priority, timeout time.Duration) (*Job, error) {
	keys := make([]string, 0, len(priorities))
	for _, priority := range priorities {
		keys = append(keys, jq.queues[priority])
	}
	result, err := jq.client.BRPop(ctx, timeout, keys...).Result()
	if err == redis.Nil {
		return nil, nil // timed out with no job — not an error
	}
	if err != nil {
		return nil, err
	}
	// BRPop returns [key, value].
	if len(result) < 2 {
		return nil, nil
	}
	var job Job
	if err := json.Unmarshal([]byte(result[1]), &job); err != nil {
		jq.logger.Error("Failed to unmarshal job", zap.Error(err))
		return nil, nil
	}
	return &job, nil
}

func (jq *JobQueue) MoveToDeadLetter(ctx context.Context, job *Job, reason string) error {
	job.Payload["failure_reason"] = reason
	data, err := json.Marshal(job)
	if err != nil {
		return err
	}

	return jq.client.LPush(ctx, jq.dlq, data).Err()
}

func (jq *JobQueue) Retry(ctx context.Context, job *Job) error {
	job.Retries++
	if job.Retries >= job.MaxRetries {
		return jq.MoveToDeadLetter(ctx, job, "max retries exceeded")
	}

	backoff := time.Duration(job.Retries*job.Retries) * time.Second
	scheduledAt := time.Now().Add(backoff)
	job.ScheduledAt = &scheduledAt

	return jq.Enqueue(ctx, job)
}

func (jq *JobQueue) ProcessScheduled(ctx context.Context) error {
	now := float64(time.Now().Unix())
	results, err := jq.client.ZRangeByScore(ctx, "queue:scheduled", &redis.ZRangeBy{
		Min: "0",
		Max: fmt.Sprintf("%f", now),
	}).Result()

	if err != nil {
		return err
	}

	for _, result := range results {
		var job Job
		if err := json.Unmarshal([]byte(result), &job); err != nil {
			continue
		}

		job.ScheduledAt = nil
		if err := jq.Enqueue(ctx, &job); err != nil {
			jq.logger.Error("Failed to enqueue scheduled job", zap.Error(err))
			continue
		}

		jq.client.ZRem(ctx, "queue:scheduled", result)
	}

	return nil
}

func (jq *JobQueue) GetQueueSize(ctx context.Context, priority Priority) (int64, error) {
	queueName := jq.queues[priority]
	return jq.client.LLen(ctx, queueName).Result()
}

func (jq *JobQueue) GetDeadLetterSize(ctx context.Context) (int64, error) {
	return jq.client.LLen(ctx, jq.dlq).Result()
}
