package handlers

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/mr-tron/base58"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/chainrouting"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/chainrails"
	circlepkg "github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
)

// This file implements an operator-driven, OFF-LEDGER crypto bridge: money that
// arrived in a Rail-managed Circle wallet but never reached the ledger is moved
// straight to the user's destination wallet via ChainRails, and Rail's books
// never touch it. No ledger transaction, no balance change, no withdrawal row.
//
// It exists because a user who cannot authenticate has no withdrawal path, and
// crediting the ledger only to withdraw it again is more moving parts for the
// same outcome. The trade is that the payout itself must carry the safety
// properties the ledger would otherwise provide:
//
//   - One payout per source transfer, forever (unique index). A retry resumes
//     the same row; it never opens a second path to the same money.
//   - A suppression deposit row (status 'bridged') is written before anything
//     moves, so the Circle webhook or a backfill cannot credit the same
//     transaction afterwards.
//   - The intent address and exact funding amount are persisted before the
//     Circle transfer, and the transfer uses an idempotency key derived from
//     the payout id, so retries dedupe against the original transfer.
//   - dry_run defaults to true.

// CryptoPayoutStore is the persistence surface the bridge needs.
type CryptoPayoutStore interface {
	CreatePayoutWithSuppression(ctx context.Context, payout *entities.OpsCryptoPayout) (bool, error)
	GetPayoutBySourceTxID(ctx context.Context, sourceTxID string) (*entities.OpsCryptoPayout, error)
	GetPayoutByID(ctx context.Context, id uuid.UUID) (*entities.OpsCryptoPayout, error)
	ClaimForIntent(ctx context.Context, id uuid.UUID) (bool, error)
	ResetFailedForRetry(ctx context.Context, id uuid.UUID, amount decimal.Decimal, destination, reason, actor string) error
	MarkIntentCreated(ctx context.Context, id uuid.UUID, intentID int, intentAddress string, fundingAmount, feeAmount decimal.Decimal) error
	MarkFunding(ctx context.Context, id uuid.UUID, transferID string) error
	MarkFunded(ctx context.Context, id uuid.UUID, transferID, txHash string) error
	MarkFundingUncertain(ctx context.Context, id uuid.UUID, message string) error
	MarkCompleted(ctx context.Context, id uuid.UUID, txHash string) error
	FailPayout(ctx context.Context, id uuid.UUID, message string) error
}

// CryptoBridgeCircle is the Circle surface needed to move and verify funds.
type CryptoBridgeCircle interface {
	TransferUSDCWithIdempotency(ctx context.Context, walletID, tokenID, destinationAddress, amount, idempotencyKey string) (*circlepkg.Transaction, error)
	GetTransaction(ctx context.Context, txID string) (*circlepkg.Transaction, error)
	GetTokenSymbol(ctx context.Context, walletID, tokenID string) (string, error)
	GetUSDCTokenIDOnchain(ctx context.Context, walletID string) (string, error)
	GetTokenBalanceOnchain(ctx context.Context, walletID string) ([]circlepkg.TokenBalance, error)
}

// CryptoBridgeChainRails is the ChainRails surface needed to bridge.
type CryptoBridgeChainRails interface {
	CreateIntent(ctx context.Context, req *chainrails.CreateIntentRequest) (*chainrails.CreateIntentResponse, error)
	GetIntentStatus(ctx context.Context, intentAddress string) (*chainrails.IntentStatus, error)
	TriggerIntentProcessing(ctx context.Context, intentAddress string) error
}

const (
	maxOpsBridgeActorLength  = 120
	maxOpsBridgeReasonLength = 280
	// opsBridgeMaxAmount caps one bridge so a leaked internal key cannot drain
	// a wallet in a single call. Mirrors the admin recovery cap.
	opsBridgeMaxAmount = 10000
	// circleTransferPolls is ~45s at 3s intervals. A gateway timeout is worse
	// than an honest 202: a retry resumes with the same idempotency key.
	circleTransferPolls = 15
)

var opsBridgeMaxAmountDec = decimal.NewFromInt(opsBridgeMaxAmount)

// CryptoBridgeHandlers moves money out of a Rail-managed wallet without
// touching the ledger. See the file comment for the safety contract.
type CryptoBridgeHandlers struct {
	ops        InternalOpsStore
	payouts    CryptoPayoutStore
	circle     CryptoBridgeCircle
	chainrails CryptoBridgeChainRails
	logger     *zap.Logger
}

// NewCryptoBridgeHandlers builds the off-ledger bridge handlers.
func NewCryptoBridgeHandlers(ops InternalOpsStore, payouts CryptoPayoutStore, circle CryptoBridgeCircle, chainrails CryptoBridgeChainRails, logger *zap.Logger) *CryptoBridgeHandlers {
	return &CryptoBridgeHandlers{
		ops:        ops,
		payouts:    payouts,
		circle:     circle,
		chainrails: chainrails,
		logger:     logger,
	}
}

