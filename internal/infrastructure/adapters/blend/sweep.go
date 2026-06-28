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
// the Base EOA. Best-effort: failures are logged but do not fail the redemption.
func (r *DepositRouter) sweepToSolana(ctx context.Context, acct *blendUserAccount, amount decimal.Decimal, redemptionID uuid.UUID) {
	bridge, _ := r.getChainRails()
	if bridge == nil {
		r.logger.Warn("blend sweep: ChainRails not configured, skipping Base→Solana bridge",
			zap.String("redemption_id", redemptionID.String()))
		return
	}

	solWallet, err := r.resolveUserSolanaWallet(ctx, acct.UserID)
	if err != nil || solWallet.Address == "" {
		r.logger.Warn("blend sweep: cannot resolve Solana wallet, skipping",
			zap.String("user_id", acct.UserID.String()), zap.Error(err))
		return
	}

	// Get the Base EOA's USDC token ID for the funding transfer.
	tokenID, err := r.circle.GetUSDCTokenIDOnchain(ctx, acct.CircleWalletID)
	if err != nil || tokenID == "" {
		r.logger.Warn("blend sweep: cannot resolve Base USDC token ID, skipping",
			zap.String("wallet_id", acct.CircleWalletID), zap.Error(err))
		return
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
		r.logger.Error("blend sweep: ChainRails intent creation failed",
			zap.String("redemption_id", redemptionID.String()), zap.Error(err))
		return
	}
	if strings.TrimSpace(intent.IntentAddress) == "" {
		r.logger.Error("blend sweep: ChainRails returned empty intent address",
			zap.String("redemption_id", redemptionID.String()))
		return
	}

	fundAmount, err := chainRailsFundingAmount(intent, amount)
	if err != nil {
		r.logger.Error("blend sweep: invalid funding amount from ChainRails",
			zap.String("redemption_id", redemptionID.String()), zap.Error(err))
		return
	}

	// Verify Base EOA has enough to fund (amount + bridge fee).
	have, err := r.usdcBalance(ctx, acct.CircleWalletID)
	if err != nil || have.LessThan(fundAmount) {
		r.logger.Warn("blend sweep: insufficient Base EOA balance for bridge",
			zap.String("redemption_id", redemptionID.String()),
			zap.String("have", have.StringFixed(6)),
			zap.String("need", fundAmount.StringFixed(6)), zap.Error(err))
		return
	}

	// Fund the intent: same-chain Circle transfer Base EOA → intent address.
	idemKey := uuid.NewSHA1(uuid.NameSpaceOID,
		[]byte(fmt.Sprintf("blend-sweep-%s", redemptionID.String()))).String()

	tx, err := r.circle.TransferUSDCWithIdempotency(ctx, acct.CircleWalletID, tokenID, intent.IntentAddress, fundAmount.StringFixed(6), idemKey)
	if err != nil {
		r.logger.Error("blend sweep: Circle funding transfer failed",
			zap.String("redemption_id", redemptionID.String()),
			zap.String("intent_address", intent.IntentAddress), zap.Error(err))
		return
	}

	if _, err := r.waitCircleTransfer(ctx, tx); err != nil {
		r.logger.Error("blend sweep: Circle funding transfer did not settle",
			zap.String("redemption_id", redemptionID.String()), zap.Error(err))
		return
	}

	r.logger.Info("blend sweep: Base→Solana bridge funded, ChainRails settling",
		zap.String("redemption_id", redemptionID.String()),
		zap.String("user_id", acct.UserID.String()),
		zap.String("amount", amount.StringFixed(6)),
		zap.String("intent_address", intent.IntentAddress),
		zap.String("solana_dest", solWallet.Address))
}


// sweepSourceChain derives the ChainRails source chain identifier from the
// router's configured chainID, matching the environment (mainnet vs testnet).
func (r *DepositRouter) sweepSourceChain() string {
	if r.chainID == BaseMainnetChainID {
		return "BASE_MAINNET"
	}
	return "BASE_TESTNET"
}
