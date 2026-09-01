package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/ai/core"
)

// RegisterBankTransferTools registers tools for sending money to Nigerian bank
// accounts via the existing RampHub offramp infrastructure, and for sending
// crypto (USDC) to external wallet addresses via the withdrawal service.
//
// Read tools (list_banks, resolve_bank_account) run directly so Miriam can
// look up bank codes and verify account holder names before staging a send.
// send_to_bank and send_crypto are action tools staged for Face ID confirmation
// (both are fund-moving).
func RegisterBankTransferTools(r *Registry) {
	// --- Read-only tools ---

	r.Register(NewTool(
		"list_banks",
		"List Nigerian banks supported for payouts (bank transfers). Returns bank_code and bank_name for each. Call this when the user names a bank (e.g. 'GTBank', 'Access', 'Zenith') to get the bank_code needed by resolve_bank_account and send_to_bank.",
		SimpleArgs(nil, nil),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.BankTransfer == nil {
				return &core.ToolResult{Error: "bank transfers not available"}, nil
			}
			banks, err := deps.BankTransfer.ListBanks(ctx)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"banks": banks, "count": len(banks)}}, nil
		},
	))

	r.Register(NewTool(
		"resolve_bank_account",
		"Resolve the account holder name for a Nigerian bank account. Always call this before send_to_bank so the user can confirm the name matches who they intend to pay. Does not move money.",
		SimpleArgs(map[string]map[string]interface{}{
			"bank_code":      StringParam("Bank code from list_banks (e.g. '058' for GTBank)"),
			"account_number": StringParam("10-digit NUBAN account number, e.g. '0916473844'"),
			"bank_name":       StringParam("Bank name (optional but improves match rate, e.g. 'GTBank')"),
		}, []string{"bank_code", "account_number"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.BankTransfer == nil {
				return &core.ToolResult{Error: "bank transfers not available"}, nil
			}
			bankCode := strings.TrimSpace(GetArgString(args, "bank_code"))
			accountNumber := strings.TrimSpace(GetArgString(args, "account_number"))
			bankName := GetArgString(args, "bank_name")
			if bankCode == "" || accountNumber == "" {
				return &core.ToolResult{Error: "bank_code and account_number are required"}, nil
			}
			res, err := deps.BankTransfer.ResolveBankAccount(ctx, bankCode, accountNumber, bankName)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: res}, nil
		},
	))

	// --- Action tools (staged for confirmation) ---

	r.Register(NewTool(
		"send_to_bank",
		"Send money to a Nigerian bank account via the existing offramp (USDC → NGN). The user's Spend balance is debited in USDC at the live rate and the Naira amount is delivered to the recipient's bank account. ALWAYS call resolve_bank_account first and confirm the account holder name with the user. Moves real money — staged for Face ID confirmation.",
		SimpleArgs(map[string]map[string]interface{}{
			"bank_code":      StringParam("Bank code from list_banks"),
			"account_number": StringParam("10-digit NUBAN account number"),
			"bank_name":      StringParam("Bank name (optional but improves routing)"),
			"account_name":   StringParam("Resolved account holder name from resolve_bank_account (included in confirmation)"),
			"amount":         StringParam("Amount to send in Naira, e.g. '2500' for ₦2,500"),
			"currency":       EnumParam("Currency (default NGN)", []string{"NGN"}),
		}, []string{"bank_code", "account_number", "amount"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.BankTransfer == nil {
				return &core.ToolResult{Error: "bank transfers not available"}, nil
			}
			bankCode := strings.TrimSpace(GetArgString(args, "bank_code"))
			accountNumber := strings.TrimSpace(GetArgString(args, "account_number"))
			bankName := GetArgString(args, "bank_name")
			accountName := GetArgString(args, "account_name")
			amountStr := normalizeAmountArg(GetArgString(args, "amount"))
			currency := GetArgString(args, "currency")
			if currency == "" {
				currency = "NGN"
			}
			if bankCode == "" || accountNumber == "" || amountStr == "" {
				return &core.ToolResult{Error: "bank_code, account_number, and amount are required"}, nil
			}
			res, err := deps.BankTransfer.CreateOfframp(ctx, userID, bankCode, accountNumber, bankName, amountStr, currency, accountName)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: res}, nil
		},
	))

	r.Register(NewTool(
		"send_crypto",
		"Send USDC from the user's Spend balance to an external crypto wallet address. Supports Solana and EVM chains. Moves real money — staged for Face ID confirmation. Always confirm the destination address with the user before staging.",
		SimpleArgs(map[string]map[string]interface{}{
			"destination_address": StringParam("Wallet address to send USDC to (Solana or EVM 0x... address)"),
			"amount":              StringParam("Amount in USDC, e.g. '50.00'"),
			"chain":               EnumParam("Destination chain (default solana)", []string{"solana", "evm"}),
		}, []string{"destination_address", "amount"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.CryptoSend == nil {
				return &core.ToolResult{Error: "crypto transfers not available"}, nil
			}
			address := strings.TrimSpace(GetArgString(args, "destination_address"))
			amount := normalizeAmountArg(GetArgString(args, "amount"))
			chain := GetArgString(args, "chain")
			if chain == "" {
				chain = "solana"
			}
			if address == "" || amount == "" {
				return &core.ToolResult{Error: "destination_address and amount are required"}, nil
			}
			res, err := deps.CryptoSend.SendCrypto(ctx, userID, address, amount, chain)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: res}, nil
		},
	))
}

