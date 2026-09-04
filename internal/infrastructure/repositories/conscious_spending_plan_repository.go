package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
	domainrepos "github.com/rail-service/rail_service/internal/domain/repositories"
)

type ConsciousSpendingPlanRepository struct {
	db *sqlx.DB
}

func NewConsciousSpendingPlanRepository(db *sqlx.DB) *ConsciousSpendingPlanRepository {
	return &ConsciousSpendingPlanRepository{db: db}
}

func (r *ConsciousSpendingPlanRepository) GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.ConsciousSpendingPlan, error) {
	var plan entities.ConsciousSpendingPlan
	err := r.db.GetContext(ctx, &plan, `
		SELECT * FROM conscious_spending_plan_versions
		WHERE user_id = $1 AND status = $2
		ORDER BY version DESC LIMIT 1`, userID, entities.ConsciousSpendingPlanStatusCommitted)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get conscious spending plan: %w", err)
	}
	if len(plan.Items) == 0 {
		plan.Items, err = r.loadItems(ctx, plan.ID)
		if err != nil {
			return nil, fmt.Errorf("load plan items: %w", err)
		}
	}
	if plan.NetWorth == nil {
		plan.NetWorth, err = r.latestNetWorth(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("load plan net worth: %w", err)
		}
	}
	return &plan, nil
}

func (r *ConsciousSpendingPlanRepository) GetActiveVersion(ctx context.Context, userID uuid.UUID) (*entities.ConsciousSpendingPlan, error) {
	return r.GetByUserID(ctx, userID)
}

func (r *ConsciousSpendingPlanRepository) Commit(ctx context.Context, userID uuid.UUID, in domainrepos.PlanHeaderInput) (*entities.ConsciousSpendingPlan, error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin plan commit: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	version, err := r.nextVersion(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if in.CommittedAt == nil {
		in.CommittedAt = &now
	}
	plan, err := r.insertPlan(ctx, tx, userID, version, in, now)
	if err != nil {
		return nil, err
	}
	if err := r.saveItems(ctx, tx, plan.ID, in.Items); err != nil {
		return nil, err
	}
	if in.NetWorth != nil {
		if err := r.saveNetWorth(ctx, tx, plan.ID, userID, in.NetWorth, now); err != nil {
			return nil, err
		}
	}
	if err := r.markSuperseded(ctx, tx, userID, version); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit plan tx: %w", err)
	}
	return plan, nil
}

func (r *ConsciousSpendingPlanRepository) Supersede(ctx context.Context, userID uuid.UUID, version int) (*entities.ConsciousSpendingPlan, error) {
	plan, err := r.loadPlanByVersion(ctx, userID, version, entities.ConsciousSpendingPlanStatusCommitted)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	plan.SupersededAt = &now
	plan.Status = entities.ConsciousSpendingPlanStatusSuperseded
	plan.UpdatedAt = now
	if err := r.upsertPlan(ctx, plan); err != nil {
		return nil, fmt.Errorf("supersede plan: %w", err)
	}
	return r.GetByUserID(ctx, userID)
}

func (r *ConsciousSpendingPlanRepository) Pause(ctx context.Context, userID uuid.UUID, version int) (*entities.ConsciousSpendingPlan, error) {
	plan, err := r.loadPlanByVersion(ctx, userID, version, entities.ConsciousSpendingPlanStatusCommitted)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	plan.Status = entities.ConsciousSpendingPlanStatusPaused
	plan.UpdatedAt = now
	if err := r.upsertPlan(ctx, plan); err != nil {
		return nil, fmt.Errorf("pause plan: %w", err)
	}
	return r.GetByUserID(ctx, userID)
}

