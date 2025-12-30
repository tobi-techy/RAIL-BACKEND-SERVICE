package admin

import (
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/pkg/auth"
	"github.com/rail-service/rail_service/pkg/crypto"
	"github.com/rail-service/rail_service/pkg/logger"
	"go.uber.org/zap"
)

// AdminHandlers handles admin-related operations
type AdminHandlers struct {
	db     *sql.DB
	cfg    *config.Config
	logger *zap.Logger
}

// NewAdminHandlers creates a new AdminHandlers instance
func NewAdminHandlers(db *sql.DB, cfg *config.Config, logger *zap.Logger) *AdminHandlers {
	return &AdminHandlers{
		db:     db,
		cfg:    cfg,
		logger: logger,
	}
}

// CreateAdmin handles POST /api/v1/admin/create
func (h *AdminHandlers) CreateAdmin(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	// Security: Check if admin creation is disabled
	if h.cfg.Security.DisableAdminCreation {
		h.logger.Warn("admin creation attempt blocked - endpoint disabled",
			zap.String("client_ip", c.ClientIP()),
			zap.String("user_agent", c.GetHeader("User-Agent")),
		)
		c.JSON(http.StatusForbidden, entities.ErrorResponse{
			Code:    "ADMIN_CREATION_DISABLED",
			Message: "Admin creation endpoint is disabled",
		})
		return
	}

	var req entities.CreateAdminRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("invalid create admin payload", zap.Error(err))
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, common.MsgInvalidRequest)
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Email == "" {
		common.SendBadRequest(c, common.ErrCodeInvalidEmail, "Email is required")
		return
	}

	minPasswordLen := h.getMinPasswordLength()
	if len(req.Password) < minPasswordLen {
		common.SendBadRequest(c, common.ErrCodeWeakPassword,
			fmt.Sprintf("Password must be at least %d characters", minPasswordLen))
		return
	}

	adminCount, err := h.countAdmins(ctx)
	if err != nil {
		h.logger.Error("failed to count admins", zap.Error(err))
		common.SendInternalError(c, common.ErrCodeInternalError, "Failed to process request")
		return
	}

	desiredRole := entities.AdminRoleAdmin
	if req.Role != nil {
		if !req.Role.IsValid() {
			common.SendBadRequest(c, common.ErrCodeInvalidRole, "Role must be admin or super_admin")
			return
		}
		desiredRole = *req.Role
	}

	// First admin requires bootstrap token for security
	if adminCount == 0 {
		// Audit log: first admin creation attempt
		h.logger.Info("first admin creation attempt",
			zap.String("email", req.Email),
			zap.String("client_ip", c.ClientIP()),
			zap.String("user_agent", c.GetHeader("User-Agent")),
		)

		// Security: Require bootstrap token for first admin creation
		if err := h.validateBootstrapToken(c); err != nil {
			h.logger.Warn("first admin creation rejected - invalid bootstrap token",
				zap.String("email", req.Email),
				zap.String("client_ip", c.ClientIP()),
				zap.Error(err),
			)
			c.JSON(http.StatusForbidden, entities.ErrorResponse{
				Code:    "BOOTSTRAP_TOKEN_REQUIRED",
				Message: "Valid bootstrap token required for first admin creation",
			})
			return
		}

		desiredRole = entities.AdminRoleSuperAdmin
	} else {
		// Audit log: subsequent admin creation attempt
		h.logger.Info("admin creation attempt",
			zap.String("email", req.Email),
			zap.String("client_ip", c.ClientIP()),
			zap.String("requested_role", string(desiredRole)),
		)

		if err := h.ensureSuperAdmin(c); err != nil {
			h.logger.Warn("admin creation rejected - insufficient permissions",
				zap.String("email", req.Email),
				zap.String("client_ip", c.ClientIP()),
				zap.Error(err),
			)
			status := http.StatusForbidden
			if errors.Is(err, errUnauthorized) {
				status = http.StatusUnauthorized
			}
			c.JSON(status, entities.ErrorResponse{
				Code:    common.ErrCodeAdminRequired,
				Message: err.Error(),
			})
			return
		}
	}

	exists, err := h.emailExists(ctx, req.Email)
	if err != nil {
		h.logger.Error("failed to check email existence", zap.Error(err), zap.String("email", req.Email))
		common.SendInternalError(c, common.ErrCodeInternalError, "Failed to process request")
		return
	}

	if exists {
		common.SendConflict(c, common.ErrCodeUserExists, "User already exists with this email")
		return
	}

	passwordHash, err := crypto.HashPassword(req.Password)
	if err != nil {
		h.logger.Error("failed to hash password for admin", zap.Error(err))
		common.SendInternalError(c, common.ErrCodePasswordHashFailed, "Failed to process password")
		return
	}

	adminResp, err := h.insertAdmin(ctx, req, passwordHash, desiredRole)
	if err != nil {
		h.logger.Error("failed to create admin user", zap.Error(err))
		common.SendInternalError(c, common.ErrCodeCreateFailed, "Failed to create admin")
		return
	}

	tokenPair, err := auth.GenerateTokenPair(
		adminResp.ID,
		adminResp.Email,
		string(adminResp.Role),
		h.cfg.JWT.Secret,
		h.cfg.JWT.AccessTTL,
		h.cfg.JWT.RefreshTTL,
	)
	if err != nil {
		h.logger.Error("failed to generate admin session tokens", zap.Error(err), zap.String("admin_id", adminResp.ID.String()))
		common.SendInternalError(c, common.ErrCodeTokenGenFailed, "Failed to generate admin session tokens")
		return
	}

	// Audit log: successful admin creation
	h.logger.Info("admin created successfully",
		zap.String("admin_id", adminResp.ID.String()),
		zap.String("email", adminResp.Email),
		zap.String("role", string(adminResp.Role)),
		zap.String("client_ip", c.ClientIP()),
		zap.Bool("is_first_admin", adminCount == 0),
	)

	response := entities.AdminCreationResponse{
		AdminUserResponse: *adminResp,
		AdminSession: entities.AdminSession{
			AccessToken:  tokenPair.AccessToken,
			RefreshToken: tokenPair.RefreshToken,
			ExpiresAt:    tokenPair.ExpiresAt,
		},
	}

	common.SendCreated(c, response)
}

