package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/ai/core"
	"github.com/shopspring/decimal"
)

// RegisterFundingTools registers the funding-instructions tool: the chat-native
// answer to "how do I put money in?" so a first deposit is completable without
// leaving the conversation.
func RegisterFundingTools(r *Registry) {
	r.Register(NewTool(
		"get_funding_instructions",
		`Get the user's ways to add money: their fiat virtual account details (bank transfer) and their stablecoin deposit address. Use when the user asks how to add/deposit/fund money, when they agree to make their first deposit, or when a plan ends in "put money in". Returns real account numbers and a deposit address you can hand over directly — never tell them to hunt for it in the app. Idempotent: the same address comes back every time.`,
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"method": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"bank", "crypto", "any"},
					"description": "Which rail to fetch. 'bank' for virtual account details, 'crypto' for a USDC deposit address, 'any' (default) for both.",
				},
				"chain": map[string]interface{}{
					"type":        "string",
					"description": "Chain for the crypto address: base (default), solana, polygon, ethereum, arbitrum, optimism, avalanche, celo.",
				},
				"amount": map[string]interface{}{
					"type":        "string",
					"description": "Optional USD amount they're planning to deposit (e.g. '100'). When given, the result includes the 70/30 split preview.",
				},
			},
			"required":             []string{},
			"additionalProperties": false,
		},
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps == nil || deps.FundingInstructions == nil {
				return &core.ToolResult{Data: map[string]interface{}{
					"available":  false,
					"suggestion": "Funding instructions aren't available right now. They can deposit from the RAIL app home screen.",
				}}, nil
			}

			method := strings.ToLower(strings.TrimSpace(GetArgString(args, "method")))
			if method == "" {
				method = "any"
			}
			if method != "bank" && method != "crypto" && method != "any" {
				return &core.ToolResult{Error: fmt.Sprintf("unsupported method: %s", method)}, nil
			}

			data := map[string]interface{}{
				"available": true,
				"split":     "Every deposit splits automatically: 70% to Spend, 30% to Stash (yield). No setup needed.",
			}

			if method == "bank" || method == "any" {
				accounts, err := deps.FundingInstructions.GetVirtualAccounts(ctx, userID)
				switch {
				case err != nil:
					data["virtual_accounts_error"] = err.Error()
				default:
					data["virtual_accounts"] = activeVirtualAccounts(accounts)
				}
			}

			if method == "crypto" || method == "any" {
				chain, err := parseFundingChain(GetArgString(args, "chain"))
				if err != nil {
					return &core.ToolResult{Error: err.Error()}, nil
				}
				addr, err := deps.FundingInstructions.CreateDepositAddress(ctx, userID, chain, entities.StablecoinUSDC)
				switch {
				case err != nil:
					data["crypto_error"] = err.Error()
				case addr != nil:
					data["crypto"] = map[string]interface{}{
						"chain":    string(addr.Chain),
						"address":  addr.Address,
						"currency": string(addr.Currency),
					}
				}
			}

			if amountStr := strings.TrimSpace(GetArgString(args, "amount")); amountStr != "" {
				if preview, err := splitPreview(amountStr); err != nil {
					data["split_preview_error"] = err.Error()
				} else {
					data["split_preview"] = preview
				}
			}

			return &core.ToolResult{Data: data}, nil
		},
	))
}

func activeVirtualAccounts(accounts []*entities.VirtualAccount) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(accounts))
	for _, a := range accounts {
		if a == nil || a.Status != entities.VirtualAccountStatusActive {
			continue
		}
		entry := map[string]interface{}{
			"bank_name":       a.BankName,
			"account_number":  a.AccountNumber,
			"currency":        a.Currency,
			"beneficiaryName": a.BeneficiaryName,
		}
		if a.RoutingNumber != "" {
			entry["routing_number"] = a.RoutingNumber
		}
		if len(a.PaymentRails) > 0 {
			entry["payment_rails"] = []string(a.PaymentRails)
		}
		out = append(out, entry)
	}
	return out
}

// parseFundingChain normalizes casual chain names to entity constants.
// Default is Base: cheapest, and where the managed-wallet fast path lives.
func parseFundingChain(raw string) (entities.Chain, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "base":
		return entities.ChainBase, nil
	case "sol", "solana":
		return entities.ChainSOL, nil
	case "eth", "ethereum":
		return entities.ChainETH, nil
	case "matic", "polygon":
		return entities.ChainMATIC, nil
	case "celo":
		return entities.ChainCELO, nil
	case "avax", "avalanche":
		return entities.ChainAvalanche, nil
	case "arb", "arbitrum":
		return entities.ChainArbitrum, nil
	case "op", "optimism":
		return entities.ChainOptimism, nil
	}
	return "", fmt.Errorf("unsupported chain %q (try base, solana, polygon, ethereum, arbitrum, optimism, avalanche, celo)", raw)
}

// splitPreview shows what the 70/30 engine does to a deposit before it lands.
func splitPreview(amountStr string) (map[string]interface{}, error) {
	amount, err := decimal.NewFromString(amountStr)
	if err != nil || !amount.IsPositive() {
		return nil, fmt.Errorf("invalid amount: %s", amountStr)
	}
	spend := amount.Mul(decimal.NewFromFloat(0.7)).Round(2)
	stash := amount.Sub(spend).Round(2)
	return map[string]interface{}{
		"deposit":  "$" + amount.Round(2).StringFixed(2),
		"to_spend": "$" + spend.StringFixed(2),
		"to_stash": "$" + stash.StringFixed(2),
	}, nil
}