// CryptoBridgeRequest is the payload for POST /internal/crypto/bridge.
type CryptoBridgeRequest struct {
	UserID string `json:"user_id" binding:"required"`
	// SourceChain optionally pins which wallet the money leaves from. Defaults
	// to the user's non-Solana wallet holding the most USDC, which is the only
	// useful choice when the chain is obvious from the funds.
	SourceChain string `json:"source_chain,omitempty"`
	// SourceCircleTxID is the inbound transfer being settled. Required: it is
	// the anchor for both idempotency and the suppression record.
	SourceCircleTxID string `json:"source_circle_tx_id" binding:"required"`
	// Amount is what the recipient receives, as a decimal string. Omit it (or
	// send "all") to bridge as much of the source transfer as ChainRails fees
	// allow.
	Amount string `json:"amount,omitempty"`
	// All explicitly requests the maximum bridgeable amount.
	All bool `json:"all,omitempty"`
	// DestinationAddress defaults to the user's Solana wallet.
	DestinationAddress string `json:"destination_address,omitempty"`
	Reason             string `json:"reason" binding:"required"`
	// DryRun defaults to TRUE.
	DryRun *bool `json:"dry_run,omitempty"`
}

// Bridge handles POST /internal/crypto/bridge.
func (h *CryptoBridgeHandlers) Bridge(c *gin.Context) {
	actor := strings.TrimSpace(c.GetHeader("X-Internal-Actor"))
	if actor == "" || len(actor) > maxOpsBridgeActorLength {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("X-Internal-Actor header is required (1-%d chars) so the payout is attributable", maxOpsBridgeActorLength),
		})
		return
	}

	var req CryptoBridgeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}

	reason := strings.TrimSpace(req.Reason)
	if reason == "" || len(reason) > maxOpsBridgeReasonLength {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("a reason (1-%d chars) is required for an off-ledger payout", maxOpsBridgeReasonLength),
		})
		return
	}

	userID, err := uuid.Parse(strings.TrimSpace(req.UserID))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user_id"})
		return
	}

	sourceTxID := strings.TrimSpace(req.SourceCircleTxID)
	if sourceTxID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "source_circle_tx_id is required: it is the suppression anchor"})
		return
	}

	if h.circle == nil || h.chainrails == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "crypto bridging is not configured (Circle and ChainRails are both required)",
		})
		return
	}

	dryRun := true
	if req.DryRun != nil {
		dryRun = *req.DryRun
	}

	ctx := c.Request.Context()

	// --- Verify the source transfer is real, settled, and owned by this user.

	wallets, err := h.ops.WalletsForUser(ctx, userID)
	if err != nil {
		h.logger.Error("crypto-bridge: wallet lookup failed", zap.Error(err), zap.String("user_id", userID.String()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "wallet lookup failed"})
		return
	}

	tx, err := h.circle.GetTransaction(ctx, sourceTxID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "circle transaction lookup failed: " + err.Error()})
		return
	}
	if tx == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "circle returned no transaction for source_circle_tx_id"})
		return
	}

	wallet, owned := walletForTx(wallets, tx)
	if !owned {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "the Circle transaction does not belong to any wallet of user " + userID.String(),
		})
		return
	}

	inboundProof := ""
	switch {
	case tx.DestinationAddress != "" && strings.EqualFold(strings.TrimSpace(tx.DestinationAddress), strings.TrimSpace(wallet.Address)):
		inboundProof = "destination_address"
	case tx.DestinationAddress == "" && wallet.CircleWalletID != "" &&
		strings.EqualFold(strings.TrimSpace(tx.WalletID), strings.TrimSpace(wallet.CircleWalletID)):
		inboundProof = "wallet_id"
	default:
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "the Circle transaction is not an inbound transfer to this wallet (destination " +
				tx.DestinationAddress + " does not match " + wallet.Address + ")",
		})
		return
	}

	// Unlike the read-only backfill, this moves money: require an explicitly
	// settled state rather than treating an empty one as good enough.
	if !isTransferComplete(string(tx.State)) {
		c.JSON(http.StatusConflict, gin.H{
			"error": "the source transfer is not settled (state " + string(tx.State) + "); nothing can be bridged yet",
		})
		return
	}
	if strings.TrimSpace(tx.TxHash) == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "the source transfer has no on-chain hash; it cannot anchor the suppression record",
		})
		return
	}

	// Only USDC is bridgeable. If the token cannot be resolved, continue: the
	// funds actually moved are the wallet's USDC, and the suppression record
	// is harmless for a non-USDC deposit (the ledger credit path is USDC-only
	// for this flow anyway).
	sourceToken := "USDC"
	if symbol, err := h.circle.GetTokenSymbol(ctx, wallet.CircleWalletID, tx.TokenID); err == nil && symbol != "" {
		sourceToken = strings.ToUpper(symbol)
	}
	if sourceToken != "USDC" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "the source transfer is " + sourceToken + "; only USDC can be bridged",
		})
		return
	}

	if req.SourceChain != "" {
		want := chainrouting.WalletChainFromCircleChain(strings.ToUpper(strings.TrimSpace(req.SourceChain)))
		if want == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported source_chain: " + req.SourceChain})
			return
		}
		if !strings.EqualFold(string(want), wallet.Chain) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": fmt.Sprintf("source_circle_tx_id is on %s, not the requested source_chain %s", wallet.Chain, req.SourceChain),
			})
			return
		}
	}

	sourceChain := chainrouting.WalletChainFromCircleChain(wallet.Chain)
	crSource := chainrouting.CircleChainToChainRailsChain(wallet.Chain)
	if sourceChain == "" || crSource == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no ChainRails route for source chain " + wallet.Chain})
		return
	}
	if wallet.CircleWalletID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "the source wallet has no Circle wallet id; this path only moves Circle custody funds"})
		return
	}

	tokenIn := chainrouting.USDCTokenForChainRailsChain(crSource)
	if tokenIn == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no USDC token address for ChainRails chain " + crSource})
		return
	}

	crDest := "SOLANA_MAINNET"
	if chainrails.IsTestnetChain(crSource) {
		crDest = "SOLANA_TESTNET"
	}

	destination := strings.TrimSpace(req.DestinationAddress)
	if destination == "" {
		destination = solanaWalletAddress(wallets)
	}
	if err := validateSolanaDestination(destination); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	sourceAmount, err := decimal.NewFromString(firstAmount(tx.Amounts))
	if err != nil || !sourceAmount.GreaterThan(decimal.Zero) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "the source transfer has no usable amount"})
		return
	}

	available, err := h.onchainUSDC(ctx, wallet.CircleWalletID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "could not read the source wallet balance: " + err.Error()})
		return
	}

	// Never bridge more than the transfer being settled. If the wallet holds
	// other money, that money belongs to other transfers with their own
	// suppression needs.
	bridgeable := available
	if sourceAmount.LessThan(bridgeable) {
		bridgeable = sourceAmount
	}

	all := req.All || strings.EqualFold(strings.TrimSpace(req.Amount), "all")
	var requested decimal.Decimal
	if !all {
		requested, err = decimal.NewFromString(strings.TrimSpace(req.Amount))
		if err != nil || !requested.GreaterThan(decimal.Zero) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "amount must be a positive decimal string, or omit it for the maximum bridgeable amount"})
			return
		}
		requested = requested.Truncate(6)
		if requested.GreaterThan(opsBridgeMaxAmountDec) {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("amount exceeds the bridge cap of $%s", opsBridgeMaxAmountDec.String())})
			return
		}
		if requested.GreaterThan(sourceAmount) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": fmt.Sprintf("amount %s exceeds the source transfer amount %s", requested.StringFixed(6), sourceAmount.StringFixed(6)),
			})
			return
		}
		if requested.GreaterThan(available) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": fmt.Sprintf("amount %s exceeds the on-chain balance %s", requested.StringFixed(6), available.StringFixed(6)),
			})
			return
		}
	}

	if all && !bridgeable.GreaterThan(decimal.Zero) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "nothing to bridge: the source wallet holds no USDC"})
		return
	}

	// --- Dry run: report the plan. Nothing is written, no provider object is
	// created, no money moves.

	if dryRun {
		existing, err := h.payouts.GetPayoutBySourceTxID(ctx, sourceTxID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "payout lookup failed: " + err.Error()})
			return
		}
		resp := gin.H{
			"dry_run":                 true,
			"would_bridge":            true,
			"ledger_touched":          false,
			"user_id":                 userID.String(),
			"source_chain":            wallet.Chain,
			"source_address":          wallet.Address,
			"source_circle_wallet_id": wallet.CircleWalletID,
			"source_circle_tx_id":     sourceTxID,
			"source_tx_hash":          tx.TxHash,
			"source_transfer_amount":  sourceAmount.StringFixed(6),
			"source_token":            sourceToken,
			"inbound_verified_by":     inboundProof,
			"onchain_balance":         available.StringFixed(6),
			"max_bridgeable":          bridgeable.StringFixed(6),
			"destination_chain":       crDest,
			"destination_address":     destination,
			"chainrails_source_chain": crSource,
			"note": "ChainRails fees are added on top of the requested amount and are only known once " +
				"an intent is created; omit amount (or send \"all\") to size the transfer to fit the balance.",
			"suppression": gin.H{
				"would_write":       true,
				"deposit_status":    "bridged",
				"source_tx_hash":    tx.TxHash,
				"blocks":            "Circle webhook credit and /internal/deposits/backfill for this transaction",
				"released_on_fail":  true,
				"ledger_entry_made": false,
			},
		}
		if all {
			resp["amount"] = "all (sized at intent creation)"
			resp["amount_mode"] = "all"
		} else {
			resp["amount"] = requested.StringFixed(6)
			resp["amount_mode"] = "explicit"
		}
		if existing != nil {
			resp["existing_payout"] = payoutJSON(existing)
		}
		c.JSON(http.StatusOK, resp)
		return
	}

	// --- Live path.

	payout, err := h.ensurePayout(ctx, payoutParams{
		userID:       userID,
		wallet:       wallet,
		sourceTx:     tx,
		sourceAmount: sourceAmount,
		amount:       requested,
		destination:  destination,
		crDest:       crDest,
		reason:       reason,
		actor:        actor,
	})
	if err != nil {
		switch {
		case errors.Is(err, errPayoutAlreadyDone):
			c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "payout": payoutJSON(payout)})
		case errors.Is(err, errPayoutInProgress):
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		case errors.Is(err, entities.ErrOpsPayoutSuppressionConflict):
			c.JSON(http.StatusConflict, gin.H{
				"error": err.Error() + "; resolve the existing deposit row before bridging off-ledger",
			})
		default:
			h.logger.Error("crypto-bridge: payout setup failed",
				zap.Error(err), zap.String("user_id", userID.String()), zap.String("source_tx_id", sourceTxID))
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	// Resume branch: the intent is already recorded. Fund the SAME intent with
	// the persisted amount and the per-intent idempotency key.
	intentAddress := ""
	fundingAmount := decimal.Zero
	if payout.IntentAddress != nil && *payout.IntentAddress != "" && payout.FundingAmount != nil {
		intentAddress = *payout.IntentAddress
		fundingAmount = *payout.FundingAmount

		// Only check the balance when no funding attempt has happened yet. After
		// funding_uncertain the money may already be gone, so a lower balance is
		// expected and must not release the suppression.
		if payout.Status == entities.OpsPayoutStatusIntentCreated && fundingAmount.GreaterThan(available) {
			h.failForShortfall(c, payout, available, fundingAmount)
			return
		}
	} else {
		claimed, err := h.payouts.ClaimForIntent(ctx, payout.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if !claimed {
			c.JSON(http.StatusConflict, gin.H{
				"error":  "another request is creating the ChainRails intent for this payout; retry in a few minutes",
				"payout": payoutJSON(payout),
			})
			return
		}

		base := chainrails.CreateIntentRequest{
			Sender:           wallet.Address,
			AmountSymbol:     "USDC",
			TokenIn:          tokenIn,
			SourceChain:      crSource,
			DestinationChain: crDest,
			Recipient:        destination,
			RefundAddress:    wallet.Address,
			Metadata: map[string]interface{}{
				"type":      "ops_crypto_payout",
				"payout_id": payout.ID.String(),
				"user_id":   userID.String(),
				"reason":    reason,
			},
		}

		intent, recipient, funding, err := h.createIntent(ctx, base, all, requested, bridgeable)
		if err != nil {
			h.recordPayoutFailure(ctx, payout, err)
			c.JSON(http.StatusBadGateway, gin.H{
				"error":          "chainrails intent creation failed: " + err.Error(),
				"payout_id":      payout.ID.String(),
				"status":         payout.Status,
				"ledger_touched": false,
			})
			return
		}
		if !recipient.GreaterThan(decimal.Zero) {
			h.recordPayoutFailure(ctx, payout, fmt.Errorf("derived recipient amount is not positive"))
			c.JSON(http.StatusBadRequest, gin.H{"error": "could not derive a positive amount to bridge", "payout_id": payout.ID.String()})
			return
		}
		if funding.GreaterThan(available) {
			// Definitive pre-transfer failure: release the suppression so the
			// normal credit path is still available.
			msg := fmt.Sprintf("intent %d needs %s USDC to fund but the wallet holds %s", intent.ID, funding.StringFixed(6), available.StringFixed(6))
			if err := h.payouts.FailPayout(ctx, payout.ID, msg); err != nil {
				h.logger.Error("crypto-bridge: failed to release a shortfall payout", zap.Error(err), zap.String("payout_id", payout.ID.String()))
			}
			c.JSON(http.StatusBadRequest, gin.H{
				"error":           msg,
				"payout_id":       payout.ID.String(),
				"status":          "failed",
				"onchain_balance": available.StringFixed(6),
				"funding_amount":  funding.StringFixed(6),
				"fee_amount":      funding.Sub(recipient).StringFixed(6),
				"ledger_touched":  false,
				"retry_hint":      "omit amount (or send \"all\") to size the transfer to the balance",
			})
			return
		}

		if err := h.payouts.MarkIntentCreated(ctx, payout.ID, intent.ID, intent.IntentAddress, funding, funding.Sub(recipient)); err != nil {
			// Nothing has moved; release the suppression so the fallback works.
			h.recordPayoutFailure(ctx, payout, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not persist the ChainRails intent: " + err.Error()})
			return
		}
		intentAddress = intent.IntentAddress
		fundingAmount = funding
		feeAmount := funding.Sub(recipient)
		payout.Amount = recipient
		payout.FundingAmount = &funding
		payout.FeeAmount = &feeAmount
		payout.IntentAddress = &intent.IntentAddress
		payout.ChainRailsIntentID = &intent.ID
		payout.Status = entities.OpsPayoutStatusIntentCreated

		h.logger.Warn("CRYPTO BRIDGE intent created",
			zap.String("payout_id", payout.ID.String()),
			zap.String("actor", actor),
			zap.String("user_id", userID.String()),
			zap.Int("intent_id", intent.ID),
			zap.String("intent_address", intent.IntentAddress),
			zap.String("amount", recipient.StringFixed(6)),
			zap.String("funding_amount", funding.StringFixed(6)),
			zap.String("mode", "off-ledger"))
	}

	// --- Fund the intent.

	if fundingAmount.GreaterThan(available) && payout.Status == entities.OpsPayoutStatusIntentCreated {
		h.failForShortfall(c, payout, available, fundingAmount)
		return
	}

	tokenID, err := h.circle.GetUSDCTokenIDOnchain(ctx, wallet.CircleWalletID)
	if err != nil {
		h.recordPayoutFailure(ctx, payout, err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "could not resolve the USDC token id: " + err.Error()})
		return
	}

	// Record the attempt BEFORE the call: a crash must leave the row in an
	// in-flight state, never in a state that looks safe to retry from scratch.
	if err := h.payouts.MarkFunding(ctx, payout.ID, ""); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	idemKey := uuid.NewSHA1(uuid.NameSpaceOID,
		[]byte("ops-crypto-payout-fund-"+payout.ID.String()+"-"+intentAddress)).String()

	transfer, err := h.circle.TransferUSDCWithIdempotency(ctx, wallet.CircleWalletID, tokenID, intentAddress, fundingAmount.StringFixed(6), idemKey)
	if err != nil {
		// The transfer may or may not have been created. Keep suppression and
		// tell the operator to re-run the SAME request: the idempotency key
		// makes the retry resume rather than duplicate.
		msg := "circle transfer outcome unknown: " + err.Error()
		if markErr := h.payouts.MarkFundingUncertain(ctx, payout.ID, msg); markErr != nil {
			h.logger.Error("crypto-bridge: failed to record uncertain transfer", zap.Error(markErr), zap.String("payout_id", payout.ID.String()))
		}
		h.logger.Error("CRYPTO BRIDGE transfer ambiguous",
			zap.String("payout_id", payout.ID.String()),
			zap.String("intent_address", intentAddress),
			zap.String("idempotency_key", idemKey),
			zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{
			"error":          msg,
			"payout_id":      payout.ID.String(),
			"status":         entities.OpsPayoutStatusFundingUncertain,
			"idempotent_key": idemKey,
			"retry_hint":     "re-run the same request; the same idempotency key resumes the original transfer instead of creating a second one",
			"ledger_touched": false,
		})
		return
	}

	if transfer != nil && transfer.ID != "" {
		if err := h.payouts.MarkFunding(ctx, payout.ID, transfer.ID); err != nil {
			h.logger.Warn("crypto-bridge: could not store transfer id", zap.Error(err), zap.String("payout_id", payout.ID.String()))
		}
	}

	final, waitErr := h.waitCircleTransfer(ctx, transfer)
	if waitErr != nil {
		if errors.Is(waitErr, errCircleTransferTerminal) {
			// A denied/failed/cancelled transfer is definitive: no money moved.
			msg := waitErr.Error()
			if err := h.payouts.FailPayout(ctx, payout.ID, msg); err != nil {
				h.logger.Error("crypto-bridge: failed to release a terminal payout", zap.Error(err), zap.String("payout_id", payout.ID.String()))
			}
			h.logger.Error("CRYPTO BRIDGE transfer failed",
				zap.String("payout_id", payout.ID.String()),
				zap.String("intent_address", intentAddress),
				zap.String("error", msg))
			c.JSON(http.StatusBadGateway, gin.H{
				"error":      msg,
				"payout_id":  payout.ID.String(),
				"status":     entities.OpsPayoutStatusFailed,
				"retry_hint": "the transfer was rejected before moving funds; a retry creates a fresh intent",
			})
			return
		}
		// Timeout: money may be in flight.
		if err := h.payouts.MarkFundingUncertain(ctx, payout.ID, waitErr.Error()); err != nil {
			h.logger.Warn("crypto-bridge: could not record transfer timeout", zap.Error(err))
		}
		h.logger.Warn("CRYPTO BRIDGE transfer still pending",
			zap.String("payout_id", payout.ID.String()),
			zap.String("intent_address", intentAddress),
			zap.Error(waitErr))
		c.JSON(http.StatusAccepted, gin.H{
			"payout_id":      payout.ID.String(),
			"status":         entities.OpsPayoutStatusFundingUncertain,
			"intent_address": intentAddress,
			"error":          "the Circle transfer has not confirmed yet: " + waitErr.Error(),
			"retry_hint":     "re-run the same request; the same idempotency key resumes the original transfer",
			"ledger_touched": false,
		})
		return
	}

	txHash := ""
	if final != nil {
		txHash = final.TxHash
	}
	if err := h.payouts.MarkFunded(ctx, payout.ID, transferIDOf(final, transfer), txHash); err != nil {
		h.logger.Warn("crypto-bridge: could not mark funded", zap.Error(err), zap.String("payout_id", payout.ID.String()))
	}
	payout.Status = entities.OpsPayoutStatusFunded
	payout.CircleTxHash = &txHash

	// Testnets have no ChainRails indexer; mainnet is a no-op.
	chainrails.MaybeTriggerTestnetProcessing(ctx, h.chainrails, crSource, intentAddress, h.logger)

	// One best-effort status read: delivery itself is asynchronous, so this is
	// informational unless ChainRails already reports a terminal success.
	intentStatus := ""
	if status, err := h.chainrails.GetIntentStatus(ctx, intentAddress); err == nil && status != nil {
		intentStatus = status.Status
		if chainrails.IsTerminalSuccess(status.Status) {
			if err := h.payouts.MarkCompleted(ctx, payout.ID, status.TxHash); err == nil {
				payout.Status = entities.OpsPayoutStatusCompleted
			}
		} else if chainrails.IsTerminalFailure(status.Status) {
			h.logger.Error("CRYPTO BRIDGE intent failed after funding — suppression stays held",
				zap.String("payout_id", payout.ID.String()),
				zap.String("intent_address", intentAddress),
				zap.String("intent_status", status.Status))
		}
	}

	h.logger.Warn("CRYPTO BRIDGE funded",
		zap.String("payout_id", payout.ID.String()),
		zap.String("actor", actor),
		zap.String("user_id", userID.String()),
		zap.String("source_chain", wallet.Chain),
		zap.String("destination", destination),
		zap.String("amount", payout.Amount.StringFixed(6)),
		zap.String("funding_amount", fundingAmount.StringFixed(6)),
		zap.String("intent_address", intentAddress),
		zap.String("circle_transfer_id", transferIDOf(final, transfer)),
		zap.String("circle_tx_hash", txHash),
		zap.String("reason", reason),
		zap.String("caller_ip", c.ClientIP()))

	resp := payoutJSON(payout)
	resp["dry_run"] = false
	resp["intent_status"] = intentStatus
	resp["onchain_balance_before"] = available.StringFixed(6)
	resp["note"] = "funds are on their way to the destination chain; Rail's ledger was not touched. " +
		"Poll GET /internal/crypto/bridge/:id for the settlement status."
	c.JSON(http.StatusOK, resp)
}