// GetAllUsers handles GET /api/v1/admin/users
func (h *AdminHandlers) GetAllUsers(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	limit, offset := h.parsePagination(c)

	conditions, args, err := h.parseUserFilters(c)
	if err != nil {
		return // Error already sent
	}

	query := h.buildUserListQuery(conditions, limit, offset)

	rows, err := h.db.QueryContext(ctx, query, args...)
	if err != nil {
		h.logger.Error("failed to list users", zap.Error(err))
		common.SendInternalError(c, common.ErrCodeInternalError, "Failed to retrieve users")
		return
	}
	defer rows.Close()

	users, err := h.scanUsers(rows)
	if err != nil {
		h.logger.Error("failed to scan users", zap.Error(err))
		common.SendInternalError(c, common.ErrCodeInternalError, "Failed to parse user record")
		return
	}

	common.SendSuccess(c, gin.H{
		"items": users,
		"count": len(users),
	})
}

// GetUserByID handles GET /api/v1/admin/users/:id
func (h *AdminHandlers) GetUserByID(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidID, "Invalid user ID")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := h.getUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			common.SendNotFound(c, common.ErrCodeUserNotFound, common.MsgUserNotFound)
			return
		}
		h.logger.Error("failed to get user by id", zap.Error(err), zap.String("user_id", userID.String()))
		common.SendInternalError(c, common.ErrCodeInternalError, "Failed to retrieve user")
		return
	}

	common.SendSuccess(c, resp)
}

// UpdateUserStatus handles PATCH /api/v1/admin/users/:id/status
func (h *AdminHandlers) UpdateUserStatus(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidID, "Invalid user ID")
		return
	}

	var req entities.UpdateUserStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, common.MsgInvalidRequest)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := h.updateUserStatus(ctx, userID, req.IsActive)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			common.SendNotFound(c, common.ErrCodeUserNotFound, common.MsgUserNotFound)
			return
		}
		h.logger.Error("failed to update user status", zap.Error(err), zap.String("user_id", userID.String()))
		common.SendInternalError(c, common.ErrCodeInternalError, "Failed to update user status")
		return
	}

	common.SendSuccess(c, resp)
}

