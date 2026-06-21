package blend

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// BackfillResult summarizes a one-time stash backfill run.
type BackfillResult struct {
	UsersScanned  int
	RoutesCreated int // new backfill routes inserted
	Skipped       int
	Errors        int
}

// BackfillAllStash is a ONE-TIME migration entrypoint: it routes every user's CURRENT
// stash balance into Blend. For each user with a positive stash_balance ledger account
// it creates (idempotently) a single backfill route for the portion not already in Blend
// and drives it through the normal state machine (account → Safe → bridge funding → deposit).
//
// Idempotent and safe to re-run: routes are keyed by external_ref `backfill-<userID>`, and
// the amount is the stash balance minus whatever is already routed, so re-runs don't
// double-route. Funds that aren't physically present yet simply park (fail-safe).
func (r *DepositRouter) BackfillAllStash(ctx context.Context) (*BackfillResult, error) {
	if r == nil || r.db == nil {
		return nil, errNilRouter
	}
	res := &BackfillResult{}

	rows, err := r.db.QueryxContext(ctx, `
		SELECT user_id, balance
		FROM ledger_accounts
		WHERE account_type = 'stash_balance' AND balance > 0
		ORDER BY user_id
	`)
	if err != nil {
		return nil, fmt.Errorf("blend backfill: enumerate stash balances: %w", err)
	}
	type row struct {
		UserID  uuid.UUID       `db:"user_id"`
		Balance decimal.Decimal `db:"balance"`
	}
	var users []row
	for rows.Next() {
		var x row
		if err := rows.StructScan(&x); err != nil {
			rows.Close()
			return nil, fmt.Errorf("blend backfill: scan: %w", err)
		}
		users = append(users, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, u := range users {
		select {
		case <-ctx.Done():
			return res, ctx.Err()
		default:
		}
		res.UsersScanned++
		created, err := r.BackfillUserStash(ctx, u.UserID, u.Balance)
		switch {
		case err != nil:
			res.Errors++
			r.logger.Warn("Blend backfill: user failed (will not block others)",
				zap.String("user_id", u.UserID.String()), zap.Error(err))
		case created:
			res.RoutesCreated++
		default:
			res.Skipped++
		}
	}
	r.logger.Info("Blend stash backfill complete",
		zap.Int("users_scanned", res.UsersScanned),
		zap.Int("routes_created", res.RoutesCreated),
		zap.Int("skipped", res.Skipped),
		zap.Int("errors", res.Errors))
	return res, nil
}

// BackfillUserStash creates (idempotently) and drives a single backfill route for one user.
// Returns created=true when a new backfill route was inserted this call.
func (r *DepositRouter) BackfillUserStash(ctx context.Context, userID uuid.UUID, stashBalance decimal.Decimal) (bool, error) {
	if userID == uuid.Nil {
		return false, fmt.Errorf("blend backfill: user_id required")
	}
	stashBalance = stashBalance.Truncate(6)
	if !stashBalance.GreaterThan(decimal.Zero) {
		return false, nil
	}
	externalRef := "backfill-" + userID.String()

	// If a backfill route already exists, just advance it (resume) — don't recreate.
	if existing, err := r.getRouteByExternalRef(ctx, externalRef); err == nil && existing != nil {
		return false, r.processRoute(ctx, existing)
	} else if err != nil {
		return false, err
	}

	// Amount to backfill = current stash minus whatever is already in the Blend pipeline
	// for this user (non-terminal or settled routes), so we never double-route.
	// Use a transaction to prevent race conditions between reading and inserting.
	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return false, fmt.Errorf("blend backfill: begin tx: %w", err)
	}
	defer tx.Rollback()

	var alreadyRouted decimal.Decimal
	if err := tx.GetContext(ctx, &alreadyRouted, `
		SELECT COALESCE(SUM(amount), 0)
		FROM blend_deposit_routes
		WHERE user_id = $1 AND status NOT IN ('error_terminal', 'error_payload')
	`, userID); err != nil {
		return false, fmt.Errorf("blend backfill: read already-routed: %w", err)
	}
	amount := stashBalance.Sub(alreadyRouted)
	if !amount.GreaterThan(decimal.Zero) {
		return false, nil // already fully covered
	}

	baseWallet, err := r.resolveUserBaseWallet(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("blend backfill: resolve base wallet: %w", err)
	}
	solWallet, err := r.resolveUserSolanaWallet(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("blend backfill: resolve funding (solana) wallet: %w", err)
	}

	micro := USDCMicroUnits(amount)
	res, err := tx.ExecContext(ctx, `
		INSERT INTO blend_deposit_routes (
			id, deposit_id, user_id, blend_account_id, eoa_address,
			circle_wallet_id, chain_id, input_asset, amount, amount_units,
			source_circle_wallet_id, source_chain, source,
			status, next_retry_at, external_ref
		) VALUES ($1, NULL, $2, '', $3, $4, $5, $6, $7, $8, $9, 'SOL', 'backfill', $10, NOW(), $11)
		ON CONFLICT (external_ref) DO NOTHING
	`,
		uuid.New(), userID, baseWallet.Address, baseWallet.CircleWalletID, r.chainID,
		r.usdcAddr, amount, micro.String(), solWallet.CircleWalletID, routeStatusPending, externalRef,
	)
	if err != nil {
		return false, fmt.Errorf("blend backfill: insert route: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Lost a race; fetch and advance.
		if existing, gerr := r.getRouteByExternalRef(ctx, externalRef); gerr == nil && existing != nil {
			return false, r.processRoute(ctx, existing)
		}
		return false, nil
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("blend backfill: commit tx: %w", err)
	}

	route, err := r.getRouteByExternalRef(ctx, externalRef)
	if err != nil {
		return true, err
	}
	r.logger.Info("Blend backfill route created",
		zap.String("user_id", userID.String()),
		zap.String("amount", amount.StringFixed(6)),
		zap.String("source_wallet", solWallet.CircleWalletID))
	return true, r.processRoute(ctx, route)
}

func (r *DepositRouter) getRouteByExternalRef(ctx context.Context, externalRef string) (*depositRoute, error) {
	var route depositRoute
	err := r.db.GetContext(ctx, &route, depositRouteSelect+` WHERE external_ref = $1`, externalRef)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("blend: get route by external_ref: %w", err)
	}
	return &route, nil
}

// resolveUserSolanaWallet finds the user's Solana Circle wallet (their primary USDC custody,
// the sweep target) to use as the bridge funding source for a backfill.
func (r *DepositRouter) resolveUserSolanaWallet(ctx context.Context, userID uuid.UUID) (baseWallet, error) {
	solChains := []string{"SOL", "SOL-DEVNET", "SOLANA"}
	var dbWallet struct {
		CircleWalletID sql.NullString `db:"circle_wallet_id"`
		Address        sql.NullString `db:"address"`
	}
	for _, table := range []string{"managed_wallets", "wallets"} {
		var query string
		switch table {
		case "managed_wallets":
			query = `
			SELECT circle_wallet_id, address
			FROM managed_wallets
			WHERE user_id = $1
				AND UPPER(chain) = ANY($2)
				AND COALESCE(circle_wallet_id, '') <> ''
				AND COALESCE(address, '') <> ''
			ORDER BY CASE WHEN status = 'live' THEN 0 ELSE 1 END, created_at ASC
			LIMIT 1
		`
		case "wallets":
			query = `
			SELECT circle_wallet_id, address
			FROM wallets
			WHERE user_id = $1
				AND UPPER(chain) = ANY($2)
				AND COALESCE(circle_wallet_id, '') <> ''
				AND COALESCE(address, '') <> ''
			ORDER BY CASE WHEN status = 'live' THEN 0 ELSE 1 END, created_at ASC
			LIMIT 1
		`
		default:
			continue
		}
		if err := r.db.GetContext(ctx, &dbWallet, query, userID.String(), pq.Array(solChains)); err == nil {
			return baseWallet{
				CircleWalletID: strings.TrimSpace(dbWallet.CircleWalletID.String),
				Address:        strings.TrimSpace(dbWallet.Address.String),
			}, nil
		} else if err != sql.ErrNoRows {
			r.logger.Debug("blend backfill: solana wallet lookup miss", zap.String("table", table), zap.Error(err))
		}
	}
	wallets, err := r.circle.ListCircleWalletsByRefID(ctx, userID.String())
	if err != nil {
		return baseWallet{}, fmt.Errorf("list circle wallets for user %s: %w", userID, err)
	}
	for _, w := range wallets {
		bc := strings.ToUpper(strings.TrimSpace(string(w.Blockchain)))
		if (bc == "SOL" || bc == "SOL-DEVNET") && strings.TrimSpace(w.ID) != "" && strings.TrimSpace(w.Address) != "" {
			return baseWallet{CircleWalletID: w.ID, Address: w.Address}, nil
		}
	}
	return baseWallet{}, fmt.Errorf("user %s has no Solana Circle wallet to fund backfill from", userID)
}
