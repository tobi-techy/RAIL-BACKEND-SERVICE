package ledger_outbox_publisher

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/rail-service/rail_service/internal/domain/services/miriam"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

type retryCall struct {
	id      uuid.UUID
	lastErr string
}

// fakeStore emulates the lease claim semantics of LedgerRepository: claiming
// leases records (claimed_at set, removed from the pending queue);
// IncrementOutboxRetry releases the lease and returns the record to the queue
// with retry_count bumped; MarkOutboxPublished removes leased records once
// their dispatch succeeds. A failed commit or rollback returns leased records
// to the queue (the claim never took effect).
type fakeStore struct {
	unpublished []repositories.OutboxRecord
	leased      []repositories.OutboxRecord

	beginErr  error
	claimErr  error
	commitErr error
	retryErr  error
	markErr   error

	begins    int
	commits   int
	rollbacks int
	claims    int
	marks     int
	retries   []retryCall
}

func (f *fakeStore) BeginTx(ctx context.Context) (context.Context, error) {
	f.begins++
	if f.beginErr != nil {
		return nil, f.beginErr
	}
	return ctx, nil
}

func (f *fakeStore) CommitTx(ctx context.Context) error {
	f.commits++
	if f.commitErr != nil {
		// A failed commit means the claim never took effect.
		f.unpublished = append(f.unpublished, f.leased...)
		f.leased = nil
	}
	return f.commitErr
}

func (f *fakeStore) RollbackTx(ctx context.Context) error {
	f.rollbacks++
	f.unpublished = append(f.unpublished, f.leased...)
	f.leased = nil
	return nil
}

func (f *fakeStore) ClaimUnpublishedOutbox(ctx context.Context, batchSize int, maxRetries int) ([]repositories.OutboxRecord, error) {
	f.claims++
	if f.claimErr != nil {
		return nil, f.claimErr
	}

	now := time.Now().UTC()
	var claimed []repositories.OutboxRecord
	var remaining []repositories.OutboxRecord
	for _, rec := range f.unpublished {
		if len(claimed) >= batchSize || rec.RetryCount >= maxRetries {
			remaining = append(remaining, rec)
			continue
		}
		rec.ClaimedAt = &now
		claimed = append(claimed, rec)
		f.leased = append(f.leased, rec)
	}
	f.unpublished = remaining
	return claimed, nil
}

func (f *fakeStore) IncrementOutboxRetry(ctx context.Context, id uuid.UUID, lastErr string) error {
	f.retries = append(f.retries, retryCall{id: id, lastErr: lastErr})
	if f.retryErr != nil {
		return f.retryErr
	}

	for i, rec := range f.leased {
		if rec.ID != id {
			continue
		}
		rec.RetryCount++
		rec.LastError = &lastErr
		rec.ClaimedAt = nil
		f.unpublished = append(f.unpublished, rec)
		f.leased = append(f.leased[:i], f.leased[i+1:]...)
		break
	}
	return nil
}

func (f *fakeStore) MarkOutboxPublished(ctx context.Context, ids []uuid.UUID) error {
	f.marks++
	if f.markErr != nil {
		return f.markErr
	}

	byID := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		byID[id] = struct{}{}
	}
	now := time.Now().UTC()
	var kept []repositories.OutboxRecord
	for _, rec := range f.leased {
		if _, ok := byID[rec.ID]; !ok {
			kept = append(kept, rec)
			continue
		}
		rec.PublishedAt = &now
		rec.ClaimedAt = nil
	}
	f.leased = kept
	return nil
}

func record(eventType string) repositories.OutboxRecord {
	return repositories.OutboxRecord{
		ID:            uuid.New(),
		EventType:     eventType,
		AggregateID:   uuid.New(),
		AggregateType: "ledger_transaction",
	}
}

func TestPublishOnce_ClaimedEventsAreNotRedispatched(t *testing.T) {
	store := &fakeStore{unpublished: []repositories.OutboxRecord{
		record("transaction.completed"),
		record("balance.updated"),
	}}
	w := NewWorker(store, zap.NewNop())

	w.publishOnce(context.Background())
	require.Empty(t, store.retries, "successful dispatch must not increment retries")
	assert.Equal(t, 1, store.marks)
	assert.Empty(t, store.leased, "successful dispatch must mark the batch published")
	assert.Empty(t, store.unpublished, "claim must retire the batch")

	// The pre-fix bug re-dispatched the same rows every tick forever.
	w.publishOnce(context.Background())
	assert.Equal(t, 2, store.claims)
	assert.Equal(t, 2, store.commits)
	assert.Equal(t, 0, store.rollbacks)
	assert.Empty(t, store.retries)
}