// GetAllTransactions handles GET /api/v1/admin/transactions
func (h *AdminHandlers) GetAllTransactions(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	limit, offset := h.parsePagination(c)

	transactions, err := h.listTransactions(ctx, limit, offset)
	if err != nil {
		h.logger.Error("failed to list transactions", zap.Error(err))
		common.SendInternalError(c, common.ErrCodeInternalError, "Failed to retrieve transactions")
		return
	}

	common.SendSuccess(c, gin.H{
		"items": transactions,
		"count": len(transactions),
	})
}

// GetSystemAnalytics handles GET /api/v1/admin/analytics
func (h *AdminHandlers) GetSystemAnalytics(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	analytics, err := h.getSystemAnalytics(ctx)
	if err != nil {
		h.logger.Error("failed to load system analytics", zap.Error(err))
		common.SendInternalError(c, common.ErrCodeInternalError, "Failed to retrieve analytics")
		return
	}

	common.SendSuccess(c, analytics)
}

// CreateCuratedBasket handles POST /api/v1/admin/baskets
func (h *AdminHandlers) CreateCuratedBasket(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	var req entities.CuratedBasketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, common.MsgInvalidRequest)
		return
	}

	if err := validateBasketRequest(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeValidationError, err.Error())
		return
	}

	basket, err := h.createBasket(ctx, &req)
	if err != nil {
		h.logger.Error("failed to create basket", zap.Error(err))
		common.SendInternalError(c, common.ErrCodeCreateFailed, "Failed to create curated basket")
		return
	}

	common.SendCreated(c, basket)
}

// UpdateCuratedBasket handles PUT /api/v1/admin/baskets/:id
func (h *AdminHandlers) UpdateCuratedBasket(c *gin.Context) {
	basketID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidID, "Invalid basket ID")
		return
	}

	var req entities.CuratedBasketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, common.MsgInvalidRequest)
		return
	}

	if err := validateBasketRequest(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeValidationError, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	basket, err := h.updateBasket(ctx, basketID, &req)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			common.SendNotFound(c, common.ErrCodeNotFound, "Basket not found")
			return
		}
		h.logger.Error("failed to update basket", zap.Error(err), zap.String("basket_id", basketID.String()))
		common.SendInternalError(c, common.ErrCodeUpdateFailed, "Failed to update curated basket")
		return
	}

	common.SendSuccess(c, basket)
}

// Helper methods

func (h *AdminHandlers) getMinPasswordLength() int {
	if h.cfg.Security.PasswordMinLength > 8 {
		return h.cfg.Security.PasswordMinLength
	}
	return 8
}

func (h *AdminHandlers) countAdmins(ctx context.Context) (int64, error) {
	var count int64
	err := h.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE role IN ('admin','super_admin')`).Scan(&count)
	return count, err
}

func (h *AdminHandlers) emailExists(ctx context.Context, email string) (bool, error) {
	var exists bool
	err := h.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM users WHERE LOWER(email) = $1)`, email).Scan(&exists)
	return exists, err
}

// validateBootstrapToken validates the bootstrap token for first admin creation
// The token can be provided via X-Bootstrap-Token header or bootstrap_token in request body
func (h *AdminHandlers) validateBootstrapToken(c *gin.Context) error {
	configuredToken := h.cfg.Security.AdminBootstrapToken
	
	// If no bootstrap token is configured, reject all first admin creation attempts
	// This is a security measure to prevent unauthorized first admin creation
	if configuredToken == "" {
		return errors.New("bootstrap token not configured - first admin creation disabled")
	}

	// Check X-Bootstrap-Token header first
	providedToken := c.GetHeader("X-Bootstrap-Token")
	if providedToken == "" {
		// Fallback: check query parameter (less secure, but useful for CLI tools)
		providedToken = c.Query("bootstrap_token")
	}

	if providedToken == "" {
		return errors.New("bootstrap token required")
	}

	// Constant-time comparison to prevent timing attacks
	if !constantTimeCompare(providedToken, configuredToken) {
		return errors.New("invalid bootstrap token")
	}

	return nil
}

// constantTimeCompare performs a constant-time string comparison to prevent timing attacks
func constantTimeCompare(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var result byte
	for i := 0; i < len(a); i++ {
		result |= a[i] ^ b[i]
	}
	return result == 0
}

