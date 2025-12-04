package treasury

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// ConversionProvider defines the interface for currency conversion providers
type ConversionProvider interface {
	// GetName returns the provider name
	GetName() string

	// GetProviderType returns the provider type (e.g., "due", "zerohash")
	GetProviderType() string

	// InitiateConversion initiates a conversion operation
	// Returns the provider's transaction ID
	InitiateConversion(ctx context.Context, req *ConversionRequest) (*ConversionResponse, error)

	// GetConversionStatus checks the status of a conversion
	GetConversionStatus(ctx context.Context, providerTxID string) (*ConversionStatusResponse, error)

	// CancelConversion cancels a pending conversion
	CancelConversion(ctx context.Context, providerTxID string) error

	// SupportsDirection checks if provider supports the conversion direction
	SupportsDirection(direction entities.ConversionDirection) bool

	// ValidateAmount checks if the amount is within provider's limits
	ValidateAmount(amount decimal.Decimal, direction entities.ConversionDirection) error

	// EstimateFees estimates fees for a conversion
	EstimateFees(ctx context.Context, amount decimal.Decimal, direction entities.ConversionDirection) (*FeeEstimate, error)
}

// ConversionRequest represents a request to initiate a conversion
type ConversionRequest struct {
	Direction         entities.ConversionDirection
	SourceAmount      decimal.Decimal
	SourceCurrency    string // "USDC" or "USD"
	DestinationCurrency string // "USD" or "USDC"
	IdempotencyKey    string
	Metadata          map[string]interface{}
}

// Validate checks if the conversion request is valid
func (r *ConversionRequest) Validate() error {
	if !r.Direction.IsValid() {
		return fmt.Errorf("invalid conversion direction")
	}
	if r.SourceAmount.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("source amount must be positive")
	}
	if r.SourceCurrency == "" {
		return fmt.Errorf("source currency is required")
	}
	if r.DestinationCurrency == "" {
		return fmt.Errorf("destination currency is required")
	}
	if r.IdempotencyKey == "" {
		return fmt.Errorf("idempotency key is required")
	}
	
	// Validate currency pairs match direction
	if r.Direction == entities.ConversionDirectionUSDCToUSD {
		if r.SourceCurrency != "USDC" || r.DestinationCurrency != "USD" {
			return fmt.Errorf("invalid currency pair for USDC to USD conversion")
		}
	} else if r.Direction == entities.ConversionDirectionUSDToUSDC {
		if r.SourceCurrency != "USD" || r.DestinationCurrency != "USDC" {
			return fmt.Errorf("invalid currency pair for USD to USDC conversion")
		}
	}
	
	return nil
}

// ConversionResponse represents the response from initiating a conversion
type ConversionResponse struct {
	ProviderTxID      string
	Status            string
	SourceAmount      decimal.Decimal
	DestinationAmount *decimal.Decimal // Estimated, may be nil if unknown
	ExchangeRate      *decimal.Decimal
	Fees              *decimal.Decimal
	EstimatedCompletionTime *int // In seconds
	ProviderResponse  map[string]interface{} // Raw provider response
}

// ConversionStatusResponse represents the status of a conversion
type ConversionStatusResponse struct {
	ProviderTxID      string
	Status            ConversionProviderStatus
	SourceAmount      decimal.Decimal
	DestinationAmount *decimal.Decimal
	ExchangeRate      *decimal.Decimal
	Fees              *decimal.Decimal
	CompletedAt       *string // ISO timestamp
	FailureReason     *string
	ProviderResponse  map[string]interface{}
}

// ConversionProviderStatus represents the provider's status for a conversion
type ConversionProviderStatus string

const (
	ConversionProviderStatusPending    ConversionProviderStatus = "pending"
	ConversionProviderStatusProcessing ConversionProviderStatus = "processing"
	ConversionProviderStatusCompleted  ConversionProviderStatus = "completed"
	ConversionProviderStatusFailed     ConversionProviderStatus = "failed"
	ConversionProviderStatusCancelled  ConversionProviderStatus = "cancelled"
)

// ToJobStatus converts provider status to internal job status
func (s ConversionProviderStatus) ToJobStatus() entities.ConversionJobStatus {
	switch s {
	case ConversionProviderStatusPending:
		return entities.ConversionJobStatusProviderSubmitted
	case ConversionProviderStatusProcessing:
		return entities.ConversionJobStatusProviderProcessing
	case ConversionProviderStatusCompleted:
		return entities.ConversionJobStatusProviderCompleted
	case ConversionProviderStatusFailed:
		return entities.ConversionJobStatusFailed
	case ConversionProviderStatusCancelled:
		return entities.ConversionJobStatusCancelled
	default:
		return entities.ConversionJobStatusFailed
	}
}

// FeeEstimate represents estimated fees for a conversion
type FeeEstimate struct {
	TotalFee         decimal.Decimal
	NetworkFee       *decimal.Decimal
	ProviderFee      *decimal.Decimal
	EstimatedRate    *decimal.Decimal
	EstimatedOutput  *decimal.Decimal
}

