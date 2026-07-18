package memory

import (
	"context"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/rail-service/rail_service/internal/infrastructure/supermemory"
	"github.com/sony/gobreaker"
	"go.uber.org/zap"
)

// SupermemoryConfig configures the enhanced Supermemory client.
type SupermemoryConfig struct {
	// Circuit breaker: after this many consecutive failures, open the circuit.
	CircuitBreakerThreshold uint
	// How long to wait before trying again after circuit opens.
	CircuitBreakerCooldown time.Duration
	// Maximum time to wait for a Supermemory API call.
	RequestTimeout time.Duration
	// If true, redact emails, phone numbers, account numbers before sending.
	RedactPII bool
	// Minimum similarity threshold for search results.
	MinSimilarity float64
}

// DefaultSupermemoryConfig returns sensible defaults.
func DefaultSupermemoryConfig() SupermemoryConfig {
	return SupermemoryConfig{
		CircuitBreakerThreshold: 5,
		CircuitBreakerCooldown:  30 * time.Second,
		RequestTimeout:          10 * time.Second,
		RedactPII:               true,
		MinSimilarity:           0.50,
	}
}

// SupermemoryClient wraps the raw supermemory.Client with circuit breaking,
// PII redaction, metrics, and consistent error handling.
type SupermemoryClient struct {
	raw     *supermemory.Client
	cb      *gobreaker.CircuitBreaker
	config  SupermemoryConfig
	metrics *SupermemoryMetrics
	logger  *zap.Logger

	// PII patterns compiled once
	emailRE   *regexp.Regexp
	phoneRE   *regexp.Regexp
	accountRE *regexp.Regexp
	ssnRE     *regexp.Regexp
}

// SupermemoryMetrics tracks call counts for monitoring.
type SupermemoryMetrics struct {
	mu             sync.RWMutex
	TotalCalls     int64
	SuccessCalls   int64
	FailedCalls    int64
	CircuitOpens   int64
	IngestCount    int64
	SearchCount    int64
	CreateCount    int64
	TotalLatencyMs int64
}

func (m *SupermemoryMetrics) Record(success bool, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.TotalCalls++
	m.TotalLatencyMs += duration.Milliseconds()
	if success {
		m.SuccessCalls++
	} else {
		m.FailedCalls++
	}
}

func (m *SupermemoryMetrics) RecordCircuitOpen() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.CircuitOpens++
}

func (m *SupermemoryMetrics) RecordIngest() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.IngestCount++
}

func (m *SupermemoryMetrics) RecordSearch() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SearchCount++
}

func (m *SupermemoryMetrics) RecordCreate() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.CreateCount++
}

func (m *SupermemoryMetrics) Snapshot() SupermemoryMetrics {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return SupermemoryMetrics{
		TotalCalls:     m.TotalCalls,
		SuccessCalls:   m.SuccessCalls,
		FailedCalls:    m.FailedCalls,
		CircuitOpens:   m.CircuitOpens,
		IngestCount:    m.IngestCount,
		SearchCount:    m.SearchCount,
		CreateCount:    m.CreateCount,
		TotalLatencyMs: m.TotalLatencyMs,
	}
}

// Compile PII patterns once.
var (
	emailRegex   = regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`)
	phoneRegex   = regexp.MustCompile(`(\+?\d{1,3}[-.\s]?)?\(?\d{3}\)?[-.\s]?\d{3}[-.\s]?\d{4}`)
	accountRegex = regexp.MustCompile(`\b\d{8,17}\b`) // bank account / card numbers
	ssnRegex     = regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`)
)

func NewSupermemoryClient(raw *supermemory.Client, config SupermemoryConfig, logger *zap.Logger) *SupermemoryClient {
	cb := gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        "supermemory",
		MaxRequests: uint32(config.CircuitBreakerThreshold),
		Interval:    0,
		Timeout:     config.CircuitBreakerCooldown,
		OnStateChange: func(name string, from, to gobreaker.State) {
			logger.Warn("supermemory circuit breaker state change",
				zap.String("from", from.String()),
				zap.String("to", to.String()),
			)
		},
	})

	return &SupermemoryClient{
		raw:       raw,
		cb:        cb,
		config:    config,
		metrics:   &SupermemoryMetrics{},
		logger:    logger,
		emailRE:   emailRegex,
		phoneRE:   phoneRegex,
		accountRE: accountRegex,
		ssnRE:     ssnRegex,
	}
}

