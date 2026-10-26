package handlers

import (
	"context"
	"database/sql"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	circlepkg "github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
)

// This file holds the support tooling for the class of incident where crypto
// arrives in a user's Rail-managed wallet but never reaches the ledger: the
// Circle inbound webhook was never delivered, or it failed before the deposit
// row was written. The money is real and on-chain, but the ledger says the user
// has nothing, so a withdrawal is refused and the in-app balance is zero.
//
// Two endpoints, deliberately separate:
//
//	GET  /internal/users/:id/crypto-status   read-only diagnosis
//	POST /internal/deposits/backfill         replay through the normal credit path
//
// The backfill never invents a credit. It calls the same
// FundingService.ProcessCircleDeposit the Circle webhook calls, which is
// idempotent on the transaction hash, so a replay either credits exactly once or
// reports that the deposit is already there. dry_run defaults to true: a caller
// has to opt in, explicitly, before any balance changes.

// CircleInboundSource is the read side of the Circle wallet provider needed to
// find an inbound transfer that never reached the ledger.
type CircleInboundSource interface {
	ListTransactions(ctx context.Context, walletID, operation, state string) ([]circlepkg.Transaction, error)
	GetTransaction(ctx context.Context, txID string) (*circlepkg.Transaction, error)
	GetTokenSymbol(ctx context.Context, walletID, tokenID string) (string, error)
	GetTokenBalanceOnchain(ctx context.Context, walletID string) ([]circlepkg.TokenBalance, error)
}

// DepositCreditor is the normal deposit credit path — the same one the Circle
// webhook uses.
type DepositCreditor interface {
	ProcessCircleDeposit(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, token entities.Stablecoin, chain, txHash, circleWalletID string) error
}

// opsWallet is one Rail-managed wallet row for the user being inspected.
type opsWallet struct {
	ID             string
	Chain          string
	Address        string
	CircleWalletID string
}

// opsDeposit is the deposit row that should exist for an inbound transfer.
type opsDeposit struct {
	ID     string
	Status string
	Amount string
	Chain  string
}

// opsSweep is the auto-sweep row that carries a non-Solana deposit to Solana.
type opsSweep struct {
	ID           string
	Status       string
	ErrorMessage string
	Attempts     int
}

// InternalOpsStore is the narrow persistence surface these diagnostics need.
// Kept as an interface so the handlers can be tested without a database.
type InternalOpsStore interface {
	WalletsForUser(ctx context.Context, userID uuid.UUID) ([]opsWallet, error)
	DepositByTxHash(ctx context.Context, txHash string) (*opsDeposit, error)
	SweepByDepositID(ctx context.Context, depositID string) (*opsSweep, error)
	LedgerBalances(ctx context.Context, userID uuid.UUID) (map[string]string, error)
}

// SQLInternalOpsStore is the production InternalOpsStore.
type SQLInternalOpsStore struct {
	db *sql.DB
}

// NewSQLInternalOpsStore builds the Postgres-backed ops store.
func NewSQLInternalOpsStore(db *sql.DB) *SQLInternalOpsStore {
	return &SQLInternalOpsStore{db: db}
}