func (r *ConsciousSpendingPlanRepository) SaveItems(ctx context.Context, planID uuid.UUID, items []entities.ConsciousSpendingPlanItem) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin save items: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err := r.deleteItems(ctx, tx, planID); err != nil {
		return err
	}
	if err := r.saveItems(ctx, tx, planID, items); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *ConsciousSpendingPlanRepository) SaveNetWorth(ctx context.Context, snapshot *entities.ConsciousSpendingNetWorth) error {
	if snapshot == nil {
		return nil
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin save net worth: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if snapshot.ID == uuid.Nil {
		snapshot.ID = uuid.New()
	}
	if snapshot.CapturedAt.IsZero() {
		snapshot.CapturedAt = time.Now().UTC()
	}
	_, err = tx.NamedExecContext(ctx, `
		INSERT INTO conscious_spending_net_worth_snapshots (
			id, user_id, plan_id, currency, assets, investments, savings, debt, total,
			source, confidence, captured_at, created_at
		) VALUES (
			:id, :user_id, :plan_id, :currency, :assets, :investments, :savings, :debt, :total,
			:source, :confidence, :captured_at, NOW()
		)`, snapshot)
	if err != nil {
		return fmt.Errorf("save net worth snapshot: %w", err)
	}
	return tx.Commit()
}

func (r *ConsciousSpendingPlanRepository) ListCommittedCheckIns(ctx context.Context) ([]entities.ConsciousSpendingPlanCheckIn, error) {
	type checkInRow struct {
		entities.ConsciousSpendingPlan
		Country string `db:"country"`
	}
	var rows []checkInRow
	if err := r.db.SelectContext(ctx, &rows, `
		SELECT csp.*, COALESCE(u.country, '') AS country
		FROM conscious_spending_plan_versions csp
		JOIN users u ON u.id = csp.user_id
		WHERE csp.status = $1
		ORDER BY csp.updated_at`, entities.ConsciousSpendingPlanStatusCommitted); err != nil {
		return nil, fmt.Errorf("list committed conscious spending plan check-ins: %w", err)
	}
	checkIns := make([]entities.ConsciousSpendingPlanCheckIn, 0, len(rows))
	for i := range rows {
		checkIns = append(checkIns, entities.ConsciousSpendingPlanCheckIn{
			Plan: rows[i].ConsciousSpendingPlan, Country: rows[i].Country,
		})
	}
	return checkIns, nil
}

func (r *ConsciousSpendingPlanRepository) nextVersion(ctx context.Context, tx *sqlx.Tx, userID uuid.UUID) (int, error) {
	var version int
	if err := tx.GetContext(ctx, &version, `
		SELECT COALESCE(MAX(version), 0) + 1 FROM conscious_spending_plan_versions WHERE user_id = $1`, userID); err != nil {
		return 0, fmt.Errorf("next plan version: %w", err)
	}
	return version, nil
}

func (r *ConsciousSpendingPlanRepository) insertPlan(ctx context.Context, tx *sqlx.Tx, userID uuid.UUID, version int, in domainrepos.PlanHeaderInput, now time.Time) (*entities.ConsciousSpendingPlan, error) {
	plan := &entities.ConsciousSpendingPlan{
		ID: uuid.New(), UserID: userID, Version: version,
		Scope: strings.ToLower(strings.TrimSpace(in.Scope)), Country: in.Country,
		BaseCurrency: in.BaseCurrency,
		GrossMonthlyIncome:      in.GrossMonthlyIncome,
		PayrollDeductions:       in.PayrollDeductions,
		PreTaxInvestments:       in.PreTaxInvestments,
		TakeHomeIncome:          in.TakeHomeIncome,
		IncomeCadence:           in.IncomeCadence,
		IncomeBasis:             in.IncomeBasis,
		IncomeSource:            in.IncomeSource,
		IncomeConfidence:        in.IncomeConfidence,
		FixedCostsSubtotal:      in.FixedCostsSubtotal,
		MiscBufferRate:          in.MiscBufferRate,
		MiscBufferAmount:        in.MiscBufferAmount,
		FixedCosts:              in.FixedCosts,
		PostTaxInvestments:      in.PostTaxInvestments,
		Savings:                 in.Savings,
		GuiltFreeSpending:       in.GuiltFreeSpending,
		FixedCostsPct:           in.FixedCostsPct,
		InvestmentsPct:          in.InvestmentsPct,
		SavingsPct:              in.SavingsPct,
		GuiltFreeSpendingPct:    in.GuiltFreeSpendingPct,
		Status:                  in.Status,
		CheckInCadence:          in.CheckInCadence,
		CommittedAt:             in.CommittedAt,
		SupersededAt:            in.SupersededAt,
		CreatedAt:               now, UpdatedAt: now,
	}
	_, err := tx.NamedExecContext(ctx, `
		INSERT INTO conscious_spending_plan_versions (
			id, user_id, version, scope, country, base_currency,
			gross_monthly_income, payroll_deductions, pre_tax_investments, take_home_income,
			income_cadence, income_basis, income_source, income_confidence,
			fixed_costs_subtotal, misc_buffer_rate, misc_buffer_amount,
			fixed_costs, post_tax_investments, savings, guilt_free_spending,
			fixed_costs_pct, investments_pct, savings_pct, guilt_free_spending_pct,
			status, check_in_cadence, committed_at, superseded_at, created_at, updated_at
		) VALUES (
			:id, :user_id, :version, :scope, :country, :base_currency,
			:gross_monthly_income, :payroll_deductions, :pre_tax_investments, :take_home_income,
			:income_cadence, :income_basis, :income_source, :income_confidence,
			:fixed_costs_subtotal, :misc_buffer_rate, :misc_buffer_amount,
			:fixed_costs, :post_tax_investments, :savings, :guilt_free_spending,
			:fixed_costs_pct, :investments_pct, :savings_pct, :guilt_free_spending_pct,
			:status, :check_in_cadence, :committed_at, :superseded_at, :created_at, :updated_at
		)`, plan)
	if err != nil {
		return nil, fmt.Errorf("insert plan: %w", err)
	}
	return plan, nil
}

func (r *ConsciousSpendingPlanRepository) markSuperseded(ctx context.Context, tx *sqlx.Tx, userID uuid.UUID, newVersion int) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE conscious_spending_plan_versions
		SET status = $1, superseded_at = NOW(), updated_at = NOW()
		WHERE user_id = $2 AND version <> $3 AND status = $1`,
		entities.ConsciousSpendingPlanStatusSuperseded, userID, newVersion); err != nil {
		return fmt.Errorf("supersede prior plans: %w", err)
	}
	return nil
}

func (r *ConsciousSpendingPlanRepository) upsertPlan(ctx context.Context, plan *entities.ConsciousSpendingPlan) error {
	_, err := r.db.NamedExecContext(ctx, `
		UPDATE conscious_spending_plan_versions SET
			scope = :scope, country = :country, base_currency = :base_currency,
			gross_monthly_income = :gross_monthly_income,
			payroll_deductions = :payroll_deductions,
			pre_tax_investments = :pre_tax_investments,
			take_home_income = :take_home_income,
			income_cadence = :income_cadence, income_basis = :income_basis,
			income_source = :income_source, income_confidence = :income_confidence,
			fixed_costs_subtotal = :fixed_costs_subtotal,
			misc_buffer_rate = :misc_buffer_rate, misc_buffer_amount = :misc_buffer_amount,
			fixed_costs = :fixed_costs, post_tax_investments = :post_tax_investments,
			savings = :savings, guilt_free_spending = :guilt_free_spending,
			fixed_costs_pct = :fixed_costs_pct, investments_pct = :investments_pct,
			savings_pct = :savings_pct, guilt_free_spending_pct = :guilt_free_spending_pct,
			status = :status, check_in_cadence = :check_in_cadence,
			committed_at = :committed_at, superseded_at = :superseded_at,
			updated_at = NOW()
		WHERE id = :id`, plan)
	if err != nil {
		return fmt.Errorf("update plan: %w", err)
	}
	return nil
}

func (r *ConsciousSpendingPlanRepository) saveItems(ctx context.Context, tx *sqlx.Tx, planID uuid.UUID, items []entities.ConsciousSpendingPlanItem) error {
	if len(items) == 0 {
		return nil
	}
	stmt, err := tx.PrepareNamedContext(ctx, `
		INSERT INTO conscious_spending_plan_items (
			id, plan_id, bucket, name, amount, cadence, source, confidence,
			original_amount, original_currency, fx_rate, fx_rate_at, evidence_ref,
			display_order, created_at
		) VALUES (
			:id, :plan_id, :bucket, :name, :amount, :cadence, :source, :confidence,
			:original_amount, :original_currency, :fx_rate, :fx_rate_at, :evidence_ref,
			:display_order, NOW()
		)`)
	if err != nil {
		return fmt.Errorf("prepare item insert: %w", err)
	}
	defer stmt.Close()
	for i := range items {
		if items[i].ID == uuid.Nil {
			items[i].ID = uuid.New()
		}
		items[i].PlanID = planID
		if _, err := stmt.ExecContext(ctx, &items[i]); err != nil {
			return fmt.Errorf("insert plan item: %w", err)
		}
	}
	return nil
}

func (r *ConsciousSpendingPlanRepository) deleteItems(ctx context.Context, tx *sqlx.Tx, planID uuid.UUID) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM conscious_spending_plan_items WHERE plan_id = $1`, planID); err != nil {
		return fmt.Errorf("delete plan items: %w", err)
	}
	return nil
}

