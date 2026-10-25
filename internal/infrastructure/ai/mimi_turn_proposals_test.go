package ai

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/go-redis/redis/v8"
	"go.uber.org/zap"
)

func decodeSample(t *testing.T, raw string) *TurnResponse {
	t.Helper()
	var r TurnResponse
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return &r
}

func TestRecordTurnProposalsHoldsMoneyAndDedupesReplays(t *testing.T) {
	store := NewMemoryProposalStore()
	req := sampleTurnRequest()
	resp := decodeSample(t, sampleTurnResponse)
	now := time.Date(2026, 10, 25, 8, 0, 0, 0, time.UTC)

	first := RecordTurnProposals(context.Background(), store, zap.NewNop(), req, resp, now)
	if first.Stored != 3 || first.HeldMoney != 1 || first.Duplicates != 0 {
		t.Fatalf("first delivery: %+v", first)
	}

	// The brain replays the same message: same ids, nothing new is stored.
	resp.Replayed = true
	second := RecordTurnProposals(context.Background(), store, zap.NewNop(), req, resp, now)
	if second.Stored != 0 || second.Duplicates != 3 {
		t.Fatalf("replay must store nothing new: %+v", second)
	}

	got := map[string]TurnProposal{}
	for _, p := range store.All() {
		got[p.ProposalID] = p
	}
	pay := got["int_f079100483cf299be4f36633"]
	if pay.Status != ProposalStatusHeldMoney || !pay.TouchMoney {
		t.Fatalf("a payment claim must be held, never committed: %+v", pay)
	}
	if stock := got["int_stock_1"]; stock.Status != ProposalStatusPendingValidation || stock.TouchMoney {
		t.Fatalf("stock count status: %+v", stock)
	}
	if d := got["drf_664d4df6815c213c8bb745f8"]; d.Status != ProposalStatusNeedsOwnerApproval || d.Type != "draft" {
		t.Fatalf("draft status: %+v", d)
	}
	for _, p := range got {
		if p.TenantID != "dist_ade" || p.ShopID != "shop_mama_bisi" || p.MessageID != "wamid.HBgN001" || len(p.Payload) == 0 {
			t.Fatalf("proposal missing provenance: %+v", p)
		}
	}
}

func TestRecordTurnProposalsDedupesWithinOneResponse(t *testing.T) {
	store := NewMemoryProposalStore()
	resp := decodeSample(t, sampleTurnResponse)
	resp.Intents = append(resp.Intents, resp.Intents[0])
	out := RecordTurnProposals(context.Background(), store, nil, sampleTurnRequest(), resp, time.Now())
	if out.Stored != 3 || out.Duplicates != 1 {
		t.Fatalf("want the repeated intent_id dropped: %+v", out)
	}
}

func TestRecordTurnProposalsRejectsWhatIsNotAProposal(t *testing.T) {
	store := NewMemoryProposalStore()
	resp := decodeSample(t, sampleTurnResponse)
	resp.Drafts = nil
	resp.Intents[0].Status = "committed"
	resp.Intents[1].Kind = "transfer_money"
	out := RecordTurnProposals(context.Background(), store, nil, sampleTurnRequest(), resp, time.Now())
	if out.Invalid != 2 || out.HeldMoney != 0 {
		t.Fatalf("want both rejected: %+v", out)
	}
	for _, p := range store.All() {
		if p.Status != ProposalStatusRejectedInvalid || p.Reason == "" {
			t.Fatalf("want rejected_invalid with a reason, got %+v", p)
		}
	}
}

func TestRecordTurnProposalsSurvivesAStoreFailure(t *testing.T) {
	out := RecordTurnProposals(context.Background(), failingStore{}, nil, sampleTurnRequest(), decodeSample(t, sampleTurnResponse), time.Now())
	if out.Failed != 3 || out.Stored != 0 {
		t.Fatalf("store failures are counted, not returned: %+v", out)
	}
	out = RecordTurnProposals(context.Background(), nil, nil, sampleTurnRequest(), decodeSample(t, sampleTurnResponse), time.Now())
	if out.Failed != 3 {
		t.Fatalf("no store: %+v", out)
	}
}

type failingStore struct{}

func (failingStore) Save(context.Context, TurnProposal) (bool, error) {
	return false, errors.New("redis down")
}

// redisAdapter wraps a go-redis client in the cache.RedisClient surface the
// store uses (SetNX only).
type redisAdapter struct {
	*fakeRedis
	c *goredis.Client
}

func (r *redisAdapter) SetNX(ctx context.Context, key string, value interface{}, exp time.Duration) (bool, error) {
	return r.c.SetNX(ctx, key, value, exp).Result()
}

func TestRedisProposalStoreDedupesOnTheID(t *testing.T) {
	mr := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store := NewRedisProposalStore(&redisAdapter{fakeRedis: newFakeRedis(), c: client}, time.Hour)

	p := TurnProposal{ProposalID: "int_1", Kind: TurnIntentRecordStockCount, Status: ProposalStatusPendingValidation}
	created, err := store.Save(context.Background(), p)
	if err != nil || !created {
		t.Fatalf("first save: %v %v", created, err)
	}
	created, err = store.Save(context.Background(), p)
	if err != nil || created {
		t.Fatalf("second save must be a duplicate: %v %v", created, err)
	}
	if ttl := mr.TTL(proposalKeyPrefix + "int_1"); ttl <= 0 || ttl > time.Hour {
		t.Fatalf("proposal must expire, ttl = %v", ttl)
	}
}
