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
	r.sweepFromChainToSolana(ctx, acct, amount, redemptionID, acct.ChainID)
}

// sweepFromChainToSolana bridges redeemed USDC from the user's EOA on the given chain
// back to their Solana wallet via ChainRails. The chainID determines which Circle wallet
// to transfer from and which ChainRails source chain to use.
func (r *DepositRouter) sweepFromChainToSolana(ctx context.Context, acct *blendUserAccount, amount decimal.Decimal, redemptionID uuid.UUID, chainID int64) {
	if err := r.doSweepFromChainToSolana(ctx, acct, amount, redemptionID, chainID); err != nil {
		r.logger.Error("blend sweep: failed, will retry on next worker tick",
			zap.String("redemption_id", redemptionID.String()), zap.Error(err))
		if dbErr := r.persistSweepFailure(redemptionID, err.Error()); dbErr != nil {
			r.logger.Error("CRITICAL: blend sweep failed AND could not persist failure to DB — worker will re-sweep",
				zap.String("redemption_id", redemptionID.String()), zap.Error(dbErr))
		}
		return
	}
	if dbErr := r.persistSweepSuccess(redemptionID); dbErr != nil {
		r.logger.Error("CRITICAL: blend sweep succeeded on-chain but could not persist to DB — worker will re-sweep risking double-bridge",
			zap.String("redemption_id", redemptionID.String()), zap.Error(dbErr))
	}
}

func (r *DepositRouter) persistSweepSuccess(redemptionID uuid.UUID) error {
	_, err := r.db.ExecContext(context.Background(), `
		UPDATE blend_yield_redemptions
		SET swept_at = NOW(), sweep_failed_reason = NULL, updated_at = NOW()
		WHERE id = $1
	`, redemptionID)
	return err
}

func (r *DepositRouter) persistSweepFailure(redemptionID uuid.UUID, reason string) error {
	_, err := r.db.ExecContext(context.Background(), `
		UPDATE blend_yield_redemptions
		SET sweep_failed_reason = $2, updated_at = NOW()
		WHERE id = $1
	`, redemptionID, reason)
	return err
}