func (h *AdminHandlers) ensureSuperAdmin(c *gin.Context) error {
	if role := c.GetString("user_role"); role != "" {
		if role == string(entities.AdminRoleSuperAdmin) {
			return nil
		}
		return errors.New("super_admin role required")
	}

	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		return errUnauthorized
	}

	const bearer = "Bearer "
	if !strings.HasPrefix(authHeader, bearer) {
		return errUnauthorized
	}

	token := strings.TrimSpace(authHeader[len(bearer):])
	if token == "" {
		return errUnauthorized
	}

	claims, err := auth.ValidateToken(token, h.cfg.JWT.Secret)
	if err != nil {
		h.logger.Warn("failed to validate token for admin creation", zap.Error(err))
		return errUnauthorized
	}

	if claims.Role != string(entities.AdminRoleSuperAdmin) {
		return errors.New("super_admin role required")
	}

	return nil
}

func (h *AdminHandlers) insertAdmin(ctx context.Context, req entities.CreateAdminRequest, passwordHash string, role entities.AdminRole) (*entities.AdminUserResponse, error) {
	now := time.Now().UTC()
	adminID := uuid.New()

	query := `
		INSERT INTO users (
			id, email, password_hash, role, is_active, email_verified, phone_verified,
			onboarding_status, kyc_status, created_at, updated_at, first_name, last_name, phone
		) VALUES (
			$1, $2, $3, $4, true, true, false,
			$5, $6, $7, $8, $9, $10, $11
		)
		RETURNING id, email, role, is_active, onboarding_status, kyc_status, last_login_at, created_at, updated_at`

	onboardingStatus := entities.OnboardingStatusCompleted
	kycStatus := entities.KYCStatusApproved

	var adminResp entities.AdminUserResponse
	var lastLogin sql.NullTime

	err := h.db.QueryRowContext(ctx, query,
		adminID,
		req.Email,
		passwordHash,
		string(role),
		string(onboardingStatus),
		string(kycStatus),
		now,
		now,
		req.FirstName,
		req.LastName,
		req.Phone,
	).Scan(
		&adminResp.ID,
		&adminResp.Email,
		&adminResp.Role,
		&adminResp.IsActive,
		&adminResp.OnboardingStatus,
		&adminResp.KYCStatus,
		&lastLogin,
		&adminResp.CreatedAt,
		&adminResp.UpdatedAt,
	)

	if err != nil {
		return nil, err
	}

	if lastLogin.Valid {
		adminResp.LastLoginAt = &lastLogin.Time
	}

	return &adminResp, nil
}

func (h *AdminHandlers) parsePagination(c *gin.Context) (limit, offset int) {
	limit = 50
	if v := strings.TrimSpace(c.DefaultQuery("limit", "50")); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}

	offset = 0
	if v := strings.TrimSpace(c.Query("offset")); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	return limit, offset
}

func (h *AdminHandlers) parseUserFilters(c *gin.Context) ([]string, []interface{}, error) {
	var conditions []string
	var args []interface{}

	if roleParam := strings.TrimSpace(c.Query("role")); roleParam != "" {
		if roleParam != "user" && roleParam != "admin" && roleParam != "super_admin" {
			common.SendBadRequest(c, common.ErrCodeInvalidRole, "Role must be user, admin, or super_admin")
			return nil, nil, errors.New("invalid role")
		}
		args = append(args, roleParam)
		conditions = append(conditions, fmt.Sprintf("role = $%d", len(args)))
	}

	if isActive := strings.TrimSpace(c.Query("isActive")); isActive != "" {
		active, err := strconv.ParseBool(isActive)
		if err != nil {
			common.SendBadRequest(c, common.ErrCodeInvalidStatus, "isActive must be a boolean")
			return nil, nil, err
		}
		args = append(args, active)
		conditions = append(conditions, fmt.Sprintf("is_active = $%d", len(args)))
	}

	return conditions, args, nil
}

func (h *AdminHandlers) buildUserListQuery(conditions []string, limit, offset int) string {
	queryBuilder := strings.Builder{}
	queryBuilder.WriteString(`
		SELECT id, email, role, is_active, onboarding_status, kyc_status, last_login_at, created_at, updated_at
		FROM users`)

	if len(conditions) > 0 {
		queryBuilder.WriteString(" WHERE ")
		queryBuilder.WriteString(strings.Join(conditions, " AND "))
	}

	queryBuilder.WriteString(" ORDER BY created_at DESC")
	queryBuilder.WriteString(fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset))

	return queryBuilder.String()
}