// GetPayout handles GET /internal/crypto/bridge/:id: row plus live ChainRails
// status, so an operator can see whether the bridge actually settled.
func (h *CryptoBridgeHandlers) GetPayout(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payout id"})
		return
	}
	payout, err := h.payouts.GetPayoutByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if payout == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "payout not found"})
		return
	}

	resp := payoutJSON(payout)
	if payout.IntentAddress != nil && *payout.IntentAddress != "" && h.chainrails != nil {
		if status, err := h.chainrails.GetIntentStatus(c.Request.Context(), *payout.IntentAddress); err == nil && status != nil {
			resp["intent_status"] = status.Status
			resp["intent_tx_hash"] = status.TxHash
			if chainrails.IsTerminalSuccess(status.Status) {
				if err := h.payouts.MarkCompleted(c.Request.Context(), payout.ID, status.TxHash); err == nil {
					resp["status"] = entities.OpsPayoutStatusCompleted
				}
			}
		}
	}
	c.JSON(http.StatusOK, resp)
}

// --- internals ---

var (
	errPayoutAlreadyDone      = errors.New("this source transaction has already been bridged")
	errPayoutInProgress       = errors.New("this payout is mid-flight")
	errCircleTransferTerminal = errors.New("circle transfer reached a terminal failure state")
)

