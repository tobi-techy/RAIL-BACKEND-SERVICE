package metrics

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type Collector struct {
	storageUploads      metric.Int64Counter
	storageDownloads    metric.Int64Counter
	storageBytes        metric.Int64Counter
	storageErrors       metric.Int64Counter
	storageDuration     metric.Float64Histogram
	computeRequests     metric.Int64Counter
	computeTokens       metric.Int64Counter
	computeErrors       metric.Int64Counter
	computeDuration     metric.Float64Histogram
	costTotal           metric.Float64Counter
	quotaUsage          metric.Int64Gauge
	mu                  sync.RWMutex
	costs               map[string]float64
}

func NewCollector() (*Collector, error) {
	meter := otel.Meter("zerog")

	storageUploads, err := meter.Int64Counter("zerog.storage.uploads.total")
	if err != nil {
		return nil, err
	}

	storageDownloads, err := meter.Int64Counter("zerog.storage.downloads.total")
	if err != nil {
		return nil, err
	}

	storageBytes, err := meter.Int64Counter("zerog.storage.bytes.total")
	if err != nil {
		return nil, err
	}

	storageErrors, err := meter.Int64Counter("zerog.storage.errors.total")
	if err != nil {
		return nil, err
	}

	storageDuration, err := meter.Float64Histogram("zerog.storage.duration.seconds")
	if err != nil {
		return nil, err
	}

	computeRequests, err := meter.Int64Counter("zerog.compute.requests.total")
	if err != nil {
		return nil, err
	}

	computeTokens, err := meter.Int64Counter("zerog.compute.tokens.total")
	if err != nil {
		return nil, err
	}

	computeErrors, err := meter.Int64Counter("zerog.compute.errors.total")
	if err != nil {
		return nil, err
	}

	computeDuration, err := meter.Float64Histogram("zerog.compute.duration.seconds")
	if err != nil {
		return nil, err
	}

	costTotal, err := meter.Float64Counter("zerog.cost.total.usd")
	if err != nil {
		return nil, err
	}

	quotaUsage, err := meter.Int64Gauge("zerog.quota.usage.percent")
	if err != nil {
		return nil, err
	}

	return &Collector{
		storageUploads:   storageUploads,
		storageDownloads: storageDownloads,
		storageBytes:     storageBytes,
		storageErrors:    storageErrors,
		storageDuration:  storageDuration,
		computeRequests:  computeRequests,
		computeTokens:    computeTokens,
		computeErrors:    computeErrors,
		computeDuration:  computeDuration,
		costTotal:        costTotal,
		quotaUsage:       quotaUsage,
		costs:            make(map[string]float64),
	}, nil
}

func (c *Collector) RecordUpload(ctx context.Context, bytes int64, duration time.Duration, err error) {
	attrs := []attribute.KeyValue{
		attribute.Bool("success", err == nil),
	}

	c.storageUploads.Add(ctx, 1, metric.WithAttributes(attrs...))
	if err == nil {
		c.storageBytes.Add(ctx, bytes, metric.WithAttributes(attribute.String("operation", "upload")))
		c.storageDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(attribute.String("operation", "upload")))
	} else {
		c.storageErrors.Add(ctx, 1, metric.WithAttributes(attribute.String("operation", "upload")))
	}
}

func (c *Collector) RecordDownload(ctx context.Context, bytes int64, duration time.Duration, err error) {
	attrs := []attribute.KeyValue{
		attribute.Bool("success", err == nil),
	}

	c.storageDownloads.Add(ctx, 1, metric.WithAttributes(attrs...))
	if err == nil {
		c.storageBytes.Add(ctx, bytes, metric.WithAttributes(attribute.String("operation", "download")))
		c.storageDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(attribute.String("operation", "download")))
	} else {
		c.storageErrors.Add(ctx, 1, metric.WithAttributes(attribute.String("operation", "download")))
	}
}

func (c *Collector) RecordInference(ctx context.Context, model string, tokens int, duration time.Duration, err error) {
	attrs := []attribute.KeyValue{
		attribute.String("model", model),
		attribute.Bool("success", err == nil),
	}

	c.computeRequests.Add(ctx, 1, metric.WithAttributes(attrs...))
	if err == nil {
		c.computeTokens.Add(ctx, int64(tokens), metric.WithAttributes(attribute.String("model", model)))
		c.computeDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(attribute.String("model", model)))
	} else {
		c.computeErrors.Add(ctx, 1, metric.WithAttributes(attribute.String("model", model)))
	}
}

func (c *Collector) RecordCost(ctx context.Context, service string, amount float64) {
	c.mu.Lock()
	c.costs[service] += amount
	c.mu.Unlock()

	c.costTotal.Add(ctx, amount, metric.WithAttributes(attribute.String("service", service)))
}

func (c *Collector) UpdateQuota(ctx context.Context, userID string, usagePercent int64) {
	c.quotaUsage.Record(ctx, usagePercent, metric.WithAttributes(attribute.String("user_id", userID)))
}

func (c *Collector) GetTotalCost(service string) float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.costs[service]
}
