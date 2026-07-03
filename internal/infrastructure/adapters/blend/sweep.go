package blend

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	chainrailspkg "github.com/rail-service/rail_service/internal/infrastructure/adapters/chainrails"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// sweepToSolana bridges redeemed USDC from the user's Base EOA back to their Solana
// wallet via ChainRails. Called after finalizeRedemption confirms funds are on-chain in
// the Base EOA. Best-effort: failures are persisted to swept_failed_reason so the worker
// can retry; success stamps swept_at so the worker ignores it.
func (r *DepositRouter) sweepToSolana(ctx context.Context, acct *blendUserAccount, amount decimal.Decimal, redemptionID uuid.UUID) {
	if err := r.doSweepToSolana(ctx, acct, amount, redemptionID); err != nil {
		r.logger.Error("blend sweep: failed, will retry on next worker tick",
			zap.String("redemption_id", redemptionID.String()), zap.Error(err))
		r.persistSweepFailure(redemptionID, err.Error())
		return
	}
	r.persistSweepSuccess(redemptionID)
}

func (r *DepositRouter) persistSweepSuccess(redemptionID uuid.UUID) {
	_, _ = r.db.ExecContext(context.Background(), `
		UPDATE blend_yield_redemptions
		SET swept_at = NOW(), sweep_failed_reason = NULL, updated_at = NOW()
		WHERE id = $1
	`, redemptionID)
}

func (r *DepositRouter) persistSweepFailure(redemptionID uuid.UUID, reason string) {
	_, _ = r.db.ExecContext(context.Background(), `
		UPDATE blend_yield_redemptions
		SET sweep_failed_reason = $2, updated_at = NOW()
		WHERE id = $1
	`, redemptionID, reason)
}

func (r *DepositRouter) doSweepToSolana(ctx context.Context, acct *blendUserAccount, amount decimal.Decimal, redemptionID uuid.UUID) error {
	bridge, _ := r.getChainRails()
	if bridge == nil {
		return fmt.Errorf("ChainRails not configured")
	}

	solWallet, err := r.resolveUserSolanaWallet(ctx, acct.UserID)
	if err != nil || solWallet.Address == "" {
		return fmt.Errorf("cannot resolve Solana wallet: %w", err)
	}

	tokenID, err := r.circle.GetUSDCTokenIDOnchain(ctx, acct.CircleWalletID)
	if err != nil || tokenID == "" {
		return fmt.Errorf("cannot resolve Base USDC token ID: %w", err)
	}

	micro := amount.Truncate(6).Shift(6).BigInt()

	intent, err := bridge.CreateIntent(ctx, &chainrailspkg.CreateIntentRequest{
		Sender:           acct.EOAAddress,
		Amount:           micro.String(),
		AmountSymbol:     "USDC",
		TokenIn:          r.usdcAddr,
		SourceChain:      r.sweepSourceChain(),
		DestinationChain: "SOLANA_MAINNET",
		Recipient:        solWallet.Address,
		RefundAddress:    acct.EOAAddress,
		Metadata: map[string]interface{}{
			"type":          "blend_redemption_sweep",
			"redemption_id": redemptionID.String(),
			"user_id":       acct.UserID.String(),
		},
	})
	if err != nil {
		return fmt.Errorf("ChainRails intent creation failed: %w", err)
	}
	if strings.TrimSpace(intent.IntentAddress) == "" {
		return fmt.Errorf("ChainRails returned empty intent address")
	}

	fundAmount, err := chainRailsFundingAmount(intent, amount)
	if err != nil {
		return fmt.Errorf("invalid funding amount from ChainRails: %w", err)
	}

	have, err := r.usdcBalance(ctx, acct.CircleWalletID)
	if err != nil {
		return fmt.Errorf("cannot check Base EOA balance: %w", err)
	}
	if have.LessThan(fundAmount) {
		return fmt.Errorf("insufficient Base EOA balance: have %s need %s", have.StringFixed(6), fundAmount.StringFixed(6))
	}

	idemKey := uuid.NewSHA1(uuid.NameSpaceOID,
		[]byte(fmt.Sprintf("blend-sweep-%s", redemptionID.String()))).String()

	tx, err := r.circle.TransferUSDCWithIdempotency(ctx, acct.CircleWalletID, tokenID, intent.IntentAddress, fundAmount.StringFixed(6), idemKey)
	if err != nil {
		return fmt.Errorf("Circle funding transfer failed: %w", err)
	}

	if _, err := r.waitCircleTransfer(ctx, tx); err != nil {
		return fmt.Errorf("Circle funding transfer did not settle: %w", err)
	}

	r.logger.Info("blend sweep: Base→Solana bridge funded, ChainRails settling",
		zap.String("redemption_id", redemptionID.String()),
		zap.String("user_id", acct.UserID.String()),
		zap.String("amount", amount.StringFixed(6)),
		zap.String("intent_address", intent.IntentAddress),
		zap.String("solana_dest", solWallet.Address))
	return nil
}

// sweepSourceChain derives the ChainRails source chain identifier from the
// router's configured chainID, matching the environment (mainnet vs testnet).
func (r *DepositRouter) sweepSourceChain() string {
	if r.chainID == BaseMainnetChainID {
		return "BASE_MAINNET"
	}
	return "BASE_TESTNET"
}
