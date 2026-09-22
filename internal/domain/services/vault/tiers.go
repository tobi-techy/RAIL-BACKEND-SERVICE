package vault

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"gopkg.in/yaml.v3"
)

// tierFileLeg is one row of a checked-in tier file. The weight stays a string
// so "40" and "40.00" parse identically and float dust can never sneak in.
type tierFileLeg struct {
	CAIP19 string `yaml:"caip19"`
	Symbol string `yaml:"symbol"`
	Weight string `yaml:"weight"`
}

// tierFile is the shape of configs/vault/*.yaml.
type tierFile struct {
	Tier    string        `yaml:"tier"`
	Name    string        `yaml:"name"`
	Risk    string        `yaml:"risk"`
	Horizon string        `yaml:"horizon"`
	Legs    []tierFileLeg `yaml:"legs"`
}

// eligibleClasses is the allowlist per tier. Anything else — including
// "other", "unknown", "crypto" or a blank — is illegal in a tier file.
func eligibleClasses(tier entities.VaultTier) map[string]bool {
	switch tier {
	case entities.VaultTierConservative:
		return map[string]bool{"treasury": true, "rwa": true, "yield": true}
	case entities.VaultTierBalanced:
		return map[string]bool{"treasury": true, "rwa": true, "yield": true, "equity": true}
	case entities.VaultTierBold:
		return map[string]bool{"treasury": true, "rwa": true, "yield": true, "equity": true, "gold": true}
	default:
		return nil
	}
}

// DefaultTierFiles returns the checked-in tier files shipped with the service.
func DefaultTierFiles() []string {
	return []string{
		filepath.Join("configs", "vault", "steady.yaml"),
		filepath.Join("configs", "vault", "balanced.yaml"),
		filepath.Join("configs", "vault", "growth.yaml"),
	}
}

