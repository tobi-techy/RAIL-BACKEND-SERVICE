package repositories

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// ErrAuthorizationNotFound means a withdrawal authorization was unknown,
// already consumed, expired, or issued for a different enrollment/amount. The
// vault domain translates it into a fail-closed refusal.
var ErrAuthorizationNotFound = errors.New("vault withdrawal authorization not found")

// VaultRepository persists the retirement vault's own records: the vault, its
// contribution lots (cost basis), valuation snapshots, penalty events,
// single-use withdrawal authorizations and on-ramp audit rows.
//
// Every read here succeeds without the investment provider being reachable,
// which is what lets the vault answer "what is this worth and when can I touch
// it" even when Glider is down.
type VaultRepository struct {
	db *sqlx.DB
}

// NewVaultRepository creates a vault repository.
func NewVaultRepository(db *sqlx.DB) *VaultRepository {
	return &VaultRepository{db: db}
}

const vaultColumns = `id, user_id, name, tier, status, retirement_age, min_lock_years,
	funded_at, unlock_date, auto_contribution_pct, glider_enrollment_id, created_at, updated_at`

// CreateVault inserts a new vault.
func (r *VaultRepository) CreateVault(ctx context.Context, vault *entities.RetirementVault) error {
	if vault == nil {
		return fmt.Errorf("vault is nil")
	}
	if vault.ID == uuid.Nil {
		vault.ID = uuid.New()
	}
	now := time.Now().UTC()
	if vault.CreatedAt.IsZero() {
		vault.CreatedAt = now
	}
	vault.UpdatedAt = now

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO retirement_vaults (id, user_id, name, tier, status, retirement_age, min_lock_years,
			funded_at, unlock_date, auto_contribution_pct, glider_enrollment_id, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		vault.ID, vault.UserID, vault.Name, string(vault.Tier), string(vault.Status), vault.RetirementAge,
		vault.MinLockYears, vault.FundedAt, vault.UnlockDate, vault.AutoContributionPct,
		vault.GliderEnrollmentID, vault.CreatedAt, vault.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create retirement vault: %w", err)
	}
	return nil
}

// UpdateVault writes the mutable vault fields.
func (r *VaultRepository) UpdateVault(ctx context.Context, vault *entities.RetirementVault) error {
	if vault == nil {
		return fmt.Errorf("vault is nil")
	}
	vault.UpdatedAt = time.Now().UTC()
	_, err := r.db.ExecContext(ctx, `
		UPDATE retirement_vaults SET
			name = $2, tier = $3, status = $4, retirement_age = $5, min_lock_years = $6,
			funded_at = $7, unlock_date = $8, auto_contribution_pct = $9, glider_enrollment_id = $10,
			updated_at = $11
		WHERE id = $1`,
		vault.ID, vault.Name, string(vault.Tier), string(vault.Status), vault.RetirementAge, vault.MinLockYears,
		vault.FundedAt, vault.UnlockDate, vault.AutoContributionPct, vault.GliderEnrollmentID, vault.UpdatedAt)
	if err != nil {
		return fmt.Errorf("update retirement vault: %w", err)
	}
	return nil
}

// GetVaultByID returns a vault by id, or (nil, nil) when absent.
func (r *VaultRepository) GetVaultByID(ctx context.Context, id uuid.UUID) (*entities.RetirementVault, error) {
	vault := &entities.RetirementVault{}
	err := r.db.GetContext(ctx, vault, `SELECT `+vaultColumns+` FROM retirement_vaults WHERE id = $1`, id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get retirement vault: %w", err)
	}
	return vault, nil
}

// GetActiveVaultByUser returns a user's active vault, or (nil, nil).
func (r *VaultRepository) GetActiveVaultByUser(ctx context.Context, userID uuid.UUID) (*entities.RetirementVault, error) {
	vault := &entities.RetirementVault{}
	err := r.db.GetContext(ctx, vault,
		`SELECT `+vaultColumns+` FROM retirement_vaults WHERE user_id = $1 AND status = 'active'`, userID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get active retirement vault: %w", err)
	}
	return vault, nil
}

