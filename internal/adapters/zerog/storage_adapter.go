package zerog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"github.com/0glabs/0g-storage-client/core"
	"github.com/0glabs/0g-storage-client/indexer"
	"github.com/0glabs/0g-storage-client/transfer"
	"github.com/openweb3/web3go"
	"github.com/sony/gobreaker"
	"github.com/stack-service/stack_service/internal/domain/entities"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// StorageAdapter implements entities.ZeroGStorageClient interface
type StorageAdapter struct {
	config         *Config
	logger         *zap.Logger
	tracer         trace.Tracer
	metrics        *StorageMetrics
	circuitBreaker *gobreaker.CircuitBreaker
	web3Client     *web3go.Client
	indexerClient  *indexer.Client
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

// NewStorageAdapter creates a new 0G storage adapter with circuit breaker
func NewStorageAdapter(cfg *Config, logger *zap.Logger) (*StorageAdapter, error) {
	if cfg == nil {
		return nil, fmt.Errorf("storage config is required")
	}

	if err := cfg.ValidateStorage(); err != nil {
		return nil, fmt.Errorf("invalid storage config: %w", err)
	}

	tracer := otel.Tracer("zerog-storage-adapter")
	meter := otel.Meter("zerog-storage-adapter")

	// Initialize metrics
	metrics, err := initStorageMetrics(meter)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize metrics: %w", err)
	}

	// Configure circuit breaker
	cbSettings := gobreaker.Settings{
		Name:        "zerog-storage",
		MaxRequests: 3,
		Interval:    time.Duration(cfg.CircuitBreaker.IntervalSeconds) * time.Second,
		Timeout:     time.Duration(cfg.CircuitBreaker.TimeoutSeconds) * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
			return counts.Requests >= uint32(cfg.CircuitBreaker.MinRequests) &&
				failureRatio >= cfg.CircuitBreaker.FailureThreshold
		},
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			logger.Info("Circuit breaker state changed",
				zap.String("name", name),
				zap.String("from", from.String()),
				zap.String("to", to.String()),
			)
		},
	}

	adapter := &StorageAdapter{
		config:         cfg,
		logger:         logger,
		tracer:         tracer,
		metrics:        metrics,
		circuitBreaker: gobreaker.NewCircuitBreaker(cbSettings),
	}

	// Initialize 0G clients
	if err := adapter.initialize(); err != nil {
		return nil, fmt.Errorf("failed to initialize 0G storage: %w", err)
	}

	logger.Info("0G storage adapter initialized successfully",
		zap.String("rpc_endpoint", cfg.Storage.RPCEndpoint),
		zap.String("indexer_rpc", cfg.Storage.IndexerRPC),
	)

	return adapter, nil
}

// initialize sets up the 0G storage client connections
func (a *StorageAdapter) initialize() error {
	// Initialize Web3 client
	web3Client, err := web3go.NewClient(a.config.Storage.RPCEndpoint)
	if err != nil {
		return fmt.Errorf("failed to create Web3 client: %w", err)
	}
	a.web3Client = web3Client

	// Initialize Indexer client
	indexerClient, err := indexer.NewClient(a.config.Storage.IndexerRPC)
	if err != nil {
		return fmt.Errorf("failed to create indexer client: %w", err)
	}
	a.indexerClient = indexerClient

	a.logger.Info("0G storage clients initialized successfully")
	return nil
}

// Store uploads data to 0G storage and returns a content-addressed URI
func (a *StorageAdapter) Store(ctx context.Context, namespace string, data []byte, metadata map[string]string) (*entities.StorageResult, error) {
	startTime := time.Now()
	ctx, span := a.tracer.Start(ctx, "storage_adapter.store", trace.WithAttributes(
		attribute.String("namespace", namespace),
		attribute.Int("data_size", len(data)),
	))
	defer span.End()

	// Increment request counter
	a.metrics.RequestsTotal.Add(ctx, 1, metric.WithAttributes(
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
		a.recordError(ctx, "store", err)
		return nil, err
	}

	// Use circuit breaker to protect storage operations
	result, err := a.circuitBreaker.Execute(func() (interface{}, error) {
		return a.performStore(ctx, namespace, data, metadata)
	})

	if err != nil {
		span.RecordError(err)
		a.recordError(ctx, "store", err)
		return nil, fmt.Errorf("storage operation failed: %w", err)
	}

	storageResult := result.(*entities.StorageResult)

	// Record successful metrics
	duration := time.Since(startTime)
	a.metrics.RequestDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(
		attribute.String("operation", "store"),
		attribute.String("namespace", namespace),
		attribute.Bool("success", true),
	))
	a.metrics.StoredBytes.Add(ctx, int64(len(data)))

	a.logger.Info("Data stored successfully",
		zap.String("uri", storageResult.URI),
		zap.String("content_hash", storageResult.Hash),
		zap.Int64("size", storageResult.Size),
		zap.Duration("duration", duration),
	)

	return storageResult, nil
}