func (r *DepositRouter) doSweepFromChainToSolana(ctx context.Context, acct *blendUserAccount, amount decimal.Decimal, redemptionID uuid.UUID, chainID int64) error {
	bridge, _ := r.getChainRails()
	if bridge == nil {
		return fmt.Errorf("ChainRails not configured")
	}

	solWallet, err := r.resolveUserSolanaWallet(ctx, acct.UserID)
	if err != nil || solWallet.Address == "" {
		return fmt.Errorf("cannot resolve Solana wallet: %w", err)
	}

	// Resolve wallet for the source chain — funds are on the chain where the vault executed.
	sourceWallet, err := r.resolveUserWalletByChainID(ctx, acct.UserID, chainID)
	if err != nil || sourceWallet.CircleWalletID == "" {
		return fmt.Errorf("cannot resolve wallet for chain %d: %w", chainID, err)
	}
	sourceAddress := sourceWallet.Address
	if sourceAddress == "" {
		sourceAddress = acct.EOAAddress // fallback to the EOA we know
	}

	tokenID, err := r.circle.GetUSDCTokenIDOnchain(ctx, sourceWallet.CircleWalletID)
	if err != nil || tokenID == "" {
		return fmt.Errorf("cannot resolve USDC token ID for chain %d: %w", chainID, err)
	}

	micro := amount.Truncate(6).Shift(6).BigInt()

	// Determine ChainRails source chain from the chain ID → Circle blockchain name → ChainRails mapping.
	circleChains, ok := chainIDToCircleChains[chainID]
	if !ok || len(circleChains) == 0 {
		return fmt.Errorf("unsupported chain %d for ChainRails sweep", chainID)
	}
	source, ok := circleBlockchainToChainRails[circleChains[0]]
	if !ok {
		return fmt.Errorf("ChainRails not configured for circle blockchain %s (chain %d)", circleChains[0], chainID)
	}

	intent, err := bridge.CreateIntent(ctx, &chainrailspkg.CreateIntentRequest{
		Sender:           sourceAddress,
		Amount:           micro.String(),
		AmountSymbol:     "USDC",
		TokenIn:          source.token,
		SourceChain:      source.chain,
		DestinationChain: "SOLANA_MAINNET",
		Recipient:        solWallet.Address,
		RefundAddress:    sourceAddress,
		Metadata: map[string]interface{}{
			"type":          "blend_redemption_sweep",
			"redemption_id": redemptionID.String(),
			"user_id":       acct.UserID.String(),
			"source_chain":  chainID,
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

	have, err := r.usdcBalance(ctx, sourceWallet.CircleWalletID)
	if err != nil {
		return fmt.Errorf("cannot check EOA balance for chain %d: %w", chainID, err)
	}
	if have.LessThan(fundAmount) {
		// If balance is close (shortfall < $0.50) and we have at least $1,
		// create a reduced intent with what we can afford rather than leaving
		// funds permanently stranded. The user gets slightly less than requested.
		gasBuffer := decimal.NewFromFloat(0.01)
		if have.GreaterThan(decimal.NewFromFloat(1)) && fundAmount.Sub(have).LessThan(decimal.NewFromFloat(0.50)) {
			reducedAmount := have.Sub(gasBuffer)
			reducedMicro := reducedAmount.Truncate(6).Shift(6).BigInt()
			r.logger.Warn("blend sweep: EOA balance slightly short, creating reduced intent",
				zap.String("redemption_id", redemptionID.String()),
				zap.Int64("chain_id", chainID),
				zap.String("requested", amount.StringFixed(6)),
				zap.String("fundAmount", fundAmount.StringFixed(6)),
				zap.String("have", have.StringFixed(6)),
				zap.String("reduced_amount", reducedAmount.StringFixed(6)))

			reducedIntent, rerr := bridge.CreateIntent(ctx, &chainrailspkg.CreateIntentRequest{
				Sender:           sourceAddress,
				Amount:           reducedMicro.String(),
				AmountSymbol:     "USDC",
				TokenIn:          source.token,
				SourceChain:      source.chain,
				DestinationChain: "SOLANA_MAINNET",
				Recipient:        solWallet.Address,
				RefundAddress:    sourceAddress,
				Metadata: map[string]interface{}{
					"type":          "blend_redemption_sweep",
					"redemption_id": redemptionID.String(),
					"user_id":       acct.UserID.String(),
					"source_chain":  chainID,
					"reduced":       true,
				},
			})
			if rerr != nil {
				return fmt.Errorf("ChainRails reduced intent creation failed: %w", rerr)
			}
			if strings.TrimSpace(reducedIntent.IntentAddress) == "" {
				return fmt.Errorf("ChainRails returned empty intent address for reduced sweep")
			}
			reducedFund, ferr := chainRailsFundingAmount(reducedIntent, reducedAmount)
			if ferr != nil {
				return fmt.Errorf("invalid reduced funding amount from ChainRails: %w", ferr)
			}
			if have.LessThan(reducedFund) {
				return fmt.Errorf("insufficient EOA balance even for reduced sweep: have %s need %s", have.StringFixed(6), reducedFund.StringFixed(6))
			}
			intent = reducedIntent
			fundAmount = reducedFund
		} else {
			return fmt.Errorf("insufficient EOA balance: have %s need %s", have.StringFixed(6), fundAmount.StringFixed(6))
		}
	}

	idemKey := uuid.NewSHA1(uuid.NameSpaceOID,
		[]byte(fmt.Sprintf("blend-sweep-%s", redemptionID.String()))).String()

	tx, err := r.circle.TransferUSDCWithIdempotency(ctx, sourceWallet.CircleWalletID, tokenID, intent.IntentAddress, fundAmount.StringFixed(6), idemKey)
	if err != nil {
		return fmt.Errorf("Circle funding transfer failed: %w", err)
	}

	if _, err := r.waitCircleTransfer(ctx, tx); err != nil {
		return fmt.Errorf("Circle funding transfer did not settle: %w", err)
	}

	r.logger.Info("blend sweep: chain→Solana bridge funded, ChainRails settling",
		zap.String("redemption_id", redemptionID.String()),
		zap.String("user_id", acct.UserID.String()),
		zap.Int64("source_chain", chainID),
		zap.String("amount", amount.StringFixed(6)),
		zap.String("fund_amount", fundAmount.StringFixed(6)),
		zap.String("intent_address", intent.IntentAddress),
		zap.String("solana_dest", solWallet.Address))
	return nil
}
