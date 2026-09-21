package investment

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// CatalogIngestOptions configures a one-shot provider asset-catalog ingest.
type CatalogIngestOptions struct {
	// Collection is the provider discovery collection. The provider requires an
	// explicit one; anything other than "top_performing" falls back to "curated".
	Collection string
	// Limit caps how many discovered strategies are scanned (1-100).
	Limit int
	// Confirm writes the catalog. When false the ingest is a read-only preview:
	// it discovers and reports, and changes nothing.
	Confirm bool
}

// IngestAssetCatalog discovers the provider's published strategies and seeds
// Rail's asset catalog from the assets those strategies actually reference.
//
// This exists because the catalog is a hard prerequisite: the validator refuses
// any allocation leg that does not resolve to an allowlisted catalog asset
// ("could not resolve asset ...; it is not in the supported asset list"), so no
// strategy — retirement or otherwise — can be created until the catalog holds
// real provider asset ids.
//
// The ids come from the provider, never from Rail. Rail does not invent CAIP-19
// identifiers, which is exactly why this step is a discovery call rather than a
// hand-written list.
//
// It never overwrites an existing row: once an operator has curated an asset
// (allowlisted, class, decimals, prohibited), a re-run leaves it alone.
func IngestAssetCatalog(
	ctx context.Context,
	provider Provider,
	assets AssetRepository,
	opts CatalogIngestOptions,
) (*entities.InvestmentAssetCatalogReport, error) {
	if provider == nil {
		return nil, fmt.Errorf("catalog ingest: no provider configured")
	}
	if assets == nil {
		return nil, fmt.Errorf("catalog ingest: no asset catalog configured")
	}

	collection := strings.ToLower(strings.TrimSpace(opts.Collection))
	if collection != "top_performing" {
		collection = "curated"
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 50 {
		limit = 50
	}

	discovered, next, err := provider.DiscoverStrategies(ctx, collection, "", limit)
	if err != nil {
		return nil, fmt.Errorf("discover strategies: %w", err)
	}

	report := &entities.InvestmentAssetCatalogReport{
		Collection: collection,
		Strategies: len(discovered),
		Written:    opts.Confirm,
		Assets:     []entities.InvestmentCatalogAsset{},
	}
	if len(discovered) == 0 {
		report.Note = "the provider returned no public strategies for this collection, so there was nothing to ingest"
		return report, nil
	}
	if strings.TrimSpace(next) != "" {
		report.Note = "the provider returned more than one page; only the first page was ingested"
	}

	seen := map[string]bool{}
	for _, strategy := range discovered {
		for _, leg := range strategy.Allocation.Assets {
			caip19 := strings.TrimSpace(firstNonEmpty(leg.CAIP19, leg.AssetID))
			if caip19 == "" || seen[caip19] {
				continue
			}
			seen[caip19] = true

			entry := entities.InvestmentCatalogAsset{
				CAIP19:     caip19,
				Symbol:     strings.TrimSpace(leg.Symbol),
				Chain:      chainFromCAIP19(caip19),
				AssetClass: "unknown",
			}
			if entry.Symbol == "" {
				entry.Symbol = symbolFromCAIP19(caip19)
			}
			if len(entry.Symbol) > 32 {
				entry.Symbol = entry.Symbol[:32]
			}

			existing, err := assets.GetByCAIP19(ctx, caip19)
			if err != nil {
				return nil, fmt.Errorf("look up asset %s: %w", caip19, err)
			}
			if existing != nil {
				// Already curated: report what we have, change nothing.
				entry.AlreadyKnown = true
				entry.Symbol = firstNonEmpty(existing.Symbol, entry.Symbol)
				entry.Chain = firstNonEmpty(existing.Chain, entry.Chain)
				entry.AssetClass = firstNonEmpty(existing.AssetClass, entry.AssetClass)
				report.Skipped++
				report.Assets = append(report.Assets, entry)
				continue
			}

			// The discovery payload carries a symbol; cap it to the DB limit.
			symbolForDB := entry.Symbol
			if len(symbolForDB) > 32 {
				symbolForDB = symbolForDB[:32]
			}

			if opts.Confirm {
				// The discovery payload carries no decimals and no asset class, so
				// neither is invented: class is recorded as "unknown" and decimals
				// takes the column default. Both are harmless for the retirement
				// vault, which funds and rebalances and never sizes a raw order,
				// but must be curated before the order path is used for the asset.
				if err := assets.Upsert(ctx, &entities.InvestmentAsset{
					ID:          uuid.New(),
					CAIP19:      caip19,
					Symbol:      symbolForDB,
					AssetClass:  "unknown",
					Chain:       entry.Chain,
					Decimals:    6,
					Allowlisted: true,
					Source:      "glider_discovery",
				}); err != nil {
					return nil, fmt.Errorf("upsert asset %s: %w", caip19, err)
				}
				report.Upserted++
			}
			report.Assets = append(report.Assets, entry)
		}
	}
	return report, nil
}

// chainFromCAIP19 reads the leading namespace of a CAIP-19 id.
func chainFromCAIP19(caip19 string) string {
	if idx := strings.Index(caip19, ":"); idx > 0 {
		return caip19[:idx]
	}
	return "solana"
}

// symbolFromCAIP19 derives a readable symbol from an id's tail when the provider
// did not publish one.
func symbolFromCAIP19(caip19 string) string {
	tail := caip19
	if idx := strings.LastIndex(tail, ":"); idx >= 0 {
		tail = tail[idx+1:]
	}
	if idx := strings.LastIndex(tail, "/"); idx >= 0 {
		tail = tail[idx+1:]
	}
	tail = strings.ToUpper(strings.TrimSpace(tail))
	if tail == "" {
		return "UNKNOWN"
	}
	return tail
}
