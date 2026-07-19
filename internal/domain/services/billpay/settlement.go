package billpay

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	chainrailspkg "github.com/rail-service/rail_service/internal/infrastructure/adapters/chainrails"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// evmToChainRails maps Circle EVM blockchain IDs to ChainRails source chain +
// USDC token, so EVM-held USDC can be bridged to Airbills' Solana deposit
// address. Mirrors the proven mapping used by the RampHub offramp path.
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

// settle sends USDC to Airbills' deposit address and, once the transfer
// confirms, processes the Airbills transaction to fulfil the bill. Solana
// wallets transfer directly; EVM wallets bridge to Solana via ChainRails and
// are left for the recovery worker to process once bridged. Any failure before
// the funds leave reverses the ledger hold.
func (s *Service) settle(ctx context.Context, userID uuid.UUID, airbillsID, productCode, depositAddr string, amountInToken, totalHold, railFee decimal.Decimal) {
	walletID, tokenID, blockchain, onChainAddress, err := s.circleTransfer.FindWalletWithUSDC(ctx, userID.String())
	if err != nil {
		s.logger.Error("airbills settle: no Circle wallet with USDC — reversing hold",
			zap.Error(err), zap.String("airbills_id", airbillsID))
		s.reverseHold(ctx, userID, airbillsID, totalHold, railFee, "no_usdc_wallet")
		return
	}

	transferAmount := amountInToken // fee stays held as Rail revenue, not sent
	if transferAmount.Add(railFee).GreaterThan(totalHold) {
		s.reverseHold(ctx, userID, airbillsID, totalHold, railFee, "invalid_transfer_amount")
		return
	}

	if strings.Contains(strings.ToUpper(blockchain), "SOL") {
		tx, txErr := s.circleTransfer.TransferUSDCWithIdempotency(ctx, walletID, tokenID, depositAddr, transferAmount.StringFixed(6), airbillsID)
		if txErr != nil {
			s.logger.Error("airbills settle: Circle SOL transfer failed — reversing hold", zap.Error(txErr), zap.String("airbills_id", airbillsID))
			s.reverseHold(ctx, userID, airbillsID, totalHold, railFee, "circle_transfer_failed")
			return
		}
		if isFailedState(tx.State) || tx.ID == "" {
			s.reverseHold(ctx, userID, airbillsID, totalHold, railFee, "circle_transfer_"+string(tx.State))
			return
		}
		s.markSent(ctx, airbillsID, "circle:"+tx.ID)
		if !s.awaitCircleComplete(ctx, tx.ID) {
			// Transfer is in-flight but not yet confirmed; leave the order in
			// 'sent' for the recovery worker to process. Funds have moved, so we
			// do NOT reverse the hold here.
			s.logger.Warn("airbills settle: transfer not confirmed within window; deferring to recovery",
				zap.String("airbills_id", airbillsID), zap.String("circle_tx_id", tx.ID))
			return
		}
		s.processAndComplete(ctx, userID, airbillsID, productCode, totalHold, railFee)
		return
	}

	// EVM-held USDC — bridge to Solana via ChainRails, then defer processing to
	// the recovery worker (bridge arrival can't be confirmed via Circle alone).
	if s.chainRails == nil {
		s.logger.Error("airbills settle: USDC on EVM but no ChainRails — reversing hold", zap.String("blockchain", blockchain))
		s.reverseHold(ctx, userID, airbillsID, totalHold, railFee, "no_chainrails_evm")
		return
	}
	s.settleViaChainRails(ctx, userID, walletID, tokenID, blockchain, onChainAddress, airbillsID, depositAddr, totalHold, transferAmount, railFee)
}

