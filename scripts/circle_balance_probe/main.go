// circle_balance_probe diagnoses (and helps recover) the Circle "phantom balance"
// problem: USDC that arrived in a Base Circle wallet via an external bridge contract
// shows on-chain (basescan) but Circle's default indexed balance reports $0, so every
// Circle operation that depends on the indexed balance (the Blend deposit funding gate,
// GetUSDCTokenID, TransferUSDC, contract execution) stalls or fails with
// "insufficient balance".
//
// For each wallet it prints three numbers side by side:
//
//  1. Circle indexed balance        — GET /balances           (what the funding gate sees today)
//  2. Circle on-chain balance       — GET /balances?includeAll=true   (forces a live read)
//  3. True chain balance            — eth_call balanceOf(...) over Base RPC (ground truth)
//
// If (2) and (3) agree but (1) is $0, the fix is simply to read balances with
// includeAll=true everywhere correctness matters — and Circle can then see the funds
// well enough to move them. If (2) is also $0 while (3) is positive, Circle genuinely
// is not indexing the wallet (often an undeployed SCA) and we escalate to the nudge /
// Circle support.
//
// Usage:
//
//	CIRCLE_API_KEY=...  BLEND_BASE_RPC_URL=https://...  \
//	  go run ./scripts/circle_balance_probe <walletId> [walletId...]
//
// Read-only by default. No mutation, no entity secret required.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	circle "github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

const defaultBaseUSDC = "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"

