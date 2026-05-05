package repositories

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

type AutomationRepository struct {
	db *sqlx.DB
}

func NewAutomationRepository(db *sqlx.DB) *AutomationRepository {
	return &AutomationRepository{db: db}
}

func (r *AutomationRepository) Create(ctx context.Context, a *entities.MiriamAutomation) error {
	query := `INSERT INTO miriam_automations (id, user_id, name, description, trigger_type, trigger_config, action_type, action_config, is_active, max_triggers_per_day, cooldown_minutes, savings_goal_id, obligation_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, NOW(), NOW())`
	_, err := r.db.ExecContext(ctx, query, a.ID, a.UserID, a.Name, a.Description, a.TriggerType, a.TriggerConfig, a.ActionType, a.ActionConfig, a.IsActive, a.MaxTriggersPerDay, a.CooldownMinutes, a.SavingsGoalID, a.ObligationID)
	return err
}

func (r *AutomationRepository) GetByID(ctx context.Context, userID, id uuid.UUID) (*entities.MiriamAutomation, error) {
	var a entities.MiriamAutomation
	err := r.db.GetContext(ctx, &a, `SELECT * FROM miriam_automations WHERE id = $1 AND user_id = $2`, id, userID)
	return &a, err
}

func (r *AutomationRepository) ListByUser(ctx context.Context, userID uuid.UUID) ([]entities.MiriamAutomation, error) {
	var list []entities.MiriamAutomation
	err := r.db.SelectContext(ctx, &list, `SELECT * FROM miriam_automations WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	return list, err
}

func (r *AutomationRepository) ListActive(ctx context.Context) ([]entities.MiriamAutomation, error) {
	var list []entities.MiriamAutomation
	err := r.db.SelectContext(ctx, &list, `SELECT * FROM miriam_automations WHERE is_active = true`)
	return list, err
}

func (r *AutomationRepository) ListActiveByTrigger(ctx context.Context, triggerType string) ([]entities.MiriamAutomation, error) {
	var list []entities.MiriamAutomation
	err := r.db.SelectContext(ctx, &list, `SELECT * FROM miriam_automations WHERE is_active = true AND trigger_type = $1`, triggerType)
	return list, err
}

func (r *AutomationRepository) Update(ctx context.Context, a *entities.MiriamAutomation) error {
	query := `UPDATE miriam_automations SET name=$1, description=$2, trigger_type=$3, trigger_config=$4, action_type=$5, action_config=$6, is_active=$7, max_triggers_per_day=$8, cooldown_minutes=$9, savings_goal_id=$10, obligation_id=$11, updated_at=NOW() WHERE id=$12 AND user_id=$13`
	_, err := r.db.ExecContext(ctx, query, a.Name, a.Description, a.TriggerType, a.TriggerConfig, a.ActionType, a.ActionConfig, a.IsActive, a.MaxTriggersPerDay, a.CooldownMinutes, a.SavingsGoalID, a.ObligationID, a.ID, a.UserID)
	return err
}

func (r *AutomationRepository) Delete(ctx context.Context, userID, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM miriam_automations WHERE id = $1 AND user_id = $2`, id, userID)
	return err
}

func (r *AutomationRepository) MarkTriggered(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `UPDATE miriam_automations SET last_triggered_at = NOW(), trigger_count = trigger_count + 1, updated_at = NOW() WHERE id = $1`, id)
	return err
}

func (r *AutomationRepository) GetTodayTriggerCount(ctx context.Context, id uuid.UUID) (int, error) {
	var count int
	err := r.db.GetContext(ctx, &count, `SELECT COUNT(*) FROM miriam_automation_logs WHERE automation_id = $1 AND executed_at >= $2`, id, time.Now().UTC().Truncate(24*time.Hour))
	return count, err
}

func (r *AutomationRepository) LogExecution(ctx context.Context, log *entities.MiriamAutomationLog) error {
	query := `INSERT INTO miriam_automation_logs (id, automation_id, user_id, status, trigger_data, result_data, error_message, executed_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	_, err := r.db.ExecContext(ctx, query, log.ID, log.AutomationID, log.UserID, log.Status, log.TriggerData, log.ResultData, log.ErrorMessage, log.ExecutedAt)
	return err
}

func (r *AutomationRepository) GetLogs(ctx context.Context, userID uuid.UUID, limit int) ([]entities.MiriamAutomationLog, error) {
	var logs []entities.MiriamAutomationLog
	err := r.db.SelectContext(ctx, &logs, `SELECT * FROM miriam_automation_logs WHERE user_id = $1 ORDER BY executed_at DESC LIMIT $2`, userID, limit)
	return logs, err
}

// PendingCardUnfreeze represents a scheduled card unfreeze operation.
type PendingCardUnfreeze struct {
	ID           uuid.UUID  `db:"id"`
	UserID       uuid.UUID  `db:"user_id"`
	CardID       uuid.UUID  `db:"card_id"`
	AutomationID uuid.UUID  `db:"automation_id"`
	UnfreezeAt   time.Time  `db:"unfreeze_at"`
	Attempts     int        `db:"attempts"`
	LastError    *string    `db:"last_error"`
	CreatedAt    time.Time  `db:"created_at"`
}

func (r *AutomationRepository) InsertPendingUnfreeze(ctx context.Context, userID, cardID, automationID uuid.UUID, unfreezeAt time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO pending_card_unfreezes (user_id, card_id, automation_id, unfreeze_at) VALUES ($1, $2, $3, $4)`,
		userID, cardID, automationID, unfreezeAt)
	return err
}

func (r *AutomationRepository) GetDueUnfreezes(ctx context.Context, now time.Time, limit int) ([]PendingCardUnfreeze, error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	var list []PendingCardUnfreeze
	if err := tx.SelectContext(ctx, &list,
		`SELECT * FROM pending_card_unfreezes WHERE unfreeze_at <= $1 AND attempts < 5 ORDER BY unfreeze_at LIMIT $2 FOR UPDATE SKIP LOCKED`,
		now, limit); err != nil {
		tx.Rollback()
		return nil, err
	}
	// Batch delete all claimed rows in a single query.
	if len(list) > 0 {
		ids := make([]uuid.UUID, len(list))
		for i, job := range list {
			ids[i] = job.ID
		}
		query, args, err := sqlx.In(`DELETE FROM pending_card_unfreezes WHERE id IN (?)`, ids)
		if err != nil {
			tx.Rollback()
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, tx.Rebind(query), args...); err != nil {
			tx.Rollback()
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return list, nil
}

func (r *AutomationRepository) ReinsertFailedUnfreeze(ctx context.Context, job PendingCardUnfreeze, errMsg string) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO pending_card_unfreezes (user_id, card_id, automation_id, unfreeze_at, attempts, last_error)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (card_id) DO UPDATE SET attempts = EXCLUDED.attempts, last_error = EXCLUDED.last_error, unfreeze_at = EXCLUDED.unfreeze_at`,
		job.UserID, job.CardID, job.AutomationID, job.UnfreezeAt, job.Attempts+1, errMsg)
	return err
}
