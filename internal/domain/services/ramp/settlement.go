package ramp

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	chainrailspkg "github.com/rail-service/rail_service/internal/infrastructure/adapters/chainrails"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/ramphub"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// evmToChainRails maps Circle EVM blockchain IDs to ChainRails source chain +
// USDC token, so EVM-held USDC can be bridged to RampHub's Solana deposit
// address. Mirrors the proven mapping used by the Paj offramp path.
var evmToChainRails = map[string]struct{ chain, token string }{
	"ETH-SEPOLIA":  {"ETHEREUM_TESTNET", "0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238"},
	"ETH":          {"ETHEREUM_MAINNET", "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"},
	"BASE-SEPOLIA": {"BASE_TESTNET", "0x036CbD53842c5426634e7929541eC2318f3dCF7e"},
	"BASE":         {"BASE_MAINNET", "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"},
	"ARB-SEPOLIA":  {"ARBITRUM_TESTNET", "0x75faf114eafb1BDbe2F0316DF893fd58CE46AA4d"},
	"ARB":          {"ARBITRUM_MAINNET", "0xaf88d065e77c8cC2239327C5EDb3A432268e5831"},
	"OP-SEPOLIA":   {"OPTIMISM_TESTNET", "0x5fd84259d66Cd46123540766Be93DFE6D43130D7"},
	"OP":           {"OPTIMISM_MAINNET", "0x0b2C639c533813f4Aa9D7837CAf62653d097Ff85"},
	"AVAX-FUJI":    {"AVALANCHE_TESTNET", "0x5425890298aed601595a70AB815c96711a31Bc65"},
	"AVAX":         {"AVALANCHE_MAINNET", "0xB97EF9Ef8734C71904D8002F8b6Bc66Dd9c48a6E"},
}

// executeCircleTransferToRampHub sends USDC from the user's Circle wallet to
// RampHub's deposit address. Solana wallets transfer directly; EVM wallets are
// bridged to Solana via ChainRails. Any failure reverses the ledger hold.
// Must be called in a goroutine; the function owns its context lifetime.
func (s *Service) executeCircleTransferToRampHub(userID uuid.UUID, order *ramphub.OrderResponse, cryptoAmount, totalHold, railFee decimal.Decimal) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("PANIC in executeCircleTransferToRampHub — reversing hold",
				zap.Any("panic", r), zap.String("ramphub_tx_id", order.TransactionID))
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			s.reverseHold(ctx, userID, order.TransactionID, totalHold, railFee, "goroutine_panic")
		}
	}()
	ctx, cancel := contextWithTimeout()
	defer cancel()

	transferAmount, slippageRefund, calcErr := calculateOfframpTransferAmounts(cryptoAmount, totalHold, railFee)
	if calcErr != nil {
		s.logger.Error("RampHub offramp transfer amount invalid — reversing hold",
			zap.Error(calcErr), zap.String("ramphub_tx_id", order.TransactionID))
		s.reverseHold(ctx, userID, order.TransactionID, totalHold, railFee, "invalid_transfer_amount")
		return
	}

	if s.circleTransfer == nil {
		s.logger.Error("Circle transfer adapter not configured — reversing hold",
			zap.String("ramphub_tx_id", order.TransactionID))
		s.reverseHold(ctx, userID, order.TransactionID, totalHold, railFee, "no_circle_adapter")
		return
	}

	walletID, tokenID, blockchain, onChainAddress, err := s.circleTransfer.FindWalletWithUSDC(ctx, userID.String())
	if err != nil {
		s.logger.Error("async: no Circle wallet with USDC for RampHub offramp — reversing hold",
			zap.Error(err), zap.String("user_id", userID.String()),
			zap.String("ramphub_tx_id", order.TransactionID))
		s.reverseHold(ctx, userID, order.TransactionID, totalHold, railFee, "no_usdc_wallet")
		return
	}

	isSolana := strings.Contains(strings.ToUpper(blockchain), "SOL")
	if isSolana {
		tx, txErr := s.circleTransfer.TransferUSDC(ctx, walletID, tokenID, order.OurCryptoAddress, transferAmount.StringFixed(2))
		if txErr != nil {
			s.logger.Error("async: Circle SOL transfer to RampHub failed — reversing hold",
				zap.Error(txErr), zap.String("ramphub_tx_id", order.TransactionID))
			s.reverseHold(ctx, userID, order.TransactionID, totalHold, railFee, "circle_transfer_failed")
			return
		}
		if tx.State == "DENIED" || tx.State == "FAILED" || tx.State == "CANCELLED" {
			s.reverseHold(ctx, userID, order.TransactionID, totalHold, railFee, "circle_transfer_"+string(tx.State))
			return
		}
		s.logger.Info("Circle SOL transfer to RampHub initiated",
			zap.String("circle_tx_id", tx.ID), zap.String("ramphub_tx_id", order.TransactionID))
		s.markBridgeTransfer(ctx, order.TransactionID, "circle:"+tx.ID)
		s.refundSlippage(ctx, userID, order.TransactionID, slippageRefund, "circle_sol")
		return
	}

	// EVM wallet — bridge to RampHub's Solana address via ChainRails.
	if s.chainRailsAdapter != nil {
		s.executeCircleViaChainRails(ctx, userID, walletID, tokenID, blockchain, onChainAddress, order, totalHold, transferAmount, railFee)
		return
	}

	s.logger.Error("USDC on EVM but no ChainRails — cannot bridge to Solana for RampHub",
		zap.String("blockchain", blockchain), zap.String("user_id", userID.String()))
	s.reverseHold(ctx, userID, order.TransactionID, totalHold, railFee, "no_chainrails_evm")
}

