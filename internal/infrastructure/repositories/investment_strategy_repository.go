package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// ---------------------------------------------------------------------------
// Asset catalog
// ---------------------------------------------------------------------------

// InvestmentAssetRepository persists the investable asset catalog.
type InvestmentAssetRepository struct {
	db *sqlx.DB
}

// NewInvestmentAssetRepository creates a new investment asset repository.
func NewInvestmentAssetRepository(db *sqlx.DB) *InvestmentAssetRepository {
	return &InvestmentAssetRepository{db: db}
}

const investmentAssetColumns = `id, caip19, symbol, name, asset_class, chain, decimals,
	allowlisted, prohibited, source, priority, created_at, updated_at`

// Upsert inserts or refreshes an asset by its CAIP-19 identifier.
func (r *InvestmentAssetRepository) Upsert(ctx context.Context, asset *entities.InvestmentAsset) error {
	if asset == nil {
		return fmt.Errorf("investment asset is nil")
	}
	if asset.ID == uuid.Nil {
		asset.ID = uuid.New()
	}
	asset.CreatedAt = investmentTimeOrNow(asset.CreatedAt)
	asset.UpdatedAt = investmentTimeOrNow(asset.UpdatedAt)

	query := `
		INSERT INTO investment_assets (id, caip19, symbol, name, asset_class, chain, decimals,
			allowlisted, prohibited, source, priority, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (caip19) DO UPDATE SET
			symbol = EXCLUDED.symbol,
			name = EXCLUDED.name,
			asset_class = EXCLUDED.asset_class,
			chain = EXCLUDED.chain,
			decimals = EXCLUDED.decimals,
			allowlisted = EXCLUDED.allowlisted,
			prohibited = EXCLUDED.prohibited,
			source = EXCLUDED.source,
			priority = EXCLUDED.priority,
			updated_at = EXCLUDED.updated_at
		RETURNING id`

	return r.db.QueryRowxContext(ctx, query,
		asset.ID, asset.CAIP19, asset.Symbol, asset.Name, asset.AssetClass, asset.Chain,
		asset.Decimals, asset.Allowlisted, asset.Prohibited, asset.Source, asset.Priority,
		asset.CreatedAt, asset.UpdatedAt,
	).Scan(&asset.ID)
}

// GetByID returns an asset by Rail id, or (nil, nil) when absent.
func (r *InvestmentAssetRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.InvestmentAsset, error) {
	asset := &entities.InvestmentAsset{}
	err := r.db.GetContext(ctx, asset,
		`SELECT `+investmentAssetColumns+` FROM investment_assets WHERE id = $1`, id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get investment asset: %w", err)
	}
	return asset, nil
}

// GetByCAIP19 returns an asset by its CAIP-19 identifier, or (nil, nil).
func (r *InvestmentAssetRepository) GetByCAIP19(ctx context.Context, caip19 string) (*entities.InvestmentAsset, error) {
	asset := &entities.InvestmentAsset{}
	err := r.db.GetContext(ctx, asset,
		`SELECT `+investmentAssetColumns+` FROM investment_assets WHERE caip19 = $1`, caip19)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get investment asset by caip19: %w", err)
	}
	return asset, nil
}

// GetBySymbol resolves a symbol to the best catalog asset. Symbols are not
// unique, so allowed assets win and ties break on priority.
func (r *InvestmentAssetRepository) GetBySymbol(ctx context.Context, symbol string) (*entities.InvestmentAsset, error) {
	asset := &entities.InvestmentAsset{}
	err := r.db.GetContext(ctx, asset,
		`SELECT `+investmentAssetColumns+`
		 FROM investment_assets
		 WHERE UPPER(symbol) = UPPER($1)
		 ORDER BY allowlisted DESC, priority DESC
		 LIMIT 1`, symbol)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get investment asset by symbol: %w", err)
	}
	return asset, nil
}

// List searches the investable catalog (allowlisted, non-prohibited assets).
func (r *InvestmentAssetRepository) List(ctx context.Context, query string, limit int) ([]*entities.InvestmentAsset, error) {
	if limit <= 0 {
		limit = 25
	}
	if limit > 100 {
		limit = 100
	}
	assets := make([]*entities.InvestmentAsset, 0)
	err := r.db.SelectContext(ctx, &assets,
		`SELECT `+investmentAssetColumns+`
		 FROM investment_assets
		 WHERE allowlisted = true AND prohibited = false
		   AND ($1 = '' OR symbol ILIKE '%' || $1 || '%' OR name ILIKE '%' || $1 || '%' OR caip19 ILIKE '%' || $1 || '%')
		 ORDER BY priority DESC, symbol ASC
		 LIMIT $2`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("list investment assets: %w", err)
	}
	return assets, nil
}