func (h *AdminHandlers) scanUsers(rows *sql.Rows) ([]entities.AdminUserResponse, error) {
	var users []entities.AdminUserResponse
	for rows.Next() {
		var user entities.AdminUserResponse
		var lastLogin sql.NullTime
		if err := rows.Scan(
			&user.ID,
			&user.Email,
			&user.Role,
			&user.IsActive,
			&user.OnboardingStatus,
			&user.KYCStatus,
			&lastLogin,
			&user.CreatedAt,
			&user.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if lastLogin.Valid {
			user.LastLoginAt = &lastLogin.Time
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (h *AdminHandlers) getUserByID(ctx context.Context, userID uuid.UUID) (*entities.AdminUserResponse, error) {
	query := `
		SELECT id, email, role, is_active, onboarding_status, kyc_status, last_login_at, created_at, updated_at
		FROM users
		WHERE id = $1`

	var resp entities.AdminUserResponse
	var lastLogin sql.NullTime

	err := h.db.QueryRowContext(ctx, query, userID).Scan(
		&resp.ID,
		&resp.Email,
		&resp.Role,
		&resp.IsActive,
		&resp.OnboardingStatus,
		&resp.KYCStatus,
		&lastLogin,
		&resp.CreatedAt,
		&resp.UpdatedAt,
	)

	if err != nil {
		return nil, err
	}

	if lastLogin.Valid {
		resp.LastLoginAt = &lastLogin.Time
	}

	return &resp, nil
}

func (h *AdminHandlers) updateUserStatus(ctx context.Context, userID uuid.UUID, isActive bool) (*entities.AdminUserResponse, error) {
	query := `
		UPDATE users
		SET is_active = $1, updated_at = $2
		WHERE id = $3
		RETURNING id, email, role, is_active, onboarding_status, kyc_status, last_login_at, created_at, updated_at`

	var resp entities.AdminUserResponse
	var lastLogin sql.NullTime

	err := h.db.QueryRowContext(ctx, query, isActive, time.Now().UTC(), userID).Scan(
		&resp.ID,
		&resp.Email,
		&resp.Role,
		&resp.IsActive,
		&resp.OnboardingStatus,
		&resp.KYCStatus,
		&lastLogin,
		&resp.CreatedAt,
		&resp.UpdatedAt,
	)

	if err != nil {
		return nil, err
	}

	if lastLogin.Valid {
		resp.LastLoginAt = &lastLogin.Time
	}

	return &resp, nil
}

func (h *AdminHandlers) listTransactions(ctx context.Context, limit, offset int) ([]entities.AdminTransaction, error) {
	query := `
		SELECT id, user_id, chain, tx_hash, token, amount, status, created_at
		FROM deposits
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2`

	rows, err := h.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var transactions []entities.AdminTransaction
	for rows.Next() {
		var tx entities.AdminTransaction
		var amount decimal.Decimal
		var chain, txHash, token string

		if err := rows.Scan(
			&tx.ID,
			&tx.UserID,
			&chain,
			&txHash,
			&token,
			&amount,
			&tx.Status,
			&tx.CreatedAt,
		); err != nil {
			return nil, err
		}

		tx.Type = "deposit"
		tx.Amount = amount.String()
		tx.Metadata = map[string]interface{}{
			"chain":  chain,
			"txHash": txHash,
			"token":  token,
		}
		transactions = append(transactions, tx)
	}

	return transactions, rows.Err()
}

func (h *AdminHandlers) getSystemAnalytics(ctx context.Context) (*entities.SystemAnalytics, error) {
	query := `
		SELECT
			(SELECT COUNT(*) FROM users) AS total_users,
			(SELECT COUNT(*) FROM users WHERE is_active = true) AS active_users,
			(SELECT COUNT(*) FROM users WHERE role IN ('admin','super_admin')) AS total_admins,
			COALESCE((SELECT SUM(amount) FROM deposits WHERE status = 'confirmed'), 0) AS total_deposits,
			(SELECT COUNT(*) FROM deposits WHERE status = 'pending') AS pending_deposits,
			COALESCE((SELECT COUNT(*) FROM wallets), 0) AS total_wallets`

	var analytics entities.SystemAnalytics
	var totalDeposits decimal.Decimal

	err := h.db.QueryRowContext(ctx, query).Scan(
		&analytics.TotalUsers,
		&analytics.ActiveUsers,
		&analytics.TotalAdmins,
		&totalDeposits,
		&analytics.PendingDeposits,
		&analytics.TotalWallets,
	)
	if err != nil {
		return nil, err
	}

	analytics.TotalDeposits = totalDeposits.String()
	analytics.GeneratedAt = time.Now().UTC()

	return &analytics, nil
}

func (h *AdminHandlers) createBasket(ctx context.Context, req *entities.CuratedBasketRequest) (*entities.Basket, error) {
	payload, err := json.Marshal(req.Composition)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal composition: %w", err)
	}

	now := time.Now().UTC()
	basketID := uuid.New()

	query := `
		INSERT INTO baskets (id, name, description, risk_level, composition_json, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, name, description, risk_level, composition_json, created_at, updated_at`

	var basket entities.Basket
	var compositionRaw []byte

	err = h.db.QueryRowContext(ctx, query,
		basketID,
		req.Name,
		req.Description,
		req.RiskLevel,
		payload,
		now,
		now,
	).Scan(
		&basket.ID,
		&basket.Name,
		&basket.Description,
		&basket.RiskLevel,
		&compositionRaw,
		&basket.CreatedAt,
		&basket.UpdatedAt,
	)

	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(compositionRaw, &basket.Composition); err != nil {
		return nil, fmt.Errorf("failed to unmarshal composition: %w", err)
	}

	return &basket, nil
}

func (h *AdminHandlers) updateBasket(ctx context.Context, basketID uuid.UUID, req *entities.CuratedBasketRequest) (*entities.Basket, error) {
	payload, err := json.Marshal(req.Composition)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal composition: %w", err)
	}

	query := `
		UPDATE baskets
		SET name = $1,
		    description = $2,
		    risk_level = $3,
		    composition_json = $4,
		    updated_at = $5
		WHERE id = $6
		RETURNING id, name, description, risk_level, composition_json, created_at, updated_at`

	var basket entities.Basket
	var compositionRaw []byte

	err = h.db.QueryRowContext(ctx, query,
		req.Name,
		req.Description,
		req.RiskLevel,
		payload,
		time.Now().UTC(),
		basketID,
	).Scan(
		&basket.ID,
		&basket.Name,
		&basket.Description,
		&basket.RiskLevel,
		&compositionRaw,
		&basket.CreatedAt,
		&basket.UpdatedAt,
	)

	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(compositionRaw, &basket.Composition); err != nil {
		return nil, fmt.Errorf("failed to unmarshal composition: %w", err)
	}

	return &basket, nil
}

// validateBasketRequest validates basket creation/update requests
func validateBasketRequest(req *entities.CuratedBasketRequest) error {
	if len(req.Composition) == 0 {
		return errors.New("composition must contain at least one component")
	}

	if req.RiskLevel != entities.RiskLevelConservative &&
		req.RiskLevel != entities.RiskLevelBalanced &&
		req.RiskLevel != entities.RiskLevelGrowth {
		return fmt.Errorf("invalid riskLevel: %s", req.RiskLevel)
	}

	total := decimal.Zero
	for idx, component := range req.Composition {
		if strings.TrimSpace(component.Symbol) == "" {
			return fmt.Errorf("composition[%d].symbol is required", idx)
		}
		if component.Weight.LessThanOrEqual(decimal.Zero) {
			return fmt.Errorf("composition[%d].weight must be greater than zero", idx)
		}
		total = total.Add(component.Weight)
	}

	diff := total.Sub(decimal.NewFromInt(1)).Abs()
	if diff.GreaterThan(decimal.NewFromFloat(0.0001)) {
		return errors.New("composition weights must sum to 1.0")
	}

	return nil
}

// AdminWalletHandlers handles admin wallet operations
type AdminWalletHandlers struct {
	db  *sql.DB
	cfg *config.Config
	log *logger.Logger
}

// NewAdminWalletHandlers creates a new AdminWalletHandlers
func NewAdminWalletHandlers(db *sql.DB, cfg *config.Config, log *logger.Logger) *AdminWalletHandlers {
	return &AdminWalletHandlers{db: db, cfg: cfg, log: log}
}

// CreateWalletSet handles POST /api/v1/admin/wallet-sets
func (h *AdminWalletHandlers) CreateWalletSet(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	var req entities.CreateWalletSetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Warnw("invalid create wallet set payload", "error", err)
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, common.MsgInvalidRequest)
		return
	}

	if req.Name == "" {
		common.SendBadRequest(c, "MISSING_NAME", "Wallet set name is required")
		return
	}

	walletSet, err := h.createWalletSet(ctx, &req)
	if err != nil {
		h.log.Errorw("failed to create wallet set", "error", err)
		common.SendInternalError(c, common.ErrCodeCreateFailed, "Failed to create wallet set")
		return
	}

	common.SendCreated(c, walletSet)
}

