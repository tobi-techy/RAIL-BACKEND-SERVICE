package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	chainrails "github.com/rail-service/rail_service/internal/infrastructure/adapters/chainrails"
	circleadapter "github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/internal/recovery"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// runRecoverFunds sweeps USDC stranded in a Base Circle wallet back to Solana via
// ChainRails. It loads config exactly like the app, so when run inside the deployment
// it inherits the same Circle + ChainRails credentials (no secret copying).
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
	logger, _ := zap.NewProduction()

	cc, err := circleadapter.NewHTTPClient(circleadapter.Config{
		APIKey:       cfg.Circle.APIKey,
		BaseURL:      cfg.Circle.BaseURL,
		Environment:  cfg.Circle.Environment,
		EntitySecret: cfg.Circle.EntitySecret,
		PublicKeyPEM: cfg.Circle.PublicKeyPEM,
	}, logger)
	if err != nil {
		return fmt.Errorf("circle client: %w", err)
	}
	cr := chainrails.NewClient(chainrails.Config{
		APIKey:           cfg.ChainRails.APIKey,
		WebhookSecret:    cfg.ChainRails.WebhookSecret,
		BaseURL:          cfg.ChainRails.BaseURL,
		DestinationChain: cfg.ChainRails.DestinationChain,
		SettlementToken:  cfg.ChainRails.SettlementToken,
	}, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	return recovery.RecoverStuckBaseToSolana(ctx, cc, cr, recovery.Params{
		WalletID:     *walletID,
		ToSolanaAddr: *toAddr,
		DestAmount:   amount,
		Confirm:      *confirm,
	}, logger, os.Stdout)
}
