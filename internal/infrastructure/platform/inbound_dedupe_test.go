package platform

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
)

func typedMessage(msgID, text string) []byte {
	raw, err := json.Marshal(InboundMessage{
		Platform: entities.PlatformWhatsApp,
		UserID:   "+2348000001",
		ThreadID: "thread-1",
		SpaceID:  "thread-1",
		MsgID:    msgID,
		Text:     text,
	})
	if err != nil {
		panic(err)
	}
	return raw
}

// replica builds one Processor the way a replica (or a restarted process)
// would: fresh memory, the same shared Redis.
func replica(t *testing.T, redis cache.RedisClient, orch *fakeOrchestrator, repo *fakeRepo) (*Processor, *[]*OutboundMessage) {
	t.Helper()
	p, sent, _ := newTestProcessor(repo, orch)
	p.SetInboundDeduper(NewInboundDeduper(redis, zap.NewNop()))
	return p, sent
}

func linkedWhatsApp(repo *fakeRepo) {
	pi := linkedIdentity(repo, "+2348000001")
	pi.Platform = entities.PlatformWhatsApp
	repo.byPlatUser[key(pi.Platform, pi.PlatformUserID)] = pi.ID
}

func TestInboundDedupe_SameMessageAcrossARestartIsOneTurnAndOneReply(t *testing.T) {
	_, redis := newRedisStore(t)
	repo := newFakeRepo()
	linkedWhatsApp(repo)
	orch := &fakeOrchestrator{}

	before, sentBefore := replica(t, redis, orch, repo)
	if err := before.Process(context.Background(), typedMessage("wamid.R1", "how much did I spend")); err != nil {
		t.Fatal(err)
	}
	repliesAfterFirst := len(*sentBefore)
	if len(orch.sources) != 1 || repliesAfterFirst == 0 {
		t.Fatalf("first delivery: turns=%d sent=%d", len(orch.sources), repliesAfterFirst)
	}

	after, sentAfter := replica(t, redis, orch, repo) // a new process: no memory of the first
	if err := after.Process(context.Background(), typedMessage("wamid.R1", "how much did I spend")); err != nil {
		t.Fatal(err)
	}

	if len(orch.sources) != 1 {
		t.Fatalf("replay after restart ran a second turn: %d", len(orch.sources))
	}
	if len(*sentAfter) != 0 {
		t.Fatalf("replay after restart sent %d outbound messages, want 0", len(*sentAfter))
	}
}

