// Package recovery contains one-off operational recovery routines for funds that get
// stranded by external-provider edge cases. These are invoked manually (a CLI subcommand
// or a script), never on the request path.
package recovery

import (
	"context"
	"fmt"
	"io"
	"math/big"
	"strings"
	"time"

	chainrails "github.com/rail-service/rail_service/internal/infrastructure/adapters/chainrails"
	circle "github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

const (
	baseUSDC    = "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"
	sourceChain = "BASE_MAINNET"
	destChain   = "SOLANA_MAINNET"
)

// Params describes a single stuck-funds recovery.
type Params struct {
	WalletID     string          // Circle Base wallet ID holding the stranded USDC
	ToSolanaAddr string          // destination Solana address
	DestAmount   decimal.Decimal // USDC the recipient should receive on Solana
	Confirm      bool            // false = dry run (no funds move)
	PollInterval time.Duration
	PollMax      int
}

// RecoverStuckBaseToSolana sweeps USDC stranded in a Base Circle wallet back to a Solana
// address via ChainRails (the same bridge that delivered it, run in reverse). It reads the
// true on-chain balance with includeAll=true (Circle's default indexed balance does not see
// bridge-delivered tokens), creates a ChainRails intent, then funds it with a same-chain
// Circle transfer. With Confirm=false it prints the quote and stops without moving funds.
func RecoverStuckBaseToSolana(ctx context.Context, cc *circle.HTTPClient, cr *chainrails.Client, p Params, logger *zap.Logger, out io.Writer) error {
	if p.WalletID == "" || p.ToSolanaAddr == "" {
		return fmt.Errorf("wallet id and destination address are required")
	}
	if !p.DestAmount.GreaterThan(decimal.Zero) {
		return fmt.Errorf("amount must be positive")
	}
	if p.PollInterval <= 0 {
		p.PollInterval = 8 * time.Second
	}
	if p.PollMax <= 0 {
		p.PollMax = 60
	}
	adapter := circle.NewAdapter(cc, logger)

	// 1. Resolve the wallet + true on-chain USDC balance and Circle token id.
	wallet, err := cc.GetWallet(ctx, p.WalletID)
	if err != nil {
		return fmt.Errorf("get wallet: %w", err)
	}
	tokenID, balance, err := onchainUSDC(ctx, cc, p.WalletID)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Wallet %s\n  address=%s chain=%s state=%s type=%s\n  on-chain USDC (includeAll)=%s  tokenId=%s\n\n",
		p.WalletID, wallet.Address, wallet.Blockchain, wallet.State, wallet.AccountType, balance.StringFixed(6), tokenID)
	if tokenID == "" {
		return fmt.Errorf("Circle exposes no USDC tokenId for this wallet even with includeAll — escalate to Circle support")
	}

	// 2. Create the ChainRails intent (Base -> Solana). Quote + address only; no funds move.
	micro := p.DestAmount.Truncate(6).Shift(6).BigInt()
	intent, err := cr.CreateIntent(ctx, &chainrails.CreateIntentRequest{
		Sender:           wallet.Address,
		Amount:           micro.String(),
		AmountSymbol:     "USDC",
		TokenIn:          baseUSDC,
		SourceChain:      sourceChain,
		DestinationChain: destChain,
		Recipient:        p.ToSolanaAddr,
		RefundAddress:    wallet.Address,
		Metadata:         map[string]interface{}{"type": "manual_stuck_funds_recovery", "wallet_id": p.WalletID},
	})
	if err != nil {
		return fmt.Errorf("chainrails create intent: %w", err)
	}

	fundAmount := decimal.Zero
	if raw, ok := new(big.Int).SetString(strings.TrimSpace(intent.TotalAmountInAssetToken), 10); ok && intent.AssetTokenDecimals > 0 {
		fundAmount = decimal.NewFromBigInt(raw, -int32(intent.AssetTokenDecimals))
	}
	if !fundAmount.GreaterThan(decimal.Zero) {
		return fmt.Errorf("chainrails returned no/invalid funding amount (total_amount_in_asset_token=%q, decimals=%d)",
			intent.TotalAmountInAssetToken, intent.AssetTokenDecimals)
	}

	fmt.Fprintf(out, "ChainRails intent\n  id=%d  intent_address=%s\n  recipient(Solana)=%s\n  receive ≈ %s USDC, fund (send to intent) = %s USDC, fees ≈ $%s\n\n",
		intent.ID, intent.IntentAddress, intent.Recipient, p.DestAmount.StringFixed(6), fundAmount.StringFixed(6), intent.FeesInUSD)

	if intent.IntentAddress == "" {
		return fmt.Errorf("chainrails returned no intent_address")
	}
	if fundAmount.GreaterThan(balance) {
		return fmt.Errorf("funding amount %s exceeds available balance %s — rerun with a smaller amount",
			fundAmount.StringFixed(6), balance.StringFixed(6))
	}

	if !p.Confirm {
		fmt.Fprintln(out, "DRY RUN — no funds moved. Re-run with -confirm to fund the intent and bridge.")
		return nil
	}

	// 3. Fund the intent: same-chain Circle transfer Base wallet -> intent address.
	fmt.Fprintf(out, "Funding intent: Circle transfer %s USDC -> %s ...\n", fundAmount.StringFixed(6), intent.IntentAddress)
	idem := fmt.Sprintf("recover-%s-%d", p.WalletID, intent.ID)
	tx, err := adapter.TransferUSDCWithIdempotency(ctx, p.WalletID, tokenID, intent.IntentAddress, fundAmount.StringFixed(6), idem)
	if err != nil {
		return fmt.Errorf("circle fund transfer failed: %w", err)
	}
	fmt.Fprintf(out, "  Circle transfer submitted: tx_id=%s state=%s\n", tx.ID, tx.State)

	// 4. Poll ChainRails until settlement.
	fmt.Fprintln(out, "Waiting for ChainRails to settle on Solana...")
	for i := 0; i < p.PollMax; i++ {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while waiting for settlement (funds are funded; recheck the intent): %w", ctx.Err())
		case <-time.After(p.PollInterval):
		}
		st, serr := cr.GetIntentStatus(ctx, intent.IntentAddress)
		if serr != nil {
			fmt.Fprintf(out, "  poll error (will retry): %v\n", serr)
			continue
		}
		s := strings.ToUpper(strings.TrimSpace(st.Status))
		fmt.Fprintf(out, "  intent status=%s tx=%s\n", s, st.TxHash)
		switch s {
		case "COMPLETED", "COMPLETE", "SUCCESS", "SUCCEEDED", "SETTLED", "FULFILLED":
			fmt.Fprintf(out, "\n✅ Recovered. %s USDC bridged to %s on Solana. dest tx=%s\n", p.DestAmount.StringFixed(6), p.ToSolanaAddr, st.TxHash)
			return nil
		case "FAILED", "CANCELLED", "CANCELED", "EXPIRED", "REJECTED", "REFUNDED":
			return fmt.Errorf("chainrails intent ended in %s — funds refunded to %s per ChainRails policy", s, wallet.Address)
		}
	}
	fmt.Fprintln(out, "\n⏳ Funded, but not settled within the poll window. It will continue settling; recheck the intent address on ChainRails.")
	return nil
}

func onchainUSDC(ctx context.Context, cc *circle.HTTPClient, walletID string) (string, decimal.Decimal, error) {
	balances, err := cc.GetTokenBalanceOnchain(ctx, walletID)
	if err != nil {
		return "", decimal.Zero, fmt.Errorf("read on-chain balances (includeAll): %w", err)
	}
	for _, b := range balances {
		if strings.EqualFold(b.Token.Symbol, "USDC") {
			amt, err := decimal.NewFromString(b.Amount)
			if err != nil {
				return "", decimal.Zero, fmt.Errorf("parse USDC amount %q: %w", b.Amount, err)
			}
			return b.Token.ID, amt, nil
		}
	}
	return "", decimal.Zero, nil
}
