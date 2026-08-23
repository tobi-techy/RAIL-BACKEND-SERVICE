// miriam_test_cli is a local tool for testing Miriam V2 conversations
// with real or mocked data before pushing to production.
//
// Usage:
//   go run scripts/miriam_test_cli/main.go
//
// Requires CENCORI_API_KEY in env (or .env file).
// Simulates the full orchestrator pipeline: system prompt, context assembly,
// tool calls (mock data), quality gate, and bubble-break formatting.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/ai"
	"github.com/rail-service/rail_service/internal/domain/services/spending"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// --- Mock providers for realistic test data ---

type mockStats struct{}

func (m *mockStats) GetAccountBalance(_ context.Context, _ uuid.UUID, acctType entities.AccountType) (decimal.Decimal, error) {
	switch acctType {
	case entities.AccountTypeSpendingBalance:
		return decimal.NewFromFloat(412.50), nil
	case entities.AccountTypeStashBalance:
		return decimal.NewFromFloat(735.00), nil
	default:
		return decimal.Zero, nil
	}
}

type mockSpending struct{}

func (m *mockSpending) GetSummary(_ context.Context, _ uuid.UUID, _, _ time.Time) (*spending.Summary, error) {
	return &spending.Summary{
		Total:   decimal.NewFromFloat(189.30),
		TxCount: 12,
		Categories: []entities.SpendingByCategory{
			{Category: "Food & Delivery", Total: decimal.NewFromFloat(94.00)},
			{Category: "Transport", Total: decimal.NewFromFloat(45.50)},
			{Category: "Entertainment", Total: decimal.NewFromFloat(29.80)},
			{Category: "Shopping", Total: decimal.NewFromFloat(20.00)},
		},
	}, nil
}

func (m *mockSpending) GetTransactions(_ context.Context, _ uuid.UUID, _, _ time.Time, _ int) ([]entities.SpendingTransaction, error) {
	return []entities.SpendingTransaction{
		{Date: "2026-06-22", Amount: decimal.NewFromFloat(23.50), Category: "Food", Source: "card"},
		{Date: "2026-06-22", Amount: decimal.NewFromFloat(12.00), Category: "Transport", Source: "card"},
		{Date: "2026-06-21", Amount: decimal.NewFromFloat(15.99), Category: "Entertainment", Source: "card"},
		{Date: "2026-06-20", Amount: decimal.NewFromFloat(45.00), Category: "Groceries", Source: "card"},
	}, nil
}

func (m *mockSpending) GetMoneyFlow(_ context.Context, _ uuid.UUID, _, _ time.Time) (*entities.MoneyFlowSummary, error) {
	return &entities.MoneyFlowSummary{
		TotalDeposits:    decimal.NewFromFloat(1200.00),
		TotalWithdrawals: decimal.NewFromFloat(150.00),
		TotalCardSpend:   decimal.NewFromFloat(189.30),
		TotalP2P:         decimal.NewFromFloat(50.00),
	}, nil
}

func (m *mockSpending) GetDailyTrend(_ context.Context, _ uuid.UUID, _, _ time.Time) ([]entities.SpendingByPeriod, error) {
	return nil, nil
}

// --- Main ---

