package repositories

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	confirmationSvc "github.com/rail-service/rail_service/internal/domain/services/confirmation"
)

func testConfirmationDB(t *testing.T) *sqlx.DB {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset: confirmation repository test needs Postgres (CI provides it)")
	}
	db, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		t.Skipf("no Postgres available: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	schema, err := os.ReadFile("../../../migrations/319_confirmations.up.sql")
	if err != nil {
		// Fallback: minimal table when run outside the repo layout.
		schema = []byte(`CREATE TABLE IF NOT EXISTS confirmations (
			id UUID PRIMARY KEY, user_id UUID NOT NULL, action TEXT NOT NULL,
			state TEXT NOT NULL, title TEXT NOT NULL DEFAULT '', subtitle TEXT NOT NULL DEFAULT '',
			amount TEXT NOT NULL DEFAULT '', asset TEXT NOT NULL DEFAULT '',
			destination TEXT NOT NULL DEFAULT '', fee TEXT NOT NULL DEFAULT '',
			risk_line TEXT NOT NULL DEFAULT '', payload JSONB NOT NULL DEFAULT '{}',
			execute_key TEXT NOT NULL UNIQUE, expires_at TIMESTAMPTZ NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			completed_at TIMESTAMPTZ, result_summary TEXT NOT NULL DEFAULT '',
			assurance TEXT NOT NULL DEFAULT '', token_used BOOLEAN NOT NULL DEFAULT FALSE,
			card_edit_failed BOOLEAN NOT NULL DEFAULT FALSE)`)
	}
	if _, err := db.Exec(string(schema)); err != nil {
		t.Fatalf("ensure confirmations table: %v", err)
	}
	return db
}

func testConfirmation(userID uuid.UUID) *entities.Confirmation {
	now := time.Now().UTC()
	return &entities.Confirmation{
		ID: uuid.New(), UserID: userID, Action: "transfer.send",
		State: "pending", Title: "Send", Subtitle: "test",
		Amount: "5", Payload: map[string]any{"to": "@a", "n": 1},
		ExecuteKey: uuid.NewString(), ExpiresAt: now.Add(5 * time.Minute),
		CreatedAt: now, UpdatedAt: now,
	}
}

func TestConfirmationRepositoryRoundTrip(t *testing.T) {
	db := testConfirmationDB(t)
	r := NewConfirmationRepository(db, zap.NewNop())
	ctx := context.Background()
	uid := uuid.New()
	c := testConfirmation(uid)

	if err := r.Save(ctx, c); err != nil {
		t.Fatal(err)
	}
	got, err := r.Load(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Send" || got.Amount != "5" || got.State != "pending" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if got.Payload["to"] != "@a" {
		t.Fatalf("payload mismatch: %v", got.Payload)
	}
	// Upsert updates in place.
	c.State = "approved"
	c.ResultSummary = ""
	if err := r.Save(ctx, c); err != nil {
		t.Fatal(err)
	}
	got2, err := r.Load(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got2.State != "approved" {
		t.Fatalf("upsert state = %s", got2.State)
	}
	// Unknown id is typed, not generic.
	if _, err := r.Load(ctx, uuid.New()); err != confirmationSvc.ErrConfirmationNotFound {
		t.Fatalf("want ErrConfirmationNotFound, got %v", err)
	}
}

func TestConfirmationRepositoryConcurrentClaim(t *testing.T) {
	db := testConfirmationDB(t)
	r := NewConfirmationRepository(db, zap.NewNop())
	ctx := context.Background()
	c := testConfirmation(uuid.New())
	if err := r.Save(ctx, c); err != nil {
		t.Fatal(err)
	}
	const racers = 20
	var winners int64
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, err := r.Claim(ctx, c.ID, "passkey")
			if err == nil && claimed.TokenUsed && claimed.Assurance == "passkey" {
				atomic.AddInt64(&winners, 1)
			}
		}()
	}
	wg.Wait()
	if winners != 1 {
		t.Fatalf("exactly one claim winner required, got %d", winners)
	}
	// The loser path is typed for the resume logic.
	if _, err := r.Claim(ctx, c.ID, "passkey"); err != confirmationSvc.ErrTokenConsumed {
		t.Fatalf("second claim must lose typed, got %v", err)
	}
	if _, err := r.Claim(ctx, uuid.New(), ""); err != confirmationSvc.ErrConfirmationNotFound {
		t.Fatalf("unknown claim must be not-found, got %v", err)
	}
}
