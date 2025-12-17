package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/rail-service/rail_service/internal/adapters/bridge"
	"github.com/rail-service/rail_service/pkg/logger"
	"go.uber.org/zap"
)

func main() {
	// Get Bridge API key from environment
	apiKey := os.Getenv("BRIDGE_API_KEY")
	if apiKey == "" {
		log.Fatal("BRIDGE_API_KEY environment variable is required")
	}

	baseURL := os.Getenv("BRIDGE_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.bridge.xyz"
	}

	// Initialize logger
	zapLogger, _ := zap.NewDevelopment()
	svcLogger := logger.NewLogger(zapLogger, "test")

	// Create Bridge config
	config := bridge.Config{
		APIKey:      apiKey,
		BaseURL:     baseURL,
		Environment:  "sandbox",
		Timeout:      30 * time.Second,
		MaxRetries:   3,
	}

	// Initialize Bridge client
	client := bridge.NewClient(config, svcLogger)
	adapter := bridge.NewAdapter(client, svcLogger)

	ctx := context.Background()

	// Test health check
	fmt.Println("Testing Bridge API health check...")
	err := adapter.HealthCheck(ctx)
	if err != nil {
		log.Printf("Health check failed: %v", err)
		return
	}
	fmt.Println("✓ Bridge API health check passed")

	// Test customer creation
	fmt.Println("\nTesting customer creation...")
	createCustomerReq := &bridge.CreateCustomerWithWalletRequest{
		FirstName: "Test",
		LastName:  "User",
		Email:     "test@example.com",
		Phone:     "+1234567890",
		Chain:     bridge.PaymentRailEthereum,
	}

	customerResponse, err := adapter.CreateCustomerWithWallet(ctx, createCustomerReq)
	if err != nil {
		log.Printf("Customer creation failed: %v", err)
		return
	}

	fmt.Printf("✓ Customer created successfully:\n")
	fmt.Printf("  Customer ID: %s\n", customerResponse.Customer.ID)
	fmt.Printf("  Email: %s\n", customerResponse.Customer.Email)
	fmt.Printf("  Status: %s\n", customerResponse.Customer.Status)
	fmt.Printf("  Wallet ID: %s\n", customerResponse.Wallet.ID)
	fmt.Printf("  Wallet Address: %s\n", customerResponse.Wallet.Address)
	fmt.Printf("  Chain: %s\n", customerResponse.Wallet.Chain)

	// Test wallet balance
	fmt.Println("\nTesting wallet balance...")
	balance, err := adapter.GetWalletBalance(ctx, customerResponse.Customer.ID, customerResponse.Wallet.ID)
	if err != nil {
		log.Printf("Get wallet balance failed: %v", err)
	} else {
		fmt.Printf("✓ Wallet balance retrieved:\n")
		fmt.Printf("  Amount: %s\n", balance.Amount)
		fmt.Printf("  Currency: %s\n", balance.Currency)
	}

	// Test KYC link
	fmt.Println("\nTesting KYC link generation...")
	kycLink, err := adapter.GetKYCLinkForCustomer(ctx, customerResponse.Customer.ID)
	if err != nil {
		log.Printf("KYC link generation failed: %v", err)
	} else {
		fmt.Printf("✓ KYC link generated:\n")
		fmt.Printf("  URL: %s\n", kycLink.KYCLink)
	}

	// Test customer status
	fmt.Println("\nTesting customer status...")
	status, err := adapter.GetCustomerStatus(ctx, customerResponse.Customer.ID)
	if err != nil {
		log.Printf("Get customer status failed: %v", err)
	} else {
		fmt.Printf("✓ Customer status retrieved:\n")
		fmt.Printf("  Customer ID: %s\n", status.CustomerID)
		fmt.Printf("  Bridge Status: %s\n", status.BridgeStatus)
		fmt.Printf("  KYC Status: %s\n", status.KYCStatus)
		fmt.Printf("  Onboarding Status: %s\n", status.OnboardingStatus)
	}

	fmt.Println("\n🎉 Bridge API integration test completed successfully!")
	fmt.Println("\nBridge sandbox connectivity is working properly.")
}