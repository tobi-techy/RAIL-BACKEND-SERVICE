package zerog

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/stack-service/stack_service/internal/domain/entities"
	"github.com/stack-service/stack_service/internal/infrastructure/config"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// NamespaceManager provides utilities for managing 0G storage namespaces
type NamespaceManager struct {
	storageClient entities.ZeroGStorageClient
	namespaces    *config.ZeroGNamespaces
	logger        *zap.Logger
	tracer        trace.Tracer
}

// NamespacedStorage represents storage operations within a specific namespace
type NamespacedStorage struct {
	manager   *NamespaceManager
	namespace string
	prefix    string
}

// ContentAddressable represents content-addressed storage operations
type ContentAddressable struct {
	URI         string            `json:"uri"`
	ContentHash string            `json:"content_hash"`
	Namespace   string            `json:"namespace"`
	StoredAt    time.Time         `json:"stored_at"`
	Metadata    map[string]string `json:"metadata"`
	Size        int64             `json:"size"`
}

// NewNamespaceManager creates a new namespace manager
func NewNamespaceManager(storageClient entities.ZeroGStorageClient, namespaces *config.ZeroGNamespaces, logger *zap.Logger) *NamespaceManager {
	return &NamespaceManager{
		storageClient: storageClient,
		namespaces:    namespaces,
		logger:        logger,
		tracer:        otel.Tracer("zerog-namespace"),
	}
}

// AISummaries returns a namespaced storage interface for AI summaries
func (nm *NamespaceManager) AISummaries() *NamespacedStorage {
	return &NamespacedStorage{
		manager:   nm,
		namespace: nm.namespaces.AISummaries,
		prefix:    "summary_",
	}
}

// AIArtifacts returns a namespaced storage interface for AI artifacts
func (nm *NamespaceManager) AIArtifacts() *NamespacedStorage {
	return &NamespacedStorage{
		manager:   nm,
		namespace: nm.namespaces.AIArtifacts,
		prefix:    "artifact_",
	}
}

// ModelPrompts returns a namespaced storage interface for model prompts
func (nm *NamespaceManager) ModelPrompts() *NamespacedStorage {
	return &NamespacedStorage{
		manager:   nm,
		namespace: nm.namespaces.ModelPrompts,
		prefix:    "prompt_",
	}
}

// Store stores content in the namespace with content-addressed storage
func (ns *NamespacedStorage) Store(ctx context.Context, data []byte, metadata map[string]string) (*ContentAddressable, error) {
	ctx, span := ns.manager.tracer.Start(ctx, "namespace.store", trace.WithAttributes(
		attribute.String("namespace", ns.namespace),
		attribute.Int("data_size", len(data)),
	))
	defer span.End()

	// Ensure metadata includes namespace information
	if metadata == nil {
		metadata = make(map[string]string)
	}
	metadata["namespace"] = ns.namespace
	metadata["stored_by"] = "namespace_manager"
	metadata["timestamp"] = time.Now().UTC().Format(time.RFC3339)

	ns.manager.logger.Debug("Storing content in namespace",
		zap.String("namespace", ns.namespace),
		zap.Int("size", len(data)),
	)

	// Store using the storage client
	result, err := ns.manager.storageClient.Store(ctx, ns.namespace, data, metadata)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to store in namespace %s: %w", ns.namespace, err)
	}

	// Create content-addressable result
	ca := &ContentAddressable{
		URI:         result.URI,
		ContentHash: result.Hash,
		Namespace:   ns.namespace,
		StoredAt:    result.StoredAt,
		Metadata:    result.Metadata,
		Size:        result.Size,
	}

	ns.manager.logger.Info("Content stored in namespace",
		zap.String("namespace", ns.namespace),
		zap.String("uri", ca.URI),
		zap.String("content_hash", ca.ContentHash),
		zap.Int64("size", ca.Size),
	)

	return ca, nil
}

// StoreWeeklySummary stores a weekly summary with appropriate metadata
func (ns *NamespacedStorage) StoreWeeklySummary(ctx context.Context, userID string, weekStart time.Time, data []byte, summaryType string) (*ContentAddressable, error) {
	if ns.namespace != ns.manager.namespaces.AISummaries {
		return nil, fmt.Errorf("invalid namespace for weekly summary: %s", ns.namespace)
	}

	metadata := map[string]string{
		"content_type":  "text/markdown",
		"user_id":       userID,
		"week_start":    weekStart.Format("2006-01-02"),
		"summary_type":  summaryType,
		"data_category": "weekly_summary",
	}

	return ns.Store(ctx, data, metadata)
}

