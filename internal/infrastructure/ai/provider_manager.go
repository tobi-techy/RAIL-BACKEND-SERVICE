package ai

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// ProviderManager manages multiple AI providers with automatic failover
type ProviderManager struct {
	primary       AIProvider
	fallbacks     []AIProvider
	logger        *zap.Logger
	tracer        trace.Tracer
	retryAttempts int
	retryDelay    time.Duration
}

// ProviderManagerConfig configures the provider manager
type ProviderManagerConfig struct {
	RetryAttempts int
	RetryDelay    time.Duration
}

// NewProviderManager creates a new provider manager
func NewProviderManager(primary AIProvider, fallbacks []AIProvider, config *ProviderManagerConfig, logger *zap.Logger) *ProviderManager {
	if config == nil {
		config = &ProviderManagerConfig{
			RetryAttempts: 2,
			RetryDelay:    time.Second,
		}
	}

	return &ProviderManager{
		primary:       primary,
		fallbacks:     fallbacks,
		logger:        logger,
		tracer:        otel.Tracer("provider-manager"),
		retryAttempts: config.RetryAttempts,
		retryDelay:    config.RetryDelay,
	}
}

// ChatCompletion performs chat completion with automatic failover
func (m *ProviderManager) ChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return m.ChatCompletionWithTools(ctx, req, nil)
}

// ChatCompletionWithTools performs chat completion with tools and automatic failover
func (m *ProviderManager) ChatCompletionWithTools(ctx context.Context, req *ChatRequest, tools []Tool) (*ChatResponse, error) {
	ctx, span := m.tracer.Start(ctx, "provider_manager.chat_completion", trace.WithAttributes(
		attribute.Int("message_count", len(req.Messages)),
		attribute.Int("tool_count", len(tools)),
	))
	defer span.End()

	// Try primary provider first
	providers := []AIProvider{m.primary}
	providers = append(providers, m.fallbacks...)

	// For "fast" hint, prefer fallback providers (typically faster/cheaper models)
	// over the primary reasoning model which may have a long timeout.
	if req.ModelHint == "fast" && len(m.fallbacks) > 0 {
		providers = make([]AIProvider, 0, len(m.fallbacks)+1)
		providers = append(providers, m.fallbacks...)
		providers = append(providers, m.primary)
	}

	var lastErr error

	for i, provider := range providers {
		isPrimary := i == 0
		providerName := provider.Name()

		m.logger.Debug("Attempting chat completion",
			zap.String("provider", providerName),
			zap.Bool("is_primary", isPrimary),
			zap.Int("attempt", i+1),
		)

		// Try the provider with retries
		resp, err := m.tryProviderWithRetry(ctx, provider, req, tools)
		if err == nil {
			span.SetAttributes(
				attribute.String("provider_used", providerName),
				attribute.Int("provider_attempt", i+1),
			)

			m.logger.Info("Chat completion successful",
				zap.String("provider", providerName),
				zap.Bool("is_primary", isPrimary),
				zap.Int("tokens", resp.TokensUsed),
			)

			return resp, nil
		}

		lastErr = err

		// If the context is already canceled, no point trying other providers
		if ctx.Err() != nil {
			break
		}

		// Log the failure
		m.logger.Warn("Provider failed",
			zap.String("provider", providerName),
			zap.Error(err),
			zap.Bool("is_primary", isPrimary),
		)

		// If this was a non-retryable error, try next provider immediately
		if provErr, ok := err.(*ProviderError); ok && !provErr.Retryable {
			m.logger.Debug("Non-retryable error, skipping to next provider",
				zap.String("provider", providerName),
				zap.String("error_code", provErr.Code),
			)
			continue
		}

		// If we have more providers to try, continue
		if i < len(providers)-1 {
			m.logger.Info("Failing over to next provider",
				zap.String("from", providerName),
				zap.String("to", providers[i+1].Name()),
			)
			continue
		}
	}

	// All providers failed
	span.RecordError(lastErr)
	span.SetAttributes(attribute.Bool("all_providers_failed", true))

	if ctx.Err() != nil {
		m.logger.Warn("AI providers cancelled (client disconnect)",
			zap.Error(ctx.Err()),
			zap.Int("providers_tried", len(providers)),
		)
	} else {
		m.logger.Error("All AI providers failed",
			zap.Error(lastErr),
			zap.Int("providers_tried", len(providers)),
		)
	}

	return nil, fmt.Errorf("all AI providers failed, last error: %w", lastErr)
}

// tryProviderWithRetry attempts to use a provider with exponential backoff retries
func (m *ProviderManager) tryProviderWithRetry(ctx context.Context, provider AIProvider, req *ChatRequest, tools []Tool) (*ChatResponse, error) {
	var lastErr error

	for attempt := 0; attempt <= m.retryAttempts; attempt++ {
		if attempt > 0 {
			// Exponential backoff
			delay := m.retryDelay * time.Duration(1<<uint(attempt-1))
			if provErr, ok := lastErr.(*ProviderError); ok && provErr.RetryAfter > delay {
				delay = provErr.RetryAfter
			}
			m.logger.Debug("Retrying after delay",
				zap.String("provider", provider.Name()),
				zap.Int("attempt", attempt+1),
				zap.Duration("delay", delay),
			)

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		// Attempt the request
		resp, err := provider.ChatCompletionWithTools(ctx, req, tools)
		if err == nil {
			return resp, nil
		}

		lastErr = err

		// Check if error is retryable
		if provErr, ok := err.(*ProviderError); ok {
			if !provErr.Retryable {
				m.logger.Debug("Non-retryable error, stopping retries",
					zap.String("provider", provider.Name()),
					zap.String("error_code", provErr.Code),
				)
				return nil, provErr
			}
		}
	}

	return nil, lastErr
}

// Name returns the primary provider's name.
func (m *ProviderManager) Name() string {
	return m.primary.Name()
}

// IsAvailable returns true if the primary provider is available.
func (m *ProviderManager) IsAvailable(ctx context.Context) bool {
	return m.primary.IsAvailable(ctx)
}

// GetPrimaryProvider returns the primary provider
func (m *ProviderManager) GetPrimaryProvider() AIProvider {
	return m.primary
}

// GetAllProviders returns all providers (primary + fallbacks)
func (m *ProviderManager) GetAllProviders() []AIProvider {
	providers := []AIProvider{m.primary}
	providers = append(providers, m.fallbacks...)
	return providers
}

// ChatCompletionStream delegates streaming to the primary provider only.
// Streaming failover is intentionally not supported because stream chunks are
// stateful — switching providers mid-stream would corrupt the response.
func (m *ProviderManager) ChatCompletionStream(ctx context.Context, req *ChatRequest, tools []Tool, ch chan<- StreamChunk) error {
	streamer, ok := m.primary.(StreamProvider)
	if !ok {
		return fmt.Errorf("primary provider %s does not support streaming", m.primary.Name())
	}
	return streamer.ChatCompletionStream(ctx, req, tools, ch)
}

// CheckProvidersHealth checks the health of all providers
func (m *ProviderManager) CheckProvidersHealth(ctx context.Context) map[string]bool {
	providers := m.GetAllProviders()
	health := make(map[string]bool, len(providers))

	for _, provider := range providers {
		health[provider.Name()] = provider.IsAvailable(ctx)
	}

	return health
}
