package investment

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// ---------------------------------------------------------------------------
// Real Glider reads + metadata mutations.
//
// Every method below calls the provider endpoint named in its comment (see
// api.txt). Nothing is estimated locally: preferences, fees, schedules,
// versions, performance curves, sector exposure and breakdowns all come from
// Glider. Access is scoped to strategies/enrollments the caller owns.
// ---------------------------------------------------------------------------

func (s *Service) resolveOwnedStrategy(ctx context.Context, userID, strategyID uuid.UUID) (*entities.InvestmentStrategy, error) {
	strategy, err := s.strategies.GetByID(ctx, strategyID)
	if err != nil {
		return nil, fmt.Errorf("get strategy: %w", err)
	}
	if strategy == nil || !s.canAccess(strategy, userID) {
		return nil, ErrNotFound
	}
	return strategy, nil
}

func (s *Service) gliderIDOf(strategy *entities.InvestmentStrategy) (string, error) {
	if strategy.GliderStrategyID == nil || strings.TrimSpace(*strategy.GliderStrategyID) == "" {
		return "", fmt.Errorf("%w: this strategy has no provider binding", ErrValidationFailed)
	}
	return *strategy.GliderStrategyID, nil
}

// GetProviderStrategyVersions returns the live version history, newest first.
// GET /strategies/{strategyId}/versions — isHead marks the active version.
func (s *Service) GetProviderStrategyVersions(ctx context.Context, userID, strategyID uuid.UUID, cursor string, limit int) ([]entities.GliderStrategyVersion, string, error) {
	strategy, err := s.resolveOwnedStrategy(ctx, userID, strategyID)
	if err != nil {
		return nil, "", err
	}
	gliderID, err := s.gliderIDOf(strategy)
	if err != nil {
		return nil, "", err
	}
	versions, next, err := s.provider.ListStrategyVersions(ctx, gliderID, cursor, limit)
	if err != nil {
		return nil, "", s.mapProviderError(err)
	}
	return versions, next, nil
}

// GetStrategyPerformance returns the provider TWR target-allocation curve.
// GET /strategies/{strategyId}/performance — template return, not the user's
// own money outcome (see portfolio performance for MWR).
func (s *Service) GetStrategyPerformance(ctx context.Context, userID, strategyID uuid.UUID) (*entities.GliderStrategyPerformance, error) {
	strategy, err := s.resolveOwnedStrategy(ctx, userID, strategyID)
	if err != nil {
		return nil, err
	}
	gliderID, err := s.gliderIDOf(strategy)
	if err != nil {
		return nil, err
	}
	perf, err := s.provider.GetStrategyPerformance(ctx, gliderID)
	if err != nil {
		return nil, s.mapProviderError(err)
	}
	return perf, nil
}

// GetStrategySchedule returns the configured cadence (type+frequency).
// GET /strategies/{strategyId}/schedule — null means no cadence on record;
// runtime nextDueAt/lastRebalanceAt live on the portfolio, not here.
func (s *Service) GetStrategySchedule(ctx context.Context, userID, strategyID uuid.UUID) (*entities.GliderSchedule, error) {
	strategy, err := s.resolveOwnedStrategy(ctx, userID, strategyID)
	if err != nil {
		return nil, err
	}
	gliderID, err := s.gliderIDOf(strategy)
	if err != nil {
		return nil, err
	}
	sched, err := s.provider.GetStrategySchedule(ctx, gliderID)
	if err != nil {
		return nil, s.mapProviderError(err)
	}
	return sched, nil
}

// GetStrategyPreferences returns stored swap overrides (null = default).
// GET /strategies/{strategyId}/preferences.
func (s *Service) GetStrategyPreferences(ctx context.Context, userID, strategyID uuid.UUID) (*entities.GliderPreferences, error) {
	strategy, err := s.resolveOwnedStrategy(ctx, userID, strategyID)
	if err != nil {
		return nil, err
	}
	gliderID, err := s.gliderIDOf(strategy)
	if err != nil {
		return nil, err
	}
	prefs, err := s.provider.GetStrategyPreferences(ctx, gliderID)
	if err != nil {
		return nil, s.mapProviderError(err)
	}
	return prefs, nil
}

// GetStrategyFees returns the per-strategy fee override.
// GET /strategies/{strategyId}/fees — {swapBps: int|null}.
func (s *Service) GetStrategyFees(ctx context.Context, userID, strategyID uuid.UUID) (*entities.GliderFeeView, error) {
	strategy, err := s.resolveOwnedStrategy(ctx, userID, strategyID)
	if err != nil {
		return nil, err
	}
	gliderID, err := s.gliderIDOf(strategy)
	if err != nil {
		return nil, err
	}
	fees, err := s.provider.GetStrategyFees(ctx, gliderID)
	if err != nil {
		return nil, s.mapProviderError(err)
	}
	return fees, nil
}

// PatchStrategyMetadata patches display fields only (name/description/isPublic).
// PATCH /strategies/{strategyId} — allocation goes through versions.
func (s *Service) PatchStrategyMetadata(ctx context.Context, userID, strategyID uuid.UUID, patch entities.GliderStrategyPatch) (*entities.GliderStrategy, error) {
	strategy, err := s.resolveOwnedStrategy(ctx, userID, strategyID)
	if err != nil {
		return nil, err
	}
	if !s.canMutate(strategy, userID) {
		return nil, ErrNotFound
	}
	gliderID, err := s.gliderIDOf(strategy)
	if err != nil {
		return nil, err
	}
	updated, err := s.provider.PatchStrategy(ctx, gliderID, patch)
	if err != nil {
		return nil, s.mapProviderError(err)
	}
	if patch.Name != nil {
		strategy.Name = strings.TrimSpace(*patch.Name)
	}
	if patch.Description != nil {
		strategy.Description = strings.TrimSpace(*patch.Description)
	}
	strategy.UpdatedAt = s.nowOr()
	if err := s.strategies.Update(ctx, strategy); err != nil {
		return nil, fmt.Errorf("store strategy: %w", err)
	}
	return updated, nil
}

