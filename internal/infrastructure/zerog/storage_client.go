package zerog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/stack-service/stack_service/internal/domain/entities"
	"github.com/stack-service/stack_service/internal/infrastructure/config"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	// 0G Storage Client imports
	"github.com/0glabs/0g-storage-client/core"
	"github.com/0glabs/0g-storage-client/indexer"
	"github.com/0glabs/0g-storage-client/transfer"
	"github.com/openweb3/web3go"
)

// StorageClient implements the ZeroGStorageClient interface
type StorageClient struct {
	config  *config.ZeroGStorageConfig
	logger  *zap.Logger
	tracer  trace.Tracer
	metrics *StorageMetrics
	// 0G client components
	web3Client    *web3go.Client
	indexerClient *indexer.Client
}

// StorageMetrics contains observability metrics for storage operations
type StorageMetrics struct {
	RequestsTotal   metric.Int64Counter
	RequestDuration metric.Float64Histogram
	RequestErrors   metric.Int64Counter
	StoredBytes     metric.Int64Counter
	RetrievedBytes  metric.Int64Counter
	ActiveReplicas  metric.Int64Gauge
}

// NewStorageClient creates a new 0G storage client
func NewStorageClient(cfg *config.ZeroGStorageConfig, logger *zap.Logger) (*StorageClient, error) {
	if cfg == nil {
		return nil, fmt.Errorf("storage config is required")
	}

	if cfg.RPCEndpoint == "" {
		return nil, fmt.Errorf("RPC endpoint is required")
	}

	if cfg.IndexerRPC == "" {
		return nil, fmt.Errorf("indexer RPC endpoint is required")
	}

	if cfg.PrivateKey == "" {
		return nil, fmt.Errorf("private key is required")
	}

	tracer := otel.Tracer("zerog-storage")
	meter := otel.Meter("zerog-storage")

	// Initialize metrics
	metrics, err := initStorageMetrics(meter)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize metrics: %w", err)
	}

	client := &StorageClient{
		config:  cfg,
		logger:  logger,
		tracer:  tracer,
		metrics: metrics,
	}

	// TODO: Initialize actual 0G clients
	if err := client.initialize(); err != nil {
		return nil, fmt.Errorf("failed to initialize 0G clients: %w", err)
	}

	return client, nil
}

// initialize sets up the 0G storage client connections
func (c *StorageClient) initialize() error {
	c.logger.Info("Initializing 0G storage client",
		zap.String("rpc_endpoint", c.config.RPCEndpoint),
		zap.String("indexer_rpc", c.config.IndexerRPC),
		zap.Int("min_replicas", c.config.MinReplicas),
		zap.Int("expected_replicas", c.config.ExpectedReplicas),
	)

	// Initialize Web3 client
	web3Client, err := web3go.NewClient(c.config.RPCEndpoint)
	if err != nil {
		return fmt.Errorf("failed to create Web3 client: %w", err)
	}
	c.web3Client = web3Client

	// Initialize Indexer client
	indexerClient, err := indexer.NewClient(c.config.IndexerRPC)
	if err != nil {
		return fmt.Errorf("failed to create indexer client: %w", err)
	}
	c.indexerClient = indexerClient

	c.logger.Info("0G storage client initialized successfully")
	return nil
}

