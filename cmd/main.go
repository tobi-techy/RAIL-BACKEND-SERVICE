package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rail-service/rail_service/internal/app"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/bridge"
	chainrails "github.com/rail-service/rail_service/internal/infrastructure/adapters/chainrails"
	circleadapter "github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/internal/infrastructure/database"
	"github.com/rail-service/rail_service/internal/recovery"
	wallet_migration "github.com/rail-service/rail_service/internal/workers/wallet_migration"
	"github.com/rail-service/rail_service/pkg/alerting"
	"github.com/shopspring/decimal"
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

	// One-off stranded-funds recovery, activated by env vars, for deployments without a
	// shell. No-op unless BLEND_RECOVER_* are set. Runs in the background so it never blocks
	// startup/health checks.
	go maybeRunBootRecovery()

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

// runRecoverFunds sweeps USDC stranded in a Base Circle wallet back to Solana via
// ChainRails. It loads config exactly like the app, so when run inside the deployment it
// inherits the same Circle + ChainRails credentials (no secret copying). Kept in this file
// (not a separate cmd/ file) so the single-file `go build cmd/main.go` form used by the
// Makefile and Dockerfiles keeps working.
//
//	rail_service recover-funds -wallet <id> -to <solana-addr> -amount 4.0          # dry run
//	rail_service recover-funds -wallet <id> -to <solana-addr> -amount 4.0 -confirm  # execute
func runRecoverFunds(args []string) error {
	fs := flag.NewFlagSet("recover-funds", flag.ContinueOnError)
	walletID := fs.String("wallet", "", "Circle Base wallet ID holding the stranded USDC")
	toAddr := fs.String("to", "", "destination Solana address (recipient)")
	amountS := fs.String("amount", "", "USDC amount to receive on Solana (e.g. 4.0); ChainRails fees are added on top")
	confirm := fs.Bool("confirm", false, "actually move funds (default: dry-run)")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil // -h/--help is a clean exit, not a failure
		}
		return err
	}
	if *walletID == "" || *toAddr == "" || *amountS == "" {
		fs.Usage()
		return fmt.Errorf("-wallet, -to and -amount are required")
	}
	amount, err := decimal.NewFromString(*amountS)
	if err != nil {
		return fmt.Errorf("invalid -amount %q", *amountS)
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Fail fast with a clear message if required credentials are missing, rather than
	// surfacing an opaque error mid-recovery. The Circle API key + ChainRails key are needed
	// even for a dry-run; the entity secret + public key are only needed to move funds.
	if strings.TrimSpace(cfg.Circle.APIKey) == "" {
		return fmt.Errorf("recover-funds: CIRCLE_API_KEY is not configured")
	}
	if strings.TrimSpace(cfg.ChainRails.APIKey) == "" {
		return fmt.Errorf("recover-funds: CHAINRAILS_API_KEY is not configured")
	}
	if *confirm {
		if strings.TrimSpace(cfg.Circle.EntitySecret) == "" {
			return fmt.Errorf("recover-funds: CIRCLE_ENTITY_SECRET is required to move funds (-confirm)")
		}
		if strings.TrimSpace(cfg.Circle.PublicKeyPEM) == "" {
			return fmt.Errorf("recover-funds: CIRCLE_PUBLIC_KEY_PEM is required to move funds (-confirm)")
		}
	}

	logger, err := zap.NewProduction()
	if err != nil {
		return fmt.Errorf("create logger: %w", err)
	}
	cc, cr, err := buildRecoveryClients(cfg, logger)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	return recovery.RecoverStuckBaseToSolana(ctx, cc, cr, recovery.Params{
		WalletID:     *walletID,
		ToSolanaAddr: *toAddr,
		DestAmount:   amount,
		Confirm:      *confirm,
	}, logger, os.Stdout)
}