func TestPublishOnce_UnknownEventTypeIncrementsRetry(t *testing.T) {
	bad := record("bogus.event")
	good := record("transaction.reversed")
	store := &fakeStore{unpublished: []repositories.OutboxRecord{bad, good}}
	w := NewWorker(store, zap.NewNop())

	w.publishOnce(context.Background())

	require.Len(t, store.retries, 1)
	assert.Equal(t, bad.ID, store.retries[0].id)
	assert.Contains(t, store.retries[0].lastErr, "unknown event type")
	// Only the failed event goes back on the queue for a later tick.
	require.Len(t, store.unpublished, 1)
	assert.Equal(t, bad.ID, store.unpublished[0].ID)
	assert.Equal(t, 1, store.unpublished[0].RetryCount)
	assert.Nil(t, store.unpublished[0].ClaimedAt, "retried event must be un-leased")
	// The successful event is marked published.
	assert.Empty(t, store.leased)
}

func TestPublishOnce_StopsRetryingPastMaxRetries(t *testing.T) {
	exhausted := record("bogus.event")
	exhausted.RetryCount = MaxOutboxRetries
	store := &fakeStore{unpublished: []repositories.OutboxRecord{exhausted}}
	w := NewWorker(store, zap.NewNop())

	w.publishOnce(context.Background())

	assert.Empty(t, store.retries, "dead-lettered events must not be claimed again")
	assert.Len(t, store.unpublished, 1)
	assert.Empty(t, store.leased)
}

func TestPublishOnce_ClaimErrorRollsBackAndDispatchesNothing(t *testing.T) {
	store := &fakeStore{
		unpublished: []repositories.OutboxRecord{record("balance.updated")},
		claimErr:    errors.New("claim exploded"),
	}
	w := NewWorker(store, zap.NewNop())

	w.publishOnce(context.Background())

	assert.Equal(t, 1, store.rollbacks)
	assert.Equal(t, 0, store.commits)
	assert.Empty(t, store.retries)
	assert.Empty(t, store.leased)
	assert.Len(t, store.unpublished, 1, "unclaimed events stay pending")
}

func TestPublishOnce_CommitErrorDispatchesNothing(t *testing.T) {
	store := &fakeStore{
		unpublished: []repositories.OutboxRecord{record("balance.updated")},
		commitErr:   errors.New("commit failed"),
	}
	w := NewWorker(store, zap.NewNop())

	w.publishOnce(context.Background())

	assert.Equal(t, 1, store.claims)
	assert.Equal(t, 1, store.commits)
	assert.Empty(t, store.retries, "a failed commit must not dispatch or retry")
	assert.Equal(t, 0, store.marks, "a failed commit must not mark anything published")
	assert.Empty(t, store.leased, "a failed commit returns the claim to the queue")
	assert.Len(t, store.unpublished, 1)
}

func TestPublishOnce_RetryWriteFailureLeavesEventLeased(t *testing.T) {
	// Dispatch fails and the retry write also fails. Under the pre-fix design
	// the event kept published_at set and was never delivered again; with the
	// lease the event stays leased and is reclaimed once the lease expires.
	bad := record("bogus.event")
	store := &fakeStore{
		unpublished: []repositories.OutboxRecord{bad},
		retryErr:    errors.New("retry write failed"),
	}
	w := NewWorker(store, zap.NewNop())

	w.publishOnce(context.Background())

	require.Len(t, store.retries, 1)
	require.Len(t, store.leased, 1, "failed retry write must leave the event leased, not lost")
	assert.Equal(t, bad.ID, store.leased[0].ID)
	assert.NotNil(t, store.leased[0].ClaimedAt, "event remains reserved until the lease expires")
	assert.Empty(t, store.unpublished)
}

func TestPublishOnce_MarkPublishedFailureLeavesEventsLeased(t *testing.T) {
	// Dispatch succeeds but the publish-mark write fails: the events stay
	// leased and are re-dispatched after the lease expires (at-least-once).
	store := &fakeStore{
		unpublished: []repositories.OutboxRecord{
			record("transaction.completed"),
			record("balance.updated"),
		},
		markErr: errors.New("mark write failed"),
	}
	w := NewWorker(store, zap.NewNop())

	w.publishOnce(context.Background())

	assert.Equal(t, 1, store.marks)
	assert.Empty(t, store.retries)
	require.Len(t, store.leased, 2, "failed publish-mark must leave the batch leased for re-dispatch")
	assert.Empty(t, store.unpublished)
}