func TestInboundDedupe_SameMessageOnTwoReplicasAtOnceIsOneTurnAndOneReply(t *testing.T) {
	_, redis := newRedisStore(t)
	repo := newFakeRepo()
	linkedWhatsApp(repo)

	entered := make(chan struct{})
	release := make(chan struct{})
	orch := &fakeOrchestrator{onMessage: func() {
		close(entered)
		<-release
	}}
	replicaA, sentA := replica(t, redis, orch, repo)
	replicaB, sentB := replica(t, redis, orch, repo)

	done := make(chan error, 1)
	go func() {
		done <- replicaA.Process(context.Background(), typedMessage("wamid.R2", "hello"))
	}()
	<-entered // replica A is mid-turn, inside the orchestrator

	err := replicaB.Process(context.Background(), typedMessage("wamid.R2", "hello"))
	if err == nil || !IsRetryable(err) {
		t.Fatalf("a delivery during an in-flight turn must be retryable, not acked: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// The bridge's retry now finds the finished turn and is acknowledged.
	if err := replicaB.Process(context.Background(), typedMessage("wamid.R2", "hello")); err != nil {
		t.Fatalf("retry after the first turn finished: %v", err)
	}

	if len(orch.sources) != 1 {
		t.Fatalf("two replicas ran %d turns for one message", len(orch.sources))
	}
	if len(*sentB) != 0 {
		t.Fatalf("the duplicate replica sent %d messages", len(*sentB))
	}
	if len(*sentA) == 0 {
		t.Fatal("the owning replica never replied")
	}
}

func TestInboundDedupe_ACancelledFirstDeliveryLetsTheBridgesRetryThrough(t *testing.T) {
	_, redis := newRedisStore(t)
	repo := newFakeRepo()
	linkedWhatsApp(repo)
	orch := &fakeOrchestrator{}
	first, _ := replica(t, redis, orch, repo)
	retry, _ := replica(t, redis, orch, repo)

	// The bridge timed out, aborted the first request, and retried. The first
	// request fails on its cancelled context and gives its claim back.
	repo.lookupErr = context.Canceled
	if err := first.Process(context.Background(), typedMessage("wamid.R8", "hi")); err == nil {
		t.Fatal("the cancelled delivery should fail")
	}
	repo.lookupErr = nil
	if err := retry.Process(context.Background(), typedMessage("wamid.R8", "hi")); err != nil {
		t.Fatal(err)
	}
	if len(orch.sources) != 1 {
		t.Fatalf("the retry after a cancelled delivery must run exactly once, got %d", len(orch.sources))
	}
}

func TestInboundDedupe_FailedTurnReleasesTheClaimSoTheRetryRuns(t *testing.T) {
	_, redis := newRedisStore(t)
	repo := newFakeRepo()
	linkedWhatsApp(repo)
	orch := &fakeOrchestrator{}
	p, _ := replica(t, redis, orch, repo)

	repo.lookupErr = errors.New("connection reset")
	if err := p.Process(context.Background(), typedMessage("wamid.R3", "hi")); err == nil {
		t.Fatal("expected a retryable failure")
	}
	repo.lookupErr = nil
	if err := p.Process(context.Background(), typedMessage("wamid.R3", "hi")); err != nil {
		t.Fatal(err)
	}
	if len(orch.sources) != 1 {
		t.Fatalf("the retry after a failed turn did not run: turns=%d", len(orch.sources))
	}
}

func TestInboundDedupe_ClaimTTLsAndKey(t *testing.T) {
	mr, redis := newRedisStore(t)
	repo := newFakeRepo()
	linkedWhatsApp(repo)
	orch := &fakeOrchestrator{}
	p, _ := replica(t, redis, orch, repo)
	k := "miriam:inbound:whatsapp:wamid.R4"

	if err := p.Process(context.Background(), typedMessage("wamid.R4", "hi")); err != nil {
		t.Fatal(err)
	}
	if !mr.Exists(k) {
		t.Fatalf("no record under %s; keys=%v", k, mr.Keys())
	}
	if ttl := mr.TTL(k); ttl <= time.Hour || ttl > inboundDoneTTL {
		t.Fatalf("handled-message ttl = %v, want ~%v", ttl, inboundDoneTTL)
	}
}

func TestInboundDedupe_NoMessageIDMeansNoDedupe(t *testing.T) {
	_, redis := newRedisStore(t)
	repo := newFakeRepo()
	linkedWhatsApp(repo)
	orch := &fakeOrchestrator{}
	p, _ := replica(t, redis, orch, repo)

	for _, text := range []string{"one", "two"} {
		if err := p.Process(context.Background(), typedMessage("", text)); err != nil {
			t.Fatal(err)
		}
	}
	if len(orch.sources) != 2 {
		t.Fatalf("messages without an id must both run, got %d", len(orch.sources))
	}
}

func TestInboundDedupe_TheSameIDOnAnotherPlatformIsAnotherMessage(t *testing.T) {
	_, redis := newRedisStore(t)
	repo := newFakeRepo()
	linkedWhatsApp(repo)
	linkedIdentity(repo, "+15551234")
	orch := &fakeOrchestrator{}
	p, _ := replica(t, redis, orch, repo)

	if err := p.Process(context.Background(), typedMessage("shared-id", "hi")); err != nil {
		t.Fatal(err)
	}
	other, err := json.Marshal(InboundMessage{Platform: entities.PlatformIMessage, UserID: "+15551234", ThreadID: "t", MsgID: "shared-id", Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Process(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	if len(orch.sources) != 2 {
		t.Fatalf("platform must scope the key, got %d turns", len(orch.sources))
	}
}

func TestInboundDedupe_RedisDownFailsOpen(t *testing.T) {
	mr, redis := newRedisStore(t)
	repo := newFakeRepo()
	linkedWhatsApp(repo)
	orch := &fakeOrchestrator{}
	p, _ := replica(t, redis, orch, repo)
	mr.Close()

	if err := p.Process(context.Background(), typedMessage("wamid.R7", "hi")); err != nil {
		t.Fatal(err)
	}
	if len(orch.sources) != 1 {
		t.Fatalf("a Redis outage must not drop the message: turns=%d", len(orch.sources))
	}
}
