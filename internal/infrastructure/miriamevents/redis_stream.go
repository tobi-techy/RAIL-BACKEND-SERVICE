package miriamevents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/miriam"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
)

const (
	defaultStreamKey = "miriam:events"
	defaultGroup     = "miriam-event-workers"
)

// RedisStream implements miriam.MoneyEventPublisher and miriam.MoneyEventConsumer
// on top of a Redis stream. It provides at-least-once delivery: events are only
// acknowledged after the handler returns nil.
type RedisStream struct {
	client   *redis.Client
	stream   string
	group    string
	consumer string
}

// NewRedisStream creates a stream consumer/publisher backed by Redis. consumer
// may be empty, in which case a per-process name is generated.
func NewRedisStream(redis cache.RedisClient, consumer string) *RedisStream {
	if redis == nil || redis.Client() == nil {
		return nil
	}
	if consumer == "" {
		host, _ := os.Hostname()
		consumer = fmt.Sprintf("%s-%d", host, os.Getpid())
	}
	return &RedisStream{
		client:   redis.Client(),
		stream:   defaultStreamKey,
		group:    defaultGroup,
		consumer: consumer,
	}
}

// PublishMoneyEvent publishes a money event to the Redis stream.
func (s *RedisStream) PublishMoneyEvent(ctx context.Context, evt miriam.MoneyEvent) error {
	if s == nil {
		return fmt.Errorf("miriamevents: nil stream")
	}
	if evt.ID == "" {
		evt.ID = uuid.New().String()
	}
	if evt.OccurredAt.IsZero() {
		evt.OccurredAt = time.Now().UTC()
	}
	payload, err := json.Marshal(evt.Payload)
	if err != nil {
		return fmt.Errorf("miriamevents: marshal payload: %w", err)
	}
	_, err = s.client.XAdd(ctx, &redis.XAddArgs{
		Stream: s.stream,
		Values: map[string]interface{}{
			"id":          evt.ID,
			"user_id":     evt.UserID.String(),
			"event_type":  evt.EventType,
			"payload":     string(payload),
			"occurred_at": evt.OccurredAt.Format(time.RFC3339Nano),
		},
	}).Result()
	if err != nil {
		return fmt.Errorf("miriamevents: xadd: %w", err)
	}
	return nil
}

// Consume reads events from the Redis stream and calls handler for each.
// It blocks until ctx is cancelled. Errors from handler leave the event
// unacknowledged for redelivery.
func (s *RedisStream) Consume(ctx context.Context, handler func(miriam.MoneyEvent) error) error {
	if s == nil {
		return fmt.Errorf("miriamevents: nil stream")
	}
	if err := s.ensureGroup(ctx); err != nil {
		return fmt.Errorf("miriamevents: ensure group: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		streams, err := s.client.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    s.group,
			Consumer: s.consumer,
			Streams:  []string{s.stream, ">"},
			Count:    10,
			Block:    5 * time.Second,
		}).Result()
		if err == redis.Nil {
			continue
		}
		if err == context.Canceled || err == context.DeadlineExceeded {
			return err
		}
		if err != nil {
			// Redis failures are transient; back off briefly and retry.
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
				continue
			}
		}

		for _, stream := range streams {
			for _, msg := range stream.Messages {
				evt, parseErr := s.parseMessage(msg)
				if parseErr != nil {
					// Bad message: ack it so it doesn't poison the stream forever.
					_ = s.client.XAck(ctx, s.stream, s.group, msg.ID).Err()
					continue
				}
				if handleErr := handler(evt); handleErr != nil {
					// Leave unacknowledged for redelivery.
					continue
				}
				_ = s.client.XAck(ctx, s.stream, s.group, msg.ID).Err()
			}
		}
	}
}

func (s *RedisStream) ensureGroup(ctx context.Context) error {
	err := s.client.XGroupCreateMkStream(ctx, s.stream, s.group, "$").Err()
	if err != nil && err != redis.Nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		return err
	}
	return nil
}

func (s *RedisStream) parseMessage(msg redis.XMessage) (miriam.MoneyEvent, error) {
	var evt miriam.MoneyEvent
	values := msg.Values

	id, ok := values["id"].(string)
	if !ok || id == "" {
		id = msg.ID
	}
	evt.ID = id

	uidStr, _ := values["user_id"].(string)
	uid, err := uuid.Parse(uidStr)
	if err != nil {
		return evt, fmt.Errorf("parse user_id: %w", err)
	}
	evt.UserID = uid

	evt.EventType, _ = values["event_type"].(string)

	payloadStr, _ := values["payload"].(string)
	if payloadStr != "" {
		_ = json.Unmarshal([]byte(payloadStr), &evt.Payload)
	}

	occurredStr, _ := values["occurred_at"].(string)
	if occurredStr != "" {
		if t, err := time.Parse(time.RFC3339Nano, occurredStr); err == nil {
			evt.OccurredAt = t
		}
	}
	if evt.OccurredAt.IsZero() {
		evt.OccurredAt = time.Now().UTC()
	}

	return evt, nil
}

// DeliveredCount returns the number of pending (not yet acked) events in the
// stream for observability.
func (s *RedisStream) PendingCount(ctx context.Context) (int64, error) {
	if s == nil {
		return 0, nil
	}
	info, err := s.client.XPending(ctx, s.stream, s.group).Result()
	if err != nil {
		return 0, err
	}
	return info.Count, nil
}

// _ ensures RedisStream satisfies the interfaces at compile time.
var _ miriam.MoneyEventPublisher = (*RedisStream)(nil)
var _ miriam.MoneyEventConsumer = (*RedisStream)(nil)
