package investment

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// ---------------------------------------------------------------------------
// State synchronization (spec §20)
//
// The provider is the source of truth for positions and operations. Rail's
// tables are a read model that the sync worker keeps honest, so Miriam always
// reads grounded state instead of remembering.
// ---------------------------------------------------------------------------

// SyncEnrollment refreshes one enrollment's positions, valuation and schedule
// from the provider. Errors are recorded on the enrollment instead of being
// swallowed, so the next answer can say the data is stale.
func (s *Service) SyncEnrollment(ctx context.Context, enrollmentID uuid.UUID) error {
	enrollment, err := s.enrollments.GetByID(ctx, enrollmentID)
	if err != nil {
		return fmt.Errorf("get enrollment: %w", err)
	}
	if enrollment == nil {
		return ErrNotFound
	}
	if enrollment.Status == entities.InvestmentEnrollmentClosed {
		return nil
	}

	portfolio, err := s.provider.GetPortfolio(ctx, enrollment.GliderPortfolioID)
	if err != nil {
		s.recordSyncError(ctx, enrollment, err)
		return fmt.Errorf("get portfolio: %w", s.mapProviderError(err))
	}

	holdings, err := s.provider.GetPositions(ctx, enrollment.GliderPortfolioID)
	if err != nil {
		s.recordSyncError(ctx, enrollment, err)
		return fmt.Errorf("get positions: %w", s.mapProviderError(err))
	}

	now := s.nowOr()
	normalized := s.normalizeHoldings(ctx, enrollment, holdings)
	if err := s.holdings.ReplaceForEnrollment(ctx, enrollment.ID, normalized); err != nil {
		return fmt.Errorf("replace holdings: %w", err)
	}

	enrollment.TotalValueUSD = holdings.TotalValueUSD
	enrollment.PositionsAsOf = &now
	enrollment.LastSyncError = ""
	if portfolio.Schedule.NextDueAt != nil {
		enrollment.NextDueAt = portfolio.Schedule.NextDueAt
	}
	if portfolio.Schedule.LastRebalanceAt != nil {
		enrollment.LastRebalanceAt = portfolio.Schedule.LastRebalanceAt
	}
	if portfolio.Status != "" {
		enrollment.AutomationStatus = portfolio.Status
	}
	switch strings.ToLower(portfolio.Status) {
	case "stopped", "paused":
		enrollment.Status = entities.InvestmentEnrollmentPaused
	case "active", "running", "":
		if enrollment.Status != entities.InvestmentEnrollmentPaused || enrollment.AutomationStatus == "active" {
			enrollment.Status = entities.InvestmentEnrollmentActive
		}
	}
	// A portfolio whose strategy version moved on is re-targeted by the provider
	// itself; Rail records which version the portfolio now mirrors.
	if portfolio.StrategyVersion > 0 && portfolio.StrategyVersion != enrollment.StrategyVersion {
		enrollment.StrategyVersion = portfolio.StrategyVersion
	}
	enrollment.UpdatedAt = now
	if err := s.enrollments.Update(ctx, enrollment); err != nil {
		return fmt.Errorf("update enrollment: %w", err)
	}
	return nil
}

