//go:build integration
// +build integration

package memory

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testDB(t *testing.T) *sqlx.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		dsn = "postgres://test:test@localhost:5432/stack_test?sslmode=disable"
	}
	db, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		t.Skipf("integration DB not reachable (%v); skipping", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestEventStore_RecordAndRetrieve(t *testing.T) {
	db := testDB(t)
	store := NewEventStore(db)
	ctx := context.Background()
	uid := uuid.New()

	event := &entities.MiriamUserEvent{
		UserID:     uid,
		EventType:  entities.EventSalaryReceived,
		Title:      "Salary received",
		Detail:     "Monthly salary deposited",
		Amount:     decimal.NewFromFloat(3200),
		Currency:   "USD",
		Metadata:   nil,
		OccurredAt: time.Now().UTC(),
	}

	err := store.RecordEvent(ctx, event)
	require.NoError(t, err)

	events, err := store.GetRecentEvents(ctx, uid, 24*time.Hour, 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "Salary received", events[0].Title)
	assert.True(t, events[0].Amount.Equal(decimal.NewFromFloat(3200)))
}

func TestEventStore_GetEventsByType(t *testing.T) {
	db := testDB(t)
	store := NewEventStore(db)
	ctx := context.Background()
	uid := uuid.New()

	require.NoError(t, store.RecordEvent(ctx, &entities.MiriamUserEvent{
		UserID: uid, EventType: entities.EventSalaryReceived,
		Title: "Salary", Detail: "Monthly", Amount: decimal.NewFromInt(3000),
		Currency: "USD", OccurredAt: time.Now().UTC(),
	}))
	require.NoError(t, store.RecordEvent(ctx, &entities.MiriamUserEvent{
		UserID: uid, EventType: entities.EventBudgetExceeded,
		Title: "Budget exceeded", Detail: "Groceries over limit", Amount: decimal.NewFromInt(100),
		Currency: "USD", OccurredAt: time.Now().UTC(),
	}))

	events, err := store.GetEventsByType(ctx, uid, entities.EventSalaryReceived, 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, entities.EventSalaryReceived, events[0].EventType)
}

func TestEventStore_BuildEventsContext(t *testing.T) {
	db := testDB(t)
	store := NewEventStore(db)
	ctx := context.Background()
	uid := uuid.New()

	require.NoError(t, store.RecordEvent(ctx, &entities.MiriamUserEvent{
		UserID: uid, EventType: entities.EventSalaryReceived,
		Title: "Salary received", Detail: "Monthly salary",
		Amount: decimal.NewFromFloat(3200), Currency: "USD",
		OccurredAt: time.Now().UTC(),
	}))

	text := store.BuildEventsContext(ctx, uid)
	assert.Contains(t, text, "RECENT FINANCIAL EVENTS")
	assert.Contains(t, text, "Salary received")
	assert.Contains(t, text, "$3,200.00")
}

func TestEventStore_BuildEventsContext_Empty(t *testing.T) {
	db := testDB(t)
	store := NewEventStore(db)
	ctx := context.Background()

	text := store.BuildEventsContext(ctx, uuid.New())
	assert.Empty(t, text)
}

func TestEventStore_GetRecentEvents_CutoffRespected(t *testing.T) {
	db := testDB(t)
	store := NewEventStore(db)
	ctx := context.Background()
	uid := uuid.New()

	// Old event (60 days ago)
	require.NoError(t, store.RecordEvent(ctx, &entities.MiriamUserEvent{
		UserID: uid, EventType: entities.EventGoalCompleted,
		Title: "Old event", Detail: "happened long ago",
		Amount: decimal.Zero, Currency: "USD",
		OccurredAt: time.Now().UTC().Add(-60 * 24 * time.Hour),
	}))

	// Recent event
	require.NoError(t, store.RecordEvent(ctx, &entities.MiriamUserEvent{
		UserID: uid, EventType: entities.EventSalaryReceived,
		Title: "Recent event", Detail: "just happened",
		Amount: decimal.NewFromInt(1000), Currency: "USD",
		OccurredAt: time.Now().UTC(),
	}))

	// Only get events from last 30 days
	events, err := store.GetRecentEvents(ctx, uid, 30*24*time.Hour, 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "Recent event", events[0].Title)
}

func TestMetrics_CountActiveFacts(t *testing.T) {
	db := testDB(t)
	metrics := NewMetrics(db)
	ctx := context.Background()
	uid := uuid.New()

	count, err := metrics.CountActiveFacts(ctx, uid)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestMetrics_GetFactDistribution_Empty(t *testing.T) {
	db := testDB(t)
	metrics := NewMetrics(db)
	ctx := context.Background()

	dist, err := metrics.GetFactDistribution(ctx, uuid.New())
	require.NoError(t, err)
	assert.Empty(t, dist)
}

func TestMetrics_GetGlobalStats(t *testing.T) {
	db := testDB(t)
	metrics := NewMetrics(db)
	ctx := context.Background()

	stats, err := metrics.GetGlobalStats(ctx)
	require.NoError(t, err)
	assert.NotNil(t, stats)
	// Just verify it doesn't error — actual values depend on DB state
	assert.GreaterOrEqual(t, stats.TotalUsers, 0)
}
