package common

import (
	"testing"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/stretchr/testify/assert"
)

func TestBuildAssetDescriptionIncludesCoreTraits(t *testing.T) {
	asset := entities.AlpacaAssetResponse{
		Symbol:       "QQQ",
		Name:         "Invesco QQQ ETF",
		Exchange:     "NASDAQ",
		Tradable:     true,
		Fractionable: true,
	}

	description := buildAssetDescription(asset)
	assert.Contains(t, description, "Invesco QQQ ETF")
	assert.Contains(t, description, "ETF")
	assert.Contains(t, description, "NASDAQ")
	assert.Contains(t, description, "fractional")
}

func TestEnrichAssetDescriptionsSetsDescriptionOnAllItems(t *testing.T) {
	assets := []entities.AlpacaAssetResponse{
		{Symbol: "AAPL", Name: "Apple Inc", Exchange: "NASDAQ", Tradable: true},
		{Symbol: "SPY", Name: "SPDR S&P 500 ETF", Exchange: "ARCA", Tradable: true, Fractionable: true},
	}

	logos := &assetLogoResolver{
		provider: "logo_dev",
		baseURL:  "https://img.logo.dev",
		token:    "pk_test_123",
		size:     128,
		format:   "png",
		theme:    "auto",
		cache:    map[string]cachedAssetLogo{},
	}
	enriched := enrichAssetMetadata(assets, logos)
	assert.Len(t, enriched, 2)
	assert.NotEmpty(t, enriched[0].Description)
	assert.NotEmpty(t, enriched[1].Description)
	assert.NotNil(t, enriched[0].LogoURL)
	assert.Contains(t, *enriched[0].LogoURL, "img.logo.dev/ticker/AAPL")
	assert.Contains(t, *enriched[0].LogoURL, "token=pk_test_123")
}
