package admin

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/pkg/auth"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

// AdminCredential is the stored login material of an admin or super_admin.
type AdminCredential struct {
	ID           uuid.UUID
	Email        string
	Role         string
	PasswordHash string
	IsActive     bool
}

// AdminCredentialStore finds an active-or-not admin by email. It returns
// sql.ErrNoRows when the email does not belong to an admin or super_admin.
type AdminCredentialStore interface {
	FindAdminByEmail(ctx context.Context, email string) (*AdminCredential, error)
}

// SecondFactorVerifier checks a TOTP (or unused backup) code for a user.
type SecondFactorVerifier interface {
	Verify(ctx context.Context, userID uuid.UUID, code string) (bool, error)
}

// AdminTokenMinter signs the admin access token.
type AdminTokenMinter interface {
	GenerateTokenPairEnhanced(userID uuid.UUID, email, role string) (*auth.EnhancedTokenPair, error)
}

// SQLAdminCredentialStore reads admin credentials from the users table.
type SQLAdminCredentialStore struct{ DB *sql.DB }

func (s SQLAdminCredentialStore) FindAdminByEmail(ctx context.Context, email string) (*AdminCredential, error) {
	if s.DB == nil {
		return nil, errors.New("database not configured")
	}
	var cred AdminCredential
	var hash sql.NullString
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, email, role, password_hash, is_active FROM users
		 WHERE LOWER(email) = $1 AND role IN ('admin', 'super_admin')`, email).
		Scan(&cred.ID, &cred.Email, &cred.Role, &hash, &cred.IsActive)
	if err != nil {
		return nil, err
	}
	cred.PasswordHash = hash.String
	return &cred, nil
}

// DashboardAuthHandler mints a dashboard admin token only for a caller who
// proves a password and a second factor. An email address alone never mints.
// Every missing or misconfigured piece (no password on the account, no 2FA
// enrolled, no 2FA service wired, no token minter) refuses.
type DashboardAuthHandler struct {
	store      AdminCredentialStore
	secondFact SecondFactorVerifier
	tokens     AdminTokenMinter
	logger     *zap.Logger
	dummyHash  []byte
}

func NewDashboardAuthHandler(store AdminCredentialStore, secondFactor SecondFactorVerifier, tokens AdminTokenMinter, logger *zap.Logger) *DashboardAuthHandler {
	if logger == nil {
		logger = zap.NewNop()
	}
	dummy, err := bcrypt.GenerateFromPassword([]byte("dashboard-auth-timing-equaliser"), 12)
	if err != nil {
		logger.Error("dashboard auth: could not precompute the timing-equaliser hash", zap.Error(err))
	}
	return &DashboardAuthHandler{store: store, secondFact: secondFactor, tokens: tokens, logger: logger, dummyHash: dummy}
}

type dashboardAuthRequest struct {
	Email    string `json:"email" binding:"required"`
	Password string `json:"password" binding:"required"`
	TOTPCode string `json:"totp_code" binding:"required"`
}

func (h *DashboardAuthHandler) Auth(c *gin.Context) {
	var req dashboardAuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email, password and totp_code are required"})
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))

	if h.store == nil || h.secondFact == nil || h.tokens == nil {
		h.refuse(c, email, "dashboard_auth_misconfigured")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "dashboard authentication unavailable"})
		return
	}

	cred, reason := h.verifyCredentials(c, email, req)
	if cred == nil {
		h.refuse(c, email, reason)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	pair, err := h.tokens.GenerateTokenPairEnhanced(cred.ID, cred.Email, cred.Role)
	if err != nil {
		h.logger.Error("dashboard admin token generation failed", append(h.audit(c), zap.String("actor_id", cred.ID.String()), zap.Error(err))...)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token generation failed"})
		return
	}
	h.logger.Warn("dashboard admin token minted", append(h.audit(c),
		zap.String("actor_id", cred.ID.String()),
		zap.String("actor_email", cred.Email),
		zap.String("actor_role", cred.Role),
		zap.Time("expires_at", pair.AccessExpiresAt))...)
	c.JSON(http.StatusOK, gin.H{"token": pair.AccessToken, "role": cred.Role, "email": cred.Email})
}

// verifyCredentials returns the admin only when the password and the second
// factor both check out; otherwise it returns nil and the audit reason.
func (h *DashboardAuthHandler) verifyCredentials(c *gin.Context, email string, req dashboardAuthRequest) (*AdminCredential, string) {
	ctx := c.Request.Context()
	cred, err := h.store.FindAdminByEmail(ctx, email)
	if err != nil || cred == nil || cred.PasswordHash == "" {
		if bcrypt.CompareHashAndPassword(h.dummyHash, []byte(req.Password)) == nil {
			return nil, "unknown_admin_dummy_hash_matched"
		}
		return nil, "unknown_admin_or_no_password"
	}
	if bcrypt.CompareHashAndPassword([]byte(cred.PasswordHash), []byte(req.Password)) != nil {
		return nil, "bad_password"
	}
	if !cred.IsActive || (cred.Role != "admin" && cred.Role != "super_admin") {
		return nil, "inactive_or_not_admin"
	}
	ok, err := h.secondFact.Verify(ctx, cred.ID, strings.TrimSpace(req.TOTPCode))
	if err != nil {
		return nil, "second_factor_unavailable_or_not_enrolled"
	}
	if !ok {
		return nil, "bad_second_factor"
	}
	return cred, ""
}

func (h *DashboardAuthHandler) refuse(c *gin.Context, email, reason string) {
	h.logger.Warn("dashboard admin token refused", append(h.audit(c),
		zap.String("attempted_email", email),
		zap.String("reason", reason))...)
}

func (h *DashboardAuthHandler) audit(c *gin.Context) []zap.Field {
	fields := []zap.Field{
		zap.String("event", "dashboard_auth"),
		zap.String("source_ip", c.ClientIP()),
		zap.String("request_id", c.GetString("request_id")),
	}
	if sc := trace.SpanContextFromContext(c.Request.Context()); sc.IsValid() {
		fields = append(fields, zap.String("trace_id", sc.TraceID().String()))
	} else {
		fields = append(fields, zap.String("trace_id", ""))
	}
	return fields
}
