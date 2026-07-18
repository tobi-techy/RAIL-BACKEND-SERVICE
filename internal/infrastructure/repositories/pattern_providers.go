package repositories

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/services/miriam"
)

// PatternUserProvider implements miriam.UserProvider by querying the users table.
type PatternUserProvider struct {
	db *sqlx.DB
}

func NewPatternUserProvider(db *sqlx.DB) *PatternUserProvider {
	return &PatternUserProvider{db: db}
}

func (p *PatternUserProvider) GetUserLastName(ctx context.Context, userID uuid.UUID) (string, error) {
	var lastName *string
	err := p.db.GetContext(ctx, &lastName, `SELECT last_name FROM users WHERE id = $1`, userID)
	if err != nil {
		return "", fmt.Errorf("get user last name: %w", err)
	}
	if lastName == nil {
		return "", nil
	}
	return *lastName, nil
}

// PatternTransferProvider implements miriam.TransferProvider by querying p2p_transfers
// joined with users to get recipient names.
type PatternTransferProvider struct {
	db *sqlx.DB
}

func NewPatternTransferProvider(db *sqlx.DB) *PatternTransferProvider {
	return &PatternTransferProvider{db: db}
}

func (p *PatternTransferProvider) GetOutgoingTransfers(ctx context.Context, userID uuid.UUID, since time.Time) ([]miriam.TransferRecord, error) {
	var records []miriam.TransferRecord
	err := p.db.SelectContext(ctx, &records, `
		SELECT
			COALESCE(
				u.first_name || ' ' || COALESCE(u.last_name, ''),
				t.recipient_identifier
			) AS counterparty,
			t.amount,
			t.created_at,
			t.currency
		FROM p2p_transfers t
		LEFT JOIN users u ON u.id = t.recipient_id
		WHERE t.sender_id = $1
		  AND t.status = 'completed'
		  AND t.created_at >= $2
		ORDER BY t.created_at DESC`, userID, since)
	if err != nil {
		return nil, fmt.Errorf("get outgoing transfers: %w", err)
	}
	return records, nil
}