// GetWalletSets handles GET /api/v1/admin/wallet-sets
func (h *AdminWalletHandlers) GetWalletSets(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	limit, offset := parsePaginationParams(c)

	walletSets, err := h.listWalletSets(ctx, limit, offset)
	if err != nil {
		h.log.Errorw("failed to list wallet sets", "error", err)
		common.SendInternalError(c, common.ErrCodeInternalError, "Failed to retrieve wallet sets")
		return
	}

	common.SendSuccess(c, gin.H{
		"items": walletSets,
		"count": len(walletSets),
	})
}

// GetWalletSetByID handles GET /api/v1/admin/wallet-sets/:id
func (h *AdminWalletHandlers) GetWalletSetByID(c *gin.Context) {
	walletSetID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidID, "Invalid wallet set ID")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	walletSet, err := h.getWalletSetByID(ctx, walletSetID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			common.SendNotFound(c, common.ErrCodeNotFound, "Wallet set not found")
			return
		}
		h.log.Errorw("failed to get wallet set by id", "error", err, "wallet_set_id", walletSetID)
		common.SendInternalError(c, common.ErrCodeInternalError, "Failed to retrieve wallet set")
		return
	}

	common.SendSuccess(c, walletSet)
}

// GetAdminWallets handles GET /api/v1/admin/wallets
func (h *AdminWalletHandlers) GetAdminWallets(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	limit, offset := parsePaginationParams(c)

	filters, err := h.parseWalletFilters(c)
	if err != nil {
		return // Error already sent
	}

	wallets, err := h.listWallets(ctx, filters, limit, offset)
	if err != nil {
		h.log.Errorw("failed to list wallets", "error", err)
		common.SendInternalError(c, common.ErrCodeInternalError, "Failed to retrieve wallets")
		return
	}

	common.SendSuccess(c, gin.H{
		"items": wallets,
		"count": len(wallets),
	})
}

