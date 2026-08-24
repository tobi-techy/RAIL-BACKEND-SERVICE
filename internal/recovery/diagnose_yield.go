package recovery

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"

	blend "github.com/rail-service/rail_service/internal/infrastructure/adapters/blend"
	circle "github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
)

// DiagnoseParams describes a yield diagnostic request.
type DiagnoseParams struct {
	UserID string // user UUID
}

// DiagnoseYield locates every dollar related to a user's yield (stash) position
// across Blend, Circle wallets, DB state, and in-flight withdrawals/redemptions.
// It is read-only — no funds move, no state changes.
func DiagnoseYield(ctx context.Context, db *sqlx.DB, blendClient *blend.Client, circleClient *circle.HTTPClient, p DiagnoseParams, out io.Writer) error {
	userID, err := uuid.Parse(p.UserID)
	if err != nil {
		return fmt.Errorf("invalid user UUID %q: %w", p.UserID, err)
	}

	fmt.Fprintf(out, "\n")
	fmt.Fprintf(out, "========================================\n")
	fmt.Fprintf(out, "  YIELD DIAGNOSTIC for user %s\n", userID)
	fmt.Fprintf(out, "  Generated: %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(out, "========================================\n\n")

	// --- 1. Managed wallets (Circle) ---
	type walletRow struct {
		Chain          string `db:"chain"`
		Address        string `db:"address"`
		CircleWalletID string `db:"circle_wallet_id"`
		Status         string `db:"status"`
		AccountType    string `db:"account_type"`
	}

	var wallets []walletRow
	if err := db.SelectContext(ctx, &wallets, `
		SELECT UPPER(chain) AS chain, address, COALESCE(circle_wallet_id, '') AS circle_wallet_id,
		       COALESCE(status, '') AS status, COALESCE(account_type, '') AS account_type
		FROM managed_wallets
		WHERE user_id = $1
		ORDER BY chain
	`, userID); err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("query managed_wallets: %w", err)
	}

	fmt.Fprintf(out, "--- MANAGED WALLETS (%d) ---\n", len(wallets))
	for _, w := range wallets {
		fmt.Fprintf(out, "  chain=%-6s  addr=%s  circle_id=%s  status=%s  type=%s\n",
			w.Chain, w.Address, w.CircleWalletID, w.Status, w.AccountType)
	}
	fmt.Fprintf(out, "\n")

	// --- 2. Blend user account ---
	type blendAcctRow struct {
		ID              uuid.UUID    `db:"id"`
		EOAAddress      string       `db:"eoa_address"`
		BlendAccountID  string       `db:"blend_account_id"`
		SafeAddress     string       `db:"safe_address"`
		ChainID         int64        `db:"chain_id"`
		SafeStatus      string       `db:"safe_status"`
		SafeRequestedAt sql.NullTime `db:"safe_requested_at"`
		SafeDeployedAt  sql.NullTime `db:"safe_deployed_at"`
		CircleWalletID  string       `db:"circle_wallet_id"`
	}

	var blendAcct blendAcctRow
	blendAcctErr := db.GetContext(ctx, &blendAcct, `
		SELECT id, eoa_address, blend_account_id, COALESCE(safe_address, '') AS safe_address,
		       chain_id, safe_status, safe_requested_at, safe_deployed_at, circle_wallet_id
		FROM blend_user_accounts
		WHERE user_id = $1
	`, userID)

	if blendAcctErr != nil && blendAcctErr != sql.ErrNoRows {
		return fmt.Errorf("query blend_user_accounts: %w", blendAcctErr)
	}

	if blendAcctErr == sql.ErrNoRows {
		fmt.Fprintf(out, "--- BLEND ACCOUNT: none ---\n")
		fmt.Fprintf(out, "  User has no Blend account. Yield routing may not be set up.\n\n")
	} else {
		fmt.Fprintf(out, "--- BLEND ACCOUNT ---\n")
		fmt.Fprintf(out, "  blend_account_id = %s\n", blendAcct.BlendAccountID)
		fmt.Fprintf(out, "  eoa_address      = %s\n", blendAcct.EOAAddress)
		fmt.Fprintf(out, "  safe_address     = %s\n", blendAcct.SafeAddress)
		fmt.Fprintf(out, "  chain_id         = %d\n", blendAcct.ChainID)
		fmt.Fprintf(out, "  safe_status      = %s\n", blendAcct.SafeStatus)
		fmt.Fprintf(out, "  circle_wallet_id = %s\n", blendAcct.CircleWalletID)
		if blendAcct.SafeDeployedAt.Valid {
			fmt.Fprintf(out, "  safe_deployed_at = %s\n", blendAcct.SafeDeployedAt.Time.Format(time.RFC3339))
		}
		fmt.Fprintf(out, "\n")
	}

	// --- 3. Blend API: balance, returns, positions ---
	if blendClient != nil && blendAcct.BlendAccountID != "" {
		fmt.Fprintf(out, "--- BLEND API ---\n")

		if bal, err := blendClient.GetBalance(ctx, blendAcct.BlendAccountID); err != nil {
			fmt.Fprintf(out, "  GetBalance: ERROR: %v\n", err)
		} else {
			totalUnderlying := aggregateUnderlyingStr(bal.PerChain)
			fmt.Fprintf(out, "  Safe balance (total underlying): %s USDC\n", totalUnderlying)
			for _, pc := range bal.PerChain {
				fmt.Fprintf(out, "    chain=%d  totalUnderlying=%s  vault=%s\n",
					pc.ChainID, pc.TotalUnderlying, truncateStr(pc.VaultAddress, 20))
			}
			if len(bal.HeldAssets) > 0 {
				fmt.Fprintf(out, "  held assets:\n")
				for _, ha := range bal.HeldAssets {
					fmt.Fprintf(out, "    symbol=%s  chain=%d  addr=%s\n", ha.Symbol, ha.ChainID, truncateStr(ha.Address, 20))
				}
			}
		}

		if ret, err := blendClient.GetReturns(ctx, blendAcct.BlendAccountID); err != nil {
			fmt.Fprintf(out, "  GetReturns: ERROR: %v\n", err)
		} else {
			fmt.Fprintf(out, "  Returns: deposited=%.6f  withdrawn=%.6f  net=%.6f  returns=%.6f  returnsPct=%.4f\n",
				getFloat(ret.TotalDeposited), getFloat(ret.TotalWithdrawn), getFloat(ret.NetDeposited),
				getFloat(ret.ReturnsAmount), ret.ReturnsPct)
		}

		if positions, err := blendClient.ListPositions(ctx, blendAcct.BlendAccountID); err != nil {
			fmt.Fprintf(out, "  ListPositions: ERROR: %v\n", err)
		} else {
			recent := positions.Events
			if len(recent) > 10 {
				recent = recent[:10]
			}
			fmt.Fprintf(out, "  Recent positions (%d shown, %d total):\n", len(recent), len(positions.Events))
			for _, ev := range recent {
				fmt.Fprintf(out, "    %s  chain=%d  tx=%s  block=%s\n",
					ev.Kind, ev.ChainID, truncateStr(ev.TransactionHash, 20), ev.BlockTime.Format(time.RFC3339))
			}
		}
		fmt.Fprintf(out, "\n")
	}

	// --- 4. Blend deposit routes ---
	type routeRow struct {
		ID             uuid.UUID       `db:"id"`
		DepositID      uuid.NullUUID   `db:"deposit_id"`
		EOAAddress     string          `db:"eoa_address"`
		SafeAddress    string          `db:"safe_address"`
		Amount         decimal.Decimal `db:"amount"`
		Status         string          `db:"status"`
		Attempts       int             `db:"attempts"`
		SourceChain    sql.NullString  `db:"source_chain"`
		BridgeIntentID sql.NullInt64   `db:"bridge_intent_id"`
		LastError      sql.NullString  `db:"last_error"`
		NextRetryAt    sql.NullTime    `db:"next_retry_at"`
		SettledAt      sql.NullTime    `db:"settled_at"`
	}

	var routes []routeRow
	if err := db.SelectContext(ctx, &routes, `
		SELECT id, deposit_id, eoa_address, COALESCE(safe_address, '') AS safe_address,
		       amount, status, attempts, source_chain, bridge_intent_id,
		       last_error, next_retry_at, settled_at
		FROM blend_deposit_routes
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT 20
	`, userID); err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("query blend_deposit_routes: %w", err)
	}

	fmt.Fprintf(out, "--- BLEND DEPOSIT ROUTES (%d) ---\n", len(routes))
	for _, r := range routes {
		fmt.Fprintf(out, "  id=%s  amount=%s  status=%s  attempts=%d", r.ID, r.Amount.StringFixed(6), r.Status, r.Attempts)
		if r.SourceChain.Valid {
			fmt.Fprintf(out, "  src=%s", r.SourceChain.String)
		}
		if r.BridgeIntentID.Valid {
			fmt.Fprintf(out, "  bridge_intent=%d", r.BridgeIntentID.Int64)
		}
		if r.SettledAt.Valid {
			fmt.Fprintf(out, "  settled=%s", r.SettledAt.Time.Format(time.RFC3339))
		}
		if r.NextRetryAt.Valid {
			fmt.Fprintf(out, "  next_retry=%s", r.NextRetryAt.Time.Format(time.RFC3339))
		}
		fmt.Fprintf(out, "\n")
		if r.LastError.Valid && r.LastError.String != "" {
			fmt.Fprintf(out, "    last_error: %s\n", truncateStr(r.LastError.String, 120))
		}
	}
	fmt.Fprintf(out, "\n")

	// --- 5. Blend yield redemptions ---
	type redemptionRow struct {
		ID                 uuid.UUID           `db:"id"`
		Amount             decimal.Decimal     `db:"amount"`
		DestinationChainID int64               `db:"destination_chain_id"`
		IntentID           sql.NullString      `db:"intent_id"`
		IntentStatus       sql.NullString      `db:"intent_status"`
		TxHash             sql.NullString      `db:"tx_hash"`
		IdempotencyKey     string              `db:"idempotency_key"`
		Status             string              `db:"status"`
		Attempts           int                 `db:"attempts"`
		LastError          sql.NullString      `db:"last_error"`
		NextRetryAt        sql.NullTime        `db:"next_retry_at"`
		SettledAt          sql.NullTime        `db:"settled_at"`
		PreRedeemBalance   decimal.NullDecimal `db:"pre_redeem_eoa_balance"`
	}

	var redemptions []redemptionRow
	if err := db.SelectContext(ctx, &redemptions, `
		SELECT id, amount, destination_chain_id, intent_id, intent_status,
		       tx_hash, idempotency_key, status, attempts, last_error,
		       next_retry_at, settled_at, pre_redeem_eoa_balance
		FROM blend_yield_redemptions
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT 20
	`, userID); err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("query blend_yield_redemptions: %w", err)
	}

	fmt.Fprintf(out, "--- BLEND YIELD REDEMPTIONS (%d) ---\n", len(redemptions))
	for _, r := range redemptions {
		fmt.Fprintf(out, "  id=%s  amount=%s  dest_chain=%d  status=%s  attempts=%d  idem=%s",
			r.ID, r.Amount.StringFixed(6), r.DestinationChainID, r.Status, r.Attempts, r.IdempotencyKey)
		if r.IntentID.Valid {
			fmt.Fprintf(out, "  intent=%s", truncateStr(r.IntentID.String, 20))
		}
		if r.IntentStatus.Valid {
			fmt.Fprintf(out, "  intent_status=%s", r.IntentStatus.String)
		}
		if r.TxHash.Valid {
			fmt.Fprintf(out, "  tx=%s", truncateStr(r.TxHash.String, 20))
		}
		if r.SettledAt.Valid {
			fmt.Fprintf(out, "  settled=%s", r.SettledAt.Time.Format(time.RFC3339))
		}
		if r.PreRedeemBalance.Valid {
			fmt.Fprintf(out, "  pre_balance=%s", r.PreRedeemBalance.Decimal.StringFixed(6))
		}
		fmt.Fprintf(out, "\n")
		if r.LastError.Valid && r.LastError.String != "" {
			fmt.Fprintf(out, "    last_error: %s\n", truncateStr(r.LastError.String, 120))
		}
	}
	fmt.Fprintf(out, "\n")

	// --- 6. Withdrawals (stash source, stuck) ---
	type withdrawalRow struct {
		ID               uuid.UUID       `db:"id"`
		Amount           decimal.Decimal `db:"amount"`
		FeeAmount        decimal.Decimal `db:"fee_amount"`
		Currency         string          `db:"currency"`
		Status           string          `db:"status"`
		SourceAccount    string          `db:"source_account"`
		DestinationChain string          `db:"destination_chain"`
		DestinationAddr  string          `db:"destination_address"`
		TxHash           sql.NullString  `db:"tx_hash"`
		CreatedAt        time.Time       `db:"created_at"`
		UpdatedAt        time.Time       `db:"updated_at"`
	}

	var withdrawals []withdrawalRow
	if err := db.SelectContext(ctx, &withdrawals, `
		SELECT id, amount, fee_amount, COALESCE(currency, '') AS currency,
		       status, COALESCE(source_account, '') AS source_account,
		       COALESCE(destination_chain, '') AS destination_chain,
		       COALESCE(destination_address, '') AS destination_address,
		       tx_hash, created_at, updated_at
		FROM withdrawals
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT 20
	`, userID); err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("query withdrawals: %w", err)
	}

	stuckCount := 0
	fmt.Fprintf(out, "--- WITHDRAWALS (%d, most recent) ---\n", len(withdrawals))
	for _, w := range withdrawals {
		isStuck := w.Status == "pending" || w.Status == "processing" || w.Status == "initiated"
		if isStuck {
			stuckCount++
		}
		marker := ""
		if isStuck {
			marker = " *** STUCK ***"
		}
		fmt.Fprintf(out, "  id=%s  amount=%s  fee=%s  curr=%s  status=%s  src=%s  dest_chain=%s%s\n",
			w.ID, w.Amount.StringFixed(6), w.FeeAmount.StringFixed(6), w.Currency, w.Status,
			w.SourceAccount, w.DestinationChain, marker)
		if w.DestinationAddr != "" {
			fmt.Fprintf(out, "    dest_addr=%s\n", truncateStr(w.DestinationAddr, 50))
		}
		if w.TxHash.Valid && w.TxHash.String != "" {
			fmt.Fprintf(out, "    tx_hash=%s\n", truncateStr(w.TxHash.String, 66))
		}
		fmt.Fprintf(out, "    created=%s  updated=%s\n", w.CreatedAt.Format(time.RFC3339), w.UpdatedAt.Format(time.RFC3339))
	}
	fmt.Fprintf(out, "\n")

	// --- 7. Circle on-chain balances for each wallet ---
	fmt.Fprintf(out, "--- CIRCLE ON-CHAIN BALANCES ---\n")
	totalOnChain := decimal.Zero

	for _, w := range wallets {
		if w.CircleWalletID == "" {
			fmt.Fprintf(out, "  chain=%-6s  addr=%s  NO CIRCLE WALLET ID\n", w.Chain, truncateStr(w.Address, 20))
			continue
		}
		if circleClient == nil {
			fmt.Fprintf(out, "  chain=%-6s  addr=%s  (Circle client not configured)\n", w.Chain, truncateStr(w.Address, 20))
			continue
		}
		balances, err := circleClient.GetTokenBalanceOnchain(ctx, w.CircleWalletID)
		if err != nil {
			fmt.Fprintf(out, "  chain=%-6s  addr=%s  ERROR: %v\n", w.Chain, truncateStr(w.Address, 20), err)
			continue
		}
		hasUSDC := false
		for _, b := range balances {
			if strings.EqualFold(b.Token.Symbol, "USDC") {
				amt, _ := decimal.NewFromString(b.Amount)
				totalOnChain = totalOnChain.Add(amt)
				hasUSDC = true
				fmt.Fprintf(out, "  chain=%-6s  addr=%s  USDC=%s  tokenId=%s\n",
					w.Chain, truncateStr(w.Address, 20), amt.StringFixed(6), truncateStr(b.Token.ID, 20))
			}
		}
		if !hasUSDC {
			fmt.Fprintf(out, "  chain=%-6s  addr=%s  USDC=0\n", w.Chain, truncateStr(w.Address, 20))
		}
	}
	fmt.Fprintf(out, "  TOTAL on-chain USDC across all wallets: %s\n", totalOnChain.StringFixed(6))
	fmt.Fprintf(out, "\n")

	// --- 8. Summary ---
	fmt.Fprintf(out, "========================================\n")
	fmt.Fprintf(out, "  SUMMARY\n")
	fmt.Fprintf(out, "========================================\n")

	// Count stuck redemptions
	stuckRedemptions := 0
	var stuckRedemptionTotal decimal.Decimal
	for _, r := range redemptions {
		if r.Status != "complete" && r.Status != "failed" {
			stuckRedemptions++
			stuckRedemptionTotal = stuckRedemptionTotal.Add(r.Amount)
		}
	}

	// Count stuck routes
	stuckRoutes := 0
	var stuckRouteTotal decimal.Decimal
	for _, r := range routes {
		if r.Status != "complete" && !strings.HasPrefix(r.Status, "error_") {
			stuckRoutes++
			stuckRouteTotal = stuckRouteTotal.Add(r.Amount)
		}
	}

	// Count stuck withdrawals
	var stuckWithdrawalTotal decimal.Decimal
	for _, w := range withdrawals {
		if w.Status == "pending" || w.Status == "processing" || w.Status == "initiated" {
			stuckWithdrawalTotal = stuckWithdrawalTotal.Add(w.Amount)
		}
	}

	fmt.Fprintf(out, "  Stuck deposit routes:     %d  (total: %s USDC)\n", stuckRoutes, stuckRouteTotal.StringFixed(6))
	fmt.Fprintf(out, "  Stuck redemptions:        %d  (total: %s USDC)\n", stuckRedemptions, stuckRedemptionTotal.StringFixed(6))
	fmt.Fprintf(out, "  Stuck withdrawals:        %d  (total: %s USDC)\n", stuckCount, stuckWithdrawalTotal.StringFixed(6))
	fmt.Fprintf(out, "  Total on-chain USDC:      %s\n", totalOnChain.StringFixed(6))
	fmt.Fprintf(out, "\n")

	if stuckRedemptions > 0 {
		fmt.Fprintf(out, "  ⚠ Funds may be stuck in Blend redemption — check redemption status and\n")
		fmt.Fprintf(out, "    whether the Blend session is CANCELLED/FAILED. Use recover-funds to bridge\n")
		fmt.Fprintf(out, "    stranded USDC from the Base EOA back to Solana.\n")
	}
	if stuckCount > 0 {
		fmt.Fprintf(out, "  ⚠ Withdrawals are stuck — check if the crypto transfer failed because the\n")
		fmt.Fprintf(out, "    source wallet had no USDC (Unified Solana Settlement forces SOL source).\n")
		fmt.Fprintf(out, "    Funds may be on the Base EOA after Blend redemption but the transfer\n")
		fmt.Fprintf(out, "    tried to source from Solana.\n")
	}
	if totalOnChain.GreaterThan(decimal.Zero) {
		fmt.Fprintf(out, "  💡 USDC found on-chain — check which wallet holds it and whether a\n")
		fmt.Fprintf(out, "    ChainRails bridge or Circle transfer can move it to the destination.\n")
	}

	fmt.Fprintf(out, "\n")

	return nil
}

// --- helpers ---

func aggregateUnderlyingStr(perChain []blend.PerChainBalance) string {
	total := decimal.Zero
	for _, pc := range perChain {
		if pc.TotalUnderlying != "" {
			if amt, err := decimal.NewFromString(pc.TotalUnderlying); err == nil {
				// Adjust for decimals if needed (USDC = 6 decimals, TotalUnderlying is already human-readable)
				total = total.Add(amt)
			}
		}
	}
	return total.StringFixed(6)
}

func getFloat(m map[string]float64) float64 {
	if m == nil {
		return 0
	}
	if v, ok := m["USD"]; ok {
		return v
	}
	// Fallback: sum all values
	var sum float64
	for _, v := range m {
		sum += v
	}
	return sum
}

func truncateStr(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