// executeCircleViaChainRails bridges EVM USDC to RampHub's Solana address via a
// ChainRails intent funded by a same-chain Circle transfer.
func (s *Service) executeCircleViaChainRails(ctx context.Context, userID uuid.UUID, walletID, tokenID, blockchain, onChainAddress string, order *ramphub.OrderResponse, totalHold, transferAmount, railFee decimal.Decimal) {
	source, ok := evmToChainRails[blockchain]
	if !ok {
		s.logger.Error("unsupported EVM chain for ChainRails RampHub bridge", zap.String("blockchain", blockchain))
		s.reverseHold(ctx, userID, order.TransactionID, totalHold, railFee, "unsupported_chain")
		return
	}

	solDest := "SOLANA_MAINNET"
	if strings.Contains(blockchain, "SEPOLIA") || strings.Contains(blockchain, "FUJI") || strings.Contains(blockchain, "AMOY") || strings.Contains(blockchain, "DEVNET") {
		solDest = "SOLANA_TESTNET"
	}

	amountMicro := transferAmount.Shift(6).IntPart()

	intent, err := s.chainRailsAdapter.CreateIntent(ctx, &chainrailspkg.CreateIntentRequest{
		Amount:           fmt.Sprintf("%d", amountMicro),
		AmountSymbol:     "USDC",
		TokenIn:          source.token,
		SourceChain:      source.chain,
		DestinationChain: solDest,
		Recipient:        order.OurCryptoAddress,
		Sender:           onChainAddress,
		RefundAddress:    onChainAddress,
		Metadata: map[string]interface{}{
			"ramphub_tx_id": order.TransactionID,
			"user_id":       userID.String(),
			"type":          "ramphub_offramp_bridge",
		},
	})
	if err != nil {
		s.logger.Error("ChainRails intent for RampHub bridge failed — reversing hold",
			zap.Error(err), zap.String("ramphub_tx_id", order.TransactionID))
		s.reverseHold(ctx, userID, order.TransactionID, totalHold, railFee, "chainrails_intent_failed")
		return
	}

	circleAmountDecimal := transferAmount
	circleAmount := transferAmount.StringFixed(2)
	if intent.TotalAmountInAssetToken != "" && intent.AssetTokenDecimals > 0 {
		totalMicro, parseOk := new(big.Int).SetString(intent.TotalAmountInAssetToken, 10)
		if parseOk {
			circleAmountDecimal = decimal.NewFromBigInt(totalMicro, -int32(intent.AssetTokenDecimals))
			circleAmount = circleAmountDecimal.StringFixed(int32(intent.AssetTokenDecimals))
		}
	}

	actualDebit := circleAmountDecimal.Add(railFee)
	if actualDebit.GreaterThan(totalHold) {
		s.logger.Error("ChainRails RampHub bridge amount exceeds ledger hold — reversing hold",
			zap.String("ramphub_tx_id", order.TransactionID),
			zap.String("hold_amount", totalHold.String()),
			zap.String("transfer_amount", circleAmountDecimal.String()),
			zap.String("rail_fee", railFee.String()))
		s.reverseHold(ctx, userID, order.TransactionID, totalHold, railFee, "transfer_exceeds_hold")
		return
	}

	tx, txErr := s.circleTransfer.TransferUSDC(ctx, walletID, tokenID, intent.IntentAddress, circleAmount)
	if txErr != nil {
		s.logger.Error("Circle transfer to ChainRails intent for RampHub failed — reversing hold",
			zap.Error(txErr), zap.String("ramphub_tx_id", order.TransactionID))
		s.reverseHold(ctx, userID, order.TransactionID, totalHold, railFee, "circle_cr_transfer_failed")
		return
	}
	if tx.State == "DENIED" || tx.State == "FAILED" || tx.State == "CANCELLED" {
		s.reverseHold(ctx, userID, order.TransactionID, totalHold, railFee, "circle_cr_"+string(tx.State))
		return
	}

	s.logger.Info("Circle→ChainRails→RampHub bridge initiated",
		zap.String("circle_tx_id", tx.ID),
		zap.Int("cr_intent_id", intent.ID),
		zap.String("ramphub_tx_id", order.TransactionID),
		zap.String("source", source.chain),
		zap.String("dest", solDest))

	s.markBridgeTransfer(ctx, order.TransactionID, fmt.Sprintf("circle-cr:%s:%d", tx.ID, intent.ID))

	// Refund only the slippage buffer. Rail fee remains charged.
	excess := totalHold.Sub(actualDebit)
	s.refundSlippage(ctx, userID, order.TransactionID, excess, "circle_chainrails")
}

