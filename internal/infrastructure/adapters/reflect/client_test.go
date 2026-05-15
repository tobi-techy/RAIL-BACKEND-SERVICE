package reflect

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestGetExchangeRateParsesCurrentResponseShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/stablecoin/0/exchange-rate", r.URL.Path)
		_, _ = w.Write([]byte(`{"success":true,"data":{"base":1024154862,"receipt":1024154862}}`))
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)

	rate, err := client.GetExchangeRate(context.Background())

	require.NoError(t, err)
	require.True(t, rate.Equal(decimal.RequireFromString("1.024154862")), rate.String())
}

func TestGetExchangeRateParsesLegacyBPSResponseShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/stablecoin/0/exchange-rate", r.URL.Path)
		_, _ = w.Write([]byte(`{"success":true,"data":{"base_usd_value_bps":10043,"receipt_usd_value_bps":10043}}`))
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)

	rate, err := client.GetExchangeRate(context.Background())

	require.NoError(t, err)
	require.True(t, rate.Equal(decimal.RequireFromString("1.0043")), rate.String())
}

func TestGenerateMintTransactionUsesRateAdjustedMinimumReceived(t *testing.T) {
	var mintBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/stablecoin/0/exchange-rate":
			_, _ = w.Write([]byte(`{"success":true,"data":{"base":1024000000,"receipt":1024000000}}`))
		case "/stablecoin/mint":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&mintBody))
			_, _ = w.Write([]byte(`{"success":true,"data":{"transaction":"raw-tx"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)

	tx, err := client.GenerateMintTransaction(context.Background(), decimal.NewFromInt(1), "signer", "fee-payer")

	require.NoError(t, err)
	require.Equal(t, "raw-tx", tx)
	require.EqualValues(t, 1_000_000, mintBody["depositAmount"])
	require.EqualValues(t, 975585, mintBody["minimumReceived"])
}

func TestGenerateBurnTransactionUsesRateAdjustedMinimumReceived(t *testing.T) {
	var burnBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/stablecoin/0/exchange-rate":
			_, _ = w.Write([]byte(`{"success":true,"data":{"base":1024000000,"receipt":1024000000}}`))
		case "/stablecoin/burn":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&burnBody))
			_, _ = w.Write([]byte(`{"success":true,"data":{"transaction":"raw-tx"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)

	tx, err := client.GenerateBurnTransaction(context.Background(), decimal.NewFromInt(1), "signer", "fee-payer")

	require.NoError(t, err)
	require.Equal(t, "raw-tx", tx)
	require.EqualValues(t, 1_000_000, burnBody["depositAmount"])
	require.EqualValues(t, 1_022_976, burnBody["minimumReceived"])
}

func newTestClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	client, err := NewClient(baseURL, "", "http://solana.invalid", "", "", USDCPlusIndex, zap.NewNop())
	require.NoError(t, err)
	return client
}
