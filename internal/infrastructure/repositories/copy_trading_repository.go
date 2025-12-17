package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// CopyTradingRepository handles database operations for copy trading
type CopyTradingRepository struct {
	db *sqlx.DB
}

// NewCopyTradingRepository creates a new copy trading repository
func NewCopyTradingRepository(db *sqlx.DB) *CopyTradingRepository {
	return &CopyTradingRepository{db: db}
}

// === Conductor Operations ===

// GetActiveConductors returns all active conductors with pagination and sorting
func (r *CopyTradingRepository) GetActiveConductors(ctx context.Context, limit, offset int, sortBy string) ([]*entities.Conductor, int, error) {
	orderClause := "followers_count DESC"
	switch sortBy {
	case "return":
		orderClause = "total_return DESC"
	case "aum":
		orderClause = "source_aum DESC"
	case "win_rate":
		orderClause = "win_rate DESC"
	}

	query := fmt.Sprintf(`
		SELECT id, user_id, display_name, bio, avatar_url, status, fee_rate, source_aum,
		       total_return, win_rate, max_drawdown, sharpe_ratio, total_trades,
		       followers_count, min_draft_amount, is_verified, verified_at, last_trade_at,
		       created_at, updated_at
		FROM conductors
		WHERE status = 'active'
		ORDER BY %s
		LIMIT $1 OFFSET $2
	`, orderClause)

	var conductors []*entities.Conductor
	if err := r.db.SelectContext(ctx, &conductors, query, limit, offset); err != nil {
		return nil, 0, fmt.Errorf("failed to get conductors: %w", err)
	}

	var total int
	countQuery := `SELECT COUNT(*) FROM conductors WHERE status = 'active'`
	if err := r.db.GetContext(ctx, &total, countQuery); err != nil {
		return nil, 0, fmt.Errorf("failed to count conductors: %w", err)
	}

	return conductors, total, nil
}

// GetConductorByID returns a conductor by ID
func (r *CopyTradingRepository) GetConductorByID(ctx context.Context, id uuid.UUID) (*entities.Conductor, error) {
	query := `
		SELECT id, user_id, display_name, bio, avatar_url, status, fee_rate, source_aum,
		       total_return, win_rate, max_drawdown, sharpe_ratio, total_trades,
		       followers_count, min_draft_amount, is_verified, verified_at, last_trade_at,
		       created_at, updated_at
		FROM conductors WHERE id = $1
	`
	var conductor entities.Conductor
	if err := r.db.GetContext(ctx, &conductor, query, id); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get conductor: %w", err)
	}
	return &conductor, nil
}

// GetConductorByUserID returns a conductor by user ID
func (r *CopyTradingRepository) GetConductorByUserID(ctx context.Context, userID uuid.UUID) (*entities.Conductor, error) {
	query := `
		SELECT id, user_id, display_name, bio, avatar_url, status, fee_rate, source_aum,
		       total_return, win_rate, max_drawdown, sharpe_ratio, total_trades,
		       followers_count, min_draft_amount, is_verified, verified_at, last_trade_at,
		       created_at, updated_at
		FROM conductors WHERE user_id = $1
	`
	var conductor entities.Conductor
	if err := r.db.GetContext(ctx, &conductor, query, userID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get conductor by user: %w", err)
	}
	return &conductor, nil
}

// UpdateConductorAUM updates the conductor's AUM
func (r *CopyTradingRepository) UpdateConductorAUM(ctx context.Context, conductorID uuid.UUID, aum decimal.Decimal) error {
	query := `UPDATE conductors SET source_aum = $1, updated_at = NOW() WHERE id = $2`
	_, err := r.db.ExecContext(ctx, query, aum, conductorID)
	return err
}

// IncrementFollowersCount increments the followers count
func (r *CopyTradingRepository) IncrementFollowersCount(ctx context.Context, conductorID uuid.UUID, delta int) error {
	query := `UPDATE conductors SET followers_count = followers_count + $1, updated_at = NOW() WHERE id = $2`
	_, err := r.db.ExecContext(ctx, query, delta, conductorID)
	return err
}

// === Draft Operations ===