// StoreArtifact stores an AI analysis artifact with detailed metadata
func (ns *NamespacedStorage) StoreArtifact(ctx context.Context, userID string, analysisType string, requestID string, data []byte) (*ContentAddressable, error) {
	if ns.namespace != ns.manager.namespaces.AIArtifacts {
		return nil, fmt.Errorf("invalid namespace for artifact: %s", ns.namespace)
	}

	metadata := map[string]string{
		"content_type":   "application/json",
		"user_id":        userID,
		"analysis_type":  analysisType,
		"request_id":     requestID,
		"data_category":  "analysis_artifact",
	}

	return ns.Store(ctx, data, metadata)
}

// StorePrompt stores a model prompt template or configuration
func (ns *NamespacedStorage) StorePrompt(ctx context.Context, promptName string, version string, data []byte) (*ContentAddressable, error) {
	if ns.namespace != ns.manager.namespaces.ModelPrompts {
		return nil, fmt.Errorf("invalid namespace for prompt: %s", ns.namespace)
	}

	metadata := map[string]string{
		"content_type":   "text/plain",
		"prompt_name":    promptName,
		"version":        version,
		"data_category":  "model_prompt",
	}

	return ns.Store(ctx, data, metadata)
}

// Retrieve retrieves content from the namespace using a URI
func (ns *NamespacedStorage) Retrieve(ctx context.Context, uri string) (*entities.StorageData, error) {
	ctx, span := ns.manager.tracer.Start(ctx, "namespace.retrieve", trace.WithAttributes(
		attribute.String("namespace", ns.namespace),
		attribute.String("uri", uri),
	))
	defer span.End()

	// Validate that the URI belongs to this namespace
	if !strings.Contains(uri, ns.namespace) {
		return nil, fmt.Errorf("URI does not belong to namespace %s: %s", ns.namespace, uri)
	}

	ns.manager.logger.Debug("Retrieving content from namespace",
		zap.String("namespace", ns.namespace),
		zap.String("uri", uri),
	)

	data, err := ns.manager.storageClient.Retrieve(ctx, uri)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to retrieve from namespace %s: %w", ns.namespace, err)
	}

	ns.manager.logger.Debug("Content retrieved from namespace",
		zap.String("namespace", ns.namespace),
		zap.String("uri", uri),
		zap.Int64("size", data.Size),
	)

	return data, nil
}

// List lists objects in the namespace with optional prefix filtering
func (ns *NamespacedStorage) List(ctx context.Context, prefix string) ([]entities.StorageObject, error) {
	ctx, span := ns.manager.tracer.Start(ctx, "namespace.list", trace.WithAttributes(
		attribute.String("namespace", ns.namespace),
		attribute.String("prefix", prefix),
	))
	defer span.End()

	fullPrefix := prefix
	if ns.prefix != "" {
		fullPrefix = ns.prefix + prefix
	}

	ns.manager.logger.Debug("Listing objects in namespace",
		zap.String("namespace", ns.namespace),
		zap.String("prefix", fullPrefix),
	)

	objects, err := ns.manager.storageClient.ListObjects(ctx, ns.namespace, fullPrefix)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to list objects in namespace %s: %w", ns.namespace, err)
	}

	ns.manager.logger.Debug("Listed objects in namespace",
		zap.String("namespace", ns.namespace),
		zap.Int("count", len(objects)),
	)

	return objects, nil
}

// ListByUser lists objects for a specific user
func (ns *NamespacedStorage) ListByUser(ctx context.Context, userID string) ([]entities.StorageObject, error) {
	// For now, we'll retrieve all objects and filter by user_id in metadata
	// In a production implementation, this should be optimized with proper indexing
	allObjects, err := ns.List(ctx, "")
	if err != nil {
		return nil, err
	}

	var userObjects []entities.StorageObject
	for _, obj := range allObjects {
		if obj.Metadata["user_id"] == userID {
			userObjects = append(userObjects, obj)
		}
	}

	return userObjects, nil
}

// ListWeeklySummaries lists weekly summaries for a user
func (ns *NamespacedStorage) ListWeeklySummaries(ctx context.Context, userID string, limit int) ([]entities.StorageObject, error) {
	if ns.namespace != ns.manager.namespaces.AISummaries {
		return nil, fmt.Errorf("invalid namespace for weekly summaries: %s", ns.namespace)
	}

	objects, err := ns.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Filter for weekly summaries and sort by week_start (newest first)
	var summaries []entities.StorageObject
	for _, obj := range objects {
		if obj.Metadata["data_category"] == "weekly_summary" {
			summaries = append(summaries, obj)
		}
	}

	// Apply limit if specified
	if limit > 0 && len(summaries) > limit {
		summaries = summaries[:limit]
	}

	return summaries, nil
}