// ProviderSelector selects the best provider for a conversion
type ProviderSelector struct {
	providers []*entities.ConversionProvider
}

// NewProviderSelector creates a new provider selector
func NewProviderSelector(providers []*entities.ConversionProvider) *ProviderSelector {
	return &ProviderSelector{
		providers: providers,
	}
}

// SelectProvider selects the best available provider for a conversion
// Selection criteria (in order):
// 1. Provider must be active and healthy
// 2. Provider must support the direction
// 3. Provider must have capacity for the amount
// 4. Lowest priority number wins (higher priority)
func (s *ProviderSelector) SelectProvider(amount decimal.Decimal, direction entities.ConversionDirection) (*entities.ConversionProvider, error) {
	var bestProvider *entities.ConversionProvider
	
	for _, provider := range s.providers {
		// Check if provider is healthy and active
		if !provider.IsHealthy() {
			continue
		}
		
		// Check if provider supports the direction
		if direction == entities.ConversionDirectionUSDCToUSD && !provider.SupportsUSDCToUSD {
			continue
		}
		if direction == entities.ConversionDirectionUSDToUSDC && !provider.SupportsUSDToUSDC {
			continue
		}
		
		// Check if provider has capacity
		if !provider.HasCapacity(amount) {
			continue
		}
		
		// Select provider with lowest priority number (highest priority)
		if bestProvider == nil || provider.Priority < bestProvider.Priority {
			bestProvider = provider
		}
	}
	
	if bestProvider == nil {
		return nil, fmt.Errorf("no available provider found for conversion")
	}
	
	return bestProvider, nil
}

// GetProviderByType returns a provider by its type
func (s *ProviderSelector) GetProviderByType(providerType string) (*entities.ConversionProvider, error) {
	for _, provider := range s.providers {
		if provider.ProviderType == providerType {
			return provider, nil
		}
	}
	return nil, fmt.Errorf("provider not found: %s", providerType)
}

// ListHealthyProviders returns all healthy providers
func (s *ProviderSelector) ListHealthyProviders() []*entities.ConversionProvider {
	var healthy []*entities.ConversionProvider
	for _, provider := range s.providers {
		if provider.IsHealthy() {
			healthy = append(healthy, provider)
		}
	}
	return healthy
}

// ProviderFactory creates conversion provider adapters
type ProviderFactory interface {
	CreateProvider(config entities.ConversionProvider) (ConversionProvider, error)
}

// BaseProviderFactory provides basic factory functionality
type BaseProviderFactory struct {
	constructors map[string]func(entities.ConversionProvider) (ConversionProvider, error)
}

// NewBaseProviderFactory creates a new base provider factory
func NewBaseProviderFactory() *BaseProviderFactory {
	return &BaseProviderFactory{
		constructors: make(map[string]func(entities.ConversionProvider) (ConversionProvider, error)),
	}
}

// Register registers a provider constructor
func (f *BaseProviderFactory) Register(providerType string, constructor func(entities.ConversionProvider) (ConversionProvider, error)) {
	f.constructors[providerType] = constructor
}

// CreateProvider creates a provider adapter
func (f *BaseProviderFactory) CreateProvider(config entities.ConversionProvider) (ConversionProvider, error) {
	constructor, exists := f.constructors[config.ProviderType]
	if !exists {
		return nil, fmt.Errorf("unsupported provider type: %s", config.ProviderType)
	}
	return constructor(config)
}

// ProviderHealthChecker checks provider health
type ProviderHealthChecker struct {
	providers []ConversionProvider
}

// NewProviderHealthChecker creates a new health checker
func NewProviderHealthChecker(providers []ConversionProvider) *ProviderHealthChecker {
	return &ProviderHealthChecker{
		providers: providers,
	}
}

// CheckAll checks health of all providers
func (c *ProviderHealthChecker) CheckAll(ctx context.Context) map[string]bool {
	results := make(map[string]bool)
	for _, provider := range c.providers {
		// Attempt a small test conversion status check
		// In real implementation, you might have a health endpoint
		_, err := provider.GetConversionStatus(ctx, "health-check-dummy")
		results[provider.GetName()] = err == nil
	}
	return results
}

// ErrorWithRetry wraps an error with retry information
type ErrorWithRetry struct {
	Err       error
	Retryable bool
}

// Error implements the error interface
func (e *ErrorWithRetry) Error() string {
	return e.Err.Error()
}

// Unwrap returns the underlying error
func (e *ErrorWithRetry) Unwrap() error {
	return e.Err
}

// NewRetryableError creates a new retryable error
func NewRetryableError(err error) *ErrorWithRetry {
	return &ErrorWithRetry{
		Err:       err,
		Retryable: true,
	}
}

// NewNonRetryableError creates a new non-retryable error
func NewNonRetryableError(err error) *ErrorWithRetry {
	return &ErrorWithRetry{
		Err:       err,
		Retryable: false,
	}
}

// IsRetryable checks if an error is retryable
func IsRetryable(err error) bool {
	if errWithRetry, ok := err.(*ErrorWithRetry); ok {
		return errWithRetry.Retryable
	}
	// Default to retryable for unknown errors
	return true
}
