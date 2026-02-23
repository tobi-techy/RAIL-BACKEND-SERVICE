package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// InvestmentRulesRepository handles investment rules persistence
type InvestmentRulesRepository struct {
	db *sqlx.DB
}

// NewInvestmentRulesRepository creates a new investment rules repository
func NewInvestmentRulesRepository(db *sqlx.DB) *InvestmentRulesRepository {
	return &InvestmentRulesRepository{db: db}
}

// investmentRulesRow represents the database row
type investmentRulesRow struct {
	ID                     uuid.UUID       `db:"id"`
	UserID                 uuid.UUID       `db:"user_id"`
	AgeBasedAllocation     []byte          `db:"age_based_allocation"`
	RebalancingConfig      []byte          `db:"rebalancing_config"`
	DRIPConfig             []byte          `db:"drip_config"`
	WithdrawalCooling      []byte          `db:"withdrawal_cooling"`
	RoundUpMultiplier      int             `db:"round_up_multiplier"`
	MilestoneNotifications bool            `db:"milestone_notifications"`
	CreatedAt              time.Time       `db:"created_at"`
	UpdatedAt              time.Time       `db:"updated_at"`
}

// Create creates a new investment rules config
func (r *InvestmentRulesRepository) Create(ctx context.Context, config *entities.InvestmentRulesConfig) error {
	row, err := r.toRow(config)
	if err != nil {
		return err
	}

	_, err = r.db.ExecContext(ctx,
		`INSERT INTO investment_rules_config (id, user_id, age_based_allocation, rebalancing_config, 
		 drip_config, withdrawal_cooling, round_up_multiplier, milestone_notifications, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		row.ID, row.UserID, row.AgeBasedAllocation, row.RebalancingConfig,
		row.DRIPConfig, row.WithdrawalCooling, row.RoundUpMultiplier, row.MilestoneNotifications,
		row.CreatedAt, row.UpdatedAt)
	return err
}

// GetByUserID retrieves investment rules for a user
func (r *InvestmentRulesRepository) GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.InvestmentRulesConfig, error) {
	var row investmentRulesRow
	err := r.db.GetContext(ctx, &row,
		`SELECT * FROM investment_rules_config WHERE user_id = $1`, userID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return r.fromRow(&row)
}

// Update updates investment rules config
func (r *InvestmentRulesRepository) Update(ctx context.Context, config *entities.InvestmentRulesConfig) error {
	config.UpdatedAt = time.Now()
	row, err := r.toRow(config)
	if err != nil {
		return err
	}

	_, err = r.db.ExecContext(ctx,
		`UPDATE investment_rules_config SET 
		 age_based_allocation = $1, rebalancing_config = $2, drip_config = $3,
		 withdrawal_cooling = $4, round_up_multiplier = $5, milestone_notifications = $6, updated_at = $7
		 WHERE user_id = $8`,
		row.AgeBasedAllocation, row.RebalancingConfig, row.DRIPConfig,
		row.WithdrawalCooling, row.RoundUpMultiplier, row.MilestoneNotifications, row.UpdatedAt, row.UserID)
	return err
}

// Upsert creates or updates investment rules config
func (r *InvestmentRulesRepository) Upsert(ctx context.Context, config *entities.InvestmentRulesConfig) error {
	config.UpdatedAt = time.Now()
	row, err := r.toRow(config)
	if err != nil {
		return err
	}

	_, err = r.db.ExecContext(ctx,
		`INSERT INTO investment_rules_config (id, user_id, age_based_allocation, rebalancing_config, 
		 drip_config, withdrawal_cooling, round_up_multiplier, milestone_notifications, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		 ON CONFLICT (user_id) DO UPDATE SET
		   age_based_allocation = EXCLUDED.age_based_allocation,
		   rebalancing_config = EXCLUDED.rebalancing_config,
		   drip_config = EXCLUDED.drip_config,
		   withdrawal_cooling = EXCLUDED.withdrawal_cooling,
		   round_up_multiplier = EXCLUDED.round_up_multiplier,
		   milestone_notifications = EXCLUDED.milestone_notifications,
		   updated_at = EXCLUDED.updated_at`,
		row.ID, row.UserID, row.AgeBasedAllocation, row.RebalancingConfig,
		row.DRIPConfig, row.WithdrawalCooling, row.RoundUpMultiplier, row.MilestoneNotifications,
		row.CreatedAt, row.UpdatedAt)
	return err
}

// GetAllWithRebalancingEnabled returns all configs with rebalancing enabled
func (r *InvestmentRulesRepository) GetAllWithRebalancingEnabled(ctx context.Context) ([]*entities.InvestmentRulesConfig, error) {
	var rows []investmentRulesRow
	err := r.db.SelectContext(ctx, &rows,
		`SELECT * FROM investment_rules_config WHERE rebalancing_config->>'enabled' = 'true'`)
	if err != nil {
		return nil, err
	}

	configs := make([]*entities.InvestmentRulesConfig, 0, len(rows))
	for _, row := range rows {
		config, err := r.fromRow(&row)
		if err != nil {
			continue
		}
		configs = append(configs, config)
	}
	return configs, nil
}

// UpdateRebalancingTimestamps updates rebalancing timestamps
func (r *InvestmentRulesRepository) UpdateRebalancingTimestamps(ctx context.Context, userID uuid.UUID, checked, rebalanced *time.Time) error {
	config, err := r.GetByUserID(ctx, userID)
	if err != nil || config == nil || config.RebalancingConfig == nil {
		return err
	}

	if checked != nil {
		config.RebalancingConfig.LastChecked = checked
	}
	if rebalanced != nil {
		config.RebalancingConfig.LastRebalanced = rebalanced
	}

	return r.Update(ctx, config)
}

// UpdateDRIPStats updates DRIP statistics
func (r *InvestmentRulesRepository) UpdateDRIPStats(ctx context.Context, userID uuid.UUID, totalReinvested decimal.Decimal, lastDividend *time.Time) error {
	config, err := r.GetByUserID(ctx, userID)
	if err != nil || config == nil || config.DRIPConfig == nil {
		return err
	}

	config.DRIPConfig.TotalReinvested = totalReinvested
	if lastDividend != nil {
		config.DRIPConfig.LastDividend = lastDividend
	}

	return r.Update(ctx, config)
}

func (r *InvestmentRulesRepository) toRow(config *entities.InvestmentRulesConfig) (*investmentRulesRow, error) {
	row := &investmentRulesRow{
		ID:                     config.ID,
		UserID:                 config.UserID,
		RoundUpMultiplier:      int(config.RoundUpMultiplier),
		MilestoneNotifications: config.MilestoneNotifications,
		CreatedAt:              config.CreatedAt,
		UpdatedAt:              config.UpdatedAt,
	}

	if config.AgeBasedAllocation != nil {
		data, err := json.Marshal(config.AgeBasedAllocation)
		if err != nil {
			return nil, err
		}
		row.AgeBasedAllocation = data
	}

	if config.RebalancingConfig != nil {
		data, err := json.Marshal(config.RebalancingConfig)
		if err != nil {
			return nil, err
		}
		row.RebalancingConfig = data
	}

	if config.DRIPConfig != nil {
		data, err := json.Marshal(config.DRIPConfig)
		if err != nil {
			return nil, err
		}
		row.DRIPConfig = data
	}

	if config.WithdrawalCooling != nil {
		data, err := json.Marshal(config.WithdrawalCooling)
		if err != nil {
			return nil, err
		}
		row.WithdrawalCooling = data
	}

	return row, nil
}

func (r *InvestmentRulesRepository) fromRow(row *investmentRulesRow) (*entities.InvestmentRulesConfig, error) {
	config := &entities.InvestmentRulesConfig{
		ID:                     row.ID,
		UserID:                 row.UserID,
		RoundUpMultiplier:      entities.RoundUpMultiplier(row.RoundUpMultiplier),
		MilestoneNotifications: row.MilestoneNotifications,
		CreatedAt:              row.CreatedAt,
		UpdatedAt:              row.UpdatedAt,
	}

	if len(row.AgeBasedAllocation) > 0 {
		var aba entities.AgeBasedAllocation
		if err := json.Unmarshal(row.AgeBasedAllocation, &aba); err == nil {
			config.AgeBasedAllocation = &aba
		}
	}

	if len(row.RebalancingConfig) > 0 {
		var rc entities.AutoRebalancingConfig
		if err := json.Unmarshal(row.RebalancingConfig, &rc); err == nil {
			config.RebalancingConfig = &rc
		}
	}

	if len(row.DRIPConfig) > 0 {
		var dc entities.DRIPConfig
		if err := json.Unmarshal(row.DRIPConfig, &dc); err == nil {
			config.DRIPConfig = &dc
		}
	}

	if len(row.WithdrawalCooling) > 0 {
		var wc entities.WithdrawalCooling
		if err := json.Unmarshal(row.WithdrawalCooling, &wc); err == nil {
			config.WithdrawalCooling = &wc
		}
	}

	return config, nil
}
