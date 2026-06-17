package statement

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	progressKeyPrefix = "stmt:progress:"
	progressTTL       = 30 * time.Minute
	progressChannel   = "stmt:progress:updates"
)

// RedisProgressReporter publishes stage progress to Redis for SSE/polling consumers.
type RedisProgressReporter struct {
	rdb    *redis.Client
	logger *zap.Logger
}

func NewRedisProgressReporter(rdb *redis.Client, logger *zap.Logger) *RedisProgressReporter {
	if rdb == nil {
		return nil
	}
	return &RedisProgressReporter{rdb: rdb, logger: logger}
}

func (r *RedisProgressReporter) Report(ctx context.Context, progress *StageProgress) {
	if r == nil {
		return
	}
	data, err := json.Marshal(progress)
	if err != nil {
		return
	}

	key := progressKeyPrefix + progress.UploadID.String()

	// Store latest state for polling (non-critical — don't fail the pipeline on Redis errors)
	if err := r.rdb.Set(ctx, key, data, progressTTL).Err(); err != nil {
		r.logger.Debug("progress set failed", zap.Error(err))
	}

	// Publish for real-time SSE subscribers
	r.rdb.Publish(ctx, progressChannel+":"+progress.UploadID.String(), data)
}

// GetProgress retrieves current progress for an upload (polling fallback).
func (r *RedisProgressReporter) GetProgress(ctx context.Context, uploadID uuid.UUID) (*StageProgress, error) {
	key := progressKeyPrefix + uploadID.String()
	data, err := r.rdb.Get(ctx, key).Bytes()
	if err != nil {
		return nil, fmt.Errorf("no progress found: %w", err)
	}
	var progress StageProgress
	if err := json.Unmarshal(data, &progress); err != nil {
		return nil, err
	}
	return &progress, nil
}

// Subscribe returns a channel that receives progress updates for an upload.
// Caller must cancel the context to unsubscribe.
func (r *RedisProgressReporter) Subscribe(ctx context.Context, uploadID uuid.UUID) <-chan *StageProgress {
	ch := make(chan *StageProgress, 10)
	sub := r.rdb.Subscribe(ctx, progressChannel+":"+uploadID.String())

	go func() {
		defer close(ch)
		defer sub.Close()
		for {
			msg, err := sub.ReceiveMessage(ctx)
			if err != nil {
				return
			}
			var progress StageProgress
			if err := json.Unmarshal([]byte(msg.Payload), &progress); err != nil {
				continue
			}
			select {
			case ch <- &progress:
			case <-ctx.Done():
				return
			}
		}
	}()

	return ch
}

// NoOpReporter discards all progress reports (for testing or when Redis is unavailable).
type NoOpReporter struct{}

func (n *NoOpReporter) Report(_ context.Context, _ *StageProgress) {}
