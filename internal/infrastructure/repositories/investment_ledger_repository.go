package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// ---------------------------------------------------------------------------
// Funding transfers (ledger + on-chain legs)
// ---------------------------------------------------------------------------

// InvestmentFundingTransferRepository persists funding legs.
type InvestmentFundingTransferRepository struct {
	db *sqlx.DB
}

// NewInvestmentFundingTransferRepository creates a funding transfer repository.
func NewInvestmentFundingTransferRepository(db *sqlx.DB) *InvestmentFundingTransferRepository {
	return &InvestmentFundingTransferRepository{db: db}
}

// The idempotency key is nullable in the schema (partial unique index), so it
// is normalised to an empty string on read and mapped to NULL on write.
const investmentFundingTransferColumns = `id, user_id, enrollment_id, direction, amount_usd, asset,
	source_account, destination_account_id, ledger_transaction_id, onchain_tx_ref, status,
	COALESCE(idempotency_key, '') AS idempotency_key, failure_reason, created_at, updated_at`

// Create inserts a funding leg.
func (r *InvestmentFundingTransferRepository) Create(ctx context.Context, transfer *entities.InvestmentFundingTransfer) error {
	if transfer == nil {
		return fmt.Errorf("investment funding transfer is nil")
	}
	if transfer.ID == uuid.Nil {
		transfer.ID = uuid.New()
	}
	transfer.CreatedAt = investmentTimeOrNow(transfer.CreatedAt)
	transfer.UpdatedAt = investmentTimeOrNow(transfer.UpdatedAt)

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO investment_funding_transfers (id, user_id, enrollment_id, direction, amount_usd, asset,
			source_account, destination_account_id, ledger_transaction_id, onchain_tx_ref, status,
			idempotency_key, failure_reason, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
		transfer.ID, transfer.UserID, transfer.EnrollmentID, transfer.Direction, transfer.AmountUSD,
		transfer.Asset, transfer.SourceAccount, transfer.DestinationAccountID, transfer.LedgerTransactionID,
		transfer.OnchainTxRef, transfer.Status, investmentNullString(transfer.IdempotencyKey),
		transfer.FailureReason, transfer.CreatedAt, transfer.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create investment funding transfer: %w", err)
	}
	return nil
}

// Update writes the mutable funding fields.
func (r *InvestmentFundingTransferRepository) Update(ctx context.Context, transfer *entities.InvestmentFundingTransfer) error {
	if transfer == nil {
		return fmt.Errorf("investment funding transfer is nil")
	}
	transfer.UpdatedAt = investmentTimeOrNow(transfer.UpdatedAt)

	_, err := r.db.ExecContext(ctx, `
		UPDATE investment_funding_transfers SET
			ledger_transaction_id = $2, onchain_tx_ref = $3, status = $4, failure_reason = $5, updated_at = $6
		WHERE id = $1`,
		transfer.ID, transfer.LedgerTransactionID, transfer.OnchainTxRef, transfer.Status,
		transfer.FailureReason, transfer.UpdatedAt)
	if err != nil {
		return fmt.Errorf("update investment funding transfer: %w", err)
	}
	return nil
}

// FindByIdempotencyKey returns the transfer recorded for a key, or (nil, nil).
func (r *InvestmentFundingTransferRepository) FindByIdempotencyKey(ctx context.Context, key string) (*entities.InvestmentFundingTransfer, error) {
	if key == "" {
		return nil, nil
	}
	transfer := &entities.InvestmentFundingTransfer{}
	err := r.db.GetContext(ctx, transfer,
		`SELECT `+investmentFundingTransferColumns+`
		 FROM investment_funding_transfers WHERE idempotency_key = $1`, key)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find investment funding transfer by key: %w", err)
	}
	return transfer, nil
}

// ListByEnrollment returns an enrollment's funding history.
func (r *InvestmentFundingTransferRepository) ListByEnrollment(ctx context.Context, enrollmentID uuid.UUID) ([]*entities.InvestmentFundingTransfer, error) {
	transfers := make([]*entities.InvestmentFundingTransfer, 0)
	err := r.db.SelectContext(ctx, &transfers,
		`SELECT `+investmentFundingTransferColumns+`
		 FROM investment_funding_transfers WHERE enrollment_id = $1 ORDER BY created_at DESC`, enrollmentID)
	if err != nil {
		return nil, fmt.Errorf("list investment funding transfers: %w", err)
	}
	return transfers, nil
}

