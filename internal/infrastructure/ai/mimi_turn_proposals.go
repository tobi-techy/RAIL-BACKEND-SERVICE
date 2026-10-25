package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"go.uber.org/zap"
)

// Proposal statuses Go assigns when it stores what the brain proposed. None of
// them means "committed": nothing on this path commits a record or moves money.
const (
	// ProposalStatusPendingValidation is a non-money intent (a stock count, a
	// delivery confirmation) waiting for Go's validator. TODO(mimi-records):
	// the validator and the records tables arrive with internal/records.
	ProposalStatusPendingValidation = "pending_validation"
	// ProposalStatusHeldMoney is any intent that touches money (a payment
	// claim). It is held for the matcher and the owner, never auto-committed:
	// a shop saying "I paid 25k" is a report, not a credit.
	ProposalStatusHeldMoney = "held_money_needs_review"
	// ProposalStatusNeedsOwnerApproval is a draft (an order request) that
	// would commit the distributor. Only the owner can approve it.
	ProposalStatusNeedsOwnerApproval = "needs_owner_approval"
	// ProposalStatusRejectedInvalid is a proposal that failed Go's shape
	// checks (wrong status, missing id, unknown kind). Kept for audit.
	ProposalStatusRejectedInvalid = "rejected_invalid"
)

// TurnProposal is one intent or draft the brain proposed, as Go stored it.
type TurnProposal struct {
	ProposalID string          `json:"proposal_id"` // intent_id or draft_id
	Type       string          `json:"type"`        // "intent" or "draft"
	Kind       string          `json:"kind"`
	Status     string          `json:"status"`
	Reason     string          `json:"reason,omitempty"`
	TouchMoney bool            `json:"touches_money"`
	TenantID   string          `json:"tenant_id"`
	ShopID     string          `json:"shop_id"`
	SenderID   string          `json:"sender_id"`
	MessageID  string          `json:"message_id"`
	TurnID     string          `json:"turn_id"`
	TraceID    string          `json:"trace_id"`
	Replayed   bool            `json:"replayed"`
	Payload    json.RawMessage `json:"payload"`
	ReceivedAt time.Time       `json:"received_at"`
}

// ProposalStore keeps proposals, deduplicated on ProposalID. Save returns
// created=false when the id was already stored, which is the normal outcome
// for a replayed turn.
//
// TODO(mimi-records): this is a minimal store. No existing table fits (the
// repo has no shop, records or proposals model yet), so proposals live in
// Redis with a TTL plus a structured log line. Move them to a durable
// agent_proposals table with a unique key on proposal_id when the records
// schema lands, and have the validator read from there.
type ProposalStore interface {
	Save(ctx context.Context, p TurnProposal) (created bool, err error)
}

// DefaultProposalTTL is how long a proposal stays in the minimal Redis store.
const DefaultProposalTTL = 7 * 24 * time.Hour

const proposalKeyPrefix = "mimi_turn_proposal:"

// RedisProposalStore dedupes with SETNX on the proposal id.
type RedisProposalStore struct {
	redis cache.RedisClient
	ttl   time.Duration
}

// NewRedisProposalStore builds the Redis-backed store.
func NewRedisProposalStore(redis cache.RedisClient, ttl time.Duration) *RedisProposalStore {
	if ttl <= 0 {
		ttl = DefaultProposalTTL
	}
	return &RedisProposalStore{redis: redis, ttl: ttl}
}

// Save stores the proposal once.
func (s *RedisProposalStore) Save(ctx context.Context, p TurnProposal) (bool, error) {
	if s == nil || s.redis == nil {
		return false, fmt.Errorf("proposal store: no redis")
	}
	body, err := json.Marshal(p)
	if err != nil {
		return false, err
	}
	return s.redis.SetNX(ctx, proposalKeyPrefix+p.ProposalID, string(body), s.ttl)
}

// MemoryProposalStore is an in-process store for tests and for wiring without Redis.
type MemoryProposalStore struct {
	mu    sync.Mutex
	items map[string]TurnProposal
	order []string
}

// NewMemoryProposalStore builds an empty in-memory store.
func NewMemoryProposalStore() *MemoryProposalStore {
	return &MemoryProposalStore{items: map[string]TurnProposal{}}
}

// Save stores the proposal once.
func (s *MemoryProposalStore) Save(_ context.Context, p TurnProposal) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[p.ProposalID]; ok {
		return false, nil
	}
	s.items[p.ProposalID] = p
	s.order = append(s.order, p.ProposalID)
	return true, nil
}

// All returns the stored proposals in insertion order.
func (s *MemoryProposalStore) All() []TurnProposal {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]TurnProposal, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, s.items[id])
	}
	return out
}

// ProposalOutcome counts what RecordTurnProposals did.
type ProposalOutcome struct {
	Stored     int
	Duplicates int
	HeldMoney  int
	Invalid    int
	Failed     int
}

