package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	confirmationSvc "github.com/rail-service/rail_service/internal/domain/services/confirmation"
)

// ConfirmationRepository is the Postgres-backed confirmation.Store. It is the
// cross-replica source of truth for the single-use token: Claim flips
// token_used atomically (exactly one winner), and execute_key is UNIQUE so a
// crash-retry can never stage the same card twice.
type ConfirmationRepository struct {
	db  *sqlx.DB
	log *zap.Logger
}

func NewConfirmationRepository(db *sqlx.DB, logger *zap.Logger) *ConfirmationRepository {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &ConfirmationRepository{db: db, log: logger}
}

var _ confirmationSvc.Store = (*ConfirmationRepository)(nil)

type confirmationRow struct {
	ID             uuid.UUID       `db:"id"`
	UserID         uuid.UUID       `db:"user_id"`
	Action         string          `db:"action"`
	State          string          `db:"state"`
	Title          string          `db:"title"`
	Subtitle       string          `db:"subtitle"`
	Amount         string          `db:"amount"`
	Asset          string          `db:"asset"`
	Destination    string          `db:"destination"`
	Fee            string          `db:"fee"`
	RiskLine       string          `db:"risk_line"`
	Payload        json.RawMessage `db:"payload"`
	ExecuteKey     string          `db:"execute_key"`
	ExpiresAt      time.Time       `db:"expires_at"`
	CreatedAt      time.Time       `db:"created_at"`
	UpdatedAt      time.Time       `db:"updated_at"`
	CompletedAt    *time.Time      `db:"completed_at"`
	ResultSummary  string          `db:"result_summary"`
	Assurance      string          `db:"assurance"`
	TokenUsed      bool            `db:"token_used"`
	CardEditFailed bool            `db:"card_edit_failed"`
}

const confirmationColumns = `id, user_id, action, state, title, subtitle, amount, asset,
	destination, fee, risk_line, payload, execute_key, expires_at, created_at,
	updated_at, completed_at, result_summary, assurance, token_used, card_edit_failed`

func rowToConfirmation(r *confirmationRow) (*entities.Confirmation, error) {
	c := &entities.Confirmation{
		ID:             r.ID,
		UserID:         r.UserID,
		Action:         entities.ConfirmationAction(r.Action),
		State:          entities.ConfirmationState(r.State),
		Title:          r.Title,
		Subtitle:       r.Subtitle,
		Amount:         r.Amount,
		Asset:          r.Asset,
		Destination:    r.Destination,
		Fee:            r.Fee,
		RiskLine:       r.RiskLine,
		ExecuteKey:     r.ExecuteKey,
		ExpiresAt:      r.ExpiresAt.UTC(),
		CreatedAt:      r.CreatedAt.UTC(),
		UpdatedAt:      r.UpdatedAt.UTC(),
		CompletedAt:    r.CompletedAt,
		ResultSummary:  r.ResultSummary,
		Assurance:      r.Assurance,
		TokenUsed:      r.TokenUsed,
		CardEditFailed: r.CardEditFailed,
	}
	if len(r.Payload) > 0 {
		var payload map[string]any
		if err := json.Unmarshal(r.Payload, &payload); err != nil {
			return nil, fmt.Errorf("decode confirmation payload: %w", err)
		}
		c.Payload = payload
	}
	return c, nil
}

// Save upserts the full record. The service always writes complete rows, so
// upsert-by-id is the correct primitive (no partial merges to drift).
func (r *ConfirmationRepository) Save(ctx context.Context, c *entities.Confirmation) error {
	payload, err := json.Marshal(c.Payload)
	if err != nil {
		return fmt.Errorf("encode confirmation payload: %w", err)
	}
	if len(payload) == 0 || string(payload) == "null" {
		payload = []byte("{}")
	}
	query := `
		INSERT INTO confirmations (` + confirmationColumns + `)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)
		ON CONFLICT (id) DO UPDATE SET
			user_id=EXCLUDED.user_id, action=EXCLUDED.action, state=EXCLUDED.state,
			title=EXCLUDED.title, subtitle=EXCLUDED.subtitle, amount=EXCLUDED.amount,
			asset=EXCLUDED.asset, destination=EXCLUDED.destination, fee=EXCLUDED.fee,
			risk_line=EXCLUDED.risk_line, payload=EXCLUDED.payload,
			execute_key=EXCLUDED.execute_key, expires_at=EXCLUDED.expires_at,
			created_at=EXCLUDED.created_at, updated_at=EXCLUDED.updated_at,
			completed_at=EXCLUDED.completed_at, result_summary=EXCLUDED.result_summary,
			assurance=EXCLUDED.assurance, token_used=EXCLUDED.token_used,
			card_edit_failed=EXCLUDED.card_edit_failed`
	_, err = r.db.ExecContext(ctx, query,
		c.ID, c.UserID, string(c.Action), string(c.State), c.Title, c.Subtitle,
		c.Amount, c.Asset, c.Destination, c.Fee, c.RiskLine, string(payload),
		c.ExecuteKey, c.ExpiresAt, c.CreatedAt, c.UpdatedAt, c.CompletedAt,
		c.ResultSummary, c.Assurance, c.TokenUsed, c.CardEditFailed)
	if err != nil {
		return fmt.Errorf("save confirmation: %w", err)
	}
	return nil
}

