package di

import (
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/services"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
)

// InitializeBasketExecutor creates a new basket executor
func (c *Container) InitializeBasketExecutor() *services.BasketExecutor {
	return services.NewBasketExecutor(c.AlpacaService, c.ZapLog)
}

// InitializeBrokerageOnboarding creates a new brokerage onboarding service
func (c *Container) InitializeBrokerageOnboarding() *services.BrokerageOnboardingService {
	return services.NewBrokerageOnboardingService(c.AlpacaService, c.ZapLog)
}

// InitializeInstantFunding creates a new instant funding service
func (c *Container) InitializeInstantFunding(firmAccountNumber string) *services.InstantFundingService {
	sqlxDB := sqlx.NewDb(c.DB, "postgres")
	virtualAccountRepo := repositories.NewVirtualAccountRepository(sqlxDB)
	
	return services.NewInstantFundingService(
		c.AlpacaService,
		virtualAccountRepo,
		c.ZapLog,
		firmAccountNumber,
	)
}