// Store uploads data to 0G storage and returns a content-addressed URI
func (c *StorageClient) Store(ctx context.Context, namespace string, data []byte, metadata map[string]string) (*entities.StorageResult, error) {
	startTime := time.Now()
	ctx, span := c.tracer.Start(ctx, "storage.store", trace.WithAttributes(
		attribute.String("namespace", namespace),
		attribute.Int("data_size", len(data)),
	))
	defer span.End()

	// Increment request counter
	c.metrics.RequestsTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("operation", "store"),
		attribute.String("namespace", namespace),
	))

	// Validate input
	if len(data) == 0 {
		err := &entities.ZeroGError{
			Code:      entities.ErrorCodeInvalidRequest,
			Message:   "data cannot be empty",
			Retryable: false,
			Timestamp: time.Now(),
		}
		span.RecordError(err)
		c.recordError(ctx, "store", err)
		return nil, err
	}

	// Validate namespace
	if !c.isValidNamespace(namespace) {
		err := &entities.ZeroGError{
			Code:      entities.ErrorCodeInvalidRequest,
			Message:   fmt.Sprintf("invalid namespace: %s", namespace),
			Retryable: false,
			Timestamp: time.Now(),
		}
		span.RecordError(err)
		c.recordError(ctx, "store", err)
		return nil, err
	}

	// Calculate content hash
	hasher := sha256.New()
	hasher.Write(data)
	contentHash := hex.EncodeToString(hasher.Sum(nil))

	c.logger.Debug("Storing data to 0G storage",
		zap.String("namespace", namespace),
		zap.Int("size", len(data)),
		zap.String("content_hash", contentHash),
	)

	// Perform the storage operation with retries
	result, err := c.storeWithRetries(ctx, namespace, data, metadata, contentHash)
	if err != nil {
		span.RecordError(err)
		c.recordError(ctx, "store", err)
		return nil, err
	}

	// Record successful metrics
	duration := time.Since(startTime)
	c.metrics.RequestDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(
		attribute.String("operation", "store"),
		attribute.String("namespace", namespace),
		attribute.Bool("success", true),
	))
	c.metrics.StoredBytes.Add(ctx, int64(len(data)))

	c.logger.Info("Data stored successfully to 0G storage",
		zap.String("uri", result.URI),
		zap.String("content_hash", result.Hash),
		zap.Int64("size", result.Size),
		zap.Duration("duration", duration),
	)

	return result, nil
}

// Retrieve downloads data from 0G storage using a URI
func (c *StorageClient) Retrieve(ctx context.Context, uri string) (*entities.StorageData, error) {
	startTime := time.Now()
	ctx, span := c.tracer.Start(ctx, "storage.retrieve", trace.WithAttributes(
		attribute.String("uri", uri),
	))
	defer span.End()

	// Increment request counter
	c.metrics.RequestsTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("operation", "retrieve"),
	))

	// Validate URI
	if uri == "" {
		err := &entities.ZeroGError{
			Code:      entities.ErrorCodeInvalidURI,
			Message:   "URI cannot be empty",
			Retryable: false,
			Timestamp: time.Now(),
		}
		span.RecordError(err)
		c.recordError(ctx, "retrieve", err)
		return nil, err
	}

	c.logger.Debug("Retrieving data from 0G storage",
		zap.String("uri", uri),
	)

	// Perform the retrieval operation with retries
	data, err := c.retrieveWithRetries(ctx, uri)
	if err != nil {
		span.RecordError(err)
		c.recordError(ctx, "retrieve", err)
		return nil, err
	}

	// Record successful metrics
	duration := time.Since(startTime)
	c.metrics.RequestDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(
		attribute.String("operation", "retrieve"),
		attribute.Bool("success", true),
	))
	c.metrics.RetrievedBytes.Add(ctx, int64(len(data.Data)))

	c.logger.Info("Data retrieved successfully from 0G storage",
		zap.String("uri", uri),
		zap.Int64("size", data.Size),
		zap.Duration("duration", duration),
	)

	return data, nil
}

// HealthCheck verifies connectivity to 0G storage network
func (c *StorageClient) HealthCheck(ctx context.Context) (*entities.HealthStatus, error) {
	startTime := time.Now()
	ctx, span := c.tracer.Start(ctx, "storage.health_check")
	defer span.End()

	c.logger.Debug("Performing 0G storage health check")

	nodes, err := c.indexerClient.SelectNodes(ctx, 1, 1, []string{}, "min")
	if err != nil {
		span.RecordError(err)
		c.recordError(ctx, "health_check", err)
		return &entities.HealthStatus{
			Status:  entities.HealthStatusDegraded,
			Latency: time.Since(startTime),
			Version: "",
			Uptime:  0,
			Metrics: map[string]interface{}{
				"replicas_expected": c.config.ExpectedReplicas,
			},
			LastChecked: time.Now(),
			Errors:      []string{err.Error()},
		}, nil
	}

	status := entities.HealthStatusHealthy
	var errors []string
	if len(nodes) == 0 {
		status = entities.HealthStatusDegraded
		errors = append(errors, "no storage nodes available")
	}

	result := &entities.HealthStatus{
		Status:  status,
		Latency: time.Since(startTime),
		Version: "",
		Uptime:  0,
		Metrics: map[string]interface{}{
			"available_nodes":   len(nodes),
			"expected_replicas": c.config.ExpectedReplicas,
		},
		LastChecked: time.Now(),
		Errors:      errors,
	}

	c.logger.Info("0G storage health check completed",
		zap.String("status", result.Status),
		zap.Duration("latency", result.Latency),
	)

	return result, nil
}

