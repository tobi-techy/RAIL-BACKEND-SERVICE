package blend

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	chainrailspkg "github.com/rail-service/rail_service/internal/infrastructure/adapters/chainrails"
	circlepkg "github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// baseWallet is a user's Base Circle wallet (Safe owner / deposit signer).
type baseWallet struct {
	CircleWalletID string
	Address        string
}

// chainRailsSource maps a Circle blockchain to its ChainRails source chain id and
// the USDC token address/mint on that chain.
type chainRailsSource struct {
	chain string
	token string
}

const solanaUSDCMint = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"

// circleBlockchainToChainRails maps Circle blockchains to ChainRails source params.
// Used to bridge the stash USDC from wherever the deposit landed into the user's
// Base EOA.
var circleBlockchainToChainRails = map[string]chainRailsSource{
	"SOL":          {"SOLANA_MAINNET", solanaUSDCMint},
	"SOL-DEVNET":   {"SOLANA_TESTNET", solanaUSDCMint},
	"ETH":          {"ETHEREUM_MAINNET", "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"},
	"ETH-SEPOLIA":  {"ETHEREUM_TESTNET", "0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238"},
	"MATIC":        {"POLYGON_MAINNET", "0x3c499c542cEF5E3811e1192ce70d8cC03d5c3359"},
	"MATIC-AMOY":   {"POLYGON_TESTNET", "0x41E94Eb019C0762f9Bfcf9Fb1E58725BfB0e7582"},
	"ARB":          {"ARBITRUM_MAINNET", "0xaf88d065e77c8cC2239327C5EDb3A432268e5831"},
	"ARB-SEPOLIA":  {"ARBITRUM_TESTNET", "0x75faf114eafb1BDbe2F0316DF893fd58CE46AA4d"},
	"OP":           {"OPTIMISM_MAINNET", "0x0b2C639c533813f4Aa9D7837CAf62653d097Ff85"},
	"OP-SEPOLIA":   {"OPTIMISM_TESTNET", "0x5fd84259d66Cd46123540766Be93DFE6D43130D7"},
	"AVAX":         {"AVALANCHE_MAINNET", "0xB97EF9Ef8734C71904D8002F8b6Bc66Dd9c48a6E"},
	"AVAX-FUJI":    {"AVALANCHE_TESTNET", "0x5425890298aed601595a70AB815c96711a31Bc65"},
	"BASE":         {"BASE_MAINNET", BaseUSDCAddress},
	"BASE-SEPOLIA": {"BASE_TESTNET", "0x036CbD53842c5426634e7929541eC2318f3dCF7e"},
}

// resolveUserBaseWallet finds the user's Base Circle wallet (the Safe owner). Prefers
// the managed_wallets/wallets tables, then falls back to Circle's ref-id lookup.
func (r *DepositRouter) resolveUserBaseWallet(ctx context.Context, userID uuid.UUID) (baseWallet, error) {
	return r.resolveUserWalletByChains(ctx, userID, r.baseCircleChains(), "Base")
}

