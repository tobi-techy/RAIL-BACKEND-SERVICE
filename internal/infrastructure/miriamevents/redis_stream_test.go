package miriamevents

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/miriam"
)

func TestParseAutoClaimReply_Redis7ThreeElementShape(t *testing.T) {
	// Redis 7.0+ replies with (cursor, entries, deleted-ids). go-redis v8's
	// typed XAutoClaim rejects this with "got 3, wanted 2".
	reply := []interface{}{
		"0-0",
		[]interface{}{
			[]interface{}{"1-1", []interface{}{"event_type", "deposit", "user_id", "u-1"}},
		},
		[]interface{}{"9-9"},
	}

	msgs, err := parseAutoClaimReply(reply)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].ID != "1-1" {
		t.Errorf("id: got %q, want 1-1", msgs[0].ID)
	}
	if msgs[0].Values["event_type"] != "deposit" || msgs[0].Values["user_id"] != "u-1" {
		t.Errorf("values: got %v", msgs[0].Values)
	}
}

func TestParseAutoClaimReply_Redis62TwoElementShape(t *testing.T) {
	reply := []interface{}{
		"0-0",
		[]interface{}{
			[]interface{}{"2-1", []interface{}{"event_type", "withdrawal"}},
		},
	}

	msgs, err := parseAutoClaimReply(reply)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(msgs) != 1 || msgs[0].ID != "2-1" {
		t.Fatalf("unexpected messages: %v", msgs)
	}
}

func TestParseAutoClaimReply_SkipsDeletedAndMalformedEntries(t *testing.T) {
	reply := []interface{}{
		"0-0",
		[]interface{}{
			nil,                          // trimmed while pending
			[]interface{}{"3-1"},         // truncated entry
			[]interface{}{int64(4), nil}, // non-string id
			[]interface{}{"5-1", []interface{}{"event_type", "deposit", "dangling"}},
		},
		[]interface{}{},
	}

	msgs, err := parseAutoClaimReply(reply)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(msgs) != 1 || msgs[0].ID != "5-1" {
		t.Fatalf("expected only the well-formed entry, got %v", msgs)
	}
	if _, ok := msgs[0].Values["dangling"]; ok {
		t.Error("odd trailing field should be dropped, not stored with a nil value")
	}
}

func TestParseAutoClaimReply_RejectsUnexpectedShapes(t *testing.T) {
	if _, err := parseAutoClaimReply("PONG"); err == nil {
		t.Error("expected error for non-array reply")
	}
	if _, err := parseAutoClaimReply([]interface{}{"0-0"}); err == nil {
		t.Error("expected error for short reply")
	}
	msgs, err := parseAutoClaimReply([]interface{}{"0-0", nil})
	if err != nil || len(msgs) != 0 {
		t.Errorf("nil entries should yield no messages and no error, got %v / %v", msgs, err)
	}
}

// TestAutoClaim_RedeliversPendingEntry proves the reclaim path works end to end:
// a message left unacknowledged by one consumer is handed to another once it has
// been idle long enough.
func TestAutoClaim_RedeliversPendingEntry(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() {
		if err := client.Close(); err != nil {
			t.Errorf("close redis client: %v", err)
		}
	}()

	ctx := context.Background()
	stream := &RedisStream{client: client, stream: defaultStreamKey, group: defaultGroup, consumer: "consumer-b"}
	if err := stream.ensureGroup(ctx); err != nil {
		t.Fatalf("ensure group: %v", err)
	}

	userID := uuid.New()
	if err := stream.PublishMoneyEvent(ctx, miriam.MoneyEvent{UserID: userID, EventType: "deposit"}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// consumer-a reads the event and dies before acknowledging it.
	if _, err := client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    defaultGroup,
		Consumer: "consumer-a",
		Streams:  []string{defaultStreamKey, ">"},
		Count:    1,
	}).Result(); err != nil {
		t.Fatalf("xreadgroup: %v", err)
	}
	// Push miniredis' clock past the idle threshold. FastForward only ages TTLs,
	// while XAUTOCLAIM compares min-idle against the server clock.
	mr.SetTime(time.Now().Add(time.Minute))

	msgs, err := stream.autoClaim(ctx, 30*time.Second, "0-0", 10)
	if err != nil {
		t.Fatalf("autoClaim: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected the pending entry to be reclaimed, got %d messages", len(msgs))
	}

	evt, err := stream.parseMessage(msgs[0])
	if err != nil {
		t.Fatalf("parse reclaimed message: %v", err)
	}
	if evt.UserID != userID || evt.EventType != "deposit" {
		t.Errorf("unexpected event: %+v", evt)
	}
}