// ListObjects lists objects in a namespace
func (c *StorageClient) ListObjects(ctx context.Context, namespace string, prefix string) ([]entities.StorageObject, error) {
	ctx, span := c.tracer.Start(ctx, "storage.list_objects", trace.WithAttributes(
		attribute.String("namespace", namespace),
		attribute.String("prefix", prefix),
	))
	defer span.End()

	return nil, fmt.Errorf("list objects not implemented for namespace %s", namespace)
}

// Delete removes an object from storage
func (c *StorageClient) Delete(ctx context.Context, uri string) error {
	ctx, span := c.tracer.Start(ctx, "storage.delete", trace.WithAttributes(
		attribute.String("uri", uri),
	))
	defer span.End()

	return fmt.Errorf("delete operation not implemented for uri %s", uri)
}

// storeWithRetries performs storage operation with exponential backoff retry
func (c *StorageClient) storeWithRetries(ctx context.Context, namespace string, data []byte, metadata map[string]string, contentHash string) (*entities.StorageResult, error) {
	var lastErr error
	maxRetries := 3 // TODO: Get from config

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff
			backoff := time.Duration(attempt*attempt) * time.Second
			c.logger.Debug("Retrying storage operation",
				zap.Int("attempt", attempt),
				zap.Duration("backoff", backoff),
			)

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}

		result, err := c.performStore(ctx, namespace, data, metadata, contentHash)
		if err == nil {
			return result, nil
		}

		lastErr = err
		c.logger.Warn("Storage operation failed",
			zap.Int("attempt", attempt+1),
			zap.Error(err),
		)

		// Don't retry non-retryable errors
		if zeroGErr, ok := err.(*entities.ZeroGError); ok && !zeroGErr.Retryable {
			break
		}
	}

	return nil, fmt.Errorf("storage operation failed after %d attempts: %w", maxRetries+1, lastErr)
}

// retrieveWithRetries performs retrieval operation with exponential backoff retry
func (c *StorageClient) retrieveWithRetries(ctx context.Context, uri string) (*entities.StorageData, error) {
	var lastErr error
	maxRetries := 3 // TODO: Get from config

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff
			backoff := time.Duration(attempt*attempt) * time.Second
			c.logger.Debug("Retrying retrieval operation",
				zap.Int("attempt", attempt),
				zap.Duration("backoff", backoff),
			)

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}

		data, err := c.performRetrieve(ctx, uri)
		if err == nil {
			return data, nil
		}

		lastErr = err
		c.logger.Warn("Retrieval operation failed",
			zap.Int("attempt", attempt+1),
			zap.Error(err),
		)

		// Don't retry non-retryable errors
		if zeroGErr, ok := err.(*entities.ZeroGError); ok && !zeroGErr.Retryable {
			break
		}
	}

	return nil, fmt.Errorf("retrieval operation failed after %d attempts: %w", maxRetries+1, lastErr)
}