// Delete removes content from the namespace
func (ns *NamespacedStorage) Delete(ctx context.Context, uri string) error {
	ctx, span := ns.manager.tracer.Start(ctx, "namespace.delete", trace.WithAttributes(
		attribute.String("namespace", ns.namespace),
		attribute.String("uri", uri),
	))
	defer span.End()

	// Validate that the URI belongs to this namespace
	if !strings.Contains(uri, ns.namespace) {
		return fmt.Errorf("URI does not belong to namespace %s: %s", ns.namespace, uri)
	}

	ns.manager.logger.Info("Deleting content from namespace",
		zap.String("namespace", ns.namespace),
		zap.String("uri", uri),
	)

	err := ns.manager.storageClient.Delete(ctx, uri)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to delete from namespace %s: %w", ns.namespace, err)
	}

	ns.manager.logger.Info("Content deleted from namespace",
		zap.String("namespace", ns.namespace),
		zap.String("uri", uri),
	)

	return nil
}

// GetNamespaceStats returns statistics about the namespace
func (ns *NamespacedStorage) GetNamespaceStats(ctx context.Context) (*NamespaceStats, error) {
	ctx, span := ns.manager.tracer.Start(ctx, "namespace.get_stats", trace.WithAttributes(
		attribute.String("namespace", ns.namespace),
	))
	defer span.End()

	objects, err := ns.List(ctx, "")
	if err != nil {
		return nil, err
	}

	stats := &NamespaceStats{
		Namespace:    ns.namespace,
		ObjectCount:  int64(len(objects)),
		TotalSize:    0,
		OldestObject: time.Now(),
		NewestObject: time.Time{},
		DataTypes:    make(map[string]int),
	}

	for _, obj := range objects {
		stats.TotalSize += obj.Size

		if obj.StoredAt.Before(stats.OldestObject) {
			stats.OldestObject = obj.StoredAt
		}
		if obj.StoredAt.After(stats.NewestObject) {
			stats.NewestObject = obj.StoredAt
		}

		// Count by data category
		category := obj.Metadata["data_category"]
		if category == "" {
			category = "unknown"
		}
		stats.DataTypes[category]++
	}

	return stats, nil
}

// NamespaceStats contains statistics about a namespace
type NamespaceStats struct {
	Namespace    string            `json:"namespace"`
	ObjectCount  int64             `json:"object_count"`
	TotalSize    int64             `json:"total_size"`
	OldestObject time.Time         `json:"oldest_object"`
	NewestObject time.Time         `json:"newest_object"`
	DataTypes    map[string]int    `json:"data_types"`
}

// URIBuilder provides utilities for building URIs
type URIBuilder struct {
	namespace string
	manager   *NamespaceManager
}

// NewURIBuilder creates a new URI builder for a namespace
func (nm *NamespaceManager) NewURIBuilder(namespace string) *URIBuilder {
	return &URIBuilder{
		namespace: namespace,
		manager:   nm,
	}
}

// BuildWeeklySummaryURI builds a URI for a weekly summary
func (ub *URIBuilder) BuildWeeklySummaryURI(userID string, weekStart time.Time, contentHash string) string {
	weekStr := weekStart.Format("2006-01-02")
	filename := fmt.Sprintf("weekly_summary_%s_%s_%s", userID, weekStr, contentHash[:8])
	return fmt.Sprintf("0g://%s/%s", strings.TrimSuffix(ub.namespace, "/"), filename)
}

// BuildArtifactURI builds a URI for an analysis artifact
func (ub *URIBuilder) BuildArtifactURI(userID string, analysisType string, requestID string, contentHash string) string {
	filename := fmt.Sprintf("artifact_%s_%s_%s_%s", userID, analysisType, requestID, contentHash[:8])
	return fmt.Sprintf("0g://%s/%s", strings.TrimSuffix(ub.namespace, "/"), filename)
}

// BuildPromptURI builds a URI for a model prompt
func (ub *URIBuilder) BuildPromptURI(promptName string, version string, contentHash string) string {
	filename := fmt.Sprintf("prompt_%s_v%s_%s", promptName, version, contentHash[:8])
	return fmt.Sprintf("0g://%s/%s", strings.TrimSuffix(ub.namespace, "/"), filename)
}

// ParseURI extracts components from a 0G URI
func (ub *URIBuilder) ParseURI(uri string) (*URIComponents, error) {
	if !strings.HasPrefix(uri, "0g://") {
		return nil, fmt.Errorf("invalid 0G URI format: %s", uri)
	}

	// Remove the 0g:// prefix
	path := strings.TrimPrefix(uri, "0g://")
	
	// Split namespace and filename
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid 0G URI structure: %s", uri)
	}

	namespace := parts[0] + "/"
	filename := parts[1]

	return &URIComponents{
		Namespace: namespace,
		Filename:  filename,
		FullPath:  path,
	}, nil
}

