package miriamevents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/miriam"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"go.uber.org/zap"
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
	logger   *zap.Logger
}

// NewRedisStream creates a stream consumer/publisher backed by Redis. consumer
// may be empty, in which case a per-process name is generated.
func NewRedisStream(redis cache.RedisClient, consumer string) *RedisStream {
	return NewRedisStreamWithLogger(redis, consumer, zap.NewNop())
}

// NewRedisStreamWithLogger is like NewRedisStream but accepts a logger for
// surfacing XAck failures and pending-entry recovery diagnostics.
func NewRedisStreamWithLogger(redis cache.RedisClient, consumer string, logger *zap.Logger) *RedisStream {
	if redis == nil || redis.Client() == nil {
		return nil
	}
	if consumer == "" {
		host, _ := os.Hostname()
		consumer = fmt.Sprintf("%s-%d", host, os.Getpid())
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &RedisStream{
		client:   redis.Client(),
		stream:   defaultStreamKey,
		group:    defaultGroup,
		consumer: consumer,
		logger:   logger,
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
// unacknowledged for redelivery. Pending entries from crashed consumers are
// recovered via XAUTOCLAIM before reading new messages.
func (s *RedisStream) Consume(ctx context.Context, handler func(miriam.MoneyEvent) error) error {
	if s == nil {
		return fmt.Errorf("miriamevents: nil stream")
	}
	if err := s.ensureGroup(ctx); err != nil {
		return fmt.Errorf("miriamevents: ensure group: %w", err)
	}

	pendingMinIdle := 30 * time.Second

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// First, try to reclaim pending entries from other consumers that have
		// been idle for a while (e.g., crashed mid-processing). This ensures
		// failed money events are redelivered as the MoneyEventConsumer contract
		// requires.
		msgs, err := s.autoClaim(ctx, pendingMinIdle, "0-0", 10)
		if err != nil && err != redis.Nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			s.logger.Warn("miriamevents: XAUTOCLAIM failed, will retry", zap.Error(err))
		}
		if len(msgs) > 0 {
			s.processMessages(ctx, msgs, handler)
			// Loop immediately to claim more pending before reading new messages.
			continue
		}

		// No pending entries — read new messages from the stream.
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
			s.processMessages(ctx, stream.Messages, handler)
		}
	}
}

// autoClaim reclaims pending entries idle for at least minIdle.
//
// It issues XAUTOCLAIM as a raw command instead of using the typed helper
// because go-redis v8 hard-requires a 2-element reply, while Redis 7.0+ returns
// three (cursor, entries, deleted-ids). Against Redis 7 the typed call fails
// every cycle with "got 3, wanted 2", so pending money events from a crashed
// consumer were never redelivered.
func (s *RedisStream) autoClaim(ctx context.Context, minIdle time.Duration, start string, count int) ([]redis.XMessage, error) {
	reply, err := s.client.Do(ctx, "XAUTOCLAIM",
		s.stream, s.group, s.consumer,
		minIdle.Milliseconds(), start,
		"COUNT", count,
	).Result()
	if err != nil {
		return nil, err
	}
	return parseAutoClaimReply(reply)
}

// parseAutoClaimReply reads the XAUTOCLAIM reply, accepting both the Redis 6.2
// shape (cursor, entries) and the Redis 7.0+ shape (cursor, entries,
// deleted-ids). Entries that no longer exist come back as nil and are skipped.
func parseAutoClaimReply(reply interface{}) ([]redis.XMessage, error) {
	outer, ok := reply.([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected XAUTOCLAIM reply type %T", reply)
	}
	if len(outer) < 2 {
		return nil, fmt.Errorf("unexpected XAUTOCLAIM reply length %d", len(outer))
	}
	entries, ok := outer[1].([]interface{})
	if !ok {
		if outer[1] == nil {
			return nil, nil
		}
		return nil, fmt.Errorf("unexpected XAUTOCLAIM entries type %T", outer[1])
	}

	msgs := make([]redis.XMessage, 0, len(entries))
	for _, raw := range entries {
		entry, ok := raw.([]interface{})
		if !ok || len(entry) != 2 {
			// A nil entry means the message was trimmed or deleted while
			// pending; there is nothing to hand the handler.
			continue
		}
		id, ok := entry[0].(string)
		if !ok {
			continue
		}
		msgs = append(msgs, redis.XMessage{ID: id, Values: parseFieldPairs(entry[1])})
	}
	return msgs, nil
}

// parseFieldPairs converts a flat [field, value, field, value] reply into the
// map shape go-redis uses for stream entries.
func parseFieldPairs(raw interface{}) map[string]interface{} {
	pairs, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	values := make(map[string]interface{}, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		field, ok := pairs[i].(string)
		if !ok {
			continue
		}
		values[field] = pairs[i+1]
	}
	return values
}

// processMessages parses, handles, and acknowledges a batch of stream messages.
func (s *RedisStream) processMessages(ctx context.Context, msgs []redis.XMessage, handler func(miriam.MoneyEvent) error) {
	for _, msg := range msgs {
		evt, parseErr := s.parseMessage(msg)
		if parseErr != nil {
			// Bad message: ack it so it doesn't poison the stream forever,
			// but log the failure instead of silently discarding.
			if ackErr := s.client.XAck(ctx, s.stream, s.group, msg.ID).Err(); ackErr != nil {
				s.logger.Warn("miriamevents: XAck failed for unparseable message",
					zap.String("msg_id", msg.ID), zap.Error(ackErr))
			}
			s.logger.Warn("miriamevents: unparseable message, acknowledging to skip",
				zap.String("msg_id", msg.ID), zap.Error(parseErr))
			continue
		}
		if handleErr := handler(evt); handleErr != nil {
			// Leave unacknowledged for redelivery on the next XAUTOCLAIM cycle.
			s.logger.Debug("miriamevents: handler returned error, leaving for redelivery",
				zap.String("msg_id", msg.ID), zap.Error(handleErr))
			continue
		}
		if ackErr := s.client.XAck(ctx, s.stream, s.group, msg.ID).Err(); ackErr != nil {
			// XAck failure means the message stays in the pending list and
			// will be redelivered via XAUTOCLAIM — not silently dropped.
			s.logger.Warn("miriamevents: XAck failed, message will be redelivered",
				zap.String("msg_id", msg.ID), zap.Error(ackErr))
		}
	}
}

func (s *RedisStream) ensureGroup(ctx context.Context) error {
	// Start at "0" so existing backlog entries are consumed by the group,
	// not just messages published after group creation.
	err := s.client.XGroupCreateMkStream(ctx, s.stream, s.group, "0").Err()
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