// buildRecoveryClients constructs the Circle + ChainRails clients used by the recovery flow
// from the loaded app config (so they inherit the deployment's credentials).
func buildRecoveryClients(cfg *config.Config, logger *zap.Logger) (*circleadapter.HTTPClient, *chainrails.Client, error) {
	if strings.TrimSpace(cfg.Circle.APIKey) == "" {
		return nil, nil, fmt.Errorf("CIRCLE_API_KEY is not configured")
	}
	if strings.TrimSpace(cfg.ChainRails.APIKey) == "" {
		return nil, nil, fmt.Errorf("CHAINRAILS_API_KEY is not configured")
	}
	cc, err := circleadapter.NewHTTPClient(circleadapter.Config{
		APIKey:       cfg.Circle.APIKey,
		BaseURL:      cfg.Circle.BaseURL,
		Environment:  cfg.Circle.Environment,
		EntitySecret: cfg.Circle.EntitySecret,
		PublicKeyPEM: cfg.Circle.PublicKeyPEM,
	}, logger)
	if err != nil {
		return nil, nil, fmt.Errorf("circle client: %w", err)
	}
	cr := chainrails.NewClient(chainrails.Config{
		APIKey:           cfg.ChainRails.APIKey,
		WebhookSecret:    cfg.ChainRails.WebhookSecret,
		BaseURL:          cfg.ChainRails.BaseURL,
		DestinationChain: cfg.ChainRails.DestinationChain,
		SettlementToken:  cfg.ChainRails.SettlementToken,
	}, logger)
	return cc, cr, nil
}

// maybeRunBootRecovery runs the stuck-funds recovery on startup when activated by env vars,
// for deployments where there is no shell to run `recover-funds` manually. It is a no-op
// unless BLEND_RECOVER_WALLET, BLEND_RECOVER_TO and BLEND_RECOVER_AMOUNT are all set. It is
// DRY-RUN (prints the quote, moves nothing) unless BLEND_RECOVER_CONFIRM=true. Output goes to
// stdout so it shows up in the deployment's logs. Safe to leave configured across restarts:
// the Circle funding transfer uses a stable idempotency key, so a repeat boot can't double-send.
//
//	BLEND_RECOVER_WALLET   = Circle Base wallet id holding the stranded USDC
//	BLEND_RECOVER_TO       = destination Solana address
//	BLEND_RECOVER_AMOUNT   = USDC to receive on Solana (e.g. 4.0)
//	BLEND_RECOVER_CONFIRM  = "true" to actually move funds (else dry-run)
func maybeRunBootRecovery() {
	wallet := strings.TrimSpace(os.Getenv("BLEND_RECOVER_WALLET"))
	to := strings.TrimSpace(os.Getenv("BLEND_RECOVER_TO"))
	amountS := strings.TrimSpace(os.Getenv("BLEND_RECOVER_AMOUNT"))
	if wallet == "" || to == "" || amountS == "" {
		return // not activated
	}
	confirm := strings.EqualFold(strings.TrimSpace(os.Getenv("BLEND_RECOVER_CONFIRM")), "true")

	fmt.Printf("\n========== BLEND BOOT RECOVERY ACTIVATED (confirm=%v) ==========\n", confirm)
	amount, err := decimal.NewFromString(amountS)
	if err != nil || amount.LessThanOrEqual(decimal.Zero) {
		fmt.Printf("BLEND BOOT RECOVERY: invalid BLEND_RECOVER_AMOUNT %q\n", amountS)
		return
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Printf("BLEND BOOT RECOVERY: load config failed: %v\n", err)
		return
	}
	logger, err := zap.NewProduction()
	if err != nil {
		fmt.Printf("BLEND BOOT RECOVERY: failed to create logger: %v\n", err)
		return
	}
	cc, cr, err := buildRecoveryClients(cfg, logger)
	if err != nil {
		fmt.Printf("BLEND BOOT RECOVERY: %v\n", err)
		return
	}
	if confirm {
		if strings.TrimSpace(cfg.Circle.EntitySecret) == "" || strings.TrimSpace(cfg.Circle.PublicKeyPEM) == "" {
			fmt.Println("BLEND BOOT RECOVERY: CIRCLE_ENTITY_SECRET and CIRCLE_PUBLIC_KEY_PEM are required to move funds (confirm=true)")
			return
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	if err := recovery.RecoverStuckBaseToSolana(ctx, cc, cr, recovery.Params{
		WalletID:     wallet,
		ToSolanaAddr: to,
		DestAmount:   amount,
		Confirm:      confirm,
	}, logger, os.Stdout); err != nil {
		fmt.Printf("========== BLEND BOOT RECOVERY FAILED: %v ==========\n", err)
		return
	}
	fmt.Println("========== BLEND BOOT RECOVERY COMPLETE ==========")
}