// resolveUserWalletByChains is the shared wallet resolution logic for any chain filter.
func (r *DepositRouter) resolveUserWalletByChains(ctx context.Context, userID uuid.UUID, chains []string, label string) (baseWallet, error) {
	var dbWallet struct {
		CircleWalletID sql.NullString `db:"circle_wallet_id"`
		Address        sql.NullString `db:"address"`
	}
	for _, table := range []string{"managed_wallets", "wallets"} {
		var query string
		switch table {
		case "managed_wallets":
			query = `SELECT circle_wallet_id, address FROM managed_wallets
				WHERE user_id = $1 AND UPPER(chain) = ANY($2)
				AND COALESCE(circle_wallet_id, '') <> '' AND COALESCE(address, '') <> ''
				ORDER BY CASE WHEN status = 'live' THEN 0 ELSE 1 END, created_at ASC LIMIT 1`
		case "wallets":
			query = `SELECT circle_wallet_id, address FROM wallets
				WHERE user_id = $1 AND UPPER(chain) = ANY($2)
				AND COALESCE(circle_wallet_id, '') <> '' AND COALESCE(address, '') <> ''
				ORDER BY CASE WHEN status = 'live' THEN 0 ELSE 1 END, created_at ASC LIMIT 1`
		default:
			continue
		}
		if err := r.db.GetContext(ctx, &dbWallet, query, userID.String(), pq.Array(chains)); err == nil {
			return baseWallet{
				CircleWalletID: strings.TrimSpace(dbWallet.CircleWalletID.String),
				Address:        strings.TrimSpace(dbWallet.Address.String),
			}, nil
		}
	}
	wallets, err := r.circle.ListCircleWalletsByRefID(ctx, userID.String())
	if err != nil {
		return baseWallet{}, fmt.Errorf("blend: list circle wallets for user %s: %w", userID, err)
	}
	for _, w := range wallets {
		bc := strings.ToUpper(strings.TrimSpace(string(w.Blockchain)))
		for _, c := range chains {
			if bc == c && strings.TrimSpace(w.ID) != "" && strings.TrimSpace(w.Address) != "" {
				return baseWallet{CircleWalletID: w.ID, Address: w.Address}, nil
			}
		}
	}
	return baseWallet{}, fmt.Errorf("blend: user %s has no %s Circle wallet", userID, label)
}

func (r *DepositRouter) baseCircleChains() []string {
	if r.chainID == BaseMainnetChainID {
		return []string{"BASE"}
	}
	// Base Sepolia (84532) or any non-mainnet config.
	return []string{"BASE-SEPOLIA", "BASE"}
}

// fundBaseEOA moves the stash USDC into the user's Base EOA. It performs at most one
// forward action per call (create+fund bridge intent, poll an in-flight bridge, or a
// same-chain transfer) and is safe to call repeatedly — Circle idempotency keys and
// persisted bridge state prevent duplicate transfers.
func (r *DepositRouter) fundBaseEOA(ctx context.Context, route *depositRoute, need decimal.Decimal) error {
	sourceWalletID := strings.TrimSpace(route.SourceCircleWalletID.String)
	if sourceWalletID == "" {
		return fmt.Errorf("blend: route %s has no funding source wallet", route.ID)
	}

	// A bridge intent is already in flight — ensure it is funded (idempotent), then
	// poll. This closes the crash window where the intent was persisted but the
	// funding transfer had not yet been issued.
	if route.BridgeIntentAddress.Valid && strings.TrimSpace(route.BridgeIntentAddress.String) != "" {
		if err := r.ensureBridgeFunded(ctx, route); err != nil {
			return err
		}
		return r.pollBridgeSettlement(ctx, route)
	}

	sourceWallet, err := r.circle.GetWallet(ctx, sourceWalletID)
	if err != nil {
		return fmt.Errorf("blend: get source wallet %s: %w", sourceWalletID, err)
	}
	srcChain := strings.ToUpper(strings.TrimSpace(string(sourceWallet.Blockchain)))

	// Same-chain Base: a direct Circle transfer to the EOA.
	if srcChain == "BASE" || srcChain == "BASE-SEPOLIA" {
		if strings.EqualFold(sourceWalletID, route.CircleWalletID) {
			// Source IS the EOA but balance < need — funds genuinely not present yet.
			return nil
		}
		return r.fundSameChain(ctx, route, sourceWalletID, need)
	}

	// Cross-chain: create+persist the bridge intent, then fund and poll.
	if err := r.createBridgeIntent(ctx, route, sourceWallet, srcChain, need); err != nil {
		return err
	}
	if err := r.ensureBridgeFunded(ctx, route); err != nil {
		return err
	}
	return r.pollBridgeSettlement(ctx, route)
}