// settleViaChainRails bridges EVM USDC to Airbills' Solana deposit address.
func (s *Service) settleViaChainRails(ctx context.Context, userID uuid.UUID, walletID, tokenID, blockchain, onChainAddress, airbillsID, depositAddr string, totalHold, transferAmount, railFee decimal.Decimal) {
	source, ok := evmToChainRails[blockchain]
	if !ok {
		s.logger.Error("airbills settle: unsupported EVM chain for ChainRails bridge", zap.String("blockchain", blockchain))
		s.reverseHold(ctx, userID, airbillsID, totalHold, railFee, "unsupported_chain")
		return
	}
	solDest := "SOLANA_MAINNET"
	if strings.Contains(blockchain, "SEPOLIA") || strings.Contains(blockchain, "FUJI") || strings.Contains(blockchain, "AMOY") || strings.Contains(blockchain, "DEVNET") {
		solDest = "SOLANA_TESTNET"
	}
	amountMicro := transferAmount.Shift(6).IntPart()

	intent, err := s.chainRails.CreateIntent(ctx, &chainrailspkg.CreateIntentRequest{
		Amount:           fmt.Sprintf("%d", amountMicro),
		AmountSymbol:     "USDC",
		TokenIn:          source.token,
		SourceChain:      source.chain,
		DestinationChain: solDest,
		Recipient:        depositAddr,
		Sender:           onChainAddress,
		RefundAddress:    onChainAddress,
		Metadata: map[string]interface{}{
			"airbills_id": airbillsID, "user_id": userID.String(), "type": "airbills_billpay_bridge",
		},
	})
	if err != nil {
		s.logger.Error("airbills settle: ChainRails intent failed — reversing hold", zap.Error(err), zap.String("airbills_id", airbillsID))
		s.reverseHold(ctx, userID, airbillsID, totalHold, railFee, "chainrails_intent_failed")
		return
	}

	circleAmountDecimal := transferAmount
	circleAmount := transferAmount.StringFixed(6)
	if intent.TotalAmountInAssetToken != "" && intent.AssetTokenDecimals > 0 {
		if totalMicro, okParse := new(big.Int).SetString(intent.TotalAmountInAssetToken, 10); okParse {
			circleAmountDecimal = decimal.NewFromBigInt(totalMicro, -int32(intent.AssetTokenDecimals))
			circleAmount = circleAmountDecimal.StringFixed(int32(intent.AssetTokenDecimals))
		}
	}
	if circleAmountDecimal.Add(railFee).GreaterThan(totalHold) {
		s.logger.Error("airbills settle: ChainRails bridge amount exceeds hold — reversing", zap.String("airbills_id", airbillsID))
		s.reverseHold(ctx, userID, airbillsID, totalHold, railFee, "transfer_exceeds_hold")
		return
	}

	idem := uuid.NewSHA1(uuid.NameSpaceOID, []byte("airbills-evm-"+airbillsID)).String()
	tx, txErr := s.circleTransfer.TransferUSDCWithIdempotency(ctx, walletID, tokenID, intent.IntentAddress, circleAmount, idem)
	if txErr != nil {
		s.logger.Error("airbills settle: Circle→ChainRails transfer failed — reversing hold", zap.Error(txErr), zap.String("airbills_id", airbillsID))
		s.reverseHold(ctx, userID, airbillsID, totalHold, railFee, "circle_cr_transfer_failed")
		return
	}
	if isFailedState(tx.State) {
		s.reverseHold(ctx, userID, airbillsID, totalHold, railFee, "circle_cr_"+string(tx.State))
		return
	}
	s.markSent(ctx, airbillsID, fmt.Sprintf("circle-cr:%s:%d", tx.ID, intent.ID))
	s.logger.Info("airbills Circle→ChainRails bridge initiated; deferring process to recovery",
		zap.String("airbills_id", airbillsID), zap.String("circle_tx_id", tx.ID), zap.Int("cr_intent_id", intent.ID))
}

// awaitCircleComplete polls a Circle transfer until it reaches a terminal
// success state or the context deadline. Returns true only on confirmed
// success.
func (s *Service) awaitCircleComplete(ctx context.Context, circleTxID string) bool {
	ticker := time.NewTicker(6 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			tx, err := s.circleTransfer.GetTransaction(ctx, circleTxID)
			if err != nil {
				continue
			}
			switch strings.ToUpper(string(tx.State)) {
			case "COMPLETE", "COMPLETED", "CONFIRMED":
				return true
			case "FAILED", "CANCELLED", "DENIED":
				return false
			}
		}
	}
}

