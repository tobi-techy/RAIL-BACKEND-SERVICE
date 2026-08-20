package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// UserGoalRepository persists free-standing savings goals surfaced by Miriam.
// Distinct from SharedGoalRepository (multi-user) and the existing automation-
// bound goals table.
type UserGoalRepository struct {
	db *sqlx.DB
}

func NewUserGoalRepository(db *sqlx.DB) *UserGoalRepository {
	return &UserGoalRepository{db: db}
}

// Create inserts a new user goal and returns it with CreatedAt populated.
func (r *UserGoalRepository) Create(ctx context.Context, goal *entities.UserGoal) error {
	if goal.ID == uuid.Nil {
		goal.ID = uuid.New()
	}
	if goal.Source == "" {
		goal.Source = entities.GoalSourceManual
	}
	if goal.Category == "" {
		goal.Category = entities.GoalCategoryFreeform
	}
	if goal.TargetCurrency == "" {
		goal.TargetCurrency = "USD"
	}
	payload, err := json.Marshal(struct{}{})
	if err != nil {
		payload = []byte("{}")
	}

	query := `
		INSERT INTO user_goals (
			id, user_id, name, target_amount, target_currency, current_amount,
			deadline, baby_step, category, source, created_at
		) VALUES (
			:id, :user_id, :name, :target_amount, :target_currency, :current_amount,
			:deadline, :baby_step, :category, :source, NOW()
		)
		RETURNING created_at`
	stmt, err := r.db.PrepareNamedContext(ctx, query)
	if err != nil {
		return fmt.Errorf("prepare create user goal: %w", err)
	}
	defer stmt.Close()

	if err := stmt.GetContext(ctx, goal, goal); err != nil {
		return fmt.Errorf("create user goal: %w", err)
	}
	_ = payload // reserved for future use; not yet persisted
	return nil
}

// GetByID returns a goal by ID, scoped to the owning user.
func (r *UserGoalRepository) GetByID(ctx context.Context, userID, goalID uuid.UUID) (*entities.UserGoal, error) {
	var goal entities.UserGoal
	err := r.db.GetContext(ctx, &goal,
		`SELECT * FROM user_goals WHERE id = $1 AND user_id = $2`,
		goalID, userID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, err
		}
		return nil, fmt.Errorf("get user goal: %w", err)
	}
	return &goal, nil
}

// ListByUser returns goals for a user. includeArchived=false returns active
// goals only (default); true returns all (active + archived + completed).
func (r *UserGoalRepository) ListByUser(ctx context.Context, userID uuid.UUID, includeArchived bool) ([]entities.UserGoal, error) {
	q := `SELECT * FROM user_goals WHERE user_id = $1`
	if !includeArchived {
		q += ` AND completed_at IS NULL AND archived_at IS NULL`
	}
	q += ` ORDER BY created_at DESC`
	var goals []entities.UserGoal
	if err := r.db.SelectContext(ctx, &goals, q, userID); err != nil {
		return nil, fmt.Errorf("list user goals: %w", err)
	}
	return goals, nil
}

// ListActiveByStep returns active goals for a user on a specific Baby Step.
// Used by the goal_progress worker to evaluate step-advance and per-step
// milestone events.
func (r *UserGoalRepository) ListActiveByStep(ctx context.Context, userID uuid.UUID, step int) ([]entities.UserGoal, error) {
	var goals []entities.UserGoal
	err := r.db.SelectContext(ctx, &goals,
		`SELECT * FROM user_goals
		 WHERE user_id = $1 AND baby_step = $2
		   AND completed_at IS NULL AND archived_at IS NULL
		 ORDER BY created_at ASC`,
		userID, step)
	if err != nil {
		return nil, fmt.Errorf("list active user goals by step: %w", err)
	}
	return goals, nil
}

// UpdateProgress updates current_amount on a goal. The caller should also emit
// a progress event via AppendProgressEvent to maintain audit trail.
func (r *UserGoalRepository) UpdateProgress(ctx context.Context, userID, goalID uuid.UUID, currentAmount decimal.Decimal) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE user_goals
		 SET current_amount = $1,
		     completed_at = CASE WHEN $1 >= target_amount AND completed_at IS NULL
		                         THEN NOW() ELSE completed_at END
		 WHERE id = $2 AND user_id = $3
		   AND archived_at IS NULL`,
		currentAmount, goalID, userID)
	if err != nil {
		return fmt.Errorf("update user goal progress: %w", err)
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// Complete marks the goal as completed (idempotent).
func (r *UserGoalRepository) Complete(ctx context.Context, userID, goalID uuid.UUID) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE user_goals
		 SET completed_at = COALESCE(completed_at, NOW())
		 WHERE id = $1 AND user_id = $2`,
		goalID, userID)
	if err != nil {
		return fmt.Errorf("complete user goal: %w", err)
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// Archive soft-deletes a goal (idempotent).
func (r *UserGoalRepository) Archive(ctx context.Context, userID, goalID uuid.UUID) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE user_goals
		 SET archived_at = COALESCE(archived_at, NOW())
		 WHERE id = $1 AND user_id = $2`,
		goalID, userID)
	if err != nil {
		return fmt.Errorf("archive user goal: %w", err)
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// AppendProgressEvent writes an audit row for a goal progress event.
func (r *UserGoalRepository) AppendProgressEvent(ctx context.Context, event *entities.UserGoalProgressEvent) error {
	if event.ID == uuid.Nil {
		event.ID = uuid.New()
	}
	if event.UserID == uuid.Nil || event.GoalID == uuid.Nil || event.Kind == "" {
		return fmt.Errorf("append progress event: user_id, goal_id, kind are required")
	}
	if event.Payload == nil || len(event.Payload) == 0 {
		event.Payload = json.RawMessage("{}")
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO user_goal_progress_events
		 (id, user_id, goal_id, kind, pct, current_amount, target_amount, payload, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		event.ID, event.UserID, event.GoalID, event.Kind,
		event.Pct, event.CurrentAmount, event.TargetAmount, event.Payload, event.CreatedAt)
	if err != nil {
		return fmt.Errorf("append progress event: %w", err)
	}
	return nil
}

// HasAnyGoal returns true when the user has at least one row in user_goals.
// Used by the onboarding seed to avoid double-seeding the Baby Steps ladder.
func (r *UserGoalRepository) HasAnyGoal(ctx context.Context, userID uuid.UUID) (bool, error) {
	var n int
	err := r.db.GetContext(ctx, &n,
		`SELECT COUNT(*) FROM user_goals WHERE user_id = $1 LIMIT 1`,
		userID)
	if err != nil {
		return false, fmt.Errorf("has any user goal: %w", err)
	}
	return n > 0, nil
}

// ListAllActiveUsers returns distinct user IDs with at least one active goal.
// Used by the goal_progress worker to fan out without scanning the whole users
// table.
func (r *UserGoalRepository) ListAllActiveUsers(ctx context.Context) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	err := r.db.SelectContext(ctx, &ids,
		`SELECT DISTINCT user_id FROM user_goals
		 WHERE completed_at IS NULL AND archived_at IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("list active goal users: %w", err)
	}
	return ids, nil
}

// filterArgs is a small helper used by ListByUser for future expansion.
// Currently unused but kept to satisfy the lint expectation that helper
// imports stay referenced.
var _ = strings.Join