// WalletsForUser returns the user's managed wallets, preferring the current
// managed_wallets table and falling back to the legacy wallets table so older
// accounts are not invisible. Rows are de-duplicated by Circle wallet id.
func (s *SQLInternalOpsStore) WalletsForUser(ctx context.Context, userID uuid.UUID) ([]opsWallet, error) {
	const q = `
		SELECT id::text, chain, address, COALESCE(circle_wallet_id, '')
		  FROM managed_wallets
		 WHERE user_id = $1
		UNION ALL
		SELECT id::text, chain, address, COALESCE(circle_wallet_id, '')
		  FROM wallets
		 WHERE user_id = $1`

	rows, err := s.db.QueryContext(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []opsWallet
	seen := make(map[string]bool)
	for rows.Next() {
		var w opsWallet
		if err := rows.Scan(&w.ID, &w.Chain, &w.Address, &w.CircleWalletID); err != nil {
			return nil, err
		}
		w.Chain = strings.ToUpper(strings.TrimSpace(w.Chain))
		key := w.CircleWalletID
		if key == "" {
			key = w.Chain + ":" + strings.ToLower(w.Address)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, w)
	}
	return out, rows.Err()
}

// DepositByTxHash returns the deposit row for a transaction hash, or nil.
func (s *SQLInternalOpsStore) DepositByTxHash(ctx context.Context, txHash string) (*opsDeposit, error) {
	const q = `SELECT id::text, status, amount::text, chain FROM deposits WHERE tx_hash = $1 ORDER BY created_at DESC LIMIT 1`
	var d opsDeposit
	err := s.db.QueryRowContext(ctx, q, txHash).Scan(&d.ID, &d.Status, &d.Amount, &d.Chain)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// SweepByDepositID returns the auto-sweep row for a deposit, or nil.
func (s *SQLInternalOpsStore) SweepByDepositID(ctx context.Context, depositID string) (*opsSweep, error) {
	const q = `SELECT id::text, status, COALESCE(error_message, ''), attempts FROM deposit_sweeps WHERE deposit_id = $1 ORDER BY created_at DESC LIMIT 1`
	var sw opsSweep
	err := s.db.QueryRowContext(ctx, q, depositID).Scan(&sw.ID, &sw.Status, &sw.ErrorMessage, &sw.Attempts)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sw, nil
}

// LedgerBalances returns the user's spendable account balances keyed by account
// type, so an operator can see whether a credit actually landed and how the
// 70/30 split distributed it.
func (s *SQLInternalOpsStore) LedgerBalances(ctx context.Context, userID uuid.UUID) (map[string]string, error) {
	const q = `SELECT account_type, balance::text FROM ledger_accounts WHERE user_id = $1`
	rows, err := s.db.QueryContext(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]string)
	for rows.Next() {
		var accountType, balance string
		if err := rows.Scan(&accountType, &balance); err != nil {
			return nil, err
		}
		out[accountType] = balance
	}
	return out, rows.Err()
}

// DepositBackfillHandlers diagnoses and repairs crypto deposits that arrived
// on-chain but never reached the ledger.
type DepositBackfillHandlers struct {
	store    InternalOpsStore
	circle   CircleInboundSource
	creditor DepositCreditor
	// chainrailsConfigured reports whether cross-chain bridging (and therefore
	// the Base -> Solana auto-sweep and cross-chain withdrawal) is available.
	chainrailsConfigured bool
	logger               *zap.Logger
}

// NewDepositBackfillHandlers builds the ops recovery handlers.
func NewDepositBackfillHandlers(store InternalOpsStore, circle CircleInboundSource, creditor DepositCreditor, chainrailsConfigured bool, logger *zap.Logger) *DepositBackfillHandlers {
	return &DepositBackfillHandlers{
		store:                store,
		circle:               circle,
		creditor:             creditor,
		chainrailsConfigured: chainrailsConfigured,
		logger:               logger,
	}
}

// walletStatus is the per-wallet view returned by the diagnosis endpoint.
type walletStatus struct {
	WalletID       string   `json:"wallet_id"`
	Chain          string   `json:"chain"`
	Address        string   `json:"address"`
	CircleWalletID string   `json:"circle_wallet_id"`
	USDCOnchain    string   `json:"usdc_onchain,omitempty"`
	Inbound        []gin.H  `json:"inbound,omitempty"`
	Errors         []string `json:"errors,omitempty"`
}

// CryptoStatus handles GET /internal/users/:id/crypto-status.
//
// Read-only. Reports, per Rail-managed wallet: the on-chain USDC balance, the
// recent inbound Circle transfers, whether each one has a deposit row, whether
// it was credited, and whether a sweep to Solana exists. This is the call that
// answers "where is the money and why is the ledger empty" without moving
// anything.
func (h *DepositBackfillHandlers) CryptoStatus(c *gin.Context) {
	userID, ok := parseOpsUserID(c)
	if !ok {
		return
	}
	if h.circle == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "circle provider is not configured"})
		return
	}

	wallets, err := h.store.WalletsForUser(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("crypto-status: wallet lookup failed", zap.Error(err), zap.String("user_id", userID.String()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "wallet lookup failed"})
		return
	}

	balances, err := h.store.LedgerBalances(c.Request.Context(), userID)
	if err != nil {
		h.logger.Warn("crypto-status: ledger balances unavailable", zap.Error(err), zap.String("user_id", userID.String()))
		balances = map[string]string{}
	}

	statuses := h.inspectWallets(c.Request.Context(), wallets, 0, true)

	c.JSON(http.StatusOK, gin.H{
		"user_id":               userID.String(),
		"chainrails_configured": h.chainrailsConfigured,
		"ledger_balances":       balances,
		"wallets":               statuses,
	})
}