// ProcessAndComplete runs the Airbills fulfilment step and marks the order
// completed. Exported for the recovery worker. Status "06" (already processed)
// is treated as success.
func (s *Service) processAndComplete(ctx context.Context, userID uuid.UUID, airbillsID, productCode string, totalHold, railFee decimal.Decimal) {
	resp, err := s.client.Process(ctx, productCode, airbillsID)
	if err != nil && (resp == nil || !resp.Succeeded()) {
		// The USDC already left the user's wallet. Do NOT reverse the hold — the
		// funds reached Airbills; mark for reconciliation instead.
		s.logger.Error("airbills process failed after funds sent — needs reconciliation",
			zap.Error(err), zap.String("airbills_id", airbillsID))
		s.markFailed(ctx, airbillsID, "process_failed")
		return
	}
	if _, dbErr := s.db.ExecContext(ctx,
		`UPDATE airbills_orders SET status='completed', updated_at=NOW() WHERE airbills_id=$1 AND status IN ('sent','processing','held')`, airbillsID); dbErr != nil {
		s.logger.Warn("failed to mark airbills order completed", zap.Error(dbErr), zap.String("airbills_id", airbillsID))
	}
	s.logger.Info("airbills bill paid", zap.String("airbills_id", airbillsID), zap.String("user_id", userID.String()))
}

// markSent records the settlement transfer reference and advances to 'sent'.
func (s *Service) markSent(ctx context.Context, airbillsID, ref string) {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE airbills_orders SET bridge_transfer_id=$1, status='sent', updated_at=NOW() WHERE airbills_id=$2`, ref, airbillsID); err != nil {
		s.logger.Warn("failed to mark airbills order sent", zap.Error(err), zap.String("airbills_id", airbillsID))
	}
}

// markFailed marks an order failed without reversing (funds already sent).
func (s *Service) markFailed(ctx context.Context, airbillsID, reason string) {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE airbills_orders SET status='failed', failure_reason=$1, updated_at=NOW() WHERE airbills_id=$2`, reason, airbillsID); err != nil {
		s.logger.Warn("failed to mark airbills order failed", zap.Error(err), zap.String("airbills_id", airbillsID))
	}
}

// reverseHold reverses the full ledger hold for a payment that never sent funds
// and marks the order reversed. deposit_id is claimed to prevent double
// reversal by the recovery worker.
func (s *Service) reverseHold(ctx context.Context, userID uuid.UUID, airbillsID string, amount, railFee decimal.Decimal, reason string) error {
	res, dbErr := s.db.ExecContext(ctx, `
		UPDATE airbills_orders SET status='reversed', deposit_id=gen_random_uuid(), failure_reason=$2, updated_at=NOW()
		WHERE airbills_id=$1 AND status IN ('created','held','processing')`, airbillsID, reason)
	if dbErr != nil {
		s.logger.Error("failed to mark airbills order reversed", zap.Error(dbErr), zap.String("airbills_id", airbillsID))
		return dbErr
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil // already claimed or terminal
	}
	if s.ledger == nil {
		return nil
	}
	if err := s.ledger.ReverseTransaction(ctx, userID, entities.AccountTypeSpendingBalance,
		uuid.New().String(), amount, map[string]interface{}{
			"provider": "airbills", "type": "billpay_" + reason + "_reversal", "airbills_id": airbillsID,
			"rail_fee": railFee.String(), "fee_revenue_posted": true,
		}); err != nil {
		s.logger.Error("CRITICAL: failed to reverse airbills hold", zap.Error(err), zap.String("airbills_id", airbillsID))
		if _, unclaimErr := s.db.ExecContext(ctx,
			`UPDATE airbills_orders SET deposit_id=NULL, status='processing', updated_at=NOW() WHERE airbills_id=$1 AND status='reversed'`, airbillsID); unclaimErr != nil {
			return fmt.Errorf("reversal failed: %w; unclaim also failed: %v", err, unclaimErr)
		}
		return err
	}
	return nil
}

func isFailedState(state interface{}) bool {
	s := strings.ToUpper(fmt.Sprintf("%v", state))
	return s == "DENIED" || s == "FAILED" || s == "CANCELLED"
}