var knownIntentKinds = map[string]bool{
	TurnIntentRecordPaymentClaim: true,
	TurnIntentRecordStockCount:   true,
	TurnIntentRecordDelivery:     true,
}

// classifyIntent decides the stored status of one intent. It never returns a
// committed status.
func classifyIntent(in TurnIntent) (status, reason string) {
	switch {
	case strings.TrimSpace(in.IntentID) == "":
		return ProposalStatusRejectedInvalid, "missing intent_id"
	case in.Status != "proposed":
		return ProposalStatusRejectedInvalid, fmt.Sprintf("status %q is not proposed", in.Status)
	case !knownIntentKinds[in.Kind]:
		return ProposalStatusRejectedInvalid, fmt.Sprintf("unknown kind %q", in.Kind)
	case in.Kind == TurnIntentRecordPaymentClaim && (in.AmountKobo <= 0 || in.Currency != "NGN"):
		return ProposalStatusRejectedInvalid, "payment claim needs a positive NGN amount"
	case in.TouchesMoney():
		return ProposalStatusHeldMoney, "money intents are never committed from a chat turn"
	default:
		return ProposalStatusPendingValidation, ""
	}
}

// RecordTurnProposals stores every intent and draft in resp as a proposal,
// deduplicated on intent_id / draft_id, and logs each one. It commits nothing.
//
// A store failure is logged and counted, never returned: the shop still gets
// its reply, and because ids are deterministic the next replay of the same
// message stores them.
func RecordTurnProposals(ctx context.Context, store ProposalStore, logger *zap.Logger, req *TurnRequest, resp *TurnResponse, now time.Time) ProposalOutcome {
	var out ProposalOutcome
	if resp == nil || req == nil {
		return out
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	save := func(p TurnProposal) {
		fields := []zap.Field{
			zap.String("proposal_id", p.ProposalID),
			zap.String("type", p.Type),
			zap.String("kind", p.Kind),
			zap.String("status", p.Status),
			zap.Bool("touches_money", p.TouchMoney),
			zap.String("tenant_id", p.TenantID),
			zap.String("shop_id", p.ShopID),
			zap.String("message_id", p.MessageID),
			zap.String("turn_id", p.TurnID),
			zap.String("trace_id", p.TraceID),
			zap.Bool("replayed", p.Replayed),
		}
		if p.Reason != "" {
			fields = append(fields, zap.String("reason", p.Reason))
		}
		if p.Status == ProposalStatusRejectedInvalid {
			out.Invalid++
		}
		if store == nil {
			out.Failed++
			logger.Warn("mimi turn proposal not stored: no proposal store wired", fields...)
			return
		}
		created, err := store.Save(ctx, p)
		switch {
		case err != nil:
			out.Failed++
			logger.Error("mimi turn proposal store failed", append(fields, zap.Error(err))...)
		case !created:
			out.Duplicates++
			logger.Debug("mimi turn proposal already stored", fields...)
		default:
			out.Stored++
			if p.Status == ProposalStatusHeldMoney {
				out.HeldMoney++
			}
			logger.Info("mimi turn proposal stored", fields...)
		}
	}

	base := TurnProposal{
		TenantID:   req.TenantID,
		ShopID:     req.ShopID,
		SenderID:   req.Sender.ID,
		MessageID:  req.MessageID,
		TurnID:     resp.TurnID,
		TraceID:    resp.TraceID,
		Replayed:   resp.Replayed,
		ReceivedAt: now.UTC(),
	}

	seen := map[string]bool{}
	for idx, in := range resp.Intents {
		if in.IntentID != "" && seen["i:"+in.IntentID] {
			out.Duplicates++
			continue
		}
		seen["i:"+in.IntentID] = true
		status, reason := classifyIntent(in)
		p := base
		p.ProposalID = in.IntentID
		if p.ProposalID == "" {
			p.ProposalID = fmt.Sprintf("invalid:%s:intent:%d", req.MessageID, idx)
		}
		p.Type = "intent"
		p.Kind = in.Kind
		p.Status = status
		p.Reason = reason
		p.TouchMoney = in.TouchesMoney()
		p.Payload = in.Raw
		save(p)
	}
	for idx, d := range resp.Drafts {
		if d.DraftID != "" && seen["d:"+d.DraftID] {
			out.Duplicates++
			continue
		}
		seen["d:"+d.DraftID] = true
		p := base
		p.ProposalID = d.DraftID
		p.Type = "draft"
		p.Kind = d.Kind
		p.Status = ProposalStatusNeedsOwnerApproval
		if strings.TrimSpace(d.DraftID) == "" {
			p.ProposalID = fmt.Sprintf("invalid:%s:draft:%d", req.MessageID, idx)
			p.Status = ProposalStatusRejectedInvalid
			p.Reason = "missing draft_id"
		}
		p.Payload = d.Raw
		save(p)
	}
	return out
}