type payoutParams struct {
	userID       uuid.UUID
	wallet       opsWallet
	sourceTx     *circlepkg.Transaction
	sourceAmount decimal.Decimal
	amount       decimal.Decimal
	destination  string
	crDest       string
	reason       string
	actor        string
}

// ensurePayout returns a payout row that is safe to continue from, creating it
// (with suppression) on first use. It never starts a second path to the same
// source transaction.
func (h *CryptoBridgeHandlers) ensurePayout(ctx context.Context, p payoutParams) (*entities.OpsCryptoPayout, error) {
	existing, err := h.payouts.GetPayoutBySourceTxID(ctx, p.sourceTx.ID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		amount := p.amount
		if !amount.GreaterThan(decimal.Zero) {
			// "all" mode: the recipient amount is only known after the intent
			// quote. The row is anchored to the source transfer amount until
			// MarkIntentCreated replaces it with the exact figure.
			amount = p.sourceAmount
		}
		now := time.Now().UTC()
		payout := &entities.OpsCryptoPayout{
			ID:                   uuid.New(),
			UserID:               p.userID,
			SourceChain:          p.wallet.Chain,
			SourceCircleWalletID: p.wallet.CircleWalletID,
			SourceAddress:        p.wallet.Address,
			SourceCircleTxID:     p.sourceTx.ID,
			SourceTxHash:         p.sourceTx.TxHash,
			DestinationChain:     p.crDest,
			DestinationAddress:   p.destination,
			Amount:               amount.Truncate(6),
			Status:               entities.OpsPayoutStatusCreated,
			Reason:               p.reason,
			Actor:                p.actor,
			CreatedAt:            now,
			UpdatedAt:            now,
		}
		if walletID, err := uuid.Parse(p.wallet.ID); err == nil {
			payout.SourceWalletID = &walletID
		}

		created, err := h.payouts.CreatePayoutWithSuppression(ctx, payout)
		if err != nil {
			return nil, err
		}
		if created {
			h.logger.Warn("CRYPTO BRIDGE suppression written",
				zap.String("payout_id", payout.ID.String()),
				zap.String("user_id", p.userID.String()),
				zap.String("source_tx_hash", p.sourceTx.TxHash),
				zap.String("source_chain", p.wallet.Chain),
				zap.String("deposit_status", "bridged"),
				zap.String("actor", p.actor))
			return payout, nil
		}

		// Lost a race with a concurrent request: continue from that row.
		existing, err = h.payouts.GetPayoutBySourceTxID(ctx, p.sourceTx.ID)
		if err != nil {
			return nil, err
		}
		if existing == nil {
			return nil, fmt.Errorf("payout row disappeared after a create conflict")
		}
	}

	switch existing.Status {
	case entities.OpsPayoutStatusFunded, entities.OpsPayoutStatusCompleted:
		return existing, fmt.Errorf("%w (status %s)", errPayoutAlreadyDone, existing.Status)
	case entities.OpsPayoutStatusIntentCreating:
		return existing, errPayoutInProgress
	case entities.OpsPayoutStatusFailed:
		amount := p.amount
		if !amount.GreaterThan(decimal.Zero) {
			amount = p.sourceAmount
		}
		if err := h.payouts.ResetFailedForRetry(ctx, existing.ID, amount.Truncate(6), p.destination, p.reason, p.actor); err != nil {
			return existing, err
		}
		return h.payouts.GetPayoutByID(ctx, existing.ID)
	default:
		return existing, nil
	}
}