// BootstrapStrategies ensures the Rail-owned strategies for every tier exist.
//
// It prefers the checked-in tier files (tier.TierFiles, defaulting to the
// shipped configs/vault/*.yaml), validating every leg against the asset catalog
// before calling EnsureRailStrategy: the leg must exist in investment_assets,
// its class must be eligible for the tier, weights are strings summing to 100,
// and no leg may exceed 40%. Anything unresolved fails that tier closed and
// persists nothing for it, so CreateVault keeps returning plan-unavailable.
//
// Human-curated inline strategies (cfg.Strategies, from configs/config.yaml)
// still apply as a fallback when no tier file resolves. seed-catalog remains a
// preview-by-default ingest that never clobbers curated rows.
func (s *Service) BootstrapStrategies(ctx context.Context) error {
	if !s.cfg.Enabled {
		return nil
	}
	files := s.cfg.TierFiles
	if files == nil {
		// TierFiles nil (not empty) means "not configured" in tests and in
		// operators who curate the inline block: use it directly. Production
		// sets TierFiles explicitly, and the wiring defaults it to the
		// checked-in configs/vault/*.yaml files.
		for _, def := range s.cfg.Strategies {
			if _, err := s.engine.EnsureRailStrategy(ctx, entities.InvestmentRailStrategyRequest{
				Name:             def.Name,
				Risk:             def.Risk,
				Horizon:          def.Horizon,
				TargetAllocation: def.Legs,
			}); err != nil {
				s.log.Error("failed to ensure rail retirement strategy",
					"tier", string(def.Tier), "name", def.Name, "error", err)
				return err
			}
		}
		return nil
	}
	var firstErr error
	resolved := 0
	for _, path := range files {
		if err := s.bootstrapTierFile(ctx, path); err != nil {
			s.log.Error("retirement tier file not applied",
				"file", path, "error", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		resolved++
	}
	if resolved > 0 {
		return firstErr
	}
	// No tier file resolved: fall back to the inline strategies block so an
	// operator who curates configs/config.yaml directly still gets a bootstrap.
	for _, def := range s.cfg.Strategies {
		if _, err := s.engine.EnsureRailStrategy(ctx, entities.InvestmentRailStrategyRequest{
			Name:             def.Name,
			Risk:             def.Risk,
			Horizon:          def.Horizon,
			TargetAllocation: def.Legs,
		}); err != nil {
			s.log.Error("failed to ensure rail retirement strategy",
				"tier", string(def.Tier), "name", def.Name, "error", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (s *Service) bootstrapTierFile(ctx context.Context, path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read tier file: %w", err)
	}
	var file tierFile
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return fmt.Errorf("parse tier file: %w", err)
	}
	tier := entities.VaultTier(strings.TrimSpace(file.Tier))
	if !tier.Valid() {
		return fmt.Errorf("%w: tier %q is not offered", ErrValidation, file.Tier)
	}
	name := strings.TrimSpace(file.Name)
	if name == "" {
		name = tier.Label()
	}
	eligible := eligibleClasses(tier)
	if eligible == nil {
		return fmt.Errorf("%w: tier %q is not offered", ErrValidation, file.Tier)
	}
	if len(file.Legs) == 0 {
		return fmt.Errorf("%w: tier %q has no legs", ErrValidation, tier)
	}
	legs := make([]entities.InvestmentAllocationLeg, 0, len(file.Legs))
	total := decimal.Zero
	for i, leg := range file.Legs {
		caip19 := strings.TrimSpace(leg.CAIP19)
		if caip19 == "" || strings.Contains(caip19, "REPLACE") {
			return fmt.Errorf("%w: tier %q leg %d has no provider asset id yet",
				ErrStrategyUnavailable, tier, i)
		}
		asset, err := s.engine.GetAssetByCAIP19(ctx, caip19)
		if err != nil {
			return fmt.Errorf("%w: tier %q leg %d: %v", ErrStrategyUnavailable, tier, i, err)
		}
		if asset == nil {
			return fmt.Errorf("%w: tier %q leg %d is not in the asset catalog",
				ErrStrategyUnavailable, tier, i)
		}
		class := strings.ToLower(strings.TrimSpace(asset.AssetClass))
		if !eligible[class] {
			return fmt.Errorf("%w: tier %q does not hold class %q",
				ErrValidation, tier, asset.AssetClass)
		}
		if asset.Prohibited {
			return fmt.Errorf("%w: tier %q leg %d is prohibited", ErrValidation, tier, i)
		}
		weight, err := decimal.NewFromString(strings.TrimSpace(leg.Weight))
		if err != nil || !weight.GreaterThan(decimal.Zero) {
			return fmt.Errorf("%w: tier %q leg %d has an invalid weight",
				ErrValidation, tier, i)
		}
		if weight.GreaterThan(decimal.NewFromInt(40)) {
			return fmt.Errorf("%w: tier %q leg %d exceeds 40%%", ErrValidation, tier, i)
		}
		legs = append(legs, entities.InvestmentAllocationLeg{
			AssetID: asset.ID.String(),
			CAIP19:  asset.CAIP19,
			Symbol:  asset.Symbol,
			Weight:  weight,
		})
		total = total.Add(weight)
	}
	if !total.Equal(decimal.NewFromInt(100)) {
		return fmt.Errorf("%w: tier %q weights sum to %s, not 100",
			ErrValidation, tier, total.String())
	}

	risk := strings.TrimSpace(file.Risk)
	if risk == "" {
		risk = tier.Risk()
	}
	horizon := strings.TrimSpace(file.Horizon)
	if horizon == "" {
		horizon = tier.Horizon()
	}
	strategy, err := s.engine.EnsureRailStrategy(ctx, entities.InvestmentRailStrategyRequest{
		Name:             name,
		Risk:             risk,
		Horizon:          horizon,
		TargetAllocation: legs,
	})
	if err != nil {
		return err
	}
	binding := &entities.VaultTierBinding{
		Tier:           tier,
		RailStrategyID: strategy.ID,
		Version:        strategy.CurrentVersion,
	}
	binding.GliderStrategyID = strategy.GliderStrategyID
	if s.repo != nil {
		if err := s.repo.UpsertTier(ctx, binding); err != nil {
			return fmt.Errorf("persist tier binding: %w", err)
		}
	}
	return nil
}