// GetVaultByEnrollment resolves the vault a portfolio belongs to, or (nil, nil).
func (r *VaultRepository) GetVaultByEnrollment(ctx context.Context, enrollmentID uuid.UUID) (*entities.RetirementVault, error) {
	vault := &entities.RetirementVault{}
	err := r.db.GetContext(ctx, vault,
		`SELECT `+vaultColumns+` FROM retirement_vaults WHERE glider_enrollment_id = $1`, enrollmentID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get vault by enrollment: %w", err)
	}
	return vault, nil
}

const vaultLotColumns = `id, vault_id, user_id, amount_usd, remaining_usd, source_account,
	ledger_transaction_id, funding_transfer_id, onramp_transfer_id, ngn_amount, fx_rate,
	acquired_at, status, idempotency_key, created_at`

// CreateLot inserts a contribution lot.
func (r *VaultRepository) CreateLot(ctx context.Context, lot *entities.VaultContributionLot) error {
	if lot == nil {
		return fmt.Errorf("lot is nil")
	}
	if lot.ID == uuid.Nil {
		lot.ID = uuid.New()
	}
	if lot.AcquiredAt.IsZero() {
		lot.AcquiredAt = time.Now().UTC()
	}
	if lot.CreatedAt.IsZero() {
		lot.CreatedAt = lot.AcquiredAt
	}
	if lot.Status == "" {
		lot.Status = entities.VaultLotOpen
	}
	var key *string
	if lot.IdempotencyKey != "" {
		key = &lot.IdempotencyKey
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO vault_contribution_lots (id, vault_id, user_id, amount_usd, remaining_usd, source_account,
			ledger_transaction_id, funding_transfer_id, onramp_transfer_id, ngn_amount, fx_rate,
			acquired_at, status, idempotency_key, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		lot.ID, lot.VaultID, lot.UserID, lot.AmountUSD, lot.RemainingUSD, lot.SourceAccount,
		lot.LedgerTransactionID, lot.FundingTransferID, lot.OnrampTransferID, lot.NGNAmount, lot.FXRate,
		lot.AcquiredAt, string(lot.Status), key, lot.CreatedAt)
	if err != nil {
		return fmt.Errorf("create vault contribution lot: %w", err)
	}
	return nil
}

// UpdateLot writes a lot's remaining basis and status.
func (r *VaultRepository) UpdateLot(ctx context.Context, lot *entities.VaultContributionLot) error {
	if lot == nil {
		return fmt.Errorf("lot is nil")
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE vault_contribution_lots SET remaining_usd = $2, status = $3 WHERE id = $1`,
		lot.ID, lot.RemainingUSD, string(lot.Status))
	if err != nil {
		return fmt.Errorf("update vault contribution lot: %w", err)
	}
	return nil
}

// GetLotByKey returns the lot for an idempotency key, or (nil, nil).
func (r *VaultRepository) GetLotByKey(ctx context.Context, key string) (*entities.VaultContributionLot, error) {
	lot := &entities.VaultContributionLot{}
	err := r.db.GetContext(ctx, lot,
		`SELECT `+vaultLotColumns+` FROM vault_contribution_lots WHERE idempotency_key = $1`, key)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get vault lot by key: %w", err)
	}
	return lot, nil
}

// ListOpenLots returns a vault's open lots, oldest first (FIFO).
func (r *VaultRepository) ListOpenLots(ctx context.Context, vaultID uuid.UUID) ([]*entities.VaultContributionLot, error) {
	lots := []*entities.VaultContributionLot{}
	err := r.db.SelectContext(ctx, &lots,
		`SELECT `+vaultLotColumns+` FROM vault_contribution_lots
		 WHERE vault_id = $1 AND status = 'open' ORDER BY acquired_at ASC`, vaultID)
	if err != nil {
		return nil, fmt.Errorf("list open vault lots: %w", err)
	}
	return lots, nil
}

// ListLots returns a vault's lots for the activity view.
func (r *VaultRepository) ListLots(ctx context.Context, vaultID uuid.UUID, limit int) ([]*entities.VaultContributionLot, error) {
	if limit <= 0 {
		limit = 50
	}
	lots := []*entities.VaultContributionLot{}
	err := r.db.SelectContext(ctx, &lots,
		`SELECT `+vaultLotColumns+` FROM vault_contribution_lots
		 WHERE vault_id = $1 ORDER BY acquired_at DESC LIMIT $2`, vaultID, limit)
	if err != nil {
		return nil, fmt.Errorf("list vault lots: %w", err)
	}
	return lots, nil
}

// ---------------------------------------------------------------------------
// Snapshots
// ---------------------------------------------------------------------------

const vaultSnapshotColumns = `id, vault_id, user_id, principal_usd, earnings_usd, market_value_usd,
	source, as_of, created_at`

// CreateSnapshot records a valuation point.
func (r *VaultRepository) CreateSnapshot(ctx context.Context, snapshot *entities.VaultEarningsSnapshot) error {
	if snapshot == nil {
		return fmt.Errorf("snapshot is nil")
	}
	if snapshot.ID == uuid.Nil {
		snapshot.ID = uuid.New()
	}
	if snapshot.AsOf.IsZero() {
		snapshot.AsOf = time.Now().UTC()
	}
	if snapshot.CreatedAt.IsZero() {
		snapshot.CreatedAt = snapshot.AsOf
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO vault_earnings_snapshots (id, vault_id, user_id, principal_usd, earnings_usd,
			market_value_usd, source, as_of, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		snapshot.ID, snapshot.VaultID, snapshot.UserID, snapshot.PrincipalUSD, snapshot.EarningsUSD,
		snapshot.MarketValueUSD, snapshot.Source, snapshot.AsOf, snapshot.CreatedAt)
	if err != nil {
		return fmt.Errorf("create vault snapshot: %w", err)
	}
	return nil
}

// LatestSnapshot returns the most recent snapshot, or (nil, nil).
func (r *VaultRepository) LatestSnapshot(ctx context.Context, vaultID uuid.UUID) (*entities.VaultEarningsSnapshot, error) {
	snapshot := &entities.VaultEarningsSnapshot{}
	err := r.db.GetContext(ctx, snapshot,
		`SELECT `+vaultSnapshotColumns+` FROM vault_earnings_snapshots
		 WHERE vault_id = $1 ORDER BY as_of DESC LIMIT 1`, vaultID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get latest vault snapshot: %w", err)
	}
	return snapshot, nil
}

// ---------------------------------------------------------------------------
// Penalty events
// ---------------------------------------------------------------------------

const vaultPenaltyColumns = `id, vault_id, user_id, withdrawal_id, execution_id,
	earnings_withdrawn_usd, penalty_usd, rate, status, reason, ledger_transaction_id, created_at, updated_at`

// CreatePenaltyEvent inserts a pending penalty.
func (r *VaultRepository) CreatePenaltyEvent(ctx context.Context, event *entities.VaultPenaltyEvent) error {
	if event == nil {
		return fmt.Errorf("penalty event is nil")
	}
	if event.ID == uuid.Nil {
		event.ID = uuid.New()
	}
	now := time.Now().UTC()
	if event.CreatedAt.IsZero() {
		event.CreatedAt = now
	}
	event.UpdatedAt = now
	if event.Status == "" {
		event.Status = entities.VaultPenaltyPending
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO vault_penalty_events (id, vault_id, user_id, withdrawal_id, execution_id,
			earnings_withdrawn_usd, penalty_usd, rate, status, reason, ledger_transaction_id, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		event.ID, event.VaultID, event.UserID, event.WithdrawalID, event.ExecutionID,
		event.EarningsWithdrawnUSD, event.PenaltyUSD, event.Rate, string(event.Status), event.Reason,
		event.LedgerTransactionID, event.CreatedAt, event.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create vault penalty event: %w", err)
	}
	return nil
}

// UpdatePenaltyEvent writes a penalty's status and settlement link.
func (r *VaultRepository) UpdatePenaltyEvent(ctx context.Context, event *entities.VaultPenaltyEvent) error {
	if event == nil {
		return fmt.Errorf("penalty event is nil")
	}
	event.UpdatedAt = time.Now().UTC()
	_, err := r.db.ExecContext(ctx, `
		UPDATE vault_penalty_events SET status = $2, execution_id = $3, ledger_transaction_id = $4,
			reason = $5, updated_at = $6
		WHERE id = $1`,
		event.ID, string(event.Status), event.ExecutionID, event.LedgerTransactionID, event.Reason, event.UpdatedAt)
	if err != nil {
		return fmt.Errorf("update vault penalty event: %w", err)
	}
	return nil
}

// GetPenaltyEvent returns a penalty by id, or (nil, nil).
func (r *VaultRepository) GetPenaltyEvent(ctx context.Context, id uuid.UUID) (*entities.VaultPenaltyEvent, error) {
	event := &entities.VaultPenaltyEvent{}
	err := r.db.GetContext(ctx, event,
		`SELECT `+vaultPenaltyColumns+` FROM vault_penalty_events WHERE id = $1`, id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get vault penalty event: %w", err)
	}
	return event, nil
}

// GetPenaltyEventByExecution returns the penalty bound to an execution.
func (r *VaultRepository) GetPenaltyEventByExecution(ctx context.Context, executionID uuid.UUID) (*entities.VaultPenaltyEvent, error) {
	event := &entities.VaultPenaltyEvent{}
	err := r.db.GetContext(ctx, event,
		`SELECT `+vaultPenaltyColumns+` FROM vault_penalty_events WHERE execution_id = $1
		 ORDER BY created_at DESC LIMIT 1`, executionID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get vault penalty event by execution: %w", err)
	}
	return event, nil
}

// ---------------------------------------------------------------------------
// Withdrawal authorizations
// ---------------------------------------------------------------------------

const vaultAuthColumns = `id, vault_id, user_id, enrollment_id, "key", gross_usd, penalty_usd,
	net_usd, plan, status, execution_id, expires_at, consumed_at, created_at`

// CreateAuthorization mints a single-use withdrawal authorization.
func (r *VaultRepository) CreateAuthorization(ctx context.Context, auth *entities.VaultWithdrawalAuthorization) error {
	if auth == nil {
		return fmt.Errorf("authorization is nil")
	}
	if auth.ID == uuid.Nil {
		auth.ID = uuid.New()
	}
	if auth.CreatedAt.IsZero() {
		auth.CreatedAt = time.Now().UTC()
	}
	if auth.Status == "" {
		auth.Status = entities.VaultAuthorizationIssued
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO vault_withdrawal_authorizations (id, vault_id, user_id, enrollment_id, "key",
			gross_usd, penalty_usd, net_usd, plan, status, execution_id, expires_at, consumed_at, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		auth.ID, auth.VaultID, auth.UserID, auth.EnrollmentID, auth.Key,
		auth.GrossUSD, auth.PenaltyUSD, auth.NetUSD, auth.Plan, string(auth.Status),
		auth.ExecutionID, auth.ExpiresAt, auth.ConsumedAt, auth.CreatedAt)
	if err != nil {
		return fmt.Errorf("create vault withdrawal authorization: %w", err)
	}
	return nil
}

// ConsumeAuthorization atomically claims an issued, unexpired authorization for
// exactly this enrollment and amount. The database resolves the race: a
// concurrent replay of the same key matches zero rows and is refused.
func (r *VaultRepository) ConsumeAuthorization(
	ctx context.Context,
	key string,
	enrollmentID uuid.UUID,
	grossUSD decimal.Decimal,
	now time.Time,
) (*entities.VaultWithdrawalAuthorization, error) {
	auth := &entities.VaultWithdrawalAuthorization{}
	err := r.db.GetContext(ctx, auth, `
		UPDATE vault_withdrawal_authorizations
		SET status = 'consumed', consumed_at = $4
		WHERE "key" = $1 AND enrollment_id = $2 AND status = 'issued'
		  AND expires_at > $4 AND gross_usd = $3
		RETURNING `+vaultAuthColumns,
		key, enrollmentID, grossUSD, now)
	if err == sql.ErrNoRows {
		return nil, ErrAuthorizationNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("consume vault withdrawal authorization: %w", err)
	}
	return auth, nil
}

// AttachAuthorizationExecution binds an authorization to the execution it
// produced, so settlement can find it later.
func (r *VaultRepository) AttachAuthorizationExecution(ctx context.Context, key string, executionID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE vault_withdrawal_authorizations SET execution_id = $2 WHERE "key" = $1`, key, executionID)
	if err != nil {
		return fmt.Errorf("attach vault authorization execution: %w", err)
	}
	return nil
}

// HasIssuedAuthorization reports whether a live authorization exists for an
// enrollment — i.e. a withdrawal is already in flight.
func (r *VaultRepository) HasIssuedAuthorization(ctx context.Context, enrollmentID uuid.UUID, now time.Time) (bool, error) {
	var exists bool
	err := r.db.GetContext(ctx, &exists,
		`SELECT EXISTS (
			SELECT 1 FROM vault_withdrawal_authorizations
			WHERE enrollment_id = $1 AND status = 'issued' AND expires_at > $2
		)`, enrollmentID, now)
	if err != nil {
		return false, fmt.Errorf("check issued vault authorization: %w", err)
	}
	return exists, nil
}

// ExpireAuthorization releases an issued authorization that will never be used.
func (r *VaultRepository) ExpireAuthorization(ctx context.Context, key string, now time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE vault_withdrawal_authorizations SET status = 'expired', consumed_at = $2
		WHERE "key" = $1 AND status = 'issued'`, key, now)
	if err != nil {
		return fmt.Errorf("expire vault authorization: %w", err)
	}
	return nil
}

// GetAuthorizationByExecution returns the authorization bound to an execution.
func (r *VaultRepository) GetAuthorizationByExecution(ctx context.Context, executionID uuid.UUID) (*entities.VaultWithdrawalAuthorization, error) {
	auth := &entities.VaultWithdrawalAuthorization{}
	err := r.db.GetContext(ctx, auth,
		`SELECT `+vaultAuthColumns+` FROM vault_withdrawal_authorizations
		 WHERE execution_id = $1 ORDER BY created_at DESC LIMIT 1`, executionID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get vault authorization by execution: %w", err)
	}
	return auth, nil
}

// ---------------------------------------------------------------------------
// On-ramp transfers
// ---------------------------------------------------------------------------

const vaultOnrampColumns = `id, user_id, vault_id, provider, direction, ngn_amount, fx_rate,
	usdc_amount, provider_ref, circle_tx_ref, status, idempotency_key, created_at, updated_at`

// CreateOnrampTransfer inserts an on-ramp audit row.
func (r *VaultRepository) CreateOnrampTransfer(ctx context.Context, transfer *entities.VaultOnrampTransfer) error {
	if transfer == nil {
		return fmt.Errorf("onramp transfer is nil")
	}
	if transfer.ID == uuid.Nil {
		transfer.ID = uuid.New()
	}
	now := time.Now().UTC()
	if transfer.CreatedAt.IsZero() {
		transfer.CreatedAt = now
	}
	transfer.UpdatedAt = now
	var key *string
	if transfer.IdempotencyKey != "" {
		key = &transfer.IdempotencyKey
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO vault_onramp_transfers (id, user_id, vault_id, provider, direction, ngn_amount,
			fx_rate, usdc_amount, provider_ref, circle_tx_ref, status, idempotency_key, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		transfer.ID, transfer.UserID, transfer.VaultID, string(transfer.Provider), transfer.Direction,
		transfer.NGNAmount, transfer.FXRate, transfer.USDCAmount, transfer.ProviderRef, transfer.CircleTxRef,
		transfer.Status, key, transfer.CreatedAt, transfer.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create vault onramp transfer: %w", err)
	}
	return nil
}

// UpdateOnrampTransfer writes an on-ramp transfer's mutable fields.
func (r *VaultRepository) UpdateOnrampTransfer(ctx context.Context, transfer *entities.VaultOnrampTransfer) error {
	if transfer == nil {
		return fmt.Errorf("onramp transfer is nil")
	}
	transfer.UpdatedAt = time.Now().UTC()
	_, err := r.db.ExecContext(ctx, `
		UPDATE vault_onramp_transfers SET fx_rate = $2, usdc_amount = $3, provider_ref = $4,
			circle_tx_ref = $5, status = $6, updated_at = $7
		WHERE id = $1`,
		transfer.ID, transfer.FXRate, transfer.USDCAmount, transfer.ProviderRef, transfer.CircleTxRef,
		transfer.Status, transfer.UpdatedAt)
	if err != nil {
		return fmt.Errorf("update vault onramp transfer: %w", err)
	}
	return nil
}

// FindOnrampByIdempotencyKey returns an on-ramp transfer by key, or (nil, nil).
func (r *VaultRepository) FindOnrampByIdempotencyKey(ctx context.Context, key string) (*entities.VaultOnrampTransfer, error) {
	transfer := &entities.VaultOnrampTransfer{}
	err := r.db.GetContext(ctx, transfer,
		`SELECT `+vaultOnrampColumns+` FROM vault_onramp_transfers WHERE idempotency_key = $1`, key)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find vault onramp transfer: %w", err)
	}
	return transfer, nil
}