// createIntent creates the ChainRails intent. In "all" mode it iterates quotes
// until the exact funding amount (recipient + fees) fits the balance, because
// fees are only known from a quote. Abandoned quotes are left to expire; none
// of them hold money.
func (h *CryptoBridgeHandlers) createIntent(ctx context.Context, base chainrails.CreateIntentRequest, all bool, requested, bridgeable decimal.Decimal) (*chainrails.CreateIntentResponse, decimal.Decimal, decimal.Decimal, error) {
	if !all {
		base.Amount = requested.Truncate(6).Shift(6).BigInt().String()
		intent, err := h.chainrails.CreateIntent(ctx, &base)
		if err != nil {
			return nil, decimal.Zero, decimal.Zero, err
		}
		funding, err := intentFundingAmount(intent)
		if err != nil {
			return nil, decimal.Zero, decimal.Zero, err
		}
		return intent, requested, funding, nil
	}

	recipient := bridgeable.Truncate(6)
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		base.Amount = recipient.Shift(6).BigInt().String()
		intent, err := h.chainrails.CreateIntent(ctx, &base)
		if err != nil {
			return nil, decimal.Zero, decimal.Zero, err
		}
		funding, err := intentFundingAmount(intent)
		if err != nil {
			return nil, decimal.Zero, decimal.Zero, err
		}
		if funding.LessThanOrEqual(bridgeable) {
			return intent, recipient, funding, nil
		}

		fee := funding.Sub(recipient)
		next := bridgeable.Sub(fee).Truncate(6)
		h.logger.Info("crypto-bridge: quote did not fit, retrying with a smaller amount",
			zap.Int("attempt", attempt+1),
			zap.String("quoted_recipient", recipient.StringFixed(6)),
			zap.String("quoted_funding", funding.StringFixed(6)),
			zap.String("next_recipient", next.StringFixed(6)),
			zap.Int("abandoned_intent_id", intent.ID))
		if !next.GreaterThan(decimal.Zero) {
			return nil, decimal.Zero, decimal.Zero, fmt.Errorf(
				"ChainRails fees (%s) exceed the bridgeable balance (%s)",
				fee.StringFixed(6), bridgeable.StringFixed(6))
		}
		if next.GreaterThanOrEqual(recipient) {
			lastErr = fmt.Errorf("quote did not converge (funding %s > balance %s)", funding.StringFixed(6), bridgeable.StringFixed(6))
			break
		}
		recipient = next
		lastErr = fmt.Errorf("no quote fit the balance after %d attempts", attempt+1)
	}
	return nil, decimal.Zero, decimal.Zero, lastErr
}