// Retrieve downloads data from 0G storage using a URI
func (a *StorageAdapter) Retrieve(ctx context.Context, uri string) (*entities.StorageData, error) {
	startTime := time.Now()
	ctx, span := a.tracer.Start(ctx, "storage_adapter.retrieve", trace.WithAttributes(
		attribute.String("uri", uri),
	))
	defer span.End()

	// Increment request counter
	a.metrics.RequestsTotal.Add(ctx, 1, metric.WithAttributes(
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
		a.recordError(ctx, "retrieve", err)
		return nil, err
	}

	// Use circuit breaker
	result, err := a.circuitBreaker.Execute(func() (interface{}, error) {
		return a.performRetrieve(ctx, uri)
	})

	if err != nil {
		span.RecordError(err)
		a.recordError(ctx, "retrieve", err)
		return nil, fmt.Errorf("retrieval operation failed: %w", err)
	}

	storageData := result.(*entities.StorageData)

	// Record successful metrics
	duration := time.Since(startTime)
	a.metrics.RequestDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(
		attribute.String("operation", "retrieve"),
		attribute.Bool("success", true),
	))
	a.metrics.RetrievedBytes.Add(ctx, int64(len(storageData.Data)))

	a.logger.Info("Data retrieved successfully",
		zap.String("uri", uri),
		zap.Int64("size", storageData.Size),
		zap.Duration("duration", duration),
	)

	return storageData, nil
}

// HealthCheck verifies connectivity to 0G storage network
func (a *StorageAdapter) HealthCheck(ctx context.Context) (*entities.HealthStatus, error) {
	startTime := time.Now()
	ctx, span := a.tracer.Start(ctx, "storage_adapter.health_check")
	defer span.End()

	a.logger.Debug("Performing 0G storage health check")

	// Use circuit breaker for health check
	result, err := a.circuitBreaker.Execute(func() (interface{}, error) {
		nodes, err := a.indexerClient.SelectNodes(ctx, 1, 1, []string{}, "min")
		if err != nil {
			return nil, fmt.Errorf("failed to select nodes: %w", err)
		}

		status := entities.HealthStatusHealthy
		var errors []string
		if len(nodes) == 0 {
			status = entities.HealthStatusDegraded
			errors = append(errors, "no storage nodes available")
		}

		return &entities.HealthStatus{
			Status:  status,
			Latency: time.Since(startTime),
			Version: "",
			Uptime:  0,
			Metrics: map[string]interface{}{
				"available_nodes":   len(nodes),
				"expected_replicas": a.config.Storage.ExpectedReplicas,
				"circuit_breaker":   a.circuitBreaker.State().String(),
			},
			LastChecked: time.Now(),
			Errors:      errors,
		}, nil
	})

	if err != nil {
		span.RecordError(err)
		a.recordError(ctx, "health_check", err)
		return &entities.HealthStatus{
			Status:  entities.HealthStatusDegraded,
			Latency: time.Since(startTime),
			Version: "",
			Uptime:  0,
			Metrics: map[string]interface{}{
				"expected_replicas": a.config.Storage.ExpectedReplicas,
				"circuit_breaker":   a.circuitBreaker.State().String(),
			},
			LastChecked: time.Now(),
			Errors:      []string{err.Error()},
		}, nil
	}

	healthStatus := result.(*entities.HealthStatus)

	a.logger.Info("0G storage health check completed",
		zap.String("status", healthStatus.Status),
		zap.Duration("latency", healthStatus.Latency),
	)

	return healthStatus, nil
}

// ListObjects lists objects in a namespace
func (a *StorageAdapter) ListObjects(ctx context.Context, namespace string, prefix string) ([]entities.StorageObject, error) {
	ctx, span := a.tracer.Start(ctx, "storage_adapter.list_objects", trace.WithAttributes(
		attribute.String("namespace", namespace),
		attribute.String("prefix", prefix),
	))
	defer span.End()

	// TODO: Implement object listing when supported by 0G SDK
	return nil, fmt.Errorf("list objects not yet implemented for namespace %s", namespace)
}

// Delete removes an object from storage
func (a *StorageAdapter) Delete(ctx context.Context, uri string) error {
	ctx, span := a.tracer.Start(ctx, "storage_adapter.delete", trace.WithAttributes(
		attribute.String("uri", uri),
	))
	defer span.End()

	// TODO: Implement deletion when supported by 0G SDK
	return fmt.Errorf("delete operation not yet implemented for uri %s", uri)
}