// SumDepositsSince totals deposited USD for a user since a timestamp, used for
// the daily volume limit.
func (r *InvestmentFundingTransferRepository) SumDepositsSince(ctx context.Context, userID uuid.UUID, since time.Time) (decimal.Decimal, error) {
	var total decimal.Decimal
	err := r.db.GetContext(ctx, &total, `
		SELECT COALESCE(SUM(amount_usd), 0)
		FROM investment_funding_transfers
		WHERE user_id = $1 AND direction = 'deposit' AND created_at >= $2`, userID, since)
	if err != nil {
		return decimal.Zero, fmt.Errorf("sum investment deposits: %w", err)
	}
	return total, nil
}

// ---------------------------------------------------------------------------
// Signature requests (two-stage owner authorizations)
// ---------------------------------------------------------------------------

// InvestmentSignatureRequestRepository persists owner authorization flows.
type InvestmentSignatureRequestRepository struct {
	db *sqlx.DB
}

// NewInvestmentSignatureRequestRepository creates a signature request repository.
func NewInvestmentSignatureRequestRepository(db *sqlx.DB) *InvestmentSignatureRequestRepository {
	return &InvestmentSignatureRequestRepository{db: db}
}

const investmentSignatureRequestColumns = `id, user_id, enrollment_id, flow, provider_flow_id,
	payload, status, expires_at, failure_reason, created_at, updated_at`

// Create inserts a signature request.
func (r *InvestmentSignatureRequestRepository) Create(ctx context.Context, request *entities.InvestmentSignatureRequest) error {
	if request == nil {
		return fmt.Errorf("investment signature request is nil")
	}
	if request.ID == uuid.Nil {
		request.ID = uuid.New()
	}
	request.CreatedAt = investmentTimeOrNow(request.CreatedAt)
	request.UpdatedAt = investmentTimeOrNow(request.UpdatedAt)

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO investment_signature_requests (id, user_id, enrollment_id, flow, provider_flow_id,
			payload, status, expires_at, failure_reason, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		request.ID, request.UserID, request.EnrollmentID, request.Flow, request.ProviderFlowID,
		investmentNonNullJSON(request.Payload, "{}"), request.Status, request.ExpiresAt,
		request.FailureReason, request.CreatedAt, request.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create investment signature request: %w", err)
	}
	return nil
}

// Update writes the mutable signature request fields.
func (r *InvestmentSignatureRequestRepository) Update(ctx context.Context, request *entities.InvestmentSignatureRequest) error {
	if request == nil {
		return fmt.Errorf("investment signature request is nil")
	}
	request.UpdatedAt = investmentTimeOrNow(request.UpdatedAt)

	_, err := r.db.ExecContext(ctx, `
		UPDATE investment_signature_requests SET
			provider_flow_id = $2, payload = $3, status = $4, expires_at = $5, failure_reason = $6, updated_at = $7
		WHERE id = $1`,
		request.ID, request.ProviderFlowID, investmentNonNullJSON(request.Payload, "{}"),
		request.Status, request.ExpiresAt, request.FailureReason, request.UpdatedAt)
	if err != nil {
		return fmt.Errorf("update investment signature request: %w", err)
	}
	return nil
}

// FindByFlow resolves a signature request by its flow + provider flow id.
func (r *InvestmentSignatureRequestRepository) FindByFlow(ctx context.Context, flow, providerFlowID string) (*entities.InvestmentSignatureRequest, error) {
	request := &entities.InvestmentSignatureRequest{}
	err := r.db.GetContext(ctx, request,
		`SELECT `+investmentSignatureRequestColumns+`
		 FROM investment_signature_requests WHERE flow = $1 AND provider_flow_id = $2`, flow, providerFlowID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find investment signature request: %w", err)
	}
	return request, nil
}

// ---------------------------------------------------------------------------
// Confirmations (explicit user "yes")
// ---------------------------------------------------------------------------