// MergePayloadKeys merges keys into the payload JSONB in a single statement
// that never touches token_used/state: concurrent Claim winners cannot be
// resurrected by a stale full-row Save.
func (r *ConfirmationRepository) MergePayloadKeys(ctx context.Context, id uuid.UUID, set map[string]any) error {
	if len(set) == 0 {
		return nil
	}
	raw, err := json.Marshal(set)
	if err != nil {
		return fmt.Errorf("encode confirmation payload patch: %w", err)
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE confirmations
		SET payload = COALESCE(payload, '{}'::jsonb) || $2::jsonb,
		    updated_at = now()
		WHERE id = $1`, id, string(raw))
	if err != nil {
		return fmt.Errorf("merge confirmation payload: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("merge confirmation payload rows: %w", err)
	}
	if n == 0 {
		return confirmationSvc.ErrConfirmationNotFound
	}
	return nil
}

// DeletePayloadKeys removes keys from the payload JSONB without touching any
// other column (same resurrection hazard as MergePayloadKeys).
func (r *ConfirmationRepository) DeletePayloadKeys(ctx context.Context, id uuid.UUID, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	query := `UPDATE confirmations SET payload = COALESCE(payload, '{}'::jsonb)`
	args := make([]any, 0, len(keys)+1)
	args = append(args, id)
	for _, k := range keys {
		args = append(args, k)
		query += ` - $` + strconv.Itoa(len(args))
	}
	query += `, updated_at = now() WHERE id = $1`
	res, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("delete confirmation payload keys: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete confirmation payload keys rows: %w", err)
	}
	if n == 0 {
		return confirmationSvc.ErrConfirmationNotFound
	}
	return nil
}

// Load fetches one card. Unknown ids are ErrConfirmationNotFound; anything
// else fails closed.
func (r *ConfirmationRepository) Load(ctx context.Context, id uuid.UUID) (*entities.Confirmation, error) {
	var row confirmationRow
	err := r.db.GetContext(ctx, &row,
		`SELECT `+confirmationColumns+` FROM confirmations WHERE id = $1`, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, confirmationSvc.ErrConfirmationNotFound
		}
		return nil, fmt.Errorf("load confirmation: %w", err)
	}
	return rowToConfirmation(&row)
}

// Claim atomically flips token_used for exactly one caller. The
// token_used=FALSE predicate makes the UPDATE itself the lock: concurrent
// claimers get zero rows, and we distinguish "no such card" from "already
// spent" with a follow-up read.
func (r *ConfirmationRepository) Claim(ctx context.Context, id uuid.UUID, assurance string) (*entities.Confirmation, error) {
	var row confirmationRow
	err := r.db.GetContext(ctx, &row, `
		UPDATE confirmations
		SET token_used = TRUE,
		    assurance = CASE WHEN $2 <> '' THEN $2 ELSE assurance END,
		    updated_at = now()
		WHERE id = $1 AND token_used = FALSE
		RETURNING `+confirmationColumns, id, assurance)
	if err != nil {
		if err == sql.ErrNoRows {
			var used bool
			if lerr := r.db.GetContext(ctx, &used,
				`SELECT token_used FROM confirmations WHERE id = $1`, id); lerr != nil {
				if lerr == sql.ErrNoRows {
					return nil, confirmationSvc.ErrConfirmationNotFound
				}
				return nil, fmt.Errorf("claim confirmation lookup: %w", lerr)
			}
			return nil, confirmationSvc.ErrTokenConsumed
		}
		return nil, fmt.Errorf("claim confirmation: %w", err)
	}
	return rowToConfirmation(&row)
}