// CreateDraft creates a new draft (copy relationship)
func (r *CopyTradingRepository) CreateDraft(ctx context.Context, draft *entities.Draft) error {
	query := `
		INSERT INTO drafts (id, drafter_id, conductor_id, status, allocated_capital, current_aum,
		                    start_value, total_profit_loss, total_fees_paid, copy_ratio, auto_adjust,
		                    created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`
	_, err := r.db.ExecContext(ctx, query,
		draft.ID, draft.DrafterID, draft.ConductorID, draft.Status, draft.AllocatedCapital,
		draft.CurrentAUM, draft.StartValue, draft.TotalProfitLoss, draft.TotalFeesPaid,
		draft.CopyRatio, draft.AutoAdjust, draft.CreatedAt, draft.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to create draft: %w", err)
	}
	return nil
}

// GetDraftByID returns a draft by ID
func (r *CopyTradingRepository) GetDraftByID(ctx context.Context, id uuid.UUID) (*entities.Draft, error) {
	query := `
		SELECT id, drafter_id, conductor_id, status, allocated_capital, current_aum,
		       start_value, total_profit_loss, total_fees_paid, copy_ratio, auto_adjust,
		       created_at, updated_at, paused_at, unlinked_at
		FROM drafts WHERE id = $1
	`
	var draft entities.Draft
	if err := r.db.GetContext(ctx, &draft, query, id); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get draft: %w", err)
	}
	return &draft, nil
}

// GetDraftsByDrafterID returns all drafts for a user
func (r *CopyTradingRepository) GetDraftsByDrafterID(ctx context.Context, drafterID uuid.UUID) ([]*entities.Draft, error) {
	query := `
		SELECT d.id, d.drafter_id, d.conductor_id, d.status, d.allocated_capital, d.current_aum,
		       d.start_value, d.total_profit_loss, d.total_fees_paid, d.copy_ratio, d.auto_adjust,
		       d.created_at, d.updated_at, d.paused_at, d.unlinked_at
		FROM drafts d
		WHERE d.drafter_id = $1
		ORDER BY d.created_at DESC
	`
	var drafts []*entities.Draft
	if err := r.db.SelectContext(ctx, &drafts, query, drafterID); err != nil {
		return nil, fmt.Errorf("failed to get drafts: %w", err)
	}
	return drafts, nil
}

// GetActiveDraftsByConductorID returns all active drafts for a conductor
func (r *CopyTradingRepository) GetActiveDraftsByConductorID(ctx context.Context, conductorID uuid.UUID) ([]*entities.Draft, error) {
	query := `
		SELECT id, drafter_id, conductor_id, status, allocated_capital, current_aum,
		       start_value, total_profit_loss, total_fees_paid, copy_ratio, auto_adjust,
		       created_at, updated_at, paused_at, unlinked_at
		FROM drafts
		WHERE conductor_id = $1 AND status = 'active'
	`
	var drafts []*entities.Draft
	if err := r.db.SelectContext(ctx, &drafts, query, conductorID); err != nil {
		return nil, fmt.Errorf("failed to get active drafts: %w", err)
	}
	return drafts, nil
}

// GetExistingDraft checks if a draft already exists between drafter and conductor
func (r *CopyTradingRepository) GetExistingDraft(ctx context.Context, drafterID, conductorID uuid.UUID) (*entities.Draft, error) {
	query := `
		SELECT id, drafter_id, conductor_id, status, allocated_capital, current_aum,
		       start_value, total_profit_loss, total_fees_paid, copy_ratio, auto_adjust,
		       created_at, updated_at, paused_at, unlinked_at
		FROM drafts
		WHERE drafter_id = $1 AND conductor_id = $2 AND status NOT IN ('unlinked')
	`
	var draft entities.Draft
	if err := r.db.GetContext(ctx, &draft, query, drafterID, conductorID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to check existing draft: %w", err)
	}
	return &draft, nil
}