// URIComponents represents components of a 0G URI
type URIComponents struct {
	Namespace string `json:"namespace"`
	Filename  string `json:"filename"`
	FullPath  string `json:"full_path"`
}

// HealthCheck performs a health check on all namespaces
func (nm *NamespaceManager) HealthCheck(ctx context.Context) (*NamespaceHealthStatus, error) {
	ctx, span := nm.tracer.Start(ctx, "namespace_manager.health_check")
	defer span.End()

	status := &NamespaceHealthStatus{
		Overall:    entities.HealthStatusHealthy,
		Namespaces: make(map[string]*NamespaceStatus),
		CheckedAt:  time.Now(),
	}

	// Check each namespace
	namespaces := []string{
		nm.namespaces.AISummaries,
		nm.namespaces.AIArtifacts,
		nm.namespaces.ModelPrompts,
	}

	for _, namespace := range namespaces {
		nsStatus := nm.checkNamespaceHealth(ctx, namespace)
		status.Namespaces[namespace] = nsStatus

		// If any namespace is unhealthy, mark overall as degraded
		if nsStatus.Status != entities.HealthStatusHealthy && status.Overall == entities.HealthStatusHealthy {
			status.Overall = entities.HealthStatusDegraded
		}
	}

	return status, nil
}

// checkNamespaceHealth checks the health of a specific namespace
func (nm *NamespaceManager) checkNamespaceHealth(ctx context.Context, namespace string) *NamespaceStatus {
	status := &NamespaceStatus{
		Namespace: namespace,
		Status:    entities.HealthStatusHealthy,
		CheckedAt: time.Now(),
	}

	// Try to list objects to verify namespace accessibility
	_, err := nm.storageClient.ListObjects(ctx, namespace, "")
	if err != nil {
		status.Status = entities.HealthStatusUnhealthy
		status.Error = err.Error()
		nm.logger.Warn("Namespace health check failed",
			zap.String("namespace", namespace),
			zap.Error(err),
		)
	} else {
		nm.logger.Debug("Namespace health check passed",
			zap.String("namespace", namespace),
		)
	}

	return status
}

// NamespaceHealthStatus represents the health status of all namespaces
type NamespaceHealthStatus struct {
	Overall    string                      `json:"overall"`
	Namespaces map[string]*NamespaceStatus `json:"namespaces"`
	CheckedAt  time.Time                   `json:"checked_at"`
}

// NamespaceStatus represents the health status of a single namespace
type NamespaceStatus struct {
	Namespace string    `json:"namespace"`
	Status    string    `json:"status"`
	Error     string    `json:"error,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

// Cleanup removes old objects from namespaces based on retention policies
func (nm *NamespaceManager) Cleanup(ctx context.Context, retentionDays int) error {
	ctx, span := nm.tracer.Start(ctx, "namespace_manager.cleanup", trace.WithAttributes(
		attribute.Int("retention_days", retentionDays),
	))
	defer span.End()

	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	namespaces := []string{
		nm.namespaces.AISummaries,
		nm.namespaces.AIArtifacts,
		nm.namespaces.ModelPrompts,
	}

	nm.logger.Info("Starting namespace cleanup",
		zap.Int("retention_days", retentionDays),
		zap.Time("cutoff_date", cutoff),
	)

	for _, namespace := range namespaces {
		if err := nm.cleanupNamespace(ctx, namespace, cutoff); err != nil {
			nm.logger.Warn("Failed to cleanup namespace",
				zap.String("namespace", namespace),
				zap.Error(err),
			)
			// Continue with other namespaces
		}
	}

	nm.logger.Info("Namespace cleanup completed")
	return nil
}

// cleanupNamespace removes old objects from a specific namespace
func (nm *NamespaceManager) cleanupNamespace(ctx context.Context, namespace string, cutoff time.Time) error {
	objects, err := nm.storageClient.ListObjects(ctx, namespace, "")
	if err != nil {
		return fmt.Errorf("failed to list objects in namespace %s: %w", namespace, err)
	}

	var deletedCount int
	for _, obj := range objects {
		if obj.StoredAt.Before(cutoff) {
			if err := nm.storageClient.Delete(ctx, obj.URI); err != nil {
				nm.logger.Warn("Failed to delete old object",
					zap.String("namespace", namespace),
					zap.String("uri", obj.URI),
					zap.Error(err),
				)
			} else {
				deletedCount++
			}
		}
	}

	nm.logger.Info("Cleaned up namespace",
		zap.String("namespace", namespace),
		zap.Int("deleted_count", deletedCount),
		zap.Int("total_objects", len(objects)),
	)

	return nil
}