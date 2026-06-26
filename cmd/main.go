package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	"github.com/rail-service/rail_service/internal/app"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/bridge"
	circleadapter "github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/internal/infrastructure/database"
	wallet_migration "github.com/rail-service/rail_service/internal/workers/wallet_migration"
	"github.com/rail-service/rail_service/pkg/alerting"
	"go.uber.org/zap"
)

// @title Rail Service API
// @version 1.0
// @description Rules-based capital engine — money splits itself the moment it arrives.
// @termsOfService https://www.getrail.app/terms

// @contact.name API Support
// @contact.url https://www.getrail.app/support
// @contact.email support@getrail.app

// @license.name MIT
// @license.url https://opensource.org/licenses/MIT

// @host localhost:8080
// @BasePath /api/v1

// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Type "Bearer" followed by a space and JWT token.

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "migrate":
			if err := runMigrations(); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to run migrations: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("Migrations complete")
			return
		case "--health-check":
			if err := runHealthCheck(); err != nil {
				fmt.Fprintf(os.Stderr, "Health check failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "recover-funds":
			if err := runRecoverFunds(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "recover-funds failed: %v\n", err)
				os.Exit(1)
			}
			return
		}
	}

	application := app.NewApplication()

	if err := application.Initialize(); err != nil {
		sendFatalAlert("Failed to initialize application", err)
		fmt.Fprintf(os.Stderr, "Failed to initialize application: %v\n", err)
		os.Exit(1)
	}

	if err := application.Start(); err != nil {
		sendFatalAlert("Failed to start application", err)
		fmt.Fprintf(os.Stderr, "Failed to start application: %v\n", err)
		os.Exit(1)
	}

	application.WaitForShutdown()

	if err := application.Shutdown(); err != nil {
		fmt.Fprintf(os.Stderr, "Error during shutdown: %v\n", err)
		os.Exit(1)
	}
}

func runMigrations() error {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		dbURL = cfg.Database.URL
	}
	if err := database.RunMigrations(dbURL); err != nil {
		return err
	}

	// One-shot: migrate legacy Bridge wallets to Circle.
	// No-ops once all wallets have circle_wallet_id set.
	if err := runWalletMigration(dbURL); err != nil {
		fmt.Fprintf(os.Stderr, "Wallet migration warning (non-fatal): %v\n", err)
	}
	return nil
}

func runWalletMigration(dbURL string) error {
	bridgeAPIKey := os.Getenv("BRIDGE_API_KEY")
	circleAPIKey := os.Getenv("CIRCLE_API_KEY")
	circleEntitySecret := os.Getenv("CIRCLE_ENTITY_SECRET")
	circleWalletSetID := os.Getenv("CIRCLE_DEFAULT_WALLET_SET_ID")

	if bridgeAPIKey == "" || circleAPIKey == "" || circleEntitySecret == "" || circleWalletSetID == "" {
		return nil // not configured, skip silently
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	logger, err := zap.NewProduction(zap.AddStacktrace(zap.ErrorLevel))
	if err != nil {
		return fmt.Errorf("create logger: %w", err)
	}
	defer logger.Sync()

	bridgeClient := bridge.NewClient(bridge.Config{APIKey: bridgeAPIKey, BaseURL: "https://api.bridge.xyz"}, logger)
	circleClient, err := circleadapter.NewHTTPClient(circleadapter.Config{
		APIKey:       circleAPIKey,
		EntitySecret: circleEntitySecret,
		Environment:  os.Getenv("CIRCLE_ENVIRONMENT"),
	}, logger)
	if err != nil {
		return fmt.Errorf("circle client: %w", err)
	}
	circleAdapter := circleadapter.NewAdapter(circleClient, logger)

	worker := wallet_migration.NewWorker(db, bridgeClient, circleAdapter, circleWalletSetID, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	result, err := worker.Run(ctx, 500)
	if err != nil {
		return err
	}
	if result.Total > 0 {
		fmt.Printf("Wallet migration: %d/%d migrated, %d failed, %s USDC transferred\n",
			result.Migrated, result.Total, result.Failed, result.Transferred.String())
	}
	return nil
}

func runHealthCheck() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	db, err := database.NewConnection(cfg.Database, cfg.Environment)
	if err != nil {
		return err
	}
	defer db.Close()
	return database.HealthCheck(db)
}

func sendFatalAlert(msg string, err error) {
	t := alerting.NewTelegramAlerter(
		os.Getenv("TELEGRAM_ALERTS_BOT_TOKEN"),
		os.Getenv("TELEGRAM_ALERTS_CHAT_ID"),
	)
	if t != nil {
		t.SendFatal(msg, err)
	}
}