// BackfillRequest is the payload for a deposit backfill.
type BackfillRequest struct {
	UserID string `json:"user_id" binding:"required"`
	// DryRun defaults to TRUE. Balance only changes when this is explicitly false.
	DryRun *bool `json:"dry_run,omitempty"`
	// LookbackDays bounds how far back to look. Defaults to a year, capped at
	// 730. Stuck deposits are often months old by the time anyone notices.
	LookbackDays int `json:"lookback_days,omitempty"`
	// Chain optionally restricts the replay to a single chain (e.g. "BASE").
	Chain string `json:"chain,omitempty"`
	// CircleTxIDs targets specific Circle transaction ids instead of scanning
	// each wallet's recent history. Use it when ops already has the transaction
	// in hand: the per-wallet scan is capped at five recent transfers, so an old
	// deposit can fall outside the page even though the lookback would include
	// it. Each id is ownership-checked against the user's wallets before credit.
	CircleTxIDs []string `json:"circle_tx_ids,omitempty"`
}

// inboundCandidate is one Circle inbound transfer considered for replay.
type inboundCandidate struct {
	CircleTxID      string `json:"circle_tx_id"`
	TxHash          string `json:"tx_hash,omitempty"`
	Chain           string `json:"chain"`
	CircleWalletID  string `json:"circle_wallet_id,omitempty"`
	Amount          string `json:"amount"`
	Token           string `json:"token,omitempty"`
	State           string `json:"state"`
	CreatedAt       string `json:"created_at,omitempty"`
	Destination     string `json:"destination,omitempty"`
	AlreadyCredited bool   `json:"already_credited"`
	DepositID       string `json:"deposit_id,omitempty"`
	DepositStatus   string `json:"deposit_status,omitempty"`
	SweepStatus     string `json:"sweep_status,omitempty"`
	Action          string `json:"action"`
	Reason          string `json:"reason,omitempty"`
}

// BackfillDeposits handles POST /internal/deposits/backfill.
//
// It replays Circle inbound transactions through ProcessCircleDeposit — the
// same idempotent path the webhook uses — so the ledger ends up exactly as it
// would have if the webhook had been delivered. Dry run by default.
func (h *DepositBackfillHandlers) BackfillDeposits(c *gin.Context) {
	var req BackfillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}

	userID, err := uuid.Parse(strings.TrimSpace(req.UserID))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user_id"})
		return
	}
	if h.circle == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "circle provider is not configured"})
		return
	}

	dryRun := true
	if req.DryRun != nil {
		dryRun = *req.DryRun
	}
	lookback := req.LookbackDays
	if lookback <= 0 {
		lookback = 365
	}
	if lookback > 730 {
		lookback = 730
	}
	chainFilter := strings.ToUpper(strings.TrimSpace(req.Chain))

	wallets, err := h.store.WalletsForUser(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("deposit-backfill: wallet lookup failed", zap.Error(err), zap.String("user_id", userID.String()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "wallet lookup failed"})
		return
	}

	var candidates []inboundCandidate
	if len(req.CircleTxIDs) > 0 {
		// Explicit ids win: they are exact, ownership-checked, and immune to the
		// five-per-wallet page limit that can hide an older transfer.
		candidates = h.candidatesFromTxIDs(c.Request.Context(), userID, wallets, req.CircleTxIDs)
	} else {
		candidates = h.collectCandidates(c.Request.Context(), wallets, chainFilter, time.Duration(lookback)*24*time.Hour, !dryRun)
	}

	credited := 0

	// Replay in a second pass so the decision to credit is made in exactly one
	// place, and a dry run never reaches it.
	if !dryRun {
		for i := range candidates {
			cand := &candidates[i]
			if cand.Action != "would_credit" {
				continue
			}
			amount, parseErr := decimal.NewFromString(cand.Amount)
			if parseErr != nil || amount.LessThanOrEqual(decimal.Zero) {
				cand.Action = "skipped"
				cand.Reason = "unparseable amount"
				continue
			}
			token := entities.Stablecoin(strings.ToUpper(cand.Token))
			if !token.IsValid() {
				cand.Action = "skipped"
				cand.Reason = "unsupported token " + cand.Token
				continue
			}
			if err := h.creditor.ProcessCircleDeposit(c.Request.Context(), userID, amount, token, cand.Chain, cand.TxHash, cand.CircleWalletID); err != nil {
				cand.Action = "failed"
				cand.Reason = err.Error()
				h.logger.Error("deposit-backfill: credit failed",
					zap.Error(err),
					zap.String("user_id", userID.String()),
					zap.String("tx_hash", cand.TxHash),
					zap.String("chain", cand.Chain),
					zap.String("amount", cand.Amount))
				continue
			}
			cand.Action = "credited"
			h.logger.Warn("DEPOSIT BACKFILL credited",
				zap.String("user_id", userID.String()),
				zap.String("tx_hash", cand.TxHash),
				zap.String("chain", cand.Chain),
				zap.String("amount", cand.Amount),
				zap.String("actor", c.GetHeader("X-Internal-Actor")),
				zap.String("caller_ip", c.ClientIP()))
		}

		// Re-read deposit/sweep state so the response reflects what actually
		// happened rather than what the processor claimed.
		h.annotateDepositState(c.Request.Context(), candidates)
		for i := range candidates {
			if candidates[i].Action == "credited" {
				credited++
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"dry_run":               dryRun,
		"user_id":               userID.String(),
		"chainrails_configured": h.chainrailsConfigured,
		"candidates":            candidates,
		"credited_count":        credited,
		"note":                  "dry_run defaults to true; send {\"dry_run\": false} to replay these transfers into the ledger",
	})
}