func (h *CryptoBridgeHandlers) onchainUSDC(ctx context.Context, walletID string) (decimal.Decimal, error) {
	balances, err := h.circle.GetTokenBalanceOnchain(ctx, walletID)
	if err != nil {
		return decimal.Zero, err
	}
	amount, err := decimal.NewFromString(usdcAmountFrom(balances))
	if err != nil {
		return decimal.Zero, fmt.Errorf("unparseable USDC balance %q", usdcAmountFrom(balances))
	}
	return amount, nil
}

// waitCircleTransfer polls a Circle transfer to a terminal state. Returns
// errCircleTransferTerminal for a definitive rejection, or a plain error for a
// timeout (money may still arrive).
func (h *CryptoBridgeHandlers) waitCircleTransfer(ctx context.Context, initial *circlepkg.Transaction) (*circlepkg.Transaction, error) {
	if initial == nil {
		return nil, fmt.Errorf("circle returned no transfer")
	}
	if initial.ID == "" {
		return nil, fmt.Errorf("circle transfer has no id; cannot confirm or dedupe a retry")
	}
	if isTransferComplete(string(initial.State)) {
		return initial, nil
	}
	if isTransferTerminalFailure(string(initial.State)) {
		return initial, fmt.Errorf("%w: state %s", errCircleTransferTerminal, initial.State)
	}
	last := initial
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for i := 0; i < circleTransferPolls; i++ {
		select {
		case <-ctx.Done():
			return last, fmt.Errorf("request cancelled before the transfer confirmed: %w", ctx.Err())
		case <-ticker.C:
			tx, err := h.circle.GetTransaction(ctx, initial.ID)
			if err != nil || tx == nil {
				h.logger.Warn("crypto-bridge: transfer poll failed", zap.String("transfer_id", initial.ID), zap.Error(err))
				continue
			}
			last = tx
			if isTransferComplete(string(tx.State)) {
				return tx, nil
			}
			if isTransferTerminalFailure(string(tx.State)) {
				return tx, fmt.Errorf("%w: state %s (reason %s)", errCircleTransferTerminal, tx.State, tx.ErrorReason)
			}
		}
	}
	return last, fmt.Errorf("transfer %s still %s after %d polls", initial.ID, last.State, circleTransferPolls)
}

