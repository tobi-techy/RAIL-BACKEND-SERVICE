package integration

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/adapters/alpaca"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

func TestAlpacaIntegration(t *testing.T) {
	// Skip if not running integration tests
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	logger := zap.NewNop()
	config := alpaca.Config{
		ClientID:    "YOUR_SANDBOX_CLIENT_ID",
		SecretKey:   "YOUR_SANDBOX_SECRET_KEY",
		Environment: "sandbox",
		Timeout:     30 * time.Second,
	}

	client := alpaca.NewClient(config, logger)
	service := alpaca.NewService(client, logger)
	ctx := context.Background()

	t.Run("Authentication", func(t *testing.T) {
		// Test OAuth2 token retrieval
		accounts, err := service.ListAccounts(ctx, nil)
		require.NoError(t, err)
		t.Logf("Found %d accounts", len(accounts))
	})

	t.Run("Account Creation", func(t *testing.T) {
		req := &entities.AlpacaCreateAccountRequest{
			Contact: entities.AlpacaContact{
				EmailAddress:  "test@example.com",
				PhoneNumber:   "+1234567890",
				StreetAddress: []string{"123 Test St"},
				City:          "Test City",
				State:         "CA",
				PostalCode:    "12345",
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

		account, err := service.CreateAccount(ctx, req)
		require.NoError(t, err)
		assert.NotEmpty(t, account.ID)
		assert.NotEmpty(t, account.AccountNumber)
		t.Logf("Created account: %s", account.ID)

		// Store account ID for subsequent tests
		testAccountID := account.ID

		t.Run("Get Account", func(t *testing.T) {
			retrievedAccount, err := service.GetAccount(ctx, testAccountID)
			require.NoError(t, err)
			assert.Equal(t, account.ID, retrievedAccount.ID)
		})

		t.Run("Fund Account", func(t *testing.T) {
			// In sandbox, use Transfer API to simulate funding
			fundingReq := &entities.AlpacaJournalRequest{
				FromAccount: "FIRM_ACCOUNT", // Your firm account
				ToAccount:   testAccountID,
				EntryType:   "JNLC",
				Amount:      decimal.NewFromFloat(1000.00),
				Description: "Test funding",
			}

			journal, err := service.CreateJournal(ctx, fundingReq)
			require.NoError(t, err)
			assert.Equal(t, "executed", journal.Status)
			t.Logf("Funded account with $1000")
		})

		t.Run("Trading", func(t *testing.T) {
			// Test market order
			orderReq := &entities.AlpacaCreateOrderRequest{
				Symbol:      "AAPL",
				Qty:         decimal.NewFromFloat(1),
				Side:        entities.AlpacaOrderSideBuy,
				Type:        entities.AlpacaOrderTypeMarket,
				TimeInForce: entities.AlpacaTimeInForceDay,
			}

			order, err := service.CreateOrder(ctx, testAccountID, orderReq)
			require.NoError(t, err)
			assert.NotEmpty(t, order.ID)
			assert.Equal(t, "AAPL", order.Symbol)
			t.Logf("Created order: %s", order.ID)

			// Test fractional order
			fractionalReq := &entities.AlpacaCreateOrderRequest{
				Symbol:      "TSLA",
				Qty:         decimal.NewFromFloat(0.5),
				Side:        entities.AlpacaOrderSideBuy,
				Type:        entities.AlpacaOrderTypeMarket,
				TimeInForce: entities.AlpacaTimeInForceDay,
			}

			fractionalOrder, err := service.CreateOrder(ctx, testAccountID, fractionalReq)
			require.NoError(t, err)
			assert.Equal(t, decimal.NewFromFloat(0.5), fractionalOrder.Qty)
			t.Logf("Created fractional order: %s", fractionalOrder.ID)

			// Test notional order
			notionalReq := &entities.AlpacaCreateOrderRequest{
				Symbol:      "SPY",
				Notional:    decimal.NewFromFloat(100),
				Side:        entities.AlpacaOrderSideBuy,
				Type:        entities.AlpacaOrderTypeMarket,
				TimeInForce: entities.AlpacaTimeInForceDay,
			}

			notionalOrder, err := service.CreateOrder(ctx, testAccountID, notionalReq)
			require.NoError(t, err)
			assert.Equal(t, decimal.NewFromFloat(100), *notionalOrder.Notional)
			t.Logf("Created notional order: %s", notionalOrder.ID)
		})

		t.Run("Portfolio", func(t *testing.T) {
			// Wait for orders to settle
			time.Sleep(2 * time.Second)

			positions, err := service.ListPositions(ctx, testAccountID)
			require.NoError(t, err)
			t.Logf("Found %d positions", len(positions))

			if len(positions) > 0 {
				for _, pos := range positions {
					t.Logf("Position: %s, Qty: %s, Market Value: %s",
						pos.Symbol, pos.Qty.String(), pos.MarketValue.String())
				}
			}
		})

		t.Run("Account Activities", func(t *testing.T) {
			activities, err := service.GetAccountActivities(ctx, testAccountID, nil)
			require.NoError(t, err)
			t.Logf("Found %d activities", len(activities))

			for _, activity := range activities {
				t.Logf("Activity: %s, Type: %s, Amount: %s",
					activity.ID, activity.ActivityType, activity.NetAmount.String())
			}
		})
	})
}

func TestAlpacaMarketData(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	logger := zap.NewNop()
	config := alpaca.Config{
		ClientID:    "YOUR_SANDBOX_CLIENT_ID",
		SecretKey:   "YOUR_SANDBOX_SECRET_KEY",
		Environment: "sandbox",
		Timeout:     30 * time.Second,
	}

	client := alpaca.NewClient(config, logger)
	ctx := context.Background()

	t.Run("Latest Quote", func(t *testing.T) {
		quote, err := client.GetLatestQuote(ctx, "AAPL")
		require.NoError(t, err)
		assert.Equal(t, "AAPL", quote.Symbol)
		assert.True(t, quote.Price.GreaterThan(decimal.Zero))
		t.Logf("AAPL Quote: $%s", quote.Price.String())
	})

	t.Run("Multiple Quotes", func(t *testing.T) {
		symbols := []string{"AAPL", "GOOGL", "MSFT"}
		quotes, err := client.GetLatestQuotes(ctx, symbols)
		require.NoError(t, err)
		assert.Len(t, quotes, 3)

		for symbol, quote := range quotes {
			t.Logf("%s: $%s", symbol, quote.Price.String())
		}
	})

	t.Run("Historical Bars", func(t *testing.T) {
		end := time.Now()
		start := end.AddDate(0, 0, -7) // Last 7 days

		bars, err := client.GetBars(ctx, "AAPL", "1Day", start, end)
		require.NoError(t, err)
		assert.NotEmpty(t, bars)

		for _, bar := range bars {
			t.Logf("AAPL %s: O:%s H:%s L:%s C:%s V:%d",
				bar.Timestamp.Format("2006-01-02"),
				bar.Open.String(), bar.High.String(),
				bar.Low.String(), bar.Close.String(), bar.Volume)
		}
	})

	t.Run("News", func(t *testing.T) {
		newsReq := &entities.AlpacaNewsRequest{
			Symbols: []string{"AAPL"},
			Limit:   5,
		}

		news, err := client.GetNews(ctx, newsReq)
		require.NoError(t, err)
		assert.NotEmpty(t, news.News)

		for _, article := range news.News {
			t.Logf("News: %s - %s", article.Headline, article.Source)
		}
	})
}

func TestAlpacaErrorHandling(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	logger := zap.NewNop()
	config := alpaca.Config{
		ClientID:    "INVALID_CLIENT_ID",
		SecretKey:   "INVALID_SECRET",
		Environment: "sandbox",
		Timeout:     30 * time.Second,
	}

	client := alpaca.NewClient(config, logger)
	service := alpaca.NewService(client, logger)
	ctx := context.Background()

	t.Run("Invalid Credentials", func(t *testing.T) {
		_, err := service.ListAccounts(ctx, nil)
		require.Error(t, err)
		t.Logf("Expected error: %v", err)
	})

	t.Run("Invalid Order", func(t *testing.T) {
		// Test with valid credentials but invalid order
		validConfig := alpaca.Config{
			ClientID:    "YOUR_SANDBOX_CLIENT_ID",
			SecretKey:   "YOUR_SANDBOX_SECRET_KEY",
			Environment: "sandbox",
		}
		validClient := alpaca.NewClient(validConfig, logger)
		validService := alpaca.NewService(validClient, logger)

		invalidOrder := &entities.AlpacaCreateOrderRequest{
			Symbol: "", // Invalid: empty symbol
			Side:   entities.AlpacaOrderSideBuy,
			Type:   entities.AlpacaOrderTypeMarket,
		}

		_, err := validService.CreateOrder(ctx, "test-account", invalidOrder)
		require.Error(t, err)
		t.Logf("Validation error: %v", err)
	})
}
