package market

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type mockExplorerClient struct {
	assets         []entities.AlpacaAssetResponse
	quotes         map[string]*entities.MarketQuote
	bars           map[string][]*entities.MarketBar
	listAssetsCall int
	snapshotsCall  int
}

func (m *mockExplorerClient) ListAssets(ctx context.Context, query map[string]string) ([]entities.AlpacaAssetResponse, error) {
	m.listAssetsCall++
	return m.assets, nil
}

func (m *mockExplorerClient) GetAsset(ctx context.Context, symbolOrID string) (*entities.AlpacaAssetResponse, error) {
	for _, a := range m.assets {
		if a.Symbol == symbolOrID {
			asset := a
			return &asset, nil
		}
	}
	return nil, fmt.Errorf("not found")
}

func (m *mockExplorerClient) GetStockSnapshots(ctx context.Context, symbols []string) (map[string]*entities.MarketQuote, error) {
	m.snapshotsCall++
	result := make(map[string]*entities.MarketQuote)
	for _, s := range symbols {
		if q, ok := m.quotes[s]; ok {
			result[s] = q
		}
	}
	return result, nil
}

func (m *mockExplorerClient) GetStockSnapshot(ctx context.Context, symbol string) (*entities.MarketQuote, error) {
	if q, ok := m.quotes[symbol]; ok {
		return q, nil
	}
	return nil, fmt.Errorf("not found")
}

func (m *mockExplorerClient) GetBars(ctx context.Context, symbol, timeframe string, start, end time.Time) ([]*entities.MarketBar, error) {
	if bars, ok := m.bars[symbol]; ok {
		return bars, nil
	}
	return []*entities.MarketBar{}, nil
}

func TestExplorerService_ExploreFiltersAndPagination(t *testing.T) {
	now := time.Now().UTC()
	client := &mockExplorerClient{
		assets: []entities.AlpacaAssetResponse{
			{Symbol: "AAPL", Name: "Apple Inc", Class: entities.AlpacaAssetClassUSEquity, Exchange: "NASDAQ", Tradable: true, Fractionable: true, Marginable: true, Shortable: true},
			{Symbol: "QQQ", Name: "Invesco QQQ ETF", Class: entities.AlpacaAssetClassUSEquity, Exchange: "NASDAQ", Tradable: true, Fractionable: true, Marginable: true, Shortable: true},
			{Symbol: "VTI", Name: "Vanguard Total Stock Market ETF", Class: entities.AlpacaAssetClassUSEquity, Exchange: "ARCA", Tradable: true, Fractionable: true, Marginable: true, Shortable: true},
		},
		quotes: map[string]*entities.MarketQuote{
			"AAPL": {Symbol: "AAPL", Price: decimal.NewFromInt(180), ChangePct: decimal.NewFromFloat(1.2), Timestamp: now},
			"QQQ":  {Symbol: "QQQ", Price: decimal.NewFromInt(410), ChangePct: decimal.NewFromFloat(0.3), Timestamp: now},
			"VTI":  {Symbol: "VTI", Price: decimal.NewFromInt(250), ChangePct: decimal.NewFromFloat(-0.4), Timestamp: now},
		},
	}

	svc := NewExplorerService(client, zap.NewNop(), "")

	resp, err := svc.Explore(context.Background(), entities.MarketExploreFilters{
		Types:     []entities.MarketInstrumentType{entities.MarketInstrumentTypeETF},
		SortBy:    "price",
		SortOrder: "desc",
		Page:      1,
		PageSize:  1,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, resp.Items, 1)

	assert.Equal(t, "QQQ", resp.Items[0].Symbol)
	assert.NotEmpty(t, resp.Items[0].Description)
	assert.Equal(t, int64(2), resp.Pagination.TotalItems)
	assert.True(t, resp.Pagination.HasNext)
	require.NotEmpty(t, resp.Facets.Types)
	assert.Equal(t, "etf", resp.Facets.Types[0].Value)
}

func TestExplorerService_GetInstrumentWithBars(t *testing.T) {
	now := time.Now().UTC()
	client := &mockExplorerClient{
		assets: []entities.AlpacaAssetResponse{
			{Symbol: "AAPL", Name: "Apple Inc", Class: entities.AlpacaAssetClassUSEquity, Exchange: "NASDAQ", Tradable: true},
		},
		quotes: map[string]*entities.MarketQuote{
			"AAPL": {Symbol: "AAPL", Price: decimal.NewFromInt(180), Timestamp: now},
		},
		bars: map[string][]*entities.MarketBar{
			"AAPL": {
				{Symbol: "AAPL", Close: decimal.NewFromInt(170), Timestamp: now.AddDate(0, 0, -2)},
				{Symbol: "AAPL", Close: decimal.NewFromInt(175), Timestamp: now.AddDate(0, 0, -1)},
				{Symbol: "AAPL", Close: decimal.NewFromInt(180), Timestamp: now},
			},
		},
	}

	svc := NewExplorerService(client, zap.NewNop(), "")
	resp, err := svc.GetInstrument(context.Background(), "aapl", true, "1Day", 2)
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, "AAPL", resp.Instrument.Symbol)
	assert.NotEmpty(t, resp.Instrument.Description)
	require.Len(t, resp.Bars, 2)
	assert.Equal(t, "175", resp.Bars[0].Close.String())
}

