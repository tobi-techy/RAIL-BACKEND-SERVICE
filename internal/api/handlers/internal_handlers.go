package handlers

import (
	"crypto/subtle"
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// InternalHandlers handles /internal/* ops endpoints.
// Authenticated via a dedicated INTERNAL_API_KEY, NOT the JWT signing secret.
type InternalHandlers struct {
	db         *sql.DB
	internalKey string
}

func NewInternalHandlers(db *sql.DB, internalKey string) *InternalHandlers {
	return &InternalHandlers{db: db, internalKey: internalKey}
}

func (h *InternalHandlers) authenticate(c *gin.Context) bool {
	if h.internalKey == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "internal API not configured"})
		return false
	}
	token := c.GetHeader("Authorization")
	if len(token) > 7 && token[:7] == "Bearer " {
		token = token[7:]
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(h.internalKey)) != 1 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return false
	}
	return true
}

// userLookupColumns is the fixed column list for user lookups.
const userLookupQuery = `SELECT id, email, first_name, last_name, kyc_status, bridge_kyc_status, is_active, alpaca_account_id, bridge_customer_id, created_at, updated_at FROM users WHERE `

// LookupUser handles GET /internal/users/lookup and GET /admin/users/lookup.
func (h *InternalHandlers) LookupUser(c *gin.Context) {
	if !h.authenticate(c) {
		return
	}
	h.doLookupUser(c)
}

// AdminLookupUser is the same lookup but relies on the admin auth middleware chain.
func AdminLookupUser(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		doLookupUser(c, db)
	}
}

func (h *InternalHandlers) doLookupUser(c *gin.Context) {
	doLookupUser(c, h.db)
}

func doLookupUser(c *gin.Context, db *sql.DB) {
	email := c.Query("email")
	uid := c.Query("id")
	if email == "" && uid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email or id query param required"})
		return
	}
	q := userLookupQuery + "email = $1"
	param := email
	if email == "" {
		q = userLookupQuery + "id = $1"
		param = uid
	}
	rows, err := db.QueryContext(c.Request.Context(), q, param)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database query failed", "request_id": c.GetString("request_id")})
		return
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	if !rows.Next() {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	vals := make([]interface{}, len(cols))
	ptrs := make([]interface{}, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read user data", "request_id": c.GetString("request_id")})
		return
	}
	result := make(map[string]interface{}, len(cols))
	for i, col := range cols {
		if b, ok := vals[i].([]byte); ok {
			result[col] = string(b)
		} else {
			result[col] = vals[i]
		}
	}
	c.JSON(http.StatusOK, gin.H{"user": result})
}

// allowedDeleteTables is the fixed whitelist of tables for cascade deletion.
var allowedDeleteTables = map[string]bool{
	"sessions": true, "notifications": true, "deposits": true,
	"ledger_entries": true, "ledger_transactions": true, "ledger_accounts": true,
	"allocation_events": true, "smart_allocation_modes": true,
	"virtual_accounts": true, "wallets": true, "cards": true, "user_settings": true,
	"auto_invest_events": true, "auto_invest_settings": true, "stash_lock_cycles": true,
	"yield_snapshots": true, "yield_distributions": true,
}

// deleteStatements maps each whitelisted table to a pre-built parameterized query.
var deleteStatements = func() map[string]string {
	m := make(map[string]string, len(allowedDeleteTables))
	for t := range allowedDeleteTables {
		m[t] = "DELETE FROM " + t + " WHERE user_id = $1"
	}
	return m
}()

// DeleteUser handles DELETE /internal/users/:id with safe parameterized queries.
func (h *InternalHandlers) DeleteUser(c *gin.Context) {
	if !h.authenticate(c) {
		return
	}
	uid := c.Param("id")
	deleted := map[string]int64{}
	for table, stmt := range deleteStatements {
		res, err := h.db.ExecContext(c.Request.Context(), stmt, uid)
		if err != nil {
			continue
		}
		n, _ := res.RowsAffected()
		if n > 0 {
			deleted[table] = n
		}
	}
	res, err := h.db.ExecContext(c.Request.Context(), "DELETE FROM users WHERE id = $1", uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete user", "request_id": c.GetString("request_id"), "deleted_related": deleted})
		return
	}
	n, _ := res.RowsAffected()
	c.JSON(http.StatusOK, gin.H{"deleted": n > 0, "user_id": uid, "related_rows_deleted": deleted})
}

// ManualCredit handles POST /internal/manual-credit
// Zeroes stale balance and credits a deposit. The allocation recovery worker handles the 70/30 split.
func (h *InternalHandlers) ManualCredit(c *gin.Context) {
	if !h.authenticate(c) {
		return
	}

	var req struct {
		UserID string `json:"user_id" binding:"required"`
		Amount string `json:"amount" binding:"required"`
		Chain  string `json:"chain" binding:"required"`
		TxHash string `json:"tx_hash" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	uid, err := uuid.Parse(req.UserID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user_id"})
		return
	}

	// Check for duplicate
	var count int
	h.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM deposits WHERE user_id = $1 AND tx_hash = $2", uid, req.TxHash).Scan(&count)
	if count > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "deposit already exists"})
		return
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "begin tx: " + err.Error()})
		return
	}
	defer tx.Rollback()

	// Zero out all existing balances for this user
	_, _ = tx.ExecContext(ctx, `UPDATE ledger_accounts SET balance = 0, updated_at = NOW() WHERE user_id = $1`, uid)

	// Get or create USDC account
	var usdcAccountID uuid.UUID
	err = tx.QueryRowContext(ctx, `SELECT id FROM ledger_accounts WHERE user_id = $1 AND account_type = 'usdc_balance'`, uid).Scan(&usdcAccountID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no usdc_balance account for user"})
		return
	}

	// Create deposit record
	depositID := uuid.New()
	idempotencyKey := fmt.Sprintf("manual-credit-%s-%s", req.UserID, req.TxHash)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO deposits (id, idempotency_key, user_id, chain, tx_hash, token, amount, status, confirmed_at, created_at)
		VALUES ($1, $2, $3, $4, $5, 'USDC', $6::decimal, 'confirmed', $7, $7)`,
		depositID, idempotencyKey, uid, req.Chain, req.TxHash, req.Amount, time.Now())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create deposit: " + err.Error()})
		return
	}

	// Create ledger transaction + entry
	creditTxID := uuid.New()
	creditKey := fmt.Sprintf("manual-deposit-credit-%s", depositID)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_transactions (id, user_id, transaction_type, reference_id, reference_type, idempotency_key, description, created_at)
		VALUES ($1, $2, 'deposit', $3, 'deposit', $4, 'Manual credit: stuck deposit', NOW())`,
		creditTxID, uid, depositID, creditKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create ledger tx: " + err.Error()})
		return
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, transaction_id, account_id, entry_type, amount, currency, description, created_at)
		VALUES ($1, $2, $3, 'debit', $4::decimal, 'USDC', 'Manual deposit credit', NOW())`,
		uuid.New(), creditTxID, usdcAccountID, req.Amount)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create ledger entry: " + err.Error()})
		return
	}

	// Update balance
	_, err = tx.ExecContext(ctx, `UPDATE ledger_accounts SET balance = $1::decimal, updated_at = NOW() WHERE id = $2`, req.Amount, usdcAccountID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "update balance: " + err.Error()})
		return
	}

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "commit: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":     "credited",
		"deposit_id": depositID,
		"amount":     req.Amount,
		"message":    "Allocation recovery worker will split 70/30 within ~15 seconds",
	})
}