// IngestConversation sends a conversation turn to Supermemory with PII redaction.
func (c *SupermemoryClient) IngestConversation(ctx context.Context, userID string, userMsg, assistantMsg string) error {
	content := userMsg
	if c.config.RedactPII {
		content = c.redactPII(content)
	}

	start := time.Now()
	msg := supermemory.Message{Role: "user", Content: content}

	_, err := c.cb.Execute(func() (interface{}, error) {
		ingestCtx, cancel := context.WithTimeout(ctx, c.config.RequestTimeout)
		defer cancel()
		return nil, c.raw.IngestConversation(ingestCtx, userID, []supermemory.Message{msg})
	})

	c.metrics.Record(err == nil, time.Since(start))
	c.metrics.RecordIngest()
	if err != nil {
		c.logger.Error("supermemory ingest failed", zap.Error(err), zap.String("user_id", userID))
		return fmt.Errorf("supermemory ingest: %w", err)
	}
	return nil
}

// SearchMemory searches Supermemory for relevant memories.
func (c *SupermemoryClient) SearchMemory(ctx context.Context, userID, query string, limit int) ([]Result, error) {
	start := time.Now()

	result, err := c.cb.Execute(func() (interface{}, error) {
		searchCtx, cancel := context.WithTimeout(ctx, c.config.RequestTimeout)
		defer cancel()
		raw, err := c.raw.SearchMemory(searchCtx, userID, query, limit)
		if err != nil {
			return nil, err
		}
		results := make([]Result, 0, len(raw))
		for _, r := range raw {
			if r.Similarity >= c.config.MinSimilarity {
				results = append(results, Result{
					Content:    r.Memory,
					Similarity: r.Similarity,
					Source:     "supermemory",
					Timestamp:  time.Now(),
				})
			}
		}
		return results, nil
	})

	c.metrics.Record(err == nil, time.Since(start))
	c.metrics.RecordSearch()

	if err != nil {
		if err == gobreaker.ErrOpenState {
			c.metrics.RecordCircuitOpen()
			c.logger.Warn("supermemory circuit open, skipping search", zap.String("user_id", userID))
			return nil, nil
		}
		c.logger.Error("supermemory search failed", zap.Error(err), zap.String("user_id", userID))
		return nil, fmt.Errorf("supermemory search: %w", err)
	}

	if res, ok := result.([]Result); ok {
		return res, nil
	}
	return nil, nil
}

// CreateMemories writes memories directly to Supermemory with PII redaction.
func (c *SupermemoryClient) CreateMemories(ctx context.Context, containerTag string, entries []MemoryEntry) error {
	if c.config.RedactPII {
		redacted := make([]MemoryEntry, len(entries))
		for i, e := range entries {
			redacted[i] = e
			redacted[i].Content = c.redactPII(e.Content)
		}
		entries = redacted
	}

	memories := make([]supermemory.Memory, len(entries))
	for i, e := range entries {
		memories[i] = supermemory.Memory{
			Content:  e.Content,
			Metadata: e.Metadata,
		}
		if e.DocumentDate != "" || len(e.EventDate) > 0 {
			memories[i].TemporalContext = &supermemory.TemporalContext{
				DocumentDate: e.DocumentDate,
				EventDate:    e.EventDate,
			}
		}
	}

	start := time.Now()
	_, err := c.cb.Execute(func() (interface{}, error) {
		createCtx, cancel := context.WithTimeout(ctx, c.config.RequestTimeout)
		defer cancel()
		return nil, c.raw.CreateMemories(createCtx, containerTag, memories)
	})

	c.metrics.Record(err == nil, time.Since(start))
	c.metrics.RecordCreate()

	if err != nil {
		c.logger.Error("supermemory create memories failed", zap.Error(err))
		return fmt.Errorf("supermemory create: %w", err)
	}
	return nil
}

// Health returns nil if Supermemory has been reachable recently.
func (c *SupermemoryClient) Health(ctx context.Context) error {
	_, err := c.cb.Execute(func() (interface{}, error) {
		// Lightweight connectivity check — raw.Search with empty query.
		// If the circuit is closed, this will attempt a call.
		searchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_, err := c.raw.SearchMemory(searchCtx, "health-check", "", 1)
		if err != nil {
			return nil, fmt.Errorf("supermemory health check: %w", err)
		}
		return nil, nil
	})
	return err
}

// Metrics returns a snapshot of the metrics counters.
func (c *SupermemoryClient) Metrics() SupermemoryMetrics {
	return c.metrics.Snapshot()
}

// redactPII delegates to the shared, fintech-aware redactor in the supermemory
// package so behaviour is identical across every path that reaches the API. It
// preserves transaction amounts and dates while stripping emails, phones,
// card/account numbers, and SSNs.
func (c *SupermemoryClient) redactPII(text string) string {
	return supermemory.RedactPII(text)
}