// RenameEnrollment renames a portfolio's display name.
// PATCH /portfolios/{portfolioId} — {portfolioName} only, strict body.
func (s *Service) RenameEnrollment(ctx context.Context, userID, enrollmentID uuid.UUID, name string) (*entities.GliderPortfolio, error) {
	enrollment, err := s.enrollments.GetByID(ctx, enrollmentID)
	if err != nil {
		return nil, fmt.Errorf("get enrollment: %w", err)
	}
	if enrollment == nil || enrollment.UserID != userID {
		return nil, ErrNotFound
	}
	if strings.TrimSpace(name) == "" || len([]rune(name)) > 64 {
		return nil, fmt.Errorf("%w: portfolio name must be 1-64 characters", ErrValidationFailed)
	}
	patched, err := s.provider.PatchPortfolio(ctx, enrollment.GliderPortfolioID, entities.GliderPortfolioPatch{PortfolioName: &name})
	if err != nil {
		return nil, s.mapProviderError(err)
	}
	// The display name lives provider-side only (no local name column);
	// touch the row so sync/audit timelines reflect the rename.
	enrollment.UpdatedAt = s.nowOr()
	if err := s.enrollments.Update(ctx, enrollment); err != nil {
		return nil, fmt.Errorf("store enrollment: %w", err)
	}
	return patched, nil
}

func (s *Service) resolveOwnedEnrollment(ctx context.Context, userID, enrollmentID uuid.UUID) (*entities.InvestmentEnrollment, error) {
	enrollment, err := s.enrollments.GetByID(ctx, enrollmentID)
	if err != nil {
		return nil, fmt.Errorf("get enrollment: %w", err)
	}
	if enrollment == nil || enrollment.UserID != userID {
		return nil, ErrNotFound
	}
	return enrollment, nil
}

// GetEnrollmentPerformance returns the daily money curve for one enrollment.
// GET /portfolios/{id}/performance?returnMethod=MWR|TWR (default MWR).
func (s *Service) GetEnrollmentPerformance(ctx context.Context, userID, enrollmentID uuid.UUID, returnMethod string) (*entities.GliderPortfolioPerformance, error) {
	enrollment, err := s.resolveOwnedEnrollment(ctx, userID, enrollmentID)
	if err != nil {
		return nil, err
	}
	method := strings.ToUpper(strings.TrimSpace(returnMethod))
	if method == "" {
		method = "MWR"
	}
	if method != "MWR" && method != "TWR" {
		return nil, fmt.Errorf("%w: returnMethod must be MWR or TWR", ErrValidationFailed)
	}
	perf, err := s.provider.GetPortfolioPerformance(ctx, enrollment.GliderPortfolioID, method)
	if err != nil {
		return nil, s.mapProviderError(err)
	}
	return perf, nil
}

// GetEnrollmentSectorExposure returns equity exposure by canonical sector.
// GET /portfolios/{id}/sector-exposure.
func (s *Service) GetEnrollmentSectorExposure(ctx context.Context, userID, enrollmentID uuid.UUID) (*entities.GliderSectorExposure, error) {
	enrollment, err := s.resolveOwnedEnrollment(ctx, userID, enrollmentID)
	if err != nil {
		return nil, err
	}
	exposure, err := s.provider.GetSectorExposure(ctx, enrollment.GliderPortfolioID)
	if err != nil {
		return nil, s.mapProviderError(err)
	}
	return exposure, nil
}

// GetAllocationBreakdown aggregates arbitrary holdings into Glider buckets.
// POST /assets/allocation-breakdown.
func (s *Service) GetAllocationBreakdown(ctx context.Context, in entities.GliderBreakdownInput) (json.RawMessage, error) {
	if len(in.Holdings) == 0 {
		return nil, fmt.Errorf("%w: holdings must not be empty", ErrValidationFailed)
	}
	out, err := s.provider.GetAllocationBreakdown(ctx, in)
	if err != nil {
		return nil, s.mapProviderError(err)
	}
	return out, nil
}

// PrepareChainActivation returns the owner-signable message for new chains.
// POST /portfolios/{id}/chains/signature — EVM only; Solana portfolios get a
// provider 400 which surfaces as an unsupported error, never a fake success.
func (s *Service) PrepareChainActivation(ctx context.Context, userID, enrollmentID uuid.UUID, chainIDs []int) (*entities.GliderChainActivationMessage, error) {
	enrollment, err := s.resolveOwnedEnrollment(ctx, userID, enrollmentID)
	if err != nil {
		return nil, err
	}
	if len(chainIDs) == 0 {
		return nil, fmt.Errorf("%w: chainIds must not be empty", ErrValidationFailed)
	}
	msg, err := s.provider.PrepareChainActivation(ctx, enrollment.GliderPortfolioID, entities.GliderChainActivationInput{ChainIDs: chainIDs})
	if err != nil {
		return nil, s.mapProviderError(err)
	}
	return msg, nil
}