// inspectWallets builds the per-wallet diagnosis. When withInbound is true it
// also lists recent inbound Circle transfers and resolves their deposit state.
func (h *DepositBackfillHandlers) inspectWallets(ctx context.Context, wallets []opsWallet, limit int, withInbound bool) []walletStatus {
	out := make([]walletStatus, len(wallets))
	var wg sync.WaitGroup

	for i := range wallets {
		w := wallets[i]
		out[i] = walletStatus{
			WalletID:       w.ID,
			Chain:          w.Chain,
			Address:        w.Address,
			CircleWalletID: w.CircleWalletID,
		}
		if w.CircleWalletID == "" {
			continue
		}
		wg.Add(1)
		go func(idx int, wallet opsWallet) {
			defer wg.Done()
			balances, err := h.circle.GetTokenBalanceOnchain(ctx, wallet.CircleWalletID)
			if err != nil {
				out[idx].Errors = append(out[idx].Errors, "balance: "+err.Error())
				return
			}
			out[idx].USDCOnchain = usdcAmountFrom(balances)
			if !withInbound || decimalStringIsZero(out[idx].USDCOnchain) {
				return
			}
			txs, err := h.circle.ListTransactions(ctx, wallet.CircleWalletID, "INBOUND", "")
			if err != nil {
				out[idx].Errors = append(out[idx].Errors, "transactions: "+err.Error())
				return
			}
			for _, tx := range txs {
				dep, _ := h.store.DepositByTxHash(ctx, tx.TxHash)
				row := gin.H{
					"circle_tx_id":     tx.ID,
					"tx_hash":          tx.TxHash,
					"state":            string(tx.State),
					"amount":           firstAmount(tx.Amounts),
					"created_at":       tx.CreateDate.UTC().Format(time.RFC3339),
					"destination":      tx.DestinationAddress,
					"already_credited": dep != nil && dep.Status == "confirmed",
				}
				if dep != nil {
					row["deposit_id"] = dep.ID
					row["deposit_status"] = dep.Status
					if sweep, _ := h.store.SweepByDepositID(ctx, dep.ID); sweep != nil {
						row["sweep_status"] = sweep.Status
					}
				}
				out[idx].Inbound = append(out[idx].Inbound, row)
			}
		}(i, w)
	}
	wg.Wait()
	return out
}