// Helper methods

func parsePaginationParams(c *gin.Context) (limit, offset int) {
	limit = 50
	if v := strings.TrimSpace(c.DefaultQuery("limit", "50")); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}

	offset = 0
	if v := strings.TrimSpace(c.Query("offset")); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	return limit, offset
}

func (h *AdminWalletHandlers) createWalletSet(ctx context.Context, req *entities.CreateWalletSetRequest) (*entities.WalletSet, error) {
	walletSetID := uuid.New()
	now := time.Now().UTC()

	query := `
		INSERT INTO wallet_sets (
			id, name, circle_wallet_set_id, status, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6
		)
		RETURNING id, name, circle_wallet_set_id, status, created_at, updated_at`

	var walletSet entities.WalletSet
	err := h.db.QueryRowContext(ctx, query,
		walletSetID,
		req.Name,
		req.CircleWalletSetID,
		string(entities.WalletSetStatusActive),
		now,
		now,
	).Scan(
		&walletSet.ID,
		&walletSet.Name,
		&walletSet.CircleWalletSetID,
		&walletSet.Status,
		&walletSet.CreatedAt,
		&walletSet.UpdatedAt,
	)

	if err != nil {
		return nil, err
	}

	return &walletSet, nil
}

func (h *AdminWalletHandlers) listWalletSets(ctx context.Context, limit, offset int) ([]entities.WalletSet, error) {
	query := `
		SELECT id, name, circle_wallet_set_id, status, created_at, updated_at
		FROM wallet_sets
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2`

	rows, err := h.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var walletSets []entities.WalletSet
	for rows.Next() {
		var walletSet entities.WalletSet
		if err := rows.Scan(
			&walletSet.ID,
			&walletSet.Name,
			&walletSet.CircleWalletSetID,
			&walletSet.Status,
			&walletSet.CreatedAt,
			&walletSet.UpdatedAt,
		); err != nil {
			return nil, err
		}
		walletSets = append(walletSets, walletSet)
	}

	return walletSets, rows.Err()
}