// InvestmentConfirmationRepository persists confirmation tokens.
type InvestmentConfirmationRepository struct {
	db *sqlx.DB
}

// NewInvestmentConfirmationRepository creates a confirmation repository.
func NewInvestmentConfirmationRepository(db *sqlx.DB) *InvestmentConfirmationRepository {
	return &InvestmentConfirmationRepository{db: db}
}

const investmentConfirmationColumns = `id, user_id, token, action, action_hash, payload, verdict,
	preview, consumed_at, expires_at, created_at`

// Create inserts a confirmation token.
func (r *InvestmentConfirmationRepository) Create(ctx context.Context, confirmation *entities.InvestmentConfirmation) error {
	if confirmation == nil {
		return fmt.Errorf("investment confirmation is nil")
	}
	if confirmation.ID == uuid.Nil {
		confirmation.ID = uuid.New()
	}
	confirmation.CreatedAt = investmentTimeOrNow(confirmation.CreatedAt)

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO investment_confirmations (id, user_id, token, action, action_hash, payload, verdict,
			preview, consumed_at, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		confirmation.ID, confirmation.UserID, confirmation.Token, confirmation.Action,
		confirmation.ActionHash, investmentNonNullJSON(confirmation.Payload, "{}"),
		string(confirmation.Verdict), confirmation.Preview, confirmation.ConsumedAt,
		confirmation.ExpiresAt, confirmation.CreatedAt)
	if err != nil {
		return fmt.Errorf("create investment confirmation: %w", err)
	}
	return nil
}

// GetByToken returns a confirmation by its token, or (nil, nil) when absent.
func (r *InvestmentConfirmationRepository) GetByToken(ctx context.Context, token string) (*entities.InvestmentConfirmation, error) {
	confirmation := &entities.InvestmentConfirmation{}
	err := r.db.GetContext(ctx, confirmation,
		`SELECT `+investmentConfirmationColumns+` FROM investment_confirmations WHERE token = $1`, token)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get investment confirmation: %w", err)
	}
	return confirmation, nil
}

// FindPending returns the newest live confirmation for the same user, action
// and payload hash, or (nil, nil) when none exists.
func (r *InvestmentConfirmationRepository) FindPending(ctx context.Context, userID uuid.UUID, action, actionHash string) (*entities.InvestmentConfirmation, error) {
	confirmation := &entities.InvestmentConfirmation{}
	err := r.db.GetContext(ctx, confirmation,
		`SELECT `+investmentConfirmationColumns+` FROM investment_confirmations
		 WHERE user_id = $1 AND action = $2 AND action_hash = $3
		   AND consumed_at IS NULL AND expires_at > NOW()
		 ORDER BY created_at DESC LIMIT 1`, userID, action, actionHash)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find pending investment confirmation: %w", err)
	}
	return confirmation, nil
}