// collectCandidates lists inbound transfers per wallet and decides, without
// moving anything, whether each one still needs a ledger credit.
func (h *DepositBackfillHandlers) collectCandidates(ctx context.Context, wallets []opsWallet, chainFilter string, lookback time.Duration, resolveToken bool) []inboundCandidate {
	cutoff := time.Now().Add(-lookback)
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		out  []inboundCandidate
		seen = make(map[string]bool)
	)

	for _, w := range wallets {
		if w.CircleWalletID == "" {
			continue
		}
		if chainFilter != "" && !strings.EqualFold(w.Chain, chainFilter) {
			continue
		}
		wg.Add(1)
		go func(wallet opsWallet) {
			defer wg.Done()
			txs, err := h.circle.ListTransactions(ctx, wallet.CircleWalletID, "INBOUND", "")
			if err != nil {
				h.logger.Warn("deposit-backfill: list transactions failed",
					zap.Error(err), zap.String("chain", wallet.Chain), zap.String("circle_wallet_id", wallet.CircleWalletID))
				return
			}
			var local []inboundCandidate
			for _, tx := range txs {
				if !tx.CreateDate.IsZero() && tx.CreateDate.Before(cutoff) {
					continue
				}
				cand, ok := h.candidateForTx(ctx, wallet, tx, resolveToken)
				if !ok {
					continue
				}
				local = append(local, cand)
			}
			mu.Lock()
			for _, cand := range local {
				key := cand.TxHash
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, cand)
			}
			mu.Unlock()
		}(w)
	}
	wg.Wait()

	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt == out[j].CreatedAt {
			return out[i].Chain < out[j].Chain
		}
		return out[i].CreatedAt < out[j].CreatedAt
	})
	return out
}

// candidateForTx classifies one Circle inbound transfer against the deposit
// table. It is a pure read: it decides whether a credit is missing, it never
// writes. Returns false when the transfer is not settled money (a failed state,
// no hash, or a zero amount), which the caller should not report on.
func (h *DepositBackfillHandlers) candidateForTx(ctx context.Context, wallet opsWallet, tx circlepkg.Transaction, resolveToken bool) (inboundCandidate, bool) {
	if !inboundStateCounts(tx.State) {
		return inboundCandidate{}, false
	}
	amount := firstAmount(tx.Amounts)
	if decimalStringIsZero(amount) || tx.TxHash == "" {
		return inboundCandidate{}, false
	}

	cand := inboundCandidate{
		CircleTxID:     tx.ID,
		TxHash:         tx.TxHash,
		Chain:          wallet.Chain,
		CircleWalletID: wallet.CircleWalletID,
		Amount:         amount,
		State:          string(tx.State),
		CreatedAt:      tx.CreateDate.UTC().Format(time.RFC3339),
		Destination:    tx.DestinationAddress,
		Token:          "USDC",
	}
	if resolveToken {
		if symbol := h.resolveTokenSymbol(ctx, wallet.CircleWalletID, tx.TokenID); symbol != "" {
			cand.Token = symbol
		}
	}

	dep, depErr := h.store.DepositByTxHash(ctx, tx.TxHash)
	if depErr != nil {
		cand.Action = "skipped"
		cand.Reason = "deposit lookup failed: " + depErr.Error()
		return cand, true
	}
	if dep != nil {
		cand.AlreadyCredited = dep.Status == "confirmed"
		cand.DepositID = dep.ID
		cand.DepositStatus = dep.Status
		if sweep, _ := h.store.SweepByDepositID(ctx, dep.ID); sweep != nil {
			cand.SweepStatus = sweep.Status
		}
		cand.Action = "skipped"
		// A confirmed row means the credit landed. Any other status (pending,
		// failed) is reported as-is because ProcessCircleDeposit will only
		// reconcile a pending row — a failed one needs a human decision.
		cand.Reason = "deposit row already exists with status " + dep.Status
		return cand, true
	}

	cand.Action = "would_credit"
	cand.Reason = "no deposit row for this transaction hash"
	return cand, true
}

// candidatesFromTxIDs resolves specific Circle transaction ids. Used when ops
// already has the transaction in hand and the per-wallet scan might not page
// far enough back to reach it.
//
// Every id is ownership-checked against the target user's wallets before it is
// considered, so a mistyped or borrowed transaction id can never credit the
// wrong account.
func (h *DepositBackfillHandlers) candidatesFromTxIDs(ctx context.Context, userID uuid.UUID, wallets []opsWallet, ids []string) []inboundCandidate {
	out := make([]inboundCandidate, 0, len(ids))
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		tx, err := h.circle.GetTransaction(ctx, id)
		if err != nil {
			out = append(out, inboundCandidate{
				CircleTxID: id,
				Action:     "skipped",
				Reason:     "circle lookup failed: " + err.Error(),
			})
			continue
		}
		if tx == nil {
			out = append(out, inboundCandidate{CircleTxID: id, Action: "skipped", Reason: "circle returned no transaction"})
			continue
		}
		wallet, owned := walletForTx(wallets, tx)
		if !owned {
			out = append(out, inboundCandidate{
				CircleTxID: id,
				TxHash:     tx.TxHash,
				Action:     "skipped",
				Reason:     "transaction does not belong to any wallet of user " + userID.String(),
			})
			continue
		}
		cand, ok := h.candidateForTx(ctx, wallet, *tx, true)
		if !ok {
			out = append(out, inboundCandidate{
				CircleTxID: id,
				TxHash:     tx.TxHash,
				Chain:      wallet.Chain,
				Action:     "skipped",
				Reason:     "not a settled inbound transfer (state " + string(tx.State) + ", needs a hash and a positive amount)",
			})
			continue
		}
		out = append(out, cand)
	}
	return out
}

