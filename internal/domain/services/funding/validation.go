package funding

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"github.com/rail-service/rail_service/pkg/webhook"
)

// FundingConfig holds funding service configuration
type FundingConfig struct {
	MinDepositAmount     decimal.Decimal
	MaxDepositsPerDay    int
	DepositTimeoutHours  int
	WebhookSecret        string
	BalanceCacheTTL      time.Duration
	RateLimitWindow      time.Duration
}

// DefaultFundingConfig returns default configuration
func DefaultFundingConfig() *FundingConfig {
	return &FundingConfig{
		MinDepositAmount:     decimal.NewFromFloat(entities.MinDepositAmountUSDC),
		MaxDepositsPerDay:    entities.MaxDepositsPerDay,
		DepositTimeoutHours:  entities.DepositTimeoutHours,
		WebhookSecret:        "",
		BalanceCacheTTL:      30 * time.Second,
		RateLimitWindow:      24 * time.Hour,
	}
}

// ValidationService handles funding validation logic
type ValidationService struct {
	cache            cache.RedisClient
	webhookValidator *webhook.WebhookValidator
	config           *FundingConfig
}

// NewValidationService creates a new validation service
func NewValidationService(redisClient cache.RedisClient, config *FundingConfig) *ValidationService {
	var webhookValidator *webhook.WebhookValidator
	if config.WebhookSecret != "" {
		webhookValidator = webhook.NewWebhookValidator(webhook.WebhookSecurityConfig{
			Secret:           config.WebhookSecret,
			MaxTimestampAge:  300, // 5 minutes
			RequireSignature: true,
			MaxPayloadSize:   1024 * 1024,
		})
	}

	return &ValidationService{
		cache:            redisClient,
		webhookValidator: webhookValidator,
		config:           config,
	}
}

// ValidateWebhookSignature validates webhook signature
func (v *ValidationService) ValidateWebhookSignature(payload []byte, signature string, timestamp int64) error {
	if v.webhookValidator == nil {
		// No webhook secret configured - skip validation in development
		return nil
	}
	return v.webhookValidator.ValidateRequest(payload, signature, timestamp, "")
}

// ValidateDepositAmount validates minimum deposit amount
func (v *ValidationService) ValidateDepositAmount(amount decimal.Decimal) error {
	if amount.LessThan(v.config.MinDepositAmount) {
		return fmt.Errorf("deposit amount %s is below minimum %s USDC", 
			amount.String(), v.config.MinDepositAmount.String())
	}
	return nil
}

// CheckDepositRateLimit checks if user has exceeded deposit address creation limit
func (v *ValidationService) CheckDepositRateLimit(ctx context.Context, userID uuid.UUID) error {
	if v.cache == nil {
		return nil // No cache configured
	}

	key := fmt.Sprintf("deposit_rate:%s", userID.String())
	
	count, err := v.cache.Incr(ctx, key)
	if err != nil {
		// On error, allow the request but log
		return nil
	}

	// Set expiry on first increment
	if count == 1 {
		_ = v.cache.Expire(ctx, key, v.config.RateLimitWindow)
	}

	if int(count) > v.config.MaxDepositsPerDay {
		return fmt.Errorf("rate limit exceeded: maximum %d deposit addresses per day", v.config.MaxDepositsPerDay)
	}

	return nil
}

// ValidateDepositStatusTransition validates deposit status transition
func (v *ValidationService) ValidateDepositStatusTransition(currentStatus, newStatus string) error {
	current := entities.DepositStatus(currentStatus)
	new := entities.DepositStatus(newStatus)
	return current.ValidateTransition(new)
}

// BalanceCacheKey returns the cache key for user balance
func BalanceCacheKey(userID uuid.UUID) string {
	return fmt.Sprintf("balance:%s", userID.String())
}
