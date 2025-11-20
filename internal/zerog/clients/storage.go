package clients

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"github.com/0glabs/0g-storage-client/common/blockchain"
	"github.com/0glabs/0g-storage-client/core"
	"github.com/0glabs/0g-storage-client/indexer"
	"github.com/0glabs/0g-storage-client/transfer"
	"github.com/ethereum/go-ethereum/common"
	"github.com/openweb3/web3go"
	"github.com/stack-service/stack_service/internal/infrastructure/config"
	"github.com/stack-service/stack_service/pkg/circuitbreaker"
	"github.com/stack-service/stack_service/pkg/retry"
	"go.uber.org/zap"
)

const (
	MaxFileSize     = 10 * 1024 * 1024 // 10MB
	MinReplicas     = 1
	DefaultReplicas = 3
)

type StorageClient struct {
	config         *config.ZeroGStorageConfig
	w3client       *web3go.Client
	indexer        *indexer.Client
	logger         *zap.Logger
	uploadCount    int64
	downloadCount  int64
	circuitBreaker *circuitbreaker.CircuitBreaker
}

func NewStorageClient(config config.ZeroGStorageConfig, logger *zap.Logger) (*StorageClient, error) {
	privateKey := config.PrivateKey
	if privateKey == "" {
		return nil, fmt.Errorf("storage private key required")
	}

	w3client := blockchain.MustNewWeb3(config.RPCEndpoint, privateKey)
	indexerClient, err := indexer.NewClient(config.IndexerRPC)
	if err != nil {
		w3client.Close()
		return nil, fmt.Errorf("indexer init failed: %w", err)
	}

	cb := circuitbreaker.New(circuitbreaker.Config{
		MaxRequests:      10,
		Interval:         60 * time.Second,
		Timeout:          30 * time.Second,
		FailureThreshold: 5,
		SuccessThreshold: 2,
		OnStateChange: func(from, to circuitbreaker.State) {
			logger.Warn("circuit breaker state changed",
				zap.String("from", fmt.Sprintf("%d", from)),
				zap.String("to", fmt.Sprintf("%d", to)),
			)
		},
	})

	return &StorageClient{
		config:         &config,
		w3client:       w3client,
		indexer:        indexerClient,
		logger:         logger,
		circuitBreaker: cb,
	}, nil
}

func (c *StorageClient) HealthCheck(ctx context.Context) error {
	_, err := c.indexer.SelectNodes(ctx, 1, 1, nil, "")
	return err
}

func (c *StorageClient) Close(ctx context.Context) error {
	c.w3client.Close()
	return nil
}

func (c *StorageClient) GetMetrics() map[string]interface{} {
	return map[string]interface{}{
		"uploads":   c.uploadCount,
		"downloads": c.downloadCount,
		"timestamp": time.Now(),
	}
}

func (c *StorageClient) Store(ctx context.Context, data []byte, metadata map[string]string) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("empty data")
	}
	if len(data) > MaxFileSize {
		return "", fmt.Errorf("file size %d exceeds maximum %d", len(data), MaxFileSize)
	}

	checksum := sha256.Sum256(data)
	c.logger.Debug("storing data", zap.Int("size", len(data)), zap.String("checksum", hex.EncodeToString(checksum[:])))

	tmpFile, err := os.CreateTemp("", "0g-upload-*")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		tmpFile.Close()
		os.Remove(tmpPath)
	}()

	if _, err := tmpFile.Write(data); err != nil {
		return "", fmt.Errorf("write temp file: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		return "", fmt.Errorf("sync temp file: %w", err)
	}

	rootHash, err := core.MerkleRoot(tmpPath)
	if err != nil {
		return "", fmt.Errorf("compute merkle root: %w", err)
	}

	replicas := c.config.ExpectedReplicas
	if replicas < MinReplicas {
		replicas = DefaultReplicas
	}

	var uploadErr error
	retryConfig := retry.RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   time.Second,
		MaxDelay:    10 * time.Second,
		Multiplier:  2.0,
	}

	err = retry.WithExponentialBackoff(ctx, retryConfig, func() error {
		nodes, err := c.indexer.SelectNodes(ctx, 1, uint(replicas), nil, "")
		if err != nil {
			return fmt.Errorf("select nodes: %w", err)
		}

		uploader, err := transfer.NewUploader(ctx, c.w3client, nodes)
		if err != nil {
			return fmt.Errorf("create uploader: %w", err)
		}

		_, _, err = uploader.UploadFile(ctx, tmpPath)
		if err != nil {
			return fmt.Errorf("upload file: %w", err)
		}
		return nil
	}, func(err error) bool { return true })

	if err != nil {
		c.logger.Error("upload failed", zap.Error(err), zap.String("root_hash", rootHash.String()))
		return "", uploadErr
	}

	c.uploadCount++
	c.logger.Info("data stored", zap.String("root_hash", rootHash.String()), zap.Int("replicas", replicas))
	return rootHash.String(), nil
}

func (c *StorageClient) Retrieve(ctx context.Context, storageID string) ([]byte, error) {
	if storageID == "" {
		return nil, fmt.Errorf("empty storage ID")
	}

	c.logger.Debug("retrieving data", zap.String("storage_id", storageID))

	tmpFile, err := os.CreateTemp("", "0g-download-*")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		tmpFile.Close()
		os.Remove(tmpPath)
	}()

	var data []byte
	retryConfig := retry.RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   time.Second,
		MaxDelay:    10 * time.Second,
		Multiplier:  2.0,
	}

	err = retry.WithExponentialBackoff(ctx, retryConfig, func() error {
		nodes, err := c.indexer.SelectNodes(ctx, 1, 1, nil, "")
		if err != nil {
			return fmt.Errorf("select nodes: %w", err)
		}

		downloader, err := transfer.NewDownloader(nodes)
		if err != nil {
			return fmt.Errorf("create downloader: %w", err)
		}

		rootHash := common.HexToHash(storageID)
		if err := downloader.Download(ctx, rootHash.String(), tmpPath, true); err != nil {
			return fmt.Errorf("download: %w", err)
		}

		data, err = os.ReadFile(tmpPath)
		if err != nil {
			return fmt.Errorf("read file: %w", err)
		}
		return nil
	}, func(err error) bool { return true })

	if err != nil {
		c.logger.Error("retrieve failed", zap.Error(err), zap.String("storage_id", storageID))
		return nil, err
	}

	checksum := sha256.Sum256(data)
	c.downloadCount++
	c.logger.Info("data retrieved", zap.String("storage_id", storageID), zap.Int("size", len(data)), zap.String("checksum", hex.EncodeToString(checksum[:])))
	return data, nil
}