// walletForTx finds which of the user's wallets a Circle transaction belongs to.
func walletForTx(wallets []opsWallet, tx *circlepkg.Transaction) (opsWallet, bool) {
	for _, w := range wallets {
		if w.CircleWalletID != "" && strings.EqualFold(w.CircleWalletID, tx.WalletID) {
			return w, true
		}
	}
	for _, w := range wallets {
		if strings.TrimSpace(w.Address) != "" && strings.EqualFold(w.Address, tx.DestinationAddress) {
			return w, true
		}
	}
	return opsWallet{}, false
}

// annotateDepositState refreshes deposit/sweep fields after a live replay, and
// downgrades a claimed credit that left no deposit row behind — which is what
// the processor's duplicate user+amount guard looks like from the outside.
func (h *DepositBackfillHandlers) annotateDepositState(ctx context.Context, candidates []inboundCandidate) {
	for i := range candidates {
		cand := &candidates[i]
		if cand.TxHash == "" {
			continue
		}
		dep, err := h.store.DepositByTxHash(ctx, cand.TxHash)
		if err != nil {
			continue
		}
		if dep == nil {
			if cand.Action == "credited" {
				cand.Action = "skipped"
				cand.Reason = "processor reported success but no deposit row exists for this tx hash; it was probably credited under a different hash (duplicate user+amount guard)"
			}
			continue
		}
		cand.AlreadyCredited = dep.Status == "confirmed"
		cand.DepositID = dep.ID
		cand.DepositStatus = dep.Status
		if sweep, _ := h.store.SweepByDepositID(ctx, dep.ID); sweep != nil {
			cand.SweepStatus = sweep.Status
		}
	}
}

// resolveTokenSymbol names the token behind a Circle token UUID. Both the
// indexed balance list and the on-chain (includeAll) view are consulted because
// a bridged/stuck token can be missing from the indexed one — which is exactly
// the case this tooling exists for.
func (h *DepositBackfillHandlers) resolveTokenSymbol(ctx context.Context, walletID, tokenID string) string {
	if symbol, err := h.circle.GetTokenSymbol(ctx, walletID, tokenID); err == nil && symbol != "" {
		return strings.ToUpper(symbol)
	}
	if balances, err := h.circle.GetTokenBalanceOnchain(ctx, walletID); err == nil {
		for _, b := range balances {
			if strings.EqualFold(b.Token.ID, tokenID) && b.Token.Symbol != "" {
				return strings.ToUpper(b.Token.Symbol)
			}
		}
	}
	return ""
}

// parseOpsUserID reads and validates the :id path parameter.
func parseOpsUserID(c *gin.Context) (uuid.UUID, bool) {
	userID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return uuid.Nil, false
	}
	return userID, true
}

// inboundStateCounts reports whether an inbound transaction represents settled
// money. Failed and cancelled transfers are ignored.
func inboundStateCounts(state circlepkg.TransactionState) bool {
	switch strings.ToUpper(string(state)) {
	case "COMPLETE", "CONFIRMED", "":
		return true
	default:
		return false
	}
}

// firstAmount returns the first amount from a Circle transfer, or "".
func firstAmount(amounts []string) string {
	if len(amounts) == 0 {
		return ""
	}
	return amounts[0]
}

// usdcAmountFrom sums the USDC balance in a token balance list.
func usdcAmountFrom(balances []circlepkg.TokenBalance) string {
	total := decimal.Zero
	for _, b := range balances {
		if !strings.EqualFold(b.Token.Symbol, "USDC") {
			continue
		}
		amt, err := decimal.NewFromString(b.Amount)
		if err != nil {
			continue
		}
		total = total.Add(amt)
	}
	return total.String()
}

// decimalStringIsZero reports whether a decimal string is zero, empty, or
// unparseable.
func decimalStringIsZero(s string) bool {
	if s == "" {
		return true
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return true
	}
	return d.LessThanOrEqual(decimal.Zero)
}