func (r *ConsciousSpendingPlanRepository) loadItems(ctx context.Context, planID uuid.UUID) ([]entities.ConsciousSpendingPlanItem, error) {
	var items []entities.ConsciousSpendingPlanItem
	if err := r.db.SelectContext(ctx, &items, `
		SELECT * FROM conscious_spending_plan_items
		WHERE plan_id = $1
		ORDER BY display_order, created_at`, planID); err != nil {
		return nil, fmt.Errorf("load plan items: %w", err)
	}
	return items, nil
}

func (r *ConsciousSpendingPlanRepository) saveNetWorth(ctx context.Context, tx *sqlx.Tx, planID, userID uuid.UUID, snapshot *entities.ConsciousSpendingNetWorth, now time.Time) error {
	if snapshot == nil {
		return nil
	}
	snapshot.ID = uuid.New()
	snapshot.PlanID = planID
	snapshot.UserID = userID
	if snapshot.CapturedAt.IsZero() {
		snapshot.CapturedAt = now
	}
	if snapshot.Total.IsZero() {
		snapshot.Total = snapshot.Assets.Add(snapshot.Investments).Add(snapshot.Savings).Sub(snapshot.Debt)
	}
	if _, err := tx.NamedExecContext(ctx, `
		INSERT INTO conscious_spending_net_worth_snapshots (
			id, plan_id, user_id, currency, assets, investments, savings, debt, total,
			source, confidence, captured_at, created_at
		) VALUES (
			:id, :plan_id, :user_id, :currency, :assets, :investments, :savings, :debt, :total,
			:source, :confidence, :captured_at, NOW()
		)`, snapshot); err != nil {
		return fmt.Errorf("insert net worth snapshot: %w", err)
	}
	return nil
}

func (r *ConsciousSpendingPlanRepository) latestNetWorth(ctx context.Context, userID uuid.UUID) (*entities.ConsciousSpendingNetWorth, error) {
	var snapshot entities.ConsciousSpendingNetWorth
	if err := r.db.GetContext(ctx, &snapshot, `
		SELECT * FROM conscious_spending_net_worth_snapshots
		WHERE user_id = $1
		ORDER BY captured_at DESC LIMIT 1`, userID); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("load net worth snapshot: %w", err)
	}
	return &snapshot, nil
}

func (r *ConsciousSpendingPlanRepository) loadPlanByVersion(ctx context.Context, userID uuid.UUID, version int, status string) (*entities.ConsciousSpendingPlan, error) {
	var plan entities.ConsciousSpendingPlan
	if err := r.db.GetContext(ctx, &plan, `
		SELECT * FROM conscious_spending_plan_versions
		WHERE user_id = $1 AND version = $2 AND status = $3`, userID, version, status); err == sql.ErrNoRows {
		return nil, domainrepos.ErrPlanNotFound
	} else if err != nil {
		return nil, fmt.Errorf("load plan version: %w", err)
	}
	return &plan, nil
}