// calculateOfframpTransferAmounts validates the crypto amount to send fits
// within the ledger hold and returns the transfer amount + slippage refund.
func calculateOfframpTransferAmounts(cryptoAmount, totalHold, railFee decimal.Decimal) (decimal.Decimal, decimal.Decimal, error) {
	// Round to cent precision for stable on-chain transfer strings.
	amt, _ := cryptoAmount.Float64()
	transferAmount := decimal.NewFromFloat(math.Round(amt*100) / 100)
	if !transferAmount.IsPositive() {
		return decimal.Zero, decimal.Zero, fmt.Errorf("transfer amount must be positive")
	}
	actualDebit := transferAmount.Add(railFee)
	if actualDebit.GreaterThan(totalHold) {
		return decimal.Zero, decimal.Zero, fmt.Errorf("transfer amount %s plus fee %s exceeds hold %s",
			transferAmount.String(), railFee.String(), totalHold.String())
	}
	return transferAmount, totalHold.Sub(actualDebit), nil
}

// markBridgeTransfer records the settlement transfer reference on the order.
func (s *Service) markBridgeTransfer(ctx context.Context, txID, ref string) {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE ramphub_orders SET bridge_transfer_id = $1, status = 'processing', updated_at = NOW() WHERE ramphub_transaction_id = $2`,
		ref, txID); err != nil {
		s.logger.Warn("failed to mark ramphub bridge transfer", zap.Error(err), zap.String("ramphub_tx_id", txID))
	}
}

// refundSlippage reverses the unused slippage buffer back to the user. The Rail
// fee remains charged.
func (s *Service) refundSlippage(ctx context.Context, userID uuid.UUID, txID string, amount decimal.Decimal, path string) {
	if !amount.IsPositive() || s.ledger == nil {
		return
	}
	if err := s.ledger.ReverseTransaction(ctx, userID, entities.AccountTypeSpendingBalance,
		"ramphub_offramp_slippage_refund_"+txID, amount, map[string]interface{}{
			"provider": "ramphub", "type": "slippage_refund", "ramphub_tx_id": txID, "path": path,
		}); err != nil {
		s.logger.Warn("failed to refund ramphub slippage", zap.Error(err), zap.String("ramphub_tx_id", txID))
	}
}

// reverseHold reverses the full ledger hold for a failed offramp and marks the
// order failed with deposit_id set (prevents double-reversal by worker/webhook).
// If the ledger reversal fails, the order is restored to unclained so the
// recovery worker can pick it up on its next sweep.
func (s *Service) reverseHold(ctx context.Context, userID uuid.UUID, txID string, amount, railFee decimal.Decimal, reason string) {
	if _, dbErr := s.db.ExecContext(ctx, `
		UPDATE ramphub_orders SET status = 'failed', deposit_id = gen_random_uuid(), updated_at = NOW()
		WHERE ramphub_transaction_id = $1 AND status IN ('pending', 'processing')`, txID); dbErr != nil {
		s.logger.Error("failed to mark RampHub order as failed",
			zap.Error(dbErr), zap.String("ramphub_tx_id", txID))
	}

	if s.ledger == nil {
		return
	}
	if err := s.ledger.ReverseTransaction(ctx, userID, entities.AccountTypeSpendingBalance,
		"ramphub_offramp_"+reason+"_"+txID, amount, map[string]interface{}{
			"provider": "ramphub", "type": "offramp_" + reason + "_reversal", "ramphub_tx_id": txID,
			"rail_fee": railFee.String(), "fee_revenue_posted": true,
		}); err != nil {
		s.logger.Error("CRITICAL: failed to reverse RampHub offramp hold",
			zap.Error(err), zap.String("user_id", userID.String()),
			zap.String("ramphub_tx_id", txID), zap.String("amount", amount.String()))
		// Unclaim so the recovery worker can retry the reversal on its next sweep.
		if _, unclaimErr := s.db.ExecContext(context.Background(), `UPDATE ramphub_orders SET deposit_id = NULL, status = 'processing', updated_at = NOW() WHERE ramphub_transaction_id = $1 AND status = 'failed'`, txID); unclaimErr != nil {
			s.logger.Error("CRITICAL: failed to unclaim RampHub order after failed reversal — requires manual intervention",
				zap.Error(unclaimErr), zap.String("user_id", userID.String()),
				zap.String("ramphub_tx_id", txID), zap.String("amount", amount.String()))
		}
	}
}