func (r *DepositRouter) fundSameChain(ctx context.Context, route *depositRoute, sourceWalletID string, need decimal.Decimal) error {
	tokenID, err := r.circle.GetUSDCTokenIDOnchain(ctx, sourceWalletID)
	if err != nil {
		return fmt.Errorf("blend: get source USDC token id: %w", err)
	}
	transfer, err := r.circle.TransferUSDCWithIdempotency(
		ctx, sourceWalletID, tokenID, route.EOAAddress, need.StringFixed(6),
		uuid.NewSHA1(uuid.NameSpaceOID, []byte("blend-fund-"+route.ID.String())).String(),
	)
	if err != nil {
		return fmt.Errorf("blend: same-chain fund transfer: %w", err)
	}
	if _, err := r.waitCircleTransfer(ctx, transfer); err != nil {
		return err
	}
	r.logger.Info("Blend same-chain funding transfer complete",
		zap.String("route_id", route.ID.String()),
		zap.String("source", sourceWalletID),
		zap.String("eoa", route.EOAAddress))
	return nil
}

// createBridgeIntent creates the ChainRails intent and persists it BEFORE any funds
// move, so a crash never strands money against an unrecorded intent.
func (r *DepositRouter) createBridgeIntent(ctx context.Context, route *depositRoute, sourceWallet *circlepkg.Wallet, srcChain string, need decimal.Decimal) error {
	bridge, destCfg := r.getChainRails()
	if bridge == nil {
		return fmt.Errorf("blend: ChainRails is required to bridge %s funds to Base but is not configured", srcChain)
	}
	source, ok := circleBlockchainToChainRails[srcChain]
	if !ok {
		return fmt.Errorf("blend: unsupported source chain %s for ChainRails bridge to Base", srcChain)
	}
	if strings.TrimSpace(sourceWallet.Address) == "" {
		return fmt.Errorf("blend: source wallet %s has no on-chain address", sourceWallet.ID)
	}
	destChain := r.baseChainRailsDestination(source.chain, destCfg)
	amountMicro := need.Truncate(6).Shift(6).BigInt()

	intent, err := bridge.CreateIntent(ctx, &chainrailspkg.CreateIntentRequest{
		Sender:           sourceWallet.Address,
		Amount:           amountMicro.String(),
		AmountSymbol:     "USDC",
		TokenIn:          source.token,
		SourceChain:      source.chain,
		DestinationChain: destChain,
		Recipient:        route.EOAAddress,
		RefundAddress:    sourceWallet.Address,
		Metadata: map[string]interface{}{
			"deposit_id": nullUUIDString(route.DepositID),
			"user_id":    route.UserID.String(),
			"route_id":   route.ID.String(),
			"type":       "blend_deposit_funding",
		},
	})
	if err != nil {
		return fmt.Errorf("blend: create ChainRails intent: %w", err)
	}
	if strings.TrimSpace(intent.IntentAddress) == "" {
		return fmt.Errorf("blend: ChainRails intent %d has no intent address", intent.ID)
	}
	fundAmount, err := chainRailsFundingAmount(intent, need)
	if err != nil {
		return err
	}
	if _, err := r.db.ExecContext(ctx, `
		UPDATE blend_deposit_routes
		SET bridge_intent_id = $2, bridge_intent_address = $3, bridge_source_chain = $4,
			bridge_fund_amount = $5, updated_at = NOW()
		WHERE id = $1
	`, route.ID, intent.ID, intent.IntentAddress, source.chain, fundAmount); err != nil {
		return fmt.Errorf("blend: persist bridge intent: %w", err)
	}
	route.BridgeIntentAddress = sql.NullString{String: intent.IntentAddress, Valid: true}
	route.BridgeFundAmount = decimal.NullDecimal{Decimal: fundAmount, Valid: true}
	r.logger.Info("Blend ChainRails intent created",
		zap.String("route_id", route.ID.String()),
		zap.Int("intent_id", intent.ID),
		zap.String("source_chain", source.chain),
		zap.String("dest_chain", destChain),
		zap.String("fund_amount", fundAmount.StringFixed(6)))
	return nil
}