// UpdateDraftStatus updates the status of a draft
func (r *CopyTradingRepository) UpdateDraftStatus(ctx context.Context, draftID uuid.UUID, status entities.DraftStatus) error {
	now := time.Now().UTC()
	var query string
	switch status {
	case entities.DraftStatusPaused:
		query = `UPDATE drafts SET status = $1, paused_at = $2, updated_at = $2 WHERE id = $3`
	case entities.DraftStatusUnlinked:
		query = `UPDATE drafts SET status = $1, unlinked_at = $2, updated_at = $2 WHERE id = $3`
	default:
		query = `UPDATE drafts SET status = $1, updated_at = $2 WHERE id = $3`
	}
	_, err := r.db.ExecContext(ctx, query, status, now, draftID)
	return err
}

// UpdateDraftCapital updates the allocated capital of a draft
func (r *CopyTradingRepository) UpdateDraftCapital(ctx context.Context, draftID uuid.UUID, newCapital decimal.Decimal) error {
	query := `UPDATE drafts SET allocated_capital = $1, updated_at = NOW() WHERE id = $2`
	_, err := r.db.ExecContext(ctx, query, newCapital, draftID)
	return err
}

// UpdateDraftAUM updates the current AUM and profit/loss of a draft
func (r *CopyTradingRepository) UpdateDraftAUM(ctx context.Context, draftID uuid.UUID, currentAUM, profitLoss decimal.Decimal) error {
	query := `UPDATE drafts SET current_aum = $1, total_profit_loss = $2, updated_at = NOW() WHERE id = $3`
	_, err := r.db.ExecContext(ctx, query, currentAUM, profitLoss, draftID)
	return err
}

// === Signal Operations ===

// CreateSignal creates a new signal
func (r *CopyTradingRepository) CreateSignal(ctx context.Context, signal *entities.Signal) error {
	query := `
		INSERT INTO signals (id, conductor_id, asset_ticker, asset_name, signal_type, side,
		                     base_quantity, base_price, base_value, conductor_aum_at_signal,
		                     order_id, status, processed_count, failed_count, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
	`
	_, err := r.db.ExecContext(ctx, query,
		signal.ID, signal.ConductorID, signal.AssetTicker, signal.AssetName, signal.SignalType,
		signal.Side, signal.BaseQuantity, signal.BasePrice, signal.BaseValue,
		signal.ConductorAUMAtSignal, signal.OrderID, signal.Status, signal.ProcessedCount,
		signal.FailedCount, signal.CreatedAt)
	return err
}

// GetSignalByID returns a signal by ID
func (r *CopyTradingRepository) GetSignalByID(ctx context.Context, id uuid.UUID) (*entities.Signal, error) {
	query := `
		SELECT id, conductor_id, asset_ticker, asset_name, signal_type, side, base_quantity,
		       base_price, base_value, conductor_aum_at_signal, order_id, status,
		       processed_count, failed_count, created_at, completed_at
		FROM signals WHERE id = $1
	`
	var signal entities.Signal
	if err := r.db.GetContext(ctx, &signal, query, id); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get signal: %w", err)
	}
	return &signal, nil
}

// GetPendingSignals returns signals that need processing
func (r *CopyTradingRepository) GetPendingSignals(ctx context.Context, limit int) ([]*entities.Signal, error) {
	query := `
		SELECT id, conductor_id, asset_ticker, asset_name, signal_type, side, base_quantity,
		       base_price, base_value, conductor_aum_at_signal, order_id, status,
		       processed_count, failed_count, created_at, completed_at
		FROM signals
		WHERE status IN ('pending', 'processing')
		ORDER BY created_at ASC
		LIMIT $1
	`
	var signals []*entities.Signal
	if err := r.db.SelectContext(ctx, &signals, query, limit); err != nil {
		return nil, fmt.Errorf("failed to get pending signals: %w", err)
	}
	return signals, nil
}

// UpdateSignalStatus updates the status of a signal
func (r *CopyTradingRepository) UpdateSignalStatus(ctx context.Context, signalID uuid.UUID, status entities.SignalStatus, processedCount, failedCount int) error {
	var query string
	if status == entities.SignalStatusCompleted || status == entities.SignalStatusFailed {
		query = `UPDATE signals SET status = $1, processed_count = $2, failed_count = $3, completed_at = NOW() WHERE id = $4`
	} else {
		query = `UPDATE signals SET status = $1, processed_count = $2, failed_count = $3 WHERE id = $4`
	}
	_, err := r.db.ExecContext(ctx, query, status, processedCount, failedCount, signalID)
	return err
}