func TestPublishOnce_SerializationFailureIsNotFatal(t *testing.T) {
	store := &fakeStore{
		unpublished: []repositories.OutboxRecord{record("balance.updated")},
		claimErr:    &pq.Error{Code: "40001"},
	}
	w := NewWorker(store, zap.NewNop())

	w.publishOnce(context.Background())

	assert.Equal(t, 1, store.rollbacks)
	assert.Len(t, store.unpublished, 1)
	assert.Empty(t, store.leased)
}

func TestPublishOnce_BeginErrorSkipsClaim(t *testing.T) {
	store := &fakeStore{
		unpublished: []repositories.OutboxRecord{record("balance.updated")},
		beginErr:    errors.New("pool exhausted"),
	}
	w := NewWorker(store, zap.NewNop())

	w.publishOnce(context.Background())

	assert.Equal(t, 0, store.claims)
	assert.Equal(t, 0, store.rollbacks)
	assert.Len(t, store.unpublished, 1)
}

func TestPublishOnce_DeadLetterWarnLoggedAtRetryCeiling(t *testing.T) {
	bad := record("bogus.event")
	bad.RetryCount = MaxOutboxRetries - 1
	store := &fakeStore{unpublished: []repositories.OutboxRecord{bad}}

	core, logs := observer.New(zapcore.WarnLevel)
	w := NewWorker(store, zap.New(core))

	w.publishOnce(context.Background())

	require.Len(t, store.retries, 1)
	require.Len(t, store.unpublished, 1)
	assert.Equal(t, MaxOutboxRetries, store.unpublished[0].RetryCount, "event is dead-lettered at the ceiling")
	assert.Equal(t, 1, logs.FilterMessage("Outbox event reached max retries, dead-lettering").Len())
}

type fakeMiriamPublisher struct {
	published []miriam.MoneyEvent
	failNext  error
}

func (f *fakeMiriamPublisher) PublishMoneyEvent(_ context.Context, evt miriam.MoneyEvent) error {
	if f.failNext != nil {
		err := f.failNext
		f.failNext = nil
		return err
	}
	f.published = append(f.published, evt)
	return nil
}

// TestPublishOnce_PublishesMoneyEventsToMiriam verifies that when a Miriam
// publisher is wired, the outbox dispatches money events to Miriam's always-on
// event stream. A publish failure leaves the event leased for retry (the
// standard dispatch-error path).
func TestPublishOnce_PublishesMoneyEventsToMiriam(t *testing.T) {
	rec := record("transaction.completed")
	rec.Payload = []byte(`{"amount":"100.00","currency":"USD"}`)
	store := &fakeStore{unpublished: []repositories.OutboxRecord{rec}}
	pub := &fakeMiriamPublisher{}

	w := NewWorker(store, zap.NewNop())
	w.SetMiriamPublisher(pub)

	w.publishOnce(context.Background())

	require.Len(t, pub.published, 1)
	assert.Equal(t, rec.ID.String(), pub.published[0].ID)
	assert.Equal(t, rec.AggregateID, pub.published[0].UserID)
	assert.Equal(t, rec.EventType, pub.published[0].EventType)
	assert.Equal(t, "100.00", pub.published[0].Payload["amount"])
	assert.Empty(t, store.leased, "successful publish marks the event published")
}

func TestPublishOnce_MiriamPublishFailureRetries(t *testing.T) {
	rec := record("balance.updated")
	store := &fakeStore{unpublished: []repositories.OutboxRecord{rec}}
	pub := &fakeMiriamPublisher{failNext: errors.New("redis unavailable")}

	w := NewWorker(store, zap.NewNop())
	w.SetMiriamPublisher(pub)

	w.publishOnce(context.Background())

	assert.Empty(t, pub.published)
	require.Len(t, store.retries, 1)
	assert.Equal(t, rec.ID, store.retries[0].id)
	assert.Contains(t, store.retries[0].lastErr, "redis unavailable")
}

// LedgerRepository must keep satisfying the interface the worker is wired with
// in internal/app/application.go.
var _ OutboxReader = (*repositories.LedgerRepository)(nil)