// ensureBridgeFunded issues the funding transfer to the intent address. The Circle
// idempotency key makes this safe to call repeatedly — if already funded it returns
// the existing transfer rather than sending again.
func (r *DepositRouter) ensureBridgeFunded(ctx context.Context, route *depositRoute) error {
	sourceWalletID := strings.TrimSpace(route.SourceCircleWalletID.String)
	intentAddr := strings.TrimSpace(route.BridgeIntentAddress.String)
	if sourceWalletID == "" || intentAddr == "" {
		return fmt.Errorf("blend: cannot fund bridge without source wallet and intent address")
	}
	if !route.BridgeFundAmount.Valid || !route.BridgeFundAmount.Decimal.GreaterThan(decimal.Zero) {
		return fmt.Errorf("blend: bridge fund amount missing for route %s", route.ID)
	}
	tokenID, err := r.circle.GetUSDCTokenIDOnchain(ctx, sourceWalletID)
	if err != nil {
		return fmt.Errorf("blend: get source USDC token id for bridge: %w", err)
	}
	transfer, err := r.circle.TransferUSDCWithIdempotency(
		ctx, sourceWalletID, tokenID, intentAddr, route.BridgeFundAmount.Decimal.StringFixed(6),
		uuid.NewSHA1(uuid.NameSpaceOID, []byte("blend-bridge-"+route.ID.String())).String(),
	)
	if err != nil {
		return fmt.Errorf("blend: fund ChainRails intent: %w", err)
	}
	if _, err := r.waitCircleTransfer(ctx, transfer); err != nil {
		return err
	}
	return nil
}

func (r *DepositRouter) pollBridgeSettlement(ctx context.Context, route *depositRoute) error {
	bridge, _ := r.getChainRails()
	if bridge == nil {
		return fmt.Errorf("blend: ChainRails not configured to reconcile bridge for route %s", route.ID)
	}
	intentAddr := strings.TrimSpace(route.BridgeIntentAddress.String)

	// Poll for up to 90 seconds — ChainRails typically settles within seconds of funding.
	const maxPolls = 15
	for i := 0; i < maxPolls; i++ {
		status, err := bridge.GetIntentStatus(ctx, intentAddr)
		if err != nil {
			return fmt.Errorf("blend: poll ChainRails intent %s: %w", intentAddr, err)
		}
		switch {
		case isChainRailsComplete(status.Status):
			if _, err := r.db.ExecContext(ctx, `
				UPDATE blend_deposit_routes SET bridge_tx_hash = NULLIF($2,''), funded_at = NOW(), updated_at = NOW() WHERE id = $1
			`, route.ID, status.TxHash); err != nil {
				return fmt.Errorf("blend: record bridge settlement: %w", err)
			}
			return nil
		case isChainRailsTerminalFailure(status.Status):
			if _, err := r.db.ExecContext(ctx, `
				UPDATE blend_deposit_routes
				SET bridge_intent_id = NULL, bridge_intent_address = NULL, bridge_source_chain = NULL,
					bridge_fund_amount = NULL, last_error = $2, updated_at = NOW()
				WHERE id = $1
			`, route.ID, fmt.Sprintf("ChainRails bridge %s failed: %s", intentAddr, status.Status)); err != nil {
				return fmt.Errorf("blend: clear failed bridge intent: %w", err)
			}
			return fmt.Errorf("blend: ChainRails bridge %s failed with status %s", intentAddr, status.Status)
		}
		// Still settling — wait before next poll.
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(6 * time.Second):
		}
	}
	// Not settled within poll window — park for next worker tick.
	return nil
}