func (h *CryptoBridgeHandlers) failForShortfall(c *gin.Context, payout *entities.OpsCryptoPayout, available, funding decimal.Decimal) {
	msg := fmt.Sprintf("funding this bridge needs %s USDC but the wallet holds %s", funding.StringFixed(6), available.StringFixed(6))
	if err := h.payouts.FailPayout(c.Request.Context(), payout.ID, msg); err != nil {
		h.logger.Error("crypto-bridge: failed to release a shortfall payout",
			zap.Error(err), zap.String("payout_id", payout.ID.String()))
	}
	c.JSON(http.StatusBadRequest, gin.H{
		"error":           msg,
		"payout_id":       payout.ID.String(),
		"status":          entities.OpsPayoutStatusFailed,
		"onchain_balance": available.StringFixed(6),
		"funding_amount":  funding.StringFixed(6),
		"ledger_touched":  false,
		"retry_hint":      "omit amount (or send \"all\") to size the transfer to the balance",
	})
}

// recordPayoutFailure releases the suppression after a failure that provably
// happened before any transfer was attempted.
func (h *CryptoBridgeHandlers) recordPayoutFailure(ctx context.Context, payout *entities.OpsCryptoPayout, cause error) {
	if err := h.payouts.FailPayout(ctx, payout.ID, cause.Error()); err != nil {
		h.logger.Error("crypto-bridge: failed to release suppression after setup failure",
			zap.Error(err), zap.String("payout_id", payout.ID.String()))
	}
}