// Consume marks a confirmation used. It fails when the token was already
// consumed (or does not exist), so two concurrent replays cannot both proceed.
func (r *InvestmentConfirmationRepository) Consume(ctx context.Context, token string, now time.Time) error {
	result, err := r.db.ExecContext(ctx,
		`UPDATE investment_confirmations SET consumed_at = $2 WHERE token = $1 AND consumed_at IS NULL`,
		token, now.UTC())
	if err != nil {
		return fmt.Errorf("consume investment confirmation: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("consume investment confirmation rows: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("investment confirmation already consumed or unknown")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Provider operations (async work mirror)
// ---------------------------------------------------------------------------

// InvestmentOperationRepository mirrors provider async operations.
type InvestmentOperationRepository struct {
	db *sqlx.DB
}

// NewInvestmentOperationRepository creates an operation repository.
func NewInvestmentOperationRepository(db *sqlx.DB) *InvestmentOperationRepository {
	return &InvestmentOperationRepository{db: db}
}

const investmentOperationColumns = `id, user_id, enrollment_id, execution_id, provider_operation_id,
	kind, state, error, finished_at, created_at, updated_at`

// Upsert inserts or advances an operation, keyed by provider_operation_id.
func (r *InvestmentOperationRepository) Upsert(ctx context.Context, operation *entities.GliderOperation) error {
	if operation == nil {
		return fmt.Errorf("investment operation is nil")
	}
	if operation.ID == uuid.Nil {
		operation.ID = uuid.New()
	}
	operation.CreatedAt = investmentTimeOrNow(operation.CreatedAt)
	operation.UpdatedAt = investmentTimeOrNow(operation.UpdatedAt)

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO investment_glider_operations (id, user_id, enrollment_id, execution_id,
			provider_operation_id, kind, state, error, finished_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (provider_operation_id) DO UPDATE SET
			state = EXCLUDED.state,
			error = EXCLUDED.error,
			finished_at = EXCLUDED.finished_at,
			updated_at = EXCLUDED.updated_at`,
		operation.ID, operation.UserID, operation.EnrollmentID, operation.ExecutionID,
		operation.ProviderOperationID, operation.Kind, operation.State, operation.Error,
		operation.FinishedAt, operation.CreatedAt, operation.UpdatedAt)
	if err != nil {
		return fmt.Errorf("upsert investment operation: %w", err)
	}
	return nil
}

// ListOpen returns operations still being worked by the provider.
func (r *InvestmentOperationRepository) ListOpen(ctx context.Context, limit int) ([]*entities.GliderOperation, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	operations := make([]*entities.GliderOperation, 0)
	err := r.db.SelectContext(ctx, &operations,
		`SELECT `+investmentOperationColumns+`
		 FROM investment_glider_operations
		 WHERE state NOT IN ('completed', 'failed', 'cancelled')
		 ORDER BY created_at ASC
		 LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list open investment operations: %w", err)
	}
	return operations, nil
}

// GetByProviderID returns an operation by provider id, or (nil, nil).
func (r *InvestmentOperationRepository) GetByProviderID(ctx context.Context, providerOperationID string) (*entities.GliderOperation, error) {
	operation := &entities.GliderOperation{}
	err := r.db.GetContext(ctx, operation,
		`SELECT `+investmentOperationColumns+`
		 FROM investment_glider_operations WHERE provider_operation_id = $1`, providerOperationID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get investment operation: %w", err)
	}
	return operation, nil
}

// ---------------------------------------------------------------------------
// Audit trail
// ---------------------------------------------------------------------------

// InvestmentAuditRepository records the investment event trail.
type InvestmentAuditRepository struct {
	db *sqlx.DB
}

// NewInvestmentAuditRepository creates an audit repository.
func NewInvestmentAuditRepository(db *sqlx.DB) *InvestmentAuditRepository {
	return &InvestmentAuditRepository{db: db}
}

const investmentAuditColumns = `id, user_id, event_type, actor, strategy_id, strategy_version,
	enrollment_id, execution_id, payload, correlation_id, created_at`

// Record inserts one audit event.
func (r *InvestmentAuditRepository) Record(ctx context.Context, event *entities.InvestmentAuditEvent) error {
	if event == nil {
		return fmt.Errorf("investment audit event is nil")
	}
	if event.ID == uuid.Nil {
		event.ID = uuid.New()
	}
	event.CreatedAt = investmentTimeOrNow(event.CreatedAt)

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO investment_audit_events (id, user_id, event_type, actor, strategy_id, strategy_version,
			enrollment_id, execution_id, payload, correlation_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		event.ID, event.UserID, event.EventType, string(event.Actor), event.StrategyID,
		event.StrategyVersion, event.EnrollmentID, event.ExecutionID,
		investmentNonNullJSON(event.Payload, "{}"), event.CorrelationID, event.CreatedAt)
	if err != nil {
		return fmt.Errorf("record investment audit event: %w", err)
	}
	return nil
}

// ListByUser returns a user's recent audit events, newest first.
func (r *InvestmentAuditRepository) ListByUser(ctx context.Context, userID uuid.UUID, limit int) ([]*entities.InvestmentAuditEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	events := make([]*entities.InvestmentAuditEvent, 0)
	err := r.db.SelectContext(ctx, &events,
		`SELECT `+investmentAuditColumns+`
		 FROM investment_audit_events WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list investment audit events: %w", err)
	}
	return events, nil
}
