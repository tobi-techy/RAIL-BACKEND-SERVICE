package di

import (
	"time"

	"github.com/rail-service/rail_service/internal/api/handlers/webhooks"
	monoadapter "github.com/rail-service/rail_service/internal/infrastructure/adapters/mono"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	monosvc "github.com/rail-service/rail_service/internal/domain/services/mono"
	"github.com/jmoiron/sqlx"
	"go.uber.org/zap"
)

// initializeMono sets up the Mono adapter, repository, and domain service.
// All Mono features are gated on cfg.Mono.APIKey being non-empty.
func (c *Container) initializeMono() error {
	cfg := c.Config
	if cfg.Mono.APIKey == "" {
		c.ZapLog.Info("Mono not configured (missing API key), open-banking features disabled")
		return nil
	}

	client := monoadapter.NewHTTPClient(monoadapter.Config{
		APIKey:      cfg.Mono.APIKey,
		Environment: cfg.Mono.Environment,
		BaseURL:     cfg.Mono.BaseURL,
		Timeout:     time.Duration(cfg.Mono.Timeout) * time.Second,
		MaxRetries:  cfg.Mono.MaxRetries,
	}, c.ZapLog)

	c.ZapLog.Info("Mono open-banking adapter initialized",
		zap.String("environment", cfg.Mono.Environment),
		zap.String("base_url", cfg.Mono.BaseURL))

	c.MonoRepo = repositories.NewMonoRepository(sqlx.NewDb(c.DB, "postgres"))
	c.MonoService = monosvc.NewService(client, c.MonoRepo, c.ZapLog)
	c.MonoWebhookHandler = webhooks.NewMonoWebhookHandler(c.MonoService, cfg.Mono.WebhookSecret, c.ZapLog)

	return nil
}

// GetMonoService returns the Mono service, or nil if not configured.
func (c *Container) GetMonoService() *monosvc.Service {
	return c.MonoService
}