func (h *AdminWalletHandlers) getWalletSetByID(ctx context.Context, walletSetID uuid.UUID) (*entities.WalletSet, error) {
	query := `
		SELECT id, name, circle_wallet_set_id, status, created_at, updated_at
		FROM wallet_sets
		WHERE id = $1`

	var walletSet entities.WalletSet
	err := h.db.QueryRowContext(ctx, query, walletSetID).Scan(
		&walletSet.ID,
		&walletSet.Name,
		&walletSet.CircleWalletSetID,
		&walletSet.Status,
		&walletSet.CreatedAt,
		&walletSet.UpdatedAt,
	)

	if err != nil {
		return nil, err
	}

	return &walletSet, nil
}

type walletFilters struct {
	userID      *uuid.UUID
	chain       *string
	accountType *string
	status      *string
}

func (h *AdminWalletHandlers) parseWalletFilters(c *gin.Context) (*walletFilters, error) {
	filters := &walletFilters{}

	if userIDParam := strings.TrimSpace(c.Query("user_id")); userIDParam != "" {
		userID, err := uuid.Parse(userIDParam)
		if err != nil {
			common.SendBadRequest(c, common.ErrCodeInvalidUserID, "Invalid user ID format")
			return nil, err
		}
		filters.userID = &userID
	}

	if chainParam := strings.TrimSpace(c.Query("chain")); chainParam != "" {
		filters.chain = &chainParam
	}

	if accountTypeParam := strings.TrimSpace(c.Query("account_type")); accountTypeParam != "" {
		if accountTypeParam != "EOA" && accountTypeParam != "SCA" {
			common.SendBadRequest(c, "INVALID_ACCOUNT_TYPE", "Account type must be EOA or SCA")
			return nil, errors.New("invalid account type")
		}
		filters.accountType = &accountTypeParam
	}

	if statusParam := strings.TrimSpace(c.Query("status")); statusParam != "" {
		if statusParam != "creating" && statusParam != "live" && statusParam != "failed" {
			common.SendBadRequest(c, common.ErrCodeInvalidStatus, "Status must be creating, live, or failed")
			return nil, errors.New("invalid status")
		}
		filters.status = &statusParam
	}

	return filters, nil
}

func (h *AdminWalletHandlers) listWallets(ctx context.Context, filters *walletFilters, limit, offset int) ([]entities.ManagedWallet, error) {
	var conditions []string
	var args []interface{}
	argIndex := 1

	if filters.userID != nil {
		args = append(args, *filters.userID)
		conditions = append(conditions, fmt.Sprintf("user_id = $%d", argIndex))
		argIndex++
	}

	if filters.chain != nil {
		args = append(args, *filters.chain)
		conditions = append(conditions, fmt.Sprintf("chain = $%d", argIndex))
		argIndex++
	}

	if filters.accountType != nil {
		args = append(args, *filters.accountType)
		conditions = append(conditions, fmt.Sprintf("account_type = $%d", argIndex))
		argIndex++
	}

	if filters.status != nil {
		args = append(args, *filters.status)
		conditions = append(conditions, fmt.Sprintf("status = $%d", argIndex))
		argIndex++
	}

	queryBuilder := strings.Builder{}
	queryBuilder.WriteString(`
		SELECT id, user_id, wallet_set_id, circle_wallet_id, chain, address, account_type, status, created_at, updated_at
		FROM managed_wallets`)

	if len(conditions) > 0 {
		queryBuilder.WriteString(" WHERE ")
		queryBuilder.WriteString(strings.Join(conditions, " AND "))
	}

	queryBuilder.WriteString(" ORDER BY created_at DESC")
	queryBuilder.WriteString(fmt.Sprintf(" LIMIT $%d OFFSET $%d", argIndex, argIndex+1))

	args = append(args, limit, offset)

	rows, err := h.db.QueryContext(ctx, queryBuilder.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var wallets []entities.ManagedWallet
	for rows.Next() {
		var wallet entities.ManagedWallet
		if err := rows.Scan(
			&wallet.ID,
			&wallet.UserID,
			&wallet.WalletSetID,
			&wallet.CircleWalletID,
			&wallet.Chain,
			&wallet.Address,
			&wallet.AccountType,
			&wallet.Status,
			&wallet.CreatedAt,
			&wallet.UpdatedAt,
		); err != nil {
			return nil, err
		}
		wallets = append(wallets, wallet)
	}

	return wallets, rows.Err()
}