// GetSignalsByConductor returns recent signals for a conductor
func (r *CopyTradingRepository) GetSignalsByConductor(ctx context.Context, conductorID uuid.UUID, limit int) ([]*entities.Signal, error) {
	query := `
		SELECT id, conductor_id, asset_ticker, asset_name, signal_type, side, base_quantity,
		       base_price, base_value, conductor_aum_at_signal, order_id, status,
		       processed_count, failed_count, created_at, completed_at
		FROM signals
		WHERE conductor_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`
	var signals []*entities.Signal
	if err := r.db.SelectContext(ctx, &signals, query, conductorID, limit); err != nil {
		return nil, fmt.Errorf("failed to get conductor signals: %w", err)
	}
	return signals, nil
}

// === Signal Execution Log Operations ===

// CreateExecutionLog creates a new execution log entry
func (r *CopyTradingRepository) CreateExecutionLog(ctx context.Context, log *entities.SignalExecutionLog) error {
	query := `
		INSERT INTO signal_execution_logs (id, draft_id, signal_id, executed_quantity, executed_price,
		                                   executed_value, status, fee_applied, error_message, order_id,
		                                   idempotency_key, created_at, executed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (idempotency_key) DO NOTHING
	`
	_, err := r.db.ExecContext(ctx, query,
		log.ID, log.DraftID, log.SignalID, log.ExecutedQuantity, log.ExecutedPrice,
		log.ExecutedValue, log.Status, log.FeeApplied, log.ErrorMessage, log.OrderID,
		log.IdempotencyKey, log.CreatedAt, log.ExecutedAt)
	return err
}

// GetExecutionLogByIdempotencyKey checks if an execution already exists
func (r *CopyTradingRepository) GetExecutionLogByIdempotencyKey(ctx context.Context, key string) (*entities.SignalExecutionLog, error) {
	query := `
		SELECT id, draft_id, signal_id, executed_quantity, executed_price, executed_value,
		       status, fee_applied, error_message, order_id, idempotency_key, created_at, executed_at
		FROM signal_execution_logs WHERE idempotency_key = $1
	`
	var log entities.SignalExecutionLog
	if err := r.db.GetContext(ctx, &log, query, key); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get execution log: %w", err)
	}
	return &log, nil
}

// GetExecutionLogsByDraft returns execution logs for a draft
func (r *CopyTradingRepository) GetExecutionLogsByDraft(ctx context.Context, draftID uuid.UUID, limit int) ([]*entities.SignalExecutionLog, error) {
	query := `
		SELECT id, draft_id, signal_id, executed_quantity, executed_price, executed_value,
		       status, fee_applied, error_message, order_id, idempotency_key, created_at, executed_at
		FROM signal_execution_logs
		WHERE draft_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`
	var logs []*entities.SignalExecutionLog
	if err := r.db.SelectContext(ctx, &logs, query, draftID, limit); err != nil {
		return nil, fmt.Errorf("failed to get execution logs: %w", err)
	}
	return logs, nil
}

// === Conductor Application Operations ===

// CreateApplication creates a new conductor application
func (r *CopyTradingRepository) CreateApplication(ctx context.Context, app *entities.ConductorApplication) error {
	query := `
		INSERT INTO conductor_applications (id, user_id, display_name, bio, investment_strategy,
		                                    experience, social_links, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`
	_, err := r.db.ExecContext(ctx, query,
		app.ID, app.UserID, app.DisplayName, app.Bio, app.InvestmentStrategy,
		app.Experience, app.SocialLinks, app.Status, app.CreatedAt, app.UpdatedAt)
	return err
}

// GetApplicationByID returns an application by ID
func (r *CopyTradingRepository) GetApplicationByID(ctx context.Context, id uuid.UUID) (*entities.ConductorApplication, error) {
	query := `
		SELECT id, user_id, display_name, bio, investment_strategy, experience, social_links,
		       status, reviewed_by, reviewed_at, rejection_reason, created_at, updated_at
		FROM conductor_applications WHERE id = $1
	`
	var app entities.ConductorApplication
	if err := r.db.GetContext(ctx, &app, query, id); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get application: %w", err)
	}
	return &app, nil
}

