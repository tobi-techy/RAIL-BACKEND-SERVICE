package ledger_outbox_publisher

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type retryCall struct {
	id      uuid.UUID
	lastErr string
}

// fakeStore emulates the claim semantics of LedgerRepository: claiming marks
// records published, and IncrementOutboxRetry un-publishes them for a later tick.
type fakeStore struct {
	unpublished []repositories.OutboxRecord

	beginErr  error
	claimErr  error
	commitErr error
	retryErr  error

	begins    int
	commits   int
	rollbacks int
	claims    int
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
	return f.commitErr
}

func (f *fakeStore) RollbackTx(ctx context.Context) error {
	f.rollbacks++
	return nil
}

func (f *fakeStore) ClaimUnpublishedOutbox(ctx context.Context, batchSize int, maxRetries int) ([]repositories.OutboxRecord, error) {
	f.claims++
	if f.claimErr != nil {
		return nil, f.claimErr
	}

	var claimed []repositories.OutboxRecord
	var remaining []repositories.OutboxRecord
	for _, rec := range f.unpublished {
		if len(claimed) >= batchSize || rec.RetryCount >= maxRetries {
			remaining = append(remaining, rec)
			continue
		}
		claimed = append(claimed, rec)
	}
	f.unpublished = remaining
	return claimed, nil
}

func (f *fakeStore) IncrementOutboxRetry(ctx context.Context, id uuid.UUID, lastErr string) error {
	f.retries = append(f.retries, retryCall{id: id, lastErr: lastErr})
	if f.retryErr != nil {
		return f.retryErr
	}
	f.unpublished = append(f.unpublished, repositories.OutboxRecord{
		ID:         id,
		EventType:  "unknown.event",
		RetryCount: 1,
	})
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
	require.Empty(t, store.unpublished, "claim must retire the batch")

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
}

func TestPublishOnce_StopsRetryingPastMaxRetries(t *testing.T) {
	exhausted := record("bogus.event")
	exhausted.RetryCount = maxOutboxRetries
	store := &fakeStore{unpublished: []repositories.OutboxRecord{exhausted}}
	w := NewWorker(store, zap.NewNop())

	w.publishOnce(context.Background())

	assert.Empty(t, store.retries, "dead-lettered events must not be claimed again")
	assert.Len(t, store.unpublished, 1)
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
	assert.Len(t, store.unpublished, 1, "unclaimed events stay pending")
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

// LedgerRepository must keep satisfying the interface the worker is wired with
// in internal/app/application.go.
var _ OutboxReader = (*repositories.LedgerRepository)(nil)