func main() {
	logger := zap.NewNop() // silent — we show our own output

	apiKey := os.Getenv("CENCORI_API_KEY")
	model := os.Getenv("CENCORI_MODEL")
	if model == "" {
		model = "gpt-4o"
	}
	if apiKey == "" {
		if data, err := os.ReadFile(".env"); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "CENCORI_API_KEY=") {
					apiKey = strings.TrimPrefix(line, "CENCORI_API_KEY=")
					apiKey = strings.TrimSpace(apiKey)
				}
				if strings.HasPrefix(line, "CENCORI_MODEL=") {
					model = strings.TrimPrefix(line, "CENCORI_MODEL=")
					model = strings.TrimSpace(model)
				}
			}
		}
	}
	if apiKey == "" {
		fmt.Println("ERROR: Set CENCORI_API_KEY in env or .env file")
		os.Exit(1)
	}

	provider := infraai.NewCencoriProvider(&infraai.CencoriConfig{
		APIKey:      apiKey,
		ModelSmart:  model,
		Temperature: 1.0,
	}, logger)

	orch := ai.NewAgentAdapter(nil, provider, nil, nil, nil, logger)
	orch.SetAggregateStats(&mockStats{})
	orch.SetSpending(&mockSpending{})
	orch.SetConversations(nil)

	// Wire Tavily if key is available
	tavilyKey := os.Getenv("TAVILY_API_KEY")
	if tavilyKey == "" {
		if data, err := os.ReadFile(".env"); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "TAVILY_API_KEY=") {
					tavilyKey = strings.TrimSpace(strings.TrimPrefix(line, "TAVILY_API_KEY="))
				}
			}
		}
	}
	if tavilyKey != "" {
		orch.SetWebSearcher(infraai.NewTavilyClient(tavilyKey))
	}

	userID := uuid.New()
	var history []infraai.Message

	searchStatus := "off"
	if tavilyKey != "" {
		searchStatus = "on"
	}

	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("  Miriam V2 Test Console (model: " + model + ")")
	fmt.Println("  Web search: " + searchStatus)
	fmt.Println("  Type as user. Ctrl+C to exit.")
	fmt.Println("  /reset  — clear history")
	fmt.Println("  /prompt — show system prompt")
	fmt.Println("  /quality — test quality gate on a response")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("  Mock data: Spend $412.50 | Stash $735 | This month: $1200 in, $389 out\n\n")

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\033[36mYou:\033[0m ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		switch input {
		case "/reset":
			history = nil
			fmt.Println("\n  [conversation reset]")
			fmt.Println()
			continue
		case "/prompt":
			fmt.Println("\n--- SystemPromptV2 (first 600 chars) ---")
			fmt.Println(ai.SystemPromptV2[:600] + "...")
			fmt.Printf("\n  [%d chars total]\n\n", len(ai.SystemPromptV2))
			continue
		case "/quality":
			fmt.Println("\n  Enter a response to test quality gate:")
			fmt.Print("  > ")
			if scanner.Scan() {
				testResp := scanner.Text()
				verdict := ai.CheckResponseQuality(testResp)
				if verdict.Pass {
					fmt.Println("  ✓ PASS")
				} else {
					fmt.Printf("  ✗ FAIL: %v\n", verdict.Failures)
					hint := ai.QualityCorrectionHint(verdict.Failures)
					fmt.Printf("  Hint: %s\n", hint)
				}
			}
			fmt.Println()
			continue
		}

		fmt.Print("\033[33mMiriam:\033[0m ")
		start := time.Now()
		bubbleCount := 0

		var err error
		for attempt := 0; attempt < 2; attempt++ {
			err = orch.ChatStream(context.Background(), userID, input, history, func(event ai.StreamEvent) {
				switch event.Type {
				case "token":
					fmt.Print(event.Content)
				case "bubble_break":
					bubbleCount++
					fmt.Print("\n\n")
				case "thinking":
					// silent
				case "tool_result":
					// silent
				case "done":
					// nothing
				}
			})
			if err == nil {
				break
			}
			// Retry once on transient errors (TLS, timeout)
			if attempt == 0 && strings.Contains(err.Error(), "tls") || strings.Contains(err.Error(), "timeout") {
				fmt.Print("\033[2m[retrying...]\033[0m ")
				continue
			}
			break
		}

		elapsed := time.Since(start)
		fmt.Println()
		if err != nil {
			fmt.Printf("  \033[31m[error: %s]\033[0m\n", err)
		}
		fmt.Printf("  \033[2m[%dms | %d bubbles]\033[0m\n\n", elapsed.Milliseconds(), bubbleCount+1)

		history = append(history, infraai.Message{Role: "user", Content: input})
	}
}