func main() {
	walletIDs := os.Args[1:]
	if len(walletIDs) == 0 {
		log.Fatal("usage: circle_balance_probe <walletId> [walletId...]")
	}

	apiKey := strings.TrimSpace(os.Getenv("CIRCLE_API_KEY"))
	if apiKey == "" {
		log.Fatal("CIRCLE_API_KEY is required")
	}
	rpcURL := firstNonEmpty(os.Getenv("BLEND_BASE_RPC_URL"), os.Getenv("BASE_RPC_URL"))
	if rpcURL == "" {
		log.Fatal("BLEND_BASE_RPC_URL (or BASE_RPC_URL) is required for the on-chain ground-truth read")
	}
	usdc := firstNonEmpty(os.Getenv("BLEND_USDC_ADDRESS"), defaultBaseUSDC)

	logger := zap.NewNop()
	client, err := circle.NewHTTPClient(circle.Config{
		APIKey:      apiKey,
		BaseURL:     strings.TrimSpace(os.Getenv("CIRCLE_BASE_URL")),
		Environment: firstNonEmpty(os.Getenv("CIRCLE_ENVIRONMENT"), "production"),
	}, logger)
	if err != nil {
		log.Fatalf("circle client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	for _, walletID := range walletIDs {
		probeWallet(ctx, client, rpcURL, usdc, strings.TrimSpace(walletID))
		fmt.Println(strings.Repeat("─", 72))
	}
}

func probeWallet(ctx context.Context, client *circle.HTTPClient, rpcURL, usdc, walletID string) {
	fmt.Printf("\nWallet %s\n", walletID)

	wallet, err := client.GetWallet(ctx, walletID)
	if err != nil {
		fmt.Printf("  ✗ GetWallet failed: %v\n", err)
		return
	}
	fmt.Printf("  address=%s  chain=%s  state=%s  accountType=%s\n",
		wallet.Address, wallet.Blockchain, wallet.State, wallet.AccountType)

	indexedBal, idxErr := client.GetTokenBalance(ctx, walletID)
	onchainBal, ocErr := client.GetTokenBalanceOnchain(ctx, walletID)
	if idxErr != nil {
		fmt.Printf("  ! indexed balance read failed: %v\n", idxErr)
	}
	if ocErr != nil {
		fmt.Printf("  ! includeAll balance read failed: %v\n", ocErr)
	}
	indexed := usdcFrom(indexedBal, idxErr)
	onchainAPI := usdcFrom(onchainBal, ocErr)
	chain, chainErr := balanceOfUSDC(ctx, rpcURL, usdc, wallet.Address)

	fmt.Printf("\n  1. Circle indexed balance (GET /balances)             : %s USDC\n", fmtAmt(indexed))
	fmt.Printf("  2. Circle on-chain balance (?includeAll=true)         : %s USDC\n", fmtAmt(onchainAPI))
	if chainErr != nil {
		fmt.Printf("  3. True chain balance (eth_call balanceOf)            : ERROR %v\n", chainErr)
	} else {
		fmt.Printf("  3. True chain balance (eth_call balanceOf)            : %s USDC\n", chain.StringFixed(6))
	}

	fmt.Printf("\n  Verdict: %s\n", verdict(indexed, onchainAPI, chain, chainErr, string(wallet.State), wallet.AccountType))
}

func verdict(indexed, onchainAPI amt, chain decimal.Decimal, chainErr error, state, accountType string) string {
	if chainErr != nil {
		return "could not read chain ground truth; check BLEND_BASE_RPC_URL"
	}
	chainPos := chain.GreaterThan(decimal.Zero)
	switch {
	case !chainPos:
		return "no USDC on-chain — nothing to recover for this wallet"
	case indexed.found && indexed.value.GreaterThan(decimal.Zero):
		return "Circle already sees the balance; this wallet is not the problem"
	case onchainAPI.found && onchainAPI.value.GreaterThan(decimal.Zero):
		return "FIX CONFIRMED → indexed=$0 but includeAll surfaces the funds. " +
			"Switch the Blend funding gate + token-id lookup to GetTokenBalanceOnchain and Circle can move it."
	default:
		extra := ""
		if !strings.EqualFold(state, "LIVE") || strings.EqualFold(accountType, "SCA") {
			extra = " (wallet may be an undeployed SCA — Circle won't index/spend until it is deployed)"
		}
		return "Circle does NOT see the funds even with includeAll" + extra +
			" → escalate to nudge transfer / Circle support."
	}
}

// --- USDC helpers ---

type amt struct {
	found bool
	value decimal.Decimal
}

func usdcFrom(balances []circle.TokenBalance, err error) amt {
	if err != nil {
		return amt{}
	}
	for _, b := range balances {
		if strings.EqualFold(b.Token.Symbol, "USDC") {
			d, perr := decimal.NewFromString(b.Amount)
			if perr != nil {
				return amt{}
			}
			return amt{found: true, value: d}
		}
	}
	return amt{found: true, value: decimal.Zero}
}

func fmtAmt(a amt) string {
	if !a.found {
		return "n/a"
	}
	return a.value.StringFixed(6)
}

// --- minimal eth_call balanceOf(address) over Base RPC ---

// balanceOfSelector is keccak256("balanceOf(address)")[:4].
const balanceOfSelector = "0x70a08231"

func balanceOfUSDC(ctx context.Context, rpcURL, token, holder string) (decimal.Decimal, error) {
	h := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(holder)), "0x")
	if len(h) != 40 {
		return decimal.Zero, fmt.Errorf("invalid holder address %q", holder)
	}
	data := balanceOfSelector + strings.Repeat("0", 24) + h
	reqBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "eth_call",
		"params": []any{map[string]string{"to": token, "data": data}, "latest"},
	})
	if err != nil {
		return decimal.Zero, fmt.Errorf("marshal rpc request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rpcURL, bytes.NewReader(reqBody))
	if err != nil {
		return decimal.Zero, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return decimal.Zero, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return decimal.Zero, fmt.Errorf("read rpc response: %w", err)
	}
	var out struct {
		Result string `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return decimal.Zero, fmt.Errorf("decode rpc: %w (%s)", err, string(raw))
	}
	if out.Error != nil {
		return decimal.Zero, fmt.Errorf("rpc error: %s", out.Error.Message)
	}
	hexv := strings.TrimPrefix(strings.TrimSpace(out.Result), "0x")
	if hexv == "" {
		return decimal.Zero, fmt.Errorf("empty rpc result")
	}
	units, ok := new(big.Int).SetString(hexv, 16)
	if !ok {
		return decimal.Zero, fmt.Errorf("parse balance %q", out.Result)
	}
	// USDC has 6 decimals.
	return decimal.NewFromBigInt(units, -6), nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
