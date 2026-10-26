package handlers

import (
	"crypto/subtle"
	"database/sql"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// InternalHandlers handles /internal/* ops endpoints.
// Authenticated via a dedicated INTERNAL_API_KEY, NOT the JWT signing secret.
type InternalHandlers struct {
	db          *sql.DB
	internalKey string
	logger      *zap.Logger
}

func NewInternalHandlers(db *sql.DB, internalKey string, logger *zap.Logger) *InternalHandlers {
	return &InternalHandlers{db: db, internalKey: internalKey, logger: logger}
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
const userLookupQuery = `SELECT id, email, first_name, last_name, kyc_status, bridge_kyc_status, is_active, bridge_customer_id, created_at, updated_at FROM users WHERE `

// walletLookupQuery resolves a user from anything an operator has in hand: an
// on-chain address, a Circle wallet id, a Bridge wallet id, or a Rail wallet
// row id. Both the current (managed_wallets) and legacy (wallets) tables are
// searched so old accounts are not invisible.
const walletLookupQuery = `id IN (
	SELECT user_id FROM managed_wallets
	 WHERE LOWER(address) = LOWER($1)
	    OR circle_wallet_id = $1
	    OR bridge_wallet_id = $1
	    OR id::text = $1
	UNION
	SELECT user_id FROM wallets
	 WHERE LOWER(address) = LOWER($1)
	    OR circle_wallet_id = $1
	    OR id::text = $1
)`

// LookupUser handles GET /internal/users/lookup and GET /admin/users/lookup.
// Accepts ?email=, ?id=, or ?wallet= (address / provider wallet id / wallet row id).
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
	walletRef := strings.TrimSpace(c.Query("wallet"))
	if email == "" && uid == "" && walletRef == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email, id, or wallet query param required"})
		return
	}
	q := userLookupQuery + "email = $1"
	param := email
	switch {
	case email != "":
	case uid != "":
		q = userLookupQuery + "id = $1"
		param = uid
	default:
		q = userLookupQuery + walletLookupQuery + " LIMIT 1"
		param = walletRef
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
	response := gin.H{"user": result}
	if walletRef != "" {
		// Echo the matching wallets so the operator can see the chain and
		// provider ids they will need for a recovery call.
		response["wallets"] = matchingWallets(c, db, walletRef)
	}
	c.JSON(http.StatusOK, response)
}

// matchingWallets returns the wallet rows (current + legacy) that matched a
// wallet reference. Best effort: a failure here never fails the user lookup.
func matchingWallets(c *gin.Context, db *sql.DB, ref string) []map[string]interface{} {
	queries := []string{
		`SELECT id::text, chain, address, COALESCE(circle_wallet_id, ''), COALESCE(bridge_wallet_id, '')
		   FROM managed_wallets
		  WHERE LOWER(address) = LOWER($1) OR circle_wallet_id = $1 OR bridge_wallet_id = $1 OR id::text = $1
		  LIMIT 5`,
		`SELECT id::text, chain, address, COALESCE(circle_wallet_id, ''), '' AS bridge_wallet_id
		   FROM wallets
		  WHERE LOWER(address) = LOWER($1) OR circle_wallet_id = $1 OR id::text = $1
		  LIMIT 5`,
	}
	var out []map[string]interface{}
	for _, q := range queries {
		rows, err := db.QueryContext(c.Request.Context(), q, ref)
		if err != nil {
			continue
		}
		for rows.Next() {
			var id, chain, address, circleWalletID, bridgeWalletID string
			if err := rows.Scan(&id, &chain, &address, &circleWalletID, &bridgeWalletID); err != nil {
				continue
			}
			out = append(out, map[string]interface{}{
				"id":               id,
				"chain":            chain,
				"address":          address,
				"circle_wallet_id": circleWalletID,
				"bridge_wallet_id": bridgeWalletID,
			})
		}
		rows.Close()
	}
	return out
}

// CompleteStuckPajOrders marks stuck PAJ offramp orders as completed/failed.
// POST /internal/paj-orders/complete-stuck
//
// This moves NO ledger funds — it only flips order state, so it must be used
// after verifying provider truth out-of-band (e.g. Paj dashboard shows NGN
// delivered but the webhook never landed and no session exists to re-verify).
// Guardrails: a reason is required (audit), the age window is capped, orders
// already claimed for credit/reversal (deposit_id set) are never touched, and
// every flipped order id is returned + logged.
func (h *InternalHandlers) CompleteStuckPajOrders(c *gin.Context) {
	var req struct {
		MaxAgeHours int      `json:"max_age_hours"`
		Status      string   `json:"status"` // "completed" or "failed"
		Reason      string   `json:"reason"`
		OrderIDs    []string `json:"order_ids"` // optional: scope to explicit orders
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body (need status, reason, max_age_hours)"})
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "reason is required for manual state flips"})
		return
	}
	if req.MaxAgeHours < 1 {
		req.MaxAgeHours = 1
	}
	if req.MaxAgeHours > 72 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "max_age_hours capped at 72 — scope with order_ids for older rows"})
		return
	}
	targetStatus := "completed"
	if req.Status == "failed" {
		targetStatus = "failed"
	} else if req.Status != "" && req.Status != "completed" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "status must be completed or failed"})
		return
	}

	webhookStatus := fmt.Sprintf("manual-%s:admin-api:%s", targetStatus, strings.TrimSpace(req.Reason))
	query := `
	UPDATE paj_orders
	SET status = $1,
	    last_webhook_status = $2,
	    updated_at = NOW()
	WHERE order_type = 'offramp'
	  AND status IN ('pending', 'processing')
	  AND deposit_id IS NULL
	  AND created_at < NOW() - ($3 || ' hours')::interval`
	args := []interface{}{targetStatus, webhookStatus, fmt.Sprintf("%d", req.MaxAgeHours)}
	if len(req.OrderIDs) > 0 {
		query += ` AND paj_order_id = ANY($4)`
		args = append(args, req.OrderIDs)
	}
	query += ` RETURNING paj_order_id`

	rows, err := h.db.QueryContext(c.Request.Context(), query, args...)
	if err != nil {
		h.logger.Error("complete stuck paj orders failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query failed"})
		return
	}
	defer rows.Close()
	var flipped []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			flipped = append(flipped, id)
		}
	}

	h.logger.Warn("manual paj order state flip (no ledger movement — verify provider truth out-of-band)",
		zap.Strings("paj_order_ids", flipped),
		zap.String("target_status", targetStatus),
		zap.String("reason", strings.TrimSpace(req.Reason)),
		zap.String("caller_ip", c.ClientIP()))
	c.JSON(http.StatusOK, gin.H{"updated": len(flipped), "status": targetStatus, "order_ids": flipped})
}