// CountAllowed counts the assets the engine may actually allocate to.
func (r *InvestmentAssetRepository) CountAllowed(ctx context.Context) (int, error) {
	var count int
	err := r.db.GetContext(ctx, &count,
		`SELECT COUNT(*) FROM investment_assets WHERE allowlisted = true AND prohibited = false`)
	if err != nil {
		return 0, fmt.Errorf("count allowed investment assets: %w", err)
	}
	return count, nil
}

// ---------------------------------------------------------------------------
// Strategies
// ---------------------------------------------------------------------------

// InvestmentStrategyRepository persists strategy identities and versions.
type InvestmentStrategyRepository struct {
	db *sqlx.DB
}

// NewInvestmentStrategyRepository creates a new investment strategy repository.
func NewInvestmentStrategyRepository(db *sqlx.DB) *InvestmentStrategyRepository {
	return &InvestmentStrategyRepository{db: db}
}

const investmentStrategyColumns = `id, user_id, owner_type, glider_strategy_id, name, description,
	objective, risk, horizon, status, current_version, is_public, created_by,
	created_at, updated_at, closed_at`

// Create inserts a new strategy identity.
func (r *InvestmentStrategyRepository) Create(ctx context.Context, strategy *entities.InvestmentStrategy) error {
	if strategy == nil {
		return fmt.Errorf("investment strategy is nil")
	}
	if strategy.ID == uuid.Nil {
		strategy.ID = uuid.New()
	}
	strategy.CreatedAt = investmentTimeOrNow(strategy.CreatedAt)
	strategy.UpdatedAt = investmentTimeOrNow(strategy.UpdatedAt)

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO investment_strategies (id, user_id, owner_type, glider_strategy_id, name,
			description, objective, risk, horizon, status, current_version, is_public,
			created_by, created_at, updated_at, closed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
		strategy.ID, strategy.UserID, string(strategy.OwnerType), strategy.GliderStrategyID,
		strategy.Name, strategy.Description, strategy.Objective, strategy.Risk, strategy.Horizon,
		string(strategy.Status), strategy.CurrentVersion, strategy.IsPublic, string(strategy.CreatedBy),
		strategy.CreatedAt, strategy.UpdatedAt, strategy.ClosedAt)
	if err != nil {
		return fmt.Errorf("create investment strategy: %w", err)
	}
	return nil
}

// Update writes the mutable strategy fields.
func (r *InvestmentStrategyRepository) Update(ctx context.Context, strategy *entities.InvestmentStrategy) error {
	if strategy == nil {
		return fmt.Errorf("investment strategy is nil")
	}
	strategy.UpdatedAt = investmentTimeOrNow(strategy.UpdatedAt)

	_, err := r.db.ExecContext(ctx, `
		UPDATE investment_strategies SET
			user_id = $2, owner_type = $3, glider_strategy_id = $4, name = $5,
			description = $6, objective = $7, risk = $8, horizon = $9, status = $10,
			current_version = $11, is_public = $12, created_by = $13, updated_at = $14, closed_at = $15
		WHERE id = $1`,
		strategy.ID, strategy.UserID, string(strategy.OwnerType), strategy.GliderStrategyID,
		strategy.Name, strategy.Description, strategy.Objective, strategy.Risk, strategy.Horizon,
		string(strategy.Status), strategy.CurrentVersion, strategy.IsPublic, string(strategy.CreatedBy),
		strategy.UpdatedAt, strategy.ClosedAt)
	if err != nil {
		return fmt.Errorf("update investment strategy: %w", err)
	}
	return nil
}

// GetByID returns a strategy by id, or (nil, nil) when absent.
func (r *InvestmentStrategyRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.InvestmentStrategy, error) {
	strategy := &entities.InvestmentStrategy{}
	err := r.db.GetContext(ctx, strategy,
		`SELECT `+investmentStrategyColumns+` FROM investment_strategies WHERE id = $1`, id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get investment strategy: %w", err)
	}
	return strategy, nil
}

