package memory

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// EventStore persists and queries the user's financial event timeline.
type EventStore struct {
	db *sqlx.DB
}

// NewEventStore creates a new event store.
func NewEventStore(db *sqlx.DB) *EventStore {
	return &EventStore{db: db}
}

// RecordEvent inserts a financial event into the timeline.
func (s *EventStore) RecordEvent(ctx context.Context, event *entities.MiriamUserEvent) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO miriam_user_events (user_id, event_type, title, detail, amount, currency, metadata, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		event.UserID, event.EventType, event.Title, event.Detail,
		event.Amount, event.Currency, event.Metadata, event.OccurredAt,
	)
	if err != nil {
		return fmt.Errorf("record event: %w", err)
	}
	return nil
}

// GetRecentEvents returns the most recent events for a user, limited to the
// given duration. Used to inject recent financial context into prompts.
func (s *EventStore) GetRecentEvents(ctx context.Context, userID uuid.UUID, since time.Duration, limit int) ([]*entities.MiriamUserEvent, error) {
	if limit <= 0 {
		limit = 15
	}
	cutoff := time.Now().UTC().Add(-since)

	var events []*entities.MiriamUserEvent
	err := s.db.SelectContext(ctx, &events, `
		SELECT id, user_id, event_type, title, detail, amount, currency, metadata, occurred_at, created_at
		FROM miriam_user_events
		WHERE user_id = $1 AND occurred_at >= $2
		ORDER BY occurred_at DESC
		LIMIT $3`, userID, cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("get recent events: %w", err)
	}
	return events, nil
}

// GetEventsByType returns recent events of a specific type for a user.
func (s *EventStore) GetEventsByType(ctx context.Context, userID uuid.UUID, eventType string, limit int) ([]*entities.MiriamUserEvent, error) {
	if limit <= 0 {
		limit = 10
	}
	var events []*entities.MiriamUserEvent
	err := s.db.SelectContext(ctx, &events, `
		SELECT id, user_id, event_type, title, detail, amount, currency, metadata, occurred_at, created_at
		FROM miriam_user_events
		WHERE user_id = $1 AND event_type = $2
		ORDER BY occurred_at DESC
		LIMIT $3`, userID, eventType, limit)
	if err != nil {
		return nil, fmt.Errorf("get events by type: %w", err)
	}
	return events, nil
}

// BuildEventsContext formats recent events into a system prompt injection.
func (s *EventStore) BuildEventsContext(ctx context.Context, userID uuid.UUID) string {
	events, err := s.GetRecentEvents(ctx, userID, 30*24*time.Hour, 15)
	if err != nil || len(events) == 0 {
		return ""
	}

	text := "[RECENT FINANCIAL EVENTS — weave these into your understanding of the user's situation naturally.]\n"
	for _, e := range events {
		amount := ""
		if e.Amount.GreaterThan(decimal.Zero) {
			amount = fmt.Sprintf(" ($%s %s)", e.Amount.StringFixed(2), e.Currency)
		}
		text += fmt.Sprintf("- %s%s — %s (%s)\n",
			e.Title, amount, e.Detail, e.OccurredAt.Format("Jan 2"))
	}
	return text
}