// performStore executes the actual storage operation
func (a *StorageAdapter) performStore(ctx context.Context, namespace string, data []byte, metadata map[string]string) (*entities.StorageResult, error) {
	// Calculate content hash
	contentHash := a.calculateHash(data)

	// Create temporary file for merkle root calculation
	tempFile := fmt.Sprintf("/tmp/0g_upload_%s", contentHash)
	defer os.Remove(tempFile)

	if err := os.WriteFile(tempFile, data, 0644); err != nil {
		return nil, &entities.ZeroGError{
			Code:      entities.ErrorCodeInternalError,
			Message:   fmt.Sprintf("failed to create temp file: %v", err),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	// Calculate merkle root
	merkleRoot, err := core.MerkleRoot(tempFile)
	if err != nil {
		return nil, &entities.ZeroGError{
			Code:      entities.ErrorCodeInternalError,
			Message:   fmt.Sprintf("failed to calculate merkle root: %v", err),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	// Calculate segment number (each segment is 256 bytes)
	dataSize := len(data)
	segmentNumber := (dataSize + 255) / 256
	if segmentNumber == 0 {
		segmentNumber = 1
	}

	// Select storage nodes
	nodes, err := a.indexerClient.SelectNodes(ctx, uint64(segmentNumber), uint(a.config.Storage.ExpectedReplicas), []string{}, "min")
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
	uploader, err := transfer.NewUploader(ctx, a.web3Client, transfer.UploaderOption{
		// Configure uploader with proper settings
	})
	if err != nil {
		return nil, &entities.ZeroGError{
			Code:      entities.ErrorCodeInternalError,
			Message:   fmt.Sprintf("failed to create uploader: %v", err),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	// Upload to nodes
	txHash, err := uploader.Upload(ctx, tempFile, 0, nil)
	if err != nil {
		return nil, &entities.ZeroGError{
			Code:      entities.ErrorCodeUploadFailed,
			Message:   fmt.Sprintf("upload failed: %v", err),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	// Build URI
	uri := fmt.Sprintf("0g://%s/%s", namespace, contentHash)

	// Merge metadata
	finalMetadata := make(map[string]string)
	for k, v := range metadata {
		finalMetadata[k] = v
	}
	finalMetadata["merkle_root"] = hex.EncodeToString(merkleRoot[:])
	finalMetadata["tx_hash"] = txHash.String()
	finalMetadata["segment_count"] = fmt.Sprintf("%d", segmentNumber)

	return &entities.StorageResult{
		URI:       uri,
		Hash:      contentHash,
		Size:      int64(dataSize),
		Namespace: namespace,
		Metadata:  finalMetadata,
		StoredAt:  time.Now(),
		Replicas:  len(nodes),
	}, nil
}

// performRetrieve executes the actual retrieval operation
func (a *StorageAdapter) performRetrieve(ctx context.Context, uri string) (*entities.StorageData, error) {
	// TODO: Implement actual retrieval from 0G storage
	// This is a placeholder implementation
	return nil, &entities.ZeroGError{
		Code:      entities.ErrorCodeNotImplemented,
		Message:   "retrieval not yet fully implemented",
		Retryable: false,
		Timestamp: time.Now(),
	}
}

// calculateHash calculates SHA256 hash of data
func (a *StorageAdapter) calculateHash(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// recordError records error metrics
func (a *StorageAdapter) recordError(ctx context.Context, operation string, err error) {
	var errorCode string
	if zeroGErr, ok := err.(*entities.ZeroGError); ok {
		errorCode = zeroGErr.Code
	} else {
		errorCode = entities.ErrorCodeInternalError
	}

	a.metrics.RequestErrors.Add(ctx, 1, metric.WithAttributes(
		attribute.String("operation", operation),
		attribute.String("error_code", errorCode),
	))
}

// initStorageMetrics initializes OpenTelemetry metrics for storage operations
func initStorageMetrics(meter metric.Meter) (*StorageMetrics, error) {
	requestsTotal, err := meter.Int64Counter("zerog_storage_requests_total",
		metric.WithDescription("Total number of storage requests"))
	if err != nil {
		return nil, fmt.Errorf("failed to create requests_total metric: %w", err)
	}

	requestDuration, err := meter.Float64Histogram("zerog_storage_request_duration_seconds",
		metric.WithDescription("Duration of storage requests in seconds"))
	if err != nil {
		return nil, fmt.Errorf("failed to create request_duration metric: %w", err)
	}

	requestErrors, err := meter.Int64Counter("zerog_storage_request_errors_total",
		metric.WithDescription("Total number of storage request errors"))
	if err != nil {
		return nil, fmt.Errorf("failed to create request_errors metric: %w", err)
	}

	storedBytes, err := meter.Int64Counter("zerog_storage_stored_bytes_total",
		metric.WithDescription("Total bytes stored"))
	if err != nil {
		return nil, fmt.Errorf("failed to create stored_bytes metric: %w", err)
	}

	retrievedBytes, err := meter.Int64Counter("zerog_storage_retrieved_bytes_total",
		metric.WithDescription("Total bytes retrieved"))
	if err != nil {
		return nil, fmt.Errorf("failed to create retrieved_bytes metric: %w", err)
	}

	activeReplicas, err := meter.Int64Gauge("zerog_storage_active_replicas",
		metric.WithDescription("Number of active storage replicas"))
	if err != nil {
		return nil, fmt.Errorf("failed to create active_replicas metric: %w", err)
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
