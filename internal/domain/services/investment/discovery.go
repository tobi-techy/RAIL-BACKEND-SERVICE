package investment

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// ---------------------------------------------------------------------------
// Discovery (spec §6, §11)
//
// Public strategies are exposed as "investor" profiles. Rail never invents
// investor activity: every field group carries a provenance label, and data the
// provider does not publish is reported as UNAVAILABLE rather than guessed.
// ---------------------------------------------------------------------------

// ListInvestors returns mirrorable public strategies with provenance labels.
func (s *Service) ListInvestors(ctx context.Context, collection, cursor string, limit int) (*entities.InvestmentInvestorListResponse, error) {
	if !s.cfg.Enabled {
		return nil, ErrDisabled
	}
	// Glider requires an explicit collection; default to the curated catalog
	// so a bare agent call surfaces editorial strategies instead of a 400.
	collection = strings.ToLower(strings.TrimSpace(collection))
	if collection != "top_performing" {
		collection = "curated"
	}
	if limit <= 0 {
		limit = 25
	}
	if limit > 100 {
		limit = 100
	}
	discovered, next, err := s.provider.DiscoverStrategies(ctx, collection, cursor, limit)
	if err != nil {
		return nil, fmt.Errorf("discover strategies: %w", s.mapProviderError(err))
	}

	response := &entities.InvestmentInvestorListResponse{
		Investors:  make([]entities.InvestmentInvestor, 0, len(discovered)),
		NextCursor: next,
		Collection: collection,
		Source:     "glider",
	}
	if len(discovered) == 0 {
		response.Note = "the provider returned no public strategies for this collection"
	}
	for _, item := range discovered {
		response.Investors = append(response.Investors, s.investorFromDiscovered(item))
	}
	return response, nil
}

// GetInvestor resolves one public strategy by id.
func (s *Service) GetInvestor(ctx context.Context, investorID string) (*entities.InvestmentInvestor, error) {
	if !s.cfg.Enabled {
		return nil, ErrDisabled
	}
	if strings.TrimSpace(investorID) == "" {
		return nil, fmt.Errorf("%w: investor_id is required", ErrValidationFailed)
	}

	// Discovery is the authoritative list of mirrorable strategies; search it
	// first so a non-public strategy can never be presented as mirrorable.
	cursor := ""
	for page := 0; page < 10; page++ {
		discovered, next, err := s.provider.DiscoverStrategies(ctx, "", cursor, 100)
		if err != nil {
			return nil, fmt.Errorf("discover strategies: %w", s.mapProviderError(err))
		}
		for _, item := range discovered {
			if item.StrategyID == investorID {
				investor := s.investorFromDiscovered(item)
				return &investor, nil
			}
		}
		if next == "" {
			break
		}
		cursor = next
	}
	return nil, fmt.Errorf("%w: that strategy is not published for mirroring", ErrNotFound)
}

// GetInvestorActivity explains what activity can be verified. An empty list is
// not the same as "no activity", so the response always carries provenance and,
// when we cannot see anything, the reason why.
func (s *Service) GetInvestorActivity(ctx context.Context, userID uuid.UUID, investorID string) (*entities.InvestmentInvestorActivityResponse, error) {
	response := &entities.InvestmentInvestorActivityResponse{
		InvestorID: investorID,
		Activity:   []entities.InvestmentActivityEntry{},
		Provenance: entities.InvestmentInvestorProvenance{
			Allocation:  entities.InvestmentProvenanceVerified,
			Performance: entities.InvestmentProvenanceUnavailable,
			Fees:        entities.InvestmentProvenanceUnavailable,
			Trades:      entities.InvestmentProvenanceUnavailable,
			Holders:     entities.InvestmentProvenanceUnavailable,
			Methodology: entities.InvestmentProvenanceUnavailable,
		},
		UnavailableReason: "the provider does not publish the underlying investor's individual trades, fees or holders; only the target allocation is observable",
	}

	// What we CAN verify is the user's own history against that strategy.
	if userID != uuid.Nil && s.executions != nil {
		executions, err := s.executions.ListByUser(ctx, userID, "", 50)
		if err != nil {
			return nil, fmt.Errorf("list executions: %w", err)
		}
		for _, execution := range executions {
			if execution == nil || execution.StrategyID == nil {
				continue
			}
			strategy, err := s.strategies.GetByID(ctx, *execution.StrategyID)
			if err != nil || strategy == nil || strategy.GliderStrategyID == nil || *strategy.GliderStrategyID != investorID {
				continue
			}
			response.Activity = append(response.Activity, entities.InvestmentActivityEntry{
				At:         execution.CreatedAt,
				Kind:       string(execution.Kind),
				Symbol:     execution.Symbol,
				Side:       execution.Side,
				Note:       fmt.Sprintf("your own %s order on this strategy (%s)", strings.ToLower(string(execution.Kind)), execution.Status),
				Provenance: entities.InvestmentProvenanceVerified,
			})
		}
		if len(response.Activity) > 0 {
			response.Provenance.Trades = entities.InvestmentProvenanceVerified
		}
	}
	return response, nil
}

// investorFromDiscovered maps provider discovery output onto the "investor"
// read model, labelling precisely what is observed versus unavailable.
func (s *Service) investorFromDiscovered(item entities.GliderDiscoveredStrategy) entities.InvestmentInvestor {
	investor := entities.InvestmentInvestor{
		InvestorID: item.StrategyID,
		Name:       item.Name,
		CanMirror:  item.CanMirror,
		Allocation: item.Allocation.Assets,
		CreatedAt:  item.CreatedAt,
		Source:     "glider",
		Provenance: entities.InvestmentInvestorProvenance{
			Allocation:  entities.InvestmentProvenanceVerified,
			Performance: entities.InvestmentProvenanceUnavailable,
			Fees:        entities.InvestmentProvenanceUnavailable,
			Trades:      entities.InvestmentProvenanceUnavailable,
			Holders:     entities.InvestmentProvenanceUnavailable,
			Methodology: entities.InvestmentProvenanceUnavailable,
		},
	}
	if item.Description != nil {
		investor.Description = *item.Description
	}

	metrics := entities.InvestmentInvestorMetrics{MaxAPY: item.MaxAPY}
	if item.Metrics.TVLUSD != nil {
		metrics.TVLUSD = item.Metrics.TVLUSD
	}
	if item.Metrics.PortfolioCount != nil {
		metrics.PortfolioCount = item.Metrics.PortfolioCount
		investor.Provenance.Holders = entities.InvestmentProvenanceVerified
	}
	if performance := item.Metrics.Performance; performance != nil {
		for _, window := range performance.Summary.Windows {
			metrics.Performance = append(metrics.Performance, entities.InvestmentPerformanceWindow{
				Window:        window.Window,
				PercentChange: window.PercentChange,
				Since:         window.Since,
			})
		}
		if len(metrics.Performance) > 0 {
			investor.Provenance.Performance = entities.InvestmentProvenanceVerified
			metrics.PerformanceNote = "provider-published performance of the strategy template; it is not a forecast and not your own return"
		}
	}
	if item.MaxAPY != nil {
		investor.Provenance.Performance = entities.InvestmentProvenanceVerified
	}
	if metrics.PerformanceNote == "" {
		metrics.PerformanceNote = "the provider publishes no performance history for this strategy, so none is shown"
	}
	investor.Metrics = metrics
	return investor
}
