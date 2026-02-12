package di

import (
	"github.com/rail-service/rail_service/internal/domain/services"
	"github.com/rail-service/rail_service/internal/domain/services/funding"
)

// InitializeBasketExecutor creates a new basket executor
func (c *Container) InitializeBasketExecutor() *services.BasketExecutor {
	return services.NewBasketExecutor(c.AlpacaService, c.ZapLog)
}

// InitializeBrokerageOnboarding creates a new brokerage onboarding service
func (c *Container) InitializeBrokerageOnboarding() *services.BrokerageOnboardingService {
	return services.NewBrokerageOnboardingService(c.AlpacaService, c.ZapLog)
}

// InitializeInstantFunding returns the instant funding service
// Deprecated: Use GetInstantFundingService() instead
func (c *Container) InitializeInstantFunding(firmAccountNumber string) *funding.InstantFundingService {
	return c.InstantFundingService
}

