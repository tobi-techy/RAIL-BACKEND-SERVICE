package repositories

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// MilestoneRepository handles investment milestone persistence
type MilestoneRepository struct {
	db *sqlx.DB
}

// NewMilestoneRepository creates a new milestone repository
func NewMilestoneRepository(db *sqlx.DB) *MilestoneRepository {
	return &MilestoneRepository{db: db}
}

// Create creates a new milestone record
func (r *MilestoneRepository) Create(ctx context.Context, milestone *entities.InvestmentMilestone) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO investment_milestones (id, user_id, type, amount, achieved_at, celebrated, celebrated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		milestone.ID, milestone.UserID, milestone.Type, milestone.Amount,
		milestone.AchievedAt, milestone.Celebrated, milestone.CelebratedAt)
	return err
}

// GetByUserID retrieves all milestones for a user
func (r *MilestoneRepository) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.InvestmentMilestone, error) {
	var milestones []*entities.InvestmentMilestone
	err := r.db.SelectContext(ctx, &milestones,
		`SELECT * FROM investment_milestones WHERE user_id = $1 ORDER BY achieved_at DESC`, userID)
	return milestones, err
}

// GetUncelebrated retrieves uncelebrated milestones for a user
func (r *MilestoneRepository) GetUncelebrated(ctx context.Context, userID uuid.UUID) ([]*entities.InvestmentMilestone, error) {
	var milestones []*entities.InvestmentMilestone
	err := r.db.SelectContext(ctx, &milestones,
		`SELECT * FROM investment_milestones WHERE user_id = $1 AND celebrated = false ORDER BY achieved_at`, userID)
	return milestones, err
}

// MarkCelebrated marks a milestone as celebrated
func (r *MilestoneRepository) MarkCelebrated(ctx context.Context, id uuid.UUID) error {
	now := time.Now()
	_, err := r.db.ExecContext(ctx,
		`UPDATE investment_milestones SET celebrated = true, celebrated_at = $1 WHERE id = $2`,
		now, id)
	return err
}

// HasAchieved checks if user has achieved a specific milestone
func (r *MilestoneRepository) HasAchieved(ctx context.Context, userID uuid.UUID, milestoneType entities.MilestoneType, amount decimal.Decimal) (bool, error) {
	var count int
	err := r.db.GetContext(ctx, &count,
		`SELECT COUNT(*) FROM investment_milestones WHERE user_id = $1 AND type = $2 AND amount = $3`,
		userID, milestoneType, amount)
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	return count > 0, nil
}
