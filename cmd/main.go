package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/app"
	blendadapter "github.com/rail-service/rail_service/internal/infrastructure/adapters/blend"
	chainrails "github.com/rail-service/rail_service/internal/infrastructure/adapters/chainrails"
	circleadapter "github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/internal/infrastructure/database"
	"github.com/rail-service/rail_service/internal/recovery"
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
		case "diagnose-yield":
			if err := runDiagnoseYield(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "diagnose-yield failed: %v\n", err)
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

	// One-off read-only yield diagnostic, activated by BLEND_DIAGNOSE_USER, for deployments
	// without a shell. Output lands in the runtime logs.
	go maybeRunBootDiagnose()

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

// runDiagnoseYield prints a comprehensive diagnostic of where a user's yield (stash)
// funds are across Blend, Circle wallets, DB state, and in-flight withdrawals/redemptions.
// It is read-only — no funds move, no state changes.
//
//	rail_service diagnose-yield -user <uuid>
func runDiagnoseYield(args []string) error {
	fs := flag.NewFlagSet("diagnose-yield", flag.ContinueOnError)
	userID := fs.String("user", "", "user UUID to diagnose")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if *userID == "" {
		fs.Usage()
		return fmt.Errorf("-user is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return diagnoseYield(ctx, *userID, os.Stdout)
}

// diagnoseYield loads config, opens the DB, builds the Circle + Blend clients and runs the
// read-only diagnostic. Shared by the `diagnose-yield` subcommand and the boot-time trigger
// so both behave identically. Missing Circle/Blend credentials degrade to partial output
// rather than failing — the DB half of the picture is still worth printing.
func diagnoseYield(ctx context.Context, userID string, out io.Writer) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger, err := zap.NewProduction()
	if err != nil {
		return fmt.Errorf("create logger: %w", err)
	}

	// Connect to the database
	db, err := database.NewConnection(cfg.Database)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer db.Close()

	dbx := sqlx.NewDb(db, "postgres")

	// Build Circle client (for on-chain balance reads)
	var circleClient *circleadapter.HTTPClient
	if strings.TrimSpace(cfg.Circle.APIKey) != "" {
		circleClient, err = circleadapter.NewHTTPClient(circleadapter.Config{
			APIKey:       cfg.Circle.APIKey,
			BaseURL:      cfg.Circle.BaseURL,
			Environment:  cfg.Circle.Environment,
			EntitySecret: cfg.Circle.EntitySecret,
			PublicKeyPEM: cfg.Circle.PublicKeyPEM,
		}, logger)
		if err != nil {
			logger.Warn("diagnose-yield: Circle client init failed, will skip on-chain balances", zap.Error(err))
			circleClient = nil
		}
	} else {
		logger.Warn("diagnose-yield: CIRCLE_API_KEY not set, will skip on-chain balances")
	}

	// Build Blend client (for balance/returns/positions)
	var blendClient *blendadapter.Client
	if cfg.Blend.Enabled && strings.TrimSpace(cfg.Blend.APIKey) != "" {
		blendClient, err = blendadapter.NewClient(blendadapter.Config{
			BaseURL:       cfg.Blend.BaseURL,
			APIKey:        cfg.Blend.APIKey,
			AccountTypeID: cfg.Blend.AccountTypeID,
		}, logger)
		if err != nil {
			logger.Warn("diagnose-yield: Blend client init failed, will skip Blend API calls", zap.Error(err))
			blendClient = nil
		}
	} else {
		logger.Warn("diagnose-yield: Blend not enabled or API key not set, will skip Blend API calls")
	}

	return recovery.DiagnoseYield(ctx, dbx, blendClient, circleClient, recovery.DiagnoseParams{
		UserID: userID,
	}, out)
}

// maybeRunBootDiagnose runs the read-only yield diagnostic on startup when activated by an
// env var, for deployments (AtlasFlow) where there is no shell to run `diagnose-yield`
// manually. No-op unless BLEND_DIAGNOSE_USER is set. Output goes to stdout so it lands in the
// deployment's Runtime Logs. Read-only: it moves no funds and changes no state, so it is safe
// to leave configured across restarts (it simply reprints on each boot).
//
//	BLEND_DIAGNOSE_USER = user UUID to diagnose
func maybeRunBootDiagnose() {
	userID := strings.TrimSpace(os.Getenv("BLEND_DIAGNOSE_USER"))
	if userID == "" {
		return // not activated
	}

	fmt.Printf("\n========== BLEND BOOT DIAGNOSTIC (user=%s) ==========\n", userID)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := diagnoseYield(ctx, userID, os.Stdout); err != nil {
		fmt.Printf("========== BLEND BOOT DIAGNOSTIC FAILED: %v ==========\n", err)
		return
	}
	fmt.Println("========== BLEND BOOT DIAGNOSTIC COMPLETE ==========")
}
