// recover_stuck_funds sweeps USDC stranded in a Base Circle wallet (it bridged in but the
// Blend deposit never settled) back to a Solana address via ChainRails — the same bridge
// that brought it over, run in reverse. This is the LOCAL variant that takes credentials
// from the environment; when the keys live in the deployment, prefer the built-in
// subcommand instead: `rail_service recover-funds ...` (see cmd/recover.go), which loads
// them from the app config automatically.
//
// SAFE BY DEFAULT: dry-run unless you pass -confirm.
//
//	CIRCLE_API_KEY=... CIRCLE_ENTITY_SECRET=... CHAINRAILS_API_KEY=... \
//	  go run ./scripts/recover_stuck_funds \
//	    -wallet a78c5caa-e031-5b46-8951-7539974b67ae -to <SOLANA_ADDR> -amount 4.0 [-confirm]
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"strings"
	"time"

	chainrails "github.com/rail-service/rail_service/internal/infrastructure/adapters/chainrails"
	circle "github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
	"github.com/rail-service/rail_service/internal/recovery"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

func main() {
	walletID := flag.String("wallet", "", "Circle Base wallet ID holding the stranded USDC")
	toAddr := flag.String("to", "", "destination Solana address (recipient)")
	amountS := flag.String("amount", "", "USDC amount to receive on Solana (e.g. 4.0)")
	confirm := flag.Bool("confirm", false, "actually move funds (default: dry-run)")
	flag.Parse()

	if *walletID == "" || *toAddr == "" || *amountS == "" {
		flag.Usage()
		log.Fatal("-wallet, -to and -amount are required")
	}
	amount, err := decimal.NewFromString(*amountS)
	if err != nil {
		log.Fatalf("invalid -amount %q", *amountS)
	}

	apiKey := must("CIRCLE_API_KEY")
	entitySecret := must("CIRCLE_ENTITY_SECRET")
	crKey := must("CHAINRAILS_API_KEY")
	logger := zap.NewNop()

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	// Fetch Circle's RSA public key if not provided, so the entity secret can be encrypted.
	boot, err := circle.NewHTTPClient(circle.Config{APIKey: apiKey, Environment: env()}, logger)
	if err != nil {
		log.Fatalf("circle bootstrap client: %v", err)
	}
	pubPEM := strings.TrimSpace(os.Getenv("CIRCLE_PUBLIC_KEY_PEM"))
	if pubPEM == "" {
		if pubPEM, err = boot.GetEntityPublicKey(ctx); err != nil {
			log.Fatalf("fetch Circle entity public key: %v", err)
		}
	}

	cc, err := circle.NewHTTPClient(circle.Config{
		APIKey: apiKey, EntitySecret: entitySecret, PublicKeyPEM: pubPEM, Environment: env(),
	}, logger)
	if err != nil {
		log.Fatalf("circle client: %v", err)
	}
	cr := chainrails.NewClient(chainrails.Config{
		APIKey:  crKey,
		BaseURL: strings.TrimSpace(os.Getenv("CHAINRAILS_BASE_URL")),
	}, logger)

	if err := recovery.RecoverStuckBaseToSolana(ctx, cc, cr, recovery.Params{
		WalletID:     *walletID,
		ToSolanaAddr: *toAddr,
		DestAmount:   amount,
		Confirm:      *confirm,
	}, logger, os.Stdout); err != nil {
		log.Fatal(err)
	}
}

func must(k string) string {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		log.Fatalf("%s is required", k)
	}
	return v
}

func env() string {
	if v := strings.TrimSpace(os.Getenv("CIRCLE_ENVIRONMENT")); v != "" {
		return v
	}
	return "production"
}