// ListByUser returns a user's strategies, optionally filtered by status.
func (r *InvestmentStrategyRepository) ListByUser(ctx context.Context, userID uuid.UUID, status string) ([]*entities.InvestmentStrategy, error) {
	strategies := make([]*entities.InvestmentStrategy, 0)
	err := r.db.SelectContext(ctx, &strategies,
		`SELECT `+investmentStrategyColumns+`
		 FROM investment_strategies
		 WHERE user_id = $1 AND ($2 = '' OR status = $2)
		 ORDER BY created_at DESC`, userID, status)
	if err != nil {
		return nil, fmt.Errorf("list investment strategies: %w", err)
	}
	return strategies, nil
}

// ListByOwnerType returns strategies of a given owner type (Rail-curated,
// discovered public strategies) for browsing.
func (r *InvestmentStrategyRepository) ListByOwnerType(ctx context.Context, ownerType entities.InvestmentStrategyOwnerType, limit int) ([]*entities.InvestmentStrategy, error) {
	if limit <= 0 {
		limit = 25
	}
	if limit > 200 {
		limit = 200
	}
	strategies := make([]*entities.InvestmentStrategy, 0)
	err := r.db.SelectContext(ctx, &strategies,
		`SELECT `+investmentStrategyColumns+`
		 FROM investment_strategies
		 WHERE owner_type = $1
		 ORDER BY updated_at DESC
		 LIMIT $2`, string(ownerType), limit)
	if err != nil {
		return nil, fmt.Errorf("list investment strategies by owner: %w", err)
	}
	return strategies, nil
}

// FindByGliderID resolves the Rail strategy mirroring a provider strategy.
func (r *InvestmentStrategyRepository) FindByGliderID(ctx context.Context, gliderStrategyID string) (*entities.InvestmentStrategy, error) {
	strategy := &entities.InvestmentStrategy{}
	err := r.db.GetContext(ctx, strategy,
		`SELECT `+investmentStrategyColumns+` FROM investment_strategies WHERE glider_strategy_id = $1`, gliderStrategyID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find investment strategy by glider id: %w", err)
	}
	return strategy, nil
}

// ---------------------------------------------------------------------------
// Strategy versions (immutable allocation revisions)
// ---------------------------------------------------------------------------

// investmentStrategyVersionRow carries the JSONB columns as raw bytes so they
// can be decoded explicitly.
type investmentStrategyVersionRow struct {
	ID                    uuid.UUID `db:"id"`
	StrategyID            uuid.UUID `db:"strategy_id"`
	Version               int       `db:"version"`
	TargetAllocation      []byte    `db:"target_allocation"`
	Risk                  string    `db:"risk"`
	Horizon               string    `db:"horizon"`
	RebalanceRules        []byte    `db:"rebalance_rules"`
	ContributionRules     []byte    `db:"contribution_rules"`
	ExecutionRules        []byte    `db:"execution_rules"`
	Constraints           []byte    `db:"constraints"`
	Rationale             string    `db:"rationale"`
	ValidationReport      []byte    `db:"validation_report"`
	GliderStrategyVersion *int      `db:"glider_strategy_version"`
	CreatedBy             string    `db:"created_by"`
	CreatedAt             time.Time `db:"created_at"`
}

const investmentStrategyVersionColumns = `id, strategy_id, version, target_allocation, risk, horizon,
	rebalance_rules, contribution_rules, execution_rules, constraints, rationale,
	validation_report, glider_strategy_version, created_by, created_at`

func (row *investmentStrategyVersionRow) toEntity() (*entities.InvestmentStrategyVersion, error) {
	version := &entities.InvestmentStrategyVersion{
		ID:                    row.ID,
		StrategyID:            row.StrategyID,
		Version:               row.Version,
		TargetAllocation:      []entities.InvestmentAllocationLeg{},
		Risk:                  row.Risk,
		Horizon:               row.Horizon,
		GliderStrategyVersion: row.GliderStrategyVersion,
		CreatedBy:             entities.InvestmentActor(row.CreatedBy),
		CreatedAt:             row.CreatedAt,
	}
	if err := investmentDecodeJSON(row.TargetAllocation, &version.TargetAllocation); err != nil {
		return nil, fmt.Errorf("decode target allocation: %w", err)
	}
	if err := investmentDecodeJSON(row.RebalanceRules, &version.RebalanceRules); err != nil {
		return nil, fmt.Errorf("decode rebalance rules: %w", err)
	}
	if err := investmentDecodeJSON(row.ContributionRules, &version.ContributionRules); err != nil {
		return nil, fmt.Errorf("decode contribution rules: %w", err)
	}
	if err := investmentDecodeJSON(row.ExecutionRules, &version.ExecutionRules); err != nil {
		return nil, fmt.Errorf("decode execution rules: %w", err)
	}
	if err := investmentDecodeJSON(row.Constraints, &version.Constraints); err != nil {
		return nil, fmt.Errorf("decode constraints: %w", err)
	}
	if len(row.ValidationReport) > 0 {
		report := &entities.InvestmentValidationReport{}
		if err := investmentDecodeJSON(row.ValidationReport, report); err != nil {
			return nil, fmt.Errorf("decode validation report: %w", err)
		}
		version.ValidationReport = report
	}
	return version, nil
}

