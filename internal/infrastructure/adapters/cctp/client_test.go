package cctp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestNewClient(t *testing.T) {
	logger := zap.NewNop()

	t.Run("defaults to sandbox URL", func(t *testing.T) {
		client := NewClient(Config{Environment: "sandbox"}, logger)
		assert.Equal(t, IrisSandboxURL, client.config.BaseURL)
	})

	t.Run("uses mainnet URL", func(t *testing.T) {
		client := NewClient(Config{Environment: "mainnet"}, logger)
		assert.Equal(t, IrisMainnetURL, client.config.BaseURL)
	})

	t.Run("respects custom base URL", func(t *testing.T) {
		client := NewClient(Config{BaseURL: "https://custom.api"}, logger)
		assert.Equal(t, "https://custom.api", client.config.BaseURL)
	})
}

func TestGetAttestation(t *testing.T) {
	logger := zap.NewNop()

	t.Run("returns attestation on success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/v2/messages", r.URL.Path)
			assert.Equal(t, "0xabc123", r.URL.Query().Get("transactionHash"))

			resp := AttestationResponse{
				Messages: []CCTPMessage{{
					Attestation:       "0xattestation",
					AttestationStatus: AttestationStatusComplete,
					MessageHash:       "0xhash",
					SourceDomain:      DomainPolygon,
					DestinationDomain: DomainSolana,
					Amount:            "1000000",
				}},
			}
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := NewClient(Config{BaseURL: server.URL}, logger)
		resp, err := client.GetAttestation(context.Background(), "0xabc123")

		require.NoError(t, err)
		require.Len(t, resp.Messages, 1)
		assert.Equal(t, AttestationStatusComplete, resp.Messages[0].AttestationStatus)
		assert.Equal(t, DomainPolygon, resp.Messages[0].SourceDomain)
		assert.Equal(t, DomainSolana, resp.Messages[0].DestinationDomain)
	})

	t.Run("returns error when no messages", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(AttestationResponse{Messages: []CCTPMessage{}})
		}))
		defer server.Close()

		client := NewClient(Config{BaseURL: server.URL}, logger)
		_, err := client.GetAttestation(context.Background(), "0xabc123")

		assert.ErrorIs(t, err, ErrNoMessages)
	})
}

func TestGetFees(t *testing.T) {
	logger := zap.NewNop()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v2/burn/USDC/fees", r.URL.Path)
		assert.Equal(t, "7", r.URL.Query().Get("sourceDomain"))
		assert.Equal(t, "5", r.URL.Query().Get("destinationDomain"))

		resp := FeesResponse{
			SourceDomain:      DomainPolygon,
			DestinationDomain: DomainSolana,
			StandardFee:       Fee{MinimumFee: 0},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(Config{BaseURL: server.URL}, logger)
	resp, err := client.GetFees(context.Background(), DomainPolygon, DomainSolana)

	require.NoError(t, err)
	assert.Equal(t, DomainPolygon, resp.SourceDomain)
	assert.Equal(t, DomainSolana, resp.DestinationDomain)
}

func TestGetPublicKeys(t *testing.T) {
	logger := zap.NewNop()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v2/publicKeys", r.URL.Path)

		resp := PublicKeysResponse{
			Keys: []PublicKey{{KeyID: "key1", PublicKey: "0xpubkey", Algorithm: "ECDSA"}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(Config{BaseURL: server.URL}, logger)
	resp, err := client.GetPublicKeys(context.Background())

	require.NoError(t, err)
	require.Len(t, resp.Keys, 1)
	assert.Equal(t, "key1", resp.Keys[0].KeyID)
}

func TestDomainConstants(t *testing.T) {
	assert.Equal(t, uint32(5), DomainSolana)
	assert.Equal(t, uint32(7), DomainPolygon)
	assert.Equal(t, uint32(25), DomainStarknet)

	assert.Equal(t, "Solana", DomainNames[DomainSolana])
	assert.Equal(t, "Polygon", DomainNames[DomainPolygon])
	assert.Equal(t, "Starknet", DomainNames[DomainStarknet])
}
