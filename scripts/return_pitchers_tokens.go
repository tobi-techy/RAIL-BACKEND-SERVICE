//go:build ignore

// Return PITCHERS tokens to sender wallet via Circle Programmable Wallets API.
//
// Usage: go run scripts/return_pitchers_tokens.go
//
// Required env vars (from SSM/config):
//   CIRCLE_API_KEY, CIRCLE_ENTITY_SECRET, CIRCLE_PUBLIC_KEY_PEM
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
	"go.uber.org/zap"
)

const (
	walletID           = "42e98a94-3d05-5add-a59c-1438b2c3a1de"
	tokenID            = "" // Will use tokenAddress approach instead
	tokenAddress       = "5uC8peY52XYMRcZeCU2KBgPeDJcqQYsLvnvn1qkT1PoS"
	destinationAddress = "55n2vjqbVTrvb5nT9T86zZY83nH3aEVeCpqM9mi9WqWq"
	amount             = "90.7284276364"
	blockchain         = "SOL"
	walletAddress      = "Huq7SF5qBgEYjKA7db53pWhcT6q7NrsNmUzDq89gB32X"
	idempotencyKey     = "pitchers-return-42e98a94-2026-05-18"
)

func main() {
	apiKey := os.Getenv("CIRCLE_API_KEY")
	entitySecret := os.Getenv("CIRCLE_ENTITY_SECRET")
	publicKeyPEM := os.Getenv("CIRCLE_PUBLIC_KEY_PEM")

	if apiKey == "" || entitySecret == "" || publicKeyPEM == "" {
		log.Fatal("Set CIRCLE_API_KEY, CIRCLE_ENTITY_SECRET, CIRCLE_PUBLIC_KEY_PEM")
	}

	logger, _ := zap.NewProduction()

	client, err := circle.NewHTTPClient(circle.Config{
		APIKey:       apiKey,
		EntitySecret: entitySecret,
		PublicKeyPEM: publicKeyPEM,
		Environment:  "production",
		Timeout:      30 * time.Second,
	}, logger)
	if err != nil {
		log.Fatalf("Failed to create Circle client: %v", err)
	}

	adapter := circle.NewAdapter(client, logger)

	// First try to get the token ID from wallet balances
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	symbol, err := adapter.GetTokenSymbol(ctx, walletID, tokenAddress)
	if err != nil {
		log.Printf("Warning: could not resolve token symbol (may need tokenId): %v", err)
		log.Printf("Attempting transfer using tokenAddress directly...")
	} else {
		log.Printf("Token resolved: %s", symbol)
	}

	// Use ReturnUnsupportedToken which handles entity secret encryption
	err = adapter.ReturnUnsupportedToken(ctx, walletID, tokenAddress, destinationAddress, []string{amount}, idempotencyKey)
	if err != nil {
		log.Fatalf("Failed to return tokens: %v", err)
	}

	fmt.Println("✓ PITCHERS token return submitted successfully")
	fmt.Printf("  From wallet: %s\n", walletID)
	fmt.Printf("  To address:  %s\n", destinationAddress)
	fmt.Printf("  Amount:      %s PITCHERS\n", amount)
}