// CreateVersion persists one immutable allocation revision.
func (r *InvestmentStrategyRepository) CreateVersion(ctx context.Context, version *entities.InvestmentStrategyVersion) error {
	if version == nil {
		return fmt.Errorf("investment strategy version is nil")
	}
	if version.ID == uuid.Nil {
		version.ID = uuid.New()
	}
	version.CreatedAt = investmentTimeOrNow(version.CreatedAt)

	target := version.TargetAllocation
	if target == nil {
		target = []entities.InvestmentAllocationLeg{}
	}
	targetJSON, err := investmentEncodeJSON(target)
	if err != nil {
		return fmt.Errorf("encode target allocation: %w", err)
	}
	rebalanceJSON, err := investmentEncodeJSON(version.RebalanceRules)
	if err != nil {
		return fmt.Errorf("encode rebalance rules: %w", err)
	}
	contributionJSON, err := investmentEncodeJSON(version.ContributionRules)
	if err != nil {
		return fmt.Errorf("encode contribution rules: %w", err)
	}
	executionJSON, err := investmentEncodeJSON(version.ExecutionRules)
	if err != nil {
		return fmt.Errorf("encode execution rules: %w", err)
	}
	constraintsJSON, err := investmentEncodeJSON(version.Constraints)
	if err != nil {
		return fmt.Errorf("encode constraints: %w", err)
	}
	var validationJSON []byte
	if version.ValidationReport != nil {
		validationJSON, err = investmentEncodeJSON(version.ValidationReport)
		if err != nil {
			return fmt.Errorf("encode validation report: %w", err)
		}
	}

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO investment_strategy_versions (id, strategy_id, version, target_allocation, risk,
			horizon, rebalance_rules, contribution_rules, execution_rules, constraints, rationale,
			validation_report, glider_strategy_version, created_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
		version.ID, version.StrategyID, version.Version, targetJSON, version.Risk, version.Horizon,
		rebalanceJSON, contributionJSON, executionJSON, constraintsJSON, version.Rationale,
		validationJSON, version.GliderStrategyVersion, string(version.CreatedBy), version.CreatedAt)
	if err != nil {
		return fmt.Errorf("create investment strategy version: %w", err)
	}
	return nil
}

// GetVersion returns one strategy version, or (nil, nil) when absent.
func (r *InvestmentStrategyRepository) GetVersion(ctx context.Context, strategyID uuid.UUID, version int) (*entities.InvestmentStrategyVersion, error) {
	row := investmentStrategyVersionRow{}
	err := r.db.GetContext(ctx, &row,
		`SELECT `+investmentStrategyVersionColumns+`
		 FROM investment_strategy_versions WHERE strategy_id = $1 AND version = $2`, strategyID, version)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get investment strategy version: %w", err)
	}
	return row.toEntity()
}

// ListVersions returns a strategy's version history, newest first.
func (r *InvestmentStrategyRepository) ListVersions(ctx context.Context, strategyID uuid.UUID) ([]*entities.InvestmentStrategyVersion, error) {
	rows := make([]investmentStrategyVersionRow, 0)
	err := r.db.SelectContext(ctx, &rows,
		`SELECT `+investmentStrategyVersionColumns+`
		 FROM investment_strategy_versions WHERE strategy_id = $1 ORDER BY version DESC`, strategyID)
	if err != nil {
		return nil, fmt.Errorf("list investment strategy versions: %w", err)
	}
	versions := make([]*entities.InvestmentStrategyVersion, 0, len(rows))
	for i := range rows {
		version, err := rows[i].toEntity()
		if err != nil {
			return nil, err
		}
		versions = append(versions, version)
	}
	return versions, nil
}
