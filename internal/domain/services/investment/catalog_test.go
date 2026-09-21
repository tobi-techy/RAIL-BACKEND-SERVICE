package investment

import (
	"context"
	"testing"

	"github.com/rail-service/rail_service/internal/infrastructure/adapters/glider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	simUSDC = "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp/token:EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
	simSOL  = "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp/slip44:501"
)

func TestIngestAssetCatalogPreviewWritesNothing(t *testing.T) {
	store := newMemoryStore()
	provider := glider.NewSimulated(glider.SimulatedConfig{})

	report, err := IngestAssetCatalog(context.Background(), provider, &assetRepoAdapter{store: store}, CatalogIngestOptions{
		Collection: "curated",
		Confirm:    false,
	})
	require.NoError(t, err)

	assert.False(t, report.Written)
	assert.Equal(t, 1, report.Strategies, "one simulated public strategy")
	assert.Zero(t, report.Upserted)
	require.Len(t, report.Assets, 2, "USDC and SOL are referenced by the simulated strategy")

	// A preview must not touch the catalog.
	assert.Empty(t, store.assets, "a preview may not write")

	symbols := map[string]bool{}
	for _, asset := range report.Assets {
		symbols[asset.Symbol] = true
		assert.False(t, asset.AlreadyKnown)
		assert.Equal(t, "solana", asset.Chain)
	}
	assert.True(t, symbols["USDC"])
	assert.True(t, symbols["SOL"])
}

func TestIngestAssetCatalogWritesAllowlistedAssets(t *testing.T) {
	store := newMemoryStore()
	provider := glider.NewSimulated(glider.SimulatedConfig{})

	report, err := IngestAssetCatalog(context.Background(), provider, &assetRepoAdapter{store: store}, CatalogIngestOptions{
		Confirm: true,
	})
	require.NoError(t, err)

	assert.True(t, report.Written)
	assert.Equal(t, 2, report.Upserted)
	require.Len(t, store.assets, 2)

	for _, asset := range store.assets {
		assert.True(t, asset.Allowlisted, "an ingested asset must be usable, or the catalog is pointless")
		assert.False(t, asset.Prohibited)
		assert.Equal(t, "glider_discovery", asset.Source)
		// Nothing was invented: the provider publishes neither.
		assert.Equal(t, "unknown", asset.AssetClass)
	}

	// The ids are the provider's, verbatim.
	usdc, err := (&assetRepoAdapter{store: store}).GetByCAIP19(context.Background(), simUSDC)
	require.NoError(t, err)
	require.NotNil(t, usdc)
	assert.Equal(t, "USDC", usdc.Symbol)

	sol, err := (&assetRepoAdapter{store: store}).GetByCAIP19(context.Background(), simSOL)
	require.NoError(t, err)
	require.NotNil(t, sol)
	assert.Equal(t, "SOL", sol.Symbol)
}

func TestIngestAssetCatalogNeverClobbersCuratedAssets(t *testing.T) {
	store := newMemoryStore()
	repo := &assetRepoAdapter{store: store}
	provider := glider.NewSimulated(glider.SimulatedConfig{})

	_, err := IngestAssetCatalog(context.Background(), provider, repo, CatalogIngestOptions{Confirm: true})
	require.NoError(t, err)

	// An operator curates the USDC row.
	curated, err := repo.GetByCAIP19(context.Background(), simUSDC)
	require.NoError(t, err)
	require.NotNil(t, curated)
	curated.AssetClass = "cash"
	curated.Decimals = 6
	curated.Allowlisted = false // deliberately switched off
	require.NoError(t, repo.Upsert(context.Background(), curated))

	report, err := IngestAssetCatalog(context.Background(), provider, repo, CatalogIngestOptions{Confirm: true})
	require.NoError(t, err)
	assert.Zero(t, report.Upserted, "a re-run must not re-add known assets")
	assert.Equal(t, 2, report.Skipped)

	after, err := repo.GetByCAIP19(context.Background(), simUSDC)
	require.NoError(t, err)
	assert.False(t, after.Allowlisted, "the operator's decision must survive a re-run")
	assert.Equal(t, "cash", after.AssetClass, "curation must survive a re-run")
}

func TestIngestAssetCatalogRejectsMissingCollaborators(t *testing.T) {
	store := newMemoryStore()
	provider := glider.NewSimulated(glider.SimulatedConfig{})

	_, err := IngestAssetCatalog(context.Background(), nil, &assetRepoAdapter{store: store}, CatalogIngestOptions{})
	require.Error(t, err)

	_, err = IngestAssetCatalog(context.Background(), provider, nil, CatalogIngestOptions{})
	require.Error(t, err)
}

func TestSymbolAndChainDerivation(t *testing.T) {
	assert.Equal(t, "solana", chainFromCAIP19(simUSDC))
	assert.Equal(t, "eip155", chainFromCAIP19("eip155:8453/erc20:0xabc"))
	assert.Equal(t, "solana", chainFromCAIP19("bare-address-without-namespace"))

	assert.Equal(t, "EPJFWDD5AUFQSSQEM2QN1XZYBAPC8G4WEGGKZWYTDT1V", symbolFromCAIP19(simUSDC))
	assert.Equal(t, "501", symbolFromCAIP19(simSOL))
	assert.Equal(t, "UNKNOWN", symbolFromCAIP19(""))
}