func payoutJSON(p *entities.OpsCryptoPayout) gin.H {
	if p == nil {
		return gin.H{}
	}
	resp := gin.H{
		"payout_id":               p.ID.String(),
		"status":                  p.Status,
		"user_id":                 p.UserID.String(),
		"source_chain":            p.SourceChain,
		"source_address":          p.SourceAddress,
		"source_circle_wallet_id": p.SourceCircleWalletID,
		"source_circle_tx_id":     p.SourceCircleTxID,
		"source_tx_hash":          p.SourceTxHash,
		"destination_chain":       p.DestinationChain,
		"destination_address":     p.DestinationAddress,
		"amount":                  p.Amount.StringFixed(6),
		"attempts":                p.Attempts,
		"reason":                  p.Reason,
		"actor":                   p.Actor,
		"ledger_touched":          false,
		"suppression_held":        p.SuppressionHeld(),
		"created_at":              p.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at":              p.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if p.FundingAmount != nil {
		resp["funding_amount"] = p.FundingAmount.StringFixed(6)
	}
	if p.FeeAmount != nil {
		resp["fee_amount"] = p.FeeAmount.StringFixed(6)
	}
	if p.ChainRailsIntentID != nil {
		resp["chainrails_intent_id"] = *p.ChainRailsIntentID
	}
	if p.IntentAddress != nil {
		resp["intent_address"] = *p.IntentAddress
	}
	if p.CircleTransferID != nil {
		resp["circle_transfer_id"] = *p.CircleTransferID
	}
	if p.CircleTxHash != nil {
		resp["circle_tx_hash"] = *p.CircleTxHash
	}
	if p.SuppressionDepositID != nil {
		resp["suppression_deposit_id"] = p.SuppressionDepositID.String()
	}
	if p.ErrorMessage != nil {
		resp["error_message"] = *p.ErrorMessage
	}
	if p.CompletedAt != nil {
		resp["completed_at"] = p.CompletedAt.UTC().Format(time.RFC3339)
	}
	return resp
}

// intentFundingAmount converts ChainRails' quoted total (amount + fees, in
// token micro-units) into the exact USDC to send to the intent address.
func intentFundingAmount(intent *chainrails.CreateIntentResponse) (decimal.Decimal, error) {
	if intent == nil {
		return decimal.Zero, fmt.Errorf("chainrails returned no intent")
	}
	if strings.TrimSpace(intent.TotalAmountInAssetToken) == "" || intent.AssetTokenDecimals <= 0 {
		return decimal.Zero, fmt.Errorf("chainrails intent %d did not return a total funding amount", intent.ID)
	}
	// This flow is USDC-only (6 decimals). Refuse anything else rather than
	// derive a wrong amount from a different token.
	if intent.AssetTokenDecimals != 6 {
		return decimal.Zero, fmt.Errorf("chainrails intent %d has unexpected asset decimals %d (want 6 for USDC)",
			intent.ID, intent.AssetTokenDecimals)
	}
	units, ok := new(big.Int).SetString(intent.TotalAmountInAssetToken, 10)
	if !ok {
		return decimal.Zero, fmt.Errorf("unparseable chainrails funding amount %q", intent.TotalAmountInAssetToken)
	}
	amount := decimal.NewFromBigInt(units, -6)
	if !amount.GreaterThan(decimal.Zero) {
		return decimal.Zero, fmt.Errorf("chainrails intent %d returned a non-positive funding amount", intent.ID)
	}
	return amount, nil
}

func transferIDOf(final, initial *circlepkg.Transaction) string {
	if final != nil && final.ID != "" {
		return final.ID
	}
	if initial != nil {
		return initial.ID
	}
	return ""
}

func solanaWalletAddress(wallets []opsWallet) string {
	for _, w := range wallets {
		if strings.EqualFold(w.Chain, string(entities.WalletChainSolana)) && strings.TrimSpace(w.Address) != "" {
			return strings.TrimSpace(w.Address)
		}
	}
	return ""
}

// validateSolanaDestination requires a base58 string that decodes to a 32-byte
// public key. A typo here sends money nowhere.
func validateSolanaDestination(address string) error {
	if address == "" {
		return fmt.Errorf("destination_address is required (no Solana wallet was found for this user)")
	}
	if len(address) < 32 || len(address) > 44 {
		return fmt.Errorf("invalid Solana destination: %d characters, expected 32-44", len(address))
	}
	decoded, err := base58.Decode(address)
	if err != nil {
		return fmt.Errorf("invalid Solana destination: %v", err)
	}
	if len(decoded) != 32 {
		return fmt.Errorf("invalid Solana destination: decodes to %d bytes, expected 32", len(decoded))
	}
	return nil
}

// isTransferComplete reports whether a Circle transfer has settled.
func isTransferComplete(state string) bool {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "COMPLETE", "COMPLETED", "CONFIRMED":
		return true
	}
	return false
}

// isTransferTerminalFailure reports whether a Circle transfer will never move
// funds. Only these states are safe to release the suppression record.
func isTransferTerminalFailure(state string) bool {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "DENIED", "FAILED", "CANCELLED", "CANCELED":
		return true
	}
	return false
}