// RegisterAllBankTransferTools is the convenience entry point.
func RegisterAllBankTransferTools(r *Registry) {
	RegisterBankTransferTools(r)
}

// truncateAddress shortens a crypto address for display in confirmation cards.
func truncateAddress(addr string) string {
	if len(addr) <= 12 {
		return addr
	}
	return addr[:6] + "..." + addr[len(addr)-4:]
}

// formatBankTransferDescription builds a human-readable summary for the
// pending-action confirmation card.
func formatBankTransferDescription(accountName, bankName, accountNumber, amount, currency string) string {
	if accountName != "" {
		return fmt.Sprintf("Send %s%s to %s — %s %s", currencySymbol(currency), amount, accountName, bankName, accountNumber)
	}
	return fmt.Sprintf("Send %s%s to %s %s", currencySymbol(currency), amount, bankName, accountNumber)
}

func formatCryptoSendDescription(address, amount string) string {
	return fmt.Sprintf("Send $%s USDC to %s", amount, truncateAddress(address))
}

func currencySymbol(currency string) string {
	switch strings.ToUpper(currency) {
	case "NGN":
		return "₦"
	case "USD":
		return "$"
	case "GBP":
		return "£"
	case "EUR":
		return "€"
	default:
		return ""
	}
}

// normalizeAmountArg expands shorthand amount notation to full numeric strings.
// "2k" → "2000", "2.5k" → "2500", "1m" → "1000000", "500" → "500".
// If the input doesn't match a shorthand pattern, it is returned unchanged.
func normalizeAmountArg(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return s
	}

	// Try k suffix (thousands): "2k", "2.5k", "0.5k"
	if strings.HasSuffix(s, "k") {
		numStr := strings.TrimSuffix(s, "k")
		if f, err := strconv.ParseFloat(numStr, 64); err == nil {
			return strconv.FormatFloat(f*1000, 'f', -1, 64)
		}
	}

	// Try m suffix (millions): "1m", "2.5m"
	if strings.HasSuffix(s, "m") {
		numStr := strings.TrimSuffix(s, "m")
		if f, err := strconv.ParseFloat(numStr, 64); err == nil {
			return strconv.FormatFloat(f*1000000, 'f', -1, 64)
		}
	}

	return s
}
