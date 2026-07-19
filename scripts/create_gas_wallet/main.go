// Command create_gas_wallet provisions ONE dedicated Circle developer-controlled
// Solana (EOA) wallet to be used as a SOL gas/fee-payer pool.
//
// Why this exists: Circle paused Gas Station sponsorship, so Solana USDC sends and
// USDC ATA (associated token account) creation are no longer gas-sponsored. A wallet
// funded with a little SOL can pay its own network fee (and ATA rent) without the
// Gas Station. This script mints that wallet. Creating a Circle wallet is FREE — no
// on-chain transaction — so this costs nothing. You fund it with SOL afterwards.
//
// It does NOT move any money. It only calls Circle's wallet-create API and prints the
// resulting walletID + Solana address so you can:
//   1. put them in config (Circle gas wallet id / address), and
//   2. send SOL to the printed address to fund gas.
//
// Run (from repo root), reusing your existing Circle credentials:
//
//	CIRCLE_API_KEY=...            \
//	CIRCLE_ENTITY_SECRET=...      \  # 64-char hex (same one the service uses)
//	CIRCLE_PUBLIC_KEY_PEM="$EXPO_PUBLIC_CIRCLE_PUBLIC_KEY_PEM" \
//	CIRCLE_DEFAULT_WALLET_SET_ID=... \  # optional; defaults to the seeded prod wallet set
//	CIRCLE_ENVIRONMENT=production \
//	go run ./scripts/create_gas_wallet
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	circle "github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
	"go.uber.org/zap"
)

// defaultWalletSetID is the production wallet set seeded in migration 194. Used only
// when CIRCLE_DEFAULT_WALLET_SET_ID is not provided.
const defaultWalletSetID = "d68f52f2-cbff-50ea-92cb-bf9c5dab80d3"

func main() {
	logger, _ := zap.NewProduction()
	defer func() { _ = logger.Sync() }()

	apiKey := strings.TrimSpace(os.Getenv("CIRCLE_API_KEY"))
	entitySecret := strings.TrimSpace(os.Getenv("CIRCLE_ENTITY_SECRET"))
	publicKeyPEM := strings.TrimSpace(os.Getenv("CIRCLE_PUBLIC_KEY_PEM"))
	if publicKeyPEM == "" {
		publicKeyPEM = strings.TrimSpace(os.Getenv("EXPO_PUBLIC_CIRCLE_PUBLIC_KEY_PEM"))
	}
	walletSetID := strings.TrimSpace(os.Getenv("CIRCLE_DEFAULT_WALLET_SET_ID"))
	if walletSetID == "" {
		walletSetID = defaultWalletSetID
	}
	environment := strings.TrimSpace(os.Getenv("CIRCLE_ENVIRONMENT"))
	if environment == "" {
		environment = "production"
	}

	if apiKey == "" || entitySecret == "" || publicKeyPEM == "" {
		fmt.Println("Missing required env. Need:")
		fmt.Println("  CIRCLE_API_KEY")
		fmt.Println("  CIRCLE_ENTITY_SECRET            (64-char hex)")
		fmt.Println("  CIRCLE_PUBLIC_KEY_PEM           (or EXPO_PUBLIC_CIRCLE_PUBLIC_KEY_PEM)")
		os.Exit(1)
	}

	client, err := circle.NewHTTPClient(circle.Config{
		APIKey:             apiKey,
		Environment:        environment,
		EntitySecret:       entitySecret,
		PublicKeyPEM:       publicKeyPEM,
		DefaultWalletSetID: walletSetID,
	}, logger)
	if err != nil {
		fmt.Printf("failed to build Circle client: %v\n", err)
		os.Exit(1)
	}

	adapter := circle.NewAdapter(client, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	fmt.Printf("Creating dedicated SOL gas wallet in wallet set %s (%s)...\n", walletSetID, environment)

	// Sanity check: verify creds + wallet set are reachable before creating.
	wallets, err := adapter.ListCircleWallets(ctx, walletSetID)
	if err != nil {
		fmt.Printf("WARNING: could not list existing wallets (continuing anyway): %v\n", err)
	} else {
		fmt.Printf("Wallet set reachable; it currently has %d wallet(s).\n", len(wallets))
	}

	// Solana wallets are EOA in this codebase (SCA is unsupported on SOL). EOA is
	// exactly what we want: the wallet can pay its own SOL gas.
	created, err := client.CreateWalletsWithType(ctx, walletSetID, []circle.Blockchain{circle.BlockchainSOL}, 1, "EOA", []circle.WalletMetadata{
		{Name: "RAIL-GAS-POOL-SOL", RefID: "rail-gas-pool"},
	})
	if err != nil {
		fmt.Printf("create gas wallet failed: %v\n", err)
		os.Exit(1)
	}
	if len(created) == 0 {
		fmt.Println("Circle returned no wallets")
		os.Exit(1)
	}

	w := created[0]
	fmt.Println()
	fmt.Println("================ GAS WALLET CREATED ================")
	fmt.Printf("  walletID : %s\n", w.ID)
	fmt.Printf("  address  : %s\n", w.Address)
	fmt.Printf("  chain    : %s\n", w.Blockchain)
	fmt.Printf("  state    : %s\n", w.State)
	fmt.Println("====================================================")
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  1. Send SOL to the address above to fund gas (even ~$1 of SOL works to start).")
	fmt.Println("  2. Save walletID + address into config as the Circle gas wallet.")
	fmt.Println("  3. Whitelist this address in the Circle inbound webhook so the SOL is NOT auto-returned.")
}
