package paj

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestCreateOnrampOrderReturnsTypedUnauthorizedError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/pub/onramp", r.URL.Path)
		require.Equal(t, "Bearer expired-token", r.Header.Get("Authorization"))

		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Session is invalid or expired","error":"Unauthorized","statusCode":401}`))
	}))
	defer server.Close()

	client := NewClient(Config{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		TokenMint:  "usdc-mint",
		WebhookURL: "https://example.com/webhooks/paj",
	}, zap.NewNop())

	_, err := client.CreateOnrampOrder(context.Background(), "expired-token", 100, "NGN", "recipient")
	require.Error(t, err)
	require.True(t, IsUnauthorized(err))

	var apiErr *APIError
	require.True(t, errors.As(err, &apiErr))
	require.Equal(t, http.StatusUnauthorized, apiErr.StatusCode)
	require.Equal(t, "/pub/onramp", apiErr.Path)
}