// GetApplicationByUserID returns an application by user ID
func (r *CopyTradingRepository) GetApplicationByUserID(ctx context.Context, userID uuid.UUID) (*entities.ConductorApplication, error) {
	query := `
		SELECT id, user_id, display_name, bio, investment_strategy, experience, social_links,
		       status, reviewed_by, reviewed_at, rejection_reason, created_at, updated_at
		FROM conductor_applications WHERE user_id = $1
		ORDER BY created_at DESC LIMIT 1
	`
	var app entities.ConductorApplication
	if err := r.db.GetContext(ctx, &app, query, userID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get application: %w", err)
	}
	return &app, nil
}

// GetPendingApplications returns pending applications with pagination
func (r *CopyTradingRepository) GetPendingApplications(ctx context.Context, limit, offset int) ([]*entities.ConductorApplication, int, error) {
	query := `
		SELECT id, user_id, display_name, bio, investment_strategy, experience, social_links,
		       status, reviewed_by, reviewed_at, rejection_reason, created_at, updated_at
		FROM conductor_applications WHERE status = 'pending'
		ORDER BY created_at ASC
		LIMIT $1 OFFSET $2
	`
	var apps []*entities.ConductorApplication
	if err := r.db.SelectContext(ctx, &apps, query, limit, offset); err != nil {
		return nil, 0, fmt.Errorf("failed to get applications: %w", err)
	}

	var total int
	countQuery := `SELECT COUNT(*) FROM conductor_applications WHERE status = 'pending'`
	if err := r.db.GetContext(ctx, &total, countQuery); err != nil {
		return nil, 0, fmt.Errorf("failed to count applications: %w", err)
	}

	return apps, total, nil
}

// UpdateApplicationStatus updates the status of an application
func (r *CopyTradingRepository) UpdateApplicationStatus(ctx context.Context, id uuid.UUID, status entities.ConductorApplicationStatus, reviewerID *uuid.UUID, reason string) error {
	now := time.Now().UTC()
	query := `
		UPDATE conductor_applications
		SET status = $1, reviewed_by = $2, reviewed_at = $3, rejection_reason = $4, updated_at = $3
		WHERE id = $5
	`
	_, err := r.db.ExecContext(ctx, query, status, reviewerID, now, reason, id)
	return err
}

// CreateConductor creates a new conductor
func (r *CopyTradingRepository) CreateConductor(ctx context.Context, conductor *entities.Conductor) error {
	query := `
		INSERT INTO conductors (id, user_id, display_name, bio, avatar_url, status, fee_rate,
		                        source_aum, total_return, win_rate, max_drawdown, sharpe_ratio,
		                        total_trades, followers_count, min_draft_amount, is_verified,
		                        verified_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
	`
	_, err := r.db.ExecContext(ctx, query,
		conductor.ID, conductor.UserID, conductor.DisplayName, conductor.Bio, conductor.AvatarURL,
		conductor.Status, conductor.FeeRate, conductor.SourceAUM, conductor.TotalReturn,
		conductor.WinRate, conductor.MaxDrawdown, conductor.SharpeRatio, conductor.TotalTrades,
		conductor.FollowersCount, conductor.MinDraftAmount, conductor.IsVerified,
		conductor.VerifiedAt, conductor.CreatedAt, conductor.UpdatedAt)
	return err
}

// === Track Operations ===

// CreateTrack creates a new track
func (r *CopyTradingRepository) CreateTrack(ctx context.Context, track *entities.Track) error {
	query := `
		INSERT INTO tracks (id, conductor_id, name, description, risk_level, is_active,
		                    followers_count, total_return, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`
	_, err := r.db.ExecContext(ctx, query,
		track.ID, track.ConductorID, track.Name, track.Description, track.RiskLevel,
		track.IsActive, track.FollowersCount, track.TotalReturn, track.CreatedAt, track.UpdatedAt)
	return err
}

// GetTrackByID returns a track by ID
func (r *CopyTradingRepository) GetTrackByID(ctx context.Context, id uuid.UUID) (*entities.Track, error) {
	query := `
		SELECT id, conductor_id, name, description, risk_level, is_active,
		       followers_count, total_return, created_at, updated_at
		FROM tracks WHERE id = $1
	`
	var track entities.Track
	if err := r.db.GetContext(ctx, &track, query, id); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get track: %w", err)
	}
	return &track, nil
}

