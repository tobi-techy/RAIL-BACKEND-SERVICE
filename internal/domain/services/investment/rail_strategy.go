package investment

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// EnsureRailStrategy creates a Rail-owned strategy if one with the same name
// does not already exist, and returns it.
//
// Rail-owned strategies are the only thing users can enroll a retirement vault
// into: the allocation is Rail's, not the user's, and it is never mutable from
// a client or an agent. The method is idempotent by name so a bootstrap pass can
// run on every startup.
func (s *Service) EnsureRailStrategy(ctx context.Context, req entities.InvestmentRailStrategyRequest) (*entities.InvestmentStrategy, error) {
	if !s.cfg.Enabled {
		return nil, ErrDisabled
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, fmt.Errorf("%w: a Rail strategy needs a name", ErrValidationFailed)
	}
	if len(req.TargetAllocation) == 0 {
		return nil, fmt.Errorf("%w: a Rail strategy needs an allocation", ErrValidationFailed)
	}

	existing, err := s.ListRailStrategies(ctx)
	if err != nil {
		return nil, err
	}
	for _, strategy := range existing {
		if strategy != nil && strings.EqualFold(strings.TrimSpace(strategy.Name), name) {
			return strategy, nil
		}
	}

	limits := s.defaultLimits()
	report, err := s.validator.Validate(ctx, ValidationInput{
		Legs:      req.TargetAllocation,
		AmountUSD: decimal.Zero,
		Limits:    limits,
	})
	if err != nil {
		return nil, err
	}
	if !report.Valid {
		return nil, fmt.Errorf("%w: rail strategy %q: %s", ErrValidationFailed, name, firstViolation(report))
	}

	created, err := s.provider.CreateStrategy(ctx, entities.GliderStrategyInput{
		Name:        name,
		Allocation:  entities.GliderAllocation{Assets: report.NormalizedAllocation},
		Schedule:    &entities.GliderSchedule{Type: "interval", Frequency: "monthly"},
		Preferences: s.preferencesFor(entities.InvestmentExecutionRules{}),
	})
	if err != nil {
		return nil, fmt.Errorf("create provider rail strategy: %w", s.mapProviderError(err))
	}

	now := s.nowOr()
	strategy := &entities.InvestmentStrategy{
		ID:               uuid.New(),
		UserID:           nil,
		OwnerType:        entities.InvestmentOwnerRail,
		GliderStrategyID: &created.StrategyID,
		Name:             name,
		Risk:             req.Risk,
		Horizon:          req.Horizon,
		Status:           entities.InvestmentStrategyActive,
		CurrentVersion:   1,
		CreatedBy:        entities.InvestmentActorSystem,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := s.strategies.Create(ctx, strategy); err != nil {
		return nil, fmt.Errorf("store rail strategy: %w", err)
	}

	version := &entities.InvestmentStrategyVersion{
		ID:               uuid.New(),
		StrategyID:       strategy.ID,
		Version:          1,
		TargetAllocation: report.NormalizedAllocation,
		Risk:             req.Risk,
		Horizon:          req.Horizon,
		RebalanceRules:   entities.InvestmentRebalanceRules{Type: "contribution", Frequency: "monthly", PreferContributions: true},
		ValidationReport: report,
		CreatedBy:        entities.InvestmentActorSystem,
		CreatedAt:        now,
	}
	if created.Version > 0 {
		gliderVersion := created.Version
		version.GliderStrategyVersion = &gliderVersion
	}
	if err := s.strategies.CreateVersion(ctx, version); err != nil {
		return nil, fmt.Errorf("store rail strategy version: %w", err)
	}

	s.log.Info("rail investment strategy created",
		"strategy_id", strategy.ID.String(),
		"name", strategy.Name,
		"provider_strategy_id", created.StrategyID)
	return strategy, nil
}

// ListRailStrategies returns every Rail-owned strategy.
func (s *Service) ListRailStrategies(ctx context.Context) ([]*entities.InvestmentStrategy, error) {
	if s.strategies == nil {
		return nil, nil
	}
	strategies, err := s.strategies.ListByOwnerType(ctx, entities.InvestmentOwnerRail, 100)
	if err != nil {
		return nil, fmt.Errorf("list rail strategies: %w", err)
	}
	return strategies, nil
}

// LinkVaultEnrollment records which retirement vault a portfolio belongs to.
// After this, the portfolio can only be withdrawn from through the vault.
func (s *Service) LinkVaultEnrollment(ctx context.Context, userID, enrollmentID, vaultID uuid.UUID) error {
	enrollment, err := s.enrollments.GetByID(ctx, enrollmentID)
	if err != nil {
		return fmt.Errorf("get enrollment: %w", err)
	}
	if enrollment == nil || enrollment.UserID != userID {
		return ErrNotFound
	}
	enrollment.VaultID = &vaultID
	enrollment.UpdatedAt = s.nowOr()
	if err := s.enrollments.Update(ctx, enrollment); err != nil {
		return fmt.Errorf("link vault enrollment: %w", err)
	}
	return nil
}