// SyncUserEnrollments refreshes every active enrollment for a user.
func (s *Service) SyncUserEnrollments(ctx context.Context, userID uuid.UUID) error {
	enrollments, err := s.enrollments.ListByUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("list enrollments: %w", err)
	}
	var firstErr error
	for _, enrollment := range enrollments {
		if enrollment.Status == entities.InvestmentEnrollmentClosed {
			continue
		}
		if err := s.SyncEnrollment(ctx, enrollment.ID); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// PollOperations advances every open provider operation and closes the
// executions it belongs to.
func (s *Service) PollOperations(ctx context.Context, limit int) (int, error) {
	if s.operations == nil {
		return 0, nil
	}
	if limit <= 0 {
		limit = 50
	}
	open, err := s.operations.ListOpen(ctx, limit)
	if err != nil {
		return 0, fmt.Errorf("list open operations: %w", err)
	}
	polled := 0
	for _, operation := range open {
		if operation == nil {
			continue
		}
		portfolioID := ""
		if operation.EnrollmentID != nil {
			if enrollment, err := s.enrollments.GetByID(ctx, *operation.EnrollmentID); err == nil && enrollment != nil {
				portfolioID = enrollment.GliderPortfolioID
			}
		}
		if portfolioID == "" {
			continue
		}
		state, err := s.provider.GetOperation(ctx, portfolioID, operation.ProviderOperationID)
		if err != nil {
			s.log.Error("failed to poll provider operation",
				"operation_id", operation.ProviderOperationID,
				"error", err)
			continue
		}
		polled++

		operation.State = state.State
		operation.UpdatedAt = s.nowOr()
		if state.Error != nil {
			operation.Error = *state.Error
		}
		if state.FinishedAt != nil {
			operation.FinishedAt = state.FinishedAt
		}
		if err := s.operations.Upsert(ctx, operation); err != nil {
			return polled, fmt.Errorf("update operation: %w", err)
		}
		if operation.ExecutionID != nil {
			if err := s.settleExecution(ctx, *operation.ExecutionID, state); err != nil {
				return polled, err
			}
		}
		if state.FinishedAt != nil && operation.EnrollmentID != nil {
			if err := s.SyncEnrollment(ctx, *operation.EnrollmentID); err != nil {
				s.log.Error("post-operation sync failed",
					"enrollment_id", operation.EnrollmentID.String(),
					"error", err)
			}
		}
	}
	return polled, nil
}

// settleExecution mirrors a finished operation onto its execution record.
func (s *Service) settleExecution(ctx context.Context, executionID uuid.UUID, state *entities.GliderOperationState) error {
	if s.executions == nil {
		return nil
	}
	execution, err := s.executions.GetByID(ctx, executionID)
	if err != nil {
		return fmt.Errorf("get execution: %w", err)
	}
	if execution == nil {
		return nil
	}
	now := s.nowOr()
	switch strings.ToLower(state.State) {
	case "completed", "filled", "success":
		execution.Status = entities.InvestmentExecutionFilled
		execution.CompletedAt = &now
		_ = s.recordEvent(ctx, execution.UserID, EventOrderFilled, entities.InvestmentActorWorker, map[string]any{
			"order_id":     execution.ID.String(),
			"operation_id": state.OperationID,
		})
	case "failed", "error":
		execution.Status = entities.InvestmentExecutionFailed
		execution.FailureCode = "provider_failed"
		if state.Error != nil {
			execution.FailureReason = *state.Error
		}
		execution.CompletedAt = &now
		_ = s.recordEvent(ctx, execution.UserID, EventOrderFailed, entities.InvestmentActorWorker, map[string]any{
			"order_id":     execution.ID.String(),
			"operation_id": state.OperationID,
			"reason":       execution.FailureReason,
		})
	case "cancelled", "canceled":
		execution.Status = entities.InvestmentExecutionCancelled
		execution.CompletedAt = &now
		_ = s.recordEvent(ctx, execution.UserID, EventOrderCancelled, entities.InvestmentActorWorker, map[string]any{
			"order_id":     execution.ID.String(),
			"operation_id": state.OperationID,
		})
	case "running", "executing", "processing":
		execution.Status = entities.InvestmentExecutionExecuting
	default:
		execution.Status = entities.InvestmentExecutionSubmitted
	}
	execution.UpdatedAt = now
	if err := s.executions.Update(ctx, execution); err != nil {
		return fmt.Errorf("update execution: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Rebalance sweep
// ---------------------------------------------------------------------------

// DueForRebalance reports whether an enrollment has drifted past its strategy's
// threshold, and by how much. Pure: it reads state and computes, it does not act.
func (s *Service) DueForRebalance(
	ctx context.Context,
	enrollment *entities.InvestmentEnrollment,
) (bool, decimal.Decimal, error) {
	version, err := s.strategies.GetVersion(ctx, enrollment.StrategyID, enrollment.StrategyVersion)
	if err != nil {
		return false, decimal.Zero, fmt.Errorf("get strategy version: %w", err)
	}
	if version == nil {
		return false, decimal.Zero, nil
	}
	if version.RebalanceRules.Type != "" && version.RebalanceRules.Type != "threshold" && version.RebalanceRules.Type != "hybrid" {
		return false, decimal.Zero, nil
	}
	threshold := version.RebalanceRules.ThresholdPct
	if threshold == nil || !threshold.GreaterThan(decimal.Zero) {
		return false, decimal.Zero, nil
	}

	holdings, err := s.holdings.ListByEnrollment(ctx, enrollment.ID)
	if err != nil {
		return false, decimal.Zero, fmt.Errorf("list holdings: %w", err)
	}
	value := decimal.Zero
	bySymbol := map[string]decimal.Decimal{}
	for _, holding := range holdings {
		if holding == nil {
			continue
		}
		value = value.Add(holding.ValueUSD)
		bySymbol[strings.ToUpper(holding.Symbol)] = holding.ValueUSD
	}
	if !value.GreaterThan(decimal.Zero) {
		return false, decimal.Zero, nil
	}

	drift := decimal.Zero
	for _, leg := range version.TargetAllocation {
		current := bySymbol[strings.ToUpper(leg.Symbol)]
		weight := current.Div(value).Mul(decimal.NewFromInt(100))
		if diff := weight.Sub(leg.Weight).Abs(); diff.GreaterThan(drift) {
			drift = diff
		}
	}
	return drift.GreaterThanOrEqual(*threshold), drift, nil
}

// RunRebalanceSweep triggers a rebalance for every active enrollment that has
// drifted past its threshold and is not inside the provider's cooldown.
func (s *Service) RunRebalanceSweep(ctx context.Context, limit int) (int, error) {
	if !s.cfg.Enabled {
		return 0, nil
	}
	if limit <= 0 {
		limit = 25
	}
	enrollments, err := s.enrollments.ListActive(ctx, limit)
	if err != nil {
		return 0, fmt.Errorf("list active enrollments: %w", err)
	}
	triggered := 0
	for _, enrollment := range enrollments {
		if enrollment == nil || enrollment.Status != entities.InvestmentEnrollmentActive {
			continue
		}
		due, drift, err := s.DueForRebalance(ctx, enrollment)
		if err != nil {
			s.log.Error("drift check failed", "enrollment_id", enrollment.ID.String(), "error", err)
			continue
		}
		if !due {
			continue
		}
		_, err = s.TriggerRebalance(ctx, enrollment.UserID, enrollment.StrategyID, "drift", entities.InvestmentActorWorker)
		if err != nil {
			// A cooldown is expected and not an error worth alerting on.
			if strings.Contains(err.Error(), "cooldown") || strings.Contains(err.Error(), "conflict") {
				continue
			}
			s.log.Error("automatic rebalance failed",
				"enrollment_id", enrollment.ID.String(),
				"drift", drift.String(),
				"error", err)
			continue
		}
		triggered++
	}
	return triggered, nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func (s *Service) recordSyncError(ctx context.Context, enrollment *entities.InvestmentEnrollment, cause error) {
	enrollment.LastSyncError = cause.Error()
	enrollment.UpdatedAt = s.nowOr()
	if err := s.enrollments.Update(ctx, enrollment); err != nil {
		s.log.Error("failed to record sync error",
			"enrollment_id", enrollment.ID.String(),
			"error", err)
	}
}

// normalizeHoldings maps provider positions onto the Rail read model, attaching
// catalog asset ids where we know the asset and leaving them nil where we do
// not (the provider is still authoritative for the position itself).
func (s *Service) normalizeHoldings(
	ctx context.Context,
	enrollment *entities.InvestmentEnrollment,
	positions *entities.GliderPositions,
) []*entities.InvestmentHolding {
	now := s.nowOr()
	total := positions.TotalValueUSD
	holdings := make([]*entities.InvestmentHolding, 0, len(positions.Assets))
	for _, position := range positions.Assets {
		holding := &entities.InvestmentHolding{
			ID:           uuid.New(),
			UserID:       enrollment.UserID,
			EnrollmentID: enrollment.ID,
			CAIP19:       position.AssetID,
			Symbol:       position.Symbol,
			Name:         position.Name,
			Balance:      position.Balance,
			BalanceRaw:   position.BalanceRaw,
			Decimals:     position.Decimals,
			PriceUSD:     position.PriceUSD,
			ValueUSD:     position.ValueUSD,
			Source:       "glider",
			AsOf:         now,
			UpdatedAt:    now,
		}
		if total.GreaterThan(decimal.Zero) {
			holding.WeightPct = position.ValueUSD.Div(total).Mul(decimal.NewFromInt(100)).Round(2)
		}
		if s.resolver != nil {
			if asset, err := s.resolver.Resolve(ctx, "", position.AssetID, position.Symbol); err == nil && asset != nil {
				assetID := asset.ID
				holding.AssetID = &assetID
				holding.Symbol = asset.Symbol
				if holding.CAIP19 == "" {
					holding.CAIP19 = asset.CAIP19
				}
			}
		}
		holdings = append(holdings, holding)
	}
	return holdings
}
