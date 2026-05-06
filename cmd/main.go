package main

import (
	"fmt"
	"os"

	"github.com/rail-service/rail_service/internal/app"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/internal/infrastructure/database"
	"github.com/rail-service/rail_service/pkg/alerting"
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
	return database.RunMigrations(dbURL)
}

func runHealthCheck() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	db, err := database.NewConnection(cfg.Database)
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
