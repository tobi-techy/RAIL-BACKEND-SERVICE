//go:build ignore

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/infrastructure/adapters/alpaca"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

func main() {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	// Get credentials from environment or use your sandbox keys
	clientID := getEnv("ALPACA_CLIENT_ID", "CAQ5K26YD7DGSJBUCLOT3L373B")
	secretKey := getEnv("ALPACA_SECRET_KEY", "Avw9W3Xf35fr4gNLGg8umUi5mvgivFdSccCFq1fWSvHr")

	config := alpaca.Config{
		ClientID:    clientID,
		SecretKey:   secretKey,
		Environment: "sandbox",
		Timeout:     30 * time.Second,
	}

	client := alpaca.NewClient(config, logger)
	service := alpaca.NewService(client, logger)
	ctx := context.Background()

	fmt.Println("🚀 Testing Alpaca Integration...")

	// Test 1: Authentication & List Accounts
	fmt.Println("\n1. Testing Authentication...")
	accounts, err := client.ListAccounts(ctx, nil)
	if err != nil {
		log.Fatalf("❌ Authentication failed: %v", err)
	}
	fmt.Printf("✅ Authentication successful. Found %d accounts\n", len(accounts))

	// Test 2: Market Data
	fmt.Println("\n2. Testing Market Data...")
	quote, err := client.GetLatestQuote(ctx, "AAPL")
	if err != nil {
		log.Printf("⚠️  Market data error: %v", err)
	} else {
		fmt.Printf("✅ AAPL Quote: $%s (Bid: $%s, Ask: $%s)\n", 
			quote.Price.String(), quote.Bid.String(), quote.Ask.String())
	}

	// Test 3: Assets
	fmt.Println("\n3. Testing Assets API...")
	assets, err := client.ListAssets(ctx, map[string]string{
		"status": "active",
		"asset_class": "us_equity",
	})
	if err != nil {
		log.Printf("⚠️  Assets error: %v", err)
	} else {
		fmt.Printf("✅ Found %d active assets\n", len(assets))
		if len(assets) > 0 {
			fmt.Printf("   Sample: %s (%s) - Tradable: %t, Fractionable: %t\n",
				assets[0].Symbol, assets[0].Name, assets[0].Tradable, assets[0].Fractionable)
		}
	}

	// Test 4: Create Test Account (if no accounts exist)
	var testAccountID string
	if len(accounts) == 0 {
		fmt.Println("\n4. Creating Test Account...")
		account, err := createTestAccount(service, ctx)
		if err != nil {
			log.Printf("⚠️  Account creation error: %v", err)
		} else {
			testAccountID = account.ID
			fmt.Printf("✅ Created test account: %s\n", testAccountID)
		}
	} else {
		testAccountID = accounts[0].ID
		fmt.Printf("\n4. Using existing account: %s\n", testAccountID)
	}

	if testAccountID != "" {
		// Test 5: Account Balance
		fmt.Println("\n5. Testing Account Balance...")
		account, err := service.GetAccount(ctx, testAccountID)
		if err != nil {
			log.Printf("⚠️  Account balance error: %v", err)
		} else {
			fmt.Printf("✅ Account Balance - Cash: $%s, Buying Power: $%s\n",
				account.Cash.String(), account.BuyingPower.String())
		}

		// Test 6: Order Validation
		fmt.Println("\n6. Testing Order Validation...")
		qty := decimal.NewFromFloat(0.1)
		testOrder := &entities.AlpacaCreateOrderRequest{
			Symbol:      "AAPL",
			Qty:         &qty,
			Side:        entities.AlpacaOrderSideBuy,
			Type:        entities.AlpacaOrderTypeMarket,
			TimeInForce: entities.AlpacaTimeInForceDay,
		}

		if err := alpaca.ValidateOrderRequest(testOrder); err != nil {
			log.Printf("⚠️  Order validation error: %v", err)
		} else {
			fmt.Println("✅ Order validation passed")
		}

		// Test 7: List Positions
		fmt.Println("\n7. Testing Positions...")
		positions, err := service.ListPositions(ctx, testAccountID)
		if err != nil {
			log.Printf("⚠️  Positions error: %v", err)
		} else {
			fmt.Printf("✅ Found %d positions\n", len(positions))
			for _, pos := range positions {
				fmt.Printf("   %s: %s shares @ $%s (P&L: $%s)\n",
					pos.Symbol, pos.Qty.String(), pos.AvgEntryPrice.String(), pos.UnrealizedPL.String())
			}
		}
	}

	fmt.Println("\n🎉 Alpaca integration test completed!")
}

func createTestAccount(service *alpaca.Service, ctx context.Context) (*entities.AlpacaAccountResponse, error) {
	req := &entities.AlpacaCreateAccountRequest{
		Contact: entities.AlpacaContact{
			EmailAddress:  fmt.Sprintf("test+%d@rail.example.com", time.Now().Unix()),
			PhoneNumber:   "+1234567890",
			StreetAddress: []string{"123 Test Street"},
			City:          "San Francisco",
			State:         "CA",
			PostalCode:    "94105",
			Country:       "USA",
		},
		Identity: entities.AlpacaIdentity{
			GivenName:             "Test",
			FamilyName:            "User",
			DateOfBirth:           "1990-01-01",
			TaxID:                 "123456789",
			TaxIDType:             "USA_SSN",
			CountryOfCitizenship:  "USA",
			CountryOfBirth:        "USA",
			CountryOfTaxResidence: "USA",
			FundingSource:         []string{"employment_income"},
		},
		Disclosures: entities.AlpacaDisclosures{
			IsControlPerson:             false,
			IsAffiliatedExchangeOrFINRA: false,
			IsPoliticallyExposed:        false,
			ImmediateFamilyExposed:      false,
			EmploymentStatus:            "employed",
		},
		Agreements: []entities.AlpacaAgreement{
			{
				Agreement: "account",
				SignedAt:  time.Now().Format(time.RFC3339),
				IPAddress: "127.0.0.1",
			},
		},
	}

	return service.CreateAccount(ctx, req)
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