// performStore executes the actual storage operation
func (c *StorageClient) performStore(ctx context.Context, namespace string, data []byte, metadata map[string]string, contentHash string) (*entities.StorageResult, error) {
	// Create a temporary file to calculate merkle root (0G client expects file path)
	tempFile := fmt.Sprintf("/tmp/0g_upload_%s", contentHash)
	defer func() {
		// Clean up temp file
		os.Remove(tempFile)
	}()

	// Write data to temporary file
	if err := os.WriteFile(tempFile, data, 0644); err != nil {
		return nil, &entities.ZeroGError{
			Code:      entities.ErrorCodeInternalError,
			Message:   fmt.Sprintf("failed to create temp file: %v", err),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	// Calculate merkle root for the file
	merkleRoot, err := core.MerkleRoot(tempFile)
	if err != nil {
		return nil, &entities.ZeroGError{
			Code:      entities.ErrorCodeInternalError,
			Message:   fmt.Sprintf("failed to calculate merkle root: %v", err),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	// Calculate segment number based on data size
	// Each segment is 256 bytes, so we need ceiling division
	dataSize := len(data)
	segmentNumber := (dataSize + 255) / 256 // Ceiling division
	if segmentNumber == 0 {
		segmentNumber = 1
	}

	// Select storage nodes from indexer
	nodes, err := c.indexerClient.SelectNodes(ctx, uint64(segmentNumber), uint(c.config.ExpectedReplicas), []string{}, "min")
	if err != nil {
		return nil, &entities.ZeroGError{
			Code:      entities.ErrorCodeServiceUnavailable,
			Message:   fmt.Sprintf("failed to select storage nodes: %v", err),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	if len(nodes) == 0 {
		return nil, &entities.ZeroGError{
			Code:      entities.ErrorCodeServiceUnavailable,
			Message:   "no storage nodes available",
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	// Create uploader
	uploader, err := transfer.NewUploader(ctx, c.web3Client, nodes)
	if err != nil {
		return nil, &entities.ZeroGError{
			Code:      entities.ErrorCodeInternalError,
			Message:   fmt.Sprintf("failed to create uploader: %v", err),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	// Upload file to 0G storage using file path
	txHash, fileHash, err := uploader.UploadFile(ctx, tempFile)
	if err != nil {
		return nil, &entities.ZeroGError{
			Code:      entities.ErrorCodeNetworkError,
			Message:   fmt.Sprintf("upload failed: %v", err),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	// Verify the file hash matches our calculated merkle root
	if fileHash != merkleRoot {
		c.logger.Warn("Upload completed but merkle root mismatch",
			zap.String("expected", merkleRoot.String()),
			zap.String("actual", fileHash.String()),
		)
	}

	// Build URI using the actual file hash returned from upload
	uri := fmt.Sprintf("0g://%s/%s", namespace, fileHash.String())

	// Add transaction hash and other metadata
	if metadata == nil {
		metadata = make(map[string]string)
	}
	metadata["tx_hash"] = txHash.String()
	metadata["merkle_root"] = fileHash.String()
	metadata["calculated_root"] = merkleRoot.String()
	metadata["segment_count"] = fmt.Sprintf("%d", segmentNumber)

	return &entities.StorageResult{
		URI:       uri,
		Hash:      fileHash.String(),
		Size:      int64(len(data)),
		Namespace: namespace,
		Metadata:  metadata,
		StoredAt:  time.Now(),
		Replicas:  len(nodes),
	}, nil
}

// performRetrieve executes the actual retrieval operation
func (c *StorageClient) performRetrieve(ctx context.Context, uri string) (*entities.StorageData, error) {
	// Parse URI to extract merkle root hash
	merkleRootStr, err := c.extractHashFromURI(uri)
	if err != nil {
		return nil, &entities.ZeroGError{
			Code:      entities.ErrorCodeInvalidURI,
			Message:   fmt.Sprintf("failed to parse URI: %v", err),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	// Get available storage nodes for download
	nodes, err := c.indexerClient.SelectNodes(ctx, 1, 1, []string{}, "min") // Just need one node for download
	if err != nil {
		return nil, &entities.ZeroGError{
			Code:      entities.ErrorCodeServiceUnavailable,
			Message:   fmt.Sprintf("failed to select download nodes: %v", err),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	if len(nodes) == 0 {
		return nil, &entities.ZeroGError{
			Code:      entities.ErrorCodeServiceUnavailable,
			Message:   "no storage nodes available for download",
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	// Create downloader
	downloader, err := transfer.NewDownloader(nodes)
	if err != nil {
		return nil, &entities.ZeroGError{
			Code:      entities.ErrorCodeInternalError,
			Message:   fmt.Sprintf("failed to create downloader: %v", err),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	// Use a temporary file for download (0G client expects file path)
	tempFile := fmt.Sprintf("/tmp/0g_download_%s", merkleRootStr)
	defer func() {
		// Clean up temp file
		os.Remove(tempFile)
	}()

	// Download data from 0G storage with proof verification
	// The downloader expects the root hash as a string, not common.Hash
	err = downloader.Download(ctx, merkleRootStr, tempFile, true)
	if err != nil {
		return nil, &entities.ZeroGError{
			Code:      entities.ErrorCodeNotFound,
			Message:   fmt.Sprintf("download failed: %v", err),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	// Read the downloaded file
	data, err := os.ReadFile(tempFile)
	if err != nil {
		return nil, &entities.ZeroGError{
			Code:      entities.ErrorCodeInternalError,
			Message:   fmt.Sprintf("failed to read downloaded file: %v", err),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	// Create metadata with the merkle root
	metadata := map[string]string{
		"merkle_root": merkleRootStr,
		"verified":    "true", // Since we used proof verification
	}

	return &entities.StorageData{
		Data:     data,
		URI:      uri,
		Hash:     merkleRootStr,
		Size:     int64(len(data)),
		Metadata: metadata,
		StoredAt: time.Now(), // Note: This should ideally be the original storage time
	}, nil
}

// isValidNamespace checks if a namespace is valid
func (c *StorageClient) isValidNamespace(namespace string) bool {
	validNamespaces := []string{
		c.config.Namespaces.AISummaries,
		c.config.Namespaces.AIArtifacts,
		c.config.Namespaces.ModelPrompts,
	}

	for _, valid := range validNamespaces {
		if namespace == valid {
			return true
		}
	}

	return false
}

// recordError records error metrics
func (c *StorageClient) recordError(ctx context.Context, operation string, err error) {
	var errorCode string
	if zeroGErr, ok := err.(*entities.ZeroGError); ok {
		errorCode = zeroGErr.Code
	} else {
		errorCode = entities.ErrorCodeInternalError
	}

	c.metrics.RequestErrors.Add(ctx, 1, metric.WithAttributes(
		attribute.String("operation", operation),
		attribute.String("error_code", errorCode),
	))
}

// initStorageMetrics initializes OpenTelemetry metrics for storage operations
func initStorageMetrics(meter metric.Meter) (*StorageMetrics, error) {
	requestsTotal, err := meter.Int64Counter("zerog_storage_requests_total",
		metric.WithDescription("Total number of storage requests"))
	if err != nil {
		return nil, err
	}

	requestDuration, err := meter.Float64Histogram("zerog_storage_request_duration_seconds",
		metric.WithDescription("Duration of storage requests in seconds"))
	if err != nil {
		return nil, err
	}

	requestErrors, err := meter.Int64Counter("zerog_storage_request_errors_total",
		metric.WithDescription("Total number of storage request errors"))
	if err != nil {
		return nil, err
	}

	storedBytes, err := meter.Int64Counter("zerog_storage_stored_bytes_total",
		metric.WithDescription("Total bytes stored in 0G storage"))
	if err != nil {
		return nil, err
	}

	retrievedBytes, err := meter.Int64Counter("zerog_storage_retrieved_bytes_total",
		metric.WithDescription("Total bytes retrieved from 0G storage"))
	if err != nil {
		return nil, err
	}

	activeReplicas, err := meter.Int64Gauge("zerog_storage_active_replicas",
		metric.WithDescription("Number of active storage replicas"))
	if err != nil {
		return nil, err
	}

	return &StorageMetrics{
		RequestsTotal:   requestsTotal,
		RequestDuration: requestDuration,
		RequestErrors:   requestErrors,
		StoredBytes:     storedBytes,
		RetrievedBytes:  retrievedBytes,
		ActiveReplicas:  activeReplicas,
	}, nil
}

// extractHashFromURI extracts the merkle root hash from a 0G URI
func (c *StorageClient) extractHashFromURI(uri string) (string, error) {
	// Expected format: 0g://namespace/merkle_root_hash
	if !strings.HasPrefix(uri, "0g://") {
		return "", fmt.Errorf("invalid URI format, expected 0g:// prefix")
	}

	// Remove the 0g:// prefix
	path := strings.TrimPrefix(uri, "0g://")

	// Split by / to get namespace and hash
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid URI structure, expected namespace/hash")
	}

	return parts[1], nil
}

// Close closes the storage client and cleans up resources
func (c *StorageClient) Close() error {
	c.logger.Info("Closing 0G storage client")

	// Close Web3 client connection
	if c.web3Client != nil {
		c.web3Client.Close()
	}

	return nil
}

// GetNamespaces returns the configured namespaces
func (c *StorageClient) GetNamespaces() *config.ZeroGNamespaces {
	return &c.config.Namespaces
}
