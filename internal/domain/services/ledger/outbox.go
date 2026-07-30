package ledger

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// OutboxEventType enumerates ledger events published to the outbox.
type OutboxEventType string

const (
	EventTransactionCreated   OutboxEventType = "transaction.created"
	EventTransactionCompleted OutboxEventType = "transaction.completed"
	EventTransactionFailed    OutboxEventType = "transaction.failed"
	EventTransactionReversed  OutboxEventType = "transaction.reversed"
	EventBalanceUpdated       OutboxEventType = "balance.updated"
)

// OutboxWriter inserts outbox records atomically with ledger writes.
type OutboxWriter struct {
	repo outboxRepository
}

// outboxRepository is the narrow interface the outbox writer needs.
type outboxRepository interface {
	InsertOutboxRecord(ctx context.Context, eventType string, aggregateID uuid.UUID, aggregateType string, payload json.RawMessage) error
}

// NewOutboxWriter creates a new outbox writer.
func NewOutboxWriter(repo outboxRepository) *OutboxWriter {
	return &OutboxWriter{repo: repo}
}

// WriteTransactionEvents writes outbox events for a completed transaction.
func (w *OutboxWriter) WriteTransactionEvents(ctx context.Context, tx *entities.LedgerTransaction) error {
	payload := map[string]any{
		"transaction_id":   tx.ID,
		"transaction_type": tx.TransactionType,
		"user_id":          tx.UserID,
		"status":           tx.Status,
		"description":      tx.Description,
		"idempotency_key":  tx.IdempotencyKey,
		"reference_id":     tx.ReferenceID,
		"reference_type":   tx.ReferenceType,
	}

	eventType := EventTransactionCreated
	if tx.Status == entities.TransactionStatusCompleted {
		eventType = EventTransactionCompleted
	}
	if tx.Status == entities.TransactionStatusFailed {
		eventType = EventTransactionFailed
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal outbox payload: %w", err)
	}

	return w.repo.InsertOutboxRecord(ctx, string(eventType), tx.ID, "ledger_transaction", raw)
}

// WriteBalanceEvent writes an outbox event for a balance change.
func (w *OutboxWriter) WriteBalanceEvent(ctx context.Context, accountID uuid.UUID, oldBalance, newBalance any) error {
	payload := map[string]any{
		"account_id":  accountID,
		"old_balance": oldBalance,
		"new_balance": newBalance,
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal outbox payload: %w", err)
	}

	return w.repo.InsertOutboxRecord(ctx, string(EventBalanceUpdated), accountID, "ledger_account", raw)
}