// GetTracksByConductorID returns all tracks for a conductor
func (r *CopyTradingRepository) GetTracksByConductorID(ctx context.Context, conductorID uuid.UUID) ([]*entities.Track, error) {
	query := `
		SELECT id, conductor_id, name, description, risk_level, is_active,
		       followers_count, total_return, created_at, updated_at
		FROM tracks WHERE conductor_id = $1
		ORDER BY created_at DESC
	`
	var tracks []*entities.Track
	if err := r.db.SelectContext(ctx, &tracks, query, conductorID); err != nil {
		return nil, fmt.Errorf("failed to get tracks: %w", err)
	}
	return tracks, nil
}

// GetActiveTracks returns active tracks with pagination
func (r *CopyTradingRepository) GetActiveTracks(ctx context.Context, limit, offset int) ([]*entities.Track, int, error) {
	query := `
		SELECT id, conductor_id, name, description, risk_level, is_active,
		       followers_count, total_return, created_at, updated_at
		FROM tracks WHERE is_active = TRUE
		ORDER BY followers_count DESC
		LIMIT $1 OFFSET $2
	`
	var tracks []*entities.Track
	if err := r.db.SelectContext(ctx, &tracks, query, limit, offset); err != nil {
		return nil, 0, fmt.Errorf("failed to get tracks: %w", err)
	}

	var total int
	countQuery := `SELECT COUNT(*) FROM tracks WHERE is_active = TRUE`
	if err := r.db.GetContext(ctx, &total, countQuery); err != nil {
		return nil, 0, fmt.Errorf("failed to count tracks: %w", err)
	}

	return tracks, total, nil
}

// UpdateTrack updates a track
func (r *CopyTradingRepository) UpdateTrack(ctx context.Context, track *entities.Track) error {
	query := `
		UPDATE tracks SET name = $1, description = $2, risk_level = $3, is_active = $4, updated_at = $5
		WHERE id = $6
	`
	_, err := r.db.ExecContext(ctx, query, track.Name, track.Description, track.RiskLevel, track.IsActive, track.UpdatedAt, track.ID)
	return err
}

// DeleteTrack deletes a track (soft delete via is_active)
func (r *CopyTradingRepository) DeleteTrack(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE tracks SET is_active = FALSE, updated_at = NOW() WHERE id = $1`
	_, err := r.db.ExecContext(ctx, query, id)
	return err
}

// === Track Allocation Operations ===

// CreateTrackAllocations creates allocations for a track
func (r *CopyTradingRepository) CreateTrackAllocations(ctx context.Context, allocations []entities.TrackAllocation) error {
	query := `
		INSERT INTO track_allocations (id, track_id, asset_ticker, asset_name, target_weight, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	for _, alloc := range allocations {
		_, err := r.db.ExecContext(ctx, query,
			alloc.ID, alloc.TrackID, alloc.AssetTicker, alloc.AssetName, alloc.TargetWeight, alloc.CreatedAt, alloc.UpdatedAt)
		if err != nil {
			return fmt.Errorf("failed to create allocation: %w", err)
		}
	}
	return nil
}

// GetTrackAllocations returns allocations for a track
func (r *CopyTradingRepository) GetTrackAllocations(ctx context.Context, trackID uuid.UUID) ([]entities.TrackAllocation, error) {
	query := `
		SELECT id, track_id, asset_ticker, asset_name, target_weight, created_at, updated_at
		FROM track_allocations WHERE track_id = $1
		ORDER BY target_weight DESC
	`
	var allocations []entities.TrackAllocation
	if err := r.db.SelectContext(ctx, &allocations, query, trackID); err != nil {
		return nil, fmt.Errorf("failed to get allocations: %w", err)
	}
	return allocations, nil
}

// DeleteTrackAllocations deletes all allocations for a track
func (r *CopyTradingRepository) DeleteTrackAllocations(ctx context.Context, trackID uuid.UUID) error {
	query := `DELETE FROM track_allocations WHERE track_id = $1`
	_, err := r.db.ExecContext(ctx, query, trackID)
	return err
}