func (r *DepositRouter) waitCircleTransfer(ctx context.Context, initial *circlepkg.Transaction) (*circlepkg.Transaction, error) {
	if initial == nil {
		return nil, fmt.Errorf("blend: nil circle transfer")
	}
	if isCircleTransferComplete(initial.State) {
		return initial, nil
	}
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	const maxPolls = 40 // 40 × 3s = 2 minutes max
	for i := 0; i < maxPolls; i++ {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("blend: circle transfer %s did not complete before timeout: %w", initial.ID, ctx.Err())
		case <-ticker.C:
			tx, err := r.circle.GetTransaction(ctx, initial.ID)
			if err != nil {
				r.logger.Warn("blend: poll circle transfer failed", zap.String("tx_id", initial.ID), zap.Error(err))
				continue
			}
			if isCircleTransferComplete(tx.State) {
				return tx, nil
			}
			if isCircleTransferTerminalFailure(tx.State) {
				return nil, fmt.Errorf("blend: circle transfer %s failed: %s (%s)", initial.ID, tx.State, tx.ErrorReason)
			}
		}
	}
	return nil, fmt.Errorf("blend: circle transfer %s still pending after %d polls", initial.ID, maxPolls)
}

func (r *DepositRouter) baseChainRailsDestination(sourceChain, configured string) string {
	dest := strings.TrimSpace(configured)
	if dest == "" {
		if r.chainID == BaseMainnetChainID {
			dest = "BASE_MAINNET"
		} else {
			dest = "BASE_TESTNET"
		}
	}
	if strings.Contains(sourceChain, "TESTNET") && strings.Contains(dest, "MAINNET") {
		return strings.Replace(dest, "MAINNET", "TESTNET", 1)
	}
	return dest
}

// --- shared ChainRails / Circle helpers (blend-local copies) ---

func chainRailsFundingAmount(intent *chainrailspkg.CreateIntentResponse, requested decimal.Decimal) (decimal.Decimal, error) {
	if strings.TrimSpace(intent.TotalAmountInAssetToken) == "" || intent.AssetTokenDecimals <= 0 {
		return decimal.Zero, fmt.Errorf("blend: ChainRails intent %d did not return total funding amount", intent.ID)
	}
	totalUnits, ok := new(big.Int).SetString(intent.TotalAmountInAssetToken, 10)
	if !ok {
		return decimal.Zero, fmt.Errorf("blend: failed to parse ChainRails funding amount %q", intent.TotalAmountInAssetToken)
	}
	amount := decimal.NewFromBigInt(totalUnits, -int32(intent.AssetTokenDecimals))
	if !amount.GreaterThan(decimal.Zero) {
		return decimal.Zero, fmt.Errorf("blend: ChainRails intent %d returned non-positive funding amount %s", intent.ID, amount.String())
	}
	if amount.LessThan(requested.Truncate(6)) {
		return decimal.Zero, fmt.Errorf("blend: ChainRails intent %d funding amount %s is less than requested %s", intent.ID, amount.StringFixed(6), requested.Truncate(6).StringFixed(6))
	}
	return amount, nil
}

func isChainRailsComplete(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "COMPLETED", "COMPLETE", "SUCCESS", "SUCCEEDED", "SETTLED", "FULFILLED":
		return true
	}
	return false
}

func isChainRailsTerminalFailure(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "FAILED", "FAILURE", "CANCELLED", "CANCELED", "EXPIRED", "REJECTED", "REFUNDED":
		return true
	}
	return false
}

func isCircleTransferComplete(state circlepkg.TransactionState) bool {
	return state == circlepkg.TransactionStateComplete || strings.EqualFold(string(state), "COMPLETED")
}

func isCircleTransferTerminalFailure(state circlepkg.TransactionState) bool {
	return state == circlepkg.TransactionStateFailed ||
		state == circlepkg.TransactionStateCancelled ||
		state == circlepkg.TransactionStateDenied
}