func TestExplorerService_CachesAssetsAndSnapshots(t *testing.T) {
	now := time.Now().UTC()
	client := &mockExplorerClient{
		assets: []entities.AlpacaAssetResponse{
			{Symbol: "AAPL", Name: "Apple Inc", Class: entities.AlpacaAssetClassUSEquity, Exchange: "NASDAQ", Tradable: true},
		},
		quotes: map[string]*entities.MarketQuote{
			"AAPL": {Symbol: "AAPL", Price: decimal.NewFromInt(180), Timestamp: now},
		},
	}

	svc := NewExplorerService(client, zap.NewNop(), "")
	_, err := svc.Explore(context.Background(), entities.MarketExploreFilters{})
	require.NoError(t, err)
	_, err = svc.Explore(context.Background(), entities.MarketExploreFilters{})
	require.NoError(t, err)

	assert.Equal(t, 1, client.listAssetsCall)
	assert.Equal(t, 1, client.snapshotsCall)
}

func TestExplorerService_InjectsLogoURLWhenConfigured(t *testing.T) {
	t.Setenv("MARKET_LOGO_PROVIDER", "logo_dev")
	t.Setenv("MARKET_LOGO_BASE_URL", "https://img.logo.dev")
	t.Setenv("LOGO_DEV_PUBLISHABLE_KEY", "pk_test_123")
	t.Setenv("LOGO_DEV_SIZE", "128")
	t.Setenv("LOGO_DEV_FORMAT", "png")
	t.Setenv("LOGO_DEV_THEME", "auto")

	now := time.Now().UTC()
	client := &mockExplorerClient{
		assets: []entities.AlpacaAssetResponse{
			{Symbol: "AAPL", Name: "Apple Inc", Class: entities.AlpacaAssetClassUSEquity, Exchange: "NASDAQ", Tradable: true},
		},
		quotes: map[string]*entities.MarketQuote{
			"AAPL": {Symbol: "AAPL", Price: decimal.NewFromInt(180), Timestamp: now},
		},
	}

	svc := NewExplorerService(client, zap.NewNop(), "")
	resp, err := svc.Explore(context.Background(), entities.MarketExploreFilters{})
	require.NoError(t, err)
	require.Len(t, resp.Items, 1)
	require.NotNil(t, resp.Items[0].LogoURL)
	assert.Contains(t, *resp.Items[0].LogoURL, "https://img.logo.dev/ticker/AAPL")
	assert.Contains(t, *resp.Items[0].LogoURL, "token=pk_test_123")
}
