package unit

import (
	"context"
	"testing"

	"github.com/stack-service/stack_service/internal/infrastructure/config"
	"github.com/stack-service/stack_service/internal/infrastructure/zerog"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestStorageClient_Validation(t *testing.T) {
	logger := zap.NewNop()
	cfg := config.ZeroGStorageConfig{
		RPCEndpoint:      "https://evmrpc-testnet.0g.ai/",
		IndexerRPC:       "https://indexer-storage-testnet-turbo.0g.ai",
		PrivateKey:       "test-key",
		ExpectedReplicas: 3,
	}

	client, err := zerog.NewStorageClient(cfg, logger)
	assert.NoError(t, err)
	assert.NotNil(t, client)

	ctx := context.Background()

	t.Run("empty data", func(t *testing.T) {
		_, err := client.Store(ctx, []byte{}, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "empty data")
	})

	t.Run("oversized data", func(t *testing.T) {
		largeData := make([]byte, 11*1024*1024) // 11MB
		_, err := client.Store(ctx, largeData, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "exceeds maximum")
	})

	t.Run("empty storage ID", func(t *testing.T) {
		_, err := client.Retrieve(ctx, "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "empty storage ID")
	})
}
