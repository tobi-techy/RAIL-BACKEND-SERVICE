package publictrades

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestParseAmountMid(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"$1,001 - $15,000", "8000.5"},
		{"$500,001 - $1,000,000", "750000.5"},
		{"$50,000,000 +", "50000000"},
		{"garbage", "0"},
	}
	for _, tc := range cases {
		got := parseAmountMid(tc.in)
		require.Equal(t, tc.want, got.String(), "input %q", tc.in)
	}
}

func TestParseSide(t *testing.T) {
	require.Equal(t, "buy", parseSide("Purchase"))
	require.Equal(t, "sell", parseSide("Sale (Full)"))
	require.Equal(t, "sell", parseSide("sale_partial"))
	require.Equal(t, "", parseSide("exchange"))
}

func TestBuildTradeFiltersNonStockRows(t *testing.T) {
	_, ok := buildTrade("house", "Nancy Pelosi", "--", "US Treasury", "purchase", "$1,001 - $15,000", "2026-06-01", "2026-06-20")
	require.False(t, ok, "no ticker must be rejected")

	_, ok = buildTrade("house", "Nancy Pelosi", "NVDA", "NVIDIA", "exchange", "$1,001 - $15,000", "2026-06-01", "2026-06-20")
	require.False(t, ok, "unknown side must be rejected")

	trade, ok := buildTrade("house", "Nancy Pelosi", "nvda", "NVIDIA", "Purchase", "$1,001 - $15,000", "2026-06-01", "2026-06-20")
	require.True(t, ok)
	require.Equal(t, "NVDA", trade.Ticker)
	require.Equal(t, "nancy pelosi", trade.FigureKey)
	require.Equal(t, "buy", trade.Side)
	require.Equal(t, "8000.5", trade.AmountMid.String())
	require.NotEmpty(t, trade.Ref)
}

func TestBuildTradeRefIsStable(t *testing.T) {
	a, _ := buildTrade("house", "Nancy Pelosi", "NVDA", "NVIDIA", "purchase", "$1,001 - $15,000", "2026-06-01", "2026-06-20")
	b, _ := buildTrade("house", "Nancy Pelosi", "NVDA", "NVIDIA", "purchase", "$1,001 - $15,000", "2026-06-01", "2026-06-20")
	c, _ := buildTrade("house", "Nancy Pelosi", "NVDA", "NVIDIA", "purchase", "$1,001 - $15,000", "2026-06-02", "2026-06-20")
	require.Equal(t, a.Ref, b.Ref)
	require.NotEqual(t, a.Ref, c.Ref)
}

func TestUnconfiguredClientErrors(t *testing.T) {
	client := NewClient(Config{}, zap.NewNop())
	_, err := client.SearchFigures(context.Background(), "pelosi")
	require.ErrorContains(t, err, "FMP_API_KEY")
}

func TestSearchAndFigureTrades(t *testing.T) {
	houseRows := []fmpDisclosure{
		{Symbol: "NVDA", DisclosureDate: "2026-06-20", TransactionDate: "2026-06-01", FirstName: "Nancy", LastName: "Pelosi", AssetDescription: "NVIDIA", AssetType: "Stock", Type: "Purchase", Amount: "$500,001 - $1,000,000"},
		{Symbol: "AAPL", DisclosureDate: "2026-06-10", TransactionDate: "2026-05-15", FirstName: "Nancy", LastName: "Pelosi", AssetDescription: "Apple", AssetType: "Stock", Type: "Sale (Full)", Amount: "$100,001 - $250,000"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NotEmpty(t, r.URL.Query().Get("apikey"))
		switch {
		case strings.Contains(r.URL.Path, "house-trades-by-name"):
			_ = json.NewEncoder(w).Encode(houseRows)
		default:
			_ = json.NewEncoder(w).Encode([]fmpDisclosure{})
		}
	}))
	defer srv.Close()

	client := NewClient(Config{APIKey: "test", BaseURL: srv.URL}, zap.NewNop())

	figures, err := client.SearchFigures(context.Background(), "Pelosi")
	require.NoError(t, err)
	require.Len(t, figures, 1)
	require.Equal(t, "Nancy Pelosi", figures[0].Name)
	require.Equal(t, "nancy pelosi", figures[0].Key)
	require.Equal(t, "house", figures[0].Chamber)

	trades, err := client.GetFigureTrades(context.Background(), "nancy pelosi", time.Time{}, 10)
	require.NoError(t, err)
	require.Len(t, trades, 2)
	require.Equal(t, "NVDA", trades[0].Ticker, "newest disclosure first")

	since, _ := time.Parse("2006-01-02", "2026-06-15")
	recent, err := client.GetFigureTrades(context.Background(), "nancy pelosi", since, 10)
	require.NoError(t, err)
	require.Len(t, recent, 1)
	require.Equal(t, "buy", recent[0].Side)
}

func TestListPopularFigures(t *testing.T) {
	senateRows := []fmpDisclosure{
		{Symbol: "TSLA", DisclosureDate: "2026-06-25", TransactionDate: "2026-06-05", FirstName: "Some", LastName: "Senator", AssetType: "Stock", Type: "Purchase", Amount: "$1,001 - $15,000"},
		{Symbol: "MSFT", DisclosureDate: "2026-06-26", TransactionDate: "2026-06-06", FirstName: "Some", LastName: "Senator", AssetType: "Stock", Type: "Purchase", Amount: "$1,001 - $15,000"},
		{Symbol: "NVDA", DisclosureDate: "2026-06-20", TransactionDate: "2026-06-01", FirstName: "Other", LastName: "Member", AssetType: "Stock", Type: "Purchase", Amount: "$1,001 - $15,000"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "senate-latest") && r.URL.Query().Get("page") == "0" {
			_ = json.NewEncoder(w).Encode(senateRows)
			return
		}
		_ = json.NewEncoder(w).Encode([]fmpDisclosure{})
	}))
	defer srv.Close()

	client := NewClient(Config{APIKey: "test", BaseURL: srv.URL}, zap.NewNop())
	figures, err := client.ListPopularFigures(context.Background(), 10)
	require.NoError(t, err)
	require.Len(t, figures, 2)
	require.Equal(t, "Some Senator", figures[0].Name, "most active first")
	require.Equal(t, 2, figures[0].TradeCount)
}