// RunRecovery reconciles stuck bill-pay orders. It is safe to call repeatedly
// from a background worker:
//   - 'held'/'processing' orders past abandonAge with no transfer started are
//     reversed (funds never left the wallet).
//   - 'sent' orders funded by a direct Circle transfer (bridge_transfer_id
//     "circle:<id>") are checked against Circle's terminal state: COMPLETE runs
//     the Airbills fulfilment step; a confirmed FAILED reverses the hold.
//   - 'sent' orders funded via a ChainRails bridge ("circle-cr:...") past
//     bridgeProcessAge attempt the Airbills fulfilment step (the bridge has had
//     time to arrive); on success they complete, otherwise they are left for the
//     next sweep or manual review (funds already left — never auto-reversed).
func (s *Service) RunRecovery(ctx context.Context) {
	const (
		abandonAge       = 15 * time.Minute
		sentReconcileAge = 3 * time.Minute
		bridgeProcessAge = 5 * time.Minute
	)
	s.reverseAbandoned(ctx, abandonAge)
	s.reconcileSent(ctx, sentReconcileAge, bridgeProcessAge)
}

func (s *Service) reverseAbandoned(ctx context.Context, age time.Duration) {
	if s.ledger == nil {
		return
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT airbills_id, user_id, COALESCE(hold_amount,0), COALESCE(rail_fee_usdc,0)
		FROM airbills_orders
		WHERE status IN ('held','processing') AND bridge_transfer_id IS NULL AND deposit_id IS NULL
		  AND created_at < NOW() - make_interval(secs => $1) LIMIT 25`, int(age.Seconds()))
	if err != nil {
		s.logger.Error("airbills recovery: abandoned query failed", zap.Error(err))
		return
	}
	defer rows.Close()
	type item struct {
		airbillsID string
		userID     uuid.UUID
		hold, fee  decimal.Decimal
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.airbillsID, &it.userID, &it.hold, &it.fee); err != nil {
			continue
		}
		items = append(items, it)
	}
	for _, it := range items {
		if it.hold.IsPositive() {
			_ = s.reverseHold(ctx, it.userID, it.airbillsID, it.hold, it.fee, "abandoned_recovery")
		}
	}
}

func (s *Service) reconcileSent(ctx context.Context, directAge, bridgeAge time.Duration) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT airbills_id, user_id, product_code, bridge_transfer_id, COALESCE(hold_amount,0), COALESCE(rail_fee_usdc,0), created_at
		FROM airbills_orders
		WHERE status = 'sent' AND bridge_transfer_id IS NOT NULL
		  AND created_at < NOW() - make_interval(secs => $1) LIMIT 25`, int(directAge.Seconds()))
	if err != nil {
		s.logger.Error("airbills recovery: sent query failed", zap.Error(err))
		return
	}
	defer rows.Close()
	type item struct {
		airbillsID, productCode, ref string
		userID                       uuid.UUID
		hold, fee                    decimal.Decimal
		createdAt                    time.Time
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.airbillsID, &it.userID, &it.productCode, &it.ref, &it.hold, &it.fee, &it.createdAt); err != nil {
			continue
		}
		items = append(items, it)
	}
	for _, it := range items {
		switch {
		case strings.HasPrefix(it.ref, "circle:"):
			circleTxID := strings.TrimPrefix(it.ref, "circle:")
			tx, terr := s.circleTransfer.GetTransaction(ctx, circleTxID)
			if terr != nil || tx == nil {
				continue
			}
			state := strings.ToUpper(string(tx.State))
			switch {
			case state == "COMPLETE" || state == "COMPLETED" || state == "CONFIRMED":
				s.processAndComplete(ctx, it.userID, it.airbillsID, it.productCode, it.hold, it.fee)
			case isFailedState(tx.State):
				_ = s.reverseHold(ctx, it.userID, it.airbillsID, it.hold, it.fee, "circle_confirmed_failure_recovery")
			}
		case strings.HasPrefix(it.ref, "circle-cr:"):
			// ChainRails bridge: only attempt fulfilment once the bridge has had
			// time to arrive, so we don't process before funds land.
			if time.Since(it.createdAt) < bridgeAge {
				continue
			}
			s.processAndComplete(ctx, it.userID, it.airbillsID, it.productCode, it.hold, it.fee)
		}
	}
}